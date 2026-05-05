package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
)

// RelayHealthResult captures the result of probing a single relay.
type RelayHealthResult struct {
	URL       string
	Reachable bool
	Latency   time.Duration
	Err       error
}

type relayHealthState struct {
	failures      int
	cooldownUntil time.Time
}

// RelayHealthTracker tracks per-relay degradation for retry ordering/backoff.
type RelayHealthTracker struct {
	mu     sync.Mutex
	relays map[string]*relayHealthState
}

const (
	relayFailureCooldownThreshold = 5
	relayFailureCooldownWindow    = 2 * time.Minute
)

// NewRelayHealthTracker constructs a per-relay health tracker.
func NewRelayHealthTracker() *RelayHealthTracker {
	return &RelayHealthTracker{relays: map[string]*relayHealthState{}}
}

// Seed registers the current relay set and prunes entries for removed relays.
func (t *RelayHealthTracker) Seed(relays []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	next := make(map[string]*relayHealthState, len(relays))
	for _, relay := range normalizeRelayURLs(relays) {
		if st, ok := t.relays[relay]; ok {
			next[relay] = st
			continue
		}
		next[relay] = &relayHealthState{}
	}
	t.relays = next
}

// RecordFailure increments the relay's failure count and applies cooldown once
// the relay crosses the failure threshold.
func (t *RelayHealthTracker) RecordFailure(relay string) {
	relay = strings.TrimSpace(relay)
	if relay == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.ensure(relay)
	st.failures++
	if st.failures >= relayFailureCooldownThreshold {
		st.cooldownUntil = time.Now().Add(relayFailureCooldownWindow)
	}
}

// RecordSuccess clears any accumulated degradation for the relay.
func (t *RelayHealthTracker) RecordSuccess(relay string) {
	relay = strings.TrimSpace(relay)
	if relay == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.ensure(relay)
	st.failures = 0
	st.cooldownUntil = time.Time{}
}

// Allowed reports whether the relay may be retried at the given time.
func (t *RelayHealthTracker) Allowed(relay string, now time.Time) bool {
	relay = strings.TrimSpace(relay)
	if relay == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	st, ok := t.relays[relay]
	if !ok {
		return true
	}
	if st.cooldownUntil.IsZero() {
		return true
	}
	return !now.Before(st.cooldownUntil)
}

// NextAllowedIn reports how long remains before the relay may be retried.
func (t *RelayHealthTracker) NextAllowedIn(relay string, now time.Time) time.Duration {
	relay = strings.TrimSpace(relay)
	if relay == "" {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	st, ok := t.relays[relay]
	if !ok || st.cooldownUntil.IsZero() || !now.Before(st.cooldownUntil) {
		return 0
	}
	return st.cooldownUntil.Sub(now)
}

// SortRelays orders relays best-first, pushing degraded relays to the end.
func (t *RelayHealthTracker) SortRelays(relays []string) []string {
	type relayOrder struct {
		url      string
		failures int
		blocked  bool
		index    int
	}
	now := time.Now()
	list := normalizeRelayURLs(relays)
	ordered := make([]relayOrder, 0, len(list))

	t.mu.Lock()
	for idx, relay := range list {
		st, ok := t.relays[relay]
		item := relayOrder{url: relay, index: idx}
		if ok {
			item.failures = st.failures
			item.blocked = !st.cooldownUntil.IsZero() && now.Before(st.cooldownUntil)
		}
		ordered = append(ordered, item)
	}
	t.mu.Unlock()

	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].blocked != ordered[j].blocked {
			return !ordered[i].blocked
		}
		if ordered[i].failures != ordered[j].failures {
			return ordered[i].failures < ordered[j].failures
		}
		return ordered[i].index < ordered[j].index
	})

	out := make([]string, 0, len(ordered))
	for _, item := range ordered {
		out = append(out, item.url)
	}
	return out
}

// Candidates returns currently allowed relays ordered best-first. If every
// relay is cooled down, it falls back to the full sorted list so callers still
// have a last-resort relay to try.
func (t *RelayHealthTracker) Candidates(relays []string, now time.Time) []string {
	sortedRelays := t.SortRelays(relays)
	candidates := make([]string, 0, len(sortedRelays))
	for _, relay := range sortedRelays {
		if t.Allowed(relay, now) {
			candidates = append(candidates, relay)
		}
	}
	if len(candidates) == 0 {
		return sortedRelays
	}
	return candidates
}

// RelayHealthProbe probes a relay and reports whether it is reachable.
type RelayHealthProbe func(ctx context.Context, relayURL string) RelayHealthResult

// RelayHealthMonitorOptions configures RelayHealthMonitor behavior.
type RelayHealthMonitorOptions struct {
	Timeout   time.Duration
	Interval  time.Duration
	Probe     RelayHealthProbe
	OnResults func(initial bool, results []RelayHealthResult)
}

// RelayHealthMonitor periodically probes the currently configured relay set.
type RelayHealthMonitor struct {
	mu        sync.RWMutex
	relays    []string
	timeout   time.Duration
	interval  time.Duration
	probe     RelayHealthProbe
	onResults func(initial bool, results []RelayHealthResult)
	triggerCh chan struct{}
	started   bool
}

