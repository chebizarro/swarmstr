package planner

import (
	"testing"

	"metiq/internal/store/state"
)

func linearPlan() state.PlanSpec {
	return state.PlanSpec{
		Version:  1,
		PlanID:   "plan-linear",
		GoalID:   "goal-1",
		Title:    "Linear plan",
		Revision: 1,
		Status:   state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "s1", Title: "Step 1", Instructions: "Do first", Status: state.PlanStepStatusPending},
			{StepID: "s2", Title: "Step 2", Instructions: "Do second", DependsOn: []string{"s1"}, Status: state.PlanStepStatusPending},
			{StepID: "s3", Title: "Step 3", Instructions: "Do third", DependsOn: []string{"s2"}, Status: state.PlanStepStatusPending},
		},
	}
}

func parallelPlan() state.PlanSpec {
	return state.PlanSpec{
		Version:  1,
		PlanID:   "plan-parallel",
		GoalID:   "goal-2",
		Title:    "Parallel plan",
		Revision: 1,
		Status:   state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "a", Title: "Branch A", Instructions: "Do A", Status: state.PlanStepStatusPending},
			{StepID: "b", Title: "Branch B", Instructions: "Do B", Status: state.PlanStepStatusPending},
			{StepID: "c", Title: "Merge", Instructions: "Merge A+B", DependsOn: []string{"a", "b"}, Status: state.PlanStepStatusPending},
		},
	}
}

func TestCompile_LinearPlan_FirstStepOnly(t *testing.T) {
	result, err := Compile(CompileRequest{
		Plan: linearPlan(),
		Goal: state.GoalSpec{GoalID: "goal-1", Title: "Test", Priority: state.TaskPriorityHigh},
		Now:  1000,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// Only s1 should be materialized (no deps).
	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result.Tasks))
	}
	task := result.Tasks[0]
	if task.TaskID != "task:plan-linear:s1" {
		t.Errorf("task_id = %q", task.TaskID)
	}
	if task.GoalID != "goal-1" {
		t.Errorf("goal_id = %q", task.GoalID)
	}
	if task.PlanID != "plan-linear" {
		t.Errorf("plan_id = %q", task.PlanID)
	}
	if task.Status != state.TaskStatusReady {
		t.Errorf("status = %q, want ready", task.Status)
	}
	if task.Priority != state.TaskPriorityHigh {
		t.Errorf("priority = %q, want high", task.Priority)
	}
	if task.Meta["plan_step_id"] != "s1" {
		t.Errorf("meta.plan_step_id = %v", task.Meta["plan_step_id"])
	}
	// Plan step should be updated.
	if result.UpdatedPlan.Steps[0].TaskID != task.TaskID {
		t.Errorf("plan step not linked to task")
	}
	if result.UpdatedPlan.Steps[0].Status != state.PlanStepStatusReady {
		t.Errorf("plan step status = %q, want ready", result.UpdatedPlan.Steps[0].Status)
	}
}

func TestCompile_ParallelPlan_BothBranches(t *testing.T) {
	result, err := Compile(CompileRequest{
		Plan: parallelPlan(),
		Now:  1000,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// Both a and b should be materialized (no deps); c should not.
	if len(result.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(result.Tasks))
	}
	ids := map[string]bool{}
	for _, task := range result.Tasks {
		ids[task.TaskID] = true
	}
	if !ids["task:plan-parallel:a"] || !ids["task:plan-parallel:b"] {
		t.Errorf("expected tasks for a and b, got %v", ids)
	}
	// c should still be pending with no task ID.
	cStep := result.UpdatedPlan.Steps[2]
	if cStep.TaskID != "" {
		t.Errorf("step c should not have a task ID yet, got %q", cStep.TaskID)
	}
	if cStep.Status != state.PlanStepStatusPending {
		t.Errorf("step c status = %q, want pending", cStep.Status)
	}
}

func TestCompile_IncrementalAfterCompletion(t *testing.T) {
	plan := linearPlan()
	// Simulate s1 already materialized and completed.
	plan.Steps[0].TaskID = "task:plan-linear:s1"
	plan.Steps[0].Status = state.PlanStepStatusCompleted

	result, err := Compile(CompileRequest{Plan: plan, Now: 2000})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// s2 should now be materialized.
	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 new task, got %d", len(result.Tasks))
	}
	if result.Tasks[0].TaskID != "task:plan-linear:s2" {
		t.Errorf("task_id = %q", result.Tasks[0].TaskID)
	}
	// s2 task should depend on s1's task.
	if len(result.Tasks[0].Dependencies) != 1 || result.Tasks[0].Dependencies[0] != "task:plan-linear:s1" {
		t.Errorf("dependencies = %v", result.Tasks[0].Dependencies)
	}
}

