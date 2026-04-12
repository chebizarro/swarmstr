package planner

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// ── WorkerState ─────────────────────────────────────────────────────────────

func TestValidWorkerState_AllKnown(t *testing.T) {
	known := []WorkerState{
		WorkerStatePending, WorkerStateAccepted, WorkerStateRejected,
		WorkerStateRunning, WorkerStateProgress, WorkerStateBlocked,
		WorkerStateCompleted, WorkerStateFailed, WorkerStateCancelled,
		WorkerStateTimedOut,
	}
	for _, s := range known {
		if !ValidWorkerState(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
}

func TestValidWorkerState_Unknown(t *testing.T) {
	if ValidWorkerState("bogus") {
		t.Fatal("expected bogus to be invalid")
	}
}

func TestWorkerState_IsTerminal(t *testing.T) {
	terminal := []WorkerState{
		WorkerStateRejected, WorkerStateCompleted, WorkerStateFailed,
		WorkerStateCancelled, WorkerStateTimedOut,
	}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("expected %q to be terminal", s)
		}
	}
	nonTerminal := []WorkerState{
		WorkerStatePending, WorkerStateAccepted, WorkerStateRunning,
		WorkerStateProgress, WorkerStateBlocked,
	}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("expected %q to be non-terminal", s)
		}
	}
}

// ── WorkerTracker basics ────────────────────────────────────────────────────

func TestNewWorkerTracker_Defaults(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	if tr.TaskID() != "task-1" {
		t.Fatalf("got %s", tr.TaskID())
	}
	if tr.RunID() != "run-1" {
		t.Fatalf("got %s", tr.RunID())
	}
	if tr.WorkerID() != "worker-a" {
		t.Fatalf("got %s", tr.WorkerID())
	}
	if tr.State() != WorkerStatePending {
		t.Fatalf("expected pending, got %s", tr.State())
	}
	if len(tr.Events()) != 0 {
		t.Fatal("expected no events")
	}
}

// ── Happy path: pending → accepted → running → progress → completed ────────

func TestWorkerTracker_HappyPath(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()

	_, err := tr.RecordEvent(WorkerStateAccepted, "task accepted", now)
	if err != nil {
		t.Fatal(err)
	}
	if tr.State() != WorkerStateAccepted {
		t.Fatalf("expected accepted, got %s", tr.State())
	}

	_, err = tr.RecordEvent(WorkerStateRunning, "starting work", now+1)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tr.RecordEvent(WorkerStateProgress, "50% done", now+10,
		WithProgress(ProgressInfo{PercentComplete: 0.5, StepCurrent: 2, StepTotal: 4}))
	if err != nil {
		t.Fatal(err)
	}

	evt, err := tr.RecordEvent(WorkerStateCompleted, "all done", now+20,
		WithResultRef("result-abc"),
		WithUsage(state.TaskUsage{TotalTokens: 500, ToolCalls: 5}))
	if err != nil {
		t.Fatal(err)
	}

	if tr.State() != WorkerStateCompleted {
		t.Fatalf("expected completed, got %s", tr.State())
	}
	if evt.ResultRef != "result-abc" {
		t.Fatal("expected result ref")
	}
	if evt.Usage.TotalTokens != 500 {
		t.Fatalf("expected 500 tokens, got %d", evt.Usage.TotalTokens)
	}
	if len(tr.Events()) != 4 {
		t.Fatalf("expected 4 events, got %d", len(tr.Events()))
	}
}

// ── Rejection ───────────────────────────────────────────────────────────────

func TestWorkerTracker_Rejection(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()

	evt, err := tr.RecordEvent(WorkerStateRejected, "cannot handle this", now,
		WithRejectInfo(RejectInfo{Reason: "missing tool: nostr_zap", Recoverable: true, Suggestion: "try agent-b"}))
	if err != nil {
		t.Fatal(err)
	}

	if evt.RejectInfo == nil {
		t.Fatal("expected reject info")
	}
	if evt.RejectInfo.Reason != "missing tool: nostr_zap" {
		t.Fatal("wrong reason")
	}
	if !evt.RejectInfo.Recoverable {
		t.Fatal("expected recoverable")
	}
	if tr.State() != WorkerStateRejected {
		t.Fatalf("expected rejected, got %s", tr.State())
	}
}

// ── Terminal state blocks further transitions ───────────────────────────────

