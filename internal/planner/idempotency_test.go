package planner

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// ── SideEffectClass classification ──────────────────────────────────────────

func TestClassifySideEffect_PureTools(t *testing.T) {
	pure := []string{
		"nostr_fetch", "nostr_profile", "nostr_resolve_nip05",
		"nostr_follows", "nostr_followers", "nostr_wot_distance",
		"nostr_relay_hints", "nostr_watch_list",
		"relay_list", "relay_info", "relay_ping",
		"fetch_data", "get_profile", "list_items", "search_events",
	}
	for _, tool := range pure {
		if got := ClassifySideEffect(tool); got != SideEffectPure {
			t.Errorf("ClassifySideEffect(%q) = %s, want pure", tool, got)
		}
	}
}

func TestClassifySideEffect_RetryableTools(t *testing.T) {
	retryable := []string{
		"update_profile", "set_relay_list", "update_config",
		"set_preference", "upsert_record",
	}
	for _, tool := range retryable {
		if got := ClassifySideEffect(tool); got != SideEffectRetryable {
			t.Errorf("ClassifySideEffect(%q) = %s, want retryable", tool, got)
		}
	}
}

func TestClassifySideEffect_SideEffectfulTools(t *testing.T) {
	effectful := []string{
		"nostr_publish", "nostr_send_dm", "nostr_zap_send",
		"nostr_watch", "nostr_unwatch",
		"broadcast_event", "pay_invoice", "delete_event",
		"create_task", "post_message", "remove_file",
	}
	for _, tool := range effectful {
		if got := ClassifySideEffect(tool); got != SideEffectSideEffectful {
			t.Errorf("ClassifySideEffect(%q) = %s, want side_effectful", tool, got)
		}
	}
}

func TestClassifySideEffect_UnknownDefaultsToSideEffectful(t *testing.T) {
	if got := ClassifySideEffect("mysterious_tool"); got != SideEffectSideEffectful {
		t.Errorf("unknown tool should default to side_effectful, got %s", got)
	}
}

func TestValidSideEffectClass_Known(t *testing.T) {
	for _, c := range []SideEffectClass{SideEffectPure, SideEffectRetryable, SideEffectSideEffectful} {
		if !ValidSideEffectClass(c) {
			t.Errorf("expected %q to be valid", c)
		}
	}
}

func TestValidSideEffectClass_Unknown(t *testing.T) {
	if ValidSideEffectClass("bogus") {
		t.Fatal("expected bogus to be invalid")
	}
}

func TestIsSafeToReplay_Pure(t *testing.T) {
	if !IsSafeToReplay("nostr_fetch") {
		t.Fatal("pure tool should be safe to replay")
	}
}

func TestIsSafeToReplay_Retryable(t *testing.T) {
	if !IsSafeToReplay("update_profile") {
		t.Fatal("retryable tool should be safe to replay")
	}
}

func TestIsSafeToReplay_SideEffectful(t *testing.T) {
	if IsSafeToReplay("nostr_publish") {
		t.Fatal("side-effectful tool should NOT be safe to replay")
	}
}

// ── IdempotencyKey generation ───────────────────────────────────────────────

func TestGenerateIdempotencyKey_Deterministic(t *testing.T) {
	k1 := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 42)
	k2 := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 42)
	if k1.Key != k2.Key {
		t.Fatalf("expected deterministic key, got %s vs %s", k1.Key, k2.Key)
	}
}

func TestGenerateIdempotencyKey_DifferentSequence(t *testing.T) {
	k1 := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 1)
	k2 := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 2)
	if k1.Key == k2.Key {
		t.Fatal("different sequences should produce different keys")
	}
}

func TestGenerateIdempotencyKey_DifferentRun(t *testing.T) {
	k1 := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 1)
	k2 := GenerateIdempotencyKey("task-1", "run-2", "nostr_publish", 1)
	if k1.Key == k2.Key {
		t.Fatal("different runs should produce different keys")
	}
}

func TestGenerateIdempotencyKey_DifferentTool(t *testing.T) {
	k1 := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 1)
	k2 := GenerateIdempotencyKey("task-1", "run-1", "nostr_send_dm", 1)
	if k1.Key == k2.Key {
		t.Fatal("different tools should produce different keys")
	}
}

