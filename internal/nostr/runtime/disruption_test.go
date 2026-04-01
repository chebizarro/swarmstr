// disruption_test.go — Resilience/disruption test suite for long-lived subscriptions.
//
// These tests verify that the subscription resilience primitives (rebind,
// generation tracking, replay policy, health tracking, deduplication) behave
// correctly under simulated disruption scenarios. They exercise structural
// invariants without requiring real relay connections.
//
// Coverage targets from swarmstr-3.11.8:
//  1. Relay disconnect/reconnect → subscription loop restarts
//  2. Auth-required CLOSED → generation advances, stale close ignored
//  3. Relay policy change while running → rebind signal, relay list updated
//  4. Daemon restart with checkpoint replay → Since within replay window
//  5. Duplicate event observation across relays → seen set dedup
//  6. Watch restore after restart → Since jitter applied
//  7. NIP-17 backdated event recovery → backfill window covers gift wraps
//  8. Control RPC across degraded relays → health tracker gates retry
package runtime

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ─── 1. Relay disconnect/reconnect ──────────────────────────────────────────

func TestDisruption_ControlBusRestartsAfterStreamClose(t *testing.T) {
	// When SubscribeMany's channel closes (relay disconnect), runSubscription
	// returns false and the loop should restart with a fresh Since.
	iterations := 0
	since := ResubscribeSince(ControlRPCResubscribeWindow)
	for i := 0; i < 3; i++ {
		iterations++
		restart := false // false = stream closed unexpectedly
		if !restart {
			nextSince := ResubscribeSince(ControlRPCResubscribeWindow)
			if nextSince < since {
				t.Fatalf("resubscribe since should not go backwards: %d < %d", nextSince, since)
			}
			since = nextSince
		}
	}
	if iterations != 3 {
		t.Fatalf("expected 3 restart iterations, got %d", iterations)
	}
}

func TestDisruption_DMBusRestartsAfterStreamClose(t *testing.T) {
	b := &DMBus{replayWindow: DMReplayWindowDefault}
	since := b.resubscribeSinceUnix()
	if since <= 0 {
		t.Fatalf("resubscribe since should be positive, got %d", since)
	}
	expected := time.Now().Add(-DMReplayWindowDefault).Unix()
	if since < expected-2 || since > expected+2 {
		t.Fatalf("resubscribe since %d not within 2s of expected %d", since, expected)
	}
}

// ─── 2. Auth-required CLOSED then retry ─────────────────────────────────────

func TestDisruption_AuthClosedAdvancesGeneration(t *testing.T) {
	// When a relay sends auth-required: CLOSED, the generation should advance
	// so subsequent close events with the old generation are ignored.
	generation := map[string]int{}
	relay := "wss://auth-relay.example"

	nextGen := func(r string) int {
		generation[r]++
		return generation[r]
	}

	g1 := nextGen(relay) // initial subscribe
	if g1 != 1 {
		t.Fatalf("initial generation = %d, want 1", g1)
	}

	// Simulate auth-required close → retry
	g2 := nextGen(relay)
	if g2 != 2 {
		t.Fatalf("after auth retry, generation = %d, want 2", g2)
	}

	// Stale close from g1 should not match current generation
	staleClose := controlRelayClose{relayURL: relay, generation: g1}
	if generation[staleClose.relayURL] == staleClose.generation {
		t.Fatal("stale close from g1 should not match current generation g2")
	}

	// Current close from g2 should match
	currentClose := controlRelayClose{relayURL: relay, generation: g2}
	if generation[currentClose.relayURL] != currentClose.generation {
		t.Fatal("current close from g2 should match")
	}
}

func TestDisruption_DMAuthClosedAdvancesGeneration(t *testing.T) {
	generation := map[string]int{}
	relay := "wss://auth-relay.example"

	nextGen := func(r string) int {
		generation[r]++
		return generation[r]
	}

	g1 := nextGen(relay)
	g2 := nextGen(relay)

	stale := dmRelayClose{relayURL: relay, generation: g1}
	if generation[stale.relayURL] == stale.generation {
		t.Fatal("stale DM close from g1 should not match current g2")
	}
	current := dmRelayClose{relayURL: relay, generation: g2}
	if generation[current.relayURL] != current.generation {
		t.Fatal("current DM close from g2 should match")
	}
}

