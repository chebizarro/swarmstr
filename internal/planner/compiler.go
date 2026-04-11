// compiler.go materializes plan steps into executable TaskSpec entries
// and synchronizes step/task state as execution progresses.
package planner

import (
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// CompileRequest holds inputs for plan → task compilation.
type CompileRequest struct {
	Plan  state.PlanSpec
	Goal  state.GoalSpec // optional: enriches compiled tasks with goal-level fields
	Now   int64          // unix timestamp; 0 = use time.Now()
	Actor string         // who triggered compilation
}

// CompileResult holds the output of a compilation pass.
type CompileResult struct {
	// Tasks are newly-created TaskSpec entries for steps that became ready.
	Tasks []state.TaskSpec
	// UpdatedPlan is the plan with step statuses and task IDs updated.
	UpdatedPlan state.PlanSpec
	// StepTaskMap maps step_id → task_id for all materialized steps.
	StepTaskMap map[string]string
}

// Compile materializes ready plan steps into TaskSpec entries.
//
// It is idempotent: steps that already have a TaskID are skipped.
// Steps whose dependencies are all completed or skipped become tasks.
// The returned plan has step statuses and TaskIDs updated.
func Compile(req CompileRequest) (CompileResult, error) {
	plan := req.Plan.Normalize()
	if err := plan.Validate(); err != nil {
		return CompileResult{}, fmt.Errorf("compile: invalid plan: %w", err)
	}
	if plan.HasCycle() {
		return CompileResult{}, fmt.Errorf("compile: plan has cyclic dependencies")
	}

	now := req.Now
	if now <= 0 {
		now = time.Now().Unix()
	}

	// Build step-to-task mapping from already-materialized steps.
	stepTaskMap := make(map[string]string, len(plan.Steps))
	for _, step := range plan.Steps {
		if step.TaskID != "" {
			stepTaskMap[step.StepID] = step.TaskID
		}
	}

	// Build dependency resolution: step deps → task IDs.
	stepStatusByID := make(map[string]state.PlanStepStatus, len(plan.Steps))
	for _, step := range plan.Steps {
		stepStatusByID[step.StepID] = step.Status
	}

	var newTasks []state.TaskSpec
	updatedSteps := make([]state.PlanStep, len(plan.Steps))
	copy(updatedSteps, plan.Steps)

	for i, step := range updatedSteps {
		// Skip already-materialized, terminal, or blocked steps.
		if step.TaskID != "" {
			continue
		}
		if step.Status != state.PlanStepStatusPending {
			continue
		}

		// Check if all dependencies are satisfied.
		ready := true
		for _, dep := range step.DependsOn {
			ds := stepStatusByID[dep]
			if ds != state.PlanStepStatusCompleted && ds != state.PlanStepStatusSkipped {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}

		// Materialize this step as a task.
		taskID := fmt.Sprintf("task:%s:%s", plan.PlanID, step.StepID)

		// Map step dependencies to task dependencies.
		// Completed/skipped dependencies that were never materialized into tasks
		// (e.g. carried forward during replanning) are acceptable — they don't
		// need a task dependency edge since they're already terminal. But if a
		// dependency has a TaskID, we must include it.
		var taskDeps []string
		for _, dep := range step.DependsOn {
			if tid, ok := stepTaskMap[dep]; ok {
				taskDeps = append(taskDeps, tid)
			}
			// No TaskID is fine for completed/skipped deps (already validated
			// as satisfied above). Log-worthy but not an error.
		}

		task := state.TaskSpec{
			Version:      1,
			TaskID:       taskID,
			GoalID:       plan.GoalID,
			PlanID:       plan.PlanID,
			Title:        step.Title,
			Instructions: step.Instructions,
			Dependencies: taskDeps,
			Status:       state.TaskStatusReady,
			Priority:     req.Goal.Priority,
			CreatedAt:    now,
			UpdatedAt:    now,
			Meta: map[string]any{
				"plan_step_id": step.StepID,
			},
		}
		if task.Priority == "" {
			task.Priority = state.TaskPriorityMedium
		}

		// Inherit goal-level fields.
		if req.Goal.GoalID != "" {
			task.GoalID = req.Goal.GoalID
		}
		if step.Agent != "" {
			task.AssignedAgent = step.Agent
		}
		if len(step.Outputs) > 0 {
			task.ExpectedOutputs = step.Outputs
		}
		// Inherit goal authority/budget when present.
		task.Authority = req.Goal.Authority
		task.Budget = req.Goal.Budget

		newTasks = append(newTasks, task)
		stepTaskMap[step.StepID] = taskID

		// Mark step as ready and link to task.
		updatedSteps[i].Status = state.PlanStepStatusReady
		updatedSteps[i].TaskID = taskID
	}

	plan.Steps = updatedSteps
	plan.UpdatedAt = now

	return CompileResult{
		Tasks:       newTasks,
		UpdatedPlan: plan,
		StepTaskMap: stepTaskMap,
	}, nil
}

// SyncStepStates updates plan step statuses based on task execution results.
// Returns the updated plan and whether any steps changed.
func SyncStepStates(plan state.PlanSpec, taskStates map[string]state.TaskStatus) (state.PlanSpec, bool) {
	changed := false
	for i, step := range plan.Steps {
		if step.TaskID == "" {
			continue
		}
		taskStatus, ok := taskStates[step.TaskID]
		if !ok {
			continue
		}
		newStepStatus := taskStatusToPlanStepStatus(taskStatus)
		if newStepStatus == "" || newStepStatus == step.Status {
			continue
		}
		plan.Steps[i].Status = newStepStatus
		changed = true
	}
	if changed {
		plan.UpdatedAt = time.Now().Unix()
		// Check if all steps are terminal → plan is complete.
		plan = inferPlanCompletion(plan)
	}
	return plan, changed
}

// inferPlanCompletion checks if all steps are terminal and updates plan status.
func inferPlanCompletion(plan state.PlanSpec) state.PlanSpec {
	if plan.IsTerminal() {
		return plan
	}
	allDone := true
	anyFailed := false
	for _, step := range plan.Steps {
		switch step.Status {
		case state.PlanStepStatusCompleted, state.PlanStepStatusSkipped:
			// terminal success
		case state.PlanStepStatusFailed:
			anyFailed = true
		default:
			allDone = false
		}
	}
	if allDone {
		if anyFailed {
			plan.Status = state.PlanStatusFailed
		} else {
			plan.Status = state.PlanStatusCompleted
		}
	}
	return plan
}

// taskStatusToPlanStepStatus maps a task lifecycle status to the corresponding
// plan step status. Returns "" for unmappable statuses.
func taskStatusToPlanStepStatus(ts state.TaskStatus) state.PlanStepStatus {
	switch ts {
	case state.TaskStatusInProgress:
		return state.PlanStepStatusInProgress
	case state.TaskStatusCompleted:
		return state.PlanStepStatusCompleted
	case state.TaskStatusFailed:
		return state.PlanStepStatusFailed
	case state.TaskStatusCancelled:
		return state.PlanStepStatusSkipped
	case state.TaskStatusBlocked, state.TaskStatusAwaitingApproval:
		return state.PlanStepStatusBlocked
	default:
		return ""
	}
}

// TaskIDForStep returns the deterministic task ID for a plan step.
func TaskIDForStep(planID, stepID string) string {
	return fmt.Sprintf("task:%s:%s", strings.TrimSpace(planID), strings.TrimSpace(stepID))
}
