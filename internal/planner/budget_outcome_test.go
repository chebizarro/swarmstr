package planner

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"metiq/internal/store/state"
)

// ── DefaultExhaustionPolicy tests ──────────────────────────────────────────────

func TestDefaultPolicy_CostAlwaysEscalates(t *testing.T) {
	policy := DefaultExhaustionPolicy()
	modes := []state.AutonomyMode{
		state.AutonomyFull, state.AutonomyPlanApproval,
		state.AutonomyStepApproval, state.AutonomySupervised,
	}
	for _, mode := range modes {
		action, ok := policy.Lookup(ExhaustionCost, mode)
		if !ok {
			t.Errorf("mode=%s: missing cost rule", mode)
			continue
		}
		if action != ActionEscalate {
			t.Errorf("mode=%s: cost should always escalate, got %s", mode, action)
		}
	}
}

func TestDefaultPolicy_SupervisedAlwaysEscalates(t *testing.T) {
	policy := DefaultExhaustionPolicy()
	reasons := []ExhaustionReason{
		ExhaustionTokens, ExhaustionRuntime, ExhaustionToolCalls, ExhaustionDelegation,
	}
	for _, reason := range reasons {
		action, ok := policy.Lookup(reason, state.AutonomySupervised)
		if !ok {
			t.Errorf("reason=%s: missing supervised rule", reason)
			continue
		}
		if action != ActionEscalate {
			t.Errorf("reason=%s: supervised should escalate, got %s", reason, action)
		}
	}
}

func TestDefaultPolicy_FullMode_TokensFallback(t *testing.T) {
	policy := DefaultExhaustionPolicy()
	action, _ := policy.Lookup(ExhaustionTokens, state.AutonomyFull)
	if action != ActionFallback {
		t.Errorf("full/tokens: expected fallback, got %s", action)
	}
}

func TestDefaultPolicy_FullMode_RuntimeFails(t *testing.T) {
	policy := DefaultExhaustionPolicy()
	action, _ := policy.Lookup(ExhaustionRuntime, state.AutonomyFull)
	if action != ActionFail {
		t.Errorf("full/runtime: expected fail, got %s", action)
	}
}

func TestDefaultPolicy_FullMode_ToolCallsReplan(t *testing.T) {
	policy := DefaultExhaustionPolicy()
	action, _ := policy.Lookup(ExhaustionToolCalls, state.AutonomyFull)
	if action != ActionReplan {
		t.Errorf("full/tool_calls: expected replan, got %s", action)
	}
}

func TestDefaultPolicy_FullMode_DelegationReplan(t *testing.T) {
	policy := DefaultExhaustionPolicy()
	action, _ := policy.Lookup(ExhaustionDelegation, state.AutonomyFull)
	if action != ActionReplan {
		t.Errorf("full/delegation: expected replan, got %s", action)
	}
}

func TestDefaultPolicy_PlanApproval_TokensEscalate(t *testing.T) {
	policy := DefaultExhaustionPolicy()
	action, _ := policy.Lookup(ExhaustionTokens, state.AutonomyPlanApproval)
	if action != ActionEscalate {
		t.Errorf("plan_approval/tokens: expected escalate, got %s", action)
	}
}

func TestDefaultPolicy_PlanApproval_ToolCallsReplan(t *testing.T) {
	policy := DefaultExhaustionPolicy()
	action, _ := policy.Lookup(ExhaustionToolCalls, state.AutonomyPlanApproval)
	if action != ActionReplan {
		t.Errorf("plan_approval/tool_calls: expected replan, got %s", action)
	}
}

func TestDefaultPolicy_StepApproval_DelegationReplan(t *testing.T) {
	policy := DefaultExhaustionPolicy()
	action, _ := policy.Lookup(ExhaustionDelegation, state.AutonomyStepApproval)
	if action != ActionReplan {
		t.Errorf("step_approval/delegation: expected replan, got %s", action)
	}
}

func TestDefaultPolicy_StepApproval_TokensEscalate(t *testing.T) {
	policy := DefaultExhaustionPolicy()
	action, _ := policy.Lookup(ExhaustionTokens, state.AutonomyStepApproval)
	if action != ActionEscalate {
		t.Errorf("step_approval/tokens: expected escalate, got %s", action)
	}
}

