package planner

import (
	"encoding/json"
	"strings"
	"testing"

	"metiq/internal/store/state"
)

// ── Helper builders ──────────────────────────────────────────────────────────

func parentTask() state.TaskSpec {
	return state.TaskSpec{
		Version:      1,
		TaskID:       "task-parent",
		GoalID:       "goal-1",
		PlanID:       "plan-1",
		SessionID:    "session-1",
		Title:        "Parent Task",
		Instructions: "do the parent thing",
		Status:       state.TaskStatusInProgress,
		Priority:     state.TaskPriorityHigh,
		Authority: state.TaskAuthority{
			AutonomyMode:       state.AutonomyFull,
			CanAct:             true,
			CanDelegate:        true,
			CanEscalate:        true,
			RiskClass:          state.RiskClassLow,
			MaxDelegationDepth: 3,
		},
		Budget: state.TaskBudget{
			MaxTotalTokens: 10000,
			MaxToolCalls:   50,
			MaxDelegations: 5,
			MaxRuntimeMS:   60000,
		},
		MemoryScope: state.AgentMemoryScopeProject,
		ToolProfile: "coding",
		EnabledTools: []string{"nostr_fetch", "nostr_publish"},
	}
}

func parentRun() state.TaskRun {
	return state.TaskRun{
		Version: 1,
		RunID:   "run-parent",
		TaskID:  "task-parent",
		Attempt: 1,
		Status:  state.TaskRunStatusRunning,
	}
}

func baseRequest() DelegationRequest {
	return DelegationRequest{
		ParentTask:        parentTask(),
		ParentRun:         parentRun(),
		ParentUsage:       state.TaskUsage{TotalTokens: 2000, ToolCalls: 10},
		ChildInstructions: "fetch the latest events and summarize them",
		Now:               1000,
	}
}

// ── BuildDelegatedTask tests ────────────────────────────────────────────────

func TestBuildDelegatedTask_Basic(t *testing.T) {
	result, err := BuildDelegatedTask(baseRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := result.Task
	if task.TaskID == "" {
		t.Fatal("expected non-empty task ID")
	}
	if task.ParentTaskID != "task-parent" {
		t.Fatalf("expected parent task ID, got %s", task.ParentTaskID)
	}
	if task.GoalID != "goal-1" {
		t.Fatalf("expected goal-1, got %s", task.GoalID)
	}
	if task.PlanID != "plan-1" {
		t.Fatalf("expected plan-1, got %s", task.PlanID)
	}
	if task.Instructions != "fetch the latest events and summarize them" {
		t.Fatalf("unexpected instructions: %s", task.Instructions)
	}
	if task.Status != state.TaskStatusPending {
		t.Fatalf("expected pending, got %s", task.Status)
	}
	if task.Priority != state.TaskPriorityHigh {
		t.Fatalf("expected high priority, got %s", task.Priority)
	}
}

func TestBuildDelegatedTask_InheritsAuthority(t *testing.T) {
	result, err := BuildDelegatedTask(baseRequest())
	if err != nil {
		t.Fatal(err)
	}

	auth := result.EffectiveAuthority
	if auth.AutonomyMode != state.AutonomyFull {
		t.Fatalf("expected full autonomy, got %s", auth.AutonomyMode)
	}
	if !auth.CanAct || !auth.CanDelegate {
		t.Fatal("expected CanAct and CanDelegate")
	}
}

func TestBuildDelegatedTask_NarrowsAuthority(t *testing.T) {
	req := baseRequest()
	req.ChildAuthority = &state.TaskAuthority{
		AutonomyMode:       state.AutonomyPlanApproval,
		CanAct:             true,
		CanDelegate:        false,
		MaxDelegationDepth: 1,
	}

	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatal(err)
	}

	auth := result.EffectiveAuthority
	// plan_approval is more restrictive than full → should be narrowed
	if auth.AutonomyMode != state.AutonomyPlanApproval {
		t.Fatalf("expected plan_approval, got %s", auth.AutonomyMode)
	}
	// CanDelegate: parent=true AND child=false → false
	if auth.CanDelegate {
		t.Fatal("expected CanDelegate=false after narrowing")
	}
	// MaxDelegationDepth: min(3, 1) → 1
	if auth.MaxDelegationDepth != 1 {
		t.Fatalf("expected depth 1, got %d", auth.MaxDelegationDepth)
	}
}

