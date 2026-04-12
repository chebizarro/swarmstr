package methods

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

func BuildTaskGetResponse(ctx context.Context, docsRepo *state.DocsRepository, taskID string, runsLimit int) (TasksGetResponse, error) {
	if docsRepo == nil {
		return TasksGetResponse{}, fmt.Errorf("docs repository is nil")
	}
	task, err := docsRepo.GetTask(ctx, taskID)
	if err != nil {
		return TasksGetResponse{}, err
	}
	runs, err := docsRepo.ListTaskRuns(ctx, task.TaskID, runsLimit)
	if err != nil {
		return TasksGetResponse{}, err
	}
	return TasksGetResponse{Task: task, Runs: runs}, nil
}

func CreateTask(ctx context.Context, docsRepo *state.DocsRepository, req TasksCreateRequest, actor string, now time.Time) (TasksGetResponse, error) {
	if docsRepo == nil {
		return TasksGetResponse{}, fmt.Errorf("docs repository is nil")
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
			return TasksGetResponse{}, err
		}
	}
	if _, err := docsRepo.PutTask(ctx, task); err != nil {
		return TasksGetResponse{}, err
	}
	return BuildTaskGetResponse(ctx, docsRepo, task.TaskID, 20)
}

func ListFilteredTasks(ctx context.Context, docsRepo *state.DocsRepository, req TasksListRequest) (TasksListResponse, error) {
	if docsRepo == nil {
		return TasksListResponse{}, fmt.Errorf("docs repository is nil")
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
		return TasksListResponse{}, err
	}
	filtered := FilterTasks(tasks, req)
	return TasksListResponse{Tasks: filtered, Count: len(filtered)}, nil
}

func CancelTask(ctx context.Context, docsRepo *state.DocsRepository, req TasksCancelRequest, actor string, now time.Time) (TasksGetResponse, error) {
	if docsRepo == nil {
		return TasksGetResponse{}, fmt.Errorf("docs repository is nil")
	}
	task, err := docsRepo.GetTask(ctx, req.TaskID)
	if err != nil {
		return TasksGetResponse{}, err
	}
	at := now.Unix()
	currentRunID := strings.TrimSpace(task.CurrentRunID)
	if task.Status != state.TaskStatusCancelled {
		if err := task.ApplyTransition(state.TaskStatusCancelled, at, actor, "control_rpc", req.Reason, nil); err != nil {
			return TasksGetResponse{}, err
		}
	}
	if currentRunID != "" {
		run, err := docsRepo.GetTaskRun(ctx, currentRunID)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				return TasksGetResponse{}, err
			}
		} else {
			if run.Status != state.TaskRunStatusCancelled && state.AllowedTaskRunTransition(run.Status, state.TaskRunStatusCancelled) {
				if err := run.ApplyTransition(state.TaskRunStatusCancelled, at, actor, "control_rpc", req.Reason, nil); err != nil {
					return TasksGetResponse{}, err
				}
				if _, err := docsRepo.PutTaskRun(ctx, run); err != nil {
					return TasksGetResponse{}, err
				}
			}
		}
		task.CurrentRunID = ""
		task.LastRunID = currentRunID
	}
	task.UpdatedAt = at
	if _, err := docsRepo.PutTask(ctx, task); err != nil {
		return TasksGetResponse{}, err
	}
	return BuildTaskGetResponse(ctx, docsRepo, task.TaskID, 20)
}

func ResumeTask(ctx context.Context, docsRepo *state.DocsRepository, req TasksResumeRequest, actor string, now time.Time) (TasksGetResponse, error) {
	if docsRepo == nil {
		return TasksGetResponse{}, fmt.Errorf("docs repository is nil")
	}
	task, err := docsRepo.GetTask(ctx, req.TaskID)
	if err != nil {
		return TasksGetResponse{}, err
	}
	at := now.Unix()
	priorRuns, err := docsRepo.ListTaskRuns(ctx, task.TaskID, 200)
	if err != nil {
		return TasksGetResponse{}, err
	}
	var resumedRun state.TaskRun
	var haveRun bool
	currentRunID := strings.TrimSpace(task.CurrentRunID)
	if currentRunID != "" {
		run, err := docsRepo.GetTaskRun(ctx, currentRunID)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				return TasksGetResponse{}, err
			}
		} else {
			if run.Status == state.TaskRunStatusQueued {
				resumedRun = run
				haveRun = true
			} else if state.AllowedTaskRunTransition(run.Status, state.TaskRunStatusQueued) {
				if err := run.ApplyTransition(state.TaskRunStatusQueued, at, actor, "control_rpc", req.Reason, nil); err != nil {
					return TasksGetResponse{}, err
				}
				if _, err := docsRepo.PutTaskRun(ctx, run); err != nil {
					return TasksGetResponse{}, err
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
			return TasksGetResponse{}, err
		}
		if _, err := docsRepo.PutTaskRun(ctx, run); err != nil {
			return TasksGetResponse{}, err
		}
		resumedRun = run
	}
	if task.Status != state.TaskStatusReady {
		if err := task.ApplyTransition(state.TaskStatusReady, at, actor, "control_rpc", req.Reason, map[string]any{"run_id": resumedRun.RunID}); err != nil {
			return TasksGetResponse{}, err
		}
	}
	task.CurrentRunID = resumedRun.RunID
	task.LastRunID = resumedRun.RunID
	task.UpdatedAt = at
	if _, err := docsRepo.PutTask(ctx, task); err != nil {
		return TasksGetResponse{}, err
	}
	return BuildTaskGetResponse(ctx, docsRepo, task.TaskID, 20)
}