func TestGenerateIdempotencyKey_HasPrefix(t *testing.T) {
	k := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 1)
	if !strings.HasPrefix(k.Key, "idem-") {
		t.Fatalf("expected idem- prefix, got %s", k.Key)
	}
}

func TestGenerateIdempotencyKey_Fields(t *testing.T) {
	k := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 42)
	if k.TaskID != "task-1" || k.RunID != "run-1" || k.Tool != "nostr_publish" || k.Sequence != 42 {
		t.Fatalf("field mismatch: %+v", k)
	}
}

// ── IdempotencyRegistry ─────────────────────────────────────────────────────

func TestIdempotencyRegistry_Empty(t *testing.T) {
	r := NewIdempotencyRegistry()
	if r.Len() != 0 {
		t.Fatalf("expected 0, got %d", r.Len())
	}
	_, ok := r.Check("nonexistent")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestIdempotencyRegistry_RecordAndCheck(t *testing.T) {
	r := NewIdempotencyRegistry()
	stored := r.MarkCompleted("key-1", "nostr_publish", "evt-abc", 1000)
	if !stored {
		t.Fatal("expected first record to succeed")
	}
	o, ok := r.Check("key-1")
	if !ok {
		t.Fatal("expected to find key-1")
	}
	if o.Status != "completed" || o.ResultRef != "evt-abc" {
		t.Fatalf("unexpected outcome: %+v", o)
	}
}

func TestIdempotencyRegistry_FirstWriteWins(t *testing.T) {
	r := NewIdempotencyRegistry()
	r.MarkCompleted("key-1", "tool-a", "ref-1", 1000)
	stored := r.MarkCompleted("key-1", "tool-a", "ref-2", 2000)
	if stored {
		t.Fatal("expected second write to be rejected")
	}
	o, _ := r.Check("key-1")
	if o.ResultRef != "ref-1" {
		t.Fatalf("expected first write to win, got %s", o.ResultRef)
	}
}

func TestIdempotencyRegistry_AlreadyExecuted(t *testing.T) {
	r := NewIdempotencyRegistry()
	r.MarkCompleted("key-1", "tool-a", "ref-1", 1000)
	if !r.AlreadyExecuted("key-1") {
		t.Fatal("expected already executed")
	}
	if r.AlreadyExecuted("key-2") {
		t.Fatal("expected not executed for unknown key")
	}
}

func TestIdempotencyRegistry_FailedNotAlreadyExecuted(t *testing.T) {
	r := NewIdempotencyRegistry()
	r.MarkFailed("key-1", "tool-a", "timeout", 1000)
	if r.AlreadyExecuted("key-1") {
		t.Fatal("failed operations should not be considered already executed")
	}
	// But the outcome should be recorded
	o, ok := r.Check("key-1")
	if !ok {
		t.Fatal("expected to find failed outcome")
	}
	if o.Status != "failed" || o.Error != "timeout" {
		t.Fatalf("unexpected failed outcome: %+v", o)
	}
}

func TestIdempotencyRegistry_Outcomes(t *testing.T) {
	r := NewIdempotencyRegistry()
	r.MarkCompleted("k1", "t1", "r1", 1000)
	r.MarkFailed("k2", "t2", "err", 1000)

	outcomes := r.Outcomes()
	if len(outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %d", len(outcomes))
	}
}

func TestIdempotencyRegistry_RestoreOutcomes(t *testing.T) {
	r := NewIdempotencyRegistry()
	r.RestoreOutcomes([]IdempotencyOutcome{
		{Key: "k1", Tool: "t1", Status: "completed", ResultRef: "ref-1"},
		{Key: "k2", Tool: "t2", Status: "failed", Error: "err"},
	})
	if r.Len() != 2 {
		t.Fatalf("expected 2, got %d", r.Len())
	}
	if !r.AlreadyExecuted("k1") {
		t.Fatal("expected k1 already executed after restore")
	}
}

func TestIdempotencyRegistry_RestoreDoesNotOverwrite(t *testing.T) {
	r := NewIdempotencyRegistry()
	r.MarkCompleted("k1", "t1", "original", 1000)
	r.RestoreOutcomes([]IdempotencyOutcome{
		{Key: "k1", Tool: "t1", Status: "completed", ResultRef: "restored"},
	})
	o, _ := r.Check("k1")
	if o.ResultRef != "original" {
		t.Fatalf("expected original to be preserved, got %s", o.ResultRef)
	}
}

// ── DispatchGuard ───────────────────────────────────────────────────────────

func TestDispatchGuard_PureAlwaysAllowed(t *testing.T) {
	guard := NewDispatchGuard(NewIdempotencyRegistry())
	key := GenerateIdempotencyKey("task-1", "run-1", "nostr_fetch", 1)
	dec := guard.ShouldDispatch(key)
	if !dec.Allowed {
		t.Fatalf("pure tool should always be allowed: %s", dec.Reason)
	}
	if dec.SideEffectClass != SideEffectPure {
		t.Fatalf("expected pure class, got %s", dec.SideEffectClass)
	}
}

func TestDispatchGuard_RetryableAlwaysAllowed(t *testing.T) {
	guard := NewDispatchGuard(NewIdempotencyRegistry())
	key := GenerateIdempotencyKey("task-1", "run-1", "update_profile", 1)
	dec := guard.ShouldDispatch(key)
	if !dec.Allowed {
		t.Fatal("retryable tool should always be allowed")
	}
}

func TestDispatchGuard_SideEffectfulFirstTime(t *testing.T) {
	guard := NewDispatchGuard(NewIdempotencyRegistry())
	key := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 1)
	dec := guard.ShouldDispatch(key)
	if !dec.Allowed {
		t.Fatal("first execution should be allowed")
	}
	if dec.IdempotencyKey == "" {
		t.Fatal("expected idempotency key for side-effectful tool")
	}
	if dec.SideEffectClass != SideEffectSideEffectful {
		t.Fatalf("expected side_effectful, got %s", dec.SideEffectClass)
	}
}

