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
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip17"
	"fiatjaf.com/nostr/nip59"

	metricspkg "metiq/internal/metrics"
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

var ErrRecipientNotNIP17Ready = errors.New("recipient is not NIP-17 ready: no kind:10050 DM relay list")

// NIP17Participant identifies a member of a NIP-17 room. RelayURL is an
// optional p-tag hint; delivery still uses the participant's kind-10050 list.
type NIP17Participant struct {
	PubKey   string
	RelayURL string
}

// NIP17Room is the participant set that defines a NIP-17 chat room. The sender
// is represented by the rumor pubkey and must not be repeated in Participants.
type NIP17Room struct {
	Participants []NIP17Participant
	Subject      string
}

// NIP17FileMessage contains the mandatory kind-15 fields from NIP-17.
type NIP17FileMessage struct {
	URL                 string
	FileType            string
	EncryptionAlgorithm string
	DecryptionKey       string
	DecryptionNonce     string
	SHA256              string
	OriginalSHA256      string
	Size                string
	Dimensions          string
	Thumbhash           string
	Blurhash            string
	Thumbnail           string
	Fallbacks           []string
}

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

	// testListenGiftWraps is an unexported seam used by runtime tests to drive
	// deterministic gift-wrap stream closure/rebind behavior.
	testListenGiftWraps func(ctx context.Context, relays []string, since nostr.Timestamp) <-chan nostr.Event
	// testLookupDMRelays is an unexported seam used by runtime tests to avoid
	// network relay-list lookup when exercising SendDM routing behavior.
	testLookupDMRelays func(ctx context.Context, pk nostr.PubKey) []string
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

	pool := NewPoolNIP42(ks)
	ownsPool := true
	if opts.Hub != nil {
		if opts.Hub.PubKey() != pub {
			return nil, fmt.Errorf("nip17 bus: hub pubkey does not match keyer pubkey")
		}
		pool = opts.Hub.Pool()
		ownsPool = false
	}
	ctx, cancel := context.WithCancel(parent)
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
						metricspkg.RecordHandlerFailure("dm")
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
	if b == nil {
		return
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.ownsPool && b.pool != nil {
		b.pool.Close("nip17 bus closed")
	}
	b.wg.Wait()
}

// SendDM sends a one-to-one kind-14 NIP-17 message.
func (b *NIP17Bus) SendDM(ctx context.Context, toPubKey string, text string) error {
	return b.SendRoomMessage(ctx, NIP17Room{Participants: []NIP17Participant{{PubKey: toPubKey}}}, text)
}

// SendRoomMessage sends a kind-14 message to every participant in a room and
// stores a separately wrapped copy for the sender.
func (b *NIP17Bus) SendRoomMessage(ctx context.Context, room NIP17Room, text string) error {
	text, err := normalizeOutboundDMText(text)
	if err != nil {
		return err
	}
	return b.sendNIP17Rumor(ctx, nostr.KindDirectMessage, text, room, nil)
}

// SendRoomReply sends a kind-14 direct reply. The e tag is the direct parent
// message, as specified by NIP-17.
func (b *NIP17Bus) SendRoomReply(ctx context.Context, room NIP17Room, parentEventID, relayHint, text string) error {
	if _, err := nostr.IDFromHex(strings.TrimSpace(parentEventID)); err != nil {
		return fmt.Errorf("invalid reply event id: %w", err)
	}
	text, err := normalizeOutboundDMText(text)
	if err != nil {
		return err
	}
	tag := nostr.Tag{"e", strings.TrimSpace(parentEventID)}
	if hint := strings.TrimSpace(relayHint); hint != "" {
		tag = append(tag, hint)
	}
	return b.sendNIP17Rumor(ctx, nostr.KindDirectMessage, text, room, nostr.Tags{tag})
}

