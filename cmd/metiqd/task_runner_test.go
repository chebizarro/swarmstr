package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/autoreply"
	"metiq/internal/store/state"
	taskspkg "metiq/internal/tasks"
)

func TestTaskRunnerRoutesAllowedQueuedRunSources(t *testing.T) {
	ctx := context.Background()
	allowed := []taskspkg.TaskSource{
		taskspkg.TaskSourceManual,
		taskspkg.TaskSourceWorkflow,
		taskspkg.TaskSourceCron,
		taskspkg.TaskSourceWebhook,
	}

	for _, source := range allowed {
		t.Run(string(source), func(t *testing.T) {
			task := state.TaskSpec{
				TaskID:        "task-" + string(source),
				Title:         "Runnable task",
				Instructions:  "Run this queued task",
				SessionID:     "session-" + string(source),
				AssignedAgent: "builder",
			}
			runner, queued := newTestTaskRunner(t, ctx, task)

			runner.OnRunCreated(ctx, taskspkg.RunEntry{
				Run: state.TaskRun{
					RunID:  "run-" + string(source),
					TaskID: task.TaskID,
					Status: state.TaskRunStatusQueued,
				},
				Source:    source,
				SourceRef: "source-ref",
			})

			if len(*queued) != 1 {
				t.Fatalf("queued dispatch count = %d, want 1", len(*queued))
			}
			got := (*queued)[0]
			if got.Source != source || got.SourceRef != "source-ref" {
				t.Fatalf("unexpected source metadata: %+v", got)
			}
			if got.SessionID != task.SessionID {
				t.Fatalf("session id = %q, want %q", got.SessionID, task.SessionID)
			}
			if got.AgentID != defaultAgentID(task.AssignedAgent) {
				t.Fatalf("agent id = %q, want %q", got.AgentID, defaultAgentID(task.AssignedAgent))
			}
		})
	}
}

func TestTaskRunnerIgnoresACPAndNonLocalQueuedSources(t *testing.T) {
	ctx := context.Background()
	disallowed := []taskspkg.TaskSource{
		taskspkg.TaskSourceACP,
		taskspkg.TaskSourceDVM,
		taskspkg.TaskSourceApproval,
		taskspkg.TaskSourceSandbox,
		"",
	}

	for _, source := range disallowed {
		t.Run(string(source), func(t *testing.T) {
			task := state.TaskSpec{TaskID: "task-filter", Title: "Filtered task", Instructions: "Do not run"}
			runner, queued := newTestTaskRunner(t, ctx, task)

			runner.OnRunCreated(ctx, taskspkg.RunEntry{
				Run: state.TaskRun{
					RunID:  "run-filter",
					TaskID: task.TaskID,
					Status: state.TaskRunStatusQueued,
				},
				Source: source,
			})

			if len(*queued) != 0 {
				t.Fatalf("queued dispatch count = %d, want 0", len(*queued))
			}
		})
	}
}

func TestTaskRunnerIgnoresNonQueuedRuns(t *testing.T) {
	ctx := context.Background()
	task := state.TaskSpec{TaskID: "task-nonqueued", Title: "Running task", Instructions: "Already running"}
	runner, queued := newTestTaskRunner(t, ctx, task)

	runner.OnRunCreated(ctx, taskspkg.RunEntry{
		Run: state.TaskRun{
			RunID:  "run-nonqueued",
			TaskID: task.TaskID,
			Status: state.TaskRunStatusRunning,
		},
		Source: taskspkg.TaskSourceManual,
	})

	if len(*queued) != 0 {
		t.Fatalf("queued dispatch count = %d, want 0", len(*queued))
	}
}

