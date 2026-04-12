package planner

import (
	"encoding/json"
	"sync"
	"testing"

	"metiq/internal/store/state"
)

// ── Helper ─────────────────────────────────────────────────────────────────────

func collectorWithUsage(tokens, tools, delegations int) *UsageCollector {
	c := NewUsageCollector("run-1", "task-1")
	c.RecordTurn(TurnUsage{
		InputTokens:  int64(tokens / 2),
		OutputTokens: int64(tokens / 2),
		ToolCalls:    tools,
		Delegations:  delegations,
	})
	return c
}

// ── BudgetDecision tests ───────────────────────────────────────────────────────

func TestBudgetDecision_Allowed(t *testing.T) {
	if !(BudgetDecision{Verdict: BudgetAllow}).Allowed() {
		t.Error("allow should be Allowed()")
	}
	if !(BudgetDecision{Verdict: BudgetWarn}).Allowed() {
		t.Error("warn should be Allowed()")
	}
	if !(BudgetDecision{Verdict: BudgetSkip}).Allowed() {
		t.Error("skip should be Allowed()")
	}
	if (BudgetDecision{Verdict: BudgetBlock}).Allowed() {
		t.Error("block should not be Allowed()")
	}
}

// ── CheckTurnStart tests ───────────────────────────────────────────────────────

func TestCheckTurnStart_NoBudget(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(5000, 3, 1)
	dec := e.CheckTurnStart(state.TaskBudget{}, c)
	if dec.Verdict != BudgetSkip {
		t.Errorf("no budget should skip, got %s", dec.Verdict)
	}
}

func TestCheckTurnStart_WithinBudget(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(5000, 3, 1)
	budget := state.TaskBudget{MaxTotalTokens: 100000, MaxToolCalls: 50}
	dec := e.CheckTurnStart(budget, c)
	if dec.Verdict != BudgetAllow {
		t.Errorf("should allow, got %s: %s", dec.Verdict, dec.Reason)
	}
}

func TestCheckTurnStart_Exceeded(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(15000, 3, 1)
	budget := state.TaskBudget{MaxTotalTokens: 10000}
	dec := e.CheckTurnStart(budget, c)
	if dec.Verdict != BudgetBlock {
		t.Errorf("should block, got %s", dec.Verdict)
	}
	if len(dec.ExceededDimensions) == 0 {
		t.Error("should list exceeded dimensions")
	}
}

func TestCheckTurnStart_Warning(t *testing.T) {
	e := NewBudgetEnforcer() // threshold 0.8
	c := collectorWithUsage(8500, 3, 1)
	budget := state.TaskBudget{MaxTotalTokens: 10000}
	dec := e.CheckTurnStart(budget, c)
	if dec.Verdict != BudgetWarn {
		t.Errorf("should warn at 85%%, got %s: %s", dec.Verdict, dec.Reason)
	}
	if len(dec.WarningDimensions) == 0 {
		t.Error("should list warning dimensions")
	}
}

func TestCheckTurnStart_CustomThreshold(t *testing.T) {
	e := NewBudgetEnforcerWithThreshold(0.5) // warn at 50%
	c := collectorWithUsage(6000, 0, 0)
	budget := state.TaskBudget{MaxTotalTokens: 10000}
	dec := e.CheckTurnStart(budget, c)
	if dec.Verdict != BudgetWarn {
		t.Errorf("should warn at 60%% with 50%% threshold, got %s", dec.Verdict)
	}
}

func TestCheckTurnStart_InvalidThreshold(t *testing.T) {
	e := NewBudgetEnforcerWithThreshold(-1)
	if e.WarningThreshold != 0.8 {
		t.Errorf("invalid threshold should default to 0.8, got %f", e.WarningThreshold)
	}
}

// ── CheckTurnStartFromUsage tests ──────────────────────────────────────────────

func TestCheckTurnStartFromUsage_Blocked(t *testing.T) {
	e := NewBudgetEnforcer()
	usage := state.TaskUsage{TotalTokens: 15000}
	budget := state.TaskBudget{MaxTotalTokens: 10000}
	dec := e.CheckTurnStartFromUsage(budget, usage)
	if dec.Verdict != BudgetBlock {
		t.Errorf("should block, got %s", dec.Verdict)
	}
}

func TestCheckTurnStartFromUsage_NoBudget(t *testing.T) {
	e := NewBudgetEnforcer()
	dec := e.CheckTurnStartFromUsage(state.TaskBudget{}, state.TaskUsage{TotalTokens: 99999})
	if dec.Verdict != BudgetSkip {
		t.Errorf("no budget should skip, got %s", dec.Verdict)
	}
}

// ── CheckToolDispatch tests ────────────────────────────────────────────────────

func TestCheckToolDispatch_NoBudget(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(0, 100, 0)
	dec := e.CheckToolDispatch(state.TaskBudget{}, c)
	if dec.Verdict != BudgetSkip {
		t.Errorf("no budget should skip, got %s", dec.Verdict)
	}
}