func TestWorkerTracker_TerminalBlocks(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()
	tr.RecordEvent(WorkerStateAccepted, "", now)
	tr.RecordEvent(WorkerStateRunning, "", now+1)
	tr.RecordEvent(WorkerStateCompleted, "done", now+2)

	_, err := tr.RecordEvent(WorkerStateRunning, "try again", now+3)
	if err == nil {
		t.Fatal("expected error for transition from terminal state")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Invalid transitions ─────────────────────────────────────────────────────

func TestWorkerTracker_InvalidTransition(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()

	// pending → running is not allowed (must accept first)
	_, err := tr.RecordEvent(WorkerStateRunning, "skip accept", now)
	if err == nil {
		t.Fatal("expected error for pending → running")
	}
	if !strings.Contains(err.Error(), "illegal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkerTracker_PendingToCompleted(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	_, err := tr.RecordEvent(WorkerStateCompleted, "instant", time.Now().Unix())
	if err == nil {
		t.Fatal("expected error for pending → completed")
	}
}

// ── Heartbeat and timeout ───────────────────────────────────────────────────

func TestWorkerTracker_Heartbeat(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 10*time.Second)
	now := time.Now().Unix()
	tr.Heartbeat(now + 5)
	if tr.LastHeartbeat() != now+5 {
		t.Fatalf("expected heartbeat at %d, got %d", now+5, tr.LastHeartbeat())
	}
}

func TestWorkerTracker_CheckTimeout_NotTimedOut(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()
	tr.Heartbeat(now)

	if tr.CheckTimeout(now + 10) {
		t.Fatal("should not be timed out after 10s with 30s timeout")
	}
}

func TestWorkerTracker_CheckTimeout_TimedOut(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()
	tr.Heartbeat(now)

	if !tr.CheckTimeout(now + 31) {
		t.Fatal("should be timed out after 31s with 30s timeout")
	}
}

func TestWorkerTracker_CheckTimeout_TerminalNotTimedOut(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 10*time.Second)
	now := time.Now().Unix()
	tr.RecordEvent(WorkerStateAccepted, "", now)
	tr.RecordEvent(WorkerStateRunning, "", now+1)
	tr.RecordEvent(WorkerStateCompleted, "done", now+2)

	// Even with old heartbeat, terminal state means no timeout.
	if tr.CheckTimeout(now + 100) {
		t.Fatal("terminal state should not time out")
	}
}

func TestWorkerTracker_CheckTimeout_ZeroDisabled(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 0)
	now := time.Now().Unix()
	if tr.CheckTimeout(now + 999999) {
		t.Fatal("zero timeout should never time out")
	}
}

func TestWorkerTracker_MarkTimedOut(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 10*time.Second)
	now := time.Now().Unix()

	evt, err := tr.MarkTimedOut(now + 20)
	if err != nil {
		t.Fatal(err)
	}
	if evt.State != WorkerStateTimedOut {
		t.Fatalf("expected timed_out, got %s", evt.State)
	}
	if tr.State() != WorkerStateTimedOut {
		t.Fatal("state should be timed_out")
	}
}

func TestWorkerTracker_MarkTimedOut_AlreadyTerminal(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 10*time.Second)
	now := time.Now().Unix()
	tr.RecordEvent(WorkerStateRejected, "no", now)

	_, err := tr.MarkTimedOut(now + 20)
	if err == nil {
		t.Fatal("expected error marking timed out on terminal state")
	}
}

// ── Progress events ─────────────────────────────────────────────────────────

func TestWorkerTracker_MultipleProgress(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()

	tr.RecordEvent(WorkerStateAccepted, "", now)
	tr.RecordEvent(WorkerStateRunning, "", now+1)
	tr.RecordEvent(WorkerStateProgress, "25%", now+5, WithProgress(ProgressInfo{PercentComplete: 0.25}))
	tr.RecordEvent(WorkerStateProgress, "50%", now+10, WithProgress(ProgressInfo{PercentComplete: 0.50}))
	tr.RecordEvent(WorkerStateProgress, "75%", now+15, WithProgress(ProgressInfo{PercentComplete: 0.75}))

	events := tr.Events()
	progressCount := 0
	for _, e := range events {
		if e.State == WorkerStateProgress {
			progressCount++
		}
	}
	if progressCount != 3 {
		t.Fatalf("expected 3 progress events, got %d", progressCount)
	}
}

// ── Blocked/unblocked ───────────────────────────────────────────────────────

func TestWorkerTracker_BlockedAndResumed(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()

	tr.RecordEvent(WorkerStateAccepted, "", now)
	tr.RecordEvent(WorkerStateRunning, "", now+1)
	tr.RecordEvent(WorkerStateBlocked, "waiting for approval", now+5)
	tr.RecordEvent(WorkerStateRunning, "approval received", now+10)
	tr.RecordEvent(WorkerStateCompleted, "done", now+20)

	if tr.State() != WorkerStateCompleted {
		t.Fatalf("expected completed, got %s", tr.State())
	}
	if len(tr.Events()) != 5 {
		t.Fatalf("expected 5 events, got %d", len(tr.Events()))
	}
}

// ── Cancellation ────────────────────────────────────────────────────────────