func TestBuildDelegatedTask_NarrowsBudgetToRemaining(t *testing.T) {
	req := baseRequest()
	// Parent has 10000 tokens, used 2000 → 8000 remaining.
	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatal(err)
	}

	budget := result.EffectiveBudget
	if budget.MaxTotalTokens != 8000 {
		t.Fatalf("expected 8000 tokens remaining, got %d", budget.MaxTotalTokens)
	}
	if budget.MaxToolCalls != 40 {
		t.Fatalf("expected 40 tool calls remaining, got %d", budget.MaxToolCalls)
	}
}

func TestBuildDelegatedTask_ChildBudgetNarrowedFurther(t *testing.T) {
	req := baseRequest()
	req.ChildBudget = &state.TaskBudget{
		MaxTotalTokens: 5000,
		MaxToolCalls:   20,
	}

	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatal(err)
	}

	budget := result.EffectiveBudget
	// min(8000 remaining, 5000 requested) → 5000
	if budget.MaxTotalTokens != 5000 {
		t.Fatalf("expected 5000 tokens, got %d", budget.MaxTotalTokens)
	}
	// min(40 remaining, 20 requested) → 20
	if budget.MaxToolCalls != 20 {
		t.Fatalf("expected 20 tools, got %d", budget.MaxToolCalls)
	}
}

func TestBuildDelegatedTask_DepthExceeded(t *testing.T) {
	req := baseRequest()
	req.DelegationDepth = 3 // max is 3

	_, err := BuildDelegatedTask(req)
	if err == nil {
		t.Fatal("expected error for depth exceeded")
	}
	if !strings.Contains(err.Error(), "depth") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildDelegatedTask_NoDelegatePermission(t *testing.T) {
	req := baseRequest()
	req.ParentTask.Authority.CanDelegate = false

	_, err := BuildDelegatedTask(req)
	if err == nil {
		t.Fatal("expected error for no delegation permission")
	}
	if !strings.Contains(err.Error(), "delegation permission") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildDelegatedTask_EmptyInstructions(t *testing.T) {
	req := baseRequest()
	req.ChildInstructions = ""

	_, err := BuildDelegatedTask(req)
	if err == nil {
		t.Fatal("expected error for empty instructions")
	}
}

func TestBuildDelegatedTask_EmptyParentID(t *testing.T) {
	req := baseRequest()
	req.ParentTask.TaskID = ""

	_, err := BuildDelegatedTask(req)
	if err == nil {
		t.Fatal("expected error for empty parent ID")
	}
}

func TestBuildDelegatedTask_InheritsLineageFields(t *testing.T) {
	result, err := BuildDelegatedTask(baseRequest())
	if err != nil {
		t.Fatal(err)
	}

	task := result.Task
	if task.MemoryScope != state.AgentMemoryScopeProject {
		t.Fatalf("expected project scope, got %s", task.MemoryScope)
	}
	if task.ToolProfile != "coding" {
		t.Fatalf("expected coding profile, got %s", task.ToolProfile)
	}
	if len(task.EnabledTools) != 2 {
		t.Fatalf("expected 2 enabled tools, got %d", len(task.EnabledTools))
	}
}

func TestBuildDelegatedTask_IncludesExpectedOutputs(t *testing.T) {
	req := baseRequest()
	req.ChildExpectedOutputs = []state.TaskOutputSpec{
		{Name: "summary", Description: "Event summary", Format: "text", Required: true},
	}

	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Task.ExpectedOutputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(result.Task.ExpectedOutputs))
	}
	if result.Task.ExpectedOutputs[0].Name != "summary" {
		t.Fatalf("expected 'summary', got %s", result.Task.ExpectedOutputs[0].Name)
	}
}

func TestBuildDelegatedTask_IncludesAcceptanceCriteria(t *testing.T) {
	req := baseRequest()
	req.ChildAcceptanceCriteria = []state.TaskAcceptanceCriterion{
		{Description: "Summary covers at least 10 events", Required: true},
	}

	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Task.AcceptanceCriteria) != 1 {
		t.Fatalf("expected 1 criterion, got %d", len(result.Task.AcceptanceCriteria))
	}
}

func TestBuildDelegatedTask_IncludesVerification(t *testing.T) {
	req := baseRequest()
	req.ChildVerification = &state.VerificationSpec{
		Policy: state.VerificationPolicyAdvisory,
	}

	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatal(err)
	}

	if result.Task.Verification.Policy != state.VerificationPolicyAdvisory {
		t.Fatalf("expected advisory policy, got %s", result.Task.Verification.Policy)
	}
}

