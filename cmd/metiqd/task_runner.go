package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	"metiq/internal/autoreply"
	ctxengine "metiq/internal/context"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	"metiq/internal/memory"
	"metiq/internal/planner"
	"metiq/internal/store/state"
	taskspkg "metiq/internal/tasks"
)

// taskRunner is the daemon-owned observer entry point for ledger-created task
// runs. It keeps ACP worker tasks on their existing inline path and executes
// local queued task runs by reusing the daemon agent runtime pipeline.
type taskRunner struct {
	ledger         *taskspkg.Ledger
	taskService    *taskspkg.Service
	agentRegistry  *agent.AgentRuntimeRegistry
	sessionTurns   *autoreply.SessionTurns
	chatCancels    *chatAbortRegistry
	sessionStore   *state.SessionStore
	contextEngine  ctxengine.Engine
	memoryIndex    memory.Store
	docsRepo       *state.DocsRepository
	transcriptRepo *state.TranscriptRepository
	toolRegistry   *agent.ToolRegistry
	runtimeConfig  *runtimeConfigStore
	emitter        gatewayws.EventEmitter
	enqueue        taskRunEnqueueFunc

	mu           sync.Mutex
	active       map[string]taskRunnerActiveRun
	activeWG     sync.WaitGroup
	shuttingDown bool
}

type taskRunEnqueueFunc func(context.Context, taskRunnerQueuedRun)

type taskRunnerActiveRun struct {
	RunID     string
	TaskID    string
	SessionID string
	cancel    context.CancelCauseFunc
}

var errTaskRunnerShutdown = errors.New("task runner shutdown")
var errTaskRunCancelled = errors.New("task run cancelled")

type taskRunnerQueuedRun struct {
	Task      state.TaskSpec
	Run       state.TaskRun
	Source    taskspkg.TaskSource
	SourceRef string
	SessionID string
	AgentID   string
}

func newTaskRunner(svc *daemonServices) *taskRunner {
	if svc == nil {
		return nil
	}
	ledger := svc.tasks.ledger
	if ledger == nil && svc.tasks.service != nil {
		ledger = svc.tasks.service.Ledger()
	}
	if ledger == nil {
		return nil
	}
	r := &taskRunner{
		ledger:         ledger,
		taskService:    svc.tasks.service,
		agentRegistry:  svc.session.agentRegistry,
		sessionTurns:   svc.session.sessionTurns,
		chatCancels:    svc.session.chatCancels,
		sessionStore:   svc.session.sessionStore,
		contextEngine:  svc.session.contextEngine,
		memoryIndex:    svc.session.memoryStore,
		docsRepo:       svc.docsRepo,
		transcriptRepo: svc.transcriptRepo,
		toolRegistry:   svc.session.toolRegistry,
		runtimeConfig:  svc.runtimeConfig,
		emitter:        svc.emitter,
	}
	r.enqueue = r.enqueueQueuedRun
	return r
}

func (r *taskRunner) OnTaskCreated(context.Context, taskspkg.LedgerEntry) {}

func (r *taskRunner) OnTaskUpdated(context.Context, taskspkg.LedgerEntry, state.TaskTransition) {}

func (r *taskRunner) OnRunCreated(ctx context.Context, entry taskspkg.RunEntry) {
	r.observeQueuedRun(ctx, entry)
}

func (r *taskRunner) OnRunUpdated(ctx context.Context, entry taskspkg.RunEntry, transition state.TaskRunTransition) {
	if transition.To == state.TaskRunStatusCancelled {
		r.cancelActiveRun(entry.Run.RunID, errTaskRunCancelled)
		return
	}
	if transition.To != state.TaskRunStatusQueued {
		return
	}
	r.observeQueuedRun(ctx, entry)
}

func (r *taskRunner) observeQueuedRun(ctx context.Context, entry taskspkg.RunEntry) bool {
	queued, ok := r.prepareQueuedRun(ctx, entry)
	if !ok {
		return false
	}
	if r.enqueue == nil {
		return false
	}
	r.enqueue(ctx, queued)
	return true
}

func (r *taskRunner) prepareQueuedRun(ctx context.Context, entry taskspkg.RunEntry) (taskRunnerQueuedRun, bool) {
	if r == nil || r.ledger == nil {
		return taskRunnerQueuedRun{}, false
	}
	run := entry.Run.Normalize()
	if strings.TrimSpace(run.RunID) == "" || strings.TrimSpace(run.TaskID) == "" {
		return taskRunnerQueuedRun{}, false
	}
	if run.Status != state.TaskRunStatusQueued {
		return taskRunnerQueuedRun{}, false
	}
	if !taskRunnerSourceAllowed(entry.Source) {
		return taskRunnerQueuedRun{}, false
	}

	taskEntry, err := r.ledger.GetTask(ctx, run.TaskID)
	if err != nil || taskEntry == nil {
		if err != nil {
			log.Printf("task runner: queued run %s ignored: task %s not found: %v", run.RunID, run.TaskID, err)
		}
		return taskRunnerQueuedRun{}, false
	}
	task := taskEntry.Task.Normalize()
	if taskWorkflowStepType(task) != "" && taskWorkflowStepType(task) != string(taskspkg.StepTypeAgentTurn) {
		return taskRunnerQueuedRun{}, false
	}
	return taskRunnerQueuedRun{
		Task:      task,
		Run:       run,
		Source:    entry.Source,
		SourceRef: entry.SourceRef,
		SessionID: taskRunnerSessionID(task, run),
		AgentID:   taskRunnerAgentID(task, run),
	}, true
}

