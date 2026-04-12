package state

import (
	"encoding/json"
	"testing"
)

// --- TaskBudget.IsZero ---

func TestTaskBudget_IsZero_Empty(t *testing.T) {
	if !(TaskBudget{}).IsZero() {
		t.Error("zero-value budget should be zero")
	}
}

func TestTaskBudget_IsZero_WithLimit(t *testing.T) {
	b := TaskBudget{MaxToolCalls: 10}
	if b.IsZero() {
		t.Error("budget with a limit should not be zero")
	}
}

// --- TaskBudget.Validate ---

func TestTaskBudget_Validate_Valid(t *testing.T) {
	b := TaskBudget{
		MaxPromptTokens:     32000,
		MaxCompletionTokens: 8000,
		MaxTotalTokens:      40000,
		MaxRuntimeMS:        120000,
		MaxToolCalls:        24,
		MaxDelegations:      4,
		MaxCostMicrosUSD:    250000,
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("valid budget: %v", err)
	}
}

func TestTaskBudget_Validate_Zero(t *testing.T) {
	if err := (TaskBudget{}).Validate(); err != nil {
		t.Fatalf("zero budget should be valid: %v", err)
	}
}

func TestTaskBudget_Validate_NegativePromptTokens(t *testing.T) {
	b := TaskBudget{MaxPromptTokens: -1}
	if err := b.Validate(); err == nil {
		t.Fatal("negative prompt tokens should fail")
	}
}

func TestTaskBudget_Validate_NegativeCompletionTokens(t *testing.T) {
	b := TaskBudget{MaxCompletionTokens: -1}
	if err := b.Validate(); err == nil {
		t.Fatal("negative completion tokens should fail")
	}
}

func TestTaskBudget_Validate_NegativeTotalTokens(t *testing.T) {
	b := TaskBudget{MaxTotalTokens: -1}
	if err := b.Validate(); err == nil {
		t.Fatal("negative total tokens should fail")
	}
}

func TestTaskBudget_Validate_NegativeRuntime(t *testing.T) {
	b := TaskBudget{MaxRuntimeMS: -1}
	if err := b.Validate(); err == nil {
		t.Fatal("negative runtime should fail")
	}
}

func TestTaskBudget_Validate_NegativeToolCalls(t *testing.T) {
	b := TaskBudget{MaxToolCalls: -1}
	if err := b.Validate(); err == nil {
		t.Fatal("negative tool calls should fail")
	}
}

func TestTaskBudget_Validate_NegativeDelegations(t *testing.T) {
	b := TaskBudget{MaxDelegations: -1}
	if err := b.Validate(); err == nil {
		t.Fatal("negative delegations should fail")
	}
}

func TestTaskBudget_Validate_NegativeCost(t *testing.T) {
	b := TaskBudget{MaxCostMicrosUSD: -1}
	if err := b.Validate(); err == nil {
		t.Fatal("negative cost should fail")
	}
}

func TestTaskBudget_Validate_InconsistentTokenLimits(t *testing.T) {
	b := TaskBudget{
		MaxPromptTokens:     20000,
		MaxCompletionTokens: 20000,
		MaxTotalTokens:      30000, // < 20000+20000
	}
	if err := b.Validate(); err == nil {
		t.Fatal("total < prompt + completion should fail")
	}
}

func TestTaskBudget_Validate_ConsistentTokenLimits(t *testing.T) {
	b := TaskBudget{
		MaxPromptTokens:     20000,
		MaxCompletionTokens: 20000,
		MaxTotalTokens:      40000,
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("consistent limits should pass: %v", err)
	}
}

func TestTaskBudget_Validate_PartialTokenLimitsOK(t *testing.T) {
	// Only total set — no consistency check needed.
	b := TaskBudget{MaxTotalTokens: 50000}
	if err := b.Validate(); err != nil {
		t.Fatalf("partial limits: %v", err)
	}
}

