package permissions

import "sort"

// EvaluationRule is the common, already-matched rule representation used by
// every tool authorization surface. Matching remains the responsibility of the
// public API adapter because permissions rules support content/category/scope
// fields while ToolPolicy rules support groups and origins.
type EvaluationRule struct {
	ID              string
	Behavior        Behavior
	ScopePrecedence int
	Specificity     int
	Immutable       bool
}

// EvaluationResult describes the canonical ordering and winning rule. Order
// contains indexes into the rules passed to ResolveEvaluation.
type EvaluationResult struct {
	Behavior      Behavior
	Winner        int
	Order         []int
	Matched       bool
	ImmutableDeny bool
}

// ResolveEvaluation applies the single precedence algorithm used for tool
// authorization decisions:
//
//  1. immutable deny rules are non-overridable;
//  2. otherwise the highest-precedence scope wins;
//  3. within a scope, deny > ask > allow;
//  4. equally restrictive rules prefer the more specific rule, then stable ID.
//
// Adapters that do not expose scopes assign the same ScopePrecedence to every
// rule, which intentionally gives them the same deny > ask > allow behavior.
func ResolveEvaluation(defaultBehavior Behavior, rules []EvaluationRule) EvaluationResult {
	result := EvaluationResult{Behavior: defaultBehavior, Winner: -1}
	if len(rules) == 0 {
		return result
	}

	order := make([]int, 0, len(rules))
	for i, rule := range rules {
		if rule.Behavior.IsValid() {
			order = append(order, i)
		}
	}
	if len(order) == 0 {
		return result
	}

	sort.SliceStable(order, func(i, j int) bool {
		left, right := rules[order[i]], rules[order[j]]
		leftImmutableDeny := left.Immutable && left.Behavior == BehaviorDeny
		rightImmutableDeny := right.Immutable && right.Behavior == BehaviorDeny
		if leftImmutableDeny != rightImmutableDeny {
			return leftImmutableDeny
		}
		if left.ScopePrecedence != right.ScopePrecedence {
			return left.ScopePrecedence > right.ScopePrecedence
		}
		if left.Behavior.Priority() != right.Behavior.Priority() {
			return left.Behavior.Priority() > right.Behavior.Priority()
		}
		if left.Specificity != right.Specificity {
			return left.Specificity > right.Specificity
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return order[i] < order[j]
	})

	winner := order[0]
	winningRule := rules[winner]
	result.Behavior = winningRule.Behavior
	result.Winner = winner
	result.Order = order
	result.Matched = true
	result.ImmutableDeny = winningRule.Immutable && winningRule.Behavior == BehaviorDeny
	return result
}
