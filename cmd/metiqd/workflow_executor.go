package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
	taskspkg "metiq/internal/tasks"
)

type workflowGatewayCallFunc func(ctx context.Context, method string, params json.RawMessage) (map[string]any, error)
type workflowStepStatePersistFunc func(ctx context.Context, run *taskspkg.WorkflowRun) error

type workflowExecutor struct {
	taskService  *taskspkg.Service
	ledger       *taskspkg.Ledger
	sessionStore *state.SessionStore
	gatewayCall  workflowGatewayCallFunc
	persistStep  workflowStepStatePersistFunc
	now          func() time.Time

	mu      sync.Mutex
	waiters map[string][]chan state.TaskRun
}

func newWorkflowExecutor(svc *daemonServices) *workflowExecutor {
	if svc == nil || svc.tasks.service == nil {
		return nil
	}
	ledger := svc.tasks.ledger
	if ledger == nil {
		ledger = svc.tasks.service.Ledger()
	}
	if ledger == nil {
		return nil
	}
	e := &workflowExecutor{
		taskService:  svc.tasks.service,
		ledger:       ledger,
		sessionStore: svc.session.sessionStore,
		now:          time.Now,
	}
	if svc.tasks.workflowStore != nil {
		e.persistStep = func(ctx context.Context, run *taskspkg.WorkflowRun) error {
			return svc.tasks.workflowStore.SaveRun(ctx, run)
		}
	}
	return e
}

func (e *workflowExecutor) ExecuteStep(ctx context.Context, run *taskspkg.WorkflowRun, step *taskspkg.StepRun, def *taskspkg.StepDefinition) error {
	if e == nil || e.taskService == nil || e.ledger == nil {
		return fmt.Errorf("workflow executor is not configured")
	}
	if run == nil || step == nil || def == nil {
		return fmt.Errorf("workflow run, step, and definition are required")
	}
	switch def.Type {
	case taskspkg.StepTypeAgentTurn:
		return e.executeAgentTurnStep(ctx, run, step, def)
	case taskspkg.StepTypeACPDispatch:
		return e.executeACPDispatchStep(ctx, run, step, def)
	case taskspkg.StepTypeGatewayCall:
		return e.executeGatewayCallStep(ctx, run, step, def)
	default:
		return fmt.Errorf("workflow step type %q is not supported by daemon executor", def.Type)
	}
}

func (e *workflowExecutor) OnTaskCreated(context.Context, taskspkg.LedgerEntry) {}
func (e *workflowExecutor) OnTaskUpdated(context.Context, taskspkg.LedgerEntry, state.TaskTransition) {
}
func (e *workflowExecutor) OnRunCreated(context.Context, taskspkg.RunEntry) {}

func (e *workflowExecutor) OnRunUpdated(ctx context.Context, entry taskspkg.RunEntry, transition state.TaskRunTransition) {
	if !workflowTaskRunTerminal(transition.To) {
		return
	}
	e.notifyRunTerminal(entry.Run.Normalize())
}

func (e *workflowExecutor) executeAgentTurnStep(ctx context.Context, run *taskspkg.WorkflowRun, step *taskspkg.StepRun, def *taskspkg.StepDefinition) error {
	task := e.childTask(run, step, def)
	task.Status = state.TaskStatusReady
	entry, err := e.taskService.CreateTask(ctx, task, taskspkg.TaskSourceWorkflow, workflowSourceRef(run, step), "workflow_executor")
	if err != nil {
		return err
	}
	runEntry, _, err := e.taskService.ResumeTask(ctx, entry.Task.TaskID, taskspkg.ResumeDecisionResume, "workflow_executor", "workflow step queued")
	if err != nil {
		return err
	}
	if err := e.linkStepRun(ctx, run, step, entry.Task.TaskID, runEntry.Run.RunID); err != nil {
		return err
	}
	e.recordWorkflowWorkerEvent(task.SessionID, entry.Task, runEntry.Run, step, "accepted", "workflow child task queued", "", 0)
	terminal, err := e.waitForRunTerminal(ctx, runEntry.Run.RunID)
	if err != nil {
		return err
	}
	step.Output = workflowStepOutputFromRun(terminal)
	e.recordWorkflowWorkerEvent(task.SessionID, entry.Task, terminal, step, workerStateFromRunStatus(terminal.Status), "workflow child task finished", terminal.Error, 1)
	if terminal.Status != state.TaskRunStatusCompleted {
		return workflowTerminalRunError(terminal)
	}
	return nil
}

