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
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	nostruntime "metiq/internal/nostr/runtime"
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

// WatchSpec is a JSON-serializable snapshot of a watch subscription.
// It captures everything needed to restart the watch after a daemon restart.
type WatchSpec struct {
	Name      string         `json:"name"`
	SessionID string         `json:"session_id"`
	FilterRaw map[string]any `json:"filter"` // original args for buildNostrFilter
	Relays    []string       `json:"relays"`
	ExplicitRelays bool      `json:"explicit_relays,omitempty"`
	TTLSec    int            `json:"ttl_seconds"`
	MaxEvents int            `json:"max_events"`
	Received  int            `json:"received"`
	CreatedAt int64          `json:"created_at"` // unix seconds
	Deadline  int64          `json:"deadline"`   // unix seconds, when TTL expires
}

// watchEntry is a single active subscription.
type watchEntry struct {
	name      string
	sessionID string
	cancel    context.CancelFunc
	done      chan struct{}
	createdAt time.Time
	mu        sync.Mutex
	maxEvents int
	received  int
	filterRaw map[string]any // original filter args for persistence
	relays    []string       // resolved relays for persistence
	relaysMu  sync.RWMutex   // protects relays during rebind
	explicitRelays bool      // true if the watch was created with explicit relays
	rebindCh  chan struct{}  // signals the watch loop to restart with new relays
	ttlSec    int            // original TTL for persistence
	deadline  time.Time      // absolute expiry
}

// WatchRegistry manages active named subscriptions.
type WatchRegistry struct {
	mu      sync.Mutex
	entries map[string]*watchEntry // key: name
	stopping map[string]chan struct{} // key: name -> done channel
	hubFunc func() *nostruntime.NostrHub
}

// NewWatchRegistry creates an empty WatchRegistry.
func NewWatchRegistry() *WatchRegistry {
	return &WatchRegistry{entries: map[string]*watchEntry{}, stopping: map[string]chan struct{}{}}
}

// SetHubFunc sets the hub provider so watches can use managed subscriptions.
func (r *WatchRegistry) SetHubFunc(fn func() *nostruntime.NostrHub) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hubFunc = fn
}

func (r *WatchRegistry) hub() *nostruntime.NostrHub {
	if r.hubFunc != nil {
		return r.hubFunc()
	}
	return nil
}

// start creates and registers a new watch subscription.
// filterRaw is the original filter args map for persistence/restore.
func (r *WatchRegistry) start(
	ctx context.Context,
	opts NostrToolOpts,
	name, sessionID string,
	filter nostr.Filter,
	filterRaw map[string]any,
	relays []string,
	explicitRelays bool,
	ttl time.Duration,
	maxEvents int,
	deliver WatchDelivery,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("watch %q already exists; unwatch first", name)
	}
	if _, stopping := r.stopping[name]; stopping {
		return fmt.Errorf("watch %q is stopping; try again shortly", name)
	}
	if len(r.entries) >= maxActiveWatches {
		return fmt.Errorf("maximum of %d active watches reached", maxActiveWatches)
	}

	now := time.Now()
	subCtx, cancel := context.WithTimeout(ctx, ttl)
	entry := &watchEntry{
		name:      name,
		sessionID: sessionID,
		cancel:    cancel,
		done:      make(chan struct{}),
		createdAt: now,
		maxEvents: maxEvents,
		filterRaw: filterRaw,
		relays:    relays,
		explicitRelays: explicitRelays,
		rebindCh:  make(chan struct{}, 1),
		ttlSec:    int(ttl.Seconds()),
		deadline:  now.Add(ttl),
	}
	r.entries[name] = entry

	filter = applyWatchSinceJitter(filter)

	go func() {
		pool, releasePool := opts.AcquirePool("watch " + name + " done")
		defer func() {
			close(entry.done)
			cancel()
			releasePool()
			r.mu.Lock()
			delete(r.entries, name)
			if ch, ok := r.stopping[name]; ok && ch == entry.done {
				delete(r.stopping, name)
			}
			r.mu.Unlock()
		}()
		seen := newWatchSeenSet(watchSeenMaxSize)
		eventHandler := func(re nostr.RelayEvent) {
			evID := re.Event.ID.Hex()
			if seen.Add(evID) {
				return
			}
			deliver(sessionID, name, eventToMap(re.Event))
			entry.mu.Lock()
			entry.received++
			done := maxEvents > 0 && entry.received >= maxEvents
			entry.mu.Unlock()
			if done {
				cancel()
			}
		}

		r.runWatchLoop(subCtx, pool, entry, filter, relays, eventHandler)
	}()

	return nil
}

// stop cancels a named watch.
func (r *WatchRegistry) stop(name string) error {
	r.mu.Lock()
	e, ok := r.entries[name]
	if !ok {
		if _, stopping := r.stopping[name]; stopping {
			r.mu.Unlock()
			return fmt.Errorf("watch %q is stopping", name)
		}
		r.mu.Unlock()
		return fmt.Errorf("watch %q not found", name)
	}
	delete(r.entries, name)
	r.stopping[name] = e.done
	r.mu.Unlock()

	e.cancel()

	select {
	case <-e.done:
	case <-time.After(5 * time.Second):
	}

	r.mu.Lock()
	if ch, ok := r.stopping[name]; ok && ch == e.done {
		delete(r.stopping, name)
	}
	r.mu.Unlock()
	return nil
}

