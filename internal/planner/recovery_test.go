package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// ── Mock fetcher ─────────────────────────────────────────────────────────────

type mockRunFetcher struct {
	tasks    map[string]state.TaskSpec
	journals map[string]state.WorkflowJournalDoc
}

func newMockFetcher() *mockRunFetcher {
	return &mockRunFetcher{
		tasks:    map[string]state.TaskSpec{},
		journals: map[string]state.WorkflowJournalDoc{},
	}
}

func (m *mockRunFetcher) ListTaskRuns(_ context.Context, _ string, _ int) ([]state.TaskRun, error) {
	return nil, nil
}

func (m *mockRunFetcher) GetTask(_ context.Context, taskID string) (state.TaskSpec, error) {
	t, ok := m.tasks[taskID]
	if !ok {
		return state.TaskSpec{}, fmt.Errorf("not found")
	}
	return t, nil
}

func (m *mockRunFetcher) GetWorkflowJournal(_ context.Context, runID string) (state.WorkflowJournalDoc, error) {
	j, ok := m.journals[runID]
	if !ok {
		return state.WorkflowJournalDoc{}, fmt.Errorf("not found")
	}
	return j, nil
}

// ── Helper builders ──────────────────────────────────────────────────────────

func runningRun(runID, taskID string, startedAt int64) state.TaskRun {
	return state.TaskRun{
		Version:   1,
		RunID:     runID,
		TaskID:    taskID,
		Attempt:   1,
		Status:    state.TaskRunStatusRunning,
		StartedAt: startedAt,
		Transitions: []state.TaskRunTransition{
			{To: state.TaskRunStatusQueued, At: startedAt - 1},
			{From: state.TaskRunStatusQueued, To: state.TaskRunStatusRunning, At: startedAt},
		},
	}
}

func queuedRun(runID, taskID string, at int64) state.TaskRun {
	return state.TaskRun{
		Version: 1,
		RunID:   runID,
		TaskID:  taskID,
		Attempt: 1,
		Status:  state.TaskRunStatusQueued,
		Transitions: []state.TaskRunTransition{
			{To: state.TaskRunStatusQueued, At: at},
		},
	}
}

func completedRun(runID, taskID string) state.TaskRun {
	return state.TaskRun{
		Version:   1,
		RunID:     runID,
		TaskID:    taskID,
		Attempt:   1,
		Status:    state.TaskRunStatusCompleted,
		StartedAt: 1000,
		EndedAt:   2000,
	}
}

func journalWithCheckpoint(runID, taskID, stepID string) state.WorkflowJournalDoc {
	return state.WorkflowJournalDoc{
		Version: 1,
		TaskID:  taskID,
		RunID:   runID,
		Checkpoint: &state.WorkflowCheckpointDoc{
			StepID:    stepID,
			Attempt:   1,
			Status:    "running",
			CreatedAt: time.Now().Unix(),
		},
		NextSeq: 5,
	}
}

func journalWithoutCheckpoint(runID, taskID string) state.WorkflowJournalDoc {
	return state.WorkflowJournalDoc{
		Version: 1,
		TaskID:  taskID,
		RunID:   runID,
		Entries: []state.WorkflowJournalEntryDoc{
			{EntryID: "je-1", Sequence: 1, Type: "step_start", CreatedAt: 100},
		},
		NextSeq: 2,
	}
}

func fixedTime(unix int64) func() time.Time {
	return func() time.Time { return time.Unix(unix, 0) }
}

// ── DetectOrphans tests ─────────────────────────────────────────────────────

func TestDetectOrphans_NoRuns(t *testing.T) {
	fetcher := newMockFetcher()
	cfg := DefaultRecoveryConfig()
	orphans := DetectOrphans(context.Background(), fetcher, nil, cfg)
	if len(orphans) != 0 {
		t.Fatalf("expected no orphans, got %d", len(orphans))
	}
}

func TestDetectOrphans_SkipsTerminalRuns(t *testing.T) {
	fetcher := newMockFetcher()
	cfg := DefaultRecoveryConfig()
	runs := []state.TaskRun{
		completedRun("run-1", "task-1"),
		{Version: 1, RunID: "run-2", TaskID: "task-2", Attempt: 1, Status: state.TaskRunStatusFailed},
		{Version: 1, RunID: "run-3", TaskID: "task-3", Attempt: 1, Status: state.TaskRunStatusCancelled},
	}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)
	if len(orphans) != 0 {
		t.Fatalf("expected no orphans for terminal runs, got %d", len(orphans))
	}
}

