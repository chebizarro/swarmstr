// Package toolbuiltin nostr_watch.go — persistent Nostr subscription tools.
//
// nostr_watch creates a named subscription that delivers matching events back
// to the agent session as synthesized turns. nostr_unwatch cancels one.
// nostr_watch_list lists active subscriptions.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"swarmstr/internal/agent"
)

// maxActiveWatches is the maximum number of concurrent subscriptions per registry.
const maxActiveWatches = 10

// watchSinceJitter is subtracted from the Since timestamp when starting a watch
// subscription to capture events during the connection setup window.
const watchSinceJitter = 30 * time.Second

// watchSeenMaxSize bounds in-memory event-ID dedup state per watch.
const watchSeenMaxSize = 10_000

type watchSeenSet struct {
	items  map[string]struct{}
	ring   []string
	cursor int
	size   int
}

func newWatchSeenSet(capacity int) *watchSeenSet {
	if capacity < 1 {
		capacity = 1
	}
	return &watchSeenSet{
		items: make(map[string]struct{}, capacity),
		ring:  make([]string, capacity),
	}
}

func (s *watchSeenSet) Add(id string) (duplicate bool) {
	if _, ok := s.items[id]; ok {
		return true
	}

	if s.size < len(s.ring) {
		s.ring[s.size] = id
		s.size++
	} else {
		evicted := s.ring[s.cursor]
		delete(s.items, evicted)
		s.ring[s.cursor] = id
		s.cursor = (s.cursor + 1) % len(s.ring)
	}

	s.items[id] = struct{}{}
	return false
}

// WatchDelivery is called for each matched event.
// sessionID identifies the agent session that owns the subscription.
type WatchDelivery func(sessionID, name string, event map[string]any)

// watchEntry is a single active subscription.
type watchEntry struct {
	name      string
	sessionID string
	cancel    context.CancelFunc
	createdAt time.Time
	maxEvents int
	received  int
}

// WatchRegistry manages active named subscriptions.
type WatchRegistry struct {
	mu      sync.Mutex
	entries map[string]*watchEntry // key: name
}

// NewWatchRegistry creates an empty WatchRegistry.
func NewWatchRegistry() *WatchRegistry {
	return &WatchRegistry{entries: map[string]*watchEntry{}}
}

// start creates and registers a new watch subscription.
func (r *WatchRegistry) start(
	ctx context.Context,
	opts NostrToolOpts,
	name, sessionID string,
	filter nostr.Filter,
	relays []string,
	ttl time.Duration,
	maxEvents int,
	deliver WatchDelivery,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("watch %q already exists; unwatch first", name)
	}
	if len(r.entries) >= maxActiveWatches {
		return fmt.Errorf("maximum of %d active watches reached", maxActiveWatches)
	}

	subCtx, cancel := context.WithTimeout(ctx, ttl)
	entry := &watchEntry{
		name:      name,
		sessionID: sessionID,
		cancel:    cancel,
		createdAt: time.Now(),
		maxEvents: maxEvents,
	}
	r.entries[name] = entry

	pool, releasePool := opts.AcquirePool("watch " + name + " done")

	// Apply jitter: backdate Since to capture events during connection setup.
	if filter.Since > 0 {
		backdated := filter.Since - nostr.Timestamp(watchSinceJitter.Seconds())
		if backdated < 0 {
			backdated = 0
		}
		filter.Since = backdated
	}

	sub := pool.SubscribeMany(subCtx, relays, filter, nostr.SubscriptionOptions{})

	go func() {
		defer func() {
			cancel()
			releasePool()
			r.mu.Lock()
			delete(r.entries, name)
			r.mu.Unlock()
		}()
		seen := newWatchSeenSet(watchSeenMaxSize)
		for {
			select {
			case <-subCtx.Done():
				return
			case re, ok := <-sub:
				if !ok {
					return
				}
				evID := re.Event.ID.Hex()
				if seen.Add(evID) {
					continue
				}
				deliver(sessionID, name, eventToMap(re.Event))
				r.mu.Lock()
				entry.received++
				done := maxEvents > 0 && entry.received >= maxEvents
				r.mu.Unlock()
				if done {
					return
				}
			}
		}
	}()

	return nil
}