func (e *workflowExecutor) executeACPDispatchStep(ctx context.Context, run *taskspkg.WorkflowRun, step *taskspkg.StepRun, def *taskspkg.StepDefinition) error {
	task, taskRun, err := e.startSynchronousChildRun(ctx, run, step, def, "workflow_acp_dispatch")
	if err != nil {
		return err
	}
	req := methods.ACPDispatchRequest{
		TargetPubKey:  strings.TrimSpace(def.Config.PeerPubKey),
		Instructions:  task.Instructions,
		Task:          &task,
		MemoryScope:   task.MemoryScope,
		ToolProfile:   task.ToolProfile,
		EnabledTools:  append([]string(nil), task.EnabledTools...),
		TimeoutMS:     def.Timeout,
		Wait:          true,
		ParentContext: &methods.ACPParentContextHint{SessionID: task.SessionID, AgentID: task.AssignedAgent},
	}
	params, _ := json.Marshal(req)
	out, callErr := e.callGateway(ctx, methods.MethodACPDispatch, params)
	return e.finishSynchronousChildRun(ctx, run, step, task, taskRun, out, callErr)
}

func (e *workflowExecutor) executeGatewayCallStep(ctx context.Context, run *taskspkg.WorkflowRun, step *taskspkg.StepRun, def *taskspkg.StepDefinition) error {
	task, taskRun, err := e.startSynchronousChildRun(ctx, run, step, def, "workflow_gateway_call")
	if err != nil {
		return err
	}
	method := strings.TrimSpace(def.Config.Method)
	if method == "" {
		err = fmt.Errorf("gateway_call method is required")
		return e.finishSynchronousChildRun(ctx, run, step, task, taskRun, nil, err)
	}
	params, err := json.Marshal(def.Config.Params)
	if err != nil {
		return e.finishSynchronousChildRun(ctx, run, step, task, taskRun, nil, err)
	}
	out, callErr := e.callGateway(ctx, method, params)
	return e.finishSynchronousChildRun(ctx, run, step, task, taskRun, out, callErr)
}

func (e *workflowExecutor) startSynchronousChildRun(ctx context.Context, run *taskspkg.WorkflowRun, step *taskspkg.StepRun, def *taskspkg.StepDefinition, trigger string) (state.TaskSpec, state.TaskRun, error) {
	task := e.childTask(run, step, def)
	task.Status = state.TaskStatusInProgress
	task, taskRun, err := e.taskService.StartWorkerRun(ctx, task, taskspkg.TaskSourceWorkflow, workflowSourceRef(run, step), "", trigger, "workflow_executor", "workflow step started", "")
	if err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	if err := e.linkStepRun(ctx, run, step, task.TaskID, taskRun.RunID); err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	e.recordWorkflowWorkerEvent(task.SessionID, task, taskRun, step, "running", "workflow child task started", "", 0)
	return task, taskRun, nil
}

func (e *workflowExecutor) finishSynchronousChildRun(ctx context.Context, wfRun *taskspkg.WorkflowRun, step *taskspkg.StepRun, task state.TaskSpec, run state.TaskRun, out map[string]any, callErr error) error {
	if out != nil {
		step.Output = cloneAnyMap(out)
	}
	resultRef := state.TaskResultRef{Kind: "workflow_step", ID: step.StepID}
	if _, _, err := e.taskService.FinishWorkerRun(ctx, task.TaskID, run.RunID, resultRef, state.TaskUsage{}, "workflow_executor", callErr, nil); err != nil {
		return err
	}
	if callErr != nil {
		e.recordWorkflowWorkerEvent(task.SessionID, task, run, step, "failed", "workflow child task failed", callErr.Error(), 1)
	} else {
		e.recordWorkflowWorkerEvent(task.SessionID, task, run, step, "completed", "workflow child task completed", "", 1)
	}
	if callErr != nil {
		return callErr
	}
	if err := e.persistLinkedStep(ctx, wfRun); err != nil {
		return err
	}
	return nil
}

func (e *workflowExecutor) callGateway(ctx context.Context, method string, params json.RawMessage) (map[string]any, error) {
	if e.gatewayCall == nil {
		return nil, fmt.Errorf("gateway call executor is not configured")
	}
	return e.gatewayCall(ctx, method, params)
}