func TestBuildDelegatedTask_CustomTitle(t *testing.T) {
	req := baseRequest()
	req.ChildTitle = "Custom Title"

	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatal(err)
	}

	if result.Task.Title != "Custom Title" {
		t.Fatalf("expected 'Custom Title', got %s", result.Task.Title)
	}
}

func TestBuildDelegatedTask_DerivesTitleFromInstructions(t *testing.T) {
	req := baseRequest()
	req.ChildInstructions = "Very long instructions that should be truncated to something reasonable for a title"

	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Task.Title) > 83 { // 80 + "..."
		t.Fatalf("title too long: %d chars", len(result.Task.Title))
	}
}

func TestBuildDelegatedTask_Meta(t *testing.T) {
	result, err := BuildDelegatedTask(baseRequest())
	if err != nil {
		t.Fatal(err)
	}

	meta := result.Task.Meta
	if meta == nil {
		t.Fatal("expected meta")
	}
	depth, ok := meta["delegation_depth"]
	if !ok {
		t.Fatal("expected delegation_depth in meta")
	}
	if depth != 1 {
		t.Fatalf("expected depth 1, got %v", depth)
	}
	parentRun, ok := meta["parent_run_id"]
	if !ok {
		t.Fatal("expected parent_run_id in meta")
	}
	if parentRun != "run-parent" {
		t.Fatalf("expected run-parent, got %v", parentRun)
	}
}

func TestBuildDelegatedTask_AssignedAgent(t *testing.T) {
	req := baseRequest()
	req.AssignedAgent = "worker-agent-1"

	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatal(err)
	}

	if result.Task.AssignedAgent != "worker-agent-1" {
		t.Fatalf("expected worker-agent-1, got %s", result.Task.AssignedAgent)
	}
}

func TestBuildDelegatedTask_BudgetWarnings(t *testing.T) {
	req := baseRequest()
	// Nearly exhausted: parent has 10000 tokens, used 9950
	req.ParentUsage = state.TaskUsage{TotalTokens: 9950, ToolCalls: 49}

	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Warnings) == 0 {
		t.Fatal("expected budget warnings")
	}
	foundTokenWarn := false
	foundToolWarn := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "tokens") {
			foundTokenWarn = true
		}
		if strings.Contains(w, "tool calls") {
			foundToolWarn = true
		}
	}
	if !foundTokenWarn {
		t.Fatal("expected token warning")
	}
	if !foundToolWarn {
		t.Fatal("expected tool call warning")
	}
}

func TestBuildDelegatedTask_AuthorityTrace(t *testing.T) {
	req := baseRequest()
	req.ChildAuthority = &state.TaskAuthority{
		AutonomyMode: state.AutonomyPlanApproval,
		CanAct:       true,
	}

	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.AuthorityTrace.Layers) == 0 {
		t.Fatal("expected authority trace layers")
	}
}

// ── ValidateDelegation ──────────────────────────────────────────────────────

