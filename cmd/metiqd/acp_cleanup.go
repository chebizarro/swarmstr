package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent"
	"metiq/internal/store/state"
)

const acpWorkerTaskMetaKey = "acp_worker_task"

func acpWorkerTaskMeta(agentID, peerPubKey, taskID, runID, parentTaskID, parentRunID string, payload acppkg.TaskPayload, startedAt time.Time) map[string]any {
	meta := map[string]any{
		"task_id":       strings.TrimSpace(taskID),
		"run_id":        strings.TrimSpace(runID),
		"agent_id":      strings.TrimSpace(agentID),
		"peer_pubkey":   strings.TrimSpace(peerPubKey),
		"started_at_ms": startedAt.UnixMilli(),
		"status":        "running",
	}
	if strings.TrimSpace(parentTaskID) != "" {
		meta["parent_task_id"] = strings.TrimSpace(parentTaskID)
	}
	if strings.TrimSpace(parentRunID) != "" {
		meta["parent_run_id"] = strings.TrimSpace(parentRunID)
	}
	if payload.TimeoutMS > 0 {
		meta["timeout_ms"] = payload.TimeoutMS
	}
	if parent := payload.ParentContext; parent != nil {
		parentMeta := map[string]any{}
		if sessionID := strings.TrimSpace(parent.SessionID); sessionID != "" {
			parentMeta["session_id"] = sessionID
		}
		if agentID := strings.TrimSpace(parent.AgentID); agentID != "" {
			parentMeta["agent_id"] = agentID
		}
		if len(parentMeta) > 0 {
			meta["parent_context"] = parentMeta
		}
	}
	return meta
}

func beginACPWorkerTask(ctx context.Context, docsRepo *state.DocsRepository, sessionID, peerPubKey, agentID, taskID string, payload acppkg.TaskPayload, startedAt time.Time) (state.TaskSpec, state.TaskRun, func(), error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return state.TaskSpec{}, state.TaskRun{}, nil, fmt.Errorf("session id is empty")
	}
	if docsRepo == nil {
		return state.TaskSpec{}, state.TaskRun{}, nil, fmt.Errorf("docs repository is nil")
	}
	task, run, err := startACPWorkerTaskDocs(ctx, docsRepo, sessionID, agentID, taskID, payload, startedAt)
	if err != nil {
		return state.TaskSpec{}, state.TaskRun{}, nil, err
	}
	parentTaskID := strings.TrimSpace(task.ParentTaskID)
	parentRunID := strings.TrimSpace(run.ParentRunID)
	if controlServices != nil && controlServices.session.sessionStore != nil {
		if err := controlServices.session.sessionStore.LinkTask(sessionID, task.TaskID, run.RunID, parentTaskID, parentRunID); err != nil {
			log.Printf("acp worker task session link failed session=%s task_id=%s run_id=%s err=%v", sessionID, task.TaskID, run.RunID, err)
		}
	}
	if docsRepo != nil && sessionID != "" {
		taskMeta := acpWorkerTaskMeta(agentID, peerPubKey, task.TaskID, run.RunID, parentTaskID, parentRunID, payload, startedAt)
		if err := updateSessionDoc(ctx, docsRepo, sessionID, peerPubKey, func(session *state.SessionDoc) error {
			session.Meta = mergeSessionMeta(session.Meta, map[string]any{
				"active_turn":        true,
				acpWorkerTaskMetaKey: taskMeta,
			})
			return nil
		}); err != nil {
			return state.TaskSpec{}, state.TaskRun{}, nil, err
		}
	}
	return task, run, func() {
		if docsRepo == nil || sessionID == "" {
			return
		}
		clearCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := updateSessionDoc(clearCtx, docsRepo, sessionID, peerPubKey, func(session *state.SessionDoc) error {
			session.Meta = mergeSessionMeta(session.Meta, map[string]any{
				"active_turn":        false,
				acpWorkerTaskMetaKey: nil,
			})
			return nil
		}); err != nil {
			log.Printf("acp worker task cleanup failed session=%s task_id=%s err=%v", sessionID, taskID, err)
		}
	}, nil
}

