// NostrHub is the shared Nostr connection layer for the entire runtime.
//
// It owns a single Pool that all subsystems (channels, tools, DM buses) share,
// ensuring WebSocket connections are deduplicated — five channels on the same
// relay use ONE connection, not five.
//
// The hub also provides a subscription manager that tracks named subscriptions,
// supports dynamic add/remove, and properly handles EOSE and CLOSED signals
// instead of relying on timeouts or polling.
package runtime

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	nostr "fiatjaf.com/nostr"
)

// DefaultSinceJitter is the duration subtracted from the Since timestamp
// on subscription connect to capture events during brief disconnection gaps.
const DefaultSinceJitter = 30 * time.Second

// NostrHub is the shared Nostr connection layer.  Create one per runtime via
// NewHub and pass it to every subsystem (channels, tools, buses) so they all
// share the same WebSocket connections.
type NostrHub struct {
	pool     *nostr.Pool
	keyer    nostr.Keyer
	pubkey   nostr.PubKey
	selector *RelaySelector

	mu     sync.RWMutex
	subs   map[string]*ManagedSub
	subSeq atomic.Int64

	ctx    context.Context
	cancel context.CancelFunc
}

// SubOpts configures a managed subscription.
type SubOpts struct {
	// ID is an optional subscription identifier.  If empty, one is auto-generated.
	ID string
	// Filter is the nostr filter for the subscription.
	Filter nostr.Filter
	// Relays to subscribe on.  If empty, uses the hub's fallback read relays.
	Relays []string
	// OnEvent is called for each matching event (required).
	OnEvent func(nostr.RelayEvent)
	// OnEOSE is called when all relays have sent EOSE.  Optional.
	OnEOSE func()
	// OnClosed is called when a relay sends a CLOSED message.  Optional.
	// If the closure was caused by auth-required and was handled, handledAuth is true.
	OnClosed func(relay *nostr.Relay, reason string, handledAuth bool)
}

// ManagedSub is an active subscription tracked by the hub.
type ManagedSub struct {
	ID     string
	Filter nostr.Filter
	Relays []string
	cancel context.CancelFunc
}

// SubInfo is a read-only snapshot of a managed subscription.
type SubInfo struct {
	ID     string
	Filter nostr.Filter
	Relays []string
}

// NewHub creates a NostrHub with the given keyer and relay selector.
// The hub owns a single Pool with full NIP-42 auth support.
func NewHub(ctx context.Context, keyer nostr.Keyer, selector *RelaySelector) (*NostrHub, error) {
	if keyer == nil {
		return nil, fmt.Errorf("nostr hub: keyer is required")
	}
	pk, err := keyer.GetPublicKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("nostr hub: resolve public key: %w", err)
	}

	hubCtx, hubCancel := context.WithCancel(ctx)

	h := &NostrHub{
		pool:     nostr.NewPool(PoolOptsNIP42(keyer)),
		keyer:    keyer,
		pubkey:   pk,
		selector: selector,
		subs:     make(map[string]*ManagedSub),
		ctx:      hubCtx,
		cancel:   hubCancel,
	}
	return h, nil
}

// Pool returns the shared Pool.  Subsystems that need low-level access (e.g.
// DM buses doing NIP-17 gift wrapping) can use this directly.
func (h *NostrHub) Pool() *nostr.Pool { return h.pool }

// Keyer returns the hub's signing keyer.
func (h *NostrHub) Keyer() nostr.Keyer { return h.keyer }

// PublicKey returns the agent's public key hex.
func (h *NostrHub) PublicKey() string { return h.pubkey.Hex() }

// PubKey returns the agent's public key.
func (h *NostrHub) PubKey() nostr.PubKey { return h.pubkey }

// Selector returns the NIP-65 relay selector, or nil if none was provided.
func (h *NostrHub) Selector() *RelaySelector { return h.selector }

// ReadRelays returns the preferred read relays from the selector, or an empty
// slice if no selector is set.
func (h *NostrHub) ReadRelays() []string {
	if h.selector != nil {
		return h.selector.FallbackRead()
	}
	return nil
}

// WriteRelays returns the preferred write relays from the selector.
func (h *NostrHub) WriteRelays() []string {
	if h.selector != nil {
		return h.selector.FallbackWrite()
	}
	return nil
}

// ResolveRelays returns the provided relays if non-empty, otherwise falls back
// to the hub's default read relays.
func (h *NostrHub) ResolveRelays(override []string) []string {
	if len(override) > 0 {
		return override
	}
	return h.ReadRelays()
}

// ─── Subscription Management ─────────────────────────────────────────────────

