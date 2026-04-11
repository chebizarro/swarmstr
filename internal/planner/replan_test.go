package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"metiq/internal/store/state"
)

// --- NeedsReplan tests ---

func TestNeedsReplan_HealthyPlan(t *testing.T) {
	plan := state.PlanSpec{
		Status: state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "a", Status: state.PlanStepStatusPending},
			{StepID: "b", Status: state.PlanStepStatusCompleted},
		},
	}
	trigger, needs := NeedsReplan(plan)
	if needs {
		t.Errorf("healthy plan should not need replan, got trigger=%q", trigger)
	}
}

func TestNeedsReplan_CompletedPlan(t *testing.T) {
	plan := state.PlanSpec{
		Status: state.PlanStatusCompleted,
		Steps: []state.PlanStep{
			{StepID: "a", Status: state.PlanStepStatusCompleted},
		},
	}
	_, needs := NeedsReplan(plan)
	if needs {
		t.Error("completed plan should not need replan")
	}
}

func TestNeedsReplan_FailedPlan(t *testing.T) {
	plan := state.PlanSpec{
		Status: state.PlanStatusFailed,
		Steps: []state.PlanStep{
			{StepID: "a", Status: state.PlanStepStatusFailed},
		},
	}
	trigger, needs := NeedsReplan(plan)
	if !needs {
		t.Fatal("failed plan should need replan")
	}
	if trigger != ReplanTriggerPlanFailed {
		t.Errorf("trigger = %q, want %q", trigger, ReplanTriggerPlanFailed)
	}
}

func TestNeedsReplan_FailedStep(t *testing.T) {
	plan := state.PlanSpec{
		Status: state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "a", Status: state.PlanStepStatusCompleted},
			{StepID: "b", Status: state.PlanStepStatusFailed},
		},
	}
	trigger, needs := NeedsReplan(plan)
	if !needs {
		t.Fatal("plan with failed step should need replan")
	}
	if trigger != ReplanTriggerStepFailed {
		t.Errorf("trigger = %q, want %q", trigger, ReplanTriggerStepFailed)
	}
}

func TestNeedsReplan_BlockedStepsNoProgress(t *testing.T) {
	// All remaining steps are blocked — no ready steps means we trigger.
	plan := state.PlanSpec{
		Status: state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "a", Status: state.PlanStepStatusCompleted},
			{StepID: "b", Status: state.PlanStepStatusBlocked, DependsOn: []string{"a"}},
		},
	}
	trigger, needs := NeedsReplan(plan)
	if !needs {
		t.Fatal("plan with all blocked and no ready steps should need replan")
	}
	if trigger != ReplanTriggerStepBlocked {
		t.Errorf("trigger = %q, want %q", trigger, ReplanTriggerStepBlocked)
	}
}

func TestNeedsReplan_BlockedStepsWithReadyOnes(t *testing.T) {
	// There's a blocked step but other ready steps exist — no trigger.
	plan := state.PlanSpec{
		Status: state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "a", Status: state.PlanStepStatusPending},
			{StepID: "b", Status: state.PlanStepStatusBlocked},
		},
	}
	_, needs := NeedsReplan(plan)
	if needs {
		t.Error("plan with blocked step but ready steps should not need replan")
	}
}

// --- Replan end-to-end tests ---

func replanGoal() state.GoalSpec {
	return state.GoalSpec{
		GoalID:       "goal-replan",
		Title:        "Deploy revised service",
		Instructions: "Fix deployment issues and retry.",
	}
}

func replanPlanJSON() string {
	return `{
  "plan_id": "plan-replan",
  "title": "Revised deployment plan",
  "steps": [
    {"step_id": "fix", "title": "Fix the build", "instructions": "Resolve build error", "status": "pending"},
    {"step_id": "deploy", "title": "Deploy again", "instructions": "Push fixed container", "depends_on": ["fix"], "status": "pending"}
  ],
  "assumptions": ["Build error is known"]
}`
}

