package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

// fakeProvider returns canned text from Generate.
type fakeProvider struct {
	text string
	err  error
}

func (f *fakeProvider) Generate(_ context.Context, _ agent.Turn) (agent.ProviderResult, error) {
	if f.err != nil {
		return agent.ProviderResult{}, f.err
	}
	return agent.ProviderResult{Text: f.text}, nil
}

func validGoal() state.GoalSpec {
	return state.GoalSpec{
		GoalID:       "goal-1",
		Title:        "Deploy the widget service",
		Instructions: "Build, test, and deploy the widget service to production.",
		Constraints:  []string{"Must pass CI"},
		SuccessCriteria: []string{"Service is healthy on production"},
		Priority:     state.TaskPriorityHigh,
	}
}

func validPlanJSON() string {
	return `{
  "plan_id": "plan-deploy",
  "title": "Deploy widget service",
  "steps": [
    {"step_id": "build", "title": "Build the service", "instructions": "Run go build", "status": "pending"},
    {"step_id": "test", "title": "Run tests", "instructions": "Run go test", "depends_on": ["build"], "status": "pending"},
    {"step_id": "deploy", "title": "Deploy to prod", "instructions": "Push container", "depends_on": ["test"], "status": "pending"}
  ],
  "assumptions": ["CI is green"],
  "risks": ["Deployment rollback needed"],
  "rollback_strategy": "Revert container image"
}`
}

