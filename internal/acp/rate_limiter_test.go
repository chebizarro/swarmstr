package acp

import (
	"testing"
	"time"
)

func TestFixedWindowRateLimiterRejectsWhenExceeded(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := NewFixedWindowRateLimiter(2, time.Second, func() time.Time { return now })
	if !limiter.Allow() || !limiter.Allow() {
		t.Fatal("first two requests should be allowed")
	}
	if limiter.Allow() {
		t.Fatal("third request in same window should be rejected")
	}
}

func TestFixedWindowRateLimiterResetsWindow(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := NewFixedWindowRateLimiter(1, time.Second, func() time.Time { return now })
	if !limiter.Allow() {
		t.Fatal("first request should be allowed")
	}
	if limiter.Allow() {
		t.Fatal("second request in same window should be rejected")
	}
	now = now.Add(time.Second)
	if !limiter.Allow() {
		t.Fatal("request at window boundary should be allowed")
	}
}

func TestNewRequiredFixedWindowRateLimiterFailsClosed(t *testing.T) {
	if _, err := NewRequiredFixedWindowRateLimiter(0, time.Second, nil); err == nil {
		t.Fatal("zero max must error instead of returning a default-open limiter")
	}
	if _, err := NewRequiredFixedWindowRateLimiter(1, 0, nil); err == nil {
		t.Fatal("zero window must error instead of returning a default-open limiter")
	}
	lim, err := NewRequiredFixedWindowRateLimiter(1, time.Second, func() time.Time { return time.Unix(100, 0) })
	if err != nil || lim == nil {
		t.Fatalf("valid config should construct a limiter: lim=%v err=%v", lim, err)
	}
	if !lim.Allow() {
		t.Fatal("first request should be allowed")
	}
	if lim.Allow() {
		t.Fatal("second request in the same window should be rejected (enforcement is real)")
	}
}

func TestFixedWindowRateLimiterEdgeCases(t *testing.T) {
	if !NewFixedWindowRateLimiter(0, time.Second, nil).Allow() {
		t.Fatal("nil limiter from zero max should allow")
	}
	if !NewFixedWindowRateLimiter(1, 0, nil).Allow() {
		t.Fatal("nil limiter from zero window should allow")
	}
	var limiter *FixedWindowRateLimiter
	if !limiter.Allow() {
		t.Fatal("nil limiter should allow")
	}
}
