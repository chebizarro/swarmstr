package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func TestControlRPCTasksTraceIncludesVerificationAndWorkerTelemetry(t *testing.T) {
	ctx := context.Background()
	docsRepo := state.NewDocsRepository(newTestStore(), "trace-test")
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	task := state.TaskSpec{
		TaskID:       "task-parent",
		SessionID:    "session-parent",
		Title:        "Parent task",
		Instructions: "coordinate child work",
		Status:       state.TaskStatusInProgress,
		CurrentRunID: "run-parent",
	}
	if _, err := docsRepo.PutTask(ctx, task); err != nil {
		t.Fatalf("put task: %v", err)
	}
	if _, err := docsRepo.PutTaskRun(ctx, state.TaskRun{RunID: "run-parent", TaskID: task.TaskID, SessionID: task.SessionID, Status: state.TaskRunStatusRunning, Attempt: 1, StartedAt: 10}); err != nil {
		t.Fatalf("put task run: %v", err)
	}
	if err := sessionStore.RecordVerificationEvent("verification-session", state.VerificationEventTelemetry{
		Type:      "check_pass",
		TaskID:    task.TaskID,
		RunID:     "run-parent",
		CheckID:   "verify-1",
		CheckType: "evidence",
		Status:    string(state.VerificationStatusPassed),
		CreatedAt: 20,
	}); err != nil {
		t.Fatalf("record verification event: %v", err)
	}
	if err := sessionStore.RecordWorkerEvent("session-parent", state.WorkerEventTelemetry{
		EventID:      "worker-event-1",
		TaskID:       "task-child",
		RunID:        "run-child",
		ParentTaskID: task.TaskID,
		ParentRunID:  "run-parent",
		StepID:       "step-child",
		WorkerID:     "agent-worker",
		State:        "completed",
		Message:      "child complete",
		CreatedAt:    21,
	}); err != nil {
		t.Fatalf("record worker event: %v", err)
	}

	h := newControlRPCHandler(controlRPCDeps{docsRepo: docsRepo, sessionStore: sessionStore})
	params, _ := json.Marshal(methods.TasksTraceRequest{TaskID: task.TaskID, Limit: 20})
	result, handled, err := h.handleTaskRPC(ctx, nostruntime.ControlRPCInbound{FromPubKey: "operator", Params: params}, methods.MethodTasksTrace)
	if err != nil {
		t.Fatalf("tasks.trace: %v", err)
	}
	if !handled {
		t.Fatal("tasks.trace was not handled")
	}
	trace, ok := result.Result.(methods.TasksTraceResponse)
	if !ok {
		t.Fatalf("unexpected result type %T", result.Result)
	}
	var sawVerification, sawDelegation bool
	for _, event := range trace.Events {
		switch event.Kind {
		case methods.TraceKindVerification:
			sawVerification = event.Verification != nil && event.Verification.CheckID == "verify-1"
		case methods.TraceKindDelegation:
			sawDelegation = event.Delegation != nil && event.TaskID == "task-child" && event.Delegation.WorkerID == "agent-worker" && event.Delegation.WorkerState == "completed"
		}
	}
	if !sawVerification || !sawDelegation {
		t.Fatalf("expected verification and delegation events in trace, got %+v", trace.Events)
	}
}
