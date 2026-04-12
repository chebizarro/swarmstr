package state

import (
	"testing"
)

func TestAllowedTaskRunTransition_AllBranches(t *testing.T) {
	tests := []struct {
		from TaskRunStatus
		to   TaskRunStatus
		want bool
	}{
		// Empty from → only queued allowed
		{"", TaskRunStatusQueued, true},
		{"", TaskRunStatusRunning, false},
		{"", "", false},

		// Queued transitions
		{TaskRunStatusQueued, TaskRunStatusRunning, true},
		{TaskRunStatusQueued, TaskRunStatusBlocked, true},
		{TaskRunStatusQueued, TaskRunStatusAwaitingApproval, true},
		{TaskRunStatusQueued, TaskRunStatusCancelled, true},
		{TaskRunStatusQueued, TaskRunStatusFailed, true},
		{TaskRunStatusQueued, TaskRunStatusCompleted, false},

		// Running transitions
		{TaskRunStatusRunning, TaskRunStatusBlocked, true},
		{TaskRunStatusRunning, TaskRunStatusAwaitingApproval, true},
		{TaskRunStatusRunning, TaskRunStatusRetrying, true},
		{TaskRunStatusRunning, TaskRunStatusCompleted, true},
		{TaskRunStatusRunning, TaskRunStatusFailed, true},
		{TaskRunStatusRunning, TaskRunStatusCancelled, true},
		{TaskRunStatusRunning, TaskRunStatusQueued, false},

		// Blocked transitions
		{TaskRunStatusBlocked, TaskRunStatusQueued, true},
		{TaskRunStatusBlocked, TaskRunStatusRunning, true},
		{TaskRunStatusBlocked, TaskRunStatusAwaitingApproval, true},
		{TaskRunStatusBlocked, TaskRunStatusRetrying, true},
		{TaskRunStatusBlocked, TaskRunStatusFailed, true},
		{TaskRunStatusBlocked, TaskRunStatusCancelled, true},
		{TaskRunStatusBlocked, TaskRunStatusCompleted, false},

		// AwaitingApproval transitions
		{TaskRunStatusAwaitingApproval, TaskRunStatusQueued, true},
		{TaskRunStatusAwaitingApproval, TaskRunStatusRunning, true},
		{TaskRunStatusAwaitingApproval, TaskRunStatusBlocked, true},
		{TaskRunStatusAwaitingApproval, TaskRunStatusCancelled, true},
		{TaskRunStatusAwaitingApproval, TaskRunStatusFailed, true},
		{TaskRunStatusAwaitingApproval, TaskRunStatusCompleted, false},

		// Retrying transitions
		{TaskRunStatusRetrying, TaskRunStatusQueued, true},
		{TaskRunStatusRetrying, TaskRunStatusRunning, true},
		{TaskRunStatusRetrying, TaskRunStatusBlocked, true},
		{TaskRunStatusRetrying, TaskRunStatusCancelled, true},
		{TaskRunStatusRetrying, TaskRunStatusFailed, true},
		{TaskRunStatusRetrying, TaskRunStatusCompleted, false},

		// Terminal states cannot transition
		{TaskRunStatusCompleted, TaskRunStatusRunning, false},
		{TaskRunStatusFailed, TaskRunStatusRunning, false},
		{TaskRunStatusCancelled, TaskRunStatusRunning, false},

		// Invalid target
		{TaskRunStatusQueued, "bogus", false},
	}

	for _, tc := range tests {
		got := AllowedTaskRunTransition(tc.from, tc.to)
		if got != tc.want {
			t.Errorf("AllowedTaskRunTransition(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestTaskRunApplyTransition(t *testing.T) {
	// Nil run
	var nilRun *TaskRun
	if err := nilRun.ApplyTransition(TaskRunStatusQueued, 1000, "", "", "", nil); err == nil {
		t.Error("expected error for nil run")
	}

	// Start with a queued run
	run := &TaskRun{RunID: "r-1", Status: TaskRunStatusQueued}

	// Same status → error
	if err := run.ApplyTransition(TaskRunStatusQueued, 1001, "", "", "", nil); err == nil {
		t.Error("expected error for same status")
	}

	// Invalid status
	if err := run.ApplyTransition("bogus", 1002, "", "", "", nil); err == nil {
		t.Error("expected error for invalid status")
	}

	// Illegal transition queued→completed
	if err := run.ApplyTransition(TaskRunStatusCompleted, 1003, "", "", "", nil); err == nil {
		t.Error("expected error for illegal transition queued→completed")
	}

	// Valid: queued→running
	if err := run.ApplyTransition(TaskRunStatusRunning, 1004, "agent", "system", "starting", nil); err != nil {
		t.Fatalf("queued→running: %v", err)
	}
	if run.Status != TaskRunStatusRunning {
		t.Errorf("expected running, got %s", run.Status)
	}

	// Valid: running→completed (terminal)
	if err := run.ApplyTransition(TaskRunStatusCompleted, 1005, "", "", "done", nil); err != nil {
		t.Fatalf("running→completed: %v", err)
	}
	if run.EndedAt != 1005 {
		t.Errorf("expected EndedAt=1005, got %d", run.EndedAt)
	}
}

func TestNewTaskRunAttempt(t *testing.T) {
	spec := TaskSpec{
		TaskID:       "task-1",
		Title:        "Test task",
		Instructions: "Do the thing",
		Status:       TaskStatusInProgress,
	}
	run, err := NewTaskRunAttempt(spec, "run-1", nil, 1000, "manual", "agent-1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID != "run-1" {
		t.Errorf("expected run-1, got %s", run.RunID)
	}
	if run.TaskID != "task-1" {
		t.Errorf("expected task-1, got %s", run.TaskID)
	}
	if run.Status != TaskRunStatusQueued {
		t.Errorf("expected queued, got %s", run.Status)
	}
	if run.Attempt != 1 {
		t.Errorf("expected attempt 1, got %d", run.Attempt)
	}

	// Duplicate run ID
	_, err = NewTaskRunAttempt(spec, "run-1", []TaskRun{run}, 1001, "", "", "")
	if err == nil {
		t.Error("expected error for duplicate run_id")
	}

	// Empty run ID
	_, err = NewTaskRunAttempt(spec, "", nil, 1002, "", "", "")
	if err == nil {
		t.Error("expected error for empty run_id")
	}

	// Zero timestamp
	_, err = NewTaskRunAttempt(spec, "run-2", nil, 0, "", "", "")
	if err == nil {
		t.Error("expected error for zero timestamp")
	}

	// Attempt increments
	run2, err := NewTaskRunAttempt(spec, "run-2", []TaskRun{run}, 1003, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if run2.Attempt != 2 {
		t.Errorf("expected attempt 2, got %d", run2.Attempt)
	}
}

func TestAllowedTaskTransition_AllStates(t *testing.T) {
	// Verifying → completed (already tested elsewhere, but checking here for completeness)
	if !AllowedTaskTransition(TaskStatusVerifying, TaskStatusCompleted) {
		t.Error("expected verifying→completed to be allowed")
	}
	// Failed → planned (recovery)
	if !AllowedTaskTransition(TaskStatusFailed, TaskStatusPlanned) {
		t.Error("expected failed→planned to be allowed")
	}
	// Completed → nothing (terminal)
	if AllowedTaskTransition(TaskStatusCompleted, TaskStatusInProgress) {
		t.Error("completed should be terminal")
	}
	// Cancelled → nothing (terminal)
	if AllowedTaskTransition(TaskStatusCancelled, TaskStatusInProgress) {
		t.Error("cancelled should be terminal")
	}
}
