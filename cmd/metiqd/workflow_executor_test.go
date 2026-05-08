package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/store/state"
	taskspkg "metiq/internal/tasks"
)

func TestWorkflowExecutorAgentTurnCreatesChildTaskAndPersistsStepIDs(t *testing.T) {
	ctx := context.Background()
	service := taskspkg.NewService(taskspkg.NewDocsStore(state.NewDocsRepository(newTestStore(), "workflow-executor-test")))
	exec := &workflowExecutor{taskService: service, ledger: service.Ledger()}

	var persisted []*taskspkg.WorkflowRun
	exec.persistStep = func(_ context.Context, run *taskspkg.WorkflowRun) error {
		cp := *run
		cp.Steps = append([]taskspkg.StepRun(nil), run.Steps...)
		persisted = append(persisted, &cp)
		return nil
	}
	service.Ledger().AddObserver(exec)
	service.Ledger().AddObserver(workflowRunCompleter{ledger: service.Ledger()})

	run := &taskspkg.WorkflowRun{
		RunID:      "wf-run-agent",
		WorkflowID: "wf-agent",
		Status:     taskspkg.WorkflowStatusRunning,
		Inputs:     map[string]any{"session_id": "session-wf"},
		Steps: []taskspkg.StepRun{{
			StepID:   "agent",
			StepName: "Agent step",
			Type:     taskspkg.StepTypeAgentTurn,
			Status:   taskspkg.StepStatusRunning,
		}},
	}
	def := &taskspkg.StepDefinition{
		ID:   "agent",
		Name: "Agent step",
		Type: taskspkg.StepTypeAgentTurn,
		Config: taskspkg.StepConfig{
			AgentID:      "builder",
			Instructions: "do the workflow work",
			ToolProfile:  "coding",
		},
	}

	if err := exec.ExecuteStep(ctx, run, &run.Steps[0], def); err != nil {
		t.Fatalf("ExecuteStep: %v", err)
	}
	if run.Steps[0].TaskID == "" || run.Steps[0].RunID == "" {
		t.Fatalf("expected step task/run linkage, got %+v", run.Steps[0])
	}
	if len(persisted) == 0 || persisted[0].Steps[0].TaskID == "" || persisted[0].Steps[0].RunID == "" {
		t.Fatalf("expected persisted step linkage, got %+v", persisted)
	}
	task, runs, err := service.GetTask(ctx, run.Steps[0].TaskID, 10)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != state.TaskStatusCompleted {
		t.Fatalf("task status = %q, want completed", task.Status)
	}
	if task.Meta["workflow_step_type"] != string(taskspkg.StepTypeAgentTurn) {
		t.Fatalf("workflow step metadata missing: %+v", task.Meta)
	}
	if len(runs) != 1 || runs[0].RunID != run.Steps[0].RunID || runs[0].Status != state.TaskRunStatusCompleted {
		t.Fatalf("unexpected child runs: %+v", runs)
	}
	if run.Steps[0].Output["status"] != string(state.TaskRunStatusCompleted) {
		t.Fatalf("expected completed step output, got %+v", run.Steps[0].Output)
	}
}