func TestTaskRunnerIgnoresSynchronousWorkflowStepTasks(t *testing.T) {
	ctx := context.Background()
	for _, stepType := range []string{string(taskspkg.StepTypeACPDispatch), string(taskspkg.StepTypeGatewayCall)} {
		t.Run(stepType, func(t *testing.T) {
			task := state.TaskSpec{
				TaskID:       "task-" + stepType,
				Title:        "Synchronous workflow child",
				Instructions: "handled by workflow executor",
				Meta:         map[string]any{"workflow_step_type": stepType},
			}
			runner, queued := newTestTaskRunner(t, ctx, task)

			runner.OnRunCreated(ctx, taskspkg.RunEntry{
				Run: state.TaskRun{
					RunID:  "run-" + stepType,
					TaskID: task.TaskID,
					Status: state.TaskRunStatusQueued,
				},
				Source: taskspkg.TaskSourceWorkflow,
			})

			if len(*queued) != 0 {
				t.Fatalf("queued dispatch count = %d, want 0", len(*queued))
			}
		})
	}
}

func TestTaskRunnerRoutesRunUpdatedToQueued(t *testing.T) {
	ctx := context.Background()
	task := state.TaskSpec{TaskID: "task-requeued", Title: "Requeued task", Instructions: "Run after unblock"}
	runner, queued := newTestTaskRunner(t, ctx, task)

	runner.OnRunUpdated(ctx, taskspkg.RunEntry{
		Run: state.TaskRun{
			RunID:  "run-requeued",
			TaskID: task.TaskID,
			Status: state.TaskRunStatusQueued,
		},
		Source: taskspkg.TaskSourceManual,
	}, state.TaskRunTransition{From: state.TaskRunStatusBlocked, To: state.TaskRunStatusQueued})

	if len(*queued) != 1 {
		t.Fatalf("queued dispatch count = %d, want 1", len(*queued))
	}

	runner.OnRunUpdated(ctx, taskspkg.RunEntry{
		Run: state.TaskRun{
			RunID:  "run-requeued-ignored",
			TaskID: task.TaskID,
			Status: state.TaskRunStatusQueued,
		},
		Source: taskspkg.TaskSourceManual,
	}, state.TaskRunTransition{From: state.TaskRunStatusQueued, To: state.TaskRunStatusRunning})

	if len(*queued) != 1 {
		t.Fatalf("queued dispatch count after non-queued transition = %d, want 1", len(*queued))
	}
}

func TestTaskRunnerDefaultsSessionAndRunAgent(t *testing.T) {
	ctx := context.Background()
	task := state.TaskSpec{TaskID: "task-defaults", Title: "Defaults", Instructions: "Use defaults", AssignedAgent: "task-agent"}
	runner, queued := newTestTaskRunner(t, ctx, task)

	runner.OnRunCreated(ctx, taskspkg.RunEntry{
		Run: state.TaskRun{
			RunID:   "run-defaults",
			TaskID:  task.TaskID,
			AgentID: "run-agent",
			Status:  state.TaskRunStatusQueued,
		},
		Source: taskspkg.TaskSourceManual,
	})

	if len(*queued) != 1 {
		t.Fatalf("queued dispatch count = %d, want 1", len(*queued))
	}
	got := (*queued)[0]
	if got.SessionID != "task:"+task.TaskID {
		t.Fatalf("session id = %q, want task:%s", got.SessionID, task.TaskID)
	}
	if got.AgentID != defaultAgentID("run-agent") {
		t.Fatalf("agent id = %q, want %q", got.AgentID, defaultAgentID("run-agent"))
	}
}

