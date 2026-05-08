package methods

import (
	"context"
	"fmt"
	"strings"

	"metiq/internal/store/state"
	taskspkg "metiq/internal/tasks"
)

func BuildTaskGetResponse(ctx context.Context, taskService *taskspkg.Service, taskID string, runsLimit int) (TasksGetResponse, error) {
	if taskService == nil {
		return TasksGetResponse{}, fmt.Errorf("task service is nil")
	}
	task, runs, err := taskService.GetTask(ctx, taskID, runsLimit)
	if err != nil {
		return TasksGetResponse{}, err
	}
	return TasksGetResponse{Task: task, Runs: runs}, nil
}

func CreateTask(ctx context.Context, taskService *taskspkg.Service, req TasksCreateRequest, actor string) (TasksGetResponse, error) {
	if taskService == nil {
		return TasksGetResponse{}, fmt.Errorf("task service is nil")
	}
	entry, err := taskService.CreateTask(ctx, req.Task, taskspkg.TaskSourceManual, "control_rpc", actor)
	if err != nil {
		return TasksGetResponse{}, err
	}
	return BuildTaskGetResponse(ctx, taskService, entry.Task.TaskID, 20)
}

func ListFilteredTasks(ctx context.Context, taskService *taskspkg.Service, req TasksListRequest) (TasksListResponse, error) {
	if taskService == nil {
		return TasksListResponse{}, fmt.Errorf("task service is nil")
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
	opts := taskspkg.ListTasksOptions{
		AgentID:      req.AssignedAgent,
		GoalID:       req.GoalID,
		ParentTaskID: req.ParentTaskID,
		SessionID:    req.SessionID,
		Limit:        fetchLimit,
		OrderBy:      "created_at",
		OrderDesc:    false,
	}
	if req.Status != "" {
		opts.Status = []state.TaskStatus{req.Status}
	}
	entries, err := taskService.ListTasks(ctx, opts)
	if err != nil {
		return TasksListResponse{}, err
	}
	tasks := make([]state.TaskSpec, 0, len(entries))
	for _, entry := range entries {
		if entry != nil {
			tasks = append(tasks, entry.Task)
		}
	}
	filtered := FilterTasks(tasks, req)
	return TasksListResponse{Tasks: filtered, Count: len(filtered)}, nil
}

func CancelTask(ctx context.Context, taskService *taskspkg.Service, req TasksCancelRequest, actor string) (TasksGetResponse, error) {
	if taskService == nil {
		return TasksGetResponse{}, fmt.Errorf("task service is nil")
	}
	if err := taskService.CancelTask(ctx, req.TaskID, actor, req.Reason); err != nil {
		return TasksGetResponse{}, err
	}
	return BuildTaskGetResponse(ctx, taskService, req.TaskID, 20)
}

func ResumeTask(ctx context.Context, taskService *taskspkg.Service, req TasksResumeRequest, actor string) (TasksGetResponse, error) {
	if taskService == nil {
		return TasksGetResponse{}, fmt.Errorf("task service is nil")
	}
	if _, _, err := taskService.ResumeTask(ctx, req.TaskID, req.Decision, strings.TrimSpace(actor), req.Reason); err != nil {
		return TasksGetResponse{}, err
	}
	return BuildTaskGetResponse(ctx, taskService, req.TaskID, 20)
}
