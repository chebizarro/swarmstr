// budget_enforcement.go enforces budget checks at turn start, tool dispatch,
// and delegation boundaries. It makes budgets operationally real by refusing
// work that would violate constraints.
package planner

import (
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// ── Budget verdict ─────────────────────────────────────────────────────────────

// BudgetVerdict is the outcome of a budget enforcement check.
type BudgetVerdict string

const (
	// BudgetAllow permits the operation within budget.
	BudgetAllow BudgetVerdict = "allow"
	// BudgetWarn permits the operation but warns that budget is nearly exhausted.
	BudgetWarn BudgetVerdict = "warn"
	// BudgetBlock stops the operation because budget is exhausted.
	BudgetBlock BudgetVerdict = "block"
	// BudgetSkip indicates no budget is set (unlimited).
	BudgetSkip BudgetVerdict = "skip"
)

// BudgetDecision describes the enforcement outcome for a proposed operation.
type BudgetDecision struct {
	// Verdict is the enforcement outcome.
	Verdict BudgetVerdict `json:"verdict"`
	// Reason explains the decision.
	Reason string `json:"reason"`
	// ExceededDimensions lists which budget dimensions are exceeded.
	ExceededDimensions []string `json:"exceeded_dimensions,omitempty"`
	// WarningDimensions lists which dimensions are above the warning threshold.
	WarningDimensions []string `json:"warning_dimensions,omitempty"`
	// Usage is the current cumulative usage at decision time.
	Usage state.TaskUsage `json:"usage"`
	// Budget is the effective budget at decision time.
	Budget state.TaskBudget `json:"budget"`
}

// Allowed reports whether the operation may proceed.
func (d BudgetDecision) Allowed() bool {
	return d.Verdict == BudgetAllow || d.Verdict == BudgetWarn || d.Verdict == BudgetSkip
}

// ── Budget enforcer ────────────────────────────────────────────────────────────

// BudgetEnforcer checks proposed operations against budget constraints
// using live usage data from a UsageCollector.
type BudgetEnforcer struct {
	// WarningThreshold is the fraction (0.0–1.0) of budget consumed that
	// triggers a warning. Default: 0.8 (80%).
	WarningThreshold float64
}

// NewBudgetEnforcer creates an enforcer with the default warning threshold.
func NewBudgetEnforcer() *BudgetEnforcer {
	return &BudgetEnforcer{WarningThreshold: 0.8}
}

// NewBudgetEnforcerWithThreshold creates an enforcer with a custom threshold.
func NewBudgetEnforcerWithThreshold(threshold float64) *BudgetEnforcer {
	if threshold <= 0 || threshold > 1 {
		threshold = 0.8
	}
	return &BudgetEnforcer{WarningThreshold: threshold}
}

// ── Turn-level enforcement ─────────────────────────────────────────────────────

// CheckTurnStart evaluates whether a new turn may begin given the current
// usage and budget. This is the primary pre-turn gate.
func (e *BudgetEnforcer) CheckTurnStart(budget state.TaskBudget, collector *UsageCollector) BudgetDecision {
	if budget.IsZero() {
		return BudgetDecision{Verdict: BudgetSkip, Reason: "no budget set"}
	}

	usage := collector.Cumulative()
	exceeded := budget.CheckUsage(usage)

	if exceeded.Any() {
		return BudgetDecision{
			Verdict:            BudgetBlock,
			Reason:             "budget exhausted: " + joinReasons(exceeded.Reasons()),
			ExceededDimensions: exceeded.Reasons(),
			Usage:              usage,
			Budget:             budget,
		}
	}

	warnings := e.checkWarnings(budget, usage)
	if len(warnings) > 0 {
		return BudgetDecision{
			Verdict:           BudgetWarn,
			Reason:            "budget nearing limit: " + strings.Join(warnings, ", "),
			WarningDimensions: warnings,
			Usage:             usage,
			Budget:            budget,
		}
	}

	return BudgetDecision{
		Verdict: BudgetAllow,
		Reason:  "within budget",
		Usage:   usage,
		Budget:  budget,
	}
}

// CheckTurnStartFromUsage is a variant that takes pre-computed usage
// instead of a collector, for callers that already have cumulative usage.
func (e *BudgetEnforcer) CheckTurnStartFromUsage(budget state.TaskBudget, usage state.TaskUsage) BudgetDecision {
	if budget.IsZero() {
		return BudgetDecision{Verdict: BudgetSkip, Reason: "no budget set"}
	}

	exceeded := budget.CheckUsage(usage)
	if exceeded.Any() {
		return BudgetDecision{
			Verdict:            BudgetBlock,
			Reason:             "budget exhausted: " + joinReasons(exceeded.Reasons()),
			ExceededDimensions: exceeded.Reasons(),
			Usage:              usage,
			Budget:             budget,
		}
	}

	warnings := e.checkWarnings(budget, usage)
	if len(warnings) > 0 {
		return BudgetDecision{
			Verdict:           BudgetWarn,
			Reason:            "budget nearing limit: " + strings.Join(warnings, ", "),
			WarningDimensions: warnings,
			Usage:             usage,
			Budget:            budget,
		}
	}

	return BudgetDecision{
		Verdict: BudgetAllow,
		Reason:  "within budget",
		Usage:   usage,
		Budget:  budget,
	}
}

// ── Tool-level enforcement ─────────────────────────────────────────────────────

// CheckToolDispatch evaluates whether a tool call may proceed. It checks
// the tool call count dimension and overall budget.
func (e *BudgetEnforcer) CheckToolDispatch(budget state.TaskBudget, collector *UsageCollector) BudgetDecision {
	if budget.IsZero() {
		return BudgetDecision{Verdict: BudgetSkip, Reason: "no budget set"}
	}

	usage := collector.Cumulative()

	// Check if adding one more tool call would exceed budget.
	projected := usage
	projected.ToolCalls++
	exceeded := budget.CheckUsage(projected)

	if exceeded.ToolCalls {
		return BudgetDecision{
			Verdict:            BudgetBlock,
			Reason:             fmt.Sprintf("tool call would exceed budget (%d/%d)", usage.ToolCalls+1, budget.MaxToolCalls),
			ExceededDimensions: []string{"tool_calls"},
			Usage:              usage,
			Budget:             budget,
		}
	}

	// Also check if overall budget (tokens, cost) is already exceeded.
	currentExceeded := budget.CheckUsage(usage)
	if currentExceeded.Any() {
		return BudgetDecision{
			Verdict:            BudgetBlock,
			Reason:             "budget already exhausted: " + joinReasons(currentExceeded.Reasons()),
			ExceededDimensions: currentExceeded.Reasons(),
			Usage:              usage,
			Budget:             budget,
		}
	}

	return BudgetDecision{
		Verdict: BudgetAllow,
		Reason:  fmt.Sprintf("tool call permitted (%d/%s)", usage.ToolCalls, fmtLimit(budget.MaxToolCalls)),
		Usage:   usage,
		Budget:  budget,
	}
}

// ── Delegation enforcement ─────────────────────────────────────────────────────

// CheckDelegation evaluates whether a delegation may proceed and computes
// the child budget. It checks the delegation count dimension, ensures the
// child budget is narrowed from the parent's remaining budget, and returns
// the effective child budget on success.
func (e *BudgetEnforcer) CheckDelegation(
	parentBudget state.TaskBudget,
	collector *UsageCollector,
	childBudget state.TaskBudget,
) (BudgetDecision, state.TaskBudget) {
	if parentBudget.IsZero() {
		// No parent budget → child budget passes through as-is.
		return BudgetDecision{Verdict: BudgetSkip, Reason: "no parent budget set"}, childBudget
	}

	usage := collector.Cumulative()

	// Check if adding one more delegation would exceed budget.
	projected := usage
	projected.Delegations++
	exceeded := parentBudget.CheckUsage(projected)

	if exceeded.Delegations {
		return BudgetDecision{
			Verdict:            BudgetBlock,
			Reason:             fmt.Sprintf("delegation would exceed budget (%d/%d)", usage.Delegations+1, parentBudget.MaxDelegations),
			ExceededDimensions: []string{"delegations"},
			Usage:              usage,
			Budget:             parentBudget,
		}, state.TaskBudget{}
	}

	// Check if overall budget is already exceeded.
	currentExceeded := parentBudget.CheckUsage(usage)
	if currentExceeded.Any() {
		return BudgetDecision{
			Verdict:            BudgetBlock,
			Reason:             "parent budget already exhausted: " + joinReasons(currentExceeded.Reasons()),
			ExceededDimensions: currentExceeded.Reasons(),
			Usage:              usage,
			Budget:             parentBudget,
		}, state.TaskBudget{}
	}

	// Compute child budget: narrow the parent's remaining capacity with the
	// child's requested budget. This ensures the child can never overspend
	// what the parent has left.
	remaining := parentBudget.Remaining(usage)
	effectiveChild := remaining.Narrow(childBudget)

	return BudgetDecision{
		Verdict: BudgetAllow,
		Reason:  fmt.Sprintf("delegation permitted (%d/%s)", usage.Delegations, fmtLimit(parentBudget.MaxDelegations)),
		Usage:   usage,
		Budget:  parentBudget,
	}, effectiveChild
}

// ── Runtime enforcement ────────────────────────────────────────────────────────

// CheckRuntime evaluates whether a task has exceeded its wall-clock budget.
// startedAt is the Unix timestamp when the task started.
func (e *BudgetEnforcer) CheckRuntime(budget state.TaskBudget, startedAt int64, now int64) BudgetDecision {
	if budget.MaxRuntimeMS == 0 {
		return BudgetDecision{Verdict: BudgetSkip, Reason: "no runtime budget set"}
	}

	if now == 0 {
		now = time.Now().UnixMilli()
	}
	elapsed := now - startedAt
	if elapsed < 0 {
		elapsed = 0
	}

	if elapsed > budget.MaxRuntimeMS {
		return BudgetDecision{
			Verdict:            BudgetBlock,
			Reason:             fmt.Sprintf("runtime exceeded (%dms/%dms)", elapsed, budget.MaxRuntimeMS),
			ExceededDimensions: []string{"runtime_ms"},
			Usage:              state.TaskUsage{WallClockMS: elapsed},
			Budget:             budget,
		}
	}

	// Warning threshold for runtime.
	threshold := int64(float64(budget.MaxRuntimeMS) * e.WarningThreshold)
	if elapsed > threshold {
		return BudgetDecision{
			Verdict:           BudgetWarn,
			Reason:            fmt.Sprintf("runtime nearing limit (%dms/%dms)", elapsed, budget.MaxRuntimeMS),
			WarningDimensions: []string{"runtime_ms"},
			Usage:             state.TaskUsage{WallClockMS: elapsed},
			Budget:            budget,
		}
	}

	return BudgetDecision{
		Verdict: BudgetAllow,
		Reason:  fmt.Sprintf("runtime within budget (%dms/%dms)", elapsed, budget.MaxRuntimeMS),
		Usage:   state.TaskUsage{WallClockMS: elapsed},
		Budget:  budget,
	}
}

// ── Warning threshold checks ───────────────────────────────────────────────────

// checkWarnings returns dimension names where usage exceeds the warning
// threshold but has not yet exceeded the hard limit.
func (e *BudgetEnforcer) checkWarnings(budget state.TaskBudget, usage state.TaskUsage) []string {
	var warnings []string
	if budget.MaxTotalTokens > 0 {
		if float64(usage.TotalTokens) > float64(budget.MaxTotalTokens)*e.WarningThreshold {
			warnings = append(warnings, "total_tokens")
		}
	}
	if budget.MaxPromptTokens > 0 {
		if float64(usage.PromptTokens) > float64(budget.MaxPromptTokens)*e.WarningThreshold {
			warnings = append(warnings, "prompt_tokens")
		}
	}
	if budget.MaxCompletionTokens > 0 {
		if float64(usage.CompletionTokens) > float64(budget.MaxCompletionTokens)*e.WarningThreshold {
			warnings = append(warnings, "completion_tokens")
		}
	}
	if budget.MaxToolCalls > 0 {
		if float64(usage.ToolCalls) > float64(budget.MaxToolCalls)*e.WarningThreshold {
			warnings = append(warnings, "tool_calls")
		}
	}
	if budget.MaxDelegations > 0 {
		if float64(usage.Delegations) > float64(budget.MaxDelegations)*e.WarningThreshold {
			warnings = append(warnings, "delegations")
		}
	}
	if budget.MaxCostMicrosUSD > 0 {
		if float64(usage.CostMicrosUSD) > float64(budget.MaxCostMicrosUSD)*e.WarningThreshold {
			warnings = append(warnings, "cost_micros_usd")
		}
	}
	return warnings
}

// ── Convenience: combined check ────────────────────────────────────────────────

// CheckAll runs turn and runtime checks in a single pass and returns the
// most restrictive verdict. Useful as a pre-turn gate. It does NOT include
// tool dispatch checking (use CheckToolDispatch separately before each tool
// call, since it projects the next tool count).
func (e *BudgetEnforcer) CheckAll(
	budget state.TaskBudget,
	collector *UsageCollector,
	startedAt int64,
	now int64,
) BudgetDecision {
	// Turn check.
	turn := e.CheckTurnStart(budget, collector)
	if turn.Verdict == BudgetBlock {
		return turn
	}

	// Runtime check.
	runtime := e.CheckRuntime(budget, startedAt, now)
	if runtime.Verdict == BudgetBlock {
		return runtime
	}

	// Return the most restrictive non-block verdict.
	if turn.Verdict == BudgetWarn || runtime.Verdict == BudgetWarn {
		// Merge warnings.
		warnings := append(turn.WarningDimensions, runtime.WarningDimensions...)
		reasons := make([]string, 0, 2)
		if turn.Verdict == BudgetWarn {
			reasons = append(reasons, turn.Reason)
		}
		if runtime.Verdict == BudgetWarn {
			reasons = append(reasons, runtime.Reason)
		}
		return BudgetDecision{
			Verdict:           BudgetWarn,
			Reason:            strings.Join(reasons, "; "),
			WarningDimensions: warnings,
			Usage:             turn.Usage,
			Budget:            budget,
		}
	}

	return turn
}