func TestTaskRunnerExecutesQueuedRunThroughAgentPipeline(t *testing.T) {
	ctx := context.Background()
	captured := make(chan agent.Turn, 1)
	runner, queued, docsRepo, sessionStore := newExecutableTestTaskRunner(t, taskRunnerRuntimeFunc(func(_ context.Context, turn agent.Turn) (agent.TurnResult, error) {
		captured <- turn
		if turn.ToolEventSink == nil {
			t.Fatalf("expected tool lifecycle sink")
		}
		turn.ToolEventSink(agent.ToolLifecycleEvent{
			Type:       agent.ToolLifecycleEventResult,
			TS:         time.Now().UnixMilli(),
			SessionID:  turn.SessionID,
			TurnID:     turn.TurnID,
			ToolCallID: "call-1",
			ToolName:   "memory_search",
			Result:     "ok",
			Trace:      turn.Trace,
		})
		return agent.TurnResult{
			Text:         "done",
			Usage:        agent.TurnUsage{InputTokens: 7, OutputTokens: 5},
			HistoryDelta: []agent.ConversationMessage{{Role: "assistant", Content: "done"}},
		}, nil
	}))

	runner.executeQueuedRun(ctx, queued)

	select {
	case turn := <-captured:
		if turn.SessionID != queued.SessionID || turn.TurnID != queued.Run.RunID {
			t.Fatalf("unexpected turn identity: %+v", turn)
		}
		if turn.UserText != queued.Task.Instructions {
			t.Fatalf("turn user text = %q, want %q", turn.UserText, queued.Task.Instructions)
		}
		if turn.Trace.TaskID != queued.Task.TaskID || turn.Trace.RunID != queued.Run.RunID || turn.Trace.GoalID != queued.Task.GoalID {
			t.Fatalf("trace not bound to task/run: %+v", turn.Trace)
		}
		if !strings.Contains(turn.Context, "Task title: Executable task") {
			t.Fatalf("expected task context in turn context, got %q", turn.Context)
		}
	default:
		t.Fatal("runtime was not invoked")
	}

	gotRun, err := docsRepo.GetTaskRun(ctx, queued.Run.RunID)
	if err != nil {
		t.Fatalf("GetTaskRun: %v", err)
	}
	if gotRun.Status != state.TaskRunStatusCompleted || gotRun.StartedAt == 0 || gotRun.EndedAt == 0 {
		t.Fatalf("expected completed run with start/end timestamps, got %+v", gotRun)
	}
	if gotRun.Usage.PromptTokens != 7 || gotRun.Usage.CompletionTokens != 5 || gotRun.Usage.TotalTokens != 12 {
		t.Fatalf("unexpected run usage: %+v", gotRun.Usage)
	}
	gotTask, err := docsRepo.GetTask(ctx, queued.Task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if gotTask.Status != state.TaskStatusCompleted || gotTask.CurrentRunID != "" || gotTask.LastRunID != queued.Run.RunID {
		t.Fatalf("expected completed task with cleared current run, got %+v", gotTask)
	}
	entry, ok := sessionStore.Get(queued.SessionID)
	if !ok || entry.LastTurn == nil {
		t.Fatalf("expected turn telemetry in session store, got %+v", entry)
	}
	if entry.LastTurn.TaskID != queued.Task.TaskID || entry.LastTurn.RunID != queued.Run.RunID {
		t.Fatalf("expected trace-linked turn telemetry, got %+v", entry.LastTurn)
	}
	if len(entry.RecentToolLifecycle) != 1 || entry.RecentToolLifecycle[0].TaskID != queued.Task.TaskID || entry.RecentToolLifecycle[0].RunID != queued.Run.RunID {
		t.Fatalf("expected trace-linked tool lifecycle telemetry, got %+v", entry.RecentToolLifecycle)
	}
	if entry.LastTaskResult.Kind != "transcript_entry" || entry.LastCompletedTaskID != queued.Task.TaskID || entry.LastCompletedRunID != queued.Run.RunID {
		t.Fatalf("expected task result linkage, got entry=%+v", entry)
	}
}

func TestTaskRunnerCancelsActiveRunThroughLedgerCancellation(t *testing.T) {
	ctx := context.Background()
	entered := make(chan struct{})
	runtimeReleased := make(chan error, 1)
	runner, queued, docsRepo, _ := newExecutableTestTaskRunner(t, taskRunnerRuntimeFunc(func(ctx context.Context, _ agent.Turn) (agent.TurnResult, error) {
		close(entered)
		<-ctx.Done()
		runtimeReleased <- ctx.Err()
		return agent.TurnResult{}, ctx.Err()
	}))
	runner.chatCancels = newChatAbortRegistry()
	runner.ledger.AddObserver(runner)

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.executeQueuedRun(ctx, queued)
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("runtime was not invoked")
	}
	if len(runner.activeSnapshot()) != 1 {
		t.Fatalf("expected one active run, got %d", len(runner.activeSnapshot()))
	}
	if err := runner.taskService.CancelTask(ctx, queued.Task.TaskID, "tester", "operator cancel"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	select {
	case err := <-runtimeReleased:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runtime error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not observe cancellation")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not drain after cancellation")
	}
	if got := len(runner.activeSnapshot()); got != 0 {
		t.Fatalf("active run count after cancel = %d, want 0", got)
	}
	gotRun, err := docsRepo.GetTaskRun(ctx, queued.Run.RunID)
	if err != nil {
		t.Fatalf("GetTaskRun: %v", err)
	}
	if gotRun.Status != state.TaskRunStatusCancelled {
		t.Fatalf("run status = %s, want cancelled; run=%+v", gotRun.Status, gotRun)
	}
	gotTask, err := docsRepo.GetTask(ctx, queued.Task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if gotTask.Status != state.TaskStatusCancelled || gotTask.CurrentRunID != "" || gotTask.LastRunID != queued.Run.RunID {
		t.Fatalf("expected cancelled task with cleared current run, got %+v", gotTask)
	}
	if runner.chatCancels.Abort(queued.SessionID) {
		t.Fatal("expected chat cancel handle released after cancelled run drained")
	}
}

func TestTaskRunnerShutdownCancelsAndDrainsActiveRuns(t *testing.T) {
	ctx := context.Background()
	entered := make(chan struct{})
	runner, queued, docsRepo, _ := newExecutableTestTaskRunner(t, taskRunnerRuntimeFunc(func(ctx context.Context, _ agent.Turn) (agent.TurnResult, error) {
		close(entered)
		<-ctx.Done()
		return agent.TurnResult{}, ctx.Err()
	}))
	runner.chatCancels = newChatAbortRegistry()
	runner.ledger.AddObserver(runner)

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.executeQueuedRun(ctx, queued)
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("runtime was not invoked")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runner.Shutdown(shutdownCtx)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not drain on shutdown")
	}
	if got := len(runner.activeSnapshot()); got != 0 {
		t.Fatalf("active run count after shutdown = %d, want 0", got)
	}
	gotRun, err := docsRepo.GetTaskRun(ctx, queued.Run.RunID)
	if err != nil {
		t.Fatalf("GetTaskRun: %v", err)
	}
	if gotRun.Status != state.TaskRunStatusCancelled {
		t.Fatalf("run status = %s, want cancelled; run=%+v", gotRun.Status, gotRun)
	}
	gotTask, err := docsRepo.GetTask(ctx, queued.Task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if gotTask.Status != state.TaskStatusCancelled || gotTask.CurrentRunID != "" || gotTask.LastRunID != queued.Run.RunID {
		t.Fatalf("expected cancelled task with cleared current run, got %+v", gotTask)
	}
}

func TestTaskRunnerMarksQueuedRunFailedWhenRuntimeFails(t *testing.T) {
	ctx := context.Background()
	runtimeErr := errors.New("provider exploded")
	runner, queued, docsRepo, sessionStore := newExecutableTestTaskRunner(t, taskRunnerRuntimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{Usage: agent.TurnUsage{InputTokens: 2}}, runtimeErr
	}))

	runner.executeQueuedRun(ctx, queued)

	gotRun, err := docsRepo.GetTaskRun(ctx, queued.Run.RunID)
	if err != nil {
		t.Fatalf("GetTaskRun: %v", err)
	}
	if gotRun.Status != state.TaskRunStatusFailed || !strings.Contains(gotRun.Error, runtimeErr.Error()) {
		t.Fatalf("expected failed run with runtime error, got %+v", gotRun)
	}
	gotTask, err := docsRepo.GetTask(ctx, queued.Task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if gotTask.Status != state.TaskStatusFailed || gotTask.CurrentRunID != "" || gotTask.LastRunID != queued.Run.RunID {
		t.Fatalf("expected failed task with cleared current run, got %+v", gotTask)
	}
	entry, ok := sessionStore.Get(queued.SessionID)
	if !ok || entry.LastTurn == nil {
		t.Fatalf("expected failed turn telemetry in session store, got %+v", entry)
	}
	if entry.LastTurn.TaskID != queued.Task.TaskID || entry.LastTurn.RunID != queued.Run.RunID || !strings.Contains(entry.LastTurn.Error, runtimeErr.Error()) {
		t.Fatalf("expected trace-linked failed turn telemetry, got %+v", entry.LastTurn)
	}
}