// NewRelayHealthMonitor constructs a new monitor for the provided relay URLs.
func NewRelayHealthMonitor(relays []string, opts RelayHealthMonitorOptions) *RelayHealthMonitor {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	probe := opts.Probe
	if probe == nil {
		probe = ProbeRelayREQ
	}
	return &RelayHealthMonitor{
		relays:    normalizeRelayURLs(relays),
		timeout:   timeout,
		interval:  opts.Interval,
		probe:     probe,
		onResults: opts.OnResults,
		triggerCh: make(chan struct{}, 1),
	}
}

// UpdateRelays replaces the monitored relay set.
func (m *RelayHealthMonitor) UpdateRelays(relays []string) {
	m.mu.Lock()
	m.relays = normalizeRelayURLs(relays)
	m.mu.Unlock()
}

// RunOnce executes a single probe pass against the current relay set.
func (m *RelayHealthMonitor) RunOnce(ctx context.Context) []RelayHealthResult {
	return m.run(ctx, false)
}

// Trigger schedules an immediate background probe on the monitor's worker.
func (m *RelayHealthMonitor) Trigger() {
	select {
	case m.triggerCh <- struct{}{}:
	default:
	}
}

// Start launches the monitor in the background. It performs an immediate probe
// pass, then repeats at the configured interval if interval > 0.
func (m *RelayHealthMonitor) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()
	go func() {
		m.run(ctx, true)
		var ticker *time.Ticker
		var tickCh <-chan time.Time
		if m.interval > 0 {
			ticker = time.NewTicker(m.interval)
			tickCh = ticker.C
			defer ticker.Stop()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-tickCh:
				m.run(ctx, false)
			case <-m.triggerCh:
				m.run(ctx, false)
			}
		}
	}()
}

func (m *RelayHealthMonitor) run(parent context.Context, initial bool) []RelayHealthResult {
	relays := m.snapshotRelays()
	results := make([]RelayHealthResult, len(relays))
	if len(relays) == 0 {
		if m.onResults != nil {
			m.onResults(initial, results)
		}
		return results
	}

	var wg sync.WaitGroup
	for i, relayURL := range relays {
		wg.Add(1)
		go func(idx int, url string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(parent, m.timeout)
			defer cancel()
			results[idx] = m.probe(ctx, url)
		}(i, relayURL)
	}
	wg.Wait()

	if m.onResults != nil {
		m.onResults(initial, results)
	}
	return results
}

func (m *RelayHealthMonitor) snapshotRelays() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string{}, m.relays...)
}

// ProbeRelayREQ verifies that a relay accepts a websocket connection and
// responds to a lightweight REQ subscription.
func ProbeRelayREQ(ctx context.Context, relayURL string) RelayHealthResult {
	relayURL = strings.TrimSpace(relayURL)
	start := time.Now()
	fail := func(err error) RelayHealthResult {
		return RelayHealthResult{
			URL:       relayURL,
			Reachable: false,
			Latency:   time.Since(start),
			Err:       err,
		}
	}

	if relayURL == "" {
		return fail(fmt.Errorf("relay URL is empty"))
	}

	relay, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{})
	if err != nil {
		return fail(err)
	}
	defer relay.Close()

	maxWaitForEOSE := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline) - 250*time.Millisecond; remaining > 0 && remaining < maxWaitForEOSE {
			maxWaitForEOSE = remaining
		}
	}

	sub, err := relay.Subscribe(ctx, nostr.Filter{
		Kinds: []nostr.Kind{1},
		Since: nostr.Timestamp(time.Now().Unix()),
		Limit: 1,
	}, nostr.SubscriptionOptions{
		Label:          "relay-health",
		MaxWaitForEOSE: maxWaitForEOSE,
	})
	if err != nil {
		return fail(fmt.Errorf("relay subscription failed: %w", err))
	}
	defer sub.Unsub()

	for {
		select {
		case _, ok := <-sub.Events:
			if !ok {
				return fail(fmt.Errorf("relay health-check event stream ended before response"))
			}
			return RelayHealthResult{URL: relayURL, Reachable: true, Latency: time.Since(start)}
		case <-sub.EndOfStoredEvents:
			return RelayHealthResult{URL: relayURL, Reachable: true, Latency: time.Since(start)}
		case reason := <-sub.ClosedReason:
			reason = strings.TrimSpace(reason)
			if reason == "" {
				return fail(fmt.Errorf("relay closed health-check subscription"))
			}
			return fail(fmt.Errorf("relay closed health-check subscription: %s", reason))
		case <-sub.Context.Done():
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			if err := relay.ConnectionError; err != nil {
				return fail(err)
			}
			return fail(fmt.Errorf("relay health-check subscription ended before response"))
		}
	}
}

func normalizeRelayURLs(relays []string) []string {
	seen := make(map[string]struct{}, len(relays))
	out := make([]string, 0, len(relays))
	for _, relay := range relays {
		relay = strings.TrimSpace(relay)
		if relay == "" {
			continue
		}
		if _, ok := seen[relay]; ok {
			continue
		}
		seen[relay] = struct{}{}
		out = append(out, relay)
	}
	return out
}

func (t *RelayHealthTracker) ensure(relay string) *relayHealthState {
	if t.relays == nil {
		t.relays = map[string]*relayHealthState{}
	}
	st, ok := t.relays[relay]
	if !ok {
		st = &relayHealthState{}
		t.relays[relay] = st
	}
	return st
}