func TestDetectOrphans_DetectsRunning(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	fetcher.tasks["task-1"] = state.TaskSpec{Version: 1, TaskID: "task-1", Title: "Test Task", Status: state.TaskStatusInProgress}
	fetcher.journals["run-1"] = journalWithCheckpoint("run-1", "task-1", "s1")

	cfg := DefaultRecoveryConfig()
	cfg.Now = fixedTime(now)

	runs := []state.TaskRun{runningRun("run-1", "task-1", now-60)}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	o := orphans[0]
	if o.Run.RunID != "run-1" {
		t.Fatalf("expected run-1, got %s", o.Run.RunID)
	}
	if o.Reason != OrphanReasonDaemonRestart {
		t.Fatalf("expected daemon_restart reason, got %s", o.Reason)
	}
	if o.Action != RecoveryResume {
		t.Fatalf("expected resume action, got %s", o.Action)
	}
	if o.Checkpoint == nil {
		t.Fatal("expected checkpoint")
	}
	if o.Task.TaskID != "task-1" {
		t.Fatalf("expected task-1, got %s", o.Task.TaskID)
	}
}

func TestDetectOrphans_DetectsQueued(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	cfg := DefaultRecoveryConfig()
	cfg.Now = fixedTime(now)

	runs := []state.TaskRun{queuedRun("run-1", "task-1", now-10)}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	// Queued with no checkpoint → fail
	if orphans[0].Action != RecoveryFail {
		t.Fatalf("expected fail for queued without checkpoint, got %s", orphans[0].Action)
	}
}

func TestDetectOrphans_DetectsBlocked(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	fetcher.journals["run-1"] = journalWithCheckpoint("run-1", "task-1", "s2")
	cfg := DefaultRecoveryConfig()
	cfg.Now = fixedTime(now)

	runs := []state.TaskRun{
		{Version: 1, RunID: "run-1", TaskID: "task-1", Attempt: 1, Status: state.TaskRunStatusBlocked, StartedAt: now - 30},
	}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0].Action != RecoveryResume {
		t.Fatalf("expected resume for blocked with checkpoint, got %s", orphans[0].Action)
	}
}

func TestDetectOrphans_DetectsRetrying(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	cfg := DefaultRecoveryConfig()
	cfg.Now = fixedTime(now)

	runs := []state.TaskRun{
		{Version: 1, RunID: "run-1", TaskID: "task-1", Attempt: 2, Status: state.TaskRunStatusRetrying, StartedAt: now - 5},
	}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
}

func TestDetectOrphans_NoCheckpointFails(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	fetcher.journals["run-1"] = journalWithoutCheckpoint("run-1", "task-1")

	cfg := DefaultRecoveryConfig()
	cfg.Now = fixedTime(now)

	runs := []state.TaskRun{runningRun("run-1", "task-1", now-60)}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0].Action != RecoveryFail {
		t.Fatalf("expected fail for no checkpoint, got %s", orphans[0].Action)
	}
	if !strings.Contains(orphans[0].ActionNote, "no checkpoint") {
		t.Fatalf("expected no checkpoint note, got %s", orphans[0].ActionNote)
	}
}

func TestDetectOrphans_NoJournalFails(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	cfg := DefaultRecoveryConfig()
	cfg.Now = fixedTime(now)

	runs := []state.TaskRun{runningRun("run-1", "task-1", now-60)}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if orphans[0].Action != RecoveryFail {
		t.Fatalf("expected fail for missing journal, got %s", orphans[0].Action)
	}
}

func TestDetectOrphans_StaleRunFails(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	fetcher.journals["run-1"] = journalWithCheckpoint("run-1", "task-1", "s1")

	cfg := DefaultRecoveryConfig()
	cfg.MaxOrphanAge = 1 * time.Hour
	cfg.Now = fixedTime(now)

	// Run started 2 hours ago
	runs := []state.TaskRun{runningRun("run-1", "task-1", now-7200)}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if orphans[0].Reason != OrphanReasonStale {
		t.Fatalf("expected stale reason, got %s", orphans[0].Reason)
	}
	if orphans[0].Action != RecoveryFail {
		t.Fatalf("expected fail for stale run, got %s", orphans[0].Action)
	}
}

