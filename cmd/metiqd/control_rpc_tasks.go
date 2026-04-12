package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func buildControlTaskResponse(ctx context.Context, docsRepo *state.DocsRepository, taskID string, runsLimit int) (methods.TasksGetResponse, error) {
	if docsRepo == nil {
		return methods.TasksGetResponse{}, fmt.Errorf("docs repository is nil")
	}
	task, err := docsRepo.GetTask(ctx, taskID)
	if err != nil {
		return methods.TasksGetResponse{}, err
	}
	runs, err := docsRepo.ListTaskRuns(ctx, task.TaskID, runsLimit)
	if err != nil {
		return methods.TasksGetResponse{}, err
	}
	return methods.TasksGetResponse{Task: task, Runs: runs}, nil
}

func createControlTask(ctx context.Context, docsRepo *state.DocsRepository, req methods.TasksCreateRequest, actor string, now time.Time) (methods.TasksGetResponse, error) {
	if docsRepo == nil {
		return methods.TasksGetResponse{}, fmt.Errorf("docs repository is nil")
	}
	task := req.Task.Normalize()
	if strings.TrimSpace(task.TaskID) == "" {
		task.TaskID = fmt.Sprintf("task-%d", now.UnixNano())
	}
	task.CurrentRunID = ""
	task.LastRunID = ""
	at := now.Unix()
	if task.CreatedAt == 0 {
		task.CreatedAt = at
	}
	task.UpdatedAt = at
	requestedStatus := task.Status
	if !requestedStatus.Valid() {
		requestedStatus = state.TaskStatusPending
	}
	task.Status = state.TaskStatusPending
	task.Transitions = []state.TaskTransition{{To: state.TaskStatusPending, At: at, Actor: strings.TrimSpace(actor), Source: "control_rpc", Reason: "task created"}}
	if requestedStatus != state.TaskStatusPending {
		if err := task.ApplyTransition(requestedStatus, at, actor, "control_rpc", "task created", nil); err != nil {
			return methods.TasksGetResponse{}, err
		}
	}
	if _, err := docsRepo.PutTask(ctx, task); err != nil {
		return methods.TasksGetResponse{}, err
	}
	return buildControlTaskResponse(ctx, docsRepo, task.TaskID, 20)
}

func listControlTasks(ctx context.Context, docsRepo *state.DocsRepository, req methods.TasksListRequest) (methods.TasksListResponse, error) {
	if docsRepo == nil {
		return methods.TasksListResponse{}, fmt.Errorf("docs repository is nil")
	}
	fetchLimit := req.Limit
	if req.Status != "" || req.GoalID != "" || req.AssignedAgent != "" || req.SessionID != "" || req.ParentTaskID != "" || req.PlanID != "" || req.CreatedAfter > 0 || req.UpdatedAfter > 0 {
		fetchLimit = req.Limit * 8
		if fetchLimit < 500 {
			fetchLimit = 500
		}
		if fetchLimit > 5000 {
			fetchLimit = 5000
		}
	}
	tasks, err := docsRepo.ListTasks(ctx, fetchLimit)
	if err != nil {
		return methods.TasksListResponse{}, err
	}
	filtered := methods.FilterTasks(tasks, req)
	return methods.TasksListResponse{Tasks: filtered, Count: len(filtered)}, nil
}

