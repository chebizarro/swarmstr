package state

import (
	"fmt"
	"strings"
)

// TaskTransition records a durable task state transition.
type TaskTransition struct {
	From   TaskStatus     `json:"from,omitempty"`
	To     TaskStatus     `json:"to"`
	At     int64          `json:"at"`
	Actor  string         `json:"actor,omitempty"`
	Source string         `json:"source,omitempty"`
	Reason string         `json:"reason,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}

// TaskRunTransition records a durable task-run state transition.
type TaskRunTransition struct {
	From   TaskRunStatus  `json:"from,omitempty"`
	To     TaskRunStatus  `json:"to"`
	At     int64          `json:"at"`
	Actor  string         `json:"actor,omitempty"`
	Source string         `json:"source,omitempty"`
	Reason string         `json:"reason,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}

func AllowedTaskTransition(from, to TaskStatus) bool {
	if to == "" || !to.Valid() {
		return false
	}
	if from == "" {
		return to == TaskStatusPending
	}
	switch from {
	case TaskStatusPending:
		switch to {
		case TaskStatusPlanned, TaskStatusReady, TaskStatusInProgress, TaskStatusBlocked, TaskStatusAwaitingApproval, TaskStatusCancelled, TaskStatusFailed:
			return true
		}
	case TaskStatusPlanned:
		switch to {
		case TaskStatusReady, TaskStatusInProgress, TaskStatusBlocked, TaskStatusAwaitingApproval, TaskStatusCancelled, TaskStatusFailed:
			return true
		}
	case TaskStatusReady:
		switch to {
		case TaskStatusInProgress, TaskStatusBlocked, TaskStatusAwaitingApproval, TaskStatusCancelled, TaskStatusFailed:
			return true
		}
	case TaskStatusInProgress:
		switch to {
		case TaskStatusBlocked, TaskStatusAwaitingApproval, TaskStatusVerifying, TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled:
			return true
		}
	case TaskStatusAwaitingApproval:
		switch to {
		case TaskStatusReady, TaskStatusInProgress, TaskStatusBlocked, TaskStatusCancelled, TaskStatusFailed:
			return true
		}
	case TaskStatusBlocked:
		switch to {
		case TaskStatusPlanned, TaskStatusReady, TaskStatusInProgress, TaskStatusAwaitingApproval, TaskStatusCancelled, TaskStatusFailed:
			return true
		}
	case TaskStatusVerifying:
		switch to {
		case TaskStatusCompleted, TaskStatusFailed, TaskStatusBlocked, TaskStatusAwaitingApproval:
			return true
		}
	case TaskStatusFailed:
		switch to {
		case TaskStatusPlanned, TaskStatusReady, TaskStatusInProgress, TaskStatusBlocked, TaskStatusAwaitingApproval, TaskStatusCancelled:
			return true
		}
	}
	return false
}

func AllowedTaskRunTransition(from, to TaskRunStatus) bool {
	if to == "" || !to.Valid() {
		return false
	}
	if from == "" {
		return to == TaskRunStatusQueued
	}
	switch from {
	case TaskRunStatusQueued:
		switch to {
		case TaskRunStatusRunning, TaskRunStatusBlocked, TaskRunStatusAwaitingApproval, TaskRunStatusCancelled, TaskRunStatusFailed:
			return true
		}
	case TaskRunStatusRunning:
		switch to {
		case TaskRunStatusBlocked, TaskRunStatusAwaitingApproval, TaskRunStatusRetrying, TaskRunStatusCompleted, TaskRunStatusFailed, TaskRunStatusCancelled:
			return true
		}
	case TaskRunStatusBlocked:
		switch to {
		case TaskRunStatusQueued, TaskRunStatusRunning, TaskRunStatusAwaitingApproval, TaskRunStatusRetrying, TaskRunStatusFailed, TaskRunStatusCancelled:
			return true
		}
	case TaskRunStatusAwaitingApproval:
		switch to {
		case TaskRunStatusQueued, TaskRunStatusRunning, TaskRunStatusBlocked, TaskRunStatusCancelled, TaskRunStatusFailed:
			return true
		}
	case TaskRunStatusRetrying:
		switch to {
		case TaskRunStatusQueued, TaskRunStatusRunning, TaskRunStatusBlocked, TaskRunStatusCancelled, TaskRunStatusFailed:
			return true
		}
	}
	return false
}

func (t *TaskSpec) ApplyTransition(to TaskStatus, at int64, actor, source, reason string, meta map[string]any) error {
	if t == nil {
		return fmt.Errorf("task is nil")
	}
	norm := t.Normalize()
	from := norm.Status
	if to == "" || !to.Valid() {
		return fmt.Errorf("invalid task status %q", to)
	}
	if from == to {
		return fmt.Errorf("task already in status %q", to)
	}
	if !AllowedTaskTransition(from, to) {
		return fmt.Errorf("illegal task transition %q -> %q", from, to)
	}
	if at <= 0 {
		return fmt.Errorf("transition timestamp is required")
	}
	if norm.CreatedAt == 0 {
		norm.CreatedAt = at
	}
	norm.Status = to
	norm.UpdatedAt = at
	norm.Transitions = append(norm.Transitions, TaskTransition{
		From:   from,
		To:     to,
		At:     at,
		Actor:  strings.TrimSpace(actor),
		Source: strings.TrimSpace(source),
		Reason: strings.TrimSpace(reason),
		Meta:   cloneTransitionMeta(meta),
	})
	*t = norm
	return nil
}

func (r *TaskRun) ApplyTransition(to TaskRunStatus, at int64, actor, source, reason string, meta map[string]any) error {
	if r == nil {
		return fmt.Errorf("task run is nil")
	}
	norm := r.Normalize()
	from := norm.Status
	if to == "" || !to.Valid() {
		return fmt.Errorf("invalid run status %q", to)
	}
	if from == to {
		return fmt.Errorf("task run already in status %q", to)
	}
	if !AllowedTaskRunTransition(from, to) {
		return fmt.Errorf("illegal run transition %q -> %q", from, to)
	}
	if at <= 0 {
		return fmt.Errorf("transition timestamp is required")
	}
	if to == TaskRunStatusRunning && norm.StartedAt == 0 {
		norm.StartedAt = at
	}
	if isTerminalTaskRunStatus(to) {
		norm.EndedAt = at
	}
	norm.Status = to
	norm.Transitions = append(norm.Transitions, TaskRunTransition{
		From:   from,
		To:     to,
		At:     at,
		Actor:  strings.TrimSpace(actor),
		Source: strings.TrimSpace(source),
		Reason: strings.TrimSpace(reason),
		Meta:   cloneTransitionMeta(meta),
	})
	*r = norm
	return nil
}

func NewTaskRunAttempt(task TaskSpec, runID string, priorRuns []TaskRun, at int64, trigger, actor, source string) (TaskRun, error) {
	task = task.Normalize()
	if err := task.Validate(); err != nil {
		return TaskRun{}, err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return TaskRun{}, fmt.Errorf("run_id is required")
	}
	attempt := 1
	for _, prior := range priorRuns {
		if strings.TrimSpace(prior.RunID) == runID {
			return TaskRun{}, fmt.Errorf("run_id %q already exists", runID)
		}
		if strings.TrimSpace(prior.TaskID) != task.TaskID {
			continue
		}
		if prior.Attempt >= attempt {
			attempt = prior.Attempt + 1
		}
	}
	if at <= 0 {
		return TaskRun{}, fmt.Errorf("attempt timestamp is required")
	}
	run := TaskRun{
		Version:     1,
		RunID:       runID,
		TaskID:      task.TaskID,
		GoalID:      task.GoalID,
		SessionID:   task.SessionID,
		AgentID:     task.AssignedAgent,
		Attempt:     attempt,
		Status:      TaskRunStatusQueued,
		Trigger:     strings.TrimSpace(trigger),
		Transitions: []TaskRunTransition{{To: TaskRunStatusQueued, At: at, Actor: strings.TrimSpace(actor), Source: strings.TrimSpace(source), Reason: "attempt created"}},
	}
	return run, nil
}

func isTerminalTaskRunStatus(status TaskRunStatus) bool {
	switch status {
	case TaskRunStatusCompleted, TaskRunStatusFailed, TaskRunStatusCancelled:
		return true
	default:
		return false
	}
}

func cloneTransitionMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}