func TestDetectOrphans_StaleButWithinAge(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	fetcher.journals["run-1"] = journalWithCheckpoint("run-1", "task-1", "s1")

	cfg := DefaultRecoveryConfig()
	cfg.MaxOrphanAge = 1 * time.Hour
	cfg.Now = fixedTime(now)

	// Run started 30 minutes ago — within limit
	runs := []state.TaskRun{runningRun("run-1", "task-1", now-1800)}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if orphans[0].Reason != OrphanReasonDaemonRestart {
		t.Fatalf("expected daemon_restart, got %s", orphans[0].Reason)
	}
	if orphans[0].Action != RecoveryResume {
		t.Fatalf("expected resume, got %s", orphans[0].Action)
	}
}

func TestDetectOrphans_DisabledAgeCheck(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	fetcher.journals["run-1"] = journalWithCheckpoint("run-1", "task-1", "s1")

	cfg := DefaultRecoveryConfig()
	cfg.MaxOrphanAge = 0 // disabled
	cfg.Now = fixedTime(now)

	// Run started 1 week ago — should still be resumable since age check disabled
	runs := []state.TaskRun{runningRun("run-1", "task-1", now-604800)}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if orphans[0].Action != RecoveryResume {
		t.Fatalf("expected resume with age check disabled, got %s", orphans[0].Action)
	}
}

// ── AutoResume / AutoFail configuration ─────────────────────────────────────

func TestDetectOrphans_AutoResumeDisabled(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	fetcher.journals["run-1"] = journalWithCheckpoint("run-1", "task-1", "s1")

	cfg := DefaultRecoveryConfig()
	cfg.AutoResume = false
	cfg.Now = fixedTime(now)

	runs := []state.TaskRun{runningRun("run-1", "task-1", now-60)}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if orphans[0].Action != RecoveryRequiresAttention {
		t.Fatalf("expected requires_attention when auto-resume disabled, got %s", orphans[0].Action)
	}
	if !strings.Contains(orphans[0].ActionNote, "operator approval") {
		t.Fatalf("expected operator approval note, got %s", orphans[0].ActionNote)
	}
}

func TestDetectOrphans_AutoFailDisabled(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()

	cfg := DefaultRecoveryConfig()
	cfg.AutoFail = false
	cfg.Now = fixedTime(now)

	runs := []state.TaskRun{runningRun("run-1", "task-1", now-60)}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if orphans[0].Action != RecoveryRequiresAttention {
		t.Fatalf("expected requires_attention when auto-fail disabled, got %s", orphans[0].Action)
	}
	if !strings.Contains(orphans[0].ActionNote, "operator review") {
		t.Fatalf("expected operator review note, got %s", orphans[0].ActionNote)
	}
}

// ── Multiple orphans ────────────────────────────────────────────────────────

func TestDetectOrphans_MultipleRuns(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()
	fetcher.journals["run-1"] = journalWithCheckpoint("run-1", "task-1", "s1")
	// run-2 has no journal/checkpoint

	cfg := DefaultRecoveryConfig()
	cfg.Now = fixedTime(now)

	runs := []state.TaskRun{
		runningRun("run-1", "task-1", now-60),
		runningRun("run-2", "task-2", now-30),
		completedRun("run-3", "task-3"), // should be skipped
	}
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)

	if len(orphans) != 2 {
		t.Fatalf("expected 2 orphans, got %d", len(orphans))
	}
	// run-1 has checkpoint → resume
	if orphans[0].Action != RecoveryResume {
		t.Fatalf("expected resume for run-1, got %s", orphans[0].Action)
	}
	// run-2 has no checkpoint → fail
	if orphans[1].Action != RecoveryFail {
		t.Fatalf("expected fail for run-2, got %s", orphans[1].Action)
	}
}

// ── RecoveryExecutor ────────────────────────────────────────────────────────