func (e *workflowExecutor) childTask(run *taskspkg.WorkflowRun, step *taskspkg.StepRun, def *taskspkg.StepDefinition) state.TaskSpec {
	taskID := strings.TrimSpace(step.TaskID)
	if taskID == "" {
		taskID = workflowChildTaskID(run, step)
	}
	sessionID := stringFromMaps("session_id", def.Meta, step.Meta, run.Inputs, run.Meta)
	task := state.TaskSpec{
		TaskID:        taskID,
		Title:         firstNonEmptyTrimmed(def.Name, step.StepName, string(def.Type)+" step"),
		Instructions:  firstNonEmptyTrimmed(def.Config.Instructions, def.Name, step.StepName),
		SessionID:     sessionID,
		AssignedAgent: strings.TrimSpace(def.Config.AgentID),
		ToolProfile:   strings.TrimSpace(def.Config.ToolProfile),
		EnabledTools:  append([]string(nil), def.Config.EnabledTools...),
		Inputs:        workflowChildInputs(run, step, def),
		Meta: map[string]any{
			"workflow_id":        strings.TrimSpace(run.WorkflowID),
			"workflow_run_id":    strings.TrimSpace(run.RunID),
			"workflow_step_id":   strings.TrimSpace(step.StepID),
			"workflow_step_name": strings.TrimSpace(step.StepName),
			"workflow_step_type": string(def.Type),
		},
	}
	if goalID := stringFromMaps("goal_id", def.Meta, step.Meta, run.Inputs, run.Meta); goalID != "" {
		task.GoalID = goalID
	}
	if parentTaskID := stringFromMaps("parent_task_id", def.Meta, step.Meta, run.Inputs, run.Meta); parentTaskID != "" {
		task.ParentTaskID = parentTaskID
	}
	if task.Instructions == "" {
		switch def.Type {
		case taskspkg.StepTypeGatewayCall:
			task.Instructions = "Call gateway method " + strings.TrimSpace(def.Config.Method)
		case taskspkg.StepTypeACPDispatch:
			task.Instructions = "Dispatch ACP workflow step to " + strings.TrimSpace(def.Config.PeerPubKey)
		}
	}
	return task.Normalize()
}

func workflowChildInputs(run *taskspkg.WorkflowRun, step *taskspkg.StepRun, def *taskspkg.StepDefinition) map[string]any {
	inputs := map[string]any{
		"workflow_run_id": run.RunID,
		"workflow_id":     run.WorkflowID,
		"step_id":         step.StepID,
		"step_type":       string(def.Type),
	}
	if def.Config.Method != "" {
		inputs["method"] = def.Config.Method
		inputs["params"] = cloneAnyMap(def.Config.Params)
	}
	if def.Config.PeerPubKey != "" {
		inputs["peer_pubkey"] = def.Config.PeerPubKey
	}
	return inputs
}

func (e *workflowExecutor) linkStepRun(ctx context.Context, run *taskspkg.WorkflowRun, step *taskspkg.StepRun, taskID, runID string) error {
	step.TaskID = strings.TrimSpace(taskID)
	step.RunID = strings.TrimSpace(runID)
	if step.Meta == nil {
		step.Meta = map[string]any{}
	}
	step.Meta["task_id"] = step.TaskID
	step.Meta["run_id"] = step.RunID
	return e.persistLinkedStep(ctx, run)
}

func (e *workflowExecutor) persistLinkedStep(ctx context.Context, run *taskspkg.WorkflowRun) error {
	if e.persistStep == nil || run == nil {
		return nil
	}
	run.UpdatedAt = e.clock().Unix()
	return e.persistStep(ctx, run)
}

func (e *workflowExecutor) waitForRunTerminal(ctx context.Context, runID string) (state.TaskRun, error) {
	if current, ok, err := e.currentTerminalRun(ctx, runID); err != nil || ok {
		return current, err
	}
	ch := make(chan state.TaskRun, 1)
	e.mu.Lock()
	if e.waiters == nil {
		e.waiters = map[string][]chan state.TaskRun{}
	}
	e.waiters[runID] = append(e.waiters[runID], ch)
	e.mu.Unlock()
	defer e.removeRunWaiter(runID, ch)

	if current, ok, err := e.currentTerminalRun(ctx, runID); err != nil || ok {
		return current, err
	}
	select {
	case run := <-ch:
		return run, nil
	case <-ctx.Done():
		return state.TaskRun{}, ctx.Err()
	}
}

func (e *workflowExecutor) currentTerminalRun(ctx context.Context, runID string) (state.TaskRun, bool, error) {
	entry, err := e.ledger.GetRun(ctx, runID)
	if err != nil {
		return state.TaskRun{}, false, err
	}
	run := entry.Run.Normalize()
	return run, workflowTaskRunTerminal(run.Status), nil
}

func (e *workflowExecutor) notifyRunTerminal(run state.TaskRun) {
	runID := strings.TrimSpace(run.RunID)
	if runID == "" {
		return
	}
	e.mu.Lock()
	waiters := append([]chan state.TaskRun(nil), e.waiters[runID]...)
	delete(e.waiters, runID)
	e.mu.Unlock()
	for _, ch := range waiters {
		select {
		case ch <- run:
		default:
		}
	}
}