func TestReplan_EndToEnd(t *testing.T) {
	provider := &fakeProvider{text: replanPlanJSON()}
	p := New(provider)

	current := state.PlanSpec{
		Version:  1,
		PlanID:   "plan-original",
		GoalID:   "goal-replan",
		Title:    "Original plan",
		Revision: 1,
		Status:   state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "build", Title: "Build", Instructions: "go build", Status: state.PlanStepStatusCompleted, TaskID: "task:1"},
			{StepID: "test", Title: "Test", Instructions: "go test", Status: state.PlanStepStatusFailed, DependsOn: []string{"build"}},
			{StepID: "deploy", Title: "Deploy", Instructions: "push", Status: state.PlanStepStatusPending, DependsOn: []string{"test"}},
		},
		CreatedAt: 1000,
	}

	newPlan, revision, err := p.Replan(context.Background(), ReplanRequest{
		CurrentPlan: current,
		Goal:        replanGoal(),
		Trigger:     ReplanTriggerStepFailed,
		Reason:      "test step failed",
		Evidence:    map[string]string{"test": "exit code 1"},
		Now:         2000,
	})
	if err != nil {
		t.Fatalf("Replan: %v", err)
	}

	// Identity preserved.
	if newPlan.PlanID != "plan-original" {
		t.Errorf("PlanID = %q, want plan-original", newPlan.PlanID)
	}
	if newPlan.GoalID != "goal-replan" {
		t.Errorf("GoalID = %q, want goal-replan", newPlan.GoalID)
	}
	// Revision bumped.
	if newPlan.Revision != 2 {
		t.Errorf("Revision = %d, want 2", newPlan.Revision)
	}
	// Status is active.
	if newPlan.Status != state.PlanStatusActive {
		t.Errorf("Status = %q, want active", newPlan.Status)
	}
	// CreatedAt preserved from original.
	if newPlan.CreatedAt != 1000 {
		t.Errorf("CreatedAt = %d, want 1000 (preserved)", newPlan.CreatedAt)
	}

	// Completed step carried forward.
	foundBuild := false
	for _, s := range newPlan.Steps {
		if s.StepID == "build" {
			foundBuild = true
			if s.Status != state.PlanStepStatusCompleted {
				t.Errorf("carried-forward build step status = %q, want completed", s.Status)
			}
			if s.TaskID != "task:1" {
				t.Errorf("carried-forward build step TaskID = %q, want task:1", s.TaskID)
			}
		}
	}
	if !foundBuild {
		t.Error("completed build step should be carried forward into new plan")
	}

	// Revision metadata.
	if revision.PlanID != "plan-original" {
		t.Errorf("revision PlanID = %q", revision.PlanID)
	}
	if revision.FromVersion != 1 || revision.ToVersion != 2 {
		t.Errorf("revision versions = %d→%d, want 1→2", revision.FromVersion, revision.ToVersion)
	}
	if revision.Trigger != ReplanTriggerStepFailed {
		t.Errorf("revision trigger = %q", revision.Trigger)
	}
	if revision.Evidence["test"] != "exit code 1" {
		t.Errorf("revision evidence = %v", revision.Evidence)
	}
}

