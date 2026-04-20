// Package runtime — relay_sets.go manages NIP-51 kind:30002 relay set
// subscriptions. The agent publishes purpose-specific relay sets (e.g.
// nip29-relays, chat-relays, search-relays) and subscribes to its own
// events for bidirectional sync — just like the NIP-65 self-sync pattern.
//
// When a relay set is updated (either locally via a tool call or remotely
// via another client), the registered callback is invoked so the runtime
// can rebind affected subscriptions.
package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/nip51"
)

// RelaySetEntry holds the current state of one relay set.
type RelaySetEntry struct {
	DTag      string
	Relays    []string
	EventID   string
	CreatedAt int64
}

// RelaySetCallback is called when a relay set is updated.
// dtag identifies which set changed; relays is the new relay list.
type RelaySetCallback func(dtag string, relays []string)

// RelaySetRegistry manages the agent's kind:30002 relay sets.
// It caches current values, publishes updates, and dispatches change callbacks.
type RelaySetRegistry struct {
	mu        sync.RWMutex
	sets      map[string]*RelaySetEntry // keyed by d-tag
	callbacks []RelaySetCallback
}

// NewRelaySetRegistry creates an empty registry.
func NewRelaySetRegistry() *RelaySetRegistry {
	return &RelaySetRegistry{
		sets: make(map[string]*RelaySetEntry),
	}
}

// OnChange registers a callback that fires whenever any relay set is updated.
func (r *RelaySetRegistry) OnChange(fn RelaySetCallback) {
	r.mu.Lock()
	r.callbacks = append(r.callbacks, fn)
	r.mu.Unlock()
}

// Get returns the current relay list for a d-tag, or nil if not loaded.
func (r *RelaySetRegistry) Get(dtag string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.sets[dtag]; ok {
		out := make([]string, len(e.Relays))
		copy(out, e.Relays)
		return out
	}
	return nil
}

// GetEntry returns the full entry for a d-tag.
func (r *RelaySetRegistry) GetEntry(dtag string) (RelaySetEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.sets[dtag]
	if !ok {
		return RelaySetEntry{}, false
	}
	cp := *e
	cp.Relays = make([]string, len(e.Relays))
	copy(cp.Relays, e.Relays)
	return cp, true
}

// All returns a snapshot of all relay sets.
func (r *RelaySetRegistry) All() map[string]RelaySetEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]RelaySetEntry, len(r.sets))
	for k, v := range r.sets {
		cp := *v
		cp.Relays = make([]string, len(v.Relays))
		copy(cp.Relays, v.Relays)
		out[k] = cp
	}
	return out
}

// Set updates a relay set locally (e.g. from config). Does NOT publish.
// Fires callbacks if the relay list actually changed.
func (r *RelaySetRegistry) Set(dtag string, relays []string) {
	r.mu.Lock()
	existing := r.sets[dtag]
	changed := existing == nil || !relaySliceEqual(existing.Relays, relays)
	if changed {
		r.sets[dtag] = &RelaySetEntry{
			DTag:   dtag,
			Relays: append([]string{}, relays...),
		}
	}
	cbs := append([]RelaySetCallback{}, r.callbacks...)
	r.mu.Unlock()

	if changed {
		for _, cb := range cbs {
			cb(dtag, relays)
		}
	}
}

// applyFromEvent updates a relay set from a decoded NIP-51 event.
// Called by the subscription watcher. Fires callbacks if changed.
func (r *RelaySetRegistry) applyFromEvent(list *nip51.List) {
	relays := nip51.RelaysFromList(list)
	r.mu.Lock()
	existing := r.sets[list.DTag]
	// Only apply if this event is newer than what we have.
	if existing != nil {
		if existing.CreatedAt > list.CreatedAt {
			r.mu.Unlock()
			return
		}
		if existing.CreatedAt == list.CreatedAt {
			// Deterministic tie-breaker: use EventID lexicographic ordering.
			// This prevents ignoring valid updates that share a timestamp.
			if existing.EventID >= list.EventID {
				r.mu.Unlock()
				return
			}
		}
	}
	changed := existing == nil || !relaySliceEqual(existing.Relays, relays)
	r.sets[list.DTag] = &RelaySetEntry{
		DTag:      list.DTag,
		Relays:    relays,
		EventID:   list.EventID,
		CreatedAt: list.CreatedAt,
	}
	cbs := append([]RelaySetCallback{}, r.callbacks...)
	r.mu.Unlock()

	if changed {
		for _, cb := range cbs {
			cb(list.DTag, relays)
		}
	}
}