func (r *taskRunner) enqueueQueuedRun(ctx context.Context, queued taskRunnerQueuedRun) {
	ctx = contextWithoutNil(ctx)
	go r.executeQueuedRun(context.WithoutCancel(ctx), queued)
}

func (r *taskRunner) beginActiveRun(parent context.Context, queued taskRunnerQueuedRun) (context.Context, func(), bool) {
	runID := strings.TrimSpace(queued.Run.RunID)
	if r == nil || runID == "" {
		return parent, nil, false
	}
	parent = contextWithoutNil(parent)
	runCtx, cancel := context.WithCancelCause(parent)
	active := taskRunnerActiveRun{
		RunID:     runID,
		TaskID:    strings.TrimSpace(queued.Task.TaskID),
		SessionID: strings.TrimSpace(queued.SessionID),
		cancel:    cancel,
	}
	r.mu.Lock()
	if r.shuttingDown {
		r.mu.Unlock()
		cancel(errTaskRunnerShutdown)
		return parent, nil, false
	}
	if r.active == nil {
		r.active = map[string]taskRunnerActiveRun{}
	}
	if _, exists := r.active[runID]; exists {
		r.mu.Unlock()
		cancel(errTaskRunCancelled)
		return parent, nil, false
	}
	r.active[runID] = active
	r.activeWG.Add(1)
	r.mu.Unlock()

	finish := func() {
		cancel(nil)
		r.mu.Lock()
		if _, ok := r.active[runID]; ok {
			delete(r.active, runID)
		}
		r.mu.Unlock()
		r.activeWG.Done()
	}
	return runCtx, finish, true
}

func (r *taskRunner) cancelActiveRun(runID string, cause error) bool {
	if r == nil {
		return false
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return false
	}
	if cause == nil {
		cause = errTaskRunCancelled
	}
	r.mu.Lock()
	active, ok := r.active[runID]
	r.mu.Unlock()
	if !ok {
		return false
	}
	active.cancel(cause)
	if r.chatCancels != nil && strings.TrimSpace(active.SessionID) != "" {
		r.chatCancels.AbortWithCause(active.SessionID, cause)
	}
	return true
}

func (r *taskRunner) activeSnapshot() []taskRunnerActiveRun {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]taskRunnerActiveRun, 0, len(r.active))
	for _, active := range r.active {
		out = append(out, active)
	}
	return out
}

func (r *taskRunner) Shutdown(ctx context.Context) {
	if r == nil {
		return
	}
	ctx = contextWithoutNil(ctx)
	r.mu.Lock()
	r.shuttingDown = true
	active := make([]taskRunnerActiveRun, 0, len(r.active))
	for _, run := range r.active {
		active = append(active, run)
	}
	r.mu.Unlock()

	cancelledTasks := map[string]struct{}{}
	for _, run := range active {
		if taskID := strings.TrimSpace(run.TaskID); taskID != "" {
			cancelledTasks[taskID] = struct{}{}
		}
	}
	for taskID := range cancelledTasks {
		if r.taskService == nil {
			continue
		}
		if err := r.taskService.CancelTask(ctx, taskID, "task_runner", "daemon shutdown cancelled active task run"); err != nil {
			log.Printf("task runner: shutdown cancel task=%s failed: %v", taskID, err)
		}
	}
	for _, run := range active {
		run.cancel(errTaskRunnerShutdown)
		if r.chatCancels != nil && strings.TrimSpace(run.SessionID) != "" {
			r.chatCancels.AbortWithCause(run.SessionID, errTaskRunnerShutdown)
		}
	}

	done := make(chan struct{})
	go func() {
		r.activeWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		log.Printf("task runner: shutdown timed out with %d active run(s)", len(r.activeSnapshot()))
	}
}