func TestDispatchGuard_SideEffectfulAlreadyCompleted(t *testing.T) {
	reg := NewIdempotencyRegistry()
	guard := NewDispatchGuard(reg)

	key := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 1)
	reg.MarkCompleted(key.Key, key.Tool, "evt-abc", 1000)

	dec := guard.ShouldDispatch(key)
	if dec.Allowed {
		t.Fatal("already-completed side-effectful tool should be blocked")
	}
	if dec.PriorOutcome == nil {
		t.Fatal("expected prior outcome")
	}
	if dec.PriorOutcome.Status != "completed" {
		t.Fatalf("expected completed status, got %s", dec.PriorOutcome.Status)
	}
}

func TestDispatchGuard_SideEffectfulPreviouslyFailed(t *testing.T) {
	reg := NewIdempotencyRegistry()
	guard := NewDispatchGuard(reg)

	key := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 1)
	reg.MarkFailed(key.Key, key.Tool, "timeout", 1000)

	dec := guard.ShouldDispatch(key)
	if !dec.Allowed {
		t.Fatal("failed operation should allow retry")
	}
	if !strings.Contains(dec.Reason, "retry allowed") {
		t.Fatalf("expected retry message, got %s", dec.Reason)
	}
}

// ── FormatDispatchDecision ──────────────────────────────────────────────────

func TestFormatDispatchDecision_Allowed(t *testing.T) {
	d := DispatchDecision{Allowed: true, Reason: "pure tool", IdempotencyKey: "k1"}
	out := FormatDispatchDecision(d)
	if !strings.Contains(out, "✅ ALLOW") {
		t.Fatal("expected allow marker")
	}
	if !strings.Contains(out, "[key=k1]") {
		t.Fatal("expected key in output")
	}
}

func TestFormatDispatchDecision_Blocked(t *testing.T) {
	d := DispatchDecision{Allowed: false, Reason: "already completed"}
	out := FormatDispatchDecision(d)
	if !strings.Contains(out, "🚫 SKIP") {
		t.Fatal("expected skip marker")
	}
}

// ── Concurrency ─────────────────────────────────────────────────────────────