func TestWorkerTracker_CancelFromPending(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	_, err := tr.RecordEvent(WorkerStateCancelled, "parent cancelled", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if tr.State() != WorkerStateCancelled {
		t.Fatalf("expected cancelled, got %s", tr.State())
	}
}

func TestWorkerTracker_CancelFromRunning(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()
	tr.RecordEvent(WorkerStateAccepted, "", now)
	tr.RecordEvent(WorkerStateRunning, "", now+1)
	_, err := tr.RecordEvent(WorkerStateCancelled, "parent cancelled", now+5)
	if err != nil {
		t.Fatal(err)
	}
}

// ── Failure ─────────────────────────────────────────────────────────────────

func TestWorkerTracker_FailureWithError(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()
	tr.RecordEvent(WorkerStateAccepted, "", now)
	tr.RecordEvent(WorkerStateRunning, "", now+1)

	evt, err := tr.RecordEvent(WorkerStateFailed, "crash", now+5,
		WithError("segfault in tool"),
		WithUsage(state.TaskUsage{TotalTokens: 100}))
	if err != nil {
		t.Fatal(err)
	}
	if evt.Error != "segfault in tool" {
		t.Fatal("expected error message")
	}
	if evt.Usage.TotalTokens != 100 {
		t.Fatal("expected usage")
	}
}

// ── Formatting ──────────────────────────────────────────────────────────────

func TestFormatWorkerEvent_Basic(t *testing.T) {
	evt := WorkerEvent{
		EventID:  "we-1",
		WorkerID: "worker-a",
		State:    WorkerStateProgress,
		Message:  "halfway",
		Progress: &ProgressInfo{PercentComplete: 0.5},
	}
	out := FormatWorkerEvent(evt)
	if !strings.Contains(out, "worker-a") {
		t.Fatal("expected worker ID")
	}
	if !strings.Contains(out, "50%") {
		t.Fatal("expected progress")
	}
}

func TestFormatWorkerTracker_Summary(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()
	tr.RecordEvent(WorkerStateAccepted, "ok", now)

	out := FormatWorkerTracker(tr)
	if !strings.Contains(out, "worker-a") {
		t.Fatal("expected worker ID")
	}
	if !strings.Contains(out, "accepted") {
		t.Fatal("expected state")
	}
}

// ── Concurrency ─────────────────────────────────────────────────────────────

func TestWorkerTracker_ConcurrentAccess(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()
	tr.RecordEvent(WorkerStateAccepted, "", now)
	tr.RecordEvent(WorkerStateRunning, "", now+1)

	var wg sync.WaitGroup
	const n = 20

	// Concurrent heartbeats and reads.
	for i := 0; i < n; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			tr.Heartbeat(now + int64(i))
			tr.State()
			tr.Events()
			tr.LastHeartbeat()
			tr.CheckTimeout(now + int64(i))
		}()
	}
	wg.Wait()
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestWorkerEvent_JSONRoundTrip(t *testing.T) {
	evt := WorkerEvent{
		EventID:   "we-1",
		TaskID:    "task-1",
		RunID:     "run-1",
		WorkerID:  "worker-a",
		State:     WorkerStateProgress,
		Message:   "50% done",
		Progress:  &ProgressInfo{PercentComplete: 0.5, StepCurrent: 2, StepTotal: 4},
		Usage:     state.TaskUsage{TotalTokens: 200},
		CreatedAt: 1000,
	}
	b, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WorkerEvent
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.State != evt.State || decoded.Progress.PercentComplete != 0.5 {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
}

func TestRejectInfo_JSONRoundTrip(t *testing.T) {
	ri := RejectInfo{Reason: "no tools", Recoverable: true, Suggestion: "try B"}
	b, err := json.Marshal(ri)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RejectInfo
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Reason != ri.Reason || decoded.Recoverable != ri.Recoverable {
		t.Fatal("round-trip mismatch")
	}
}

// ── End-to-end ──────────────────────────────────────────────────────────────

func TestEndToEnd_WorkerLifecycle(t *testing.T) {
	tr := NewWorkerTracker("task-1", "run-1", "worker-a", 30*time.Second)
	now := time.Now().Unix()

	// Accept.
	tr.RecordEvent(WorkerStateAccepted, "ready to work", now)

	// Start running.
	tr.RecordEvent(WorkerStateRunning, "starting", now+1)

	// Heartbeats.
	tr.Heartbeat(now + 5)
	tr.Heartbeat(now + 10)
	if tr.CheckTimeout(now + 15) {
		t.Fatal("should not be timed out")
	}

	// Progress.
	tr.RecordEvent(WorkerStateProgress, "step 1 done", now+15,
		WithProgress(ProgressInfo{PercentComplete: 0.33, StepCurrent: 1, StepTotal: 3}))

	// Block and unblock.
	tr.RecordEvent(WorkerStateBlocked, "awaiting approval", now+20)
	tr.RecordEvent(WorkerStateRunning, "approved", now+25)

	// Complete.
	tr.RecordEvent(WorkerStateCompleted, "all done", now+30,
		WithResultRef("result-xyz"),
		WithUsage(state.TaskUsage{TotalTokens: 1000, ToolCalls: 10}))

	if tr.State() != WorkerStateCompleted {
		t.Fatalf("expected completed, got %s", tr.State())
	}
	events := tr.Events()
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d", len(events))
	}
}