func TestCheckToolDispatch_WithinBudget(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(0, 5, 0)
	budget := state.TaskBudget{MaxToolCalls: 10}
	dec := e.CheckToolDispatch(budget, c)
	if dec.Verdict != BudgetAllow {
		t.Errorf("should allow, got %s: %s", dec.Verdict, dec.Reason)
	}
}

func TestCheckToolDispatch_WouldExceed(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(0, 10, 0)
	budget := state.TaskBudget{MaxToolCalls: 10}
	dec := e.CheckToolDispatch(budget, c)
	if dec.Verdict != BudgetBlock {
		t.Errorf("should block (10+1 > 10), got %s", dec.Verdict)
	}
}

func TestCheckToolDispatch_OverallExceeded(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(15000, 2, 0)
	budget := state.TaskBudget{MaxTotalTokens: 10000, MaxToolCalls: 50}
	dec := e.CheckToolDispatch(budget, c)
	if dec.Verdict != BudgetBlock {
		t.Errorf("overall exceeded should block tool dispatch, got %s", dec.Verdict)
	}
}

// ── CheckDelegation tests ──────────────────────────────────────────────────────

func TestCheckDelegation_NoBudget(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(0, 0, 0)
	childBudget := state.TaskBudget{MaxToolCalls: 5}
	dec, effective := e.CheckDelegation(state.TaskBudget{}, c, childBudget)
	if dec.Verdict != BudgetSkip {
		t.Errorf("no parent budget should skip, got %s", dec.Verdict)
	}
	// Child budget passes through unchanged.
	if effective.MaxToolCalls != 5 {
		t.Errorf("child budget should pass through, got %d", effective.MaxToolCalls)
	}
}

func TestCheckDelegation_WithinBudget(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(5000, 3, 1)
	parentBudget := state.TaskBudget{MaxTotalTokens: 100000, MaxDelegations: 5}
	childBudget := state.TaskBudget{MaxTotalTokens: 20000}
	dec, effective := e.CheckDelegation(parentBudget, c, childBudget)
	if dec.Verdict != BudgetAllow {
		t.Errorf("should allow, got %s: %s", dec.Verdict, dec.Reason)
	}
	// Child tokens should be narrowed to min(remaining, requested).
	if effective.MaxTotalTokens > 20000 {
		t.Errorf("child tokens should be <= requested, got %d", effective.MaxTotalTokens)
	}
}

func TestCheckDelegation_WouldExceedCount(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(0, 0, 3)
	parentBudget := state.TaskBudget{MaxDelegations: 3}
	dec, effective := e.CheckDelegation(parentBudget, c, state.TaskBudget{})
	if dec.Verdict != BudgetBlock {
		t.Errorf("should block (3+1 > 3), got %s", dec.Verdict)
	}
	if !effective.IsZero() {
		t.Error("blocked delegation should return zero child budget")
	}
}

func TestCheckDelegation_ParentExhausted(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(15000, 0, 0)
	parentBudget := state.TaskBudget{MaxTotalTokens: 10000, MaxDelegations: 5}
	dec, _ := e.CheckDelegation(parentBudget, c, state.TaskBudget{})
	if dec.Verdict != BudgetBlock {
		t.Errorf("exhausted parent should block delegation, got %s", dec.Verdict)
	}
}

func TestCheckDelegation_NarrowsChildToRemaining(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(8000, 5, 0)
	parentBudget := state.TaskBudget{
		MaxTotalTokens: 10000,
		MaxToolCalls:   20,
		MaxDelegations: 5,
	}
	// Child requests more than parent has remaining.
	childBudget := state.TaskBudget{
		MaxTotalTokens: 50000, // parent only has ~2000 left
		MaxToolCalls:   100,   // parent only has ~15 left
	}
	dec, effective := e.CheckDelegation(parentBudget, c, childBudget)
	if dec.Verdict != BudgetAllow {
		t.Fatalf("should allow, got %s: %s", dec.Verdict, dec.Reason)
	}
	// Remaining tokens: 10000 - 8000 = 2000. Narrow(50000) → 2000.
	if effective.MaxTotalTokens != 2000 {
		t.Errorf("child tokens should be narrowed to remaining 2000, got %d", effective.MaxTotalTokens)
	}
	// Remaining tools: 20 - 5 = 15. Narrow(100) → 15.
	if effective.MaxToolCalls != 15 {
		t.Errorf("child tools should be narrowed to remaining 15, got %d", effective.MaxToolCalls)
	}
}

// ── CheckRuntime tests ─────────────────────────────────────────────────────────

func TestCheckRuntime_NoBudget(t *testing.T) {
	e := NewBudgetEnforcer()
	dec := e.CheckRuntime(state.TaskBudget{}, 1000, 9999999)
	if dec.Verdict != BudgetSkip {
		t.Errorf("no runtime budget should skip, got %s", dec.Verdict)
	}
}

func TestCheckRuntime_WithinBudget(t *testing.T) {
	e := NewBudgetEnforcer()
	budget := state.TaskBudget{MaxRuntimeMS: 60000}
	dec := e.CheckRuntime(budget, 1000, 31000) // 30s elapsed, limit 60s
	if dec.Verdict != BudgetAllow {
		t.Errorf("should allow, got %s", dec.Verdict)
	}
}