// SendFileMessage sends a kind-15 encrypted file message.
func (b *NIP17Bus) SendFileMessage(ctx context.Context, room NIP17Room, file NIP17FileMessage, parentEventID, relayHint string) error {
	tags := nostr.Tags{
		{"file-type", strings.TrimSpace(file.FileType)},
		{"encryption-algorithm", strings.TrimSpace(file.EncryptionAlgorithm)},
		{"decryption-key", strings.TrimSpace(file.DecryptionKey)},
		{"decryption-nonce", strings.TrimSpace(file.DecryptionNonce)},
		{"x", strings.TrimSpace(file.SHA256)},
	}
	optional := []struct{ name, value string }{
		{"ox", file.OriginalSHA256}, {"size", file.Size}, {"dim", file.Dimensions},
		{"thumbhash", file.Thumbhash}, {"blurhash", file.Blurhash}, {"thumb", file.Thumbnail},
	}
	for _, item := range optional {
		if value := strings.TrimSpace(item.value); value != "" {
			tags = append(tags, nostr.Tag{item.name, value})
		}
	}
	for _, fallback := range file.Fallbacks {
		if fallback = strings.TrimSpace(fallback); fallback != "" {
			tags = append(tags, nostr.Tag{"fallback", fallback})
		}
	}
	if parentEventID = strings.TrimSpace(parentEventID); parentEventID != "" {
		if _, err := nostr.IDFromHex(parentEventID); err != nil {
			return fmt.Errorf("invalid reply event id: %w", err)
		}
		tag := nostr.Tag{"e", parentEventID}
		if hint := strings.TrimSpace(relayHint); hint != "" {
			tag = append(tag, hint, "reply")
		} else {
			tag = append(tag, "", "reply")
		}
		tags = append(tags, tag)
	}
	return b.sendNIP17Rumor(ctx, nostr.KindFileMessage, strings.TrimSpace(file.URL), room, tags)
}

// SendReaction sends a wrapped NIP-25 kind-7 reaction in the room.
func (b *NIP17Bus) SendReaction(ctx context.Context, room NIP17Room, targetEventID, relayHint, reaction string) error {
	targetEventID = strings.TrimSpace(targetEventID)
	if _, err := nostr.IDFromHex(targetEventID); err != nil {
		return fmt.Errorf("invalid reaction target event id: %w", err)
	}
	tag := nostr.Tag{"e", targetEventID}
	if hint := strings.TrimSpace(relayHint); hint != "" {
		tag = append(tag, hint)
	}
	return b.sendNIP17Rumor(ctx, nostr.KindReaction, reaction, room, nostr.Tags{tag})
}

// DeleteMessages sends a wrapped NIP-09 kind-5 deletion request to the room.
func (b *NIP17Bus) DeleteMessages(ctx context.Context, room NIP17Room, reason string, eventIDs ...string) error {
	if len(eventIDs) == 0 {
		return fmt.Errorf("at least one event id is required")
	}
	tags := make(nostr.Tags, 0, len(eventIDs)+1)
	for _, eventID := range eventIDs {
		eventID = strings.TrimSpace(eventID)
		if _, err := nostr.IDFromHex(eventID); err != nil {
			return fmt.Errorf("invalid deletion event id: %w", err)
		}
		tags = append(tags, nostr.Tag{"e", eventID})
	}
	tags = append(tags, nostr.Tag{"k", fmt.Sprint(nostr.KindDirectMessage)})
	return b.sendNIP17Rumor(ctx, nostr.KindDeletion, reason, room, tags)
}

func (b *NIP17Bus) sendNIP17Rumor(ctx context.Context, kind nostr.Kind, content string, room NIP17Room, extraTags nostr.Tags) (err error) {
	defer func() {
		outcome := "success"
		if err != nil {
			outcome = "failure"
		}
		metricspkg.RecordPublishOutcome("nip17", outcome)
	}()

	rumor, recipients, err := b.buildNIP17Rumor(kind, content, room, extraTags)
	if err != nil {
		return err
	}
	return b.publishNIP17Rumor(ctx, rumor, recipients)
}

func (b *NIP17Bus) buildNIP17Rumor(kind nostr.Kind, content string, room NIP17Room, extraTags nostr.Tags) (nostr.Event, []nostr.PubKey, error) {
	if len(room.Participants) == 0 {
		return nostr.Event{}, nil, fmt.Errorf("nip17 room requires at least one participant")
	}
	tags := make(nostr.Tags, 0, len(room.Participants)+len(extraTags)+1)
	recipients := make([]nostr.PubKey, 0, len(room.Participants))
	seen := make(map[nostr.PubKey]struct{}, len(room.Participants))
	for _, participant := range room.Participants {
		pk, err := ParsePubKey(strings.TrimSpace(participant.PubKey))
		if err != nil {
			return nostr.Event{}, nil, fmt.Errorf("invalid room participant: %w", err)
		}
		if pk == b.public {
			return nostr.Event{}, nil, fmt.Errorf("room participants must not repeat the sender pubkey")
		}
		if _, exists := seen[pk]; exists {
			continue
		}
		seen[pk] = struct{}{}
		recipients = append(recipients, pk)
		tag := nostr.Tag{"p", pk.Hex()}
		if hint := strings.TrimSpace(participant.RelayURL); hint != "" {
			tag = append(tag, hint)
		}
		tags = append(tags, tag)
	}
	if subject := strings.TrimSpace(room.Subject); subject != "" {
		tags = append(tags, nostr.Tag{"subject", subject})
	}
	for _, tag := range extraTags {
		tags = append(tags, append(nostr.Tag(nil), tag...))
	}
	rumor := nostr.Event{
		Kind: kind, PubKey: b.public, CreatedAt: nostr.Now(), Tags: tags, Content: content,
	}
	rumor.ID = rumor.GetID()
	if err := validateNIP17RumorKind(rumor); err != nil {
		return nostr.Event{}, nil, err
	}
	return rumor, recipients, nil
}

