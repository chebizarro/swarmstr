package channels

import (
	"sync"
	"testing"
)

// Concurrent redelivery of the SAME event must consume the pair budget exactly
// once (bot-loop review P0): without holding the lock across lookup+record, two
// goroutines could both miss the idempotency cache and double-record.
func TestBotLoopProtection_ConcurrentReplayCountsOnce(t *testing.T) {
	clock := newGuardClock()
	guard := NewPairLoopGuard(PairLoopGuardOptions{Now: clock.now})
	blp := NewBotLoopProtection(guard, 0)

	facts := BotLoopProtectionFacts{
		ScopeID: "acct", ConversationID: "room", SenderID: "botA", ReceiverID: "me",
		DefaultEnabled: true, EventID: "evt-1",
	}

	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			blp.RecordAndCheck(facts)
		}()
	}
	wg.Wait()

	snap := guard.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected exactly 1 tracked pair, got %d", len(snap))
	}
	if snap[0].RecentCount != 1 {
		t.Fatalf("same event delivered %d times must count once, got recent=%d", n, snap[0].RecentCount)
	}
}
