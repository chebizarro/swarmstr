package channels

import (
	"sync"
	"testing"
	"time"
)

type guardClock struct {
	mu sync.Mutex
	t  time.Time
}

func newGuardClock() *guardClock { return &guardClock{t: time.Unix(1_700_000_000, 0)} }
func (c *guardClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *guardClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func intPtr(i int) *int { return &i }
func bPtr(b bool) *bool { return &b }

func defaultSettings() PairLoopGuardSettings {
	return ResolvePairLoopGuardSettings(nil, nil, true)
}

func TestResolvePairLoopGuardSettings_Defaults(t *testing.T) {
	s := ResolvePairLoopGuardSettings(nil, nil, true)
	if !s.Enabled || s.MaxEventsPerWindow != 20 || s.Window != 60*time.Second || s.Cooldown != 60*time.Second {
		t.Fatalf("unexpected defaults: %+v", s)
	}
}

func TestResolvePairLoopGuardSettings_Precedence(t *testing.T) {
	defaults := &PairLoopGuardConfig{MaxEventsPerWindow: intPtr(5), WindowSeconds: intPtr(30)}
	room := &PairLoopGuardConfig{MaxEventsPerWindow: intPtr(3)} // room overrides only max
	s := ResolvePairLoopGuardSettings(room, defaults, true)
	if s.MaxEventsPerWindow != 3 {
		t.Errorf("room override: got maxEvents %d, want 3", s.MaxEventsPerWindow)
	}
	if s.Window != 30*time.Second {
		t.Errorf("defaults fallback: got window %v, want 30s", s.Window)
	}
	if s.Cooldown != 60*time.Second {
		t.Errorf("built-in fallback: got cooldown %v, want 60s", s.Cooldown)
	}
}

func TestResolvePairLoopGuardSettings_DefaultEnabledGate(t *testing.T) {
	// Config enables, but channel capability gate (defaultEnabled=false) wins.
	s := ResolvePairLoopGuardSettings(&PairLoopGuardConfig{Enabled: bPtr(true)}, nil, false)
	if s.Enabled {
		t.Error("defaultEnabled=false must force protection off")
	}
	// defaultsConfig can disable fleet-wide.
	s = ResolvePairLoopGuardSettings(nil, &PairLoopGuardConfig{Enabled: bPtr(false)}, true)
	if s.Enabled {
		t.Error("defaultsConfig enabled=false must disable")
	}
}

func TestPairGuard_DisabledNeverSuppresses(t *testing.T) {
	g := NewPairLoopGuard(PairLoopGuardOptions{})
	s := ResolvePairLoopGuardSettings(&PairLoopGuardConfig{Enabled: bPtr(false)}, nil, true)
	for i := 0; i < 100; i++ {
		if g.RecordAndCheck(RecordAndCheckParams{ScopeID: "a", ConversationID: "r", SenderID: "x", ReceiverID: "y", Settings: s}).Suppressed {
			t.Fatal("disabled guard must never suppress")
		}
	}
}

// Parity row 8: after 20 events / 60s the pair is suppressed for the cooldown;
// a different pair in the same room is unaffected.
func TestPairGuard_BudgetThenCooldown(t *testing.T) {
	clock := newGuardClock()
	g := NewPairLoopGuard(PairLoopGuardOptions{Now: clock.now})
	s := defaultSettings() // 20 / 60s / 60s

	rec := func(sender, receiver string) PairLoopGuardResult {
		return g.RecordAndCheck(RecordAndCheckParams{
			ScopeID: "acct", ConversationID: "room", SenderID: sender, ReceiverID: receiver, Settings: s,
		})
	}

	// First 20 events for pair (A,me) are allowed.
	for i := 0; i < 20; i++ {
		if rec("botA", "me").Suppressed {
			t.Fatalf("event %d should not be suppressed", i+1)
		}
		clock.advance(time.Second) // all within the 60s window? 20s elapsed — yes
	}
	// 21st event trips the cooldown.
	if !rec("botA", "me").Suppressed {
		t.Fatal("21st event in window must be suppressed")
	}
	// Still suppressed during cooldown.
	if !rec("botA", "me").Suppressed {
		t.Fatal("subsequent event during cooldown must be suppressed")
	}

	// A DIFFERENT pair in the same room is unaffected.
	if rec("botB", "me").Suppressed {
		t.Fatal("distinct pair must not be suppressed")
	}

	// After the cooldown elapses, the pair recovers.
	clock.advance(61 * time.Second)
	if rec("botA", "me").Suppressed {
		t.Fatal("pair should recover after cooldown")
	}
}

// Unordered pair: A->me and me->A are the same pair.
func TestPairGuard_UnorderedPair(t *testing.T) {
	clock := newGuardClock()
	g := NewPairLoopGuard(PairLoopGuardOptions{Now: clock.now})
	s := defaultSettings()

	rec := func(sender, receiver string) bool {
		return g.RecordAndCheck(RecordAndCheckParams{
			ScopeID: "acct", ConversationID: "room", SenderID: sender, ReceiverID: receiver, Settings: s,
		}).Suppressed
	}
	// Alternate direction each event; they must accumulate on ONE pair.
	tripped := false
	for i := 0; i < 21; i++ {
		var sup bool
		if i%2 == 0 {
			sup = rec("botA", "me")
		} else {
			sup = rec("me", "botA")
		}
		if sup {
			tripped = true
			break
		}
	}
	if !tripped {
		t.Fatal("alternating-direction events must count as one pair and trip")
	}
}

func TestPairGuard_WindowSlidesOff(t *testing.T) {
	clock := newGuardClock()
	g := NewPairLoopGuard(PairLoopGuardOptions{Now: clock.now})
	s := defaultSettings() // window 60s

	rec := func() bool {
		return g.RecordAndCheck(RecordAndCheckParams{
			ScopeID: "acct", ConversationID: "room", SenderID: "botA", ReceiverID: "me", Settings: s,
		}).Suppressed
	}
	// Spread 20 events over > window so they slide off; never trips.
	for i := 0; i < 20; i++ {
		if rec() {
			t.Fatalf("event %d should not trip when spread across window", i+1)
		}
		clock.advance(10 * time.Second) // 200s total, window is 60s
	}
	if rec() {
		t.Fatal("events spread beyond the window must not accumulate to a trip")
	}
}

func TestPairGuard_SelfPairNotSuppressed(t *testing.T) {
	g := NewPairLoopGuard(PairLoopGuardOptions{})
	s := defaultSettings()
	for i := 0; i < 100; i++ {
		if g.RecordAndCheck(RecordAndCheckParams{ScopeID: "a", ConversationID: "r", SenderID: "me", ReceiverID: "me", Settings: s}).Suppressed {
			t.Fatal("self-pair (sender==receiver) must never suppress")
		}
	}
}

func TestPairGuard_AccountIsolation(t *testing.T) {
	clock := newGuardClock()
	g := NewPairLoopGuard(PairLoopGuardOptions{Now: clock.now})
	s := defaultSettings()

	trip := func(scope string) {
		for i := 0; i < 21; i++ {
			g.RecordAndCheck(RecordAndCheckParams{ScopeID: scope, ConversationID: "room", SenderID: "botA", ReceiverID: "me", Settings: s})
		}
	}
	trip("acctA")
	// A different account's same room+pair must be independent.
	res := g.RecordAndCheck(RecordAndCheckParams{ScopeID: "acctB", ConversationID: "room", SenderID: "botA", ReceiverID: "me", Settings: s})
	if res.Suppressed {
		t.Fatal("account B must not inherit account A's cooldown")
	}
}

// Parity row 9: a redelivered event (same eventId) consumes the pair budget
// exactly once — the wrapper replays the first decision.
func TestBotLoopProtection_IdempotentReplay(t *testing.T) {
	clock := newGuardClock()
	guard := NewPairLoopGuard(PairLoopGuardOptions{Now: clock.now})
	blp := NewBotLoopProtection(guard, 0)

	facts := func(eventID string) BotLoopProtectionFacts {
		return BotLoopProtectionFacts{
			ScopeID: "acct", ConversationID: "room", SenderID: "botA", ReceiverID: "me",
			DefaultEnabled: true, EventID: eventID,
		}
	}

	// Record 20 DISTINCT events (budget not yet exceeded).
	for i := 0; i < 20; i++ {
		id := time.Duration(i).String() // distinct string ids
		blp.RecordAndCheck(facts(id))
	}
	// Re-deliver all 20 events again — must NOT advance the budget (replay).
	for i := 0; i < 20; i++ {
		id := time.Duration(i).String()
		if blp.RecordAndCheck(facts(id)).Suppressed {
			t.Fatalf("replayed event %d must not be suppressed differently", i)
		}
	}
	// The pair guard should have recorded exactly 20 events, not 40.
	snap := guard.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 tracked pair, got %d", len(snap))
	}
	if snap[0].RecentCount != 20 {
		t.Fatalf("idempotency failed: recent count = %d, want 20", snap[0].RecentCount)
	}
}