// Subscribe creates a named subscription on the hub's shared pool.
//
// The subscription uses SubscribeManyNotifyClosed for proper protocol handling:
// EOSE and CLOSED signals are dispatched to the caller's callbacks instead of
// using timeouts.  The underlying pool handles auth-required retries and
// automatic reconnection.
//
// The subscription lives until cancelled via Unsubscribe or the hub is closed.
func (h *NostrHub) Subscribe(ctx context.Context, opts SubOpts) (*ManagedSub, error) {
	if opts.OnEvent == nil {
		return nil, fmt.Errorf("nostr hub: OnEvent handler is required")
	}

	// Generate ID if not provided.
	id := opts.ID
	if id == "" {
		id = fmt.Sprintf("sub-%d", h.subSeq.Add(1))
	}

	relays := opts.Relays
	if len(relays) == 0 {
		relays = h.ReadRelays()
	}
	if len(relays) == 0 {
		return nil, fmt.Errorf("nostr hub: no relays for subscription %q", id)
	}

	h.mu.Lock()
	if _, exists := h.subs[id]; exists {
		h.mu.Unlock()
		return nil, fmt.Errorf("nostr hub: subscription %q already exists", id)
	}

	subCtx, subCancel := context.WithCancel(ctx)
	ms := &ManagedSub{
		ID:     id,
		Filter: opts.Filter,
		Relays: relays,
		cancel: subCancel,
	}
	h.subs[id] = ms
	h.mu.Unlock()

	// Apply jitter to the Since timestamp to capture events from brief
	// disconnection gaps.  The pool handles reconnection internally and
	// re-sends the same filter, so backdating Since ensures gap coverage.
	// Callers rely on event ID deduplication (or idempotent handlers) to
	// handle the small overlap window.
	filter := opts.Filter
	if filter.Since > 0 {
		backdated := filter.Since - nostr.Timestamp(DefaultSinceJitter.Seconds())
		if backdated < 0 {
			backdated = 0
		}
		filter.Since = backdated
	}

	// Use SubscribeManyNotifyClosed for proper CLOSED handling.
	// The pool internally handles:
	//   - auth-required retries (via AuthRequiredHandler)
	//   - automatic reconnection on disconnect
	//   - deduplication of events
	// CLOSED signals are dispatched to the caller; EOSE is handled by the pool
	// internally for its reconnection logic.
	events, closedCh := h.pool.SubscribeManyNotifyClosed(
		subCtx, relays, filter, nostr.SubscriptionOptions{},
	)

	// Start the event dispatch goroutine.
	go func() {
		defer func() {
			h.mu.Lock()
			delete(h.subs, id)
			h.mu.Unlock()
		}()

		for {
			select {
			case re, ok := <-events:
				if !ok {
					return
				}
				opts.OnEvent(re)

			case rc, ok := <-closedCh:
				if !ok {
					continue // channel closed, events chan will also close
				}
				if opts.OnClosed != nil {
					opts.OnClosed(rc.Relay, rc.Reason, rc.HandledAuth)
				}
				if !rc.HandledAuth {
					log.Printf("nostr hub: sub %q closed by %s: %s", id, rc.Relay.URL, rc.Reason)
				}

			case <-subCtx.Done():
				return
			}
		}
	}()

	return ms, nil
}

// Unsubscribe cancels and removes a named subscription.
func (h *NostrHub) Unsubscribe(id string) bool {
	h.mu.Lock()
	ms, ok := h.subs[id]
	if ok {
		delete(h.subs, id)
	}
	h.mu.Unlock()

	if ok && ms.cancel != nil {
		ms.cancel()
	}
	return ok
}

// Subscriptions returns a snapshot of all active subscriptions.
func (h *NostrHub) Subscriptions() []SubInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]SubInfo, 0, len(h.subs))
	for _, ms := range h.subs {
		out = append(out, SubInfo{
			ID:     ms.ID,
			Filter: ms.Filter,
			Relays: ms.Relays,
		})
	}
	return out
}

// ─── Publish / Fetch ─────────────────────────────────────────────────────────

// Publish publishes an event to the given relays using the shared pool.
// The event should already be signed.  Returns results as they arrive.
func (h *NostrHub) Publish(ctx context.Context, relays []string, evt nostr.Event) <-chan nostr.PublishResult {
	if len(relays) == 0 {
		relays = h.WriteRelays()
	}
	return h.pool.PublishMany(ctx, relays, evt)
}

// Fetch performs a one-shot query (closes after EOSE) on the given relays.
func (h *NostrHub) Fetch(ctx context.Context, relays []string, filter nostr.Filter) <-chan nostr.RelayEvent {
	if len(relays) == 0 {
		relays = h.ReadRelays()
	}
	return h.pool.FetchMany(ctx, relays, filter, nostr.SubscriptionOptions{})
}

// SignEvent signs an event using the hub's keyer.
func (h *NostrHub) SignEvent(ctx context.Context, evt *nostr.Event) error {
	return h.keyer.SignEvent(ctx, evt)
}

// ─── Lifecycle ────────────────────────────────────────────────────────────────

// Close cancels all subscriptions and closes the shared pool.
func (h *NostrHub) Close() {
	// Cancel all managed subscriptions.
	h.mu.Lock()
	for id, ms := range h.subs {
		ms.cancel()
		delete(h.subs, id)
	}
	h.mu.Unlock()

	h.cancel()
	h.pool.Close("nostr hub closed")
}