func TestGeneratePlan_ValidOutput(t *testing.T) {
	p := New(&fakeProvider{text: validPlanJSON()}, WithModel("test-model"))
	plan, err := p.GeneratePlan(context.Background(), PlanRequest{Goal: validGoal()})
	if err != nil {
		t.Fatalf("GeneratePlan: %v", err)
	}
	if plan.PlanID != "plan-deploy" {
		t.Errorf("plan_id = %q, want plan-deploy", plan.PlanID)
	}
	if plan.GoalID != "goal-1" {
		t.Errorf("goal_id = %q, want goal-1", plan.GoalID)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[1].DependsOn[0] != "build" {
		t.Errorf("step test depends_on = %v, want [build]", plan.Steps[1].DependsOn)
	}
	if plan.Meta["planner_model"] != "test-model" {
		t.Errorf("planner_model = %v", plan.Meta["planner_model"])
	}
	if plan.Status != state.PlanStatusDraft {
		t.Errorf("status = %q, want draft", plan.Status)
	}
	if len(plan.Assumptions) != 1 || plan.Assumptions[0] != "CI is green" {
		t.Errorf("assumptions = %v", plan.Assumptions)
	}
}

func TestGeneratePlan_MarkdownFenced(t *testing.T) {
	fenced := "Here's the plan:\n```json\n" + validPlanJSON() + "\n```\nDone."
	p := New(&fakeProvider{text: fenced})
	plan, err := p.GeneratePlan(context.Background(), PlanRequest{Goal: validGoal()})
	if err != nil {
		t.Fatalf("GeneratePlan with fenced output: %v", err)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps from fenced output, got %d", len(plan.Steps))
	}
}

func TestGeneratePlan_MalformedOutputReturnsFallback(t *testing.T) {
	p := New(&fakeProvider{text: "I don't know how to plan this."})
	plan, err := p.GeneratePlan(context.Background(), PlanRequest{Goal: validGoal()})
	if err != nil {
		t.Fatalf("GeneratePlan fallback: %v", err)
	}
	if plan.Meta["fallback"] != true {
		t.Fatalf("expected fallback plan, got meta: %v", plan.Meta)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("expected 1 fallback step, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Status != state.PlanStepStatusBlocked {
		t.Errorf("fallback step status = %q, want blocked", plan.Steps[0].Status)
	}
}

func TestGeneratePlan_ProviderError(t *testing.T) {
	p := New(&fakeProvider{err: fmt.Errorf("rate limited")})
	_, err := p.GeneratePlan(context.Background(), PlanRequest{Goal: validGoal()})
	if err == nil {
		t.Fatal("expected error from provider")
	}
	if !strings.Contains(err.Error(), "provider error") {
		t.Errorf("error = %q, want provider error", err)
	}
}

func TestGeneratePlan_InvalidGoal(t *testing.T) {
	p := New(&fakeProvider{text: "{}"})
	_, err := p.GeneratePlan(context.Background(), PlanRequest{
		Goal: state.GoalSpec{}, // missing ID and title
	})
	if err == nil {
		t.Fatal("expected error for invalid goal")
	}
	if !strings.Contains(err.Error(), "invalid goal") {
		t.Errorf("error = %q, want invalid goal", err)
	}
}

func TestGeneratePlan_NilProvider(t *testing.T) {
	p := New(nil)
	_, err := p.GeneratePlan(context.Background(), PlanRequest{Goal: validGoal()})
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestGeneratePlan_CyclicPlanRejected(t *testing.T) {
	cyclic := `{
  "plan_id": "plan-cycle",
  "title": "Cyclic plan",
  "steps": [
    {"step_id": "a", "title": "A", "instructions": "Do A", "depends_on": ["b"], "status": "pending"},
    {"step_id": "b", "title": "B", "instructions": "Do B", "depends_on": ["a"], "status": "pending"}
  ]
}`
	p := New(&fakeProvider{text: cyclic})
	_, err := p.GeneratePlan(context.Background(), PlanRequest{Goal: validGoal()})
	if err == nil {
		t.Fatal("expected error for cyclic plan")
	}
	if !strings.Contains(err.Error(), "cyclic") {
		t.Errorf("error = %q, want cyclic", err)
	}
}

func TestGeneratePlan_UnderspecifiedGoalBlockedSteps(t *testing.T) {
	underspecified := `{
  "plan_id": "plan-underspec",
  "title": "Incomplete plan",
  "steps": [
    {"step_id": "clarify", "title": "Clarify requirements", "instructions": "The goal does not specify the target environment.", "status": "blocked"},
    {"step_id": "implement", "title": "Implement", "instructions": "Implement once requirements are clear.", "depends_on": ["clarify"], "status": "pending"}
  ]
}`
	p := New(&fakeProvider{text: underspecified})
	plan, err := p.GeneratePlan(context.Background(), PlanRequest{Goal: validGoal()})
	if err != nil {
		t.Fatalf("GeneratePlan underspecified: %v", err)
	}
	if plan.Steps[0].Status != state.PlanStepStatusBlocked {
		t.Errorf("first step status = %q, want blocked", plan.Steps[0].Status)
	}
}

func TestGeneratePlan_EmptyPlanIDDefaulted(t *testing.T) {
	noID := `{
  "title": "No ID plan",
  "steps": [
    {"step_id": "s1", "title": "Do it", "instructions": "Just do it", "status": "pending"}
  ]
}`
	p := New(&fakeProvider{text: noID})
	plan, err := p.GeneratePlan(context.Background(), PlanRequest{Goal: validGoal()})
	if err != nil {
		t.Fatalf("GeneratePlan no ID: %v", err)
	}
	if !strings.HasPrefix(plan.PlanID, "plan:") {
		t.Errorf("plan_id = %q, expected plan: prefix", plan.PlanID)
	}
}

func TestBuildPlannerPrompt_IncludesGoalFields(t *testing.T) {
	goal := validGoal()
	prompt := buildPlannerPrompt(goal, "Extra context here", 5)
	for _, want := range []string{"goal-1", "Deploy the widget service", "Must pass CI", "Extra context here", "Maximum steps: 5"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestParsePlanResponse_ValidJSON(t *testing.T) {
	plan, err := parsePlanResponse(validPlanJSON(), validGoal())
	if err != nil {
		t.Fatalf("parsePlanResponse: %v", err)
	}
	if plan.PlanID != "plan-deploy" {
		t.Errorf("plan_id = %q", plan.PlanID)
	}
	if len(plan.Steps) != 3 {
		t.Errorf("steps = %d", len(plan.Steps))
	}
}

func TestParsePlanResponse_InvalidJSON(t *testing.T) {
	_, err := parsePlanResponse("not json at all", validGoal())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFallbackPlan_Structure(t *testing.T) {
	goal := validGoal()
	plan := fallbackPlan(goal, "raw output text", fmt.Errorf("bad json"))
	if plan.Meta["fallback"] != true {
		t.Fatal("expected fallback=true in meta")
	}
	if len(plan.Steps) != 1 || plan.Steps[0].StepID != "review" {
		t.Fatal("expected single review step")
	}
	if plan.Steps[0].Status != state.PlanStepStatusBlocked {
		t.Errorf("fallback step status = %q", plan.Steps[0].Status)
	}
	// Verify it round-trips to JSON.
	blob, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal fallback: %v", err)
	}
	var decoded state.PlanSpec
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal fallback: %v", err)
	}
}

func TestParsePlanResponse_FenceInsideJSON(t *testing.T) {
	// A JSON response that happens to contain ``` inside a string value
	// should not be stripped.
	jsonWithFence := `{
	"plan_id": "plan-fence",
	"title": "Plan with fence in instructions",
	"steps": [
	{"step_id": "s1", "title": "Do it", "instructions": "Run ` + "```" + `go build` + "```" + `", "status": "pending"}
	]
}`
	plan, err := parsePlanResponse(jsonWithFence, validGoal())
	if err != nil {
		t.Fatalf("parsePlanResponse with internal fence: %v", err)
	}
	if plan.PlanID != "plan-fence" {
		t.Errorf("plan_id = %q, want plan-fence", plan.PlanID)
	}
}

func TestParsePlanResponse_PreambleBeforeJSON(t *testing.T) {
	// Text before the JSON but no fence — should still work if it starts with {.
	preambleJSON := `  {
	"plan_id": "plan-preamble",
	"title": "Preamble plan",
	"steps": [{"step_id": "s1", "title": "Do", "instructions": "do", "status": "pending"}]
}`
	plan, err := parsePlanResponse(preambleJSON, validGoal())
	if err != nil {
		t.Fatalf("parsePlanResponse with preamble: %v", err)
	}
	if plan.PlanID != "plan-preamble" {
		t.Errorf("plan_id = %q", plan.PlanID)
	}
}

func TestFallbackPlan_TruncatesLongOutput(t *testing.T) {
	long := strings.Repeat("x", 1000)
	plan := fallbackPlan(validGoal(), long, fmt.Errorf("err"))
	if len(plan.Steps[0].Instructions) > 600 {
		t.Errorf("expected truncated instructions, got len=%d", len(plan.Steps[0].Instructions))
	}
}
