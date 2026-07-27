// Package channels — redispatch_scheduler.go ports the bounded inbound
// redispatch policy (ocn-8kh). When an inbound dispatch fails after its seen
// mark was rolled back (model timeout / signer failure / unconfirmed send), the
// event is retried on a backoff (30s / 2m / 5m), capped at 3 attempts per
// PROCESS lifetime. The give-up mark is in-memory only, so a restart retries.
package channels

import (
	"sync"
	"time"
)

// DefaultRedispatchBackoffs is the per-attempt delay schedule.
var DefaultRedispatchBackoffs = []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute}

// DefaultRedispatchMaxAttempts caps retries per process lifetime.
const DefaultRedispatchMaxAttempts = 3

// RedispatchSchedulerOptions configure a RedispatchScheduler.
type RedispatchSchedulerOptions struct {
	// Backoffs is the per-attempt delay schedule (attempt i uses Backoffs[min(i,
	// len-1)]). Default 30s / 2m / 5m.
	Backoffs []time.Duration
	// MaxAttempts caps retries per process. Default 3.
	MaxAttempts int
	// Schedule runs fn after d. Injected for tests; default time.AfterFunc.
	Schedule func(d time.Duration, fn func())
}

// RedispatchScheduler tracks per-event retry attempts and schedules bounded,
// backed-off redispatches. Safe for concurrent use.
type RedispatchScheduler struct {
	mu       sync.Mutex
	attempts map[string]int
	gaveUp   map[string]struct{}
	backoffs []time.Duration
	maxTries int
	schedule func(time.Duration, func())
}

// NewRedispatchScheduler constructs a scheduler.
func NewRedispatchScheduler(opts RedispatchSchedulerOptions) *RedispatchScheduler {
	backoffs := opts.Backoffs
	if len(backoffs) == 0 {
		backoffs = DefaultRedispatchBackoffs
	}
	maxTries := opts.MaxAttempts
	if maxTries <= 0 {
		maxTries = DefaultRedispatchMaxAttempts
	}
	sched := opts.Schedule
	if sched == nil {
		sched = func(d time.Duration, fn func()) { time.AfterFunc(d, fn) }
	}
	return &RedispatchScheduler{
		attempts: map[string]int{},
		gaveUp:   map[string]struct{}{},
		backoffs: backoffs,
		maxTries: maxTries,
		schedule: sched,
	}
}

// Schedule registers a redispatch attempt for eventKey after a backoff delay.
// It returns (delay, true) when a retry was scheduled, or (0, false) when the
// event has exhausted its attempts and is given up (in-memory, until restart).
// redispatch is invoked after the delay.
func (r *RedispatchScheduler) Schedule(eventKey string, redispatch func()) (time.Duration, bool) {
	r.mu.Lock()
	if _, done := r.gaveUp[eventKey]; done {
		r.mu.Unlock()
		return 0, false
	}
	n := r.attempts[eventKey]
	if n >= r.maxTries {
		r.gaveUp[eventKey] = struct{}{}
		delete(r.attempts, eventKey)
		r.mu.Unlock()
		return 0, false
	}
	idx := n
	if idx >= len(r.backoffs) {
		idx = len(r.backoffs) - 1
	}
	delay := r.backoffs[idx]
	r.attempts[eventKey] = n + 1
	r.mu.Unlock()

	r.schedule(delay, redispatch)
	return delay, true
}

// Succeeded clears the retry state for eventKey (called when a dispatch finally
// confirms delivery, so a later unrelated failure starts fresh).
func (r *RedispatchScheduler) Succeeded(eventKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.attempts, eventKey)
	delete(r.gaveUp, eventKey)
}

// Attempts returns the number of retries already scheduled for eventKey (tests).
func (r *RedispatchScheduler) Attempts(eventKey string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts[eventKey]
}

// GaveUp reports whether eventKey has exhausted its process-lifetime attempts.
func (r *RedispatchScheduler) GaveUp(eventKey string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, done := r.gaveUp[eventKey]
	return done
}
