// budget_outcome.go defines what happens when budgets are exhausted:
// structured reasons, policy-driven next actions (escalate, replan,
// fallback, fail), and integration with autonomy mode.
package planner

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"metiq/internal/store/state"
)

// ── Exhaustion reason ──────────────────────────────────────────────────────────

// ExhaustionReason categorises why a budget was exhausted.
type ExhaustionReason string

const (
	ExhaustionTokens     ExhaustionReason = "tokens_exhausted"
	ExhaustionRuntime    ExhaustionReason = "runtime_exhausted"
	ExhaustionCost       ExhaustionReason = "cost_exhausted"
	ExhaustionToolCalls  ExhaustionReason = "tool_calls_exhausted"
	ExhaustionDelegation ExhaustionReason = "delegations_exhausted"
)

// ExhaustionAction is the policy-driven next step after budget exhaustion.
type ExhaustionAction string

const (
	// ActionFail terminates the task with a budget failure status.
	ActionFail ExhaustionAction = "fail"
	// ActionEscalate pauses the task and escalates to an operator.
	ActionEscalate ExhaustionAction = "escalate"
	// ActionReplan triggers a replanning attempt with reduced scope.
	ActionReplan ExhaustionAction = "replan"
	// ActionFallback switches to a cheaper model or reduced capability.
	ActionFallback ExhaustionAction = "fallback"
	// ActionBlock pauses the task waiting for budget increase.
	ActionBlock ExhaustionAction = "block"
)

// ── Exhaustion event ───────────────────────────────────────────────────────────

// ExhaustionEvent is the structured record of a budget exhaustion occurrence.
type ExhaustionEvent struct {
	// EventID uniquely identifies this exhaustion event.
	EventID string `json:"event_id"`
	// TaskID identifies the affected task.
	TaskID string `json:"task_id"`
	// RunID identifies the affected run.
	RunID string `json:"run_id"`
	// Reasons lists all dimensions that were exhausted.
	Reasons []ExhaustionReason `json:"reasons"`
	// Action is the policy-driven outcome.
	Action ExhaustionAction `json:"action"`
	// ActionReason explains why this action was chosen.
	ActionReason string `json:"action_reason"`
	// Usage is the cumulative usage at exhaustion time.
	Usage state.TaskUsage `json:"usage"`
	// Budget is the budget that was exceeded.
	Budget state.TaskBudget `json:"budget"`
	// AutonomyMode is the effective mode at exhaustion time.
	AutonomyMode state.AutonomyMode `json:"autonomy_mode,omitempty"`
	// CreatedAt is the Unix timestamp of the event.
	CreatedAt int64 `json:"created_at"`
	// Meta holds optional structured metadata.
	Meta map[string]any `json:"meta,omitempty"`
}

// ── Exhaustion policy ──────────────────────────────────────────────────────────

// ExhaustionPolicy defines the action to take for each exhaustion reason
// under each autonomy mode.
type ExhaustionPolicy struct {
	rules map[exhaustionKey]ExhaustionAction
}

type exhaustionKey struct {
	Reason ExhaustionReason
	Mode   state.AutonomyMode
}

// NewExhaustionPolicy creates a policy from explicit rules.
func NewExhaustionPolicy(rules map[exhaustionKey]ExhaustionAction) *ExhaustionPolicy {
	cp := make(map[exhaustionKey]ExhaustionAction, len(rules))
	for k, v := range rules {
		cp[k] = v
	}
	return &ExhaustionPolicy{rules: cp}
}

// Lookup returns the action for the given reason and mode.
func (p *ExhaustionPolicy) Lookup(reason ExhaustionReason, mode state.AutonomyMode) (ExhaustionAction, bool) {
	a, ok := p.rules[exhaustionKey{Reason: reason, Mode: mode}]
	return a, ok
}