func TestCheckRuntime_Exceeded(t *testing.T) {
	e := NewBudgetEnforcer()
	budget := state.TaskBudget{MaxRuntimeMS: 60000}
	dec := e.CheckRuntime(budget, 1000, 62000) // 61s elapsed, limit 60s
	if dec.Verdict != BudgetBlock {
		t.Errorf("should block, got %s", dec.Verdict)
	}
}

func TestCheckRuntime_Warning(t *testing.T) {
	e := NewBudgetEnforcer() // threshold 0.8
	budget := state.TaskBudget{MaxRuntimeMS: 60000}
	dec := e.CheckRuntime(budget, 1000, 50000) // 49s elapsed, 80% = 48s
	if dec.Verdict != BudgetWarn {
		t.Errorf("should warn at ~82%%, got %s", dec.Verdict)
	}
}

func TestCheckRuntime_NegativeElapsed(t *testing.T) {
	e := NewBudgetEnforcer()
	budget := state.TaskBudget{MaxRuntimeMS: 60000}
	dec := e.CheckRuntime(budget, 9999, 1000) // now < startedAt
	if dec.Verdict != BudgetAllow {
		t.Errorf("negative elapsed should clamp to 0 and allow, got %s", dec.Verdict)
	}
}

// ── CheckAll tests ─────────────────────────────────────────────────────────────

func TestCheckAll_AllWithinBudget(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(5000, 3, 1)
	budget := state.TaskBudget{MaxTotalTokens: 100000, MaxToolCalls: 50, MaxRuntimeMS: 60000}
	dec := e.CheckAll(budget, c, 1000, 31000)
	if dec.Verdict != BudgetAllow {
		t.Errorf("should allow, got %s: %s", dec.Verdict, dec.Reason)
	}
}

func TestCheckAll_TurnBlocked(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(15000, 3, 1)
	budget := state.TaskBudget{MaxTotalTokens: 10000, MaxRuntimeMS: 60000}
	dec := e.CheckAll(budget, c, 1000, 31000)
	if dec.Verdict != BudgetBlock {
		t.Errorf("should block on tokens, got %s", dec.Verdict)
	}
}

func TestCheckAll_RuntimeBlocked(t *testing.T) {
	e := NewBudgetEnforcer()
	c := collectorWithUsage(5000, 3, 1)
	budget := state.TaskBudget{MaxTotalTokens: 100000, MaxRuntimeMS: 30000}
	dec := e.CheckAll(budget, c, 1000, 62000) // 61s elapsed, limit 30s
	if dec.Verdict != BudgetBlock {
		t.Errorf("should block on runtime, got %s", dec.Verdict)
	}
}

func TestCheckAll_MergesWarnings(t *testing.T) {
	e := NewBudgetEnforcer()
	// 85% token usage + 85% runtime usage.
	c := collectorWithUsage(8500, 0, 0)
	budget := state.TaskBudget{MaxTotalTokens: 10000, MaxRuntimeMS: 60000}
	dec := e.CheckAll(budget, c, 1000, 50000) // 49s elapsed ~82%
	if dec.Verdict != BudgetWarn {
		t.Errorf("should warn, got %s: %s", dec.Verdict, dec.Reason)
	}
	if len(dec.WarningDimensions) < 2 {
		t.Errorf("should have warnings from both, got %v", dec.WarningDimensions)
	}
}

// ── Concurrency test ───────────────────────────────────────────────────────────

func TestBudgetEnforcer_ConcurrentChecks(t *testing.T) {
	e := NewBudgetEnforcer()
	c := NewUsageCollector("run-1", "task-1")
	budget := state.TaskBudget{MaxTotalTokens: 100000, MaxToolCalls: 1000}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.RecordTurn(TurnUsage{InputTokens: 100, OutputTokens: 50, ToolCalls: 1})
			_ = e.CheckTurnStart(budget, c)
			_ = e.CheckToolDispatch(budget, c)
		}()
	}
	wg.Wait()

	usage := c.Cumulative()
	if usage.ToolCalls != 50 {
		t.Errorf("expected 50 tool calls, got %d", usage.ToolCalls)
	}
}

// ── JSON round-trip ────────────────────────────────────────────────────────────

func TestBudgetDecision_JSONRoundTrip(t *testing.T) {
	dec := BudgetDecision{
		Verdict:            BudgetBlock,
		Reason:             "budget exhausted: total_tokens",
		ExceededDimensions: []string{"total_tokens"},
		Usage:              state.TaskUsage{TotalTokens: 15000},
		Budget:             state.TaskBudget{MaxTotalTokens: 10000},
	}
	blob, err := json.Marshal(dec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded BudgetDecision
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Verdict != dec.Verdict {
		t.Errorf("verdict mismatch: got %s", decoded.Verdict)
	}
	if len(decoded.ExceededDimensions) != 1 {
		t.Errorf("exceeded dimensions mismatch: got %v", decoded.ExceededDimensions)
	}
}
