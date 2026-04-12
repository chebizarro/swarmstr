package planner

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// These integration tests validate the recovery contract by simulating full
// workflows that are interrupted and then recovered using the journal, orphan
// detection, idempotency, and retry subsystems together.

// ── Scenario 1: Crash during local run, resume from checkpoint ──────────────

func TestIntegration_CrashDuringLocalRun_ResumeFromCheckpoint(t *testing.T) {
	ctx := context.Background()

	// Phase 1: normal run with journal and checkpoint.
	var persistedDoc state.WorkflowJournalDoc
	persister := func(_ context.Context, doc state.WorkflowJournalDoc) error {
		persistedDoc = doc
		return nil
	}

	journal := NewWorkflowJournalWithPersister("task-1", "run-1", persister)
	journal.Append(ctx, JournalStateTransition, "queued → running", nil)
	journal.Append(ctx, JournalStepStart, "step s1", nil)
	journal.Append(ctx, JournalToolDispatch, "nostr_fetch", map[string]any{"tool": "nostr_fetch"})
	journal.Append(ctx, JournalToolResult, "fetched 5 events", nil)
	journal.Checkpoint(ctx, WorkflowCheckpoint{
		StepID:  "s1",
		Attempt: 1,
		Status:  "running",
		Usage:   state.TaskUsage{TotalTokens: 200, ToolCalls: 1},
		PendingActions: []PendingAction{
			{ActionID: "pa-1", Type: "tool_call", Description: "nostr_publish"},
		},
	})
	// Simulate crash — journal is gone, but persistedDoc is available.

	// Phase 2: boot-time orphan detection.
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	fetcher.tasks["task-1"] = state.TaskSpec{
		Version: 1, TaskID: "task-1", Title: "Test Task",
		Status: state.TaskStatusInProgress,
	}
	fetcher.journals["run-1"] = persistedDoc

	run := runningRun("run-1", "task-1", now-120)
	cfg := DefaultRecoveryConfig()
	cfg.Now = fixedTime(now)

	orphans := DetectOrphans(ctx, fetcher, []state.TaskRun{run}, cfg)
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0].Action != RecoveryResume {
		t.Fatalf("expected resume, got %s", orphans[0].Action)
	}

	// Phase 3: resume.
	resumed := PrepareResume(orphans[0], persister)
	if resumed == nil {
		t.Fatal("expected journal for resume")
	}
	cp := resumed.LatestCheckpoint()
	if cp.StepID != "s1" {
		t.Fatalf("expected step s1, got %s", cp.StepID)
	}
	if len(cp.PendingActions) != 1 {
		t.Fatalf("expected 1 pending action, got %d", len(cp.PendingActions))
	}

	// Phase 4: continue execution from checkpoint.
	resumed.Append(ctx, JournalStateTransition, "recovered: resuming from checkpoint", nil)
	resumed.Append(ctx, JournalToolDispatch, "nostr_publish (resumed)", nil)
	resumed.Append(ctx, JournalToolResult, "published evt-abc", nil)
	resumed.Append(ctx, JournalStepComplete, "s1 completed", nil)
	resumed.Checkpoint(ctx, WorkflowCheckpoint{
		StepID:  "s1",
		Attempt: 1,
		Status:  "completed",
		Usage:   state.TaskUsage{TotalTokens: 400, ToolCalls: 2},
	})

	finalCP := resumed.LatestCheckpoint()
	if finalCP.Status != "completed" {
		t.Fatalf("expected completed, got %s", finalCP.Status)
	}
	if finalCP.Usage.TotalTokens != 400 {
		t.Fatalf("expected 400 tokens, got %d", finalCP.Usage.TotalTokens)
	}
}

// ── Scenario 2: Crash with no checkpoint → fail ─────────────────────────────

func TestIntegration_CrashNoCheckpoint_FailsGracefully(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Unix()

	fetcher := newMockFetcher()
	fetcher.tasks["task-1"] = state.TaskSpec{
		Version: 1, TaskID: "task-1", Title: "Lost Task",
		Status: state.TaskStatusInProgress,
	}
	// Journal exists but has no checkpoint
	fetcher.journals["run-1"] = journalWithoutCheckpoint("run-1", "task-1")

	run := runningRun("run-1", "task-1", now-60)
	cfg := DefaultRecoveryConfig()
	cfg.Now = fixedTime(now)

	orphans := DetectOrphans(ctx, fetcher, []state.TaskRun{run}, cfg)
	if orphans[0].Action != RecoveryFail {
		t.Fatalf("expected fail, got %s", orphans[0].Action)
	}

	// Mark as failed.
	err := MarkFailed(&orphans[0].Run, orphans[0].ActionNote, now, "recovery")
	if err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if orphans[0].Run.Status != state.TaskRunStatusFailed {
		t.Fatalf("expected failed status, got %s", orphans[0].Run.Status)
	}

	// Verify the run has recovery metadata.
	last := orphans[0].Run.Transitions[len(orphans[0].Run.Transitions)-1]
	if last.Source != "recovery" {
		t.Fatalf("expected recovery source, got %s", last.Source)
	}
}