func (e *workflowExecutor) removeRunWaiter(runID string, ch chan state.TaskRun) {
	e.mu.Lock()
	defer e.mu.Unlock()
	waiters := e.waiters[runID]
	for i := range waiters {
		if waiters[i] == ch {
			waiters = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(waiters) == 0 {
		delete(e.waiters, runID)
	} else {
		e.waiters[runID] = waiters
	}
}

func (e *workflowExecutor) clock() time.Time {
	if e == nil || e.now == nil {
		return time.Now()
	}
	return e.now()
}

func workflowTaskRunTerminal(status state.TaskRunStatus) bool {
	switch status {
	case state.TaskRunStatusCompleted, state.TaskRunStatusFailed, state.TaskRunStatusCancelled:
		return true
	default:
		return false
	}
}

func workflowTerminalRunError(run state.TaskRun) error {
	if strings.TrimSpace(run.Error) != "" {
		return fmt.Errorf("child task run %s %s: %s", run.RunID, run.Status, run.Error)
	}
	return fmt.Errorf("child task run %s ended with status %s", run.RunID, run.Status)
}

func workflowStepOutputFromRun(run state.TaskRun) map[string]any {
	out := map[string]any{
		"task_id": run.TaskID,
		"run_id":  run.RunID,
		"status":  string(run.Status),
	}
	if !isZeroTaskResultRef(run.Result) {
		out["result"] = run.Result
	}
	if strings.TrimSpace(run.Error) != "" {
		out["error"] = run.Error
	}
	return out
}

func (e *workflowExecutor) recordWorkflowWorkerEvent(sessionID string, task state.TaskSpec, run state.TaskRun, step *taskspkg.StepRun, workerState, message, errMsg string, progress float64) {
	if e == nil || e.sessionStore == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	workerID := firstNonEmptyTrimmed(task.AssignedAgent, workflowWorkerID(task, step))
	sample := state.WorkerEventTelemetry{
		EventID:      fmt.Sprintf("we-%s-%s-%d", strings.TrimSpace(run.RunID), strings.TrimSpace(workerState), e.clock().UnixNano()),
		TaskID:       strings.TrimSpace(task.TaskID),
		RunID:        strings.TrimSpace(run.RunID),
		ParentTaskID: strings.TrimSpace(task.ParentTaskID),
		ParentRunID:  strings.TrimSpace(run.ParentRunID),
		GoalID:       strings.TrimSpace(task.GoalID),
		WorkerID:     workerID,
		State:        strings.TrimSpace(workerState),
		Message:      strings.TrimSpace(message),
		Error:        strings.TrimSpace(errMsg),
		CreatedAt:    e.clock().Unix(),
	}
	if step != nil {
		sample.StepID = strings.TrimSpace(step.StepID)
	}
	if progress > 0 {
		sample.Progress = &state.WorkerProgressTelemetry{
			PercentComplete: progress,
			StepID:          sample.StepID,
			Message:         sample.Message,
		}
	}
	if err := e.sessionStore.RecordWorkerEvent(sessionID, sample); err != nil {
		// Trace buffering is best-effort and must not fail workflow execution.
		return
	}
}

func workflowWorkerID(task state.TaskSpec, step *taskspkg.StepRun) string {
	if step != nil && strings.TrimSpace(step.StepName) != "" {
		return "workflow:" + strings.TrimSpace(step.StepName)
	}
	if raw, ok := task.Meta["workflow_step_type"].(string); ok && strings.TrimSpace(raw) != "" {
		return "workflow:" + strings.TrimSpace(raw)
	}
	return "workflow:child"
}

func workerStateFromRunStatus(status state.TaskRunStatus) string {
	switch status {
	case state.TaskRunStatusCompleted:
		return "completed"
	case state.TaskRunStatusFailed:
		return "failed"
	case state.TaskRunStatusCancelled:
		return "cancelled"
	default:
		return string(status)
	}
}

func workflowSourceRef(run *taskspkg.WorkflowRun, step *taskspkg.StepRun) string {
	return strings.Trim(strings.TrimSpace(run.RunID)+":"+strings.TrimSpace(step.StepID), ":")
}

func workflowChildTaskID(run *taskspkg.WorkflowRun, step *taskspkg.StepRun) string {
	base := sanitizeWorkflowIDPart(run.RunID) + "-" + sanitizeWorkflowIDPart(step.StepID)
	if base == "-" {
		base = "step"
	}
	return "wfstep-" + base + "-" + randomHex(4)
}

func sanitizeWorkflowIDPart(v string) string {
	v = strings.TrimSpace(v)
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-_")
}

func randomHex(n int) string {
	if n <= 0 {
		n = 4
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func stringFromMaps(key string, maps ...map[string]any) string {
	for _, m := range maps {
		if len(m) == 0 {
			continue
		}
		if v, ok := m[key]; ok {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func isZeroTaskResultRef(ref state.TaskResultRef) bool {
	return strings.TrimSpace(ref.Kind) == "" && strings.TrimSpace(ref.ID) == ""
}