func TestCompile_BlockedStepSkipped(t *testing.T) {
	plan := linearPlan()
	plan.Steps[0].Status = state.PlanStepStatusBlocked

	result, err := Compile(CompileRequest{Plan: plan, Now: 1000})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// Nothing should be materialized (s1 blocked, s2/s3 depend on s1).
	if len(result.Tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(result.Tasks))
	}
}

func TestCompile_Idempotent(t *testing.T) {
	result1, err := Compile(CompileRequest{Plan: parallelPlan(), Now: 1000})
	if err != nil {
		t.Fatalf("Compile 1: %v", err)
	}
	// Re-compile the updated plan — no new tasks should appear.
	result2, err := Compile(CompileRequest{Plan: result1.UpdatedPlan, Now: 2000})
	if err != nil {
		t.Fatalf("Compile 2: %v", err)
	}
	if len(result2.Tasks) != 0 {
		t.Fatalf("expected 0 new tasks on re-compile, got %d", len(result2.Tasks))
	}
}

func TestCompile_CyclicPlanRejected(t *testing.T) {
	cyclic := state.PlanSpec{
		PlanID: "plan-cycle",
		Title:  "Cyclic",
		Steps: []state.PlanStep{
			{StepID: "a", Title: "A", DependsOn: []string{"b"}},
			{StepID: "b", Title: "B", DependsOn: []string{"a"}},
		},
	}
	_, err := Compile(CompileRequest{Plan: cyclic, Now: 1000})
	if err == nil {
		t.Fatal("expected error for cyclic plan")
	}
}

func TestCompile_InheritsGoalAuthority(t *testing.T) {
	goal := state.GoalSpec{
		GoalID: "goal-auth",
		Title:  "Auth test",
		Authority: state.TaskAuthority{
			Role:        "worker",
			CanDelegate: true,
		},
		Budget: state.TaskBudget{
			MaxTotalTokens: 50000,
		},
	}
	plan := state.PlanSpec{
		PlanID: "plan-auth",
		GoalID: "goal-auth",
		Title:  "Auth plan",
		Status: state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "s1", Title: "Do", Instructions: "Do it", Status: state.PlanStepStatusPending},
		},
	}
	result, err := Compile(CompileRequest{Plan: plan, Goal: goal, Now: 1000})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result.Tasks))
	}
	if result.Tasks[0].Authority.Role != "worker" {
		t.Errorf("authority.role = %q", result.Tasks[0].Authority.Role)
	}
	if result.Tasks[0].Budget.MaxTotalTokens != 50000 {
		t.Errorf("budget.max_total_tokens = %d", result.Tasks[0].Budget.MaxTotalTokens)
	}
}

func TestCompile_StepAgentInherited(t *testing.T) {
	plan := state.PlanSpec{
		PlanID: "plan-agent",
		Title:  "Agent plan",
		Status: state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "s1", Title: "Do", Instructions: "Do it", Agent: "worker-1", Status: state.PlanStepStatusPending},
		},
	}
	result, err := Compile(CompileRequest{Plan: plan, Now: 1000})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if result.Tasks[0].AssignedAgent != "worker-1" {
		t.Errorf("assigned_agent = %q", result.Tasks[0].AssignedAgent)
	}
}

// ── SyncStepStates tests ──────────────────────────────────────────────────────