func TestTaskRunnerPreflightBudgetFailureSkipsRuntime(t *testing.T) {
	ctx := context.Background()
	runtimeInvoked := false
	task := defaultExecutableTask()
	task.Budget = state.TaskBudget{MaxRuntimeMS: 1}
	task.Authority = state.TaskAuthority{AutonomyMode: state.AutonomyFull}
	runner, queued, docsRepo, _ := newExecutableTestTaskRunnerWithTask(t, task, taskRunnerRuntimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		runtimeInvoked = true
		return agent.TurnResult{}, nil
	}))

	runEntry, err := runner.ledger.GetRun(ctx, queued.Run.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	run := runEntry.Run
	run.Usage = state.TaskUsage{WallClockMS: 2}
	if _, err := runner.ledger.SaveRunState(ctx, run, runEntry.Source, runEntry.SourceRef); err != nil {
		t.Fatalf("SaveRunState: %v", err)
	}
	queued.Run = run

	runner.executeQueuedRun(ctx, queued)

	if runtimeInvoked {
		t.Fatal("runtime should not be invoked after over-budget preflight")
	}
	gotRun, err := docsRepo.GetTaskRun(ctx, queued.Run.RunID)
	if err != nil {
		t.Fatalf("GetTaskRun: %v", err)
	}
	if gotRun.Status != state.TaskRunStatusFailed || !strings.Contains(gotRun.Error, "action=fail") || gotRun.Usage.WallClockMS != 2 {
		t.Fatalf("expected failed budget run preserving usage, got %+v", gotRun)
	}
	if gotRun.StartedAt != 0 {
		t.Fatalf("preflight failure should not mark run started, got started_at=%d", gotRun.StartedAt)
	}
	gotTask, err := docsRepo.GetTask(ctx, queued.Task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if gotTask.Status != state.TaskStatusFailed || gotTask.CurrentRunID != "" || gotTask.LastRunID != queued.Run.RunID {
		t.Fatalf("expected failed task with cleared current run, got %+v", gotTask)
	}
	if gotTask.Meta["budget_action"] != "fail" || gotTask.Meta["budget_exhausted"] != true {
		t.Fatalf("expected budget outcome metadata, got %+v", gotTask.Meta)
	}
}

func TestTaskRunnerUsageBudgetOutcomeBlocksRun(t *testing.T) {
	ctx := context.Background()
	task := defaultExecutableTask()
	task.Budget = state.TaskBudget{MaxTotalTokens: 10}
	task.Authority = state.TaskAuthority{AutonomyMode: state.AutonomyFull}
	runner, queued, docsRepo, _ := newExecutableTestTaskRunnerWithTask(t, task, taskRunnerRuntimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{
			Text:         "done but expensive",
			Usage:        agent.TurnUsage{InputTokens: 8, OutputTokens: 5},
			HistoryDelta: []agent.ConversationMessage{{Role: "assistant", Content: "done but expensive"}},
		}, nil
	}))

	runner.executeQueuedRun(ctx, queued)

	gotRun, err := docsRepo.GetTaskRun(ctx, queued.Run.RunID)
	if err != nil {
		t.Fatalf("GetTaskRun: %v", err)
	}
	if gotRun.Status != state.TaskRunStatusBlocked || gotRun.Usage.TotalTokens != 13 || !strings.Contains(gotRun.Error, "action=fallback") {
		t.Fatalf("expected fallback budget outcome to block run, got %+v", gotRun)
	}
	gotTask, err := docsRepo.GetTask(ctx, queued.Task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if gotTask.Status != state.TaskStatusBlocked || gotTask.CurrentRunID != queued.Run.RunID {
		t.Fatalf("expected blocked task retaining current run for resume, got %+v", gotTask)
	}
	if gotTask.Meta["budget_action"] != "fallback" || gotTask.Meta["budget_exhausted"] != true {
		t.Fatalf("expected fallback budget metadata, got %+v", gotTask.Meta)
	}
}

