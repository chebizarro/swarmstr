package runtime

import (
	"sort"
	"sync"
	"time"
)

type relayHealthEntry struct {
	score       int
	consecutive int
	lastFailure time.Time
	lastSuccess time.Time
}

// RelayHealthTracker tracks relay reliability and applies temporary cool-down
// after repeated failures.
type RelayHealthTracker struct {
	mu          sync.RWMutex
	entries     map[string]*relayHealthEntry
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

func NewRelayHealthTracker() *RelayHealthTracker {
	return &RelayHealthTracker{
		entries:     map[string]*relayHealthEntry{},
		baseBackoff: 200 * time.Millisecond,
		maxBackoff:  5 * time.Second,
	}
}

func (t *RelayHealthTracker) Seed(relays []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	keep := make(map[string]struct{}, len(relays))
	for _, relay := range relays {
		if relay == "" {
			continue
		}
		keep[relay] = struct{}{}
		if _, ok := t.entries[relay]; !ok {
			t.entries[relay] = &relayHealthEntry{}
		}
	}
	// Prune relays that are no longer present.
	for relay := range t.entries {
		if _, ok := keep[relay]; !ok {
			delete(t.entries, relay)
		}
	}
}

func (t *RelayHealthTracker) RecordSuccess(relay string) {
	if relay == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entryLocked(relay)
	e.consecutive = 0
	e.lastSuccess = time.Now()
	if e.score < 1000 {
		e.score += 2
	}
}

func (t *RelayHealthTracker) RecordFailure(relay string) {
	if relay == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entryLocked(relay)
	e.consecutive++
	e.lastFailure = time.Now()
	if e.score > -1000 {
		e.score -= 3
	}
}

func (t *RelayHealthTracker) Candidates(relays []string, now time.Time) []string {
	ordered := t.SortRelays(relays)
	out := make([]string, 0, len(ordered))
	for _, relay := range ordered {
		if t.Allowed(relay, now) {
			out = append(out, relay)
		}
	}
	if len(out) == 0 {
		return ordered
	}
	return out
}

func (t *RelayHealthTracker) Allowed(relay string, now time.Time) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.entries[relay]
	if !ok || e.consecutive < 2 {
		return true
	}
	backoff := t.baseBackoff << minInt(e.consecutive-2, 6)
	if backoff > t.maxBackoff {
		backoff = t.maxBackoff
	}
	return now.Sub(e.lastFailure) >= backoff
}

// NextAllowedIn returns how long until the relay will be allowed again.
// Returns 0 if the relay is already allowed.
func (t *RelayHealthTracker) NextAllowedIn(relay string, now time.Time) time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.entries[relay]
	if !ok || e.consecutive < 2 {
		return 0
	}
	backoff := t.baseBackoff << minInt(e.consecutive-2, 6)
	if backoff > t.maxBackoff {
		backoff = t.maxBackoff
	}
	remaining := backoff - now.Sub(e.lastFailure)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func (t *RelayHealthTracker) SortRelays(relays []string) []string {
	out := make([]string, len(relays))
	copy(out, relays)
	t.mu.RLock()
	defer t.mu.RUnlock()
	sort.SliceStable(out, func(i, j int) bool {
		ei := t.entries[out[i]]
		ej := t.entries[out[j]]
		scoreI, scoreJ := 0, 0
		if ei != nil {
			scoreI = ei.score
		}
		if ej != nil {
			scoreJ = ej.score
		}
		if scoreI == scoreJ {
			return out[i] < out[j]
		}
		return scoreI > scoreJ
	})
	return out
}

func (t *RelayHealthTracker) entryLocked(relay string) *relayHealthEntry {
	e := t.entries[relay]
	if e == nil {
		e = &relayHealthEntry{}
		t.entries[relay] = e
	}
	return e
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