// ── Scenario 3: Duplicate delivery does not duplicate side effects ──────────

func TestIntegration_DuplicateDelivery_IdempotencyProtects(t *testing.T) {
	registry := NewIdempotencyRegistry()
	guard := NewDispatchGuard(registry)

	// First delivery: tool dispatch allowed.
	key1 := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 1)
	dec1 := guard.ShouldDispatch(key1)
	if !dec1.Allowed {
		t.Fatal("first dispatch should be allowed")
	}

	// Tool succeeds.
	registry.MarkCompleted(key1.Key, key1.Tool, "evt-abc", time.Now().Unix())

	// Duplicate delivery with same key.
	dec2 := guard.ShouldDispatch(key1)
	if dec2.Allowed {
		t.Fatal("duplicate dispatch should be blocked")
	}
	if dec2.PriorOutcome == nil || dec2.PriorOutcome.ResultRef != "evt-abc" {
		t.Fatal("expected prior outcome with result ref")
	}

	// Different tool in same run is still allowed (different key).
	key2 := GenerateIdempotencyKey("task-1", "run-1", "nostr_send_dm", 2)
	dec3 := guard.ShouldDispatch(key2)
	if !dec3.Allowed {
		t.Fatal("different tool should be allowed")
	}
}

// ── Scenario 4: Crash between side effect and completion write ──────────────

func TestIntegration_CrashBetweenSideEffectAndCompletion(t *testing.T) {
	registry := NewIdempotencyRegistry()
	guard := NewDispatchGuard(registry)

	// Phase 1: dispatch side-effectful tool.
	key := GenerateIdempotencyKey("task-1", "run-1", "nostr_zap_send", 3)
	dec := guard.ShouldDispatch(key)
	if !dec.Allowed {
		t.Fatal("first zap dispatch should be allowed")
	}

	// Simulate: zap executes successfully but we crash before recording completion.
	// On recovery, the registry is empty (not persisted yet).

	// Phase 2: recovery — restore registry from persisted outcomes.
	// In real code, outcomes would be persisted alongside the journal checkpoint.
	// Here we simulate that the outcome WAS persisted.
	registry2 := NewIdempotencyRegistry()
	registry2.RestoreOutcomes([]IdempotencyOutcome{
		{Key: key.Key, Tool: "nostr_zap_send", Status: "completed", ResultRef: "zap-123", CompletedAt: 1000},
	})
	guard2 := NewDispatchGuard(registry2)

	// Replay the same dispatch → blocked.
	dec2 := guard2.ShouldDispatch(key)
	if dec2.Allowed {
		t.Fatal("replay after restore should be blocked")
	}
	if dec2.PriorOutcome.ResultRef != "zap-123" {
		t.Fatal("expected restored zap result ref")
	}

	// Without restore (empty registry), the replay would succeed — this is
	// the failure mode we protect against by persisting outcomes.
	guard3 := NewDispatchGuard(NewIdempotencyRegistry())
	dec3 := guard3.ShouldDispatch(key)
	if !dec3.Allowed {
		t.Fatal("without restored outcomes, dispatch should be allowed (unsafe)")
	}
}

// ── Scenario 5: Multi-step workflow with retry on failure ────────────────────