func TestReplan_NilProvider(t *testing.T) {
	p := New(nil)
	_, _, err := p.Replan(context.Background(), ReplanRequest{
		CurrentPlan: state.PlanSpec{PlanID: "x", Revision: 1},
		Goal:        replanGoal(),
		Trigger:     ReplanTriggerManual,
	})
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestReplan_ProviderError(t *testing.T) {
	p := New(&fakeProvider{err: fmt.Errorf("timeout")})
	_, _, err := p.Replan(context.Background(), ReplanRequest{
		CurrentPlan: state.PlanSpec{PlanID: "x", Revision: 1},
		Goal:        replanGoal(),
		Trigger:     ReplanTriggerManual,
	})
	if err == nil {
		t.Fatal("expected error from provider")
	}
}

// --- DiffPlans tests ---

func TestDiffPlans_AddedRemovedChanged(t *testing.T) {
	old := state.PlanSpec{
		PlanID:   "plan-1",
		Revision: 1,
		Steps: []state.PlanStep{
			{StepID: "a", Title: "Step A", Instructions: "do A", Status: state.PlanStepStatusCompleted},
			{StepID: "b", Title: "Step B", Instructions: "do B", Status: state.PlanStepStatusFailed},
			{StepID: "c", Title: "Step C", Instructions: "do C", Status: state.PlanStepStatusPending},
		},
	}
	newPlan := state.PlanSpec{
		PlanID:   "plan-1",
		Revision: 2,
		Steps: []state.PlanStep{
			{StepID: "a", Title: "Step A", Instructions: "do A", Status: state.PlanStepStatusCompleted}, // unchanged
			{StepID: "b", Title: "Step B revised", Instructions: "do B differently", Status: state.PlanStepStatusPending}, // changed
			{StepID: "d", Title: "Step D", Instructions: "new step", Status: state.PlanStepStatusPending}, // added
			// c removed
		},
	}

	rev := DiffPlans(old, newPlan, ReplanTriggerStepFailed, "test failed", "agent", 3000)

	if rev.PlanID != "plan-1" {
		t.Errorf("PlanID = %q", rev.PlanID)
	}
	if rev.FromVersion != 1 || rev.ToVersion != 2 {
		t.Errorf("versions = %d→%d", rev.FromVersion, rev.ToVersion)
	}

	// Added: d
	if len(rev.StepsAdded) != 1 || rev.StepsAdded[0] != "d" {
		t.Errorf("StepsAdded = %v, want [d]", rev.StepsAdded)
	}
	// Removed: c
	if len(rev.StepsRemoved) != 1 || rev.StepsRemoved[0] != "c" {
		t.Errorf("StepsRemoved = %v, want [c]", rev.StepsRemoved)
	}
	// Changed: b (title, instructions, status all differ)
	if len(rev.StepsChanged) != 1 || rev.StepsChanged[0] != "b" {
		t.Errorf("StepsChanged = %v, want [b]", rev.StepsChanged)
	}
}

func TestDiffPlans_IdenticalPlans(t *testing.T) {
	plan := state.PlanSpec{
		PlanID:   "p",
		Revision: 1,
		Steps: []state.PlanStep{
			{StepID: "a", Title: "A", Instructions: "do", Status: state.PlanStepStatusPending},
		},
	}
	rev := DiffPlans(plan, plan, ReplanTriggerManual, "", "", 0)
	if len(rev.StepsAdded)+len(rev.StepsRemoved)+len(rev.StepsChanged) != 0 {
		t.Errorf("identical plans should have no diff, got added=%v removed=%v changed=%v",
			rev.StepsAdded, rev.StepsRemoved, rev.StepsChanged)
	}
}

// --- stepDiffers tests ---

func TestStepDiffers_Identical(t *testing.T) {
	s := state.PlanStep{StepID: "a", Title: "A", Instructions: "do A", Status: state.PlanStepStatusPending, DependsOn: []string{"x"}}
	if stepDiffers(s, s) {
		t.Error("identical steps should not differ")
	}
}

func TestStepDiffers_TitleChange(t *testing.T) {
	a := state.PlanStep{StepID: "a", Title: "A", Instructions: "do"}
	b := state.PlanStep{StepID: "a", Title: "B", Instructions: "do"}
	if !stepDiffers(a, b) {
		t.Error("steps with different titles should differ")
	}
}

func TestStepDiffers_DependencyChange(t *testing.T) {
	a := state.PlanStep{StepID: "a", DependsOn: []string{"x", "y"}}
	b := state.PlanStep{StepID: "a", DependsOn: []string{"x", "z"}}
	if !stepDiffers(a, b) {
		t.Error("steps with different dependencies should differ")
	}
}

func TestStepDiffers_AgentChange(t *testing.T) {
	a := state.PlanStep{StepID: "a", Agent: "worker-1"}
	b := state.PlanStep{StepID: "a", Agent: "worker-2"}
	if !stepDiffers(a, b) {
		t.Error("steps with different agents should differ")
	}
}

// --- carryForwardCompleted tests ---

func TestCarryForwardCompleted_PreservesCompletedSteps(t *testing.T) {
	old := state.PlanSpec{
		Steps: []state.PlanStep{
			{StepID: "a", Title: "A", Status: state.PlanStepStatusCompleted, TaskID: "task:a"},
			{StepID: "b", Title: "B", Status: state.PlanStepStatusFailed},
			{StepID: "c", Title: "C", Status: state.PlanStepStatusSkipped},
		},
	}
	newPlan := state.PlanSpec{
		Steps: []state.PlanStep{
			{StepID: "a", Title: "A revised", Status: state.PlanStepStatusPending}, // should be replaced
			{StepID: "d", Title: "D", Status: state.PlanStepStatusPending},         // new step
		},
	}

	result := carryForwardCompleted(old, newPlan)

	// "a" should be the completed version, not the revised one.
	var foundA, foundC, foundD bool
	for _, s := range result.Steps {
		switch s.StepID {
		case "a":
			foundA = true
			if s.Status != state.PlanStepStatusCompleted {
				t.Errorf("step a status = %q, want completed", s.Status)
			}
			if s.TaskID != "task:a" {
				t.Errorf("step a TaskID = %q, want task:a", s.TaskID)
			}
		case "c":
			foundC = true
			if s.Status != state.PlanStepStatusSkipped {
				t.Errorf("step c status = %q, want skipped", s.Status)
			}
		case "d":
			foundD = true
		}
	}
	if !foundA {
		t.Error("completed step a should be in result")
	}
	if !foundC {
		t.Error("skipped step c should be appended to result")
	}
	if !foundD {
		t.Error("new step d should remain in result")
	}
}

func TestCarryForwardCompleted_NoCompletedSteps(t *testing.T) {
	old := state.PlanSpec{
		Steps: []state.PlanStep{
			{StepID: "a", Status: state.PlanStepStatusFailed},
		},
	}
	newPlan := state.PlanSpec{
		Steps: []state.PlanStep{
			{StepID: "b", Status: state.PlanStepStatusPending},
		},
	}
	result := carryForwardCompleted(old, newPlan)
	if len(result.Steps) != 1 || result.Steps[0].StepID != "b" {
		t.Errorf("with no completed steps, result should be unchanged new plan, got %d steps", len(result.Steps))
	}
}

// --- buildReplanContext tests ---

func TestBuildReplanContext_IncludesRequiredInfo(t *testing.T) {
	plan := state.PlanSpec{
		PlanID:   "plan-ctx",
		Revision: 2,
		Status:   state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "s1", Title: "Step 1", Status: state.PlanStepStatusCompleted},
			{StepID: "s2", Title: "Step 2", Status: state.PlanStepStatusFailed},
		},
		Assumptions: []string{"the server is reachable"},
	}

	ctx := buildReplanContext(plan, ReplanRequest{
		Trigger:  ReplanTriggerStepFailed,
		Reason:   "deployment failed",
		Evidence: map[string]string{"s2": "connection timeout"},
		Context:  "retry with backup server",
	})

	for _, want := range []string{
		"REPLAN",
		"step_failed",
		"deployment failed",
		"plan-ctx",
		"revision 2",
		"connection timeout",
		"the server is reachable",
		"retry with backup server",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("replan context missing %q", want)
		}
	}
}