func TestRecoveryExecutor_Counts(t *testing.T) {
	cfg := DefaultRecoveryConfig()
	cfg.Now = fixedTime(time.Now().Unix())
	exec := NewRecoveryExecutor(cfg)

	orphans := []OrphanedRun{
		{Run: state.TaskRun{RunID: "r1"}, Action: RecoveryResume, ActionNote: "resuming"},
		{Run: state.TaskRun{RunID: "r2"}, Action: RecoveryFail, ActionNote: "failed"},
		{Run: state.TaskRun{RunID: "r3"}, Action: RecoveryResume, ActionNote: "resuming"},
		{Run: state.TaskRun{RunID: "r4"}, Action: RecoveryRequiresAttention, ActionNote: "review"},
	}

	summary := exec.Execute(orphans)
	if summary.Resumed != 2 {
		t.Fatalf("expected 2 resumed, got %d", summary.Resumed)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected 1 failed, got %d", summary.Failed)
	}
	if summary.NeedAttention != 1 {
		t.Fatalf("expected 1 need_attention, got %d", summary.NeedAttention)
	}
	if len(summary.Orphans) != 4 {
		t.Fatalf("expected 4 orphans in summary, got %d", len(summary.Orphans))
	}
}

func TestRecoveryExecutor_EmptyOrphans(t *testing.T) {
	cfg := DefaultRecoveryConfig()
	exec := NewRecoveryExecutor(cfg)
	summary := exec.Execute(nil)
	if summary.Resumed != 0 || summary.Failed != 0 || summary.NeedAttention != 0 {
		t.Fatal("expected all zeros for empty orphans")
	}
}

// ── PrepareResume ───────────────────────────────────────────────────────────

func TestPrepareResume_WithJournal(t *testing.T) {
	journal := journalWithCheckpoint("run-1", "task-1", "s1")
	orphan := OrphanedRun{
		Run:     runningRun("run-1", "task-1", 1000),
		Journal: &journal,
	}

	j := PrepareResume(orphan, nil)
	if j == nil {
		t.Fatal("expected journal")
	}
	if j.TaskID() != "task-1" {
		t.Fatalf("expected task-1, got %s", j.TaskID())
	}
	if j.RunID() != "run-1" {
		t.Fatalf("expected run-1, got %s", j.RunID())
	}

	cp := j.LatestCheckpoint()
	if cp == nil {
		t.Fatal("expected checkpoint")
	}
	if cp.StepID != "s1" {
		t.Fatalf("expected step s1, got %s", cp.StepID)
	}
}

func TestPrepareResume_WithoutJournal(t *testing.T) {
	orphan := OrphanedRun{
		Run: runningRun("run-1", "task-1", 1000),
	}
	j := PrepareResume(orphan, nil)
	if j != nil {
		t.Fatal("expected nil journal when no journal doc")
	}
}

func TestPrepareResume_ContinuesAppending(t *testing.T) {
	journal := journalWithCheckpoint("run-1", "task-1", "s1")
	journal.Entries = []state.WorkflowJournalEntryDoc{
		{EntryID: "je-1", Sequence: 1, Type: "step_start", CreatedAt: 100},
	}
	journal.NextSeq = 5

	orphan := OrphanedRun{
		Run:     runningRun("run-1", "task-1", 1000),
		Journal: &journal,
	}

	j := PrepareResume(orphan, nil)
	j.Append(context.Background(), JournalStepComplete, "resumed step", nil)

	entries := j.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[1].Sequence <= 5 {
		t.Fatalf("expected sequence > 5, got %d", entries[1].Sequence)
	}
}

// ── MarkFailed ──────────────────────────────────────────────────────────────

func TestMarkFailed_TransitionsToFailed(t *testing.T) {
	run := runningRun("run-1", "task-1", 1000)
	err := MarkFailed(&run, "orphan recovery: no checkpoint", 2000, "recovery_engine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != state.TaskRunStatusFailed {
		t.Fatalf("expected failed status, got %s", run.Status)
	}
	if run.EndedAt != 2000 {
		t.Fatalf("expected ended_at=2000, got %d", run.EndedAt)
	}
	last := run.Transitions[len(run.Transitions)-1]
	if last.Source != "recovery" {
		t.Fatalf("expected source=recovery, got %s", last.Source)
	}
	if last.Meta["recovery"] != true {
		t.Fatal("expected recovery=true in meta")
	}
}

func TestMarkFailed_NilRun(t *testing.T) {
	err := MarkFailed(nil, "reason", 1000, "actor")
	if err == nil {
		t.Fatal("expected error for nil run")
	}
}

func TestMarkFailed_AlreadyFailed(t *testing.T) {
	run := state.TaskRun{Version: 1, RunID: "run-1", TaskID: "task-1", Attempt: 1, Status: state.TaskRunStatusFailed}
	err := MarkFailed(&run, "double fail", 1000, "actor")
	if err == nil {
		t.Fatal("expected error for already-failed run")
	}
}