func TestTaskRunnerVerificationPassCompletesAfterVerifying(t *testing.T) {
	ctx := context.Background()
	task := defaultExecutableTask()
	task.Verification = state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{{
			CheckID:     "evidence-output",
			Type:        state.VerificationCheckEvidence,
			Description: "output exists",
			Required:    true,
		}},
	}
	runner, queued, docsRepo, sessionStore := newExecutableTestTaskRunnerWithTask(t, task, taskRunnerRuntimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{Text: "verified output", HistoryDelta: []agent.ConversationMessage{{Role: "assistant", Content: "verified output"}}}, nil
	}))

	runner.executeQueuedRun(ctx, queued)

	gotRun, err := docsRepo.GetTaskRun(ctx, queued.Run.RunID)
	if err != nil {
		t.Fatalf("GetTaskRun: %v", err)
	}
	if gotRun.Status != state.TaskRunStatusCompleted || gotRun.Verification.Checks[0].Status != state.VerificationStatusPassed {
		t.Fatalf("expected completed verified run, got %+v", gotRun)
	}
	if !runTransitionedThrough(gotRun, state.TaskRunStatusVerifying) {
		t.Fatalf("expected run to transition through verifying, transitions=%+v", gotRun.Transitions)
	}
	gotTask, err := docsRepo.GetTask(ctx, queued.Task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if gotTask.Status != state.TaskStatusCompleted || gotTask.Verification.Checks[0].Status != state.VerificationStatusPassed || gotTask.Meta["verification_status"] != "passed" {
		t.Fatalf("expected completed verified task, got %+v", gotTask)
	}
	if !taskTransitionedThrough(gotTask, state.TaskStatusVerifying) {
		t.Fatalf("expected task to transition through verifying, transitions=%+v", gotTask.Transitions)
	}
	entry, ok := sessionStore.Get(queued.SessionID)
	if !ok || len(entry.RecentVerificationEvents) == 0 {
		t.Fatalf("expected persisted verification events, got %+v", entry)
	}
	if entry.LastCompletedTaskID != queued.Task.TaskID || entry.LastCompletedRunID != queued.Run.RunID {
		t.Fatalf("expected verified completion result linkage, got %+v", entry)
	}
}

