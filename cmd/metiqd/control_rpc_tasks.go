package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/planner"
	"metiq/internal/store/state"
	taskspkg "metiq/internal/tasks"
)

func (h controlRPCHandler) handleTaskRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string) (nostruntime.ControlRPCResult, bool, error) {
	docsRepo := h.deps.docsRepo
	taskService := h.deps.taskService
	if taskService == nil && docsRepo != nil {
		taskService = taskspkg.NewService(taskspkg.NewDocsStore(docsRepo))
	}

	switch method {
	case methods.MethodTasksCreate:
		req, err := methods.DecodeTasksCreateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.CreateTask(ctx, taskService, req, in.FromPubKey)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodTasksGet:
		req, err := methods.DecodeTasksGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.BuildTaskGetResponse(ctx, taskService, req.TaskID, req.RunsLimit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodTasksList:
		req, err := methods.DecodeTasksListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.ListFilteredTasks(ctx, taskService, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodTasksCancel:
		req, err := methods.DecodeTasksCancelParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.CancelTask(ctx, taskService, req, in.FromPubKey)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodTasksResume:
		req, err := methods.DecodeTasksResumeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.ResumeTask(ctx, taskService, req, in.FromPubKey)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodTasksDoctor:
		req, err := methods.DecodeTasksDoctorParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if docsRepo == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("docs repository is nil")
		}
		task, err := docsRepo.GetTask(ctx, req.TaskID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		runs, err := docsRepo.ListTaskRuns(ctx, task.TaskID, req.RunsLimit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		diag := methods.BuildTaskDiagnostic(task, runs, time.Now())
		return nostruntime.ControlRPCResult{Result: methods.TasksDoctorResponse{Task: task, Runs: runs, Doctor: diag}}, true, nil
	case methods.MethodTasksTrace:
		req, err := methods.DecodeTasksTraceParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if docsRepo == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("docs repository is nil")
		}
		task, err := docsRepo.GetTask(ctx, req.TaskID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		runs, err := docsRepo.ListTaskRuns(ctx, task.TaskID, 100)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var turnTelemetry []state.TurnTelemetry
		var toolLifecycle []state.ToolLifecycleTelemetry
		var memoryRecall []state.MemoryRecallSample
		var verificationEvents []planner.VerificationEvent
		var workerEvents []planner.WorkerEvent
		if h.deps.sessionStore != nil {
			for sessID, entry := range h.deps.sessionStore.List() {
				if !sessionEntryMayContainTaskTrace(sessID, entry, task, req.TaskID) {
					continue
				}
				if entry.LastTurn != nil && strings.TrimSpace(entry.LastTurn.TaskID) == req.TaskID {
					turnTelemetry = append(turnTelemetry, *entry.LastTurn)
				}
				for _, sample := range entry.RecentMemoryRecall {
					if strings.TrimSpace(sample.TaskID) == req.TaskID {
						memoryRecall = append(memoryRecall, sample)
					}
				}
				for _, sample := range entry.RecentToolLifecycle {
					if strings.TrimSpace(sample.TaskID) == req.TaskID {
						toolLifecycle = append(toolLifecycle, sample)
					}
				}
				for _, sample := range entry.RecentVerificationEvents {
					if strings.TrimSpace(sample.TaskID) == req.TaskID {
						verificationEvents = append(verificationEvents, verificationTelemetryToPlannerEvent(sample))
					}
				}
				for _, sample := range entry.RecentWorkerEvents {
					if strings.TrimSpace(sample.TaskID) == req.TaskID || strings.TrimSpace(sample.ParentTaskID) == req.TaskID {
						workerEvents = append(workerEvents, workerTelemetryToPlannerEvent(sample))
					}
				}
			}
		}
		traceInput := methods.TraceInput{Task: task, Runs: runs, TurnTelemetry: turnTelemetry, ToolLifecycle: toolLifecycle, MemoryRecall: memoryRecall, VerificationEvents: verificationEvents, WorkerEvents: workerEvents}
		traceResp := methods.AssembleTaskTrace(traceInput, req.RunID, req.Limit)
		return nostruntime.ControlRPCResult{Result: traceResp}, true, nil
	case methods.MethodTasksAuditExport:
		req, err := methods.DecodeAuditExportParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if docsRepo == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("docs repository is nil")
		}
		var scopeTasks []state.TaskSpec
		if req.TaskID != "" {
			allTasks, err := docsRepo.ListTasks(ctx, 5000)
			if err != nil {
				return nostruntime.ControlRPCResult{}, true, err
			}
			scopeTasks = methods.CollectDescendants(req.TaskID, allTasks)
			if len(scopeTasks) == 0 {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("task %q not found", req.TaskID)
			}
		} else {
			allTasks, err := docsRepo.ListTasks(ctx, 5000)
			if err != nil {
				return nostruntime.ControlRPCResult{}, true, err
			}
			for _, t := range allTasks {
				if strings.TrimSpace(t.GoalID) == req.GoalID {
					scopeTasks = append(scopeTasks, t)
				}
			}
		}
		runsByTask := make(map[string][]state.TaskRun, len(scopeTasks))
		for _, t := range scopeTasks {
			runs, err := docsRepo.ListTaskRuns(ctx, t.TaskID, req.RunsLimit)
			if err != nil {
				return nostruntime.ControlRPCResult{}, true, err
			}
			if len(runs) > 0 {
				runsByTask[t.TaskID] = runs
			}
		}
		bundle := methods.BuildAuditBundle(scopeTasks, runsByTask, req, in.FromPubKey, time.Now())
		return nostruntime.ControlRPCResult{Result: bundle}, true, nil
	case methods.MethodTasksSummary:
		req, err := methods.DecodeTasksSummaryParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if docsRepo == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("docs repository is nil")
		}
		tasks, err := docsRepo.ListTasks(ctx, 5000)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req.GoalID != "" {
			var filtered []state.TaskSpec
			for _, t := range tasks {
				if strings.TrimSpace(t.GoalID) == req.GoalID {
					filtered = append(filtered, t)
				}
			}
			tasks = filtered
		}
		summary := methods.BuildTasksSummary(tasks)
		return nostruntime.ControlRPCResult{Result: summary}, true, nil
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}

func sessionEntryMayContainTaskTrace(sessID string, entry state.SessionEntry, task state.TaskSpec, taskID string) bool {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}
	if strings.TrimSpace(entry.ActiveTaskID) == taskID || strings.TrimSpace(entry.LastCompletedTaskID) == taskID || strings.TrimSpace(entry.ParentTaskID) == taskID {
		return true
	}
	if strings.TrimSpace(sessID) != "" && strings.TrimSpace(sessID) == strings.TrimSpace(task.SessionID) {
		return true
	}
	for _, sample := range entry.RecentMemoryRecall {
		if strings.TrimSpace(sample.TaskID) == taskID {
			return true
		}
	}
	for _, sample := range entry.RecentToolLifecycle {
		if strings.TrimSpace(sample.TaskID) == taskID {
			return true
		}
	}
	for _, sample := range entry.RecentVerificationEvents {
		if strings.TrimSpace(sample.TaskID) == taskID {
			return true
		}
	}
	for _, sample := range entry.RecentWorkerEvents {
		if strings.TrimSpace(sample.TaskID) == taskID || strings.TrimSpace(sample.ParentTaskID) == taskID {
			return true
		}
	}
	return false
}

func verificationTelemetryToPlannerEvent(sample state.VerificationEventTelemetry) planner.VerificationEvent {
	return planner.VerificationEvent{
		Type:       planner.VerificationEventType(sample.Type),
		TaskID:     sample.TaskID,
		RunID:      sample.RunID,
		GoalID:     sample.GoalID,
		StepID:     sample.StepID,
		CheckID:    sample.CheckID,
		CheckType:  sample.CheckType,
		Status:     sample.Status,
		Result:     sample.Result,
		Evidence:   sample.Evidence,
		ReviewerID: sample.ReviewerID,
		Confidence: sample.Confidence,
		Duration:   sample.Duration,
		GateAction: sample.GateAction,
		CreatedAt:  sample.CreatedAt,
		Meta:       sample.Meta,
	}
}

func workerTelemetryToPlannerEvent(sample state.WorkerEventTelemetry) planner.WorkerEvent {
	event := planner.WorkerEvent{
		EventID:   sample.EventID,
		TaskID:    sample.TaskID,
		RunID:     sample.RunID,
		GoalID:    sample.GoalID,
		StepID:    sample.StepID,
		WorkerID:  sample.WorkerID,
		State:     planner.WorkerState(sample.State),
		Message:   sample.Message,
		ResultRef: sample.ResultRef,
		Error:     sample.Error,
		Usage:     sample.Usage,
		CreatedAt: sample.CreatedAt,
		Meta:      sample.Meta,
	}
	if sample.Progress != nil {
		event.Progress = &planner.ProgressInfo{
			PercentComplete: sample.Progress.PercentComplete,
			StepID:          sample.Progress.StepID,
			StepTotal:       sample.Progress.StepTotal,
			StepCurrent:     sample.Progress.StepCurrent,
			Message:         sample.Progress.Message,
		}
	}
	if sample.RejectInfo != nil {
		event.RejectInfo = &planner.RejectInfo{
			Reason:      sample.RejectInfo.Reason,
			Recoverable: sample.RejectInfo.Recoverable,
			Suggestion:  sample.RejectInfo.Suggestion,
		}
	}
	return event
}