func TestIntegration_MultiStepWorkflow_RetryOnFailure(t *testing.T) {
	ctx := context.Background()
	retryEngine := NewRetryEngine(DefaultRetryPolicy())

	var persistedDoc state.WorkflowJournalDoc
	persister := func(_ context.Context, doc state.WorkflowJournalDoc) error {
		persistedDoc = doc
		return nil
	}

	journal := NewWorkflowJournalWithPersister("task-1", "run-1", persister)

	// Step 1: fetch (pure, retryable).
	journal.Append(ctx, JournalStepStart, "step s1: fetch data", nil)
	journal.Append(ctx, JournalToolDispatch, "nostr_fetch", nil)

	// Simulate transient failure.
	dec1 := retryEngine.Evaluate("connection timeout", 1, "nostr_fetch")
	if !dec1.ShouldRetry() {
		t.Fatal("expected retry for transient fetch failure")
	}
	journal.Append(ctx, JournalError, fmt.Sprintf("nostr_fetch failed: timeout (retry in %s)", dec1.BackoffDelay), nil)

	// Retry succeeds.
	journal.Append(ctx, JournalToolDispatch, "nostr_fetch (retry)", nil)
	journal.Append(ctx, JournalToolResult, "fetched 10 events", nil)
	journal.Append(ctx, JournalStepComplete, "s1 done", nil)

	// Step 2: publish (side-effectful).
	journal.Append(ctx, JournalStepStart, "step s2: publish result", nil)
	journal.Append(ctx, JournalToolDispatch, "nostr_publish", nil)

	// Simulate transient failure on side-effectful tool.
	dec2 := retryEngine.Evaluate("connection reset", 1, "nostr_publish")
	if dec2.Action != RetryActionEscalate {
		t.Fatalf("expected escalate for side-effectful tool failure, got %s", dec2.Action)
	}
	journal.Append(ctx, JournalError, "nostr_publish failed: escalating (side-effectful)", nil)

	journal.Checkpoint(ctx, WorkflowCheckpoint{
		StepID:  "s2",
		Attempt: 1,
		Status:  "blocked",
		Usage:   state.TaskUsage{TotalTokens: 500, ToolCalls: 3},
	})

	// Verify journal state.
	// 10 entries: 2 step_start + 3 tool_dispatch + 1 tool_result + 2 error + 1 step_complete + 1 checkpoint
	if journal.Len() != 10 {
		t.Fatalf("expected 10 entries, got %d", journal.Len())
	}
	cp := journal.LatestCheckpoint()
	if cp.Status != "blocked" {
		t.Fatalf("expected blocked, got %s", cp.Status)
	}

	// Verify the persisted doc is complete.
	if len(persistedDoc.Entries) != 10 {
		t.Fatalf("expected 10 persisted entries, got %d", len(persistedDoc.Entries))
	}
}

// ── Scenario 6: Stale orphan detection and cleanup ──────────────────────────

func TestIntegration_StaleOrphan_Cleanup(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Unix()

	fetcher := newMockFetcher()
	fetcher.tasks["task-1"] = state.TaskSpec{Version: 1, TaskID: "task-1", Title: "Ancient Task"}
	fetcher.journals["run-1"] = journalWithCheckpoint("run-1", "task-1", "s1")

	cfg := DefaultRecoveryConfig()
	cfg.MaxOrphanAge = 1 * time.Hour
	cfg.Now = fixedTime(now)

	// Run started 3 hours ago.
	run := runningRun("run-1", "task-1", now-10800)
	orphans := DetectOrphans(ctx, fetcher, []state.TaskRun{run}, cfg)

	if orphans[0].Reason != OrphanReasonStale {
		t.Fatalf("expected stale, got %s", orphans[0].Reason)
	}
	if orphans[0].Action != RecoveryFail {
		t.Fatalf("expected fail for stale orphan, got %s", orphans[0].Action)
	}

	// Execute recovery.
	exec := NewRecoveryExecutor(cfg)
	summary := exec.Execute(orphans)
	if summary.Failed != 1 {
		t.Fatalf("expected 1 failed, got %d", summary.Failed)
	}

	// Actually mark as failed.
	err := MarkFailed(&orphans[0].Run, "stale orphan", now, "recovery")
	if err != nil {
		t.Fatal(err)
	}
	if orphans[0].Run.Status != state.TaskRunStatusFailed {
		t.Fatalf("expected failed, got %s", orphans[0].Run.Status)
	}
}

// ── Scenario 7: Mixed orphan batch ──────────────────────────────────────────