// --- TaskBudget.Narrow ---

func TestTaskBudget_Narrow_ParentOnly(t *testing.T) {
	parent := TaskBudget{MaxToolCalls: 10, MaxTotalTokens: 50000}
	child := TaskBudget{} // unlimited
	result := parent.Narrow(child)
	if result.MaxToolCalls != 10 || result.MaxTotalTokens != 50000 {
		t.Errorf("parent limits should persist when child is unlimited: %+v", result)
	}
}

func TestTaskBudget_Narrow_ChildOnly(t *testing.T) {
	parent := TaskBudget{} // unlimited
	child := TaskBudget{MaxToolCalls: 5}
	result := parent.Narrow(child)
	if result.MaxToolCalls != 5 {
		t.Errorf("child limit should be used when parent is unlimited: %+v", result)
	}
}

func TestTaskBudget_Narrow_ChildStricter(t *testing.T) {
	parent := TaskBudget{MaxToolCalls: 10, MaxCostMicrosUSD: 100000}
	child := TaskBudget{MaxToolCalls: 5, MaxCostMicrosUSD: 200000}
	result := parent.Narrow(child)
	if result.MaxToolCalls != 5 {
		t.Errorf("stricter child tool calls should win: %d", result.MaxToolCalls)
	}
	if result.MaxCostMicrosUSD != 100000 {
		t.Errorf("stricter parent cost should win: %d", result.MaxCostMicrosUSD)
	}
}

func TestTaskBudget_Narrow_BothUnlimited(t *testing.T) {
	result := TaskBudget{}.Narrow(TaskBudget{})
	if !result.IsZero() {
		t.Errorf("both unlimited should remain unlimited: %+v", result)
	}
}

func TestTaskBudget_Narrow_AllDimensions(t *testing.T) {
	parent := TaskBudget{
		MaxPromptTokens: 100, MaxCompletionTokens: 200, MaxTotalTokens: 300,
		MaxRuntimeMS: 1000, MaxToolCalls: 10, MaxDelegations: 3, MaxCostMicrosUSD: 500,
	}
	child := TaskBudget{
		MaxPromptTokens: 50, MaxCompletionTokens: 300, MaxTotalTokens: 250,
		MaxRuntimeMS: 2000, MaxToolCalls: 5, MaxDelegations: 1, MaxCostMicrosUSD: 600,
	}
	result := parent.Narrow(child)
	expected := TaskBudget{
		MaxPromptTokens: 50, MaxCompletionTokens: 200, MaxTotalTokens: 250,
		MaxRuntimeMS: 1000, MaxToolCalls: 5, MaxDelegations: 1, MaxCostMicrosUSD: 500,
	}
	if result != expected {
		t.Errorf("Narrow mismatch:\n  got:  %+v\n  want: %+v", result, expected)
	}
}

// --- TaskBudget.CheckUsage ---

func TestTaskBudget_CheckUsage_WithinBudget(t *testing.T) {
	b := TaskBudget{MaxToolCalls: 10, MaxTotalTokens: 50000}
	u := TaskUsage{ToolCalls: 5, TotalTokens: 30000}
	exceeded := b.CheckUsage(u)
	if exceeded.Any() {
		t.Errorf("within budget should not exceed: %v", exceeded.Reasons())
	}
}

func TestTaskBudget_CheckUsage_Exceeded(t *testing.T) {
	b := TaskBudget{MaxToolCalls: 10, MaxTotalTokens: 50000}
	u := TaskUsage{ToolCalls: 15, TotalTokens: 30000}
	exceeded := b.CheckUsage(u)
	if !exceeded.Any() {
		t.Fatal("over-budget should be detected")
	}
	if !exceeded.ToolCalls {
		t.Error("tool calls should be exceeded")
	}
	if exceeded.TotalTokens {
		t.Error("total tokens should not be exceeded")
	}
}