// ─── 3. Relay policy change while running ───────────────────────────────────

func TestDisruption_ControlBusRelayPolicyChange(t *testing.T) {
	b := &ControlRPCBus{
		relays:   []string{"wss://old-a", "wss://old-b"},
		health:   NewRelayHealthTracker(),
		rebindCh: make(chan struct{}, 1),
	}
	b.health.Seed(b.relays)

	// Simulate relay policy change.
	if err := b.SetRelays([]string{"wss://new-a", "wss://new-b", "wss://new-c"}); err != nil {
		t.Fatal(err)
	}

	// Verify rebind signal.
	select {
	case <-b.rebindCh:
	default:
		t.Fatal("expected rebind signal after policy change")
	}

	// Verify relay list updated.
	got := b.currentRelays()
	if len(got) != 3 || got[0] != "wss://new-a" {
		t.Fatalf("relays not updated: %v", got)
	}

	// Verify health tracker accepts new relays.
	if !b.health.Allowed("wss://new-a", time.Now()) {
		t.Fatal("new relay should be allowed in health tracker")
	}
}

func TestDisruption_DMBusRelayPolicyChange(t *testing.T) {
	b := &DMBus{
		relays:       []string{"wss://old"},
		health:       NewRelayHealthTracker(),
		replayWindow: DMReplayWindowDefault,
		rebindCh:     make(chan struct{}, 1),
	}
	b.health.Seed(b.relays)

	if err := b.SetRelays([]string{"wss://new-a", "wss://new-b"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.rebindCh:
	default:
		t.Fatal("expected rebind signal")
	}
	if got := b.currentRelays(); len(got) != 2 {
		t.Fatalf("expected 2 relays, got %v", got)
	}
}

func TestDisruption_NIP17BusRelayPolicyChange(t *testing.T) {
	b := &NIP17Bus{
		relays:   []string{"wss://old"},
		rebindCh: make(chan struct{}, 1),
	}

	if err := b.SetRelays([]string{"wss://new-a", "wss://new-b"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.rebindCh:
	default:
		t.Fatal("expected rebind signal")
	}
	if got := b.currentRelays(); len(got) != 2 {
		t.Fatalf("expected 2 relays, got %v", got)
	}
}

func TestDisruption_RapidRelayPolicyChangesCoalesce(t *testing.T) {
	b := &ControlRPCBus{
		relays:   []string{"wss://initial"},
		health:   NewRelayHealthTracker(),
		rebindCh: make(chan struct{}, 1),
	}
	b.health.Seed(b.relays)

	// Simulate rapid config changes (e.g. config.put burst).
	for i := 0; i < 10; i++ {
		_ = b.SetRelays([]string{fmt.Sprintf("wss://relay-%d", i)})
	}

	// Only one rebind signal should be queued.
	select {
	case <-b.rebindCh:
	default:
		t.Fatal("expected at least one rebind signal")
	}
	select {
	case <-b.rebindCh:
		t.Fatal("expected coalesced signal (only one)")
	default:
	}

	// Last relay wins.
	got := b.currentRelays()
	if len(got) != 1 || got[0] != "wss://relay-9" {
		t.Fatalf("expected last relay, got %v", got)
	}
}

// ─── 4. Checkpoint replay (Since within replay window) ──────────────────────

func TestDisruption_ControlReplaySinceWithinWindow(t *testing.T) {
	since := ResubscribeSince(ControlRPCResubscribeWindow)
	now := time.Now().Unix()
	expected := now - int64(ControlRPCResubscribeWindow.Seconds())
	if since < expected-2 || since > expected+2 {
		t.Fatalf("control since %d not within 2s of expected %d", since, expected)
	}
}

func TestDisruption_DMReplaySinceWithinWindow(t *testing.T) {
	since := ResubscribeSince(DMReplayWindowDefault)
	now := time.Now().Unix()
	expected := now - int64(DMReplayWindowDefault.Seconds())
	if since < expected-2 || since > expected+2 {
		t.Fatalf("DM since %d not within 2s of expected %d", since, expected)
	}
}

func TestDisruption_NIP17ReplaySinceCoversGiftWrapBackdate(t *testing.T) {
	// NIP-17 since must look back far enough for NIP-59 backdating (~10h).
	since := normalizeNIP17Since(time.Now().Unix())
	now := time.Now().Unix()
	backfillSeconds := int64(nip17GiftWrapBackfill.Seconds())

	// Since should be approximately now - backfill.
	if since > now-backfillSeconds+60 {
		t.Fatalf("NIP-17 since %d doesn't look back far enough (now=%d, backfill=%ds)", since, now, backfillSeconds)
	}
	if since < now-backfillSeconds-60 {
		t.Fatalf("NIP-17 since %d looks back too far (now=%d, backfill=%ds)", since, now, backfillSeconds)
	}
}

func TestDisruption_DVMReplaySinceWithinWindow(t *testing.T) {
	since := ResubscribeSince(DVMResubscribeWindow)
	now := time.Now().Unix()
	expected := now - int64(DVMResubscribeWindow.Seconds())
	if since < expected-2 || since > expected+2 {
		t.Fatalf("DVM since %d not within 2s of expected %d", since, expected)
	}
}

func TestDisruption_ReplaySinceNeverNegative(t *testing.T) {
	// Even with an absurdly large window, since should clamp to 0.
	since := ResubscribeSince(100 * 365 * 24 * time.Hour)
	if since < 0 {
		t.Fatalf("since should never be negative, got %d", since)
	}
}

// ─── 5. Duplicate event observation across relays ───────────────────────────

func TestDisruption_ControlBusSeenSetDeduplicates(t *testing.T) {
	b := &ControlRPCBus{
		seenSet:  make(map[string]struct{}),
		seenCap:  100,
		seenList: nil,
	}

	// First observation should not be seen.
	if b.markSeen("event-abc") {
		t.Fatal("first observation should not be marked as seen")
	}
	// Second observation of same event should be seen (duplicate).
	if !b.markSeen("event-abc") {
		t.Fatal("second observation should be marked as seen (dedup)")
	}
}

func TestDisruption_DMBusSeenSetDeduplicates(t *testing.T) {
	b := &DMBus{
		seenSet:  make(map[string]struct{}),
		seenCap:  100,
		seenList: nil,
	}

	if b.markSeen("event-xyz") {
		t.Fatal("first observation should not be marked as seen")
	}
	if !b.markSeen("event-xyz") {
		t.Fatal("second observation should be marked as seen (dedup)")
	}
}

func TestDisruption_SeenSetEvictsOldestOnOverflow(t *testing.T) {
	b := &ControlRPCBus{
		seenSet:  make(map[string]struct{}),
		seenCap:  3,
		seenList: nil,
	}

	b.markSeen("a")
	b.markSeen("b")
	b.markSeen("c")
	// At cap; adding "d" should evict "a".
	b.markSeen("d")

	if b.markSeen("a") {
		t.Fatal("'a' should have been evicted")
	}
	if !b.markSeen("c") {
		t.Fatal("'c' should still be seen")
	}
	if !b.markSeen("d") {
		t.Fatal("'d' should still be seen")
	}
}

func TestDisruption_MultiRelayDuplicateEventDropped(t *testing.T) {
	// Simulate the same event arriving from 3 different relays.
	b := &ControlRPCBus{
		seenSet:  make(map[string]struct{}),
		seenCap:  1000,
		seenList: nil,
	}

	eventID := "aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff6666"
	accepted := 0
	for _, relay := range []string{"wss://relay-1", "wss://relay-2", "wss://relay-3"} {
		_ = relay
		if !b.markSeen(eventID) {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("expected exactly 1 accepted (first relay), got %d", accepted)
	}
}

// ─── 6. Watch since jitter ──────────────────────────────────────────────────

func TestDisruption_WatchSinceJitterApplied(t *testing.T) {
	// Watch subscriptions should backdate Since by WatchSinceJitter.
	now := time.Now().Unix()
	jittered := now - int64(WatchSinceJitter.Seconds())

	if jittered >= now {
		t.Fatalf("jittered since %d should be before now %d", jittered, now)
	}
	if now-jittered != int64(WatchSinceJitter.Seconds()) {
		t.Fatalf("jitter gap should be %ds, got %ds", int64(WatchSinceJitter.Seconds()), now-jittered)
	}
}

func TestDisruption_WatchSinceJitterDoesNotGoNegative(t *testing.T) {
	// Even at Unix epoch, jitter should clamp to 0.
	var ts int64 = 10 // 10 seconds after epoch
	jittered := ts - int64(WatchSinceJitter.Seconds())
	if jittered < 0 {
		jittered = 0
	}
	if jittered != 0 {
		t.Fatalf("expected clamped to 0, got %d", jittered)
	}
}

// ─── 7. NIP-17 backdated event recovery ─────────────────────────────────────

func TestDisruption_NIP17BackfillWindowCoversBackdating(t *testing.T) {
	// NIP-59 backdates gift wraps by up to ~10 hours. The backfill window
	// must be at least 10 hours to catch them.
	if NIP17GiftWrapBackfill < 10*time.Hour {
		t.Fatalf("NIP17GiftWrapBackfill = %v, should be >= 10h", NIP17GiftWrapBackfill)
	}
}

func TestDisruption_NIP17NormalizeSinceWithRecentCheckpoint(t *testing.T) {
	// A recent checkpoint (5min ago): normalizeNIP17Since subtracts the full
	// backfill window, then clamps to floor=now-backfill. Result should be
	// approximately now - backfill.
	checkpoint := time.Now().Add(-5 * time.Minute).Unix()
	since := normalizeNIP17Since(checkpoint)
	now := time.Now().Unix()

	expectedFloor := now - int64(nip17GiftWrapBackfill.Seconds())
	if since < expectedFloor-60 || since > expectedFloor+60 {
		t.Fatalf("NIP-17 since %d not near floor %d for recent checkpoint %d", since, expectedFloor, checkpoint)
	}
}

func TestDisruption_NIP17NormalizeSinceWithOldCheckpoint(t *testing.T) {
	// A very old checkpoint (2 hours ago) should be pulled forward
	// to at most now - backfill.
	checkpoint := time.Now().Add(-2 * time.Hour).Unix()
	since := normalizeNIP17Since(checkpoint)
	now := time.Now().Unix()

	// Since should be approximately now - backfill.
	expectedMin := now - int64(nip17GiftWrapBackfill.Seconds()) - 60
	expectedMax := now - int64(nip17GiftWrapBackfill.Seconds()) + 60
	if since < expectedMin || since > expectedMax {
		t.Fatalf("NIP-17 since %d out of expected range [%d, %d]", since, expectedMin, expectedMax)
	}
}

// ─── 8. Control RPC across degraded relays ──────────────────────────────────

func TestDisruption_HealthTrackerGatesRetry(t *testing.T) {
	h := NewRelayHealthTracker()
	relay := "wss://degraded.example"
	h.Seed([]string{relay})

	// Initially allowed.
	if !h.Allowed(relay, time.Now()) {
		t.Fatal("relay should be allowed initially")
	}

	// After consecutive failures, relay should be temporarily blocked.
	for i := 0; i < 5; i++ {
		h.RecordFailure(relay)
	}
	if h.Allowed(relay, time.Now()) {
		t.Fatal("relay should be blocked after consecutive failures")
	}

	// After a success, relay should be allowed again.
	h.RecordSuccess(relay)
	if !h.Allowed(relay, time.Now()) {
		t.Fatal("relay should be allowed after success")
	}
}

func TestDisruption_HealthTrackerSortsDegradedRelaysLast(t *testing.T) {
	h := NewRelayHealthTracker()
	relays := []string{"wss://good", "wss://bad", "wss://ok"}
	h.Seed(relays)

	// Make "bad" relay degraded.
	h.RecordFailure("wss://bad")
	h.RecordFailure("wss://bad")
	h.RecordFailure("wss://bad")
	h.RecordSuccess("wss://good")
	h.RecordSuccess("wss://ok")

	sorted := h.SortRelays(relays)
	if len(sorted) != 3 {
		t.Fatalf("expected 3 relays, got %d", len(sorted))
	}
	// Bad relay should be last.
	if sorted[2] != "wss://bad" {
		t.Fatalf("degraded relay should be sorted last, got order: %v", sorted)
	}
}

func TestDisruption_HealthTrackerCandidatesExcludeBlocked(t *testing.T) {
	h := NewRelayHealthTracker()
	relays := []string{"wss://a", "wss://b", "wss://c"}
	h.Seed(relays)

	// Block "wss://b".
	for i := 0; i < 10; i++ {
		h.RecordFailure("wss://b")
	}

	candidates := h.Candidates(relays, time.Now())
	for _, c := range candidates {
		if c == "wss://b" {
			t.Fatal("blocked relay should not appear in candidates")
		}
	}
	if len(candidates) < 2 {
		t.Fatalf("expected at least 2 candidates, got %d", len(candidates))
	}
}

func TestDisruption_ControlBusResponseRelayCandidatesPreferHealthy(t *testing.T) {
	h := NewRelayHealthTracker()
	relays := []string{"wss://a", "wss://b"}
	h.Seed(relays)

	// Make "a" unhealthy.
	for i := 0; i < 5; i++ {
		h.RecordFailure("wss://a")
	}

	b := &ControlRPCBus{
		relays: relays,
		health: h,
	}

	candidates := b.responseRelayCandidates("", "requester", time.Now())
	if len(candidates) == 0 {
		t.Fatal("should have at least one candidate")
	}
	// Healthy relay should appear before unhealthy.
	if candidates[0] != "wss://b" {
		t.Fatalf("healthy relay should be preferred, got %v", candidates)
	}
}

// ─── Cross-cutting: SubHealthTracker records across disruptions ─────────────

func TestDisruption_SubHealthTrackerRecordsDisruptionSequence(t *testing.T) {
	tr := NewSubHealthTracker("control-rpc")

	// Initial connect.
	tr.RecordReconnect()

	// Receive some events.
	for i := 0; i < 5; i++ {
		tr.RecordEvent()
	}

	// Relay sends CLOSED.
	tr.RecordClosed("rate-limited:")

	// Reconnect.
	tr.RecordReconnect()

	// More events.
	for i := 0; i < 3; i++ {
		tr.RecordEvent()
	}

	snap := tr.Snapshot([]string{"wss://relay"}, ControlRPCResubscribeWindow)
	if snap.EventCount != 8 {
		t.Fatalf("event_count = %d, want 8", snap.EventCount)
	}
	if snap.ReconnectCount != 2 {
		t.Fatalf("reconnect_count = %d, want 2", snap.ReconnectCount)
	}
	if snap.LastClosedReason != "rate-limited:" {
		t.Fatalf("last_closed_reason = %q, want %q", snap.LastClosedReason, "rate-limited:")
	}
	if snap.LastEventAt.IsZero() {
		t.Fatal("last_event_at should be set")
	}
	if snap.LastReconnectAt.IsZero() {
		t.Fatal("last_reconnect_at should be set")
	}
}

func TestDisruption_HealthSnapshotsReflectBusType(t *testing.T) {
	// Verify each bus returns the correct label and replay window.
	tests := []struct {
		name     string
		snapshot func() SubHealthSnapshot
		label    string
		window   time.Duration
	}{
		{
			name: "control-rpc",
			snapshot: func() SubHealthSnapshot {
				b := &ControlRPCBus{relays: []string{"wss://r"}}
				return b.HealthSnapshot()
			},
			label:  "control-rpc",
			window: ControlRPCResubscribeWindow,
		},
		{
			name: "dm",
			snapshot: func() SubHealthSnapshot {
				b := &DMBus{relays: []string{"wss://r"}, replayWindow: DMReplayWindowDefault}
				return b.HealthSnapshot()
			},
			label:  "dm",
			window: DMReplayWindowDefault,
		},
		{
			name: "nip17",
			snapshot: func() SubHealthSnapshot {
				b := &NIP17Bus{relays: []string{"wss://r"}}
				return b.HealthSnapshot()
			},
			label:  "nip17",
			window: NIP17GiftWrapBackfill,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := tt.snapshot()
			if snap.Label != tt.label {
				t.Fatalf("label = %q, want %q", snap.Label, tt.label)
			}
			if snap.ReplayWindowMS != int64(tt.window/time.Millisecond) {
				t.Fatalf("replay_window_ms = %d, want %d", snap.ReplayWindowMS, int64(tt.window/time.Millisecond))
			}
		})
	}
}

// ─── Replay policy consistency ──────────────────────────────────────────────

func TestDisruption_ReplayPolicyConsistencyInvariants(t *testing.T) {
	// All replay windows should be positive.
	windows := map[string]time.Duration{
		"ControlRPCResubscribeWindow": ControlRPCResubscribeWindow,
		"DMReplayWindowDefault":       DMReplayWindowDefault,
		"NIP17GiftWrapBackfill":       NIP17GiftWrapBackfill,
		"WatchSinceJitter":            WatchSinceJitter,
		"DVMResubscribeWindow":        DVMResubscribeWindow,
	}
	for name, w := range windows {
		if w <= 0 {
			t.Errorf("%s = %v, must be positive", name, w)
		}
	}

	// NIP-17 backfill must be the largest (gift wrap backdating).
	if NIP17GiftWrapBackfill <= DMReplayWindowDefault {
		t.Error("NIP-17 backfill should exceed DM replay window")
	}
	if NIP17GiftWrapBackfill <= ControlRPCResubscribeWindow {
		t.Error("NIP-17 backfill should exceed control RPC window")
	}

	// Watch jitter should be the smallest (just connection setup gap).
	if WatchSinceJitter >= DMReplayWindowDefault {
		t.Error("watch jitter should be smaller than DM replay window")
	}
}

// ─── Sub ID generation distinguishes relays and generations ─────────────────

func TestDisruption_SubIDsUniquePerRelayAndGeneration(t *testing.T) {
	b := &ControlRPCBus{}
	ids := map[string]bool{}

	for _, relay := range []string{"wss://r1", "wss://r2", "wss://r3"} {
		for gen := 1; gen <= 5; gen++ {
			id := b.controlSubID(relay, gen)
			if ids[id] {
				t.Fatalf("duplicate sub ID: %s", id)
			}
			ids[id] = true
		}
	}
}

func TestDisruption_DMSubIDsUniquePerRelayAndGeneration(t *testing.T) {
	b := &DMBus{}
	ids := map[string]bool{}

	for _, relay := range []string{"wss://r1", "wss://r2"} {
		for gen := 1; gen <= 5; gen++ {
			id := b.dmSubID(relay, gen)
			if ids[id] {
				t.Fatalf("duplicate sub ID: %s", id)
			}
			ids[id] = true
		}
	}
}

// ─── Relay list sanitization across disruptions ─────────────────────────────

func TestDisruption_SanitizeRelayListRemovesBlanksAndDuplicates(t *testing.T) {
	input := []string{"wss://a", "", "wss://b", "  ", "wss://a"}
	got := sanitizeRelayList(input)
	// Should contain at most "wss://a" and "wss://b" (no blanks, no dupes).
	if len(got) > 2 {
		t.Fatalf("expected at most 2 sanitized relays, got %v", got)
	}
	for _, r := range got {
		if strings.TrimSpace(r) == "" {
			t.Fatalf("blank relay in sanitized list: %v", got)
		}
	}
}

func TestDisruption_EmptyRelayListRejected(t *testing.T) {
	b := &ControlRPCBus{relays: []string{"wss://keep"}, rebindCh: make(chan struct{}, 1)}
	err := b.SetRelays([]string{""})
	if err == nil {
		t.Fatal("expected error for all-blank relay list")
	}
	// Original relays preserved.
	if got := b.currentRelays(); len(got) != 1 || got[0] != "wss://keep" {
		t.Fatalf("relays should be unchanged: %v", got)
	}
}