func TestSyncStepStates_UpdatesFromTaskCompletion(t *testing.T) {
	plan := linearPlan()
	plan.Steps[0].TaskID = "task:plan-linear:s1"
	plan.Steps[0].Status = state.PlanStepStatusInProgress

	taskStates := map[string]state.TaskStatus{
		"task:plan-linear:s1": state.TaskStatusCompleted,
	}

	updated, changed := SyncStepStates(plan, taskStates)
	if !changed {
		t.Fatal("expected changes")
	}
	if updated.Steps[0].Status != state.PlanStepStatusCompleted {
		t.Errorf("step status = %q, want completed", updated.Steps[0].Status)
	}
}

func TestSyncStepStates_NoChangeWhenAlreadySync(t *testing.T) {
	plan := linearPlan()
	plan.Steps[0].TaskID = "task:plan-linear:s1"
	plan.Steps[0].Status = state.PlanStepStatusCompleted

	taskStates := map[string]state.TaskStatus{
		"task:plan-linear:s1": state.TaskStatusCompleted,
	}

	_, changed := SyncStepStates(plan, taskStates)
	if changed {
		t.Fatal("expected no changes when already in sync")
	}
}

func TestSyncStepStates_PlanCompletesWhenAllDone(t *testing.T) {
	plan := state.PlanSpec{
		PlanID: "plan-done",
		Title:  "Done plan",
		Status: state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "s1", Title: "A", TaskID: "t1", Status: state.PlanStepStatusCompleted},
			{StepID: "s2", Title: "B", TaskID: "t2", Status: state.PlanStepStatusInProgress},
		},
	}
	taskStates := map[string]state.TaskStatus{
		"t2": state.TaskStatusCompleted,
	}
	updated, changed := SyncStepStates(plan, taskStates)
	if !changed {
		t.Fatal("expected changes")
	}
	if updated.Status != state.PlanStatusCompleted {
		t.Errorf("plan status = %q, want completed", updated.Status)
	}
}

func TestSyncStepStates_PlanFailsWhenStepFails(t *testing.T) {
	plan := state.PlanSpec{
		PlanID: "plan-fail",
		Title:  "Fail plan",
		Status: state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "s1", Title: "A", TaskID: "t1", Status: state.PlanStepStatusCompleted},
			{StepID: "s2", Title: "B", TaskID: "t2", Status: state.PlanStepStatusInProgress},
		},
	}
	taskStates := map[string]state.TaskStatus{
		"t2": state.TaskStatusFailed,
	}
	updated, changed := SyncStepStates(plan, taskStates)
	if !changed {
		t.Fatal("expected changes")
	}
	if updated.Status != state.PlanStatusFailed {
		t.Errorf("plan status = %q, want failed", updated.Status)
	}
}

func TestSyncStepStates_TaskCancelledMapsToSkipped(t *testing.T) {
	plan := state.PlanSpec{
		PlanID: "plan-cancel",
		Title:  "Cancel plan",
		Status: state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "s1", Title: "A", TaskID: "t1", Status: state.PlanStepStatusInProgress},
		},
	}
	taskStates := map[string]state.TaskStatus{
		"t1": state.TaskStatusCancelled,
	}
	updated, _ := SyncStepStates(plan, taskStates)
	if updated.Steps[0].Status != state.PlanStepStatusSkipped {
		t.Errorf("step status = %q, want skipped", updated.Steps[0].Status)
	}
}

func TestSyncStepStates_TaskBlockedMapsToBlocked(t *testing.T) {
	plan := state.PlanSpec{
		PlanID: "plan-block",
		Title:  "Block plan",
		Status: state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "s1", Title: "A", TaskID: "t1", Status: state.PlanStepStatusInProgress},
		},
	}
	taskStates := map[string]state.TaskStatus{
		"t1": state.TaskStatusBlocked,
	}
	updated, _ := SyncStepStates(plan, taskStates)
	if updated.Steps[0].Status != state.PlanStepStatusBlocked {
		t.Errorf("step status = %q, want blocked", updated.Steps[0].Status)
	}
}

func TestTaskIDForStep(t *testing.T) {
	got := TaskIDForStep("plan-1", "step-a")
	if got != "task:plan-1:step-a" {
		t.Errorf("TaskIDForStep = %q", got)
	}
}