// ── runAgeSince ─────────────────────────────────────────────────────────────

func TestRunAgeSince_UsesLastTransition(t *testing.T) {
	run := state.TaskRun{
		StartedAt: 1000,
		Transitions: []state.TaskRunTransition{
			{At: 1000},
			{At: 1500},
		},
	}
	age := runAgeSince(run, 2000)
	expected := 500 * time.Second
	if age != expected {
		t.Fatalf("expected %v, got %v", expected, age)
	}
}

func TestRunAgeSince_FallsBackToStartedAt(t *testing.T) {
	run := state.TaskRun{StartedAt: 1000}
	age := runAgeSince(run, 2000)
	if age != 1000*time.Second {
		t.Fatalf("expected 1000s, got %v", age)
	}
}

func TestRunAgeSince_ZeroWhenNoTimestamps(t *testing.T) {
	run := state.TaskRun{}
	age := runAgeSince(run, 2000)
	if age != 0 {
		t.Fatalf("expected 0, got %v", age)
	}
}

// ── Formatting ──────────────────────────────────────────────────────────────

func TestFormatRecoverySummary_NoOrphans(t *testing.T) {
	s := RecoverySummary{ScannedAt: 1000}
	out := FormatRecoverySummary(s)
	if !strings.Contains(out, "No orphaned runs") {
		t.Fatalf("expected no orphans message, got: %s", out)
	}
}

func TestFormatRecoverySummary_WithOrphans(t *testing.T) {
	s := RecoverySummary{
		Orphans: []OrphanedRun{
			{Run: state.TaskRun{RunID: "r1", TaskID: "t1", Status: state.TaskRunStatusRunning}, Action: RecoveryResume, ActionNote: "resuming from s1"},
			{Run: state.TaskRun{RunID: "r2", TaskID: "t2", Status: state.TaskRunStatusRunning}, Action: RecoveryFail, ActionNote: "no checkpoint"},
		},
		Resumed:   1,
		Failed:    1,
		ScannedAt: 1000,
	}
	out := FormatRecoverySummary(s)
	if !strings.Contains(out, "✅ Resumed: 1") {
		t.Fatal("expected resumed count")
	}
	if !strings.Contains(out, "❌ Failed: 1") {
		t.Fatal("expected failed count")
	}
	if !strings.Contains(out, "run=r1") {
		t.Fatal("expected run r1 details")
	}
}

func TestFormatOrphanedRun_WithCheckpoint(t *testing.T) {
	o := OrphanedRun{
		Run:        state.TaskRun{RunID: "run-1", TaskID: "task-1", Status: state.TaskRunStatusRunning, Attempt: 1},
		Task:       state.TaskSpec{TaskID: "task-1", Title: "Test Task", Status: state.TaskStatusInProgress},
		Checkpoint: &state.WorkflowCheckpointDoc{StepID: "s1", Attempt: 1, Status: "running"},
		Reason:     OrphanReasonDaemonRestart,
		Action:     RecoveryResume,
		ActionNote: "resuming",
	}
	out := FormatOrphanedRun(o)
	if !strings.Contains(out, "run-1") {
		t.Fatal("expected run ID")
	}
	if !strings.Contains(out, "step=s1") {
		t.Fatal("expected checkpoint step")
	}
	if !strings.Contains(out, "Test Task") {
		t.Fatal("expected task title")
	}
}