func TestIdempotencyRegistry_ConcurrentAccess(t *testing.T) {
	r := NewIdempotencyRegistry()
	var wg sync.WaitGroup
	const n = 50

	for i := 0; i < n; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			key := GenerateIdempotencyKey("task-1", "run-1", "tool", int64(i)).Key
			if i%2 == 0 {
				r.MarkCompleted(key, "tool", "ref", int64(i))
			} else {
				r.MarkFailed(key, "tool", "err", int64(i))
			}
			r.Check(key)
			r.AlreadyExecuted(key)
			r.Outcomes()
			r.Len()
		}()
	}
	wg.Wait()

	if r.Len() != n {
		t.Fatalf("expected %d outcomes, got %d", n, r.Len())
	}
}

func TestDispatchGuard_ConcurrentDispatches(t *testing.T) {
	reg := NewIdempotencyRegistry()
	guard := NewDispatchGuard(reg)
	var wg sync.WaitGroup
	const n = 30

	for i := 0; i < n; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			key := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", int64(i))
			guard.ShouldDispatch(key)
		}()
	}
	wg.Wait()
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestIdempotencyKey_JSONRoundTrip(t *testing.T) {
	k := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 42)
	b, err := json.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	var decoded IdempotencyKey
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Key != k.Key || decoded.Tool != k.Tool || decoded.Sequence != k.Sequence {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
}

func TestIdempotencyOutcome_JSONRoundTrip(t *testing.T) {
	o := IdempotencyOutcome{
		Key:         "idem-abc",
		Tool:        "nostr_publish",
		Status:      "completed",
		ResultRef:   "evt-123",
		CompletedAt: 1000,
		Meta:        map[string]any{"kind": 1},
	}
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var decoded IdempotencyOutcome
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Key != o.Key || decoded.Status != o.Status {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
}

func TestDispatchDecision_JSONRoundTrip(t *testing.T) {
	d := DispatchDecision{
		Allowed:         false,
		Reason:          "already completed",
		IdempotencyKey:  "idem-abc",
		SideEffectClass: SideEffectSideEffectful,
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DispatchDecision
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Allowed != d.Allowed || decoded.SideEffectClass != d.SideEffectClass {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
}

// ── End-to-end: replay scenario ─────────────────────────────────────────────

func TestEndToEnd_ReplayProtection(t *testing.T) {
	reg := NewIdempotencyRegistry()
	guard := NewDispatchGuard(reg)

	// First execution: publish allowed
	key1 := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 1)
	dec1 := guard.ShouldDispatch(key1)
	if !dec1.Allowed {
		t.Fatal("first dispatch should be allowed")
	}

	// Execute succeeds → record outcome
	reg.MarkCompleted(key1.Key, key1.Tool, "evt-abc", 1000)

	// Replay with same key → blocked
	dec2 := guard.ShouldDispatch(key1)
	if dec2.Allowed {
		t.Fatal("replay should be blocked")
	}
	if dec2.PriorOutcome.ResultRef != "evt-abc" {
		t.Fatal("expected prior result ref")
	}

	// Different sequence (next tool call) → allowed
	key2 := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 2)
	dec3 := guard.ShouldDispatch(key2)
	if !dec3.Allowed {
		t.Fatal("different sequence should be allowed")
	}

	// Pure tool always allowed regardless of state
	keyPure := GenerateIdempotencyKey("task-1", "run-1", "nostr_fetch", 1)
	decPure := guard.ShouldDispatch(keyPure)
	if !decPure.Allowed {
		t.Fatal("pure tool should always be allowed")
	}
}

func TestEndToEnd_FailAndRetry(t *testing.T) {
	reg := NewIdempotencyRegistry()
	guard := NewDispatchGuard(reg)

	key := GenerateIdempotencyKey("task-1", "run-1", "nostr_zap_send", 1)

	// First attempt → allowed
	dec1 := guard.ShouldDispatch(key)
	if !dec1.Allowed {
		t.Fatal("first attempt should be allowed")
	}

	// Fails → record failure
	reg.MarkFailed(key.Key, key.Tool, "timeout", 1000)

	// Retry with same key → allowed (failed ops can be retried)
	dec2 := guard.ShouldDispatch(key)
	if !dec2.Allowed {
		t.Fatal("retry after failure should be allowed")
	}
}