// ── ResolveOutcome tests ───────────────────────────────────────────────────────

func TestResolveOutcome_NotExhausted(t *testing.T) {
	r := NewOutcomeResolver(nil)
	dec := BudgetDecision{Verdict: BudgetAllow, Reason: "ok"}
	event := r.ResolveOutcome(dec, "t1", "r1", state.AutonomyFull, 1000)
	if event != nil {
		t.Error("non-block decision should return nil event")
	}
}

func TestResolveOutcome_WarnNotExhausted(t *testing.T) {
	r := NewOutcomeResolver(nil)
	dec := BudgetDecision{Verdict: BudgetWarn, Reason: "warning"}
	event := r.ResolveOutcome(dec, "t1", "r1", state.AutonomyFull, 1000)
	if event != nil {
		t.Error("warn decision should return nil event")
	}
}

func TestResolveOutcome_TokenExhaustion_FullMode(t *testing.T) {
	r := NewOutcomeResolver(nil)
	dec := BudgetDecision{
		Verdict:            BudgetBlock,
		Reason:             "budget exhausted",
		ExceededDimensions: []string{"total_tokens"},
		Usage:              state.TaskUsage{TotalTokens: 15000},
		Budget:             state.TaskBudget{MaxTotalTokens: 10000},
	}
	event := r.ResolveOutcome(dec, "t1", "r1", state.AutonomyFull, 1000)
	if event == nil {
		t.Fatal("expected exhaustion event")
	}
	if event.Action != ActionFallback {
		t.Errorf("full/tokens: expected fallback, got %s", event.Action)
	}
	if event.TaskID != "t1" || event.RunID != "r1" {
		t.Error("should carry task/run IDs")
	}
	if len(event.Reasons) == 0 {
		t.Error("should have reasons")
	}
}

func TestResolveOutcome_RuntimeExhaustion_FullMode(t *testing.T) {
	r := NewOutcomeResolver(nil)
	dec := BudgetDecision{
		Verdict:            BudgetBlock,
		ExceededDimensions: []string{"runtime_ms"},
	}
	event := r.ResolveOutcome(dec, "t1", "r1", state.AutonomyFull, 1000)
	if event.Action != ActionFail {
		t.Errorf("full/runtime: expected fail, got %s", event.Action)
	}
}

func TestResolveOutcome_CostExhaustion_AnyMode(t *testing.T) {
	r := NewOutcomeResolver(nil)
	dec := BudgetDecision{
		Verdict:            BudgetBlock,
		ExceededDimensions: []string{"cost_micros_usd"},
	}
	// Even in full mode, cost → escalate.
	event := r.ResolveOutcome(dec, "t1", "r1", state.AutonomyFull, 1000)
	if event.Action != ActionEscalate {
		t.Errorf("cost exhaustion should always escalate, got %s", event.Action)
	}
}

func TestResolveOutcome_MultipleReasons_CostWins(t *testing.T) {
	r := NewOutcomeResolver(nil)
	dec := BudgetDecision{
		Verdict:            BudgetBlock,
		ExceededDimensions: []string{"total_tokens", "cost_micros_usd", "tool_calls"},
	}
	event := r.ResolveOutcome(dec, "t1", "r1", state.AutonomyFull, 1000)
	// Cost has highest priority, should drive action.
	if event.Action != ActionEscalate {
		t.Errorf("cost should be primary reason driving escalation, got %s", event.Action)
	}
	if len(event.Reasons) != 3 {
		t.Errorf("should have 3 reasons, got %d", len(event.Reasons))
	}
}

func TestResolveOutcome_SupervisedAlwaysEscalates(t *testing.T) {
	r := NewOutcomeResolver(nil)
	reasons := []string{"total_tokens", "runtime_ms", "tool_calls", "delegations"}
	for _, dim := range reasons {
		dec := BudgetDecision{
			Verdict:            BudgetBlock,
			ExceededDimensions: []string{dim},
		}
		event := r.ResolveOutcome(dec, "t1", "r1", state.AutonomySupervised, 1000)
		if event.Action != ActionEscalate {
			t.Errorf("supervised/%s: expected escalate, got %s", dim, event.Action)
		}
	}
}