func TestTaskRunnerVerificationFailFailsRunAndTask(t *testing.T) {
	ctx := context.Background()
	task := defaultExecutableTask()
	task.Verification = state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{{
			CheckID:     "contains-token",
			Type:        state.VerificationCheckCustom,
			Description: "output contains token",
			Required:    true,
			Meta:        map[string]any{"evaluator": "output_contains", "contains": "APPROVED"},
		}},
	}
	runner, queued, docsRepo, sessionStore := newExecutableTestTaskRunnerWithTask(t, task, taskRunnerRuntimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{Text: "not enough evidence", HistoryDelta: []agent.ConversationMessage{{Role: "assistant", Content: "not enough evidence"}}}, nil
	}))

	runner.executeQueuedRun(ctx, queued)

	gotRun, err := docsRepo.GetTaskRun(ctx, queued.Run.RunID)
	if err != nil {
		t.Fatalf("GetTaskRun: %v", err)
	}
	if gotRun.Status != state.TaskRunStatusFailed || gotRun.Verification.Checks[0].Status != state.VerificationStatusFailed || !strings.Contains(gotRun.Error, "blocked by") {
		t.Fatalf("expected verification-failed run, got %+v", gotRun)
	}
	if !runTransitionedThrough(gotRun, state.TaskRunStatusVerifying) {
		t.Fatalf("expected run to transition through verifying, transitions=%+v", gotRun.Transitions)
	}
	gotTask, err := docsRepo.GetTask(ctx, queued.Task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if gotTask.Status != state.TaskStatusFailed || gotTask.Verification.Checks[0].Status != state.VerificationStatusFailed || gotTask.Meta["verification_status"] != "failed" {
		t.Fatalf("expected verification-failed task, got %+v", gotTask)
	}
	entry, ok := sessionStore.Get(queued.SessionID)
	if !ok || len(entry.RecentVerificationEvents) == 0 {
		t.Fatalf("expected persisted verification events, got %+v", entry)
	}
	if entry.LastCompletedTaskID == queued.Task.TaskID || entry.LastCompletedRunID == queued.Run.RunID {
		t.Fatalf("failed verification must not mark task completed, got %+v", entry)
	}
}