func TestIntegration_MixedOrphanBatch(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Unix()

	fetcher := newMockFetcher()
	fetcher.tasks["task-1"] = state.TaskSpec{Version: 1, TaskID: "task-1", Title: "Resumable"}
	fetcher.tasks["task-2"] = state.TaskSpec{Version: 1, TaskID: "task-2", Title: "Lost"}
	fetcher.tasks["task-3"] = state.TaskSpec{Version: 1, TaskID: "task-3", Title: "Ancient"}
	fetcher.journals["run-1"] = journalWithCheckpoint("run-1", "task-1", "s1")
	// run-2 has no journal
	fetcher.journals["run-3"] = journalWithCheckpoint("run-3", "task-3", "s2")

	cfg := DefaultRecoveryConfig()
	cfg.MaxOrphanAge = 1 * time.Hour
	cfg.Now = fixedTime(now)

	runs := []state.TaskRun{
		runningRun("run-1", "task-1", now-120),    // recent with checkpoint → resume
		runningRun("run-2", "task-2", now-300),    // no checkpoint → fail
		runningRun("run-3", "task-3", now-7200),   // stale → fail
		completedRun("run-4", "task-4"),           // terminal → skip
	}

	orphans := DetectOrphans(ctx, fetcher, runs, cfg)
	if len(orphans) != 3 {
		t.Fatalf("expected 3 orphans, got %d", len(orphans))
	}

	exec := NewRecoveryExecutor(cfg)
	summary := exec.Execute(orphans)
	if summary.Resumed != 1 {
		t.Fatalf("expected 1 resumed, got %d", summary.Resumed)
	}
	if summary.Failed != 2 {
		t.Fatalf("expected 2 failed, got %d", summary.Failed)
	}

	// Format summary for observability.
	out := FormatRecoverySummary(summary)
	if !strings.Contains(out, "✅ Resumed: 1") {
		t.Fatal("expected resumed in summary")
	}
	if !strings.Contains(out, "❌ Failed: 2") {
		t.Fatal("expected failed in summary")
	}
}

// ── Scenario 8: Registry restore preserves completed outcomes ───────────────

func TestIntegration_RegistryRestore_PreservesCompletedOutcomes(t *testing.T) {
	// Simulate persisted outcomes from a prior run.
	outcomes := []IdempotencyOutcome{
		{Key: "idem-001", Tool: "nostr_publish", Status: "completed", ResultRef: "evt-1"},
		{Key: "idem-002", Tool: "nostr_send_dm", Status: "completed", ResultRef: "evt-2"},
		{Key: "idem-003", Tool: "nostr_zap_send", Status: "failed", Error: "timeout"},
	}

	registry := NewIdempotencyRegistry()
	registry.RestoreOutcomes(outcomes)
	guard := NewDispatchGuard(registry)

	// Completed keys → blocked.
	key1 := IdempotencyKey{Key: "idem-001", Tool: "nostr_publish"}
	if guard.ShouldDispatch(key1).Allowed {
		t.Fatal("completed key should be blocked")
	}
	key2 := IdempotencyKey{Key: "idem-002", Tool: "nostr_send_dm"}
	if guard.ShouldDispatch(key2).Allowed {
		t.Fatal("completed key should be blocked")
	}

	// Failed key → allowed (retry).
	key3 := IdempotencyKey{Key: "idem-003", Tool: "nostr_zap_send"}
	dec := guard.ShouldDispatch(key3)
	if !dec.Allowed {
		t.Fatal("failed key should allow retry")
	}
	if !strings.Contains(dec.Reason, "retry allowed") {
		t.Fatalf("expected retry message, got: %s", dec.Reason)
	}
}

// ── Scenario 9: Retry engine + idempotency integration ──────────────────────

func TestIntegration_RetryWithIdempotency(t *testing.T) {
	registry := NewIdempotencyRegistry()
	guard := NewDispatchGuard(registry)
	retryEngine := NewRetryEngine(DefaultRetryPolicy())

	// Pure tool: retry allowed.
	key1 := GenerateIdempotencyKey("task-1", "run-1", "nostr_fetch", 1)
	dec := guard.ShouldDispatch(key1)
	if !dec.Allowed {
		t.Fatal("pure tool dispatch should be allowed")
	}

	// Simulate failure → retry engine says retry.
	rDec := retryEngine.Evaluate("connection timeout", 1, "nostr_fetch")
	if !rDec.ShouldRetry() {
		t.Fatal("expected retry for pure tool transient failure")
	}

	// Side-effectful tool: retry engine says escalate.
	key2 := GenerateIdempotencyKey("task-1", "run-1", "nostr_publish", 2)
	dec2 := guard.ShouldDispatch(key2)
	if !dec2.Allowed {
		t.Fatal("first side-effectful dispatch should be allowed")
	}
	rDec2 := retryEngine.Evaluate("connection timeout", 1, "nostr_publish")
	if rDec2.Action != RetryActionEscalate {
		t.Fatalf("expected escalate, got %s", rDec2.Action)
	}
}
