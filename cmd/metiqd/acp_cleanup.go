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
	taskspkg "metiq/internal/tasks"
)

const acpWorkerTaskMetaKey = "acp_worker_task"

func resolveACPTaskService(docsRepo *state.DocsRepository) *taskspkg.Service {
	if controlServices != nil && controlServices.tasks.service != nil {
		return controlServices.tasks.service
	}
	if docsRepo == nil {
		return nil
	}
	return taskspkg.NewService(taskspkg.NewDocsStore(docsRepo))
}

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
	taskService := resolveACPTaskService(docsRepo)
	if taskService == nil {
		return state.TaskSpec{}, state.TaskRun{}, fmt.Errorf("task service is nil")
	}
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
	if existing, _, err := taskService.GetTask(ctx, task.TaskID, 200); err == nil {
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
	runID := fmt.Sprintf("%s-run-%d", task.TaskID, startedAt.UnixMilli())
	return taskService.StartWorkerRun(ctx, task, taskspkg.TaskSourceACP, sessionID, runID, "acp_task", agentID, "worker task started", parentRunID)
}

func finishACPWorkerTaskDocs(ctx context.Context, docsRepo *state.DocsRepository, sessionID string, task state.TaskSpec, run state.TaskRun, result state.TaskResultRef, turnResult *agent.TurnResultMetadata, turnErr error, historyEntryIDs []string) error {
	taskService := resolveACPTaskService(docsRepo)
	if taskService == nil {
		return fmt.Errorf("task service is nil")
	}
	run = run.Normalize()
	task = task.Normalize()
	usage := state.TaskUsage{}
	if turnResult != nil {
		usage = state.TaskUsage{
			PromptTokens:     int(turnResult.Usage.InputTokens),
			CompletionTokens: int(turnResult.Usage.OutputTokens),
			TotalTokens:      int(turnResult.Usage.InputTokens + turnResult.Usage.OutputTokens),
		}
	}
	updatedTask, updatedRun, err := taskService.FinishWorkerRun(ctx, task.TaskID, run.RunID, result, usage, task.AssignedAgent, turnErr, historyEntryIDs)
	if err != nil {
		return err
	}
	task = updatedTask
	run = updatedRun
	sessionStore := controlSessionStore
	if controlServices != nil && controlServices.session.sessionStore != nil {
		sessionStore = controlServices.session.sessionStore
	}
	if sessionStore != nil {
		if err := sessionStore.RecordTaskResult(sessionID, task.TaskID, run.RunID, result); err != nil {
			log.Printf("acp worker task result persist failed session=%s task_id=%s run_id=%s err=%v", sessionID, task.TaskID, run.RunID, err)
		}
	}
	return nil
}
