// Package runtime – NIP-17 gift-wrapped DM bus.
//
// NIP17Bus sends and receives private DMs using the NIP-17 protocol:
//   - Outbound:  rumor event → seal event → gift-wrap event (per NIP-17/NIP-59)
//   - Inbound:   subscribe to gift-wrap events tagged with our pubkey, unwrap each one
//   - Encryption: NIP-44 (via fiatjaf.com/nostr/keyer.KeySigner)
//   - Relay lookup: queries recipient's DM relay list (kind 10050) before falling back
//     to the configured write relays
//
// # Per-relay subscription management
//
// The nostr pool library's subMany function sets filter.Since = Now() after
// any relay disconnection (pool.go line ~548). NIP-59 gift wraps are
// intentionally backdated by up to ~10 hours, so this silently drops all
// inbound DMs after a relay reconnect. To avoid this, the NIP-17 bus manages
// per-relay subscriptions directly (see listenGiftWraps), bypassing
// pool.SubscribeMany and controlling the Since value on each reconnect.
//
// The public surface intentionally matches DMBus so callers can swap them.
package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip17"
	"fiatjaf.com/nostr/nip59"
)

const (
	// NIP-59 gift wraps are intentionally backdated. The reference implementation
	// skews CreatedAt by up to 599 minutes (~10h), but real-world clients (e.g.
	// Amethyst, Primal) may backdate by 24-48 hours or more. The NIP-59 spec
	// imposes no upper bound on backdating. We use 49 hours to safely cover
	// aggressive clients while keeping the subscription window practical.
	nip17GiftWrapBackfill = 49 * time.Hour

	// nip17ReconnectBackoffMin is the initial backoff for per-relay reconnection.
	nip17ReconnectBackoffMin = 3 * time.Second

	// nip17ReconnectBackoffMax caps the exponential backoff for relay reconnection.
	nip17ReconnectBackoffMax = 10 * time.Minute
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
	// Hub, when non-nil, shares the hub's pool instead of creating a new one.
	// This avoids duplicate WebSocket connections and NIP-42 auth flows.
	Hub *NostrHub
}

