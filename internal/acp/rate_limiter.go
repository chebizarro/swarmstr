package acp

import (
	"fmt"
	"sync"
	"time"
)

type FixedWindowRateLimiter struct {
	mu         sync.Mutex
	max        int
	window     time.Duration
	now        func() time.Time
	windowFrom time.Time
	count      int
}

func NewFixedWindowRateLimiter(max int, window time.Duration, now func() time.Time) *FixedWindowRateLimiter {
	if max <= 0 || window <= 0 {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &FixedWindowRateLimiter{max: max, window: window, now: now}
}

// NewRequiredFixedWindowRateLimiter is like NewFixedWindowRateLimiter but returns
// an error for invalid configuration instead of a nil (default-open) limiter.
//
// A nil *FixedWindowRateLimiter allows every request (see Allow). That is a
// footgun for security-sensitive call sites: passing a zero/negative max or
// window silently disables rate limiting. Such call sites must construct their
// limiter with this function so misconfiguration fails closed at startup rather
// than allowing unlimited requests at runtime.
func NewRequiredFixedWindowRateLimiter(max int, window time.Duration, now func() time.Time) (*FixedWindowRateLimiter, error) {
	if max <= 0 {
		return nil, fmt.Errorf("acp rate limiter: max must be > 0, got %d", max)
	}
	if window <= 0 {
		return nil, fmt.Errorf("acp rate limiter: window must be > 0, got %s", window)
	}
	return NewFixedWindowRateLimiter(max, window, now), nil
}

func (r *FixedWindowRateLimiter) Allow() bool {
	if r == nil {
		return true
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.windowFrom.IsZero() || now.Sub(r.windowFrom) >= r.window {
		r.windowFrom = now
		r.count = 0
	}
	if r.count >= r.max {
		return false
	}
	r.count++
	return true
}
