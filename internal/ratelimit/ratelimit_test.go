package ratelimit

import (
	"testing"
	"time"
)

func TestBucket_Allow(t *testing.T) {
	b := newBucket(3, 10) // burst=3, rate=10/s (fast refill for tests)
	if !b.Allow() {
		t.Fatal("first allow should succeed")
	}
	if !b.Allow() {
		t.Fatal("second allow should succeed")
	}
	if !b.Allow() {
		t.Fatal("third allow should succeed (burst=3)")
	}
	if b.Allow() {
		t.Fatal("fourth allow should be rate-limited")
	}
}

func TestBucket_Refill(t *testing.T) {
	b := newBucket(1, 100) // very fast refill: 100 tokens/s
	b.Allow()              // drain the single token
	if b.Allow() {
		t.Fatal("should be empty after draining")
	}
	time.Sleep(15 * time.Millisecond) // ~1.5 tokens refilled
	if !b.Allow() {
		t.Fatal("should have refilled after wait")
	}
}

func TestLimiter_Allow(t *testing.T) {
	l := NewLimiter(Config{Burst: 2, Rate: 0.001, Enabled: true})
	if !l.Allow("alice") {
		t.Fatal("alice first should be allowed")
	}
	if !l.Allow("alice") {
		t.Fatal("alice second (burst=2) should be allowed")
	}
	if l.Allow("alice") {
		t.Fatal("alice third should be rate-limited")
	}
	// bob has his own bucket
	if !l.Allow("bob") {
		t.Fatal("bob should be allowed independently")
	}
}

func TestLimiter_Disabled(t *testing.T) {
	l := NewLimiter(Config{Burst: 1, Rate: 0.001, Enabled: false})
	for i := 0; i < 100; i++ {
		if !l.Allow("key") {
			t.Fatalf("disabled limiter should always allow (iteration %d)", i)
		}
	}
}

func TestLimiter_Reset(t *testing.T) {
	l := NewLimiter(Config{Burst: 1, Rate: 0.001, Enabled: true})
	l.Allow("key") // drain
	if l.Allow("key") {
		t.Fatal("should be limited before reset")
	}
	l.Reset("key")
	if !l.Allow("key") {
		t.Fatal("should be allowed after reset")
	}
}

func TestLimiter_Prune(t *testing.T) {
	l := NewLimiter(Config{Burst: 5, Rate: 100, Enabled: true})
	l.Allow("key1")
	time.Sleep(60 * time.Millisecond) // enough to refill fully at rate=100/s
	l.Prune()
	if l.Size() != 0 {
		t.Fatalf("expected 0 buckets after prune, got %d", l.Size())
	}
}

func TestMultiLimiter_Allow(t *testing.T) {
	ml := NewMultiLimiter(
		Config{Burst: 1, Rate: 0.001, Enabled: true},
		Config{Burst: 10, Rate: 100, Enabled: true},
	)
	// first request: user has 1 token, channel has 10
	if !ml.Allow("user1", "channel1") {
		t.Fatal("first request should be allowed")
	}
	// second request: user is drained
	if ml.Allow("user1", "channel1") {
		t.Fatal("second request should be blocked by user limiter")
	}
	// different user, same channel: allowed
	if !ml.Allow("user2", "channel1") {
		t.Fatal("user2 should have own bucket")
	}
}