// relaySliceEqual checks if two string slices have the same elements (order-sensitive).
func relaySliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── Subscription ─────────────────────────────────────────────────────────────

// RelaySetSyncOptions configures the kind:30002 relay set self-sync subscriber.
type RelaySetSyncOptions struct {
	// Keyer is used to resolve the agent's pubkey.
	Keyer nostr.Keyer
	// Pool is the Nostr connection pool.
	Pool *nostr.Pool
	// Relays to subscribe on.
	Relays []string
	// Registry receives decoded relay set updates.
	Registry *RelaySetRegistry
	// WatchDTags limits the subscription to specific d-tags.
	// If empty, all kind:30002 events from the agent are processed.
	WatchDTags []string
}

// RelaySetSelfSync subscribes to the agent's own kind:30002 relay set events
// and keeps the registry up to date. It follows the same EOSE-aware pattern
// as NIP65SelfSync: events before EOSE are applied silently, events after
// EOSE trigger change callbacks and logging.
func RelaySetSelfSync(ctx context.Context, opts RelaySetSyncOptions) error {
	if opts.Keyer == nil {
		return fmt.Errorf("relay-set-sync: keyer is required")
	}
	if opts.Registry == nil {
		return fmt.Errorf("relay-set-sync: registry is required")
	}
	if len(opts.Relays) == 0 {
		return fmt.Errorf("relay-set-sync: at least one relay required")
	}

	pkCtx, pkCancel := context.WithTimeout(ctx, 10*time.Second)
	pk, err := opts.Keyer.GetPublicKey(pkCtx)
	pkCancel()
	if err != nil {
		return fmt.Errorf("relay-set-sync: get public key: %w", err)
	}

	filter := nostr.Filter{
		Kinds:   []nostr.Kind{nostr.Kind(nip51.KindRelaySet)},
		Authors: []nostr.PubKey{pk},
	}
	// If specific d-tags are requested, add a tag filter.
	if len(opts.WatchDTags) > 0 {
		filter.Tags = nostr.TagMap{"d": opts.WatchDTags}
	}

	// Build a set for fast lookup if we're filtering.
	watchSet := make(map[string]struct{}, len(opts.WatchDTags))
	for _, d := range opts.WatchDTags {
		watchSet[d] = struct{}{}
	}

	go func() {
		events, eoseCh := opts.Pool.SubscribeManyNotifyEOSE(
			ctx, opts.Relays, filter, nostr.SubscriptionOptions{},
		)
		// eoseCh is nil'd after EOSE to prevent busy-loop (closed channels return immediately).
		for {
			select {
			case re, ok := <-events:
				if !ok {
					return
				}
				list := nip51.DecodeEvent(re.Event)
				if list.DTag == "" {
					continue
				}
				// If filtering by d-tag, skip non-matching events.
				if len(watchSet) > 0 {
					if _, ok := watchSet[list.DTag]; !ok {
						continue
					}
				}
				opts.Registry.applyFromEvent(list)
				if eoseCh == nil { // post-EOSE: log live updates
					relays := nip51.RelaysFromList(list)
					log.Printf("relay-set-sync: live update d=%q relays=%v", list.DTag, relays)
				}
			case <-eoseCh:
				eoseCh = nil // prevent busy-loop: closed channel returns immediately
				sets := opts.Registry.All()
				dtags := make([]string, 0, len(sets))
				for d := range sets {
					dtags = append(dtags, d)
				}
				log.Printf("relay-set-sync: EOSE — loaded %d relay sets: %s",
					len(sets), strings.Join(dtags, ", "))
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

// ── Publishing ───────────────────────────────────────────────────────────────

// PublishRelaySet publishes a kind:30002 relay set event.
func PublishRelaySet(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, publishRelays []string, dtag string, relays []string) (string, error) {
	pkCtx, pkCancel := context.WithTimeout(ctx, 10*time.Second)
	pk, err := keyer.GetPublicKey(pkCtx)
	pkCancel()
	if err != nil {
		return "", fmt.Errorf("publish relay set: get pubkey: %w", err)
	}
	list := nip51.NewRelaySetList(pk.Hex(), dtag, relays)
	return nip51.Publish(ctx, pool, keyer, publishRelays, list)
}