func TestBuildReplanContext_MinimalRequest(t *testing.T) {
	plan := state.PlanSpec{PlanID: "p", Status: state.PlanStatusFailed}
	ctx := buildReplanContext(plan, ReplanRequest{Trigger: ReplanTriggerPlanFailed})
	if !strings.Contains(ctx, "REPLAN") {
		t.Error("minimal context should still contain REPLAN marker")
	}
	if !strings.Contains(ctx, "plan_failed") {
		t.Error("minimal context should contain trigger")
	}
}

// --- PlanRevision JSON round-trip ---

func TestPlanRevision_JSONRoundTrip(t *testing.T) {
	rev := PlanRevision{
		PlanID:       "plan-1",
		FromVersion:  1,
		ToVersion:    2,
		Trigger:      ReplanTriggerStepFailed,
		Reason:       "step failed",
		Actor:        "system",
		CreatedAt:    5000,
		StepsAdded:   []string{"d"},
		StepsRemoved: []string{"c"},
		StepsChanged: []string{"b"},
		Evidence:     map[string]string{"b": "error"},
	}
	blob, err := json.Marshal(rev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded PlanRevision
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.PlanID != rev.PlanID || decoded.FromVersion != 1 || decoded.ToVersion != 2 {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
	if decoded.Trigger != ReplanTriggerStepFailed {
		t.Errorf("trigger = %q", decoded.Trigger)
	}
	if len(decoded.StepsAdded) != 1 || decoded.StepsAdded[0] != "d" {
		t.Errorf("StepsAdded = %v", decoded.StepsAdded)
	}
}
