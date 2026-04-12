package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) handleTaskRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string) (nostruntime.ControlRPCResult, bool, error) {
	docsRepo := h.deps.docsRepo

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
		result, err := methods.CreateTask(ctx, docsRepo, req, in.FromPubKey, time.Now())
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
		result, err := methods.BuildTaskGetResponse(ctx, docsRepo, req.TaskID, req.RunsLimit)
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
		result, err := methods.ListFilteredTasks(ctx, docsRepo, req)
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
		result, err := methods.CancelTask(ctx, docsRepo, req, in.FromPubKey, time.Now())
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
		result, err := methods.ResumeTask(ctx, docsRepo, req, in.FromPubKey, time.Now())
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
		var memoryRecall []state.MemoryRecallSample
		if controlSessionStore != nil {
			for sessID, entry := range controlSessionStore.List() {
				if strings.TrimSpace(entry.ActiveTaskID) != req.TaskID && strings.TrimSpace(entry.ParentTaskID) != req.TaskID && sessID != strings.TrimSpace(task.SessionID) {
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
			}
		}
		traceInput := methods.TraceInput{Task: task, Runs: runs, TurnTelemetry: turnTelemetry, MemoryRecall: memoryRecall}
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