func TestValidateDelegation_Valid(t *testing.T) {
	err := ValidateDelegation(baseRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDelegation_NoPermission(t *testing.T) {
	req := baseRequest()
	req.ParentTask.Authority.CanDelegate = false
	if err := ValidateDelegation(req); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateDelegation_DepthExceeded(t *testing.T) {
	req := baseRequest()
	req.DelegationDepth = 5
	if err := ValidateDelegation(req); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateDelegation_EmptyInstructions(t *testing.T) {
	req := baseRequest()
	req.ChildInstructions = ""
	if err := ValidateDelegation(req); err == nil {
		t.Fatal("expected error")
	}
}

// ── FormatDelegationResult ──────────────────────────────────────────────────

func TestFormatDelegationResult_Basic(t *testing.T) {
	result, _ := BuildDelegatedTask(baseRequest())
	out := FormatDelegationResult(result)

	if !strings.Contains(out, "Delegated Task") {
		t.Fatal("expected header")
	}
	if !strings.Contains(out, "Parent: task-parent") {
		t.Fatal("expected parent ID")
	}
	if !strings.Contains(out, "mode=full") {
		t.Fatal("expected autonomy mode")
	}
}

func TestFormatDelegationResult_WithWarnings(t *testing.T) {
	req := baseRequest()
	req.ParentUsage = state.TaskUsage{TotalTokens: 9990}
	result, _ := BuildDelegatedTask(req)
	out := FormatDelegationResult(result)

	if !strings.Contains(out, "⚠️") {
		t.Fatal("expected warning markers")
	}
}

// ── deriveDelegationTitle ───────────────────────────────────────────────────

func TestDeriveDelegationTitle_Empty(t *testing.T) {
	if got := deriveDelegationTitle(""); got != "delegated task" {
		t.Fatalf("expected 'delegated task', got %s", got)
	}
}

func TestDeriveDelegationTitle_Multiline(t *testing.T) {
	got := deriveDelegationTitle("first line\nsecond line")
	if got != "first line" {
		t.Fatalf("expected 'first line', got %s", got)
	}
}

func TestDeriveDelegationTitle_Long(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := deriveDelegationTitle(long)
	if len(got) > 80 {
		t.Fatalf("expected truncated to 80, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatal("expected ... suffix")
	}
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestDelegationRequest_JSONRoundTrip(t *testing.T) {
	req := baseRequest()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DelegationRequest
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ChildInstructions != req.ChildInstructions {
		t.Fatal("round-trip mismatch")
	}
}

func TestDelegationResult_JSONRoundTrip(t *testing.T) {
	result, _ := BuildDelegatedTask(baseRequest())
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DelegationResult
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Task.ParentTaskID != "task-parent" {
		t.Fatal("round-trip mismatch")
	}
}

// ── End-to-end: parent → child with full narrowing ──────────────────────────

func TestEndToEnd_DelegationWithNarrowing(t *testing.T) {
	req := DelegationRequest{
		ParentTask: parentTask(),
		ParentRun:  parentRun(),
		ParentUsage: state.TaskUsage{
			TotalTokens: 3000,
			ToolCalls:   15,
			Delegations: 1,
		},
		ChildInstructions: "Analyze the timeline and produce a summary",
		ChildAuthority: &state.TaskAuthority{
			AutonomyMode:       state.AutonomyPlanApproval,
			CanAct:             true,
			CanDelegate:        true,
			MaxDelegationDepth: 2,
		},
		ChildBudget: &state.TaskBudget{
			MaxTotalTokens: 5000,
			MaxToolCalls:   20,
		},
		ChildExpectedOutputs: []state.TaskOutputSpec{
			{Name: "summary", Description: "Timeline analysis", Required: true},
		},
		ChildAcceptanceCriteria: []state.TaskAcceptanceCriterion{
			{Description: "Covers at least 24h of activity", Required: true},
		},
		DelegationDepth: 1,
		Now:             2000,
	}

	result, err := BuildDelegatedTask(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := result.Task

	// Authority narrowed
	if task.Authority.AutonomyMode != state.AutonomyPlanApproval {
		t.Fatalf("expected plan_approval, got %s", task.Authority.AutonomyMode)
	}
	if task.Authority.MaxDelegationDepth != 2 {
		t.Fatalf("expected depth 2, got %d", task.Authority.MaxDelegationDepth)
	}

	// Budget narrowed to min(remaining, requested)
	// remaining: 10000-3000=7000 tokens, 50-15=35 tools
	// requested: 5000, 20
	// effective: 5000, 20
	if task.Budget.MaxTotalTokens != 5000 {
		t.Fatalf("expected 5000 tokens, got %d", task.Budget.MaxTotalTokens)
	}
	if task.Budget.MaxToolCalls != 20 {
		t.Fatalf("expected 20 tools, got %d", task.Budget.MaxToolCalls)
	}

	// Outputs and criteria
	if len(task.ExpectedOutputs) != 1 || task.ExpectedOutputs[0].Name != "summary" {
		t.Fatal("expected output mismatch")
	}
	if len(task.AcceptanceCriteria) != 1 {
		t.Fatal("expected criteria mismatch")
	}

	// Lineage
	if task.ParentTaskID != "task-parent" {
		t.Fatalf("expected parent link, got %s", task.ParentTaskID)
	}
	if task.GoalID != "goal-1" {
		t.Fatalf("expected goal link, got %s", task.GoalID)
	}
	if task.Meta["delegation_depth"] != 2 {
		t.Fatalf("expected depth 2 in meta, got %v", task.Meta["delegation_depth"])
	}
}
