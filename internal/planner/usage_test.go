package planner

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"metiq/internal/store/state"
)

// --- TurnUsage effective tokens ---

func TestTurnUsage_EffectiveTokens_ProviderPreferred(t *testing.T) {
	tu := TurnUsage{
		InputTokens:          1000,
		OutputTokens:         500,
		SyntheticInputTokens: 900,
		SyntheticOutputTokens: 450,
	}
	if got := tu.EffectiveInputTokens(); got != 1000 {
		t.Errorf("EffectiveInputTokens = %d, want 1000 (provider)", got)
	}
	if got := tu.EffectiveOutputTokens(); got != 500 {
		t.Errorf("EffectiveOutputTokens = %d, want 500 (provider)", got)
	}
}

func TestTurnUsage_EffectiveTokens_SyntheticFallback(t *testing.T) {
	tu := TurnUsage{
		SyntheticInputTokens:  900,
		SyntheticOutputTokens: 450,
	}
	if got := tu.EffectiveInputTokens(); got != 900 {
		t.Errorf("EffectiveInputTokens = %d, want 900 (synthetic)", got)
	}
	if got := tu.EffectiveOutputTokens(); got != 450 {
		t.Errorf("EffectiveOutputTokens = %d, want 450 (synthetic)", got)
	}
}

func TestTurnUsage_EffectiveTokens_NoData(t *testing.T) {
	tu := TurnUsage{}
	if got := tu.EffectiveInputTokens(); got != 0 {
		t.Errorf("EffectiveInputTokens = %d, want 0", got)
	}
}

// --- classifySource ---

func TestClassifySource_ProviderOnly(t *testing.T) {
	tu := TurnUsage{InputTokens: 100}
	if got := classifySource(tu); got != UsageSourceProvider {
		t.Errorf("source = %q, want provider", got)
	}
}

func TestClassifySource_SyntheticOnly(t *testing.T) {
	tu := TurnUsage{SyntheticInputTokens: 100}
	if got := classifySource(tu); got != UsageSourceSynthetic {
		t.Errorf("source = %q, want synthetic", got)
	}
}

func TestClassifySource_Mixed(t *testing.T) {
	tu := TurnUsage{InputTokens: 100, SyntheticOutputTokens: 50}
	if got := classifySource(tu); got != UsageSourceMixed {
		t.Errorf("source = %q, want mixed", got)
	}
}

func TestClassifySource_NoData(t *testing.T) {
	tu := TurnUsage{ToolCalls: 1} // has tool calls but no tokens
	if got := classifySource(tu); got != UsageSourceProvider {
		t.Errorf("source = %q, want provider (default)", got)
	}
}

// --- UsageCollector ---

func TestUsageCollector_EmptyCumulative(t *testing.T) {
	c := NewUsageCollector("run-1", "task-1")
	usage := c.Cumulative()
	if usage.TotalTokens != 0 || usage.ToolCalls != 0 {
		t.Errorf("empty collector should have zero usage: %+v", usage)
	}
	if c.TurnCount() != 0 {
		t.Errorf("TurnCount = %d, want 0", c.TurnCount())
	}
}

func TestUsageCollector_SingleProviderTurn(t *testing.T) {
	c := NewUsageCollector("run-1", "task-1")
	c.RecordTurn(TurnUsage{
		InputTokens:  1000,
		OutputTokens: 500,
		ToolCalls:    3,
		WallClockMS:  2000,
	})

	usage := c.Cumulative()
	if usage.PromptTokens != 1000 {
		t.Errorf("PromptTokens = %d, want 1000", usage.PromptTokens)
	}
	if usage.CompletionTokens != 500 {
		t.Errorf("CompletionTokens = %d, want 500", usage.CompletionTokens)
	}
	if usage.TotalTokens != 1500 {
		t.Errorf("TotalTokens = %d, want 1500", usage.TotalTokens)
	}
	if usage.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", usage.ToolCalls)
	}
}

func TestUsageCollector_MixedProviderAndSynthetic(t *testing.T) {
	c := NewUsageCollector("run-1", "task-1")

	// Turn 1: provider-reported
	c.RecordTurn(TurnUsage{
		InputTokens:  1000,
		OutputTokens: 500,
		ToolCalls:    2,
	})
	// Turn 2: synthetic only (provider didn't report)
	c.RecordTurn(TurnUsage{
		SyntheticInputTokens:  800,
		SyntheticOutputTokens: 400,
		ToolCalls:             1,
	})

	usage := c.Cumulative()
	// Turn 1 uses provider (1000+500), turn 2 uses synthetic (800+400).
	if usage.PromptTokens != 1800 {
		t.Errorf("PromptTokens = %d, want 1800", usage.PromptTokens)
	}
	if usage.CompletionTokens != 900 {
		t.Errorf("CompletionTokens = %d, want 900", usage.CompletionTokens)
	}
	if usage.TotalTokens != 2700 {
		t.Errorf("TotalTokens = %d, want 2700", usage.TotalTokens)
	}
	if usage.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", usage.ToolCalls)
	}
}