func TestTaskBudget_CheckUsage_AllDimensions(t *testing.T) {
	b := TaskBudget{
		MaxPromptTokens: 100, MaxCompletionTokens: 100, MaxTotalTokens: 200,
		MaxRuntimeMS: 1000, MaxToolCalls: 5, MaxDelegations: 2, MaxCostMicrosUSD: 500,
	}
	u := TaskUsage{
		PromptTokens: 200, CompletionTokens: 200, TotalTokens: 400,
		WallClockMS: 2000, ToolCalls: 10, Delegations: 5, CostMicrosUSD: 1000,
	}
	exceeded := b.CheckUsage(u)
	if !exceeded.PromptTokens || !exceeded.CompletionTokens || !exceeded.TotalTokens ||
		!exceeded.RuntimeMS || !exceeded.ToolCalls || !exceeded.Delegations || !exceeded.CostMicrosUSD {
		t.Errorf("all dimensions should be exceeded: %+v", exceeded)
	}
	reasons := exceeded.Reasons()
	if len(reasons) != 7 {
		t.Errorf("expected 7 reasons, got %d: %v", len(reasons), reasons)
	}
}

func TestTaskBudget_CheckUsage_UnlimitedIgnored(t *testing.T) {
	b := TaskBudget{} // all unlimited
	u := TaskUsage{PromptTokens: 999999, ToolCalls: 999999}
	exceeded := b.CheckUsage(u)
	if exceeded.Any() {
		t.Error("unlimited budget should never be exceeded")
	}
}

// --- TaskBudget.Remaining ---

func TestTaskBudget_Remaining_Basic(t *testing.T) {
	b := TaskBudget{MaxToolCalls: 10, MaxTotalTokens: 50000}
	u := TaskUsage{ToolCalls: 3, TotalTokens: 20000}
	rem := b.Remaining(u)
	if rem.MaxToolCalls != 7 {
		t.Errorf("remaining tool calls = %d, want 7", rem.MaxToolCalls)
	}
	if rem.MaxTotalTokens != 30000 {
		t.Errorf("remaining tokens = %d, want 30000", rem.MaxTotalTokens)
	}
}

func TestTaskBudget_Remaining_Overdrawn(t *testing.T) {
	b := TaskBudget{MaxToolCalls: 5}
	u := TaskUsage{ToolCalls: 8}
	rem := b.Remaining(u)
	if rem.MaxToolCalls != 0 {
		t.Errorf("overdrawn should clamp to 0, got %d", rem.MaxToolCalls)
	}
}

func TestTaskBudget_Remaining_Unlimited(t *testing.T) {
	b := TaskBudget{} // unlimited
	u := TaskUsage{ToolCalls: 100}
	rem := b.Remaining(u)
	if rem.MaxToolCalls != 0 {
		t.Errorf("unlimited remaining should stay 0 (unlimited), got %d", rem.MaxToolCalls)
	}
}

// --- TaskUsage.Add ---

func TestTaskUsage_Add(t *testing.T) {
	a := TaskUsage{PromptTokens: 100, ToolCalls: 3, CostMicrosUSD: 500}
	b := TaskUsage{PromptTokens: 200, ToolCalls: 2, CostMicrosUSD: 300}
	a.Add(b)
	if a.PromptTokens != 300 {
		t.Errorf("PromptTokens = %d, want 300", a.PromptTokens)
	}
	if a.ToolCalls != 5 {
		t.Errorf("ToolCalls = %d, want 5", a.ToolCalls)
	}
	if a.CostMicrosUSD != 800 {
		t.Errorf("CostMicrosUSD = %d, want 800", a.CostMicrosUSD)
	}
}

// --- BudgetExceeded ---

func TestBudgetExceeded_Any_None(t *testing.T) {
	if (BudgetExceeded{}).Any() {
		t.Error("empty exceeded should return false")
	}
}