// stop cancels a named watch.
func (r *WatchRegistry) stop(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	if !ok {
		return fmt.Errorf("watch %q not found", name)
	}
	e.cancel()
	delete(r.entries, name)
	return nil
}

// list returns a snapshot of active watch names.
func (r *WatchRegistry) list() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, map[string]any{
			"name":       e.name,
			"session_id": e.sessionID,
			"created_at": e.createdAt.Unix(),
			"received":   e.received,
			"max_events": e.maxEvents,
		})
	}
	return out
}

// ─── nostr_watch tool ─────────────────────────────────────────────────────────

// NostrWatchTool returns an agent tool that creates a persistent named subscription.
//
// Parameters:
//   - name        string   — unique subscription label (required)
//   - filter      object   — NIP-01 filter (required)
//   - session_id  string   — session to deliver events to (defaults to current session)
//   - relays      []string — optional relay override
//   - ttl_seconds int      — max lifetime in seconds (default 3600)
//   - max_events  int      — stop after N events (default 100; 0 = unlimited)
func NostrWatchTool(opts NostrToolOpts, reg *WatchRegistry, deliver WatchDelivery) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		name, _ := args["name"].(string)
		if name == "" {
			return "", fmt.Errorf("nostr_watch: name is required")
		}
		sessionID, err := agent.ResolveSessionIDStrict(ctx, args)
		if err != nil {
			return "", fmt.Errorf("nostr_watch: %w", err)
		}
		if sessionID == "" {
			return "", fmt.Errorf("nostr_watch: session_id is required (not in args and not in context)")
		}

		ttlSec := 3600
		if v, ok := args["ttl_seconds"].(float64); ok && v > 0 {
			ttlSec = int(v)
		}
		maxEvents := 100
		if v, ok := args["max_events"].(float64); ok {
			maxEvents = int(v)
		}

		filterArg, ok := args["filter"].(map[string]any)
		if !ok {
			return "", fmt.Errorf("nostr_watch: filter is required")
		}
		f, err := buildNostrFilter(filterArg, maxEvents)
		if err != nil {
			return "", fmt.Errorf("nostr_watch: invalid filter: %w", err)
		}

		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", fmt.Errorf("nostr_watch: no relays configured")
		}

		if err := reg.start(ctx, opts, name, sessionID, f, relays,
			time.Duration(ttlSec)*time.Second, maxEvents, deliver); err != nil {
			return "", fmt.Errorf("nostr_watch: %w", err)
		}

		out, _ := json.Marshal(map[string]any{
			"watching":    true,
			"name":        name,
			"ttl_seconds": ttlSec,
			"max_events":  maxEvents,
		})
		return string(out), nil
	}
}

// ─── nostr_unwatch tool ───────────────────────────────────────────────────────

// NostrUnwatchTool cancels a named subscription.
//
// Parameters:
//   - name string — subscription to cancel (required)
func NostrUnwatchTool(reg *WatchRegistry) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		name, _ := args["name"].(string)
		if name == "" {
			return "", fmt.Errorf("nostr_unwatch: name is required")
		}
		if err := reg.stop(name); err != nil {
			return "", fmt.Errorf("nostr_unwatch: %w", err)
		}
		out, _ := json.Marshal(map[string]any{"unwatched": true, "name": name})
		return string(out), nil
	}
}

// ─── nostr_watch_list tool ────────────────────────────────────────────────────

// NostrWatchListTool returns an agent tool listing active subscriptions.
func NostrWatchListTool(reg *WatchRegistry) agent.ToolFunc {
	return func(_ context.Context, _ map[string]any) (string, error) {
		out, _ := json.Marshal(reg.list())
		return string(out), nil
	}
}