// DefaultExhaustionPolicy returns the standard exhaustion policy.
//
// Design principles:
//   - Full autonomy: try replan/fallback before failing
//   - Plan/step approval: escalate to operator before failing
//   - Supervised: always escalate
//   - Cost exhaustion always escalates (financial implications)
//   - Delegation exhaustion → replan (try different approach)
func DefaultExhaustionPolicy() *ExhaustionPolicy {
	rules := map[exhaustionKey]ExhaustionAction{}

	modes := []state.AutonomyMode{
		state.AutonomyFull,
		state.AutonomyPlanApproval,
		state.AutonomyStepApproval,
		state.AutonomySupervised,
	}
	reasons := []ExhaustionReason{
		ExhaustionTokens,
		ExhaustionRuntime,
		ExhaustionCost,
		ExhaustionToolCalls,
		ExhaustionDelegation,
	}

	for _, mode := range modes {
		for _, reason := range reasons {
			rules[exhaustionKey{reason, mode}] = resolveDefaultExhaustionAction(reason, mode)
		}
	}

	return NewExhaustionPolicy(rules)
}

func resolveDefaultExhaustionAction(reason ExhaustionReason, mode state.AutonomyMode) ExhaustionAction {
	// Cost exhaustion always escalates — financial implications.
	if reason == ExhaustionCost {
		return ActionEscalate
	}

	switch mode {
	case state.AutonomyFull:
		return fullModeExhaustion(reason)
	case state.AutonomyPlanApproval:
		return planApprovalExhaustion(reason)
	case state.AutonomyStepApproval:
		return stepApprovalExhaustion(reason)
	case state.AutonomySupervised:
		return ActionEscalate // supervised always escalates
	default:
		return ActionEscalate
	}
}

func fullModeExhaustion(reason ExhaustionReason) ExhaustionAction {
	switch reason {
	case ExhaustionTokens:
		return ActionFallback // try cheaper model
	case ExhaustionRuntime:
		return ActionFail // can't recover from time exhaustion
	case ExhaustionToolCalls:
		return ActionReplan // try different approach
	case ExhaustionDelegation:
		return ActionReplan // try doing it locally
	default:
		return ActionFail
	}
}

func planApprovalExhaustion(reason ExhaustionReason) ExhaustionAction {
	switch reason {
	case ExhaustionTokens:
		return ActionEscalate // ask operator for more budget
	case ExhaustionRuntime:
		return ActionEscalate
	case ExhaustionToolCalls:
		return ActionReplan
	case ExhaustionDelegation:
		return ActionEscalate
	default:
		return ActionEscalate
	}
}

func stepApprovalExhaustion(reason ExhaustionReason) ExhaustionAction {
	// Step approval mode → escalate most things, replan delegation.
	if reason == ExhaustionDelegation {
		return ActionReplan
	}
	return ActionEscalate
}

// ── Outcome resolver ───────────────────────────────────────────────────────────

// OutcomeResolver evaluates exhausted budgets and produces structured
// exhaustion events with policy-driven actions.
// It is safe for concurrent use.
type OutcomeResolver struct {
	policy *ExhaustionPolicy
	nextID atomic.Int64
}

// NewOutcomeResolver creates a resolver with the given policy.
// A nil policy falls back to DefaultExhaustionPolicy().
func NewOutcomeResolver(policy *ExhaustionPolicy) *OutcomeResolver {
	if policy == nil {
		policy = DefaultExhaustionPolicy()
	}
	return &OutcomeResolver{policy: policy}
}

// ResolveOutcome evaluates a budget decision and produces an exhaustion
// event when the decision is a block. Returns nil if not exhausted.
func (r *OutcomeResolver) ResolveOutcome(
	decision BudgetDecision,
	taskID, runID string,
	mode state.AutonomyMode,
	now int64,
) *ExhaustionEvent {
	if decision.Verdict != BudgetBlock {
		return nil
	}

	if now == 0 {
		now = time.Now().Unix()
	}

	reasons := classifyExhaustionReasons(decision)

	// Pick the most important reason for action selection.
	// Cost > tokens > runtime > tools > delegation.
	primaryReason := selectPrimaryReason(reasons)

	action, ok := r.policy.Lookup(primaryReason, mode)
	if !ok {
		action = ActionFail // fallback
	}

	id := r.nextID.Add(1)
	return &ExhaustionEvent{
		EventID:      fmt.Sprintf("exhaust-%d", id),
		TaskID:       taskID,
		RunID:        runID,
		Reasons:      reasons,
		Action:       action,
		ActionReason: buildActionReason(action, primaryReason, mode),
		Usage:        decision.Usage,
		Budget:       decision.Budget,
		AutonomyMode: mode,
		CreatedAt:    now,
	}
}

