package acp

import (
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