func TestBudgetExceeded_Reasons(t *testing.T) {
	e := BudgetExceeded{ToolCalls: true, CostMicrosUSD: true}
	reasons := e.Reasons()
	if len(reasons) != 2 {
		t.Fatalf("reasons = %d, want 2", len(reasons))
	}
}

// --- Budget inheritance scenario ---

func TestBudget_InheritanceScenario(t *testing.T) {
	// Goal sets a wide budget.
	goalBudget := TaskBudget{
		MaxTotalTokens:  100000,
		MaxToolCalls:    50,
		MaxDelegations:  5,
		MaxCostMicrosUSD: 1000000,
	}

	// Task narrows the budget.
	taskBudget := TaskBudget{
		MaxTotalTokens: 40000,
		MaxToolCalls:   20,
	}
	effective := goalBudget.Narrow(taskBudget)
	if effective.MaxTotalTokens != 40000 {
		t.Errorf("tokens = %d, want 40000 (task narrowed)", effective.MaxTotalTokens)
	}
	if effective.MaxToolCalls != 20 {
		t.Errorf("tool calls = %d, want 20 (task narrowed)", effective.MaxToolCalls)
	}
	if effective.MaxDelegations != 5 {
		t.Errorf("delegations = %d, want 5 (inherited from goal)", effective.MaxDelegations)
	}
	if effective.MaxCostMicrosUSD != 1000000 {
		t.Errorf("cost = %d, want 1000000 (inherited from goal)", effective.MaxCostMicrosUSD)
	}

	// Sub-task tries to widen — should be capped by parent.
	subtaskBudget := TaskBudget{
		MaxTotalTokens: 80000, // wider than effective
		MaxToolCalls:   10,    // narrower
	}
	subEffective := effective.Narrow(subtaskBudget)
	if subEffective.MaxTotalTokens != 40000 {
		t.Errorf("sub tokens = %d, want 40000 (parent cap)", subEffective.MaxTotalTokens)
	}
	if subEffective.MaxToolCalls != 10 {
		t.Errorf("sub tool calls = %d, want 10 (child narrowed)", subEffective.MaxToolCalls)
	}

	// Check usage against effective budget.
	usage := TaskUsage{TotalTokens: 35000, ToolCalls: 25}
	exceeded := effective.CheckUsage(usage)
	if !exceeded.ToolCalls {
		t.Error("25 tool calls > 20 limit should exceed")
	}
	if exceeded.TotalTokens {
		t.Error("35000 tokens < 40000 limit should not exceed")
	}

	// Remaining after partial usage.
	partialUsage := TaskUsage{TotalTokens: 10000, ToolCalls: 5}
	remaining := effective.Remaining(partialUsage)
	if remaining.MaxTotalTokens != 30000 {
		t.Errorf("remaining tokens = %d, want 30000", remaining.MaxTotalTokens)
	}
	if remaining.MaxToolCalls != 15 {
		t.Errorf("remaining tool calls = %d, want 15", remaining.MaxToolCalls)
	}
}

// --- JSON round-trip ---

func TestTaskBudget_JSONRoundTrip(t *testing.T) {
	b := TaskBudget{
		MaxPromptTokens: 32000, MaxCompletionTokens: 8000, MaxTotalTokens: 40000,
		MaxRuntimeMS: 120000, MaxToolCalls: 24, MaxDelegations: 4, MaxCostMicrosUSD: 250000,
	}
	blob, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded TaskBudget
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != b {
		t.Errorf("round-trip mismatch:\n  got:  %+v\n  want: %+v", decoded, b)
	}
}

func TestTaskUsage_JSONRoundTrip(t *testing.T) {
	u := TaskUsage{
		PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
		WallClockMS: 5000, ToolCalls: 3, Delegations: 1, CostMicrosUSD: 1200,
	}
	blob, _ := json.Marshal(u)
	var decoded TaskUsage
	json.Unmarshal(blob, &decoded)
	if decoded != u {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
}