func startACPWorkerTaskDocs(ctx context.Context, docsRepo *state.DocsRepository, sessionID, agentID, taskID string, payload acppkg.TaskPayload, startedAt time.Time) (state.TaskSpec, state.TaskRun, error) {
	taskID = strings.TrimSpace(taskID)
	nowUnix := startedAt.Unix()
	task := state.TaskSpec{}
	if payload.Task != nil {
		task = payload.Task.Normalize()
	}
	if task.TaskID == "" {
		task.TaskID = taskID
	}
	if task.TaskID == "" {
		return state.TaskSpec{}, state.TaskRun{}, fmt.Errorf("task id is empty")
	}
	if task.Title == "" {
		task.Title = deriveACPTaskTitle(firstNonEmptyTrimmed(task.Instructions, payload.Instructions))
	}
	if task.Instructions == "" {
		task.Instructions = strings.TrimSpace(payload.Instructions)
	}
	if task.SessionID == "" {
		task.SessionID = sessionID
	}
	if task.AssignedAgent == "" {
		task.AssignedAgent = defaultAgentID(agentID)
	}
	if task.Meta == nil {
		task.Meta = map[string]any{}
	}
	parentRunID := taskMetaString(&task, "parent_run_id")
	if _, ok := task.Meta["parent_session_id"]; !ok && sessionID != "" {
		task.Meta["parent_session_id"] = sessionID
	}
	if existing, err := docsRepo.GetTask(ctx, task.TaskID); err == nil {
		existing = existing.Normalize()
		if task.GoalID == "" {
			task.GoalID = existing.GoalID
		}
		if task.ParentTaskID == "" {
			task.ParentTaskID = existing.ParentTaskID
		}
		if task.PlanID == "" {
			task.PlanID = existing.PlanID
		}
		if task.CreatedAt == 0 {
			task.CreatedAt = existing.CreatedAt
		}
		if len(task.Transitions) == 0 {
			task.Transitions = existing.Transitions
		}
		if len(task.Meta) == 0 {
			task.Meta = cloneTaskMeta(existing.Meta)
		}
	}
	if task.CreatedAt == 0 {
		task.CreatedAt = nowUnix
	}
	if len(task.Transitions) == 0 {
		task.Status = state.TaskStatusPending
		task.Transitions = []state.TaskTransition{{To: state.TaskStatusPending, At: nowUnix, Actor: strings.TrimSpace(agentID), Source: "acp_worker", Reason: "worker task created"}}
	}
	if task.Status != state.TaskStatusInProgress {
		if state.AllowedTaskTransition(task.Status, state.TaskStatusInProgress) {
			if err := task.ApplyTransition(state.TaskStatusInProgress, nowUnix, agentID, "acp_worker", "worker task started", nil); err != nil {
				return state.TaskSpec{}, state.TaskRun{}, err
			}
		}
	}
	priorRuns, err := docsRepo.ListTaskRuns(ctx, task.TaskID, 200)
	if err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	runID := fmt.Sprintf("%s-run-%d", task.TaskID, startedAt.UnixMilli())
	run, err := state.NewTaskRunAttempt(task, runID, priorRuns, nowUnix, "acp_task", agentID, "acp_worker")
	if err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	run.ParentRunID = parentRunID
	if err := run.ApplyTransition(state.TaskRunStatusRunning, nowUnix, agentID, "acp_worker", "worker run started", nil); err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	if _, err := docsRepo.PutTaskRun(ctx, run); err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	task.CurrentRunID = run.RunID
	task.LastRunID = run.RunID
	task.UpdatedAt = nowUnix
	if _, err := docsRepo.PutTask(ctx, task); err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	return task, run, nil
}

func finishACPWorkerTaskDocs(ctx context.Context, docsRepo *state.DocsRepository, sessionID string, task state.TaskSpec, run state.TaskRun, result state.TaskResultRef, turnResult *agent.TurnResultMetadata, turnErr error, historyEntryIDs []string) error {
	if docsRepo == nil {
		return fmt.Errorf("docs repository is nil")
	}
	nowUnix := time.Now().Unix()
	run = run.Normalize()
	task = task.Normalize()
	run.Result = result
	if turnResult != nil {
		run.Usage = state.TaskUsage{
			PromptTokens:     int(turnResult.Usage.InputTokens),
			CompletionTokens: int(turnResult.Usage.OutputTokens),
			TotalTokens:      int(turnResult.Usage.InputTokens + turnResult.Usage.OutputTokens),
		}
	}
	if turnErr != nil {
		run.Error = strings.TrimSpace(turnErr.Error())
		if run.Status != state.TaskRunStatusFailed && state.AllowedTaskRunTransition(run.Status, state.TaskRunStatusFailed) {
			if err := run.ApplyTransition(state.TaskRunStatusFailed, nowUnix, task.AssignedAgent, "acp_worker", run.Error, nil); err != nil {
				return err
			}
		}
		if task.Status != state.TaskStatusFailed && state.AllowedTaskTransition(task.Status, state.TaskStatusFailed) {
			if err := task.ApplyTransition(state.TaskStatusFailed, nowUnix, task.AssignedAgent, "acp_worker", run.Error, map[string]any{"run_id": run.RunID}); err != nil {
				return err
			}
		}
	} else {
		if run.Status != state.TaskRunStatusCompleted && state.AllowedTaskRunTransition(run.Status, state.TaskRunStatusCompleted) {
			if err := run.ApplyTransition(state.TaskRunStatusCompleted, nowUnix, task.AssignedAgent, "acp_worker", "worker run completed", nil); err != nil {
				return err
			}
		}
		if task.Meta == nil {
			task.Meta = map[string]any{}
		}
		if _, ok := task.Meta["verification_status"]; !ok {
			task.Meta["verification_status"] = "pending"
		}
		if len(historyEntryIDs) > 0 {
			task.Meta["result_history_entry_id"] = historyEntryIDs[len(historyEntryIDs)-1]
		}
		if task.Status != state.TaskStatusCompleted && state.AllowedTaskTransition(task.Status, state.TaskStatusCompleted) {
			if err := task.ApplyTransition(state.TaskStatusCompleted, nowUnix, task.AssignedAgent, "acp_worker", "worker task completed", map[string]any{"run_id": run.RunID}); err != nil {
				return err
			}
		}
	}
	task.CurrentRunID = ""
	task.LastRunID = run.RunID
	task.UpdatedAt = nowUnix
	if _, err := docsRepo.PutTaskRun(ctx, run); err != nil {
		return err
	}
	if _, err := docsRepo.PutTask(ctx, task); err != nil {
		return err
	}
	if controlServices != nil && controlServices.session.sessionStore != nil {
		if err := controlServices.session.sessionStore.RecordTaskResult(sessionID, task.TaskID, run.RunID, result); err != nil {
			log.Printf("acp worker task result persist failed session=%s task_id=%s run_id=%s err=%v", sessionID, task.TaskID, run.RunID, err)
		}
	}
	return nil
}
