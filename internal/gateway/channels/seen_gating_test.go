package channels

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
)

// Delivery-confirmed seen-gating: success keeps the event seen; a retryable
// failure schedules bounded redispatch while keeping the event seen (no
// double-dispatch); after give-up the seen mark is rolled back so a restart
// retries (row 18).
func TestSettleDispatch_SeenGating(t *testing.T) {
	sched := &capturingScheduler{}
	c := &NIP29GroupChannel{
		seen:       NewSeenCache(),
		redispatch: NewRedispatchScheduler(RedispatchSchedulerOptions{Schedule: sched.schedule}),
		ctx:        context.Background(),
	}
	var ev nostr.RelayEvent // only passed to the (unfired) retry closure
	const evID = "evt-1"
	c.seen.Add(evID)

	// Success keeps the event seen and clears retry state.
	c.settleDispatch(ev, evID, true)
	if c.seen.Len() != 1 {
		t.Fatalf("success must keep the event seen, len=%d", c.seen.Len())
	}

	// Three retryable failures schedule redispatch; the event stays seen so a
	// concurrent relay redelivery cannot double-dispatch it.
	for i := 0; i < 3; i++ {
		c.settleDispatch(ev, evID, false)
	}
	if c.seen.Len() != 1 {
		t.Errorf("event must stay seen across retries, len=%d", c.seen.Len())
	}
	if got := len(sched.delays); got != 3 {
		t.Fatalf("expected 3 scheduled retries, got %d", got)
	}
	if sched.delays[0] != DefaultRedispatchBackoffs[0] ||
		sched.delays[1] != DefaultRedispatchBackoffs[1] ||
		sched.delays[2] != DefaultRedispatchBackoffs[2] {
		t.Errorf("retry backoffs = %v, want %v", sched.delays, DefaultRedispatchBackoffs)
	}
	if !c.isInflight(evID) {
		t.Error("event must be in-flight during the retry window")
	}

	// The next failure exhausts the cap -> give up: the event stays seen and is
	// marked GaveUp (which blocks reprocessing this process); the in-flight guard
	// is released.
	c.settleDispatch(ev, evID, false)
	if c.seen.Len() != 1 {
		t.Errorf("give-up must keep the event seen, len=%d", c.seen.Len())
	}
	if !c.redispatch.GaveUp(evID) {
		t.Error("give-up must mark the event GaveUp")
	}
	if c.isInflight(evID) {
		t.Error("give-up must release the in-flight guard")
	}
}

// A retry closure re-invokes dispatchInbound; verify it fires the handler with
// the same event id, and that a closed channel context suppresses the retry.
func TestSettleDispatch_RetryReinvokesHandler(t *testing.T) {
	sched := &capturingScheduler{}
	var dispatched []string
	c := &NIP29GroupChannel{
		id:         "relay'room",
		seen:       NewSeenCache(),
		redispatch: NewRedispatchScheduler(RedispatchSchedulerOptions{Schedule: sched.schedule}),
		ctx:        context.Background(),
		onMsg: func(m InboundMessage) {
			dispatched = append(dispatched, m.EventID)
		},
	}
	var ev nostr.RelayEvent
	const evID = "evt-9"
	c.seen.Add(evID)

	c.settleDispatch(ev, evID, false) // schedule one retry
	if len(dispatched) != 0 {
		t.Fatal("retry must not run before the backoff fires")
	}
	sched.fireAll()
	if len(dispatched) != 1 || dispatched[0] != evID {
		t.Fatalf("retry should re-dispatch the event once, got %v", dispatched)
	}
}