func cancelControlTask(ctx context.Context, docsRepo *state.DocsRepository, req methods.TasksCancelRequest, actor string, now time.Time) (methods.TasksGetResponse, error) {
	if docsRepo == nil {
		return methods.TasksGetResponse{}, fmt.Errorf("docs repository is nil")
	}
	task, err := docsRepo.GetTask(ctx, req.TaskID)
	if err != nil {
		return methods.TasksGetResponse{}, err
	}
	at := now.Unix()
	currentRunID := strings.TrimSpace(task.CurrentRunID)
	if task.Status != state.TaskStatusCancelled {
		if err := task.ApplyTransition(state.TaskStatusCancelled, at, actor, "control_rpc", req.Reason, nil); err != nil {
			return methods.TasksGetResponse{}, err
		}
	}
	if currentRunID != "" {
		run, err := docsRepo.GetTaskRun(ctx, currentRunID)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				return methods.TasksGetResponse{}, err
			}
		} else {
			if run.Status != state.TaskRunStatusCancelled && state.AllowedTaskRunTransition(run.Status, state.TaskRunStatusCancelled) {
				if err := run.ApplyTransition(state.TaskRunStatusCancelled, at, actor, "control_rpc", req.Reason, nil); err != nil {
					return methods.TasksGetResponse{}, err
				}
				if _, err := docsRepo.PutTaskRun(ctx, run); err != nil {
					return methods.TasksGetResponse{}, err
				}
			}
		}
		task.CurrentRunID = ""
		task.LastRunID = currentRunID
	}
	task.UpdatedAt = at
	if _, err := docsRepo.PutTask(ctx, task); err != nil {
		return methods.TasksGetResponse{}, err
	}
	return buildControlTaskResponse(ctx, docsRepo, task.TaskID, 20)
}

func resumeControlTask(ctx context.Context, docsRepo *state.DocsRepository, req methods.TasksResumeRequest, actor string, now time.Time) (methods.TasksGetResponse, error) {
	if docsRepo == nil {
		return methods.TasksGetResponse{}, fmt.Errorf("docs repository is nil")
	}
	task, err := docsRepo.GetTask(ctx, req.TaskID)
	if err != nil {
		return methods.TasksGetResponse{}, err
	}
	at := now.Unix()
	priorRuns, err := docsRepo.ListTaskRuns(ctx, task.TaskID, 200)
	if err != nil {
		return methods.TasksGetResponse{}, err
	}
	var resumedRun state.TaskRun
	var haveRun bool
	currentRunID := strings.TrimSpace(task.CurrentRunID)
	if currentRunID != "" {
		run, err := docsRepo.GetTaskRun(ctx, currentRunID)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				return methods.TasksGetResponse{}, err
			}
		} else {
			if run.Status == state.TaskRunStatusQueued {
				resumedRun = run
				haveRun = true
			} else if state.AllowedTaskRunTransition(run.Status, state.TaskRunStatusQueued) {
				if err := run.ApplyTransition(state.TaskRunStatusQueued, at, actor, "control_rpc", req.Reason, nil); err != nil {
					return methods.TasksGetResponse{}, err
				}
				if _, err := docsRepo.PutTaskRun(ctx, run); err != nil {
					return methods.TasksGetResponse{}, err
				}
				resumedRun = run
				haveRun = true
			}
		}
	}
	if !haveRun {
		runID := fmt.Sprintf("taskrun-%d", now.UnixNano())
		run, err := state.NewTaskRunAttempt(task, runID, priorRuns, at, "resume", actor, "control_rpc")
		if err != nil {
			return methods.TasksGetResponse{}, err
		}
		if _, err := docsRepo.PutTaskRun(ctx, run); err != nil {
			return methods.TasksGetResponse{}, err
		}
		resumedRun = run
	}
	if task.Status != state.TaskStatusReady {
		if err := task.ApplyTransition(state.TaskStatusReady, at, actor, "control_rpc", req.Reason, map[string]any{"run_id": resumedRun.RunID}); err != nil {
			return methods.TasksGetResponse{}, err
		}
	}
	task.CurrentRunID = resumedRun.RunID
	task.LastRunID = resumedRun.RunID
	task.UpdatedAt = at
	if _, err := docsRepo.PutTask(ctx, task); err != nil {
		return methods.TasksGetResponse{}, err
	}
	return buildControlTaskResponse(ctx, docsRepo, task.TaskID, 20)
}

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
		result, err := createControlTask(ctx, docsRepo, req, in.FromPubKey, time.Now())
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
		result, err := buildControlTaskResponse(ctx, docsRepo, req.TaskID, req.RunsLimit)
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
		result, err := listControlTasks(ctx, docsRepo, req)
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
		result, err := cancelControlTask(ctx, docsRepo, req, in.FromPubKey, time.Now())
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
		result, err := resumeControlTask(ctx, docsRepo, req, in.FromPubKey, time.Now())
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