// list returns a snapshot of active watch names.
func (r *WatchRegistry) list() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, 0, len(r.entries))
	for _, e := range r.entries {
		e.mu.Lock()
		received := e.received
		maxEvents := e.maxEvents
		createdAt := e.createdAt.Unix()
		sessionID := e.sessionID
		name := e.name
		e.mu.Unlock()
		out = append(out, map[string]any{
			"name":       name,
			"session_id": sessionID,
			"created_at": createdAt,
			"received":   received,
			"max_events": maxEvents,
		})
	}
	return out
}

// Specs returns a serializable snapshot of all active watches for persistence.
func (r *WatchRegistry) Specs() []WatchSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]WatchSpec, 0, len(r.entries))
	for _, e := range r.entries {
		e.relaysMu.RLock()
		relays := make([]string, len(e.relays))
		copy(relays, e.relays)
		e.relaysMu.RUnlock()
		e.mu.Lock()
		received := e.received
		maxEvents := e.maxEvents
		createdAt := e.createdAt.Unix()
		deadline := e.deadline.Unix()
		sessionID := e.sessionID
		name := e.name
		filterRaw := e.filterRaw
		ttlSec := e.ttlSec
		explicit := e.explicitRelays
		e.mu.Unlock()
		out = append(out, WatchSpec{
			Name:      name,
			SessionID: sessionID,
			FilterRaw: filterRaw,
			Relays:    relays,
			ExplicitRelays: explicit,
			TTLSec:    ttlSec,
			MaxEvents: maxEvents,
			Received:  received,
			CreatedAt: createdAt,
			Deadline:  deadline,
		})
	}
	return out
}

// Restore re-creates watches from persisted specs.  Watches whose deadline has
// already passed are silently skipped.  The Since timestamp in each filter is
// set to "now − jitter" so that events arriving during the restart gap are
// captured (duplicates are handled by the per-watch dedup set).
func (r *WatchRegistry) Restore(
	ctx context.Context,
	opts NostrToolOpts,
	specs []WatchSpec,
	deliver WatchDelivery,
) (restored int) {
	now := time.Now()
	for _, spec := range specs {
		remaining := time.Until(time.Unix(spec.Deadline, 0))
		if remaining <= 0 {
			continue // expired while we were down
		}
		remainingMax := spec.MaxEvents - spec.Received
		if spec.MaxEvents > 0 && remainingMax <= 0 {
			continue // already hit event limit
		}
		// Rebuild the nostr.Filter from the original args.
		f, err := buildNostrFilter(spec.FilterRaw, remainingMax)
		if err != nil {
			continue
		}
		// Override Since to now so we pick up from the restart point.
		f.Since = nostr.Timestamp(now.Unix())

		// Store persistence metadata so a subsequent Save captures this entry.
		if err := r.startRestored(ctx, opts, spec, f, remaining, deliver); err != nil {
			continue
		}
		restored++
	}
	return restored
}

// startRestored is like start but pre-populates persistence fields from a spec.
func (r *WatchRegistry) startRestored(
	ctx context.Context,
	opts NostrToolOpts,
	spec WatchSpec,
	filter nostr.Filter,
	ttl time.Duration,
	deliver WatchDelivery,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.entries[spec.Name]; exists {
		return fmt.Errorf("watch %q already exists", spec.Name)
	}
	if len(r.entries) >= maxActiveWatches {
		return fmt.Errorf("max watches reached")
	}

	subCtx, cancel := context.WithTimeout(ctx, ttl)
	entry := &watchEntry{
		name:      spec.Name,
		sessionID: spec.SessionID,
		cancel:    cancel,
		done:      make(chan struct{}),
		createdAt: time.Unix(spec.CreatedAt, 0),
		maxEvents: spec.MaxEvents,
		received:  spec.Received,
		filterRaw: spec.FilterRaw,
		relays:    spec.Relays,
		explicitRelays: spec.ExplicitRelays,
		rebindCh:  make(chan struct{}, 1),
		ttlSec:    spec.TTLSec,
		deadline:  time.Unix(spec.Deadline, 0),
	}
	r.entries[spec.Name] = entry

	filter = applyWatchSinceJitter(filter)

	go func() {
		pool, releasePool := opts.AcquirePool("watch " + spec.Name + " done")
		defer func() {
			close(entry.done)
			cancel()
			releasePool()
			r.mu.Lock()
			delete(r.entries, spec.Name)
			if ch, ok := r.stopping[spec.Name]; ok && ch == entry.done {
				delete(r.stopping, spec.Name)
			}
			r.mu.Unlock()
		}()
		seen := newWatchSeenSet(watchSeenMaxSize)
		eventHandler := func(re nostr.RelayEvent) {
			evID := re.Event.ID.Hex()
			if seen.Add(evID) {
				return
			}
			deliver(spec.SessionID, spec.Name, eventToMap(re.Event))
			entry.mu.Lock()
			entry.received++
			done := spec.MaxEvents > 0 && entry.received >= spec.MaxEvents
			entry.mu.Unlock()
			if done {
				cancel()
			}
		}

		r.runWatchLoop(subCtx, pool, entry, filter, spec.Relays, eventHandler)
	}()

	return nil
}

