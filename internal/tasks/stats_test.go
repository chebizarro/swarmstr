package tasks

import (
	"context"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func TestComputeTaskStatsAggregatesTaskAndRunCounters(t *testing.T) {
	now := time.Now()
	tasks := map[string]*LedgerEntry{
		"t1": {Task: state.TaskSpec{TaskID: "t1", Status: state.TaskStatusPending}, Source: TaskSourceCron},
		"t2": {Task: state.TaskSpec{TaskID: "t2", Status: state.TaskStatusCompleted}, Source: TaskSourceManual},
	}
	runs := map[string]*RunEntry{
		"r1": {Run: state.TaskRun{RunID: "r1", Status: state.TaskRunStatusRunning}},
		"r2": {Run: state.TaskRun{RunID: "r2", Status: state.TaskRunStatusCompleted, EndedAt: now.Unix()}},
		"r3": {Run: state.TaskRun{RunID: "r3", Status: state.TaskRunStatusFailed, EndedAt: now.Unix()}},
	}

	stats := computeTaskStats(tasks, runs, now)
	if stats.TotalTasks != 2 || stats.TotalRuns != 3 {
		t.Fatalf("unexpected totals: %+v", stats)
	}
	if stats.ByStatus[string(state.TaskStatusPending)] != 1 || stats.ByStatus[string(state.TaskStatusCompleted)] != 1 {
		t.Fatalf("unexpected by_status: %+v", stats.ByStatus)
	}
	if stats.BySource[string(TaskSourceCron)] != 1 || stats.BySource[string(TaskSourceManual)] != 1 {
		t.Fatalf("unexpected by_source: %+v", stats.BySource)
	}
	if stats.ActiveRuns != 1 || stats.CompletedToday != 1 || stats.FailedToday != 1 {
		t.Fatalf("unexpected run counters: %+v", stats)
	}
}

func TestFileStoreStatsUsesSharedAggregator(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	store.tasks["t1"] = &LedgerEntry{Task: state.TaskSpec{TaskID: "t1", Status: state.TaskStatusPending}, Source: TaskSourceCron}
	store.runs["r1"] = &RunEntry{Run: state.TaskRun{RunID: "r1", Status: state.TaskRunStatusCompleted, EndedAt: time.Now().Unix()}}

	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalTasks != 1 || stats.TotalRuns != 1 || stats.CompletedToday != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}
