package tasks

import (
	"context"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func TestServiceCreateTaskInitializesLedgerTask(t *testing.T) {
	ctx := context.Background()
	store := newRecordingStore()
	now := time.Unix(1234, 5678)
	svc := NewService(store, WithServiceClock(func() time.Time { return now }))

	entry, err := svc.CreateTask(ctx, state.TaskSpec{
		Title:        "  Test task  ",
		Instructions: "  Do the thing  ",
	}, TaskSourceManual, "control-rpc", "operator")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if entry.Task.TaskID == "" {
		t.Fatal("expected generated task id")
	}
	if entry.Task.Title != "Test task" || entry.Task.Instructions != "Do the thing" {
		t.Fatalf("task was not normalized: %+v", entry.Task)
	}
	if entry.Task.Status != state.TaskStatusPending {
		t.Fatalf("status = %q, want pending", entry.Task.Status)
	}
	if len(entry.Task.Transitions) != 1 || entry.Task.Transitions[0].To != state.TaskStatusPending || entry.Task.Transitions[0].Source != string(TaskSourceManual) {
		t.Fatalf("unexpected initial transition: %+v", entry.Task.Transitions)
	}
	if store.saveTaskCalls != 1 {
		t.Fatalf("saveTaskCalls = %d, want 1", store.saveTaskCalls)
	}
}

func TestServiceResumeTaskCreatesQueuedRunAndMarksReady(t *testing.T) {
	ctx := context.Background()
	store := newRecordingStore()
	now := time.Unix(2000, 99)
	svc := NewService(store, WithServiceClock(func() time.Time { return now }))

	created, err := svc.CreateTask(ctx, state.TaskSpec{
		TaskID:       "task-1",
		Title:        "Test task",
		Instructions: "Do the thing",
	}, TaskSourceManual, "", "operator")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	run, updated, err := svc.ResumeTask(ctx, created.Task.TaskID, "", "operator", "go")
	if err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	if run == nil || run.Run.RunID == "" || run.Run.TaskID != created.Task.TaskID {
		t.Fatalf("unexpected run: %+v", run)
	}
	if run.Run.Status != state.TaskRunStatusQueued {
		t.Fatalf("run status = %q, want queued", run.Run.Status)
	}
	if updated == nil || updated.Task.Status != state.TaskStatusReady || updated.Task.CurrentRunID != run.Run.RunID {
		t.Fatalf("unexpected task after resume: %+v", updated)
	}

	task, runs, err := svc.GetTask(ctx, created.Task.TaskID, 20)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != state.TaskStatusReady || len(runs) != 1 || runs[0].RunID != run.Run.RunID {
		t.Fatalf("unexpected GetTask payload task=%+v runs=%+v", task, runs)
	}
}

func TestServiceResumeTaskApprovalDecisions(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(3000, 42)

	t.Run("approved creates queued run", func(t *testing.T) {
		store := newRecordingStore()
		svc := NewService(store, WithServiceClock(func() time.Time { return now }))
		created, err := svc.CreateTask(ctx, state.TaskSpec{TaskID: "task-approved", Title: "Approve", Instructions: "Do it", Status: state.TaskStatusAwaitingApproval}, TaskSourceManual, "", "operator")
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		run, updated, err := svc.ResumeTask(ctx, created.Task.TaskID, ResumeDecisionApproved, "operator", "looks good")
		if err != nil {
			t.Fatalf("ResumeTask approved: %v", err)
		}
		if run == nil || run.Run.Status != state.TaskRunStatusQueued || run.Run.Trigger != string(ResumeDecisionApproved) {
			t.Fatalf("unexpected approved run: %+v", run)
		}
		if updated == nil || updated.Task.Status != state.TaskStatusReady || updated.Task.CurrentRunID != run.Run.RunID {
			t.Fatalf("unexpected approved task: %+v", updated)
		}
	})

	t.Run("rejected blocks without creating run", func(t *testing.T) {
		store := newRecordingStore()
		svc := NewService(store, WithServiceClock(func() time.Time { return now }))
		created, err := svc.CreateTask(ctx, state.TaskSpec{TaskID: "task-rejected", Title: "Reject", Instructions: "Do it", Status: state.TaskStatusAwaitingApproval}, TaskSourceManual, "", "operator")
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		store.resetCounts()

		run, updated, err := svc.ResumeTask(ctx, created.Task.TaskID, ResumeDecisionRejected, "operator", "not safe")
		if err != nil {
			t.Fatalf("ResumeTask rejected: %v", err)
		}
		if run != nil {
			t.Fatalf("rejected decision created run: %+v", run)
		}
		if updated == nil || updated.Task.Status != state.TaskStatusBlocked || updated.Task.CurrentRunID != "" {
			t.Fatalf("unexpected rejected task: %+v", updated)
		}
		if store.saveRunCalls != 0 || len(store.runs) != 0 {
			t.Fatalf("rejected should not persist runs: saveRunCalls=%d runs=%+v", store.saveRunCalls, store.runs)
		}
	})

	t.Run("amended creates queued run and records note", func(t *testing.T) {
		store := newRecordingStore()
		svc := NewService(store, WithServiceClock(func() time.Time { return now }))
		created, err := svc.CreateTask(ctx, state.TaskSpec{TaskID: "task-amended", Title: "Amend", Instructions: "Do it", Status: state.TaskStatusAwaitingApproval}, TaskSourceManual, "", "operator")
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		run, updated, err := svc.ResumeTask(ctx, created.Task.TaskID, ResumeDecisionAmended, "operator", "use safer plan")
		if err != nil {
			t.Fatalf("ResumeTask amended: %v", err)
		}
		if run == nil || run.Run.Trigger != string(ResumeDecisionAmended) {
			t.Fatalf("unexpected amended run: %+v", run)
		}
		if updated == nil || updated.Task.Status != state.TaskStatusReady {
			t.Fatalf("unexpected amended task: %+v", updated)
		}
		if got := updated.Task.Meta["approval_decision"]; got != string(ResumeDecisionAmended) {
			t.Fatalf("approval_decision meta = %#v", got)
		}
		if got := updated.Task.Meta["amendment_note"]; got != "use safer plan" {
			t.Fatalf("amendment_note meta = %#v", got)
		}
	})
}

func TestServiceCancelTaskUsesLedger(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newRecordingStore())
	created, err := svc.CreateTask(ctx, state.TaskSpec{TaskID: "task-1", Title: "Test task", Instructions: "Do the thing"}, TaskSourceManual, "", "operator")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, _, err := svc.ResumeTask(ctx, created.Task.TaskID, ResumeDecisionResume, "operator", "go"); err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	if err := svc.CancelTask(ctx, created.Task.TaskID, "operator", "stop"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	task, runs, err := svc.GetTask(ctx, created.Task.TaskID, 20)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != state.TaskStatusCancelled {
		t.Fatalf("task status = %q, want cancelled", task.Status)
	}
	if len(runs) != 1 || runs[0].Status != state.TaskRunStatusCancelled {
		t.Fatalf("runs = %+v, want one cancelled run", runs)
	}
}
