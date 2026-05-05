package tasks

import (
	"context"
	"fmt"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// Suppress unused import warning
var _ = fmt.Sprintf

func TestLedgerCreateTask(t *testing.T) {
	ledger := NewLedger(nil) // in-memory only

	task := state.TaskSpec{
		TaskID:       "task-1",
		Title:        "Test Task",
		Instructions: "Do something",
		Status:       state.TaskStatusPending,
	}

	entry, err := ledger.CreateTask(context.Background(), task, TaskSourceManual, "")
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	if entry.Task.TaskID != "task-1" {
		t.Errorf("expected task ID 'task-1', got %q", entry.Task.TaskID)
	}
	if entry.Source != TaskSourceManual {
		t.Errorf("expected source 'manual', got %q", entry.Source)
	}
	if entry.CreatedAt == 0 {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestLedgerDuplicateTask(t *testing.T) {
	ledger := NewLedger(nil)

	task := state.TaskSpec{
		TaskID:       "task-dup",
		Title:        "Duplicate Task",
		Instructions: "Test",
	}

	_, err := ledger.CreateTask(context.Background(), task, TaskSourceManual, "")
	if err != nil {
		t.Fatalf("first CreateTask failed: %v", err)
	}

	_, err = ledger.CreateTask(context.Background(), task, TaskSourceManual, "")
	if err == nil {
		t.Error("expected error for duplicate task ID")
	}
}

func TestLedgerUpdateTaskStatus(t *testing.T) {
	ledger := NewLedger(nil)
	ctx := context.Background()

	task := state.TaskSpec{
		TaskID:       "task-update",
		Title:        "Update Test",
		Instructions: "Test",
		Status:       state.TaskStatusPending,
	}

	_, err := ledger.CreateTask(ctx, task, TaskSourceManual, "")
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Valid transition: pending -> in_progress
	entry, err := ledger.UpdateTaskStatus(ctx, "task-update", state.TaskStatusInProgress, "test", "test", "starting")
	if err != nil {
		t.Fatalf("UpdateTaskStatus failed: %v", err)
	}
	if entry.Task.Status != state.TaskStatusInProgress {
		t.Errorf("expected status 'in_progress', got %q", entry.Task.Status)
	}
	if len(entry.Task.Transitions) != 1 {
		t.Errorf("expected 1 transition, got %d", len(entry.Task.Transitions))
	}

	// Valid transition: in_progress -> completed
	entry, err = ledger.UpdateTaskStatus(ctx, "task-update", state.TaskStatusCompleted, "test", "test", "done")
	if err != nil {
		t.Fatalf("UpdateTaskStatus failed: %v", err)
	}
	if entry.Task.Status != state.TaskStatusCompleted {
		t.Errorf("expected status 'completed', got %q", entry.Task.Status)
	}
}

func TestLedgerInvalidTransition(t *testing.T) {
	ledger := NewLedger(nil)
	ctx := context.Background()

	task := state.TaskSpec{
		TaskID:       "task-invalid",
		Title:        "Invalid Transition",
		Instructions: "Test",
		Status:       state.TaskStatusPending,
	}

	_, err := ledger.CreateTask(ctx, task, TaskSourceManual, "")
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Invalid transition: pending -> completed (must go through in_progress first)
	_, err = ledger.UpdateTaskStatus(ctx, "task-invalid", state.TaskStatusCompleted, "test", "test", "")
	if err == nil {
		t.Error("expected error for invalid transition")
	}
}

func TestLedgerCreateRun(t *testing.T) {
	ledger := NewLedger(nil)
	ctx := context.Background()

	task := state.TaskSpec{
		TaskID:       "task-run",
		Title:        "Run Test",
		Instructions: "Test",
	}

	_, err := ledger.CreateTask(ctx, task, TaskSourceCron, "cron-1")
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	run, err := ledger.CreateRun(ctx, "task-run", "run-1", "cron", "system", "cron")
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	if run.Run.RunID != "run-1" {
		t.Errorf("expected run ID 'run-1', got %q", run.Run.RunID)
	}
	if run.Run.TaskID != "task-run" {
		t.Errorf("expected task ID 'task-run', got %q", run.Run.TaskID)
	}
	if run.Run.Attempt != 1 {
		t.Errorf("expected attempt 1, got %d", run.Run.Attempt)
	}
	if run.Run.Status != state.TaskRunStatusQueued {
		t.Errorf("expected status 'queued', got %q", run.Run.Status)
	}
}

func TestLedgerCreateTaskReturnsPersistenceError(t *testing.T) {
	store := newRecordingStore()
	store.saveTaskErr = fmt.Errorf("save task failed")
	ledger := NewLedger(store)
	ctx := context.Background()

	task := state.TaskSpec{TaskID: "task-create-fail", Title: "Create Fail", Instructions: "Test"}
	if _, err := ledger.CreateTask(ctx, task, TaskSourceManual, ""); err == nil {
		t.Fatal("expected CreateTask persistence error")
	}
	if _, err := ledger.GetTask(ctx, "task-create-fail"); err == nil {
		t.Fatal("expected task not to be kept in memory after failed persistence")
	}
}

func TestLedgerUpdateRunStatusPersistsTaskSnapshot(t *testing.T) {
	store := newRecordingStore()
	ledger := NewLedger(store)
	ctx := context.Background()

	task := state.TaskSpec{TaskID: "task-persist", Title: "Persist", Instructions: "Test"}
	if _, err := ledger.CreateTask(ctx, task, TaskSourceManual, ""); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := ledger.CreateRun(ctx, "task-persist", "run-persist", "manual", "agent", "test"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	store.resetCounts()

	if _, err := ledger.UpdateRunStatus(ctx, "run-persist", state.TaskRunStatusRunning, "agent", "test", "started"); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	if store.saveRunCalls != 1 {
		t.Fatalf("expected run persistence, got %d", store.saveRunCalls)
	}
	if store.saveTaskCalls != 1 {
		t.Fatalf("expected task persistence for embedded run snapshot, got %d", store.saveTaskCalls)
	}
	storedTask := store.tasks["task-persist"]
	if storedTask == nil {
		t.Fatal("expected stored task entry")
	}
	if storedTask.Task.LastRunID != "run-persist" {
		t.Fatalf("expected LastRunID persisted, got %q", storedTask.Task.LastRunID)
	}
	if len(storedTask.Runs) != 1 || storedTask.Runs[0].Status != state.TaskRunStatusRunning {
		t.Fatalf("expected embedded run snapshot persisted as running: %+v", storedTask.Runs)
	}
}

func TestLedgerMultipleRuns(t *testing.T) {
	ledger := NewLedger(nil)
	ctx := context.Background()

	task := state.TaskSpec{
		TaskID:       "task-multi",
		Title:        "Multi Run Test",
		Instructions: "Test",
	}

	_, _ = ledger.CreateTask(ctx, task, TaskSourceACP, "")

	// First run
	run1, err := ledger.CreateRun(ctx, "task-multi", "run-a", "acp", "agent", "acp")
	if err != nil {
		t.Fatalf("CreateRun 1 failed: %v", err)
	}
	if run1.Run.Attempt != 1 {
		t.Errorf("expected attempt 1, got %d", run1.Run.Attempt)
	}

	// Complete first run
	_, _ = ledger.UpdateRunStatus(ctx, "run-a", state.TaskRunStatusRunning, "agent", "acp", "")
	_, _ = ledger.UpdateRunStatus(ctx, "run-a", state.TaskRunStatusFailed, "agent", "acp", "error")

	// Second run (retry)
	run2, err := ledger.CreateRun(ctx, "task-multi", "run-b", "acp", "agent", "retry")
	if err != nil {
		t.Fatalf("CreateRun 2 failed: %v", err)
	}
	if run2.Run.Attempt != 2 {
		t.Errorf("expected attempt 2, got %d", run2.Run.Attempt)
	}
}

func TestLedgerListTasks(t *testing.T) {
	ledger := NewLedger(nil)
	ctx := context.Background()

	// Create tasks with different statuses and sources
	tasks := []struct {
		id     string
		status state.TaskStatus
		source TaskSource
	}{
		{"task-a", state.TaskStatusPending, TaskSourceCron},
		{"task-b", state.TaskStatusInProgress, TaskSourceACP},
		{"task-c", state.TaskStatusCompleted, TaskSourceCron},
		{"task-d", state.TaskStatusFailed, TaskSourceWebhook},
	}

	for _, tc := range tasks {
		task := state.TaskSpec{
			TaskID:       tc.id,
			Title:        tc.id,
			Instructions: "Test",
			Status:       tc.status,
		}
		_, _ = ledger.CreateTask(ctx, task, tc.source, "")
	}

	// List all
	all, _ := ledger.ListTasks(ctx, ListTasksOptions{})
	if len(all) != 4 {
		t.Errorf("expected 4 tasks, got %d", len(all))
	}

	// Filter by status
	pending, _ := ledger.ListTasks(ctx, ListTasksOptions{
		Status: []state.TaskStatus{state.TaskStatusPending},
	})
	if len(pending) != 1 {
		t.Errorf("expected 1 pending task, got %d", len(pending))
	}

	// Filter by source
	cron, _ := ledger.ListTasks(ctx, ListTasksOptions{
		Source: []TaskSource{TaskSourceCron},
	})
	if len(cron) != 2 {
		t.Errorf("expected 2 cron tasks, got %d", len(cron))
	}

	// Filter by multiple statuses
	terminal, _ := ledger.ListTasks(ctx, ListTasksOptions{
		Status: []state.TaskStatus{state.TaskStatusCompleted, state.TaskStatusFailed},
	})
	if len(terminal) != 2 {
		t.Errorf("expected 2 terminal tasks, got %d", len(terminal))
	}
}

func TestLedgerStats(t *testing.T) {
	ledger := NewLedger(nil)
	ctx := context.Background()

	// Create some tasks and runs
	for i := 0; i < 3; i++ {
		task := state.TaskSpec{
			TaskID:       fmt.Sprintf("task-%d", i),
			Title:        fmt.Sprintf("Task %d", i),
			Instructions: "Test",
		}
		_, _ = ledger.CreateTask(ctx, task, TaskSourceCron, "")
	}

	stats := ledger.Stats(ctx)
	if stats.TotalTasks != 3 {
		t.Errorf("expected 3 tasks, got %d", stats.TotalTasks)
	}
	if stats.BySource["cron"] != 3 {
		t.Errorf("expected 3 cron tasks, got %d", stats.BySource["cron"])
	}
}

func TestLedgerObserver(t *testing.T) {
	ledger := NewLedger(nil)
	ctx := context.Background()

	// Track events
	var events []string
	observer := &testObserver{
		onTaskCreated: func(entry LedgerEntry) {
			events = append(events, "task_created:"+entry.Task.TaskID)
		},
		onTaskUpdated: func(entry LedgerEntry, tr state.TaskTransition) {
			events = append(events, "task_updated:"+entry.Task.TaskID+":"+string(tr.To))
		},
		onRunCreated: func(entry RunEntry) {
			events = append(events, "run_created:"+entry.Run.RunID)
		},
		onRunUpdated: func(entry RunEntry, tr state.TaskRunTransition) {
			events = append(events, "run_updated:"+entry.Run.RunID+":"+string(tr.To))
		},
	}
	ledger.AddObserver(observer)

	task := state.TaskSpec{
		TaskID:       "task-obs",
		Title:        "Observer Test",
		Instructions: "Test",
	}

	_, _ = ledger.CreateTask(ctx, task, TaskSourceManual, "")
	_, _ = ledger.UpdateTaskStatus(ctx, "task-obs", state.TaskStatusInProgress, "test", "test", "")
	_, _ = ledger.CreateRun(ctx, "task-obs", "run-obs", "manual", "test", "test")
	_, _ = ledger.UpdateRunStatus(ctx, "run-obs", state.TaskRunStatusRunning, "test", "test", "")

	expected := []string{
		"task_created:task-obs",
		"task_updated:task-obs:in_progress",
		"run_created:run-obs",
		"run_updated:run-obs:running",
	}

	if len(events) != len(expected) {
		t.Errorf("expected %d events, got %d", len(expected), len(events))
	}
	for i, e := range expected {
		if i >= len(events) {
			break
		}
		if events[i] != e {
			t.Errorf("event %d: expected %q, got %q", i, e, events[i])
		}
	}
}

func TestLedgerCreateRunReturnsPersistenceErrorAndRollsBack(t *testing.T) {
	store := newRecordingStore()
	ledger := NewLedger(store)
	ctx := context.Background()

	task := state.TaskSpec{TaskID: "task-run-fail", Title: "Run Fail", Instructions: "Test"}
	if _, err := ledger.CreateTask(ctx, task, TaskSourceManual, ""); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store.saveRunErr = fmt.Errorf("save run failed")

	if _, err := ledger.CreateRun(ctx, "task-run-fail", "run-run-fail", "manual", "agent", "test"); err == nil {
		t.Fatal("expected CreateRun persistence error")
	}
	if _, err := ledger.GetRun(ctx, "run-run-fail"); err == nil {
		t.Fatal("expected run not to be kept in memory after failed persistence")
	}
	entry, err := ledger.GetTask(ctx, "task-run-fail")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if entry.Task.CurrentRunID != "" || len(entry.Runs) != 0 {
		t.Fatalf("expected task run bookkeeping rolled back, got current=%q runs=%d", entry.Task.CurrentRunID, len(entry.Runs))
	}
}

func TestLedgerUpdateRunStatusReturnsPersistenceErrorAndRollsBack(t *testing.T) {
	store := newRecordingStore()
	ledger := NewLedger(store)
	ctx := context.Background()

	task := state.TaskSpec{TaskID: "task-update-run-fail", Title: "Update Run Fail", Instructions: "Test"}
	if _, err := ledger.CreateTask(ctx, task, TaskSourceManual, ""); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := ledger.CreateRun(ctx, "task-update-run-fail", "run-update-run-fail", "manual", "agent", "test"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	store.saveRunErr = fmt.Errorf("save run failed")

	if _, err := ledger.UpdateRunStatus(ctx, "run-update-run-fail", state.TaskRunStatusRunning, "agent", "test", "start"); err == nil {
		t.Fatal("expected UpdateRunStatus persistence error")
	}
	runEntry, err := ledger.GetRun(ctx, "run-update-run-fail")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if runEntry.Run.Status != state.TaskRunStatusQueued {
		t.Fatalf("expected run status rolled back to queued, got %q", runEntry.Run.Status)
	}
}

func TestLedgerCancelTaskPersistsRunCancellations(t *testing.T) {
	store := newRecordingStore()
	ledger := NewLedger(store)
	ctx := context.Background()

	task := state.TaskSpec{TaskID: "task-cancel-persist", Title: "Cancel Persist", Instructions: "Test"}
	if _, err := ledger.CreateTask(ctx, task, TaskSourceManual, ""); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := ledger.CreateRun(ctx, "task-cancel-persist", "run-cancel-persist", "manual", "agent", "test"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := ledger.UpdateRunStatus(ctx, "run-cancel-persist", state.TaskRunStatusRunning, "agent", "test", "started"); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	store.resetCounts()

	if err := ledger.CancelTask(ctx, "task-cancel-persist", "agent", "stop"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if store.saveRunCalls != 1 {
		t.Fatalf("expected cancelled run persistence, got %d", store.saveRunCalls)
	}
	storedRun := store.runs["run-cancel-persist"]
	if storedRun == nil || storedRun.Run.Status != state.TaskRunStatusCancelled {
		t.Fatalf("expected stored run cancelled, got %+v", storedRun)
	}
	storedTask := store.tasks["task-cancel-persist"]
	if storedTask == nil || len(storedTask.Runs) != 1 || storedTask.Runs[0].Status != state.TaskRunStatusCancelled {
		t.Fatalf("expected stored task embedded run cancelled, got %+v", storedTask)
	}
}

func TestLedgerCancelTaskReturnsPersistenceErrorAndRollsBack(t *testing.T) {
	store := newRecordingStore()
	ledger := NewLedger(store)
	ctx := context.Background()

	task := state.TaskSpec{TaskID: "task-cancel-fail", Title: "Cancel Fail", Instructions: "Test"}
	if _, err := ledger.CreateTask(ctx, task, TaskSourceManual, ""); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := ledger.CreateRun(ctx, "task-cancel-fail", "run-cancel-fail", "manual", "agent", "test"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := ledger.UpdateRunStatus(ctx, "run-cancel-fail", state.TaskRunStatusRunning, "agent", "test", "start"); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	store.saveRunErr = fmt.Errorf("save run failed")

	if err := ledger.CancelTask(ctx, "task-cancel-fail", "agent", "stop"); err == nil {
		t.Fatal("expected CancelTask persistence error")
	}
	entry, err := ledger.GetTask(ctx, "task-cancel-fail")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if entry.Task.Status == state.TaskStatusCancelled {
		t.Fatalf("expected task status rollback, got %q", entry.Task.Status)
	}
	runEntry, err := ledger.GetRun(ctx, "run-cancel-fail")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if runEntry.Run.Status != state.TaskRunStatusRunning {
		t.Fatalf("expected run status rollback to running, got %q", runEntry.Run.Status)
	}
}

func TestLedgerCancelTask(t *testing.T) {
	ledger := NewLedger(nil)
	ctx := context.Background()

	task := state.TaskSpec{
		TaskID:       "task-cancel",
		Title:        "Cancel Test",
		Instructions: "Test",
	}

	_, _ = ledger.CreateTask(ctx, task, TaskSourceManual, "")
	_, _ = ledger.UpdateTaskStatus(ctx, "task-cancel", state.TaskStatusInProgress, "test", "test", "")
	_, _ = ledger.CreateRun(ctx, "task-cancel", "run-cancel", "manual", "test", "test")
	_, _ = ledger.UpdateRunStatus(ctx, "run-cancel", state.TaskRunStatusRunning, "test", "test", "")

	err := ledger.CancelTask(ctx, "task-cancel", "test", "user requested")
	if err != nil {
		t.Fatalf("CancelTask failed: %v", err)
	}

	entry, _ := ledger.GetTask(ctx, "task-cancel")
	if entry.Task.Status != state.TaskStatusCancelled {
		t.Errorf("expected task status 'cancelled', got %q", entry.Task.Status)
	}

	runEntry, _ := ledger.GetRun(ctx, "run-cancel")
	if runEntry.Run.Status != state.TaskRunStatusCancelled {
		t.Errorf("expected run status 'cancelled', got %q", runEntry.Run.Status)
	}
}

func TestLedgerGetTaskLineage(t *testing.T) {
	ledger := NewLedger(nil)
	ctx := context.Background()

	// Create parent task
	parent := state.TaskSpec{
		TaskID:       "parent",
		Title:        "Parent",
		Instructions: "Parent task",
	}
	_, _ = ledger.CreateTask(ctx, parent, TaskSourceManual, "")

	// Create child tasks
	child1 := state.TaskSpec{
		TaskID:       "child-1",
		Title:        "Child 1",
		Instructions: "Child 1",
		ParentTaskID: "parent",
	}
	_, _ = ledger.CreateTask(ctx, child1, TaskSourceACP, "")

	child2 := state.TaskSpec{
		TaskID:       "child-2",
		Title:        "Child 2",
		Instructions: "Child 2",
		ParentTaskID: "parent",
	}
	_, _ = ledger.CreateTask(ctx, child2, TaskSourceACP, "")

	// Create grandchild
	grandchild := state.TaskSpec{
		TaskID:       "grandchild",
		Title:        "Grandchild",
		Instructions: "Grandchild",
		ParentTaskID: "child-1",
	}
	_, _ = ledger.CreateTask(ctx, grandchild, TaskSourceACP, "")

	lineage, err := ledger.GetTaskLineage(ctx, "parent")
	if err != nil {
		t.Fatalf("GetTaskLineage failed: %v", err)
	}

	if len(lineage) != 4 {
		t.Errorf("expected 4 tasks in lineage, got %d", len(lineage))
	}
}

// Helper types

type recordingStore struct {
	tasks map[string]*LedgerEntry
	runs  map[string]*RunEntry

	saveTaskCalls int
	saveRunCalls  int
	saveTaskErr   error
	saveRunErr    error
}

func newRecordingStore() *recordingStore {
	return &recordingStore{tasks: make(map[string]*LedgerEntry), runs: make(map[string]*RunEntry)}
}

func (s *recordingStore) resetCounts() {
	s.saveTaskCalls = 0
	s.saveRunCalls = 0
}

func (s *recordingStore) SaveTask(ctx context.Context, entry *LedgerEntry) error {
	s.saveTaskCalls++
	if s.saveTaskErr != nil {
		return s.saveTaskErr
	}
	if entry == nil {
		return nil
	}
	copyEntry := *entry
	copyEntry.Runs = append([]state.TaskRun(nil), entry.Runs...)
	s.tasks[entry.Task.TaskID] = &copyEntry
	return nil
}

func (s *recordingStore) LoadTask(ctx context.Context, taskID string) (*LedgerEntry, error) {
	return s.tasks[taskID], nil
}

func (s *recordingStore) ListTasks(ctx context.Context, opts ListTasksOptions) ([]*LedgerEntry, error) {
	out := make([]*LedgerEntry, 0, len(s.tasks))
	for _, entry := range s.tasks {
		out = append(out, entry)
	}
	return out, nil
}

func (s *recordingStore) DeleteTask(ctx context.Context, taskID string) error {
	delete(s.tasks, taskID)
	return nil
}

func (s *recordingStore) SaveRun(ctx context.Context, entry *RunEntry) error {
	s.saveRunCalls++
	if s.saveRunErr != nil {
		return s.saveRunErr
	}
	if entry == nil {
		return nil
	}
	copyEntry := *entry
	s.runs[entry.Run.RunID] = &copyEntry
	return nil
}

func (s *recordingStore) LoadRun(ctx context.Context, runID string) (*RunEntry, error) {
	return s.runs[runID], nil
}

func (s *recordingStore) ListRuns(ctx context.Context, opts ListRunsOptions) ([]*RunEntry, error) {
	out := make([]*RunEntry, 0, len(s.runs))
	for _, entry := range s.runs {
		out = append(out, entry)
	}
	return out, nil
}

func (s *recordingStore) Stats(ctx context.Context) (TaskStats, error) {
	return TaskStats{TotalTasks: len(s.tasks), TotalRuns: len(s.runs)}, nil
}

func (s *recordingStore) Prune(ctx context.Context, olderThan time.Duration) (int, error) {
	return 0, nil
}

type testObserver struct {
	onTaskCreated func(LedgerEntry)
	onTaskUpdated func(LedgerEntry, state.TaskTransition)
	onRunCreated  func(RunEntry)
	onRunUpdated  func(RunEntry, state.TaskRunTransition)
}

func (o *testObserver) OnTaskCreated(ctx context.Context, entry LedgerEntry) {
	if o.onTaskCreated != nil {
		o.onTaskCreated(entry)
	}
}

func (o *testObserver) OnTaskUpdated(ctx context.Context, entry LedgerEntry, tr state.TaskTransition) {
	if o.onTaskUpdated != nil {
		o.onTaskUpdated(entry, tr)
	}
}

func (o *testObserver) OnRunCreated(ctx context.Context, entry RunEntry) {
	if o.onRunCreated != nil {
		o.onRunCreated(entry)
	}
}

func (o *testObserver) OnRunUpdated(ctx context.Context, entry RunEntry, tr state.TaskRunTransition) {
	if o.onRunUpdated != nil {
		o.onRunUpdated(entry, tr)
	}
}
