// Package runtime – NIP-17 gift-wrapped DM bus.
//
// NIP17Bus sends and receives private DMs using the NIP-17 protocol:
//   - Outbound:  kind 14 rumor → kind 13 seal → kind 1059 gift wrap (per NIP-59)
//   - Inbound:   subscribe to kind 1059 events tagged with our pubkey, unwrap each one
//   - Encryption: NIP-44 (via fiatjaf.com/nostr/keyer.KeySigner)
//   - Relay lookup: queries recipient's DM relay list (kind 10050) before falling back
//     to the configured write relays
//
// The public surface intentionally matches DMBus so callers can swap them.
package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip17"
)

const (
	// NIP-59 gift wraps are intentionally backdated. Our current producer path
	// may skew CreatedAt by up to 599 minutes, so subscribe far enough back that
	// valid inbound 1059 events are still seen after unwrap.
	nip17GiftWrapBackfill = 10*time.Hour + 5*time.Minute
)

// NIP17BusOptions mirrors DMBusOptions so the two buses are interchangeable.
type NIP17BusOptions struct {
	Relays      []string
	SinceUnix   int64
	OnMessage   func(context.Context, InboundDM) error
	OnError     func(error)
	SeenCap     int
	WorkerCount int
	QueueSize   int
	// Keyer is the required signing/decryption interface.
	Keyer nostr.Keyer
}