func TestFormatOrphanedRun_WithoutCheckpoint(t *testing.T) {
	o := OrphanedRun{
		Run:    state.TaskRun{RunID: "run-1", TaskID: "task-1", Status: state.TaskRunStatusRunning, Attempt: 1},
		Reason: OrphanReasonNoCheckpoint,
		Action: RecoveryFail,
	}
	out := FormatOrphanedRun(o)
	if !strings.Contains(out, "Checkpoint: none") {
		t.Fatal("expected no checkpoint indicator")
	}
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestOrphanedRun_JSONRoundTrip(t *testing.T) {
	o := OrphanedRun{
		Run:        state.TaskRun{RunID: "run-1", TaskID: "task-1", Attempt: 1, Status: state.TaskRunStatusRunning},
		Task:       state.TaskSpec{TaskID: "task-1", Title: "T1"},
		Checkpoint: &state.WorkflowCheckpointDoc{StepID: "s1", Attempt: 1, Status: "running"},
		Reason:     OrphanReasonDaemonRestart,
		Action:     RecoveryResume,
		ActionNote: "ok",
		DetectedAt: 1000,
	}
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var decoded OrphanedRun
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Run.RunID != "run-1" || decoded.Reason != OrphanReasonDaemonRestart {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
}

func TestRecoverySummary_JSONRoundTrip(t *testing.T) {
	s := RecoverySummary{
		Resumed:   2,
		Failed:    1,
		ScannedAt: 1000,
		Duration:  500 * time.Millisecond,
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RecoverySummary
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Resumed != 2 || decoded.Failed != 1 {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
}

// ── End-to-end: detect → execute → resume ───────────────────────────────────

func TestEndToEnd_DetectExecuteResume(t *testing.T) {
	now := time.Now().Unix()
	fetcher := newMockFetcher()

	// Task and journal for run-1 (resumable)
	fetcher.tasks["task-1"] = state.TaskSpec{Version: 1, TaskID: "task-1", Title: "Resumable Task", Status: state.TaskStatusInProgress}
	fetcher.journals["run-1"] = journalWithCheckpoint("run-1", "task-1", "s2")

	// Task for run-2 (no checkpoint)
	fetcher.tasks["task-2"] = state.TaskSpec{Version: 1, TaskID: "task-2", Title: "Lost Task", Status: state.TaskStatusInProgress}

	cfg := DefaultRecoveryConfig()
	cfg.Now = fixedTime(now)

	runs := []state.TaskRun{
		runningRun("run-1", "task-1", now-120),
		runningRun("run-2", "task-2", now-300),
		completedRun("run-3", "task-3"),
	}

	// Detect
	orphans := DetectOrphans(context.Background(), fetcher, runs, cfg)
	if len(orphans) != 2 {
		t.Fatalf("expected 2 orphans, got %d", len(orphans))
	}

	// Execute
	exec := NewRecoveryExecutor(cfg)
	summary := exec.Execute(orphans)
	if summary.Resumed != 1 || summary.Failed != 1 {
		t.Fatalf("expected 1 resumed + 1 failed, got %d/%d", summary.Resumed, summary.Failed)
	}

	// Resume the resumable one
	var resumable *OrphanedRun
	for i, o := range orphans {
		if o.Action == RecoveryResume {
			resumable = &orphans[i]
			break
		}
	}
	if resumable == nil {
		t.Fatal("expected a resumable orphan")
	}

	j := PrepareResume(*resumable, nil)
	if j == nil {
		t.Fatal("expected journal for resume")
	}
	cp := j.LatestCheckpoint()
	if cp.StepID != "s2" {
		t.Fatalf("expected step s2, got %s", cp.StepID)
	}

	// Append recovery entry
	j.Append(context.Background(), JournalStateTransition, "recovered: resuming from checkpoint", nil)
	if j.Len() != 1 { // journal doc had no entries, only checkpoint
		t.Fatalf("expected 1 entry after resume append, got %d", j.Len())
	}

	// Mark the failed one
	var failed *OrphanedRun
	for i, o := range orphans {
		if o.Action == RecoveryFail {
			failed = &orphans[i]
			break
		}
	}
	if failed == nil {
		t.Fatal("expected a failed orphan")
	}
	err := MarkFailed(&failed.Run, failed.ActionNote, now, "recovery_engine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if failed.Run.Status != state.TaskRunStatusFailed {
		t.Fatalf("expected failed status, got %s", failed.Run.Status)
	}
}

// ── DefaultRecoveryConfig ───────────────────────────────────────────────────

func TestDefaultRecoveryConfig_Values(t *testing.T) {
	cfg := DefaultRecoveryConfig()
	if cfg.MaxOrphanAge != 24*time.Hour {
		t.Fatalf("expected 24h, got %v", cfg.MaxOrphanAge)
	}
	if !cfg.AutoResume {
		t.Fatal("expected auto-resume enabled")
	}
	if !cfg.AutoFail {
		t.Fatal("expected auto-fail enabled")
	}
	if cfg.Now != nil {
		t.Fatal("expected nil Now func")
	}
}

func TestRecoveryConfig_NowDefault(t *testing.T) {
	cfg := DefaultRecoveryConfig()
	before := time.Now()
	got := cfg.now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatal("default now() not returning current time")
	}
}