func TestResolveOutcome_EventIDsUnique(t *testing.T) {
	r := NewOutcomeResolver(nil)
	dec := BudgetDecision{
		Verdict:            BudgetBlock,
		ExceededDimensions: []string{"total_tokens"},
	}
	e1 := r.ResolveOutcome(dec, "t1", "r1", state.AutonomyFull, 1000)
	e2 := r.ResolveOutcome(dec, "t1", "r1", state.AutonomyFull, 1001)
	if e1.EventID == e2.EventID {
		t.Error("event IDs should be unique")
	}
}

// ── selectPrimaryReason tests ──────────────────────────────────────────────────

func TestSelectPrimaryReason_CostHighestPriority(t *testing.T) {
	reasons := []ExhaustionReason{ExhaustionTokens, ExhaustionCost, ExhaustionToolCalls}
	got := selectPrimaryReason(reasons)
	if got != ExhaustionCost {
		t.Errorf("cost should be primary, got %s", got)
	}
}

func TestSelectPrimaryReason_Empty(t *testing.T) {
	got := selectPrimaryReason(nil)
	if got != ExhaustionTokens {
		t.Errorf("empty should default to tokens, got %s", got)
	}
}

func TestSelectPrimaryReason_Single(t *testing.T) {
	got := selectPrimaryReason([]ExhaustionReason{ExhaustionDelegation})
	if got != ExhaustionDelegation {
		t.Errorf("single should return itself, got %s", got)
	}
}

// ── classifyExhaustionReasons tests ────────────────────────────────────────────

func TestClassifyReasons_DeduplicatesTokens(t *testing.T) {
	dec := BudgetDecision{
		Verdict:            BudgetBlock,
		ExceededDimensions: []string{"total_tokens", "prompt_tokens", "completion_tokens"},
	}
	reasons := classifyExhaustionReasons(dec)
	tokenCount := 0
	for _, r := range reasons {
		if r == ExhaustionTokens {
			tokenCount++
		}
	}
	if tokenCount != 1 {
		t.Errorf("should deduplicate token reasons, got %d", tokenCount)
	}
}

func TestClassifyReasons_AllTypes(t *testing.T) {
	dec := BudgetDecision{
		Verdict:            BudgetBlock,
		ExceededDimensions: []string{"total_tokens", "runtime_ms", "cost_micros_usd", "tool_calls", "delegations"},
	}
	reasons := classifyExhaustionReasons(dec)
	if len(reasons) != 5 {
		t.Errorf("expected 5 reasons, got %d: %v", len(reasons), reasons)
	}
}

func TestClassifyReasons_EmptyDefault(t *testing.T) {
	dec := BudgetDecision{Verdict: BudgetBlock}
	reasons := classifyExhaustionReasons(dec)
	if len(reasons) != 1 || reasons[0] != ExhaustionTokens {
		t.Errorf("empty should default to tokens, got %v", reasons)
	}
}

// ── IsBudgetFailure tests ──────────────────────────────────────────────────────

func TestIsBudgetFailure_True(t *testing.T) {
	cases := []string{
		"budget exhausted: total_tokens",
		"Budget exceeded: cost limit",
		"BUDGET EXHAUSTION: runtime",
	}
	for _, s := range cases {
		if !IsBudgetFailure(s) {
			t.Errorf("should recognize as budget failure: %q", s)
		}
	}
}

func TestIsBudgetFailure_False(t *testing.T) {
	cases := []string{
		"provider error: 500",
		"context cancelled",
		"budget is fine", // has budget but not exhaust/exceed
		"",
	}
	for _, s := range cases {
		if IsBudgetFailure(s) {
			t.Errorf("should not recognize as budget failure: %q", s)
		}
	}
}

// ── FormatExhaustionEvent tests ────────────────────────────────────────────────