// NIP17Bus is the NIP-17 equivalent of DMBus.
type NIP17Bus struct {
	pool     *nostr.Pool
	ownsPool bool
	kr       nostr.Keyer
	public   nostr.PubKey
	relaysMu sync.RWMutex
	relays   []string

	onMessage func(context.Context, InboundDM) error
	onError   func(error)
	subHealth *SubHealthTracker

	seenMu   sync.Mutex
	seenSet  map[string]struct{}
	seenList []string
	seenCap  int

	messageQueue chan InboundDM
	rebindCh     chan struct{}

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
	pool := NewPoolNIP42(ks)
	ownsPool := true
	if opts.Hub != nil {
		pool = opts.Hub.Pool()
		ownsPool = false
	}
	b := &NIP17Bus{
		pool:         pool,
		ownsPool:     ownsPool,
		kr:           ks,
		public:       pub,
		relays:       initialRelays,
		onMessage:    opts.OnMessage,
		onError:      opts.OnError,
		seenSet:      make(map[string]struct{}),
		seenCap:      max(opts.SeenCap, 10_000),
		messageQueue: make(chan InboundDM, queueSize),
		rebindCh:     make(chan struct{}, 1),
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

	b.subHealth = NewSubHealthTracker("nip17")
	b.subHealth.RecordReconnect()
	b.wg.Add(1)
	go b.receiveLoop(nostr.Timestamp(since))

	return b, nil
}

// PublicKey returns the agent's pubkey hex.
func (b *NIP17Bus) PublicKey() string { return b.public.Hex() }

// Close shuts down the bus and waits for goroutines to exit.
func (b *NIP17Bus) Close() {
	b.cancel()
	if b.ownsPool {
		b.pool.Close("nip17 bus closed")
	}
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
	b.relaysMu.Lock()
	b.relays = next
	b.relaysMu.Unlock()
	select {
	case b.rebindCh <- struct{}{}:
	default:
	}
	return nil
}

// Relays returns the current relay list.
// HealthSnapshot returns a point-in-time view of the NIP-17 subscription's health.
func (b *NIP17Bus) HealthSnapshot() SubHealthSnapshot {
	if b.subHealth == nil {
		return SubHealthSnapshot{Label: "nip17", BoundRelays: b.currentRelays(), ReplayWindowMS: int64(NIP17GiftWrapBackfill / time.Millisecond)}
	}
	return b.subHealth.Snapshot(b.currentRelays(), NIP17GiftWrapBackfill)
}

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

	currentSince := since
	for {
		if b.ctx.Err() != nil {
			return
		}
		if len(b.currentRelays()) == 0 {
			select {
			case <-b.ctx.Done():
				return
			case <-b.rebindCh:
				continue
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}

		cycleCtx, cycleCancel := context.WithCancel(b.ctx)
		rumCh := b.listenGiftWraps(cycleCtx, b.currentRelays(), currentSince)

		closed := false
		for !closed {
			select {
			case <-b.ctx.Done():
				cycleCancel()
				return
			case <-b.rebindCh:
				cycleCancel()
				closed = true
			case rumor, ok := <-rumCh:
				if !ok {
					cycleCancel()
					b.emitErr(fmt.Errorf("nip17 subscription closed; restarting"))
					closed = true
					continue
				}
				b.handleRumor(rumor)
			}
		}
		cycleCancel()
		if b.subHealth != nil {
			b.subHealth.RecordReconnect()
		}
		currentSince = nostr.Timestamp(normalizeNIP17Since(time.Now().Unix()))
		select {
		case <-b.ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		default:
		}
	}
}

// listenGiftWraps subscribes to kind:1059 events on each relay independently,
// unwraps each gift wrap, and sends the resulting rumors to the returned channel.
//
// Unlike nip17.ListenForMessages (which delegates to pool.SubscribeMany),
// this manages per-relay subscriptions directly.  Each relay goroutine
// handles its own reconnection and always resubscribes with the correct
// NIP-59 backfill Since, avoiding the pool library's filter.Since = Now()
// bug that silently drops backdated gift wraps after any relay disconnect.
func (b *NIP17Bus) listenGiftWraps(ctx context.Context, relays []string, since nostr.Timestamp) <-chan nostr.Event {
	ch := make(chan nostr.Event)
	var wg sync.WaitGroup

	// Deduplicate relay URLs.
	seen := make(map[string]struct{}, len(relays))
	var unique []string
	for _, u := range relays {
		nm := nostr.NormalizeURL(u)
		if _, ok := seen[nm]; ok {
			continue
		}
		seen[nm] = struct{}{}
		unique = append(unique, nm)
	}

	log.Printf("nip17: subscribing to %d relays (since=%d, backfill=%s)",
		len(unique), since, nip17GiftWrapBackfill)

	for _, url := range unique {
		wg.Add(1)
		go b.perRelaySubscribe(ctx, url, since, ch, &wg)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	return ch
}

// perRelaySubscribe manages a single relay's kind:1059 subscription lifecycle.
// On disconnect or CLOSED, it reconnects with a fresh Since computed from
// normalizeNIP17Since (≈ now − 10h5m) so backdated gift wraps are never missed.
func (b *NIP17Bus) perRelaySubscribe(
	ctx context.Context,
	relayURL string,
	initialSince nostr.Timestamp,
	out chan<- nostr.Event,
	wg *sync.WaitGroup,
) {
	defer wg.Done()
	backoff := nip17ReconnectBackoffMin
	since := initialSince

	for {
		if ctx.Err() != nil {
			return
		}

		relay, err := b.pool.EnsureRelay(relayURL)
		if err != nil {
			log.Printf("nip17: connect %s failed: %v (retry in %s)", relayURL, err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(nip17ReconnectBackoffMax, backoff*17/10)
			continue
		}

		filter := nostr.Filter{
			Kinds: []nostr.Kind{nostr.KindGiftWrap},
			Tags:  nostr.TagMap{"p": []string{b.public.Hex()}},
			Since: since,
		}

		hasAuthed := false

	subscribe:
		sub, subErr := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{Label: "nip17dm"})
		if subErr != nil {
			log.Printf("nip17: subscribe %s failed: %v (retry in %s)", relayURL, subErr, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(nip17ReconnectBackoffMax, backoff*17/10)
			continue
		}

		// Successful subscription — reset backoff.
		backoff = nip17ReconnectBackoffMin

		for {
			select {
			case <-ctx.Done():
				return

			case evt, more := <-sub.Events:
				if !more {
					// Connection lost — reconnect with correct backfill.
					log.Printf("nip17: subscription to %s closed, reconnecting", relayURL)
					goto reconnect
				}

				// Unwrap the gift wrap inline.
				rumor, unwrapErr := nip59.GiftUnwrap(evt, func(pk nostr.PubKey, ct string) (string, error) {
					return b.kr.Decrypt(ctx, ct, pk)
				})
				if unwrapErr != nil {
					log.Printf("nip17: unwrap from %s: %v", relayURL, unwrapErr)
					continue
				}

				select {
				case out <- rumor:
				case <-ctx.Done():
					return
				}

			case reason := <-sub.ClosedReason:
				if strings.HasPrefix(reason, "auth-required:") && !hasAuthed {
					authErr := relay.Auth(ctx, func(authCtx context.Context, authEvt *nostr.Event) error {
						return b.kr.SignEvent(authCtx, authEvt)
					})
					if authErr == nil {
						hasAuthed = true
						goto subscribe
					}
					log.Printf("nip17: AUTH to %s failed: %v", relayURL, authErr)
				}
				log.Printf("nip17: CLOSED from %s: %s", relayURL, reason)
				goto reconnect
			}
		}

	reconnect:
		// Always recompute since with the correct backfill window.
		since = nostr.Timestamp(normalizeNIP17Since(time.Now().Unix()))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(nip17ReconnectBackoffMax, backoff*17/10)
	}
}

func (b *NIP17Bus) handleRumor(rumor nostr.Event) {
	if b.subHealth != nil {
		b.subHealth.RecordEvent()
	}
	// Only process kind 14 (NIP-17 DM rumor).
	if rumor.Kind != nostr.KindDirectMessage {
		log.Printf("nip17: skipping non-DM rumor kind=%d from=%s", rumor.Kind, rumor.PubKey.Hex())
		return
	}
	// Skip self-sent (sent-to-self copy we stored).
	if rumor.PubKey == b.public {
		log.Printf("nip17: skipping self-sent rumor event=%s", rumor.ID.Hex())
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
		RelayURL:   "", // gift wraps hide relay; not available here
		CreatedAt:  int64(rumor.CreatedAt),
		Scheme:     "nip17",
		Reply: func(ctx context.Context, reply string) error {
			return b.SendDM(ctx, senderPubkey.Hex(), reply)
		},
	}

	log.Printf("nip17: rumor received event=%s from=%s kind=%d created_at=%d",
		eventID, senderPubkey.Hex(), rumor.Kind, rumor.CreatedAt)

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
