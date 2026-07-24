package permissions

import "testing"

func TestResolveEvaluationCanonicalPrecedence(t *testing.T) {
	rules := []EvaluationRule{
		{ID: "global-deny", Behavior: BehaviorDeny, ScopePrecedence: ScopeGlobal.Precedence()},
		{ID: "session-allow", Behavior: BehaviorAllow, ScopePrecedence: ScopeSession.Precedence()},
		{ID: "session-ask", Behavior: BehaviorAsk, ScopePrecedence: ScopeSession.Precedence()},
	}
	got := ResolveEvaluation(BehaviorAllow, rules)
	if !got.Matched || got.Behavior != BehaviorAsk || got.Winner != 2 {
		t.Fatalf("scope/action precedence = %+v, want session ask", got)
	}
	if len(got.Order) != 3 || got.Order[0] != 2 || got.Order[1] != 1 || got.Order[2] != 0 {
		t.Fatalf("order = %v, want [2 1 0]", got.Order)
	}
}

func TestResolveEvaluationImmutableDenyAndStableTieBreak(t *testing.T) {
	rules := []EvaluationRule{
		{ID: "z-allow", Behavior: BehaviorAllow, ScopePrecedence: ScopeSession.Precedence(), Specificity: 2},
		{ID: "b-deny", Behavior: BehaviorDeny, ScopePrecedence: ScopeGlobal.Precedence(), Immutable: true},
		{ID: "a-deny", Behavior: BehaviorDeny, ScopePrecedence: ScopeGlobal.Precedence(), Immutable: true},
	}
	got := ResolveEvaluation(BehaviorAsk, rules)
	if !got.ImmutableDeny || got.Behavior != BehaviorDeny || got.Winner != 2 {
		t.Fatalf("immutable resolution = %+v, want stable a-deny winner", got)
	}
}

func TestResolveEvaluationUsesDefaultWhenNoValidRules(t *testing.T) {
	got := ResolveEvaluation(BehaviorAsk, []EvaluationRule{{ID: "invalid", Behavior: "prompt"}})
	if got.Matched || got.Behavior != BehaviorAsk || got.Winner != -1 {
		t.Fatalf("default resolution = %+v", got)
	}
}
