// usage.go aggregates provider-reported and synthetic token/cost usage
// across turns, runs, tasks, and goals. It prefers provider-reported
// counts and falls back to synthetic estimates only when provider data
// is unavailable.
package planner

import (
	"fmt"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// UsageSource classifies the origin of a usage measurement.
type UsageSource string

const (
	UsageSourceProvider  UsageSource = "provider"  // from LLM API response
	UsageSourceSynthetic UsageSource = "synthetic" // estimated from token counting
	UsageSourceMixed     UsageSource = "mixed"     // some dimensions provider, some synthetic
)

// TurnUsage captures usage from a single provider turn with source metadata.
type TurnUsage struct {
	// Provider-reported values (zero means not reported).
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`

	// Synthetic estimates (used as fallback when provider doesn't report).
	SyntheticInputTokens  int64 `json:"synthetic_input_tokens,omitempty"`
	SyntheticOutputTokens int64 `json:"synthetic_output_tokens,omitempty"`

	// Observed dimensions (always recorded when available).
	ToolCalls    int   `json:"tool_calls,omitempty"`
	Delegations  int   `json:"delegations,omitempty"`
	WallClockMS  int64 `json:"wall_clock_ms,omitempty"`
	CostMicrosUSD int64 `json:"cost_micros_usd,omitempty"`

	// Source indicates whether the token counts come from the provider.
	Source UsageSource `json:"source"`

	// RecordedAt is the unix timestamp when this measurement was taken.
	RecordedAt int64 `json:"recorded_at,omitempty"`
}

// EffectiveInputTokens returns provider-reported input tokens when available,
// otherwise falls back to the synthetic estimate.
func (t TurnUsage) EffectiveInputTokens() int64 {
	if t.InputTokens > 0 {
		return t.InputTokens
	}
	return t.SyntheticInputTokens
}

// EffectiveOutputTokens returns provider-reported output tokens when available,
// otherwise falls back to the synthetic estimate.
func (t TurnUsage) EffectiveOutputTokens() int64 {
	if t.OutputTokens > 0 {
		return t.OutputTokens
	}
	return t.SyntheticOutputTokens
}

// UsageCollector aggregates turn-level usage into cumulative TaskUsage.
// It is safe for concurrent use.
type UsageCollector struct {
	mu     sync.Mutex
	turns  []TurnUsage
	runID  string
	taskID string
}

// NewUsageCollector creates a collector scoped to a specific run and task.
func NewUsageCollector(runID, taskID string) *UsageCollector {
	return &UsageCollector{
		runID:  runID,
		taskID: taskID,
	}
}

// RecordTurn records usage from a single provider turn.
func (c *UsageCollector) RecordTurn(turn TurnUsage) {
	if turn.RecordedAt == 0 {
		turn.RecordedAt = time.Now().Unix()
	}
	// Classify source based on what's available.
	if turn.Source == "" {
		turn.Source = classifySource(turn)
	}

	c.mu.Lock()
	c.turns = append(c.turns, turn)
	c.mu.Unlock()
}

// Cumulative returns the aggregated TaskUsage across all recorded turns.
// Provider-reported counts are preferred per-turn; synthetic estimates are
// used only when the provider didn't report for that turn.
func (c *UsageCollector) Cumulative() state.TaskUsage {
	c.mu.Lock()
	defer c.mu.Unlock()

	var usage state.TaskUsage
	for _, turn := range c.turns {
		usage.PromptTokens += int(turn.EffectiveInputTokens())
		usage.CompletionTokens += int(turn.EffectiveOutputTokens())
		usage.TotalTokens += int(turn.EffectiveInputTokens() + turn.EffectiveOutputTokens())
		usage.ToolCalls += turn.ToolCalls
		usage.Delegations += turn.Delegations
		usage.WallClockMS += turn.WallClockMS
		usage.CostMicrosUSD += turn.CostMicrosUSD
	}
	return usage
}

// TurnCount returns the number of recorded turns.
func (c *UsageCollector) TurnCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.turns)
}

// Turns returns a copy of all recorded turn measurements.
func (c *UsageCollector) Turns() []TurnUsage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]TurnUsage, len(c.turns))
	copy(out, c.turns)
	return out
}

// HasProviderData reports whether any turn has provider-reported token counts.
func (c *UsageCollector) HasProviderData() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.turns {
		if t.InputTokens > 0 || t.OutputTokens > 0 {
			return true
		}
	}
	return false
}

// SourceBreakdown returns a summary of how many turns had provider vs synthetic data.
type SourceBreakdown struct {
	ProviderTurns  int `json:"provider_turns"`
	SyntheticTurns int `json:"synthetic_turns"`
	MixedTurns     int `json:"mixed_turns"`
	TotalTurns     int `json:"total_turns"`
}

// Breakdown returns the source classification across all turns.
func (c *UsageCollector) Breakdown() SourceBreakdown {
	c.mu.Lock()
	defer c.mu.Unlock()

	var b SourceBreakdown
	b.TotalTurns = len(c.turns)
	for _, t := range c.turns {
		switch classifySource(t) {
		case UsageSourceProvider:
			b.ProviderTurns++
		case UsageSourceSynthetic:
			b.SyntheticTurns++
		case UsageSourceMixed:
			b.MixedTurns++
		}
	}
	return b
}

// classifySource determines the usage source for a turn based on available data.
func classifySource(t TurnUsage) UsageSource {
	hasProvider := t.InputTokens > 0 || t.OutputTokens > 0
	hasSynthetic := t.SyntheticInputTokens > 0 || t.SyntheticOutputTokens > 0

	if hasProvider && !hasSynthetic {
		return UsageSourceProvider
	}
	if !hasProvider && hasSynthetic {
		return UsageSourceSynthetic
	}
	if hasProvider && hasSynthetic {
		return UsageSourceMixed
	}
	return UsageSourceProvider // no token data at all; treat as provider (zero)
}

// AggregateUsage combines multiple TaskUsage values into a single cumulative total.
// This is used for rolling up run-level usage to task or goal scope.
func AggregateUsage(usages ...state.TaskUsage) state.TaskUsage {
	var total state.TaskUsage
	for _, u := range usages {
		total.Add(u)
	}
	return total
}

// UsageSummary provides a human-readable summary of usage relative to a budget.
type UsageSummary struct {
	Usage     state.TaskUsage    `json:"usage"`
	Budget    state.TaskBudget   `json:"budget"`
	Exceeded  state.BudgetExceeded `json:"exceeded"`
	Remaining state.TaskBudget   `json:"remaining"`
	Source    SourceBreakdown    `json:"source"`
}

// Summarize produces a full usage summary including budget comparison.
func (c *UsageCollector) Summarize(budget state.TaskBudget) UsageSummary {
	usage := c.Cumulative()
	return UsageSummary{
		Usage:     usage,
		Budget:    budget,
		Exceeded:  budget.CheckUsage(usage),
		Remaining: budget.Remaining(usage),
		Source:    c.Breakdown(),
	}
}

// FormatSummary returns a compact human-readable string summarizing usage.
// Unlimited dimensions (0) are rendered as "∞" rather than "0" to avoid
// misleading operators into thinking the limit is zero.
func FormatSummary(usage state.TaskUsage, budget state.TaskBudget) string {
	exceeded := budget.CheckUsage(usage)
	if budget.IsZero() {
		return fmt.Sprintf("tokens=%d tools=%d delegations=%d cost=$%.4f (no budget set)",
			usage.TotalTokens, usage.ToolCalls, usage.Delegations,
			float64(usage.CostMicrosUSD)/1_000_000)
	}

	status := "OK"
	if exceeded.Any() {
		status = "EXCEEDED: " + joinReasons(exceeded.Reasons())
	}

	return fmt.Sprintf("tokens=%d/%s tools=%d/%s delegations=%d/%s cost=$%.4f/%s [%s]",
		usage.TotalTokens, fmtLimit(budget.MaxTotalTokens),
		usage.ToolCalls, fmtLimit(budget.MaxToolCalls),
		usage.Delegations, fmtLimit(budget.MaxDelegations),
		float64(usage.CostMicrosUSD)/1_000_000, fmtCostLimit(budget.MaxCostMicrosUSD),
		status)
}

// fmtLimit formats an integer budget dimension, rendering 0 as "∞".
func fmtLimit(v int) string {
	if v == 0 {
		return "∞"
	}
	return fmt.Sprintf("%d", v)
}

// fmtCostLimit formats a cost budget dimension in USD, rendering 0 as "∞".
func fmtCostLimit(v int64) string {
	if v == 0 {
		return "∞"
	}
	return fmt.Sprintf("$%.4f", float64(v)/1_000_000)
}

func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	result := reasons[0]
	for _, r := range reasons[1:] {
		result += ", " + r
	}
	return result
}