func TestUsageCollector_ProviderPreferredOverSynthetic(t *testing.T) {
	c := NewUsageCollector("run-1", "task-1")
	// Both provider and synthetic available — provider wins.
	c.RecordTurn(TurnUsage{
		InputTokens:           1000,
		OutputTokens:          500,
		SyntheticInputTokens:  900,
		SyntheticOutputTokens: 450,
	})

	usage := c.Cumulative()
	if usage.PromptTokens != 1000 {
		t.Errorf("PromptTokens = %d, want 1000 (provider preferred)", usage.PromptTokens)
	}
	if usage.CompletionTokens != 500 {
		t.Errorf("CompletionTokens = %d, want 500 (provider preferred)", usage.CompletionTokens)
	}
}

func TestUsageCollector_HasProviderData(t *testing.T) {
	c := NewUsageCollector("run-1", "task-1")
	if c.HasProviderData() {
		t.Error("empty collector should not have provider data")
	}

	c.RecordTurn(TurnUsage{SyntheticInputTokens: 100})
	if c.HasProviderData() {
		t.Error("synthetic-only should not count as provider data")
	}

	c.RecordTurn(TurnUsage{InputTokens: 100})
	if !c.HasProviderData() {
		t.Error("should have provider data after provider turn")
	}
}

func TestUsageCollector_Breakdown(t *testing.T) {
	c := NewUsageCollector("run-1", "task-1")
	c.RecordTurn(TurnUsage{InputTokens: 100})                                     // provider
	c.RecordTurn(TurnUsage{SyntheticInputTokens: 100})                            // synthetic
	c.RecordTurn(TurnUsage{InputTokens: 100, SyntheticOutputTokens: 50})          // mixed
	c.RecordTurn(TurnUsage{InputTokens: 200, OutputTokens: 100})                  // provider

	b := c.Breakdown()
	if b.TotalTurns != 4 {
		t.Errorf("TotalTurns = %d, want 4", b.TotalTurns)
	}
	if b.ProviderTurns != 2 {
		t.Errorf("ProviderTurns = %d, want 2", b.ProviderTurns)
	}
	if b.SyntheticTurns != 1 {
		t.Errorf("SyntheticTurns = %d, want 1", b.SyntheticTurns)
	}
	if b.MixedTurns != 1 {
		t.Errorf("MixedTurns = %d, want 1", b.MixedTurns)
	}
}

func TestUsageCollector_Turns_ReturnsCopy(t *testing.T) {
	c := NewUsageCollector("run-1", "task-1")
	c.RecordTurn(TurnUsage{InputTokens: 100})
	turns := c.Turns()
	turns[0].InputTokens = 999 // mutate copy
	if c.Turns()[0].InputTokens != 100 {
		t.Error("Turns() should return a defensive copy")
	}
}

// --- Concurrency ---

func TestUsageCollector_ConcurrentAccess(t *testing.T) {
	c := NewUsageCollector("run-1", "task-1")
	var wg sync.WaitGroup

	// 10 writers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.RecordTurn(TurnUsage{InputTokens: int64(n * 100), ToolCalls: 1})
		}(i)
	}
	// 10 readers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Cumulative()
			c.Breakdown()
			c.HasProviderData()
		}()
	}
	wg.Wait()

	if c.TurnCount() != 10 {
		t.Errorf("TurnCount = %d, want 10", c.TurnCount())
	}
	usage := c.Cumulative()
	if usage.ToolCalls != 10 {
		t.Errorf("ToolCalls = %d, want 10", usage.ToolCalls)
	}
}

// --- AggregateUsage ---

func TestAggregateUsage_MultipleRuns(t *testing.T) {
	run1 := state.TaskUsage{PromptTokens: 1000, ToolCalls: 5, CostMicrosUSD: 500}
	run2 := state.TaskUsage{PromptTokens: 2000, ToolCalls: 3, CostMicrosUSD: 300}
	run3 := state.TaskUsage{PromptTokens: 500, ToolCalls: 1, CostMicrosUSD: 100}

	total := AggregateUsage(run1, run2, run3)
	if total.PromptTokens != 3500 {
		t.Errorf("PromptTokens = %d, want 3500", total.PromptTokens)
	}
	if total.ToolCalls != 9 {
		t.Errorf("ToolCalls = %d, want 9", total.ToolCalls)
	}
	if total.CostMicrosUSD != 900 {
		t.Errorf("CostMicrosUSD = %d, want 900", total.CostMicrosUSD)
	}
}