func TestWorkflowExecutorGatewayAndACPStepsUseCanonicalChildRuns(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		stepType   taskspkg.StepType
		config     taskspkg.StepConfig
		wantMethod string
	}{
		{
			name:     "gateway_call",
			stepType: taskspkg.StepTypeGatewayCall,
			config: taskspkg.StepConfig{
				Method: "health",
				Params: map[string]any{"verbose": true},
			},
			wantMethod: "health",
		},
		{
			name:     "acp_dispatch",
			stepType: taskspkg.StepTypeACPDispatch,
			config: taskspkg.StepConfig{
				PeerPubKey:   strings.Repeat("a", 64),
				Instructions: "delegate this",
			},
			wantMethod: "acp.dispatch",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := taskspkg.NewService(taskspkg.NewDocsStore(state.NewDocsRepository(newTestStore(), "workflow-executor-test")))
			sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
			if err != nil {
				t.Fatalf("new session store: %v", err)
			}
			exec := &workflowExecutor{taskService: service, ledger: service.Ledger(), sessionStore: sessionStore}
			var persisted taskspkg.WorkflowRun
			exec.persistStep = func(_ context.Context, run *taskspkg.WorkflowRun) error {
				persisted = *run
				persisted.Steps = append([]taskspkg.StepRun(nil), run.Steps...)
				return nil
			}
			var gotMethod string
			exec.gatewayCall = func(_ context.Context, method string, params json.RawMessage) (map[string]any, error) {
				gotMethod = method
				if tc.stepType == taskspkg.StepTypeACPDispatch {
					var req struct {
						Wait bool `json:"wait"`
					}
					if err := json.Unmarshal(params, &req); err != nil {
						t.Fatalf("unmarshal ACP params: %v", err)
					}
					if !req.Wait {
						t.Fatal("ACP workflow step must wait for the delegated result")
					}
				}
				return map[string]any{"ok": true, "method": method}, nil
			}

			run := &taskspkg.WorkflowRun{
				RunID:      "wf-run-" + tc.name,
				WorkflowID: "wf-sync",
				Status:     taskspkg.WorkflowStatusRunning,
				Inputs:     map[string]any{"session_id": "session-" + tc.name, "parent_task_id": "parent-" + tc.name},
				Steps: []taskspkg.StepRun{{
					StepID:   "step-" + tc.name,
					StepName: tc.name,
					Type:     tc.stepType,
					Status:   taskspkg.StepStatusRunning,
				}},
			}
			def := &taskspkg.StepDefinition{ID: run.Steps[0].StepID, Name: tc.name, Type: tc.stepType, Config: tc.config}
			if err := exec.ExecuteStep(ctx, run, &run.Steps[0], def); err != nil {
				t.Fatalf("ExecuteStep: %v", err)
			}
			if gotMethod != tc.wantMethod {
				t.Fatalf("gateway method = %q, want %q", gotMethod, tc.wantMethod)
			}
			if run.Steps[0].TaskID == "" || run.Steps[0].RunID == "" {
				t.Fatalf("expected step linkage, got %+v", run.Steps[0])
			}
			if persisted.Steps[0].TaskID != run.Steps[0].TaskID || persisted.Steps[0].RunID != run.Steps[0].RunID {
				t.Fatalf("persisted linkage = %+v, want %+v", persisted.Steps[0], run.Steps[0])
			}
			task, runs, err := service.GetTask(ctx, run.Steps[0].TaskID, 10)
			if err != nil {
				t.Fatalf("GetTask: %v", err)
			}
			if task.Status != state.TaskStatusCompleted {
				t.Fatalf("task status = %q, want completed", task.Status)
			}
			if task.Meta["workflow_step_type"] != string(tc.stepType) {
				t.Fatalf("workflow step type meta = %v, want %s", task.Meta["workflow_step_type"], tc.stepType)
			}
			if len(runs) != 1 || runs[0].Status != state.TaskRunStatusCompleted {
				t.Fatalf("expected completed child run, got %+v", runs)
			}
			sessionEntry, ok := sessionStore.Get("session-" + tc.name)
			if !ok || len(sessionEntry.RecentWorkerEvents) < 2 {
				t.Fatalf("expected buffered worker events, got %+v", sessionEntry)
			}
			lastWorkerEvent := sessionEntry.RecentWorkerEvents[len(sessionEntry.RecentWorkerEvents)-1]
			if lastWorkerEvent.TaskID != run.Steps[0].TaskID || lastWorkerEvent.ParentTaskID != "parent-"+tc.name || lastWorkerEvent.State != "completed" {
				t.Fatalf("unexpected worker event telemetry: %+v", lastWorkerEvent)
			}
		})
	}
}

type workflowRunCompleter struct {
	ledger *taskspkg.Ledger
}

func (c workflowRunCompleter) OnTaskCreated(context.Context, taskspkg.LedgerEntry) {}
func (c workflowRunCompleter) OnTaskUpdated(context.Context, taskspkg.LedgerEntry, state.TaskTransition) {
}
func (c workflowRunCompleter) OnRunUpdated(context.Context, taskspkg.RunEntry, state.TaskRunTransition) {
}

func (c workflowRunCompleter) OnRunCreated(ctx context.Context, entry taskspkg.RunEntry) {
	if c.ledger == nil || entry.Run.Status != state.TaskRunStatusQueued {
		return
	}
	_, _ = c.ledger.UpdateRunStatus(ctx, entry.Run.RunID, state.TaskRunStatusRunning, "test", "test", "started")
	if taskID := strings.TrimSpace(entry.Run.TaskID); taskID != "" {
		_, _ = c.ledger.UpdateTaskStatus(ctx, taskID, state.TaskStatusInProgress, "test", "test", "started")
	}
	_, _ = c.ledger.UpdateRunStatus(ctx, entry.Run.RunID, state.TaskRunStatusCompleted, "test", "test", "completed")
	if taskID := strings.TrimSpace(entry.Run.TaskID); taskID != "" {
		_, _ = c.ledger.UpdateTaskStatus(ctx, taskID, state.TaskStatusCompleted, "test", "test", "completed")
	}
}