func (r *taskRunner) executeQueuedRun(ctx context.Context, queued taskRunnerQueuedRun) {
	if r == nil || r.ledger == nil || r.taskService == nil {
		return
	}
	ctx = contextWithoutNil(ctx)
	queued.Task = queued.Task.Normalize()
	queued.Run = queued.Run.Normalize()
	queued.SessionID = strings.TrimSpace(queued.SessionID)
	if queued.SessionID == "" {
		queued.SessionID = taskRunnerSessionID(queued.Task, queued.Run)
	}
	queued.AgentID = defaultAgentID(queued.AgentID)
	var finishActive func()
	var ok bool
	ctx, finishActive, ok = r.beginActiveRun(ctx, queued)
	if !ok {
		return
	}
	defer finishActive()
	trace := taskRunnerTraceContext(queued.Task, queued.Run)

	freshRun, err := r.ledger.GetRun(ctx, queued.Run.RunID)
	if err != nil || freshRun == nil {
		if err != nil {
			log.Printf("task runner: run %s lookup failed: %v", queued.Run.RunID, err)
		}
		return
	}
	freshTask, err := r.ledger.GetTask(ctx, freshRun.Run.TaskID)
	if err != nil || freshTask == nil {
		if err != nil {
			log.Printf("task runner: task %s lookup failed for run %s: %v", freshRun.Run.TaskID, queued.Run.RunID, err)
		}
		return
	}
	queued.Run = freshRun.Run.Normalize()
	queued.Task = freshTask.Task.Normalize()
	if queued.Run.Status != state.TaskRunStatusQueued || taskRunnerTaskTerminal(queued.Task.Status) {
		return
	}
	if queued.SessionID == "" {
		queued.SessionID = taskRunnerSessionID(queued.Task, queued.Run)
	}
	if queued.AgentID == "" || queued.AgentID == defaultAgentID("") {
		queued.AgentID = taskRunnerAgentID(queued.Task, queued.Run)
	}
	trace = taskRunnerTraceContext(queued.Task, queued.Run)

	budgetGuard, err := r.budgetGuardForRun(ctx, queued.Task, queued.Run)
	if err != nil {
		log.Printf("task runner: budget guard setup failed task=%s run=%s err=%v", queued.Task.TaskID, queued.Run.RunID, err)
		r.finishQueuedRun(ctx, queued, trace, agent.TurnResult{}, nil, err, time.Now(), nil, nil)
		return
	}
	if event := taskRunnerBudgetEventFromExceeded(budgetGuard, budgetGuard.Check(), queued.Task); event != nil {
		r.applyBudgetOutcome(ctx, queued, *event, queued.Run.Usage)
		return
	}

	if r.sessionTurns != nil {
		r.sessionTurns.Track(queued.SessionID, queued.AgentID)
		release, err := r.sessionTurns.Acquire(ctx, queued.SessionID)
		if err != nil {
			r.finishQueuedRun(ctx, queued, trace, agent.TurnResult{}, nil, err, time.Now(), nil, budgetGuard)
			return
		}
		defer release()
	}

	startedAt := time.Now()
	if _, err := r.ledger.UpdateRunStatus(ctx, queued.Run.RunID, state.TaskRunStatusRunning, queued.AgentID, "task_runner", "queued task run started"); err != nil {
		log.Printf("task runner: start run %s failed: %v", queued.Run.RunID, err)
		return
	}
	if queued.Task.Status != state.TaskStatusInProgress && state.AllowedTaskTransition(queued.Task.Status, state.TaskStatusInProgress) {
		if _, err := r.ledger.UpdateTaskStatus(ctx, queued.Task.TaskID, state.TaskStatusInProgress, queued.AgentID, "task_runner", "queued task run started"); err != nil {
			log.Printf("task runner: mark task %s in_progress failed: %v", queued.Task.TaskID, err)
		}
	}
	if r.sessionStore != nil {
		if err := r.sessionStore.LinkTask(queued.SessionID, queued.Task.TaskID, queued.Run.RunID, queued.Task.ParentTaskID, queued.Run.ParentRunID); err != nil {
			log.Printf("task runner: link session task failed session=%s task=%s run=%s err=%v", queued.SessionID, queued.Task.TaskID, queued.Run.RunID, err)
		}
	}
	if r.chatCancels != nil {
		var releaseChatTurn func()
		ctx, releaseChatTurn = r.chatCancels.Begin(queued.SessionID, ctx)
		defer releaseChatTurn()
	}

	cfg := state.ConfigDoc{}
	if r.runtimeConfig != nil {
		cfg = r.runtimeConfig.Get()
	}
	scopeCtx := resolveMemoryScopeContext(ctx, cfg, r.docsRepo, r.sessionStore, queued.SessionID, queued.AgentID, queued.Task.MemoryScope)
	persistSessionMemoryScope(r.sessionStore, queued.SessionID, queued.AgentID, scopeCtx.Scope)
	turnCtx := taskspkg.ContextWithBudgetGuard(contextWithMemoryScope(ctx, scopeCtx), budgetGuard)

	rt := taskRunnerRuntime(r.agentRegistry, queued.AgentID)
	if rt == nil {
		err := fmt.Errorf("no runtime for agent %s", queued.AgentID)
		r.finishQueuedRun(ctx, queued, trace, agent.TurnResult{}, nil, err, startedAt, nil, budgetGuard)
		return
	}
	filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(
		turnCtx,
		cfg,
		r.docsRepo,
		queued.SessionID,
		queued.AgentID,
		rt,
		r.toolRegistry,
		turnToolConstraints{ToolProfile: queued.Task.ToolProfile, EnabledTools: append([]string(nil), queued.Task.EnabledTools...)},
	)
	prepared := buildAgentRunTurn(turnCtx, methods.AgentRequest{
		SessionID: queued.SessionID,
		Message:   queued.Task.Instructions,
		Context:   taskRunnerPromptContext(queued.Task),
	}, r.memoryIndex, scopeCtx, workspaceDirForAgent(cfg, queued.AgentID), r.sessionStore)
	prepared.Turn.Tools = turnTools
	prepared.Turn.Executor = wrapTaskBudgetToolExecutor(turnExecutor, budgetGuard)
	prepared.Turn.Trace = trace
	prepared.Turn.ToolEventSink = toolLifecyclePersistenceSink(r.sessionStore, queued.SessionID, toolLifecycleEmitter(r.emitter, queued.AgentID))
	prepared = applyPromptEnvelopeToPreparedTurn(prepared, turnPromptBuilderParams{Config: cfg, SessionID: queued.SessionID, AgentID: queued.AgentID, Channel: "task", StaticSystemPrompt: prepared.Turn.StaticSystemPrompt, Context: prepared.Turn.Context, Tools: turnTools})
	prepared.Turn.TurnID = queued.Run.RunID
	prepared.Turn.Trace = trace

	result, turnErr := filteredRuntime.ProcessTurn(prepared.TurnCtx, prepared.Turn)
	if turnErr != nil {
		if partial, ok := agent.PartialTurnResult(turnErr); ok {
			if r.transcriptRepo != nil {
				if err := persistToolTraces(ctx, r.transcriptRepo, queued.SessionID, queued.Run.RunID, partial.ToolTraces); err != nil {
					log.Printf("task runner: persist partial tool traces failed session=%s run=%s err=%v", queued.SessionID, queued.Run.RunID, err)
				}
			}
			persistAndIngestTurnHistory(ctx, r.transcriptRepo, r.contextEngine, queued.SessionID, queued.Run.RunID, partial.HistoryDelta, turnResultMetadataPtr(result, turnErr))
			updateSessionTaskState(r.sessionStore, queued.SessionID, partial.ToolTraces, partial.HistoryDelta, true)
		}
		r.finishQueuedRun(ctx, queued, trace, result, prepared.MemoryRecallSample, turnErr, startedAt, nil, budgetGuard)
		return
	}

	if prepared.MemoryRecallSample != nil {
		prepared.MemoryRecallSample.TaskID = strings.TrimSpace(queued.Task.TaskID)
		prepared.MemoryRecallSample.RunID = strings.TrimSpace(queued.Run.RunID)
		prepared.MemoryRecallSample.GoalID = strings.TrimSpace(queued.Task.GoalID)
	}
	commitMemoryRecallArtifacts(r.sessionStore, queued.SessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
	if r.transcriptRepo != nil {
		if err := persistToolTraces(ctx, r.transcriptRepo, queued.SessionID, queued.Run.RunID, result.ToolTraces); err != nil {
			log.Printf("task runner: persist tool traces failed session=%s run=%s err=%v", queued.SessionID, queued.Run.RunID, err)
		}
	}
	delta := result.HistoryDelta
	if len(delta) == 0 && strings.TrimSpace(result.Text) != "" {
		delta = []agent.ConversationMessage{{Role: "assistant", Content: strings.TrimSpace(result.Text)}}
	}
	historyEntryIDs := persistAndIngestTurnHistory(ctx, r.transcriptRepo, r.contextEngine, queued.SessionID, queued.Run.RunID, delta, turnResultMetadataPtr(result, nil))
	updateSessionTaskState(r.sessionStore, queued.SessionID, result.ToolTraces, delta, false)
	r.finishQueuedRun(ctx, queued, trace, result, prepared.MemoryRecallSample, nil, startedAt, historyEntryIDs, budgetGuard)
}

func (r *taskRunner) finishQueuedRun(ctx context.Context, queued taskRunnerQueuedRun, trace agent.TraceContext, result agent.TurnResult, recall *state.MemoryRecallSample, turnErr error, startedAt time.Time, historyEntryIDs []string, budgetGuard *taskspkg.BudgetGuard) {
	if r == nil || r.taskService == nil {
		return
	}
	usage := taskRunnerUsage(result, startedAt)
	if budgetGuard != nil {
		current := budgetGuard.CurrentUsage()
		usage.ToolCalls = current.ToolCalls
		usage.Delegations = current.Delegations
		usage.CostMicrosUSD = current.CostMicrosUSD
		budgetGuard.SetCurrentUsage(usage)
	}
	if r.sessionStore != nil && (result.Usage.InputTokens > 0 || result.Usage.OutputTokens > 0) {
		_ = r.sessionStore.AddTokens(queued.SessionID, result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.CacheReadTokens, result.Usage.CacheCreationTokens)
	}
	resultRef := state.TaskResultRef{Kind: "task_run", ID: queued.Run.RunID}
	if len(historyEntryIDs) > 0 {
		resultRef = state.TaskResultRef{Kind: "transcript_entry", ID: historyEntryIDs[len(historyEntryIDs)-1]}
	}
	turnTelemetry := buildTurnTelemetry(queued.Run.RunID, startedAt, time.Now(), result, turnErr, false, "", "", "")
	turnTelemetry.Trace = trace
	persistTurnTelemetry(r.sessionStore, queued.SessionID, turnTelemetry)
	emitTurnTelemetry(r.emitter, queued.AgentID, queued.SessionID, turnTelemetry)
	if recall != nil && turnErr != nil {
		recall.TaskID = strings.TrimSpace(queued.Task.TaskID)
		recall.RunID = strings.TrimSpace(queued.Run.RunID)
		recall.GoalID = strings.TrimSpace(queued.Task.GoalID)
		commitMemoryRecallArtifacts(r.sessionStore, queued.SessionID, queued.Run.RunID, recall, nil)
	}
	if event := taskRunnerBudgetEventFromExceeded(budgetGuard, budgetGuard.Check(), queued.Task); event != nil {
		r.applyBudgetOutcome(ctx, queued, *event, usage)
		return
	}
	if turnErr == nil {
		if err := r.finishVerifiedQueuedRun(ctx, queued, result, resultRef, usage, historyEntryIDs); err != nil {
			log.Printf("task runner: verified finish task=%s run=%s failed: %v", queued.Task.TaskID, queued.Run.RunID, err)
		}
		return
	}
	if _, _, err := r.taskService.FinishWorkerRun(ctx, queued.Task.TaskID, queued.Run.RunID, resultRef, usage, queued.AgentID, turnErr, historyEntryIDs); err != nil {
		log.Printf("task runner: finish task=%s run=%s failed: %v", queued.Task.TaskID, queued.Run.RunID, err)
	}
}

func (r *taskRunner) finishVerifiedQueuedRun(ctx context.Context, queued taskRunnerQueuedRun, result agent.TurnResult, resultRef state.TaskResultRef, usage state.TaskUsage, historyEntryIDs []string) error {
	if r == nil || r.ledger == nil {
		return fmt.Errorf("task runner ledger is nil")
	}
	actor := strings.TrimSpace(queued.AgentID)
	if actor == "" {
		actor = "task_runner"
	}
	runEntry, err := r.ledger.GetRun(ctx, queued.Run.RunID)
	if err != nil {
		return err
	}
	run := runEntry.Run.Normalize()
	run.Result = resultRef
	run.Usage = usage
	run.Error = ""
	if _, err := r.ledger.SaveRunState(ctx, run, runEntry.Source, runEntry.SourceRef); err != nil {
		return err
	}
	if run.Status != state.TaskRunStatusVerifying && state.AllowedTaskRunTransition(run.Status, state.TaskRunStatusVerifying) {
		runEntry, err = r.ledger.UpdateRunStatus(ctx, run.RunID, state.TaskRunStatusVerifying, actor, "task_runner", "task run output ready for verification")
		if err != nil {
			return err
		}
		run = runEntry.Run.Normalize()
	}

	taskEntry, err := r.ledger.GetTask(ctx, queued.Task.TaskID)
	if err != nil {
		return err
	}
	task := taskEntry.Task.Normalize()
	if task.Status != state.TaskStatusVerifying && state.AllowedTaskTransition(task.Status, state.TaskStatusVerifying) {
		taskEntry, err = r.ledger.UpdateTaskStatus(ctx, task.TaskID, state.TaskStatusVerifying, actor, "task_runner", "task output ready for verification")
		if err != nil {
			return err
		}
		task = taskEntry.Task.Normalize()
	}

	execution := taskspkg.VerificationExecutor{}.Execute(ctx, task, run, taskRunnerVerificationOutputs(result), actor)
	r.recordVerificationEvents(queued.SessionID, execution.Events)

	verificationStatus := "passed"
	targetRunStatus := state.TaskRunStatusCompleted
	targetTaskStatus := state.TaskStatusCompleted
	reason := execution.Gate.Reason
	if !execution.Gate.Allowed() {
		verificationStatus = "failed"
		targetRunStatus = state.TaskRunStatusFailed
		targetTaskStatus = state.TaskStatusFailed
		if reason == "" {
			reason = "verification failed"
		}
	}

	runEntry, err = r.ledger.GetRun(ctx, queued.Run.RunID)
	if err != nil {
		return err
	}
	run = runEntry.Run.Normalize()
	run.Result = resultRef
	run.Usage = usage
	run.Verification = execution.RuntimeResult.UpdatedSpec.Normalize()
	run.Meta = taskRunnerVerificationMeta(run.Meta, verificationStatus, execution)
	if targetRunStatus == state.TaskRunStatusFailed {
		run.Error = reason
	}
	if _, err := r.ledger.SaveRunState(ctx, run, runEntry.Source, runEntry.SourceRef); err != nil {
		return err
	}

	taskEntry, err = r.ledger.GetTask(ctx, queued.Task.TaskID)
	if err != nil {
		return err
	}
	task = taskEntry.Task.Normalize()
	task.Verification = execution.RuntimeResult.UpdatedSpec.Normalize()
	task.Meta = taskRunnerVerificationMeta(task.Meta, verificationStatus, execution)
	if len(historyEntryIDs) > 0 {
		task.Meta["result_history_entry_id"] = historyEntryIDs[len(historyEntryIDs)-1]
	}
	if _, err := r.ledger.SaveTaskState(ctx, task, taskEntry.Source, taskEntry.SourceRef); err != nil {
		return err
	}

	if run.Status != targetRunStatus && state.AllowedTaskRunTransition(run.Status, targetRunStatus) {
		if _, err := r.ledger.UpdateRunStatus(ctx, run.RunID, targetRunStatus, actor, "task_runner", reason); err != nil {
			return err
		}
	}
	if task.Status != targetTaskStatus && state.AllowedTaskTransition(task.Status, targetTaskStatus) {
		if _, err := r.ledger.UpdateTaskStatus(ctx, task.TaskID, targetTaskStatus, actor, "task_runner", reason); err != nil {
			return err
		}
	}
	if targetTaskStatus == state.TaskStatusCompleted && r.sessionStore != nil {
		if err := r.sessionStore.RecordTaskResult(queued.SessionID, queued.Task.TaskID, queued.Run.RunID, resultRef); err != nil {
			log.Printf("task runner: record task result failed session=%s task=%s run=%s err=%v", queued.SessionID, queued.Task.TaskID, queued.Run.RunID, err)
		}
	}

	taskEntry, err = r.ledger.GetTask(ctx, queued.Task.TaskID)
	if err != nil {
		return err
	}
	task = taskEntry.Task.Normalize()
	task.CurrentRunID = ""
	task.LastRunID = queued.Run.RunID
	task.UpdatedAt = time.Now().Unix()
	_, err = r.ledger.SaveTaskState(ctx, task, taskEntry.Source, taskEntry.SourceRef)
	return err
}

func taskRunnerVerificationOutputs(result agent.TurnResult) planner.TaskOutputs {
	outputs := planner.TaskOutputs{
		RawOutput:   strings.TrimSpace(result.Text),
		ToolResults: map[string]planner.ToolCallResult{},
	}
	if strings.HasPrefix(outputs.RawOutput, "{") {
		var structured map[string]any
		if err := json.Unmarshal([]byte(outputs.RawOutput), &structured); err == nil {
			outputs.StructuredOutput = structured
		}
	}
	for i, trace := range result.ToolTraces {
		callID := strings.TrimSpace(trace.Call.ID)
		if callID == "" {
			callID = fmt.Sprintf("tool-%d", i+1)
		}
		outputs.ToolResults[callID] = planner.ToolCallResult{
			ToolName: trace.Call.Name,
			Input:    trace.Call.Args,
			Output:   trace.Result,
			Error:    trace.Error,
		}
	}
	if len(outputs.ToolResults) == 0 {
		outputs.ToolResults = nil
	}
	return outputs
}

func (r *taskRunner) recordVerificationEvents(sessionID string, events []planner.VerificationEvent) {
	if r == nil || r.sessionStore == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	for _, event := range events {
		if err := r.sessionStore.RecordVerificationEvent(sessionID, state.VerificationEventTelemetry{
			Type:       string(event.Type),
			TaskID:     event.TaskID,
			RunID:      event.RunID,
			GoalID:     event.GoalID,
			StepID:     event.StepID,
			CheckID:    event.CheckID,
			CheckType:  event.CheckType,
			Status:     event.Status,
			Result:     event.Result,
			Evidence:   event.Evidence,
			ReviewerID: event.ReviewerID,
			Confidence: event.Confidence,
			Duration:   event.Duration,
			GateAction: event.GateAction,
			CreatedAt:  event.CreatedAt,
			Meta:       event.Meta,
		}); err != nil {
			log.Printf("task runner: record verification event failed session=%s task=%s run=%s err=%v", sessionID, event.TaskID, event.RunID, err)
		}
	}
}

func taskRunnerVerificationMeta(meta map[string]any, status string, execution taskspkg.VerificationExecution) map[string]any {
	out := make(map[string]any, len(meta)+6)
	for k, v := range meta {
		out[k] = v
	}
	out["verification_status"] = status
	out["verification_summary"] = execution.RuntimeResult.Summary
	out["verification_gate"] = string(execution.Gate.Decision)
	out["verification_failed_checks"] = append([]string(nil), execution.Gate.FailedChecks...)
	out["verification_policy"] = string(execution.RuntimeResult.UpdatedSpec.Policy)
	out["verification_checked_at"] = execution.Summary.VerifiedAt
	return out
}

type taskBudgetToolExecutor struct {
	base  agent.ToolExecutor
	guard *taskspkg.BudgetGuard
}

func wrapTaskBudgetToolExecutor(base agent.ToolExecutor, guard *taskspkg.BudgetGuard) agent.ToolExecutor {
	if base == nil {
		return nil
	}
	if guard == nil || guard.Budget().IsZero() {
		return base
	}
	return taskBudgetToolExecutor{base: base, guard: guard}
}

func (e taskBudgetToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (string, error) {
	if e.guard != nil {
		if exceeded, ok := e.guard.TryReserveUsage(state.TaskUsage{ToolCalls: 1}); !ok {
			return "", taskRunnerBudgetExceededError(exceeded.Reasons())
		}
	}
	return e.base.Execute(ctx, call)
}

func (r *taskRunner) budgetGuardForRun(ctx context.Context, task state.TaskSpec, run state.TaskRun) (*taskspkg.BudgetGuard, error) {
	if r == nil || r.ledger == nil {
		return nil, nil
	}
	runs, err := r.ledger.ListRuns(ctx, taskspkg.ListRunsOptions{TaskID: run.TaskID, Limit: 1000})
	if err != nil {
		return nil, err
	}
	runDocs := make([]state.TaskRun, 0, len(runs))
	for _, entry := range runs {
		if entry == nil {
			continue
		}
		runDocs = append(runDocs, entry.Run.Normalize())
	}
	return taskspkg.NewBudgetGuard(task, run, runDocs), nil
}

func taskRunnerBudgetEventFromExceeded(guard *taskspkg.BudgetGuard, exceeded state.BudgetExceeded, task state.TaskSpec) *planner.ExhaustionEvent {
	if guard == nil || !exceeded.Any() {
		return nil
	}
	dimensions := taskRunnerExceededDimensions(exceeded)
	decision := planner.BudgetDecision{
		Verdict:            planner.BudgetBlock,
		Reason:             "budget exhausted: " + strings.Join(exceeded.Reasons(), ", "),
		ExceededDimensions: dimensions,
		Usage:              guard.Usage(),
		Budget:             guard.Budget(),
	}
	mode := task.Authority.Normalize().EffectiveAutonomyMode(state.AutonomyFull)
	return planner.NewOutcomeResolver(nil).ResolveOutcome(decision, guard.TaskID(), guard.RunID(), mode, time.Now().Unix())
}

func taskRunnerExceededDimensions(exceeded state.BudgetExceeded) []string {
	var dims []string
	if exceeded.PromptTokens {
		dims = append(dims, "prompt_tokens")
	}
	if exceeded.CompletionTokens {
		dims = append(dims, "completion_tokens")
	}
	if exceeded.TotalTokens {
		dims = append(dims, "total_tokens")
	}
	if exceeded.RuntimeMS {
		dims = append(dims, "runtime_ms")
	}
	if exceeded.ToolCalls {
		dims = append(dims, "tool_calls")
	}
	if exceeded.Delegations {
		dims = append(dims, "delegations")
	}
	if exceeded.CostMicrosUSD {
		dims = append(dims, "cost_micros_usd")
	}
	return dims
}

func taskRunnerBudgetExceededError(reasons []string) error {
	reason := strings.Join(reasons, ", ")
	if reason == "" {
		reason = "budget exceeded"
	}
	return fmt.Errorf("budget exhausted: %s", reason)
}

func (r *taskRunner) applyBudgetOutcome(ctx context.Context, queued taskRunnerQueuedRun, event planner.ExhaustionEvent, usage state.TaskUsage) {
	if r == nil || r.ledger == nil {
		return
	}
	actor := queued.AgentID
	if strings.TrimSpace(actor) == "" {
		actor = "task_runner"
	}
	reason := taskRunnerBudgetOutcomeReason(event)
	runEntry, err := r.ledger.GetRun(ctx, queued.Run.RunID)
	if err != nil || runEntry == nil {
		if err != nil {
			log.Printf("task runner: budget outcome run lookup failed run=%s err=%v", queued.Run.RunID, err)
		}
		return
	}
	run := runEntry.Run.Normalize()
	run.Usage = usage
	run.Error = reason
	run.Meta = taskRunnerBudgetMeta(run.Meta, event)
	if _, err := r.ledger.SaveRunState(ctx, run, runEntry.Source, runEntry.SourceRef); err != nil {
		log.Printf("task runner: save budget run state failed run=%s err=%v", queued.Run.RunID, err)
		return
	}

	targetRun, targetTask := taskRunnerBudgetStatuses(event.Action)
	if run.Status != targetRun && state.AllowedTaskRunTransition(run.Status, targetRun) {
		if _, err := r.ledger.UpdateRunStatus(ctx, run.RunID, targetRun, actor, "task_runner", reason); err != nil {
			log.Printf("task runner: budget run transition failed run=%s status=%s err=%v", run.RunID, targetRun, err)
			return
		}
	}
	taskEntry, err := r.ledger.GetTask(ctx, queued.Task.TaskID)
	if err != nil || taskEntry == nil {
		if err != nil {
			log.Printf("task runner: budget outcome task lookup failed task=%s err=%v", queued.Task.TaskID, err)
		}
		return
	}
	task := taskEntry.Task.Normalize()
	task.Meta = taskRunnerBudgetMeta(task.Meta, event)
	if targetRun == state.TaskRunStatusFailed {
		task.CurrentRunID = ""
		task.LastRunID = run.RunID
	} else if strings.TrimSpace(task.CurrentRunID) == "" {
		task.CurrentRunID = run.RunID
	}
	if _, err := r.ledger.SaveTaskState(ctx, task, taskEntry.Source, taskEntry.SourceRef); err != nil {
		log.Printf("task runner: save budget task state failed task=%s err=%v", queued.Task.TaskID, err)
		return
	}
	if task.Status != targetTask && state.AllowedTaskTransition(task.Status, targetTask) {
		if _, err := r.ledger.UpdateTaskStatusWithMeta(ctx, task.TaskID, targetTask, actor, "task_runner", reason, map[string]any{"budget_action": string(event.Action), "budget_event_id": event.EventID}); err != nil {
			log.Printf("task runner: budget task transition failed task=%s status=%s err=%v", task.TaskID, targetTask, err)
		}
	}
}

func taskRunnerBudgetStatuses(action planner.ExhaustionAction) (state.TaskRunStatus, state.TaskStatus) {
	switch action {
	case planner.ActionFail:
		return state.TaskRunStatusFailed, state.TaskStatusFailed
	case planner.ActionEscalate:
		return state.TaskRunStatusAwaitingApproval, state.TaskStatusAwaitingApproval
	case planner.ActionBlock, planner.ActionReplan, planner.ActionFallback:
		return state.TaskRunStatusBlocked, state.TaskStatusBlocked
	default:
		return state.TaskRunStatusFailed, state.TaskStatusFailed
	}
}

func taskRunnerBudgetOutcomeReason(event planner.ExhaustionEvent) string {
	parts := []string{"budget exhausted"}
	if event.Action != "" {
		parts = append(parts, "action="+string(event.Action))
	}
	if len(event.Reasons) > 0 {
		reasons := make([]string, 0, len(event.Reasons))
		for _, reason := range event.Reasons {
			reasons = append(reasons, string(reason))
		}
		parts = append(parts, "reasons="+strings.Join(reasons, ","))
	}
	if event.ActionReason != "" {
		parts = append(parts, event.ActionReason)
	}
	return strings.Join(parts, ": ")
}

func taskRunnerBudgetMeta(meta map[string]any, event planner.ExhaustionEvent) map[string]any {
	out := make(map[string]any, len(meta)+6)
	for k, v := range meta {
		out[k] = v
	}
	out["budget_exhausted"] = true
	out["budget_event_id"] = event.EventID
	out["budget_action"] = string(event.Action)
	out["budget_action_reason"] = event.ActionReason
	out["budget_reasons"] = exhaustionReasonsStrings(event.Reasons)
	out["budget_usage"] = event.Usage
	out["budget_limit"] = event.Budget
	return out
}

func exhaustionReasonsStrings(reasons []planner.ExhaustionReason) []string {
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		out = append(out, string(reason))
	}
	return out
}

func taskRunnerSourceAllowed(source taskspkg.TaskSource) bool {
	switch source {
	case taskspkg.TaskSourceManual, taskspkg.TaskSourceWorkflow, taskspkg.TaskSourceCron, taskspkg.TaskSourceWebhook:
		// Manual covers locally/operator-created tasks; workflow, cron, and webhook
		// are daemon-owned automation sources.
		return true
	case taskspkg.TaskSourceACP:
		// ACP worker tasks are executed by handleACPMessage/startACPWorkerTaskDocs,
		// so the daemon task runner must not auto-run them a second time.
		return false
	default:
		return false
	}
}

func taskRunnerSessionID(task state.TaskSpec, run state.TaskRun) string {
	if sessionID := strings.TrimSpace(task.SessionID); sessionID != "" {
		return sessionID
	}
	if taskID := strings.TrimSpace(task.TaskID); taskID != "" {
		return "task:" + taskID
	}
	return "task:" + strings.TrimSpace(run.TaskID)
}

func taskRunnerAgentID(task state.TaskSpec, run state.TaskRun) string {
	agentID := strings.TrimSpace(run.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(task.AssignedAgent)
	}
	return defaultAgentID(agentID)
}

func taskRunnerTraceContext(task state.TaskSpec, run state.TaskRun) agent.TraceContext {
	return agent.TraceContext{
		GoalID:       strings.TrimSpace(task.GoalID),
		TaskID:       strings.TrimSpace(task.TaskID),
		RunID:        strings.TrimSpace(run.RunID),
		ParentTaskID: strings.TrimSpace(task.ParentTaskID),
		ParentRunID:  strings.TrimSpace(run.ParentRunID),
	}
}

func taskRunnerRuntime(registry *agent.AgentRuntimeRegistry, agentID string) agent.Runtime {
	if registry == nil {
		return nil
	}
	return registry.Get(defaultAgentID(agentID))
}

func taskRunnerTaskTerminal(status state.TaskStatus) bool {
	switch status {
	case state.TaskStatusCompleted, state.TaskStatusFailed, state.TaskStatusCancelled:
		return true
	default:
		return false
	}
}

func taskRunnerUsage(result agent.TurnResult, startedAt time.Time) state.TaskUsage {
	usage := state.TaskUsage{
		PromptTokens:     int(result.Usage.InputTokens),
		CompletionTokens: int(result.Usage.OutputTokens),
		TotalTokens:      int(result.Usage.InputTokens + result.Usage.OutputTokens),
	}
	if !startedAt.IsZero() {
		usage.WallClockMS = time.Since(startedAt).Milliseconds()
	}
	return usage
}

func taskRunnerPromptContext(task state.TaskSpec) string {
	parts := []string{}
	if title := strings.TrimSpace(task.Title); title != "" {
		parts = append(parts, "Task title: "+title)
	}
	if goalID := strings.TrimSpace(task.GoalID); goalID != "" {
		parts = append(parts, "Goal ID: "+goalID)
	}
	if len(task.AcceptanceCriteria) > 0 {
		criteria := make([]string, 0, len(task.AcceptanceCriteria))
		for _, criterion := range task.AcceptanceCriteria {
			if desc := strings.TrimSpace(criterion.Description); desc != "" {
				criteria = append(criteria, "- "+desc)
			}
		}
		if len(criteria) > 0 {
			parts = append(parts, "Acceptance criteria:\n"+strings.Join(criteria, "\n"))
		}
	}
	return strings.Join(parts, "\n\n")
}

func taskWorkflowStepType(task state.TaskSpec) string {
	if task.Meta == nil {
		return ""
	}
	if raw, ok := task.Meta["workflow_step_type"].(string); ok {
		return strings.TrimSpace(raw)
	}
	return ""
}

func contextWithoutNil(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