func (b *NIP17Bus) publishNIP17Rumor(ctx context.Context, rumor nostr.Event, recipients []nostr.PubKey) error {
	if b.pool == nil || b.kr == nil {
		return fmt.Errorf("nip17 publisher is not initialized")
	}
	type target struct {
		pubkey nostr.PubKey
		relays []string
		wrap   nostr.Event
	}
	targets := make([]target, 0, len(recipients)+1)
	allRecipients := append(append([]nostr.PubKey(nil), recipients...), b.public)
	for _, recipient := range allRecipients {
		var relays []string
		if recipient == b.public {
			relays = b.currentRelays()
		} else {
			var err error
			relays, err = b.lookupDMRelays(ctx, recipient)
			if err != nil {
				return fmt.Errorf("resolve DM relays for %s: %w", recipient.Hex(), err)
			}
		}
		if len(relays) == 0 {
			return fmt.Errorf("no DM relays for %s", recipient.Hex())
		}
		wrap, err := nip59.GiftWrap(
			rumor,
			recipient,
			func(plaintext string) (string, error) { return b.kr.Encrypt(ctx, plaintext, recipient) },
			func(evt *nostr.Event) error { return b.kr.SignEvent(ctx, evt) },
			nil,
		)
		if err != nil {
			return fmt.Errorf("gift wrap for %s: %w", recipient.Hex(), err)
		}
		targets = append(targets, target{pubkey: recipient, relays: relays, wrap: wrap})
	}

	results := make(chan error, len(targets))
	for _, target := range targets {
		target := target
		go func() {
			if err := b.publishNIP17Wrap(ctx, target.wrap, target.relays); err != nil {
				results <- fmt.Errorf("publish for %s: %w", target.pubkey.Hex(), err)
				return
			}
			results <- nil
		}()
	}
	var failures []error
	for range targets {
		if err := <-results; err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (b *NIP17Bus) publishNIP17Wrap(ctx context.Context, wrap nostr.Event, relays []string) error {
	results := make(chan error, len(relays))
	for _, relayURL := range relays {
		relayURL := relayURL
		go func() {
			relay, err := b.pool.EnsureRelay(relayURL)
			if err == nil {
				err = relay.Publish(ctx, wrap)
				if err != nil && strings.HasPrefix(err.Error(), "auth-required:") {
					if authErr := relay.Auth(ctx, b.kr.SignEvent); authErr == nil {
						err = relay.Publish(ctx, wrap)
					} else {
						err = authErr
					}
				}
			}
			results <- err
		}()
	}
	var failures []error
	accepted := false
	for range relays {
		if err := <-results; err != nil {
			failures = append(failures, err)
		} else {
			accepted = true
		}
	}
	if accepted {
		return nil
	}
	return fmt.Errorf("no relay accepted gift wrap: %w", errors.Join(failures...))
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
	if sinceUnix <= 0 {
		return time.Now().Add(-nip17GiftWrapBackfill).Unix()
	}
	// A caller-provided checkpoint may intentionally request history older than
	// the default backfill window. NIP-17/NIP-59 place no maximum age on valid
	// rumors, so preserve that request while adding the gift-wrap skew cushion.
	adjusted := sinceUnix - int64(nip17GiftWrapBackfill.Seconds())
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
		listenGiftWraps := b.listenGiftWraps
		if b.testListenGiftWraps != nil {
			listenGiftWraps = b.testListenGiftWraps
		}
		rumCh := listenGiftWraps(cycleCtx, b.currentRelays(), currentSince)

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
		eoseCh := sub.EndOfStoredEvents

		for {
			select {
			case <-ctx.Done():
				return

			case evt, more := <-sub.Events:
				if !more {
					// Connection lost — reconnect with correct backfill.
					// This is expected behavior for idle timeout or relay restart
					log.Printf("nip17: subscription to %s closed (relay timeout or restart), reconnecting automatically", relayURL)
					goto reconnect
				}

				if err := b.validateGiftWrapEvent(evt, time.Now()); err != nil {
					log.Printf("nip17: rejecting gift wrap from %s: %v", relayURL, err)
					continue
				}

				// Unwrap the gift wrap inline.
				rumor, unwrapErr := nip59.GiftUnwrap(evt, func(pk nostr.PubKey, ct string) (string, error) {
					return b.kr.Decrypt(ctx, ct, pk)
				})
				if unwrapErr != nil {
					log.Printf("nip17: unwrap from %s: %v", relayURL, unwrapErr)
					continue
				}

				if err := b.validateRumorEvent(rumor, time.Now()); err != nil {
					log.Printf("nip17: rejecting rumor from %s: %v", relayURL, err)
					continue
				}

				select {
				case out <- rumor:
				case <-ctx.Done():
					return
				}

			case <-eoseCh:
				log.Printf("nip17: EOSE from %s; switching to realtime", relayURL)
				eoseCh = nil

			case reason := <-sub.ClosedReason:
				if strings.HasPrefix(reason, "auth-required:") && !hasAuthed {
					authErr := relay.Auth(ctx, func(authCtx context.Context, authEvt *nostr.Event) error {
						return b.kr.SignEvent(authCtx, authEvt)
					})
					if authErr == nil {
						hasAuthed = true
						sub.Unsub()
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
	// Skip self-sent (sent-to-self copy we stored).
	if rumor.PubKey == b.public {
		return
	}

	eventID := rumor.ID.Hex()
	if b.markSeen17(eventID) {
		return
	}

	// Deliver the rumor content as-is without size validation or trimming.
	// If the gift-wrap was successfully unwrapped and decrypted, we should
	// deliver the full message to the handler. Any size-based filtering
	// belongs in the application layer, not the transport layer.
	if b.onMessage == nil {
		return
	}

	senderPubkey := rumor.PubKey
	recipients, subject, replyTo, replyRoom := nip17RumorMetadata(rumor, b.public)
	msg := InboundDM{
		EventID:    eventID,
		FromPubKey: senderPubkey.Hex(),
		Text:       rumor.Content,
		RelayURL:   "", // gift wraps hide relay; not available here
		CreatedAt:  int64(rumor.CreatedAt),
		Scheme:     "nip17",
		Kind:       rumor.Kind,
		Tags:       cloneNostrTags(rumor.Tags),
		Recipients: recipients,
		Subject:    subject,
		ReplyTo:    replyTo,
		Reply: func(ctx context.Context, reply string) error {
			return b.SendRoomReply(ctx, replyRoom, eventID, "", reply)
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
func (b *NIP17Bus) lookupDMRelays(ctx context.Context, pk nostr.PubKey) ([]string, error) {
	if b.testLookupDMRelays != nil {
		relays := sanitizeRelayList(b.testLookupDMRelays(ctx, pk))
		if len(relays) == 0 {
			return nil, ErrRecipientNotNIP17Ready
		}
		return relays, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	relays := sanitizeRelayList(nip17.GetDMRelays(lookupCtx, pk, b.pool, b.currentRelays()))
	if len(relays) == 0 {
		return nil, ErrRecipientNotNIP17Ready
	}
	return relays, nil
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

func (b *NIP17Bus) validateGiftWrapEvent(evt nostr.Event, now time.Time) error {
	if evt.Kind != nostr.KindGiftWrap {
		return fmt.Errorf("unexpected kind=%d", evt.Kind)
	}
	if !evt.Tags.ContainsAny("p", []string{b.public.Hex()}) {
		return fmt.Errorf("gift wrap missing recipient tag")
	}
	if !evt.CheckID() {
		return fmt.Errorf("invalid gift wrap id")
	}
	if !evt.VerifySignature() {
		return fmt.Errorf("invalid gift wrap signature")
	}
	if timestampTooFarFuture(int64(evt.CreatedAt), now, inboundEventMaxFutureSkew) {
		return fmt.Errorf("gift wrap timestamp from the future")
	}
	return nil
}

func (b *NIP17Bus) validateRumorEvent(rumor nostr.Event, now time.Time) error {
	switch rumor.Kind {
	case nostr.KindDirectMessage, nostr.KindFileMessage, nostr.KindReaction, nostr.KindDeletion:
	default:
		return fmt.Errorf("unexpected rumor kind=%d", rumor.Kind)
	}

	// NIP-17 requires at least one p tag identifying a recipient.
	hasRecipientTag := false
	for _, tag := range rumor.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			hasRecipientTag = true
			break
		}
	}
	if !hasRecipientTag {
		return fmt.Errorf("rumor missing recipient tag")
	}

	// Accept rumors where we are the recipient (incoming message) OR where we are
	// the sender (backup copy of our own sent message). When sending a DM to Bob,
	// the library creates a rumor with Bob's pubkey in the p tag, then gift-wraps
	// it to both Bob and ourselves. On reconnect, we fetch our backup copy which
	// has Bob's pubkey in the p tag, not ours. handleRumor will skip processing
	// self-sent messages after validation.
	isRecipient := rumor.Tags.ContainsAny("p", []string{b.public.Hex()})
	isSender := rumor.PubKey == b.public
	if !isRecipient && !isSender {
		return fmt.Errorf("rumor not addressed to us (recipient check) and not sent by us (sender check)")
	}

	if !rumor.CheckID() {
		return fmt.Errorf("invalid rumor id")
	}
	// NIP-59 rumors are intentionally unsigned. Authenticity is established by
	// successful gift-wrap and seal verification before unwrap; requiring a rumor
	// signature here would reject every spec-compliant inbound NIP-17 DM.
	if timestampTooFarFuture(int64(rumor.CreatedAt), now, inboundEventMaxFutureSkew) {
		return fmt.Errorf("rumor timestamp from the future")
	}
	return validateNIP17RumorKind(rumor)
}

func validateNIP17RumorKind(rumor nostr.Event) error {
	switch rumor.Kind {
	case nostr.KindDirectMessage:
		return nil
	case nostr.KindFileMessage:
		if strings.TrimSpace(rumor.Content) == "" {
			return fmt.Errorf("kind-15 file URL is required")
		}
		for _, name := range []string{"file-type", "encryption-algorithm", "decryption-key", "decryption-nonce", "x"} {
			if nip17TagValue(rumor.Tags, name) == "" {
				return fmt.Errorf("kind-15 missing %s tag", name)
			}
		}
		if algorithm := nip17TagValue(rumor.Tags, "encryption-algorithm"); algorithm != "aes-gcm" {
			return fmt.Errorf("kind-15 unsupported encryption algorithm %q", algorithm)
		}
		if _, err := nostr.IDFromHex(nip17TagValue(rumor.Tags, "x")); err != nil {
			return fmt.Errorf("kind-15 invalid x hash: %w", err)
		}
		return nil
	case nostr.KindReaction:
		target := nip17LastTagValue(rumor.Tags, "e")
		if _, err := nostr.IDFromHex(target); err != nil {
			return fmt.Errorf("kind-7 reaction missing valid e tag: %w", err)
		}
		return nil
	case nostr.KindDeletion:
		found := false
		for _, tag := range rumor.Tags {
			if len(tag) < 2 || (tag[0] != "e" && tag[0] != "a") {
				continue
			}
			found = true
			if tag[0] == "e" {
				if _, err := nostr.IDFromHex(tag[1]); err != nil {
					return fmt.Errorf("kind-5 invalid e tag: %w", err)
				}
			}
		}
		if !found {
			return fmt.Errorf("kind-5 deletion requires an e or a tag")
		}
		return nil
	default:
		return fmt.Errorf("unsupported NIP-17 rumor kind=%d", rumor.Kind)
	}
}

func nip17RumorMetadata(rumor nostr.Event, local nostr.PubKey) (recipients []string, subject, replyTo string, room NIP17Room) {
	seenRoom := map[string]struct{}{}
	addRoom := func(pubkey, relay string) {
		if pubkey == "" || pubkey == local.Hex() {
			return
		}
		if _, exists := seenRoom[pubkey]; exists {
			return
		}
		seenRoom[pubkey] = struct{}{}
		room.Participants = append(room.Participants, NIP17Participant{PubKey: pubkey, RelayURL: relay})
	}
	addRoom(rumor.PubKey.Hex(), "")
	for _, tag := range rumor.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "p":
			recipients = append(recipients, tag[1])
			relay := ""
			if len(tag) >= 3 {
				relay = tag[2]
			}
			addRoom(tag[1], relay)
		case "subject":
			subject = tag[1]
		case "e":
			if rumor.Kind != nostr.KindDeletion {
				replyTo = tag[1]
			}
		}
	}
	room.Subject = subject
	return recipients, subject, replyTo, room
}

func nip17TagValue(tags nostr.Tags, name string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			return strings.TrimSpace(tag[1])
		}
	}
	return ""
}

func nip17LastTagValue(tags nostr.Tags, name string) string {
	value := ""
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			value = strings.TrimSpace(tag[1])
		}
	}
	return value
}

func cloneNostrTags(tags nostr.Tags) nostr.Tags {
	cloned := make(nostr.Tags, len(tags))
	for i, tag := range tags {
		cloned[i] = append(nostr.Tag(nil), tag...)
	}
	return cloned
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