func TestAggregateUsage_Empty(t *testing.T) {
	total := AggregateUsage()
	if total.TotalTokens != 0 {
		t.Errorf("empty aggregate should be zero: %+v", total)
	}
}

// --- Summarize ---

func TestUsageCollector_Summarize(t *testing.T) {
	c := NewUsageCollector("run-1", "task-1")
	c.RecordTurn(TurnUsage{InputTokens: 5000, OutputTokens: 2000, ToolCalls: 3})

	budget := state.TaskBudget{MaxTotalTokens: 10000, MaxToolCalls: 10}
	summary := c.Summarize(budget)

	if summary.Usage.TotalTokens != 7000 {
		t.Errorf("usage tokens = %d, want 7000", summary.Usage.TotalTokens)
	}
	if summary.Exceeded.Any() {
		t.Error("should not be exceeded")
	}
	if summary.Remaining.MaxTotalTokens != 3000 {
		t.Errorf("remaining tokens = %d, want 3000", summary.Remaining.MaxTotalTokens)
	}
}

func TestUsageCollector_Summarize_Exceeded(t *testing.T) {
	c := NewUsageCollector("run-1", "task-1")
	c.RecordTurn(TurnUsage{InputTokens: 8000, OutputTokens: 5000, ToolCalls: 12})

	budget := state.TaskBudget{MaxTotalTokens: 10000, MaxToolCalls: 10}
	summary := c.Summarize(budget)

	if !summary.Exceeded.TotalTokens {
		t.Error("tokens should be exceeded")
	}
	if !summary.Exceeded.ToolCalls {
		t.Error("tool calls should be exceeded")
	}
}

// --- FormatSummary ---

func TestFormatSummary_NoBudget(t *testing.T) {
	usage := state.TaskUsage{TotalTokens: 5000, ToolCalls: 3}
	s := FormatSummary(usage, state.TaskBudget{})
	if !strings.Contains(s, "no budget set") {
		t.Errorf("format = %q, want 'no budget set'", s)
	}
}

func TestFormatSummary_WithinBudget(t *testing.T) {
	usage := state.TaskUsage{TotalTokens: 5000, ToolCalls: 3}
	budget := state.TaskBudget{MaxTotalTokens: 10000, MaxToolCalls: 10}
	s := FormatSummary(usage, budget)
	if !strings.Contains(s, "OK") {
		t.Errorf("format = %q, want 'OK'", s)
	}
	if !strings.Contains(s, "5000/10000") {
		t.Errorf("format = %q, want '5000/10000'", s)
	}
}

func TestFormatSummary_UnlimitedDimensionsShowInfinity(t *testing.T) {
	// Only token budget set; tool/delegation/cost are unlimited (0).
	usage := state.TaskUsage{TotalTokens: 5000, ToolCalls: 3, Delegations: 1}
	budget := state.TaskBudget{MaxTotalTokens: 10000}
	s := FormatSummary(usage, budget)
	// Tokens should show numeric limit.
	if !strings.Contains(s, "5000/10000") {
		t.Errorf("format = %q, want '5000/10000' for token dimension", s)
	}
	// Tools, delegations, cost should show ∞.
	if !strings.Contains(s, "3/∞") {
		t.Errorf("format = %q, want '3/∞' for unlimited tool dimension", s)
	}
	if !strings.Contains(s, "1/∞") {
		t.Errorf("format = %q, want '1/∞' for unlimited delegation dimension", s)
	}
}

func TestFormatSummary_Exceeded(t *testing.T) {
	usage := state.TaskUsage{TotalTokens: 15000, ToolCalls: 3}
	budget := state.TaskBudget{MaxTotalTokens: 10000, MaxToolCalls: 10}
	s := FormatSummary(usage, budget)
	if !strings.Contains(s, "EXCEEDED") {
		t.Errorf("format = %q, want 'EXCEEDED'", s)
	}
}

// --- TurnUsage JSON round-trip ---

func TestTurnUsage_JSONRoundTrip(t *testing.T) {
	tu := TurnUsage{
		InputTokens:           1000,
		OutputTokens:          500,
		SyntheticInputTokens:  900,
		SyntheticOutputTokens: 450,
		ToolCalls:             3,
		WallClockMS:           2000,
		CostMicrosUSD:         1200,
		Source:                UsageSourceMixed,
		RecordedAt:            5000,
	}
	blob, err := json.Marshal(tu)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded TurnUsage
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.InputTokens != 1000 || decoded.Source != UsageSourceMixed {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
}