func TestBotLoopProtection_DistinctEventsAdvance(t *testing.T) {
	clock := newGuardClock()
	guard := NewPairLoopGuard(PairLoopGuardOptions{Now: clock.now})
	blp := NewBotLoopProtection(guard, 0)

	var suppressed bool
	for i := 0; i < 21; i++ {
		res := blp.RecordAndCheck(BotLoopProtectionFacts{
			ScopeID: "acct", ConversationID: "room", SenderID: "botA", ReceiverID: "me",
			DefaultEnabled: true, EventID: "evt-" + time.Duration(i).String(),
		})
		if res.Suppressed {
			suppressed = true
		}
	}
	if !suppressed {
		t.Fatal("21 distinct events must trip the budget")
	}
}

func TestBotLoopProtection_IdempotencyCacheCap(t *testing.T) {
	guard := NewPairLoopGuard(PairLoopGuardOptions{})
	blp := NewBotLoopProtection(guard, 2) // tiny cap
	f := func(id string) BotLoopProtectionFacts {
		return BotLoopProtectionFacts{ScopeID: "a", ConversationID: "r", SenderID: "b", ReceiverID: "me", DefaultEnabled: true, EventID: id}
	}
	blp.RecordAndCheck(f("e1"))
	blp.RecordAndCheck(f("e2"))
	blp.RecordAndCheck(f("e3")) // evicts e1
	blp.mu.Lock()
	_, hasE1 := blp.idem["a"+pairKeySeparator+"r"+pairKeySeparator+"e1"]
	n := len(blp.idem)
	blp.mu.Unlock()
	if hasE1 {
		t.Error("e1 should have been evicted from the FIFO idempotency cache")
	}
	if n != 2 {
		t.Errorf("idempotency cache size = %d, want 2", n)
	}
}
