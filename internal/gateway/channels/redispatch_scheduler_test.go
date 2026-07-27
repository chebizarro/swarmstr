package channels

import (
	"sync"
	"testing"
	"time"
)

// capturingScheduler records scheduled delays and lets the test fire them.
type capturingScheduler struct {
	mu     sync.Mutex
	delays []time.Duration
	fns    []func()
}

func (c *capturingScheduler) schedule(d time.Duration, fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delays = append(c.delays, d)
	c.fns = append(c.fns, fn)
}

func (c *capturingScheduler) fireAll() {
	c.mu.Lock()
	fns := append([]func(){}, c.fns...)
	c.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func TestRedispatch_BackoffScheduleAndCap(t *testing.T) {
	cap := &capturingScheduler{}
	r := NewRedispatchScheduler(RedispatchSchedulerOptions{Schedule: cap.schedule})
	key := "room|evt-1"

	// Three retries at 30s / 2m / 5m.
	wantDelays := []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute}
	for i, want := range wantDelays {
		delay, ok := r.Schedule(key, func() {})
		if !ok {
			t.Fatalf("attempt %d should schedule", i+1)
		}
		if delay != want {
			t.Errorf("attempt %d delay = %v, want %v", i+1, delay, want)
		}
	}
	// 4th attempt exceeds the cap -> give up.
	if _, ok := r.Schedule(key, func() { t.Error("must not run after give-up") }); ok {
		t.Error("4th attempt must be given up")
	}
	if !r.GaveUp(key) {
		t.Error("event should be marked given-up")
	}
	// Further attempts stay given up.
	if _, ok := r.Schedule(key, func() {}); ok {
		t.Error("given-up event must not reschedule")
	}
}

func TestRedispatch_FiresRedispatch(t *testing.T) {
	cap := &capturingScheduler{}
	r := NewRedispatchScheduler(RedispatchSchedulerOptions{Schedule: cap.schedule})
	var ran int
	r.Schedule("k", func() { ran++ })
	if ran != 0 {
		t.Fatal("redispatch must not run before the delay fires")
	}
	cap.fireAll()
	if ran != 1 {
		t.Errorf("redispatch ran %d times, want 1", ran)
	}
}

func TestRedispatch_SucceededResets(t *testing.T) {
	cap := &capturingScheduler{}
	r := NewRedispatchScheduler(RedispatchSchedulerOptions{Schedule: cap.schedule})
	key := "k"
	r.Schedule(key, func() {})
	r.Schedule(key, func() {})
	if r.Attempts(key) != 2 {
		t.Fatalf("attempts = %d, want 2", r.Attempts(key))
	}
	// A confirmed delivery clears the retry state.
	r.Succeeded(key)
	if r.Attempts(key) != 0 || r.GaveUp(key) {
		t.Error("Succeeded must clear retry state")
	}
	// Next failure starts fresh at the first backoff.
	delay, ok := r.Schedule(key, func() {})
	if !ok || delay != 30*time.Second {
		t.Errorf("post-success schedule = (%v,%v), want (30s,true)", delay, ok)
	}
}

func TestRedispatch_DistinctEventsIndependent(t *testing.T) {
	cap := &capturingScheduler{}
	r := NewRedispatchScheduler(RedispatchSchedulerOptions{Schedule: cap.schedule})
	for i := 0; i < 3; i++ {
		r.Schedule("evt-a", func() {})
	}
	if !r.GaveUp("evt-a") {
		// a is at cap after 3
	}
	// evt-b is unaffected by evt-a's exhaustion.
	if delay, ok := r.Schedule("evt-b", func() {}); !ok || delay != 30*time.Second {
		t.Errorf("distinct event should schedule fresh, got (%v,%v)", delay, ok)
	}
}

func TestRedispatch_CustomBackoffsAndCap(t *testing.T) {
	cap := &capturingScheduler{}
	r := NewRedispatchScheduler(RedispatchSchedulerOptions{
		Backoffs:    []time.Duration{time.Second},
		MaxAttempts: 2,
		Schedule:    cap.schedule,
	})
	// Single backoff value is reused for every attempt.
	if d, ok := r.Schedule("k", func() {}); !ok || d != time.Second {
		t.Errorf("attempt 1 = (%v,%v)", d, ok)
	}
	if d, ok := r.Schedule("k", func() {}); !ok || d != time.Second {
		t.Errorf("attempt 2 = (%v,%v)", d, ok)
	}
	if _, ok := r.Schedule("k", func() {}); ok {
		t.Error("attempt 3 exceeds MaxAttempts=2")
	}
}