func TestFormatExhaustionEvent_IncludesKey(t *testing.T) {
	event := ExhaustionEvent{
		EventID:      "exhaust-1",
		TaskID:       "task-1",
		RunID:        "run-1",
		Reasons:      []ExhaustionReason{ExhaustionTokens},
		Action:       ActionFallback,
		ActionReason: "switching to fallback",
		AutonomyMode: state.AutonomyFull,
	}
	output := FormatExhaustionEvent(event)
	if !strings.Contains(output, "exhaust-1") {
		t.Error("should include event ID")
	}
	if !strings.Contains(output, "task-1") {
		t.Error("should include task ID")
	}
	if !strings.Contains(output, "fallback") {
		t.Error("should include action")
	}
}

// ── End-to-end: enforcement → outcome ──────────────────────────────────────────

func TestEndToEnd_EnforcementToOutcome(t *testing.T) {
	enforcer := NewBudgetEnforcer()
	resolver := NewOutcomeResolver(nil)
	collector := collectorWithUsage(15000, 3, 1)
	budget := state.TaskBudget{MaxTotalTokens: 10000}

	// Enforcement blocks.
	dec := enforcer.CheckTurnStart(budget, collector)
	if dec.Verdict != BudgetBlock {
		t.Fatalf("should block, got %s", dec.Verdict)
	}

	// Resolver produces outcome.
	event := resolver.ResolveOutcome(dec, "task-1", "run-1", state.AutonomyFull, 1000)
	if event == nil {
		t.Fatal("expected exhaustion event")
	}
	if event.Action != ActionFallback {
		t.Errorf("full/tokens: expected fallback, got %s", event.Action)
	}

	// Same enforcement, supervised mode → escalate.
	event2 := resolver.ResolveOutcome(dec, "task-1", "run-1", state.AutonomySupervised, 1001)
	if event2.Action != ActionEscalate {
		t.Errorf("supervised/tokens: expected escalate, got %s", event2.Action)
	}
}

// ── JSON round-trip ────────────────────────────────────────────────────────────

func TestExhaustionEvent_JSONRoundTrip(t *testing.T) {
	event := ExhaustionEvent{
		EventID:      "exhaust-1",
		TaskID:       "task-1",
		RunID:        "run-1",
		Reasons:      []ExhaustionReason{ExhaustionTokens, ExhaustionCost},
		Action:       ActionEscalate,
		ActionReason: "escalating",
		Usage:        state.TaskUsage{TotalTokens: 15000},
		Budget:       state.TaskBudget{MaxTotalTokens: 10000},
		AutonomyMode: state.AutonomyPlanApproval,
		CreatedAt:    1000,
		Meta:         map[string]any{"source": "test"},
	}
	blob, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded ExhaustionEvent
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Action != event.Action || len(decoded.Reasons) != 2 {
		t.Errorf("round-trip mismatch: got %+v", decoded)
	}
}

// ── Concurrency test ───────────────────────────────────────────────────────────

func TestOutcomeResolver_ConcurrentResolve(t *testing.T) {
	r := NewOutcomeResolver(nil)
	dec := BudgetDecision{
		Verdict:            BudgetBlock,
		ExceededDimensions: []string{"total_tokens"},
	}
	var wg sync.WaitGroup
	events := make([]*ExhaustionEvent, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			events[i] = r.ResolveOutcome(dec, "t1", "r1", state.AutonomyFull, int64(1000+i))
		}(i)
	}
	wg.Wait()
	// All event IDs should be unique.
	seen := make(map[string]bool, 50)
	for _, e := range events {
		if e == nil {
			t.Fatal("expected non-nil event")
		}
		if seen[e.EventID] {
			t.Errorf("duplicate event ID: %s", e.EventID)
		}
		seen[e.EventID] = true
	}
}

// ── Custom policy test ─────────────────────────────────────────────────────────

func TestCustomPolicy_Override(t *testing.T) {
	// Custom: always block on token exhaustion in full mode.
	rules := map[exhaustionKey]ExhaustionAction{
		{ExhaustionTokens, state.AutonomyFull}: ActionBlock,
	}
	resolver := NewOutcomeResolver(NewExhaustionPolicy(rules))
	dec := BudgetDecision{
		Verdict:            BudgetBlock,
		ExceededDimensions: []string{"total_tokens"},
	}
	event := resolver.ResolveOutcome(dec, "t1", "r1", state.AutonomyFull, 1000)
	if event.Action != ActionBlock {
		t.Errorf("custom policy should block, got %s", event.Action)
	}
}
