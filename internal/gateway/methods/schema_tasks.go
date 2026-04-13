package methods

import (
	"encoding/json"
	"fmt"
	"metiq/internal/store/state"
	"strings"
)

type TasksCreateRequest struct {
	Task state.TaskSpec `json:"task"`
}

type TasksGetRequest struct {
	TaskID    string `json:"task_id"`
	RunsLimit int    `json:"runs_limit,omitempty"`
}

type TasksListRequest struct {
	Status        state.TaskStatus `json:"status,omitempty"`
	GoalID        string           `json:"goal_id,omitempty"`
	AssignedAgent string           `json:"assigned_agent,omitempty"`
	SessionID     string           `json:"session_id,omitempty"`
	ParentTaskID  string           `json:"parent_task_id,omitempty"`
	PlanID        string           `json:"plan_id,omitempty"`
	CreatedAfter  int64            `json:"created_after,omitempty"`
	UpdatedAfter  int64            `json:"updated_after,omitempty"`
	Limit         int              `json:"limit,omitempty"`
}

type TasksCancelRequest struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason,omitempty"`
}

type TasksResumeRequest struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason,omitempty"`
}

type TasksGetResponse struct {
	Task state.TaskSpec  `json:"task"`
	Runs []state.TaskRun `json:"runs,omitempty"`
}

type TasksListResponse struct {
	Tasks []state.TaskSpec `json:"tasks"`
	Count int              `json:"count"`
}

func (r TasksCreateRequest) Normalize() (TasksCreateRequest, error) {
	r.Task.Title = strings.TrimSpace(r.Task.Title)
	r.Task.Instructions = strings.TrimSpace(r.Task.Instructions)
	r.Task.ToolProfile = strings.TrimSpace(r.Task.ToolProfile)
	r.Task.EnabledTools = normalizeACPEnabledToolList(r.Task.EnabledTools)
	norm, err := normalizeACPTaskSpec(&r.Task, r.Task.Instructions, r.Task.MemoryScope, r.Task.ToolProfile, r.Task.EnabledTools)
	if err != nil {
		return r, err
	}
	if norm == nil {
		return r, fmt.Errorf("task is required")
	}
	r.Task = *norm
	r.Task.TaskID = strings.TrimSpace(r.Task.TaskID)
	r.Task.GoalID = strings.TrimSpace(r.Task.GoalID)
	r.Task.ParentTaskID = strings.TrimSpace(r.Task.ParentTaskID)
	r.Task.PlanID = strings.TrimSpace(r.Task.PlanID)
	r.Task.SessionID = strings.TrimSpace(r.Task.SessionID)
	r.Task.AssignedAgent = normalizeAgentID(r.Task.AssignedAgent)
	r.Task.CurrentRunID = strings.TrimSpace(r.Task.CurrentRunID)
	r.Task.LastRunID = strings.TrimSpace(r.Task.LastRunID)
	return r, nil
}

func DecodeTasksCreateParams(params json.RawMessage) (TasksCreateRequest, error) {
	return decodeMethodParams[TasksCreateRequest](params)
}

func (r TasksGetRequest) Normalize() (TasksGetRequest, error) {
	r.TaskID = strings.TrimSpace(r.TaskID)
	if r.TaskID == "" {
		return r, fmt.Errorf("task_id is required")
	}
	r.RunsLimit = normalizeLimit(r.RunsLimit, 20, 200)
	return r, nil
}

func DecodeTasksGetParams(params json.RawMessage) (TasksGetRequest, error) {
	return decodeMethodParams[TasksGetRequest](params)
}

func (r TasksListRequest) Normalize() (TasksListRequest, error) {
	r.GoalID = strings.TrimSpace(r.GoalID)
	r.AssignedAgent = normalizeAgentID(r.AssignedAgent)
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.ParentTaskID = strings.TrimSpace(r.ParentTaskID)
	r.PlanID = strings.TrimSpace(r.PlanID)
	if raw := strings.TrimSpace(string(r.Status)); raw != "" {
		status, ok := state.ParseTaskStatus(raw)
		if !ok {
			return r, fmt.Errorf("status is invalid")
		}
		r.Status = status
	}
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func DecodeTasksListParams(params json.RawMessage) (TasksListRequest, error) {
	return decodeMethodParams[TasksListRequest](params)
}

func (r TasksCancelRequest) Normalize() (TasksCancelRequest, error) {
	r.TaskID = strings.TrimSpace(r.TaskID)
	r.Reason = strings.TrimSpace(r.Reason)
	if r.TaskID == "" {
		return r, fmt.Errorf("task_id is required")
	}
	if r.Reason == "" {
		r.Reason = "cancelled via control rpc"
	}
	return r, nil
}

func DecodeTasksCancelParams(params json.RawMessage) (TasksCancelRequest, error) {
	return decodeMethodParams[TasksCancelRequest](params)
}

func (r TasksResumeRequest) Normalize() (TasksResumeRequest, error) {
	r.TaskID = strings.TrimSpace(r.TaskID)
	r.Reason = strings.TrimSpace(r.Reason)
	if r.TaskID == "" {
		return r, fmt.Errorf("task_id is required")
	}
	if r.Reason == "" {
		r.Reason = "resumed via control rpc"
	}
	return r, nil
}

func DecodeTasksResumeParams(params json.RawMessage) (TasksResumeRequest, error) {
	return decodeMethodParams[TasksResumeRequest](params)
}