// ─── Subscription lifecycle ───────────────────────────────────────────────────

func applyWatchSinceJitter(filter nostr.Filter) nostr.Filter {
	if filter.Since > 0 {
		backdated := filter.Since - nostr.Timestamp(watchSinceJitter.Seconds())
		if backdated < 0 {
			backdated = 0
		}
		filter.Since = backdated
	}
	return filter
}

// runWatchLoop runs the subscription for a single watch entry, retrying on
// stream close until the context (TTL) expires. When the registry has a hub,
// it uses managed subscriptions with CLOSED visibility; otherwise it falls
// back to raw pool.SubscribeMany.
func (r *WatchRegistry) runWatchLoop(
	ctx context.Context,
	pool *nostr.Pool,
	entry *watchEntry,
	filter nostr.Filter,
	relays []string,
	onEvent func(nostr.RelayEvent),
) {
	hub := r.hub()
	currentRelays := relays
	for {
		if ctx.Err() != nil {
			return
		}
		// Check if relays were updated by RebindRelays.
		entry.relaysMu.RLock()
		currentRelays = make([]string, len(entry.relays))
		copy(currentRelays, entry.relays)
		entry.relaysMu.RUnlock()

		var restarted bool
		if hub != nil {
			restarted = r.runHubWatch(ctx, hub, entry, filter, currentRelays, onEvent)
		} else {
			restarted = r.runRawWatch(ctx, pool, entry, filter, currentRelays, onEvent)
		}
		if ctx.Err() != nil {
			return
		}
		// Update since for retry: pick up from now minus jitter.
		filter.Since = nostr.Timestamp(time.Now().Unix())
		filter = applyWatchSinceJitter(filter)
		if !restarted {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

func (r *WatchRegistry) runRawWatch(
	ctx context.Context,
	pool *nostr.Pool,
	entry *watchEntry,
	filter nostr.Filter,
	relays []string,
	onEvent func(nostr.RelayEvent),
) bool {
	cycleCtx, cycleCancel := context.WithCancel(ctx)
	defer cycleCancel()
	sub := pool.SubscribeMany(cycleCtx, relays, filter, nostr.SubscriptionOptions{})
	for {
		select {
		case <-ctx.Done():
			return true
		case <-entry.rebindCh:
			return true // rebind requested, restart with new relays
		case re, ok := <-sub:
			if !ok {
				return false // stream closed, retry
			}
			onEvent(re)
			if ctx.Err() != nil {
				return true
			}
		}
	}
}

func (r *WatchRegistry) runHubWatch(
	ctx context.Context,
	hub *nostruntime.NostrHub,
	entry *watchEntry,
	filter nostr.Filter,
	relays []string,
	onEvent func(nostr.RelayEvent),
) bool {
	subID := fmt.Sprintf("watch:%s", strings.TrimSpace(entry.name))
	closedCh := make(chan string, 4)

	ms, err := hub.Subscribe(ctx, nostruntime.SubOpts{
		ID:      subID,
		Filter:  filter,
		Relays:  relays,
		OnEvent: onEvent,
		OnClosed: func(relay *nostr.Relay, reason string, handledAuth bool) {
			if handledAuth {
				return
			}
			select {
			case closedCh <- reason:
			default:
			}
		},
	})
	if err != nil {
		return false // failed to subscribe, caller will retry
	}
	_ = ms

	select {
	case <-ctx.Done():
		hub.Unsubscribe(subID)
		return true
	case <-entry.rebindCh:
		hub.Unsubscribe(subID)
		return true // rebind requested, restart with new relays
	case <-closedCh:
		hub.Unsubscribe(subID)
		return false // closed by relay, retry
	}
}

// RebindRelays updates the default relay list for all active watches and
// restarts their subscriptions with the new relay set. Watches that specified
// explicit relays at creation time are NOT rebindn — they keep their original
// relay list.
func (r *WatchRegistry) RebindRelays(newRelays []string) {
	r.mu.Lock()
	for _, e := range r.entries {
		e.mu.Lock()
		explicit := e.explicitRelays
		e.mu.Unlock()
		if explicit {
			continue
		}
		e.relaysMu.Lock()
		e.relays = newRelays
		e.relaysMu.Unlock()
		select {
		case e.rebindCh <- struct{}{}:
		default:
		}
	}
	r.mu.Unlock()
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

		explicitRelays := len(toStringSlice(args["relays"])) > 0
		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", fmt.Errorf("nostr_watch: no relays configured")
		}

		if err := reg.start(ctx, opts, name, sessionID, f, filterArg, relays, explicitRelays,
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