func taskTransitionedThrough(task state.TaskSpec, status state.TaskStatus) bool {
	for _, transition := range task.Transitions {
		if transition.To == status {
			return true
		}
	}
	return false
}

func runTransitionedThrough(run state.TaskRun, status state.TaskRunStatus) bool {
	for _, transition := range run.Transitions {
		if transition.To == status {
			return true
		}
	}
	return false
}

func newTestTaskRunner(t *testing.T, ctx context.Context, task state.TaskSpec) (*taskRunner, *[]taskRunnerQueuedRun) {
	t.Helper()
	ledger := taskspkg.NewLedger(nil)
	if _, err := ledger.CreateTask(ctx, task, taskspkg.TaskSourceManual, "test"); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	queued := []taskRunnerQueuedRun{}
	runner := &taskRunner{
		ledger: ledger,
		enqueue: func(_ context.Context, run taskRunnerQueuedRun) {
			queued = append(queued, run)
		},
	}
	return runner, &queued
}

func newExecutableTestTaskRunner(t *testing.T, rt agent.Runtime) (*taskRunner, taskRunnerQueuedRun, *state.DocsRepository, *state.SessionStore) {
	t.Helper()
	return newExecutableTestTaskRunnerWithTask(t, defaultExecutableTask(), rt)
}

func defaultExecutableTask() state.TaskSpec {
	return state.TaskSpec{
		TaskID:        "task-exec",
		GoalID:        "goal-1",
		Title:         "Executable task",
		Instructions:  "Execute the queued task",
		AssignedAgent: "worker",
		MemoryScope:   state.AgentMemoryScopeUser,
	}
}

func newExecutableTestTaskRunnerWithTask(t *testing.T, task state.TaskSpec, rt agent.Runtime) (*taskRunner, taskRunnerQueuedRun, *state.DocsRepository, *state.SessionStore) {
	t.Helper()
	ctx := context.Background()
	docsRepo := state.NewDocsRepository(newTestStore(), "task-runner-test")
	store := taskspkg.NewDocsStore(docsRepo)
	service := taskspkg.NewService(store)
	if _, err := service.CreateTask(ctx, task, taskspkg.TaskSourceManual, "test", "tester"); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	runEntry, taskEntry, err := service.ResumeTask(ctx, task.TaskID, taskspkg.ResumeDecisionResume, "tester", "run it")
	if err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	sessionStore, err := state.NewSessionStore(t.TempDir() + "/sessions.json")
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	runner := &taskRunner{
		ledger:         service.Ledger(),
		taskService:    service,
		agentRegistry:  agent.NewAgentRuntimeRegistry(rt),
		sessionTurns:   autoreply.NewSessionTurns(),
		sessionStore:   sessionStore,
		docsRepo:       docsRepo,
		transcriptRepo: state.NewTranscriptRepository(newTestStore(), "task-runner-test"),
		toolRegistry:   agent.NewToolRegistry(),
		runtimeConfig:  newRuntimeConfigStore(state.ConfigDoc{}),
	}
	queued := taskRunnerQueuedRun{
		Task:      taskEntry.Task,
		Run:       runEntry.Run,
		Source:    runEntry.Source,
		SourceRef: runEntry.SourceRef,
		SessionID: taskRunnerSessionID(taskEntry.Task, runEntry.Run),
		AgentID:   taskRunnerAgentID(taskEntry.Task, runEntry.Run),
	}
	return runner, queued, docsRepo, sessionStore
}

type taskRunnerRuntimeFunc func(context.Context, agent.Turn) (agent.TurnResult, error)

func (f taskRunnerRuntimeFunc) ProcessTurn(ctx context.Context, turn agent.Turn) (agent.TurnResult, error) {
	return f(ctx, turn)
}