// NIP17Bus is the NIP-17 equivalent of DMBus.
type NIP17Bus struct {
	pool     *nostr.Pool
	kr       nostr.Keyer
	public   nostr.PubKey
	relaysMu sync.RWMutex
	relays   []string

	onMessage func(context.Context, InboundDM) error
	onError   func(error)

	seenMu   sync.Mutex
	seenSet  map[string]struct{}
	seenList []string
	seenCap  int

	messageQueue chan InboundDM

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// StartNIP17Bus creates and starts a NIP17Bus.  It mirrors StartDMBus.
func StartNIP17Bus(parent context.Context, opts NIP17BusOptions) (*NIP17Bus, error) {
	initialRelays := sanitizeRelayList(opts.Relays)
	if len(initialRelays) == 0 {
		return nil, fmt.Errorf("at least one relay is required")
	}

	ks := opts.Keyer
	if ks == nil {
		return nil, fmt.Errorf("keyer is required")
	}

	since := normalizeNIP17Since(opts.SinceUnix)
	workerCount := max(opts.WorkerCount, 4)
	queueSize := max(opts.QueueSize, 256)

	// Resolve pubkey from the keyer before starting goroutines.
	pkCtx, pkCancel := context.WithTimeout(parent, 10*time.Second)
	pub, err := ks.GetPublicKey(pkCtx)
	pkCancel()
	if err != nil {
		return nil, fmt.Errorf("resolve public key: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	b := &NIP17Bus{
		pool: NewPoolNIP42(ks),
		kr:           ks,
		public:       pub,
		relays:       initialRelays,
		onMessage:    opts.OnMessage,
		onError:      opts.OnError,
		seenSet:      make(map[string]struct{}),
		seenCap:      max(opts.SeenCap, 10_000),
		messageQueue: make(chan InboundDM, queueSize),
		ctx:          ctx,
		cancel:       cancel,
	}

	if b.onMessage != nil {
		for i := 0; i < workerCount; i++ {
			b.wg.Add(1)
			go func() {
				defer b.wg.Done()
				for msg := range b.messageQueue {
					if err := b.onMessage(b.ctx, msg); err != nil {
						b.emitErr(fmt.Errorf("on message handler: %w", err))
					}
				}
			}()
		}
	}

	b.wg.Add(1)
	go b.receiveLoop(nostr.Timestamp(since))

	return b, nil
}

// PublicKey returns the agent's pubkey hex.
func (b *NIP17Bus) PublicKey() string { return b.public.Hex() }

// Close shuts down the bus and waits for goroutines to exit.
func (b *NIP17Bus) Close() {
	b.cancel()
	b.pool.Close("nip17 bus closed")
	b.wg.Wait()
}

// SendDM sends a NIP-17 gift-wrapped DM to toPubKey.
// It first attempts to discover the recipient's DM relay list (kind 10050);
// if not found it falls back to the configured write relays.
func (b *NIP17Bus) SendDM(ctx context.Context, toPubKey string, text string) error {
	pk, err := ParsePubKey(toPubKey)
	if err != nil {
		return err
	}
	var textErr error
	text, textErr = sanitizeDMText(text)
	if textErr != nil {
		return textErr
	}

	theirRelays := b.lookupDMRelays(ctx, pk)
	ourRelays := b.currentRelays()

	return nip17.PublishMessage(
		ctx,
		text,
		nostr.Tags{},
		b.pool,
		ourRelays,
		theirRelays,
		b.kr,
		pk,
		nil, // no event modifier
	)
}

// SendDMWithScheme sends a DM using an explicit encryption scheme request.
// NIP17Bus supports nip17/nip44/giftwrap; auto/empty resolves to default NIP-17 flow.
func (b *NIP17Bus) SendDMWithScheme(ctx context.Context, toPubKey string, text string, scheme string) error {
	s := strings.ToLower(strings.TrimSpace(scheme))
	switch s {
	case "", "auto", "nip17", "nip-17", "nip44", "nip-44", "giftwrap", "nip59", "nip-59":
		return b.SendDM(ctx, toPubKey, text)
	case "nip04", "nip-04":
		return fmt.Errorf("dm scheme %q not supported by NIP-17 transport", scheme)
	default:
		return fmt.Errorf("unsupported dm scheme %q", scheme)
	}
}

// SetRelays updates the relay list at runtime.
func (b *NIP17Bus) SetRelays(relays []string) error {
	next := sanitizeRelayList(relays)
	if len(next) == 0 {
		return fmt.Errorf("at least one relay is required")
	}
	b.relaysMu.Lock()
	b.relays = next
	b.relaysMu.Unlock()
	return nil
}

// Relays returns the current relay list.
func (b *NIP17Bus) Relays() []string { return b.currentRelays() }

// ──────────────────────────────────────────────────────────────────────────────
// internal
// ──────────────────────────────────────────────────────────────────────────────

func normalizeNIP17Since(sinceUnix int64) int64 {
	floor := time.Now().Add(-nip17GiftWrapBackfill).Unix()
	if sinceUnix <= 0 {
		return floor
	}
	adjusted := sinceUnix - int64(nip17GiftWrapBackfill.Seconds())
	if adjusted < floor {
		adjusted = floor
	}
	if adjusted < 0 {
		return 0
	}
	return adjusted
}

func (b *NIP17Bus) receiveLoop(since nostr.Timestamp) {
	defer b.wg.Done()
	defer close(b.messageQueue)

	rumCh := nip17.ListenForMessages(b.ctx, b.pool, b.kr, b.currentRelays(), since)
	for rumor := range rumCh {
		b.handleRumor(rumor)
	}
}

func (b *NIP17Bus) handleRumor(rumor nostr.Event) {
	// Only process kind 14 (NIP-17 DM rumor).
	if rumor.Kind != nostr.KindDirectMessage {
		return
	}
	// Skip self-sent (sent-to-self copy we stored).
	if rumor.PubKey == b.public {
		return
	}

	eventID := rumor.ID.Hex()
	if b.markSeen17(eventID) {
		return
	}

	text := rumor.Content
	var err error
	text, err = sanitizeDMText(text)
	if err != nil {
		b.emitErr(fmt.Errorf("reject nip17 rumor %s: %w", eventID, err))
		return
	}
	if b.onMessage == nil {
		return
	}

	senderPubkey := rumor.PubKey
	msg := InboundDM{
		EventID:    eventID,
		FromPubKey: senderPubkey.Hex(),
		Text:       text,
		RelayURL:   "",   // gift wraps hide relay; not available here
		CreatedAt:  int64(rumor.CreatedAt),
		Reply: func(ctx context.Context, reply string) error {
			return b.SendDM(ctx, senderPubkey.Hex(), reply)
		},
	}

	select {
	case b.messageQueue <- msg:
	case <-b.ctx.Done():
	case <-time.After(2 * time.Second):
		b.emitErr(fmt.Errorf("dropped nip17 event=%s due to full queue", eventID))
	}
}

// lookupDMRelays queries the recipient's DM relay list (kind 10050).
// Falls back to our own relays if not found.
func (b *NIP17Bus) lookupDMRelays(ctx context.Context, pk nostr.PubKey) []string {
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	relays := nip17.GetDMRelays(lookupCtx, pk, b.pool, b.currentRelays())
	if len(relays) == 0 {
		return b.currentRelays()
	}
	return relays
}

func (b *NIP17Bus) currentRelays() []string {
	b.relaysMu.RLock()
	defer b.relaysMu.RUnlock()
	out := make([]string, len(b.relays))
	copy(out, b.relays)
	return out
}

func (b *NIP17Bus) emitErr(err error) {
	if b.onError != nil && err != nil {
		b.onError(err)
	}
}

func (b *NIP17Bus) markSeen17(id string) bool {
	b.seenMu.Lock()
	defer b.seenMu.Unlock()
	if _, ok := b.seenSet[id]; ok {
		return true
	}
	b.seenSet[id] = struct{}{}
	b.seenList = append(b.seenList, id)
	if len(b.seenList) > b.seenCap {
		victim := b.seenList[0]
		b.seenList = b.seenList[1:]
		delete(b.seenSet, victim)
	}
	return false
}

// sanitizeNIP17Text validates text (re-uses the same rules as NIP-04).
func sanitizeNIP17Text(text string) (string, error) {
	if utf8.RuneCountInString(text) > maxDMPlaintextRunes {
		return "", fmt.Errorf("nip17 text exceeds %d characters", maxDMPlaintextRunes)
	}
	return text, nil
}