// classifyExhaustionReasons maps exceeded dimensions to reason enums.
func classifyExhaustionReasons(decision BudgetDecision) []ExhaustionReason {
	var reasons []ExhaustionReason
	for _, dim := range decision.ExceededDimensions {
		switch dim {
		case "total_tokens", "prompt_tokens", "completion_tokens":
			if !containsReason(reasons, ExhaustionTokens) {
				reasons = append(reasons, ExhaustionTokens)
			}
		case "runtime_ms":
			reasons = append(reasons, ExhaustionRuntime)
		case "cost_micros_usd":
			reasons = append(reasons, ExhaustionCost)
		case "tool_calls":
			reasons = append(reasons, ExhaustionToolCalls)
		case "delegations":
			reasons = append(reasons, ExhaustionDelegation)
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, ExhaustionTokens) // safe default
	}
	return reasons
}

func containsReason(reasons []ExhaustionReason, r ExhaustionReason) bool {
	for _, existing := range reasons {
		if existing == r {
			return true
		}
	}
	return false
}

// selectPrimaryReason picks the highest-priority reason.
// Priority: cost > tokens > runtime > tools > delegation.
var reasonPriority = map[ExhaustionReason]int{
	ExhaustionCost:       5,
	ExhaustionTokens:     4,
	ExhaustionRuntime:    3,
	ExhaustionToolCalls:  2,
	ExhaustionDelegation: 1,
}

func selectPrimaryReason(reasons []ExhaustionReason) ExhaustionReason {
	if len(reasons) == 0 {
		return ExhaustionTokens
	}
	best := reasons[0]
	bestPri := reasonPriority[best]
	for _, r := range reasons[1:] {
		if reasonPriority[r] > bestPri {
			best = r
			bestPri = reasonPriority[r]
		}
	}
	return best
}

func buildActionReason(action ExhaustionAction, reason ExhaustionReason, mode state.AutonomyMode) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("reason=%s", reason))
	parts = append(parts, fmt.Sprintf("mode=%s", mode))

	switch action {
	case ActionFail:
		return fmt.Sprintf("budget failure: %s (unrecoverable under %s)", strings.Join(parts, ", "), mode)
	case ActionEscalate:
		return fmt.Sprintf("escalating to operator: %s", strings.Join(parts, ", "))
	case ActionReplan:
		return fmt.Sprintf("triggering replan: %s", strings.Join(parts, ", "))
	case ActionFallback:
		return fmt.Sprintf("switching to fallback: %s", strings.Join(parts, ", "))
	case ActionBlock:
		return fmt.Sprintf("blocking for budget increase: %s", strings.Join(parts, ", "))
	default:
		return fmt.Sprintf("unknown action: %s", strings.Join(parts, ", "))
	}
}

// ── Convenience helpers ────────────────────────────────────────────────────────

// IsBudgetFailure reports whether a task error string indicates a budget
// exhaustion (vs. a provider or runtime error). Useful for operators
// inspecting task run results.
func IsBudgetFailure(errStr string) bool {
	lower := strings.ToLower(errStr)
	return strings.Contains(lower, "budget") &&
		(strings.Contains(lower, "exhaust") || strings.Contains(lower, "exceed"))
}

// FormatExhaustionEvent returns a human-readable summary of an exhaustion event.
func FormatExhaustionEvent(event ExhaustionEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Budget exhaustion [%s]\n", event.EventID)
	fmt.Fprintf(&b, "  Task: %s  Run: %s\n", event.TaskID, event.RunID)
	fmt.Fprintf(&b, "  Reasons: %v\n", event.Reasons)
	fmt.Fprintf(&b, "  Action: %s\n", event.Action)
	fmt.Fprintf(&b, "  %s\n", event.ActionReason)
	fmt.Fprintf(&b, "  Mode: %s\n", event.AutonomyMode)
	return b.String()
}
