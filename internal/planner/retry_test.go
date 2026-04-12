package planner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ── Failure classification ──────────────────────────────────────────────────

func TestClassifyFailure_Transient(t *testing.T) {
	cases := []string{
		"connection reset",
		"timeout waiting for response",
		"ECONNREFUSED",
		"some random error",
	}
	for _, errStr := range cases {
		if got := ClassifyFailure(errStr); got != FailureTransient {
			t.Errorf("ClassifyFailure(%q) = %s, want transient", errStr, got)
		}
	}
}

func TestClassifyFailure_Provider(t *testing.T) {
	cases := []string{
		"model overloaded, try again later",
		"rate limit exceeded",
		"quota exceeded for API key",
		"model not available",
		"service unavailable",
		"capacity reached",
		"request throttled",
		"HTTP 503",
	}
	for _, errStr := range cases {
		if got := ClassifyFailure(errStr); got != FailureProvider {
			t.Errorf("ClassifyFailure(%q) = %s, want provider", errStr, got)
		}
	}
}

func TestClassifyFailure_Permanent(t *testing.T) {
	cases := []string{
		"unauthorized: invalid API key",
		"forbidden: access denied",
		"invalid tool parameters",
		"resource not found",
		"permission denied for this action",
		"operation not allowed",
		"bad request: missing field",
		"unsupported model",
		"validation failed: name required",
	}
	for _, errStr := range cases {
		if got := ClassifyFailure(errStr); got != FailurePermanent {
			t.Errorf("ClassifyFailure(%q) = %s, want permanent", errStr, got)
		}
	}
}

func TestClassifyFailure_Budget(t *testing.T) {
	cases := []string{
		"budget exhausted: tokens exceeded",
		"budget exceeded: cost limit",
	}
	for _, errStr := range cases {
		if got := ClassifyFailure(errStr); got != FailureBudget {
			t.Errorf("ClassifyFailure(%q) = %s, want budget", errStr, got)
		}
	}
}

func TestClassifyFailure_SideEffect(t *testing.T) {
	cases := []string{
		"side effect uncertain: network error after publish",
		"partial execution: zap sent but confirmation lost",
	}
	for _, errStr := range cases {
		if got := ClassifyFailure(errStr); got != FailureSideEffect {
			t.Errorf("ClassifyFailure(%q) = %s, want side_effect", errStr, got)
		}
	}
}

func TestValidFailureClass_Known(t *testing.T) {
	for _, c := range []FailureClass{FailureTransient, FailureProvider, FailurePermanent, FailureBudget, FailureSideEffect} {
		if !ValidFailureClass(c) {
			t.Errorf("expected %q to be valid", c)
		}
	}
}

func TestValidFailureClass_Unknown(t *testing.T) {
	if ValidFailureClass("bogus") {
		t.Fatal("expected bogus to be invalid")
	}
}

// ── RetryPolicy presets ─────────────────────────────────────────────────────

func TestDefaultRetryPolicy_Values(t *testing.T) {
	p := DefaultRetryPolicy()
	if p.MaxAttempts[FailureTransient] != 3 {
		t.Fatalf("expected 3 transient attempts, got %d", p.MaxAttempts[FailureTransient])
	}
	if p.MaxAttempts[FailurePermanent] != 0 {
		t.Fatalf("expected 0 permanent attempts, got %d", p.MaxAttempts[FailurePermanent])
	}
	if p.AllowSideEffectRetry {
		t.Fatal("expected side-effect retry disabled by default")
	}
}

func TestAggressiveRetryPolicy_MoreAttempts(t *testing.T) {
	p := AggressiveRetryPolicy()
	if p.MaxAttempts[FailureTransient] != 5 {
		t.Fatalf("expected 5, got %d", p.MaxAttempts[FailureTransient])
	}
	if p.MaxAttempts[FailureProvider] != 3 {
		t.Fatalf("expected 3, got %d", p.MaxAttempts[FailureProvider])
	}
}

func TestConservativeRetryPolicy_FewerAttempts(t *testing.T) {
	p := ConservativeRetryPolicy()
	if p.MaxAttempts[FailureTransient] != 1 {
		t.Fatalf("expected 1, got %d", p.MaxAttempts[FailureTransient])
	}
}

// ── RetryEngine.Evaluate ────────────────────────────────────────────────────

func TestEvaluate_TransientFirstAttempt(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	dec := engine.Evaluate("connection timeout", 1, "nostr_fetch")
	if !dec.ShouldRetry() {
		t.Fatalf("expected retry, got %s: %s", dec.Action, dec.Reason)
	}
	if dec.BackoffDelay != 1*time.Second {
		t.Fatalf("expected 1s backoff, got %s", dec.BackoffDelay)
	}
}

func TestEvaluate_TransientMaxAttempts(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	dec := engine.Evaluate("connection timeout", 3, "nostr_fetch")
	if dec.ShouldRetry() {
		t.Fatal("expected no more retries at max attempts")
	}
	if dec.Action != RetryActionFail {
		t.Fatalf("expected fail at max attempts, got %s", dec.Action)
	}
}

func TestEvaluate_ProviderFallback(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	dec := engine.Evaluate("model overloaded", 2, "nostr_fetch")
	if dec.Action != RetryActionFallback {
		t.Fatalf("expected fallback for exhausted provider attempts, got %s", dec.Action)
	}
}

func TestEvaluate_ProviderFirstAttempt(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	dec := engine.Evaluate("rate limit exceeded", 1, "nostr_fetch")
	if !dec.ShouldRetry() {
		t.Fatalf("expected retry on first provider attempt, got %s", dec.Action)
	}
}

func TestEvaluate_PermanentNeverRetries(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	dec := engine.Evaluate("invalid parameters", 1, "nostr_fetch")
	if dec.Action != RetryActionFail {
		t.Fatalf("expected fail for permanent, got %s", dec.Action)
	}
}

func TestEvaluate_BudgetEscalates(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	dec := engine.Evaluate("budget exhausted", 1, "nostr_fetch")
	if dec.Action != RetryActionEscalate {
		t.Fatalf("expected escalate for budget, got %s", dec.Action)
	}
}

func TestEvaluate_SideEffectSkips(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	dec := engine.Evaluate("side effect uncertain", 1, "nostr_publish")
	if dec.Action != RetryActionSkip {
		t.Fatalf("expected skip for side-effect failure, got %s", dec.Action)
	}
}

func TestEvaluate_SideEffectfulToolEscalatesOnTransient(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	dec := engine.Evaluate("connection timeout", 1, "nostr_publish")
	if dec.Action != RetryActionEscalate {
		t.Fatalf("expected escalate for side-effectful tool transient failure, got %s", dec.Action)
	}
	if !strings.Contains(dec.Reason, "side-effectful") {
		t.Fatalf("expected side-effectful in reason, got %s", dec.Reason)
	}
}

func TestEvaluate_SafeToolAllowsRetry(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	// nostr_fetch is pure → safe to retry
	dec := engine.Evaluate("connection timeout", 1, "nostr_fetch")
	if !dec.ShouldRetry() {
		t.Fatal("expected retry for safe tool")
	}
	if !dec.SideEffectSafe {
		t.Fatal("expected side_effect_safe=true for pure tool")
	}
}

func TestEvaluate_RetryableToolAllowsRetry(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	dec := engine.Evaluate("connection timeout", 1, "update_profile")
	if !dec.ShouldRetry() {
		t.Fatal("expected retry for retryable tool")
	}
}

// ── Backoff computation ─────────────────────────────────────────────────────

func TestBackoff_Exponential(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	b1 := engine.computeBackoff(1)
	b2 := engine.computeBackoff(2)
	b3 := engine.computeBackoff(3)

	if b1 != 1*time.Second {
		t.Fatalf("expected 1s for attempt 1, got %s", b1)
	}
	if b2 != 2*time.Second {
		t.Fatalf("expected 2s for attempt 2, got %s", b2)
	}
	if b3 != 4*time.Second {
		t.Fatalf("expected 4s for attempt 3, got %s", b3)
	}
}

func TestBackoff_CappedAtMax(t *testing.T) {
	policy := DefaultRetryPolicy()
	policy.MaxBackoff = 5 * time.Second
	engine := NewRetryEngine(policy)

	b := engine.computeBackoff(10)
	if b > 5*time.Second {
		t.Fatalf("expected cap at 5s, got %s", b)
	}
}

func TestBackoff_ZeroMultiplierDefaults(t *testing.T) {
	policy := DefaultRetryPolicy()
	policy.BackoffMultiplier = 0
	engine := NewRetryEngine(policy)

	b := engine.computeBackoff(2)
	// Should use default multiplier of 2
	if b != 2*time.Second {
		t.Fatalf("expected 2s with default multiplier, got %s", b)
	}
}

// ── RetryDecision.ShouldRetry ───────────────────────────────────────────────

func TestRetryDecision_ShouldRetry(t *testing.T) {
	tests := []struct {
		action RetryAction
		want   bool
	}{
		{RetryActionRetry, true},
		{RetryActionFallback, false},
		{RetryActionEscalate, false},
		{RetryActionFail, false},
		{RetryActionSkip, false},
	}
	for _, tt := range tests {
		d := RetryDecision{Action: tt.action}
		if got := d.ShouldRetry(); got != tt.want {
			t.Errorf("RetryDecision{Action: %s}.ShouldRetry() = %v, want %v", tt.action, got, tt.want)
		}
	}
}

// ── FormatRetryDecision ─────────────────────────────────────────────────────

func TestFormatRetryDecision_AllActions(t *testing.T) {
	tests := []struct {
		action RetryAction
		marker string
	}{
		{RetryActionRetry, "🔄 RETRY"},
		{RetryActionFallback, "🔀 FALLBACK"},
		{RetryActionEscalate, "⬆️  ESCALATE"},
		{RetryActionFail, "❌ FAIL"},
		{RetryActionSkip, "⏭️  SKIP"},
	}
	for _, tt := range tests {
		out := FormatRetryDecision(RetryDecision{
			Action: tt.action, Reason: "test", FailureClass: FailureTransient,
		})
		if !strings.Contains(out, tt.marker) {
			t.Errorf("FormatRetryDecision(%s) missing %s in: %s", tt.action, tt.marker, out)
		}
	}
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestRetryDecision_JSONRoundTrip(t *testing.T) {
	d := RetryDecision{
		Action:         RetryActionRetry,
		Reason:         "retrying",
		BackoffDelay:   2 * time.Second,
		AttemptsUsed:   1,
		AttemptsMax:    3,
		FailureClass:   FailureTransient,
		SideEffectSafe: true,
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RetryDecision
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Action != d.Action || decoded.FailureClass != d.FailureClass {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
}

// ── End-to-end: retry escalation chain ──────────────────────────────────────

func TestEndToEnd_TransientRetryChain(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	tool := "nostr_fetch" // pure, safe to retry

	// Attempt 1: retry
	d1 := engine.Evaluate("timeout", 1, tool)
	if d1.Action != RetryActionRetry {
		t.Fatalf("attempt 1: expected retry, got %s", d1.Action)
	}

	// Attempt 2: retry
	d2 := engine.Evaluate("timeout", 2, tool)
	if d2.Action != RetryActionRetry {
		t.Fatalf("attempt 2: expected retry, got %s", d2.Action)
	}
	if d2.BackoffDelay <= d1.BackoffDelay {
		t.Fatalf("expected increasing backoff: %s <= %s", d2.BackoffDelay, d1.BackoffDelay)
	}

	// Attempt 3: exhausted → fail
	d3 := engine.Evaluate("timeout", 3, tool)
	if d3.Action != RetryActionFail {
		t.Fatalf("attempt 3: expected fail, got %s", d3.Action)
	}
}

func TestEndToEnd_ProviderFallbackChain(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	tool := "relay_info" // pure

	// Attempt 1: retry
	d1 := engine.Evaluate("model overloaded", 1, tool)
	if d1.Action != RetryActionRetry {
		t.Fatalf("attempt 1: expected retry, got %s", d1.Action)
	}

	// Attempt 2: fallback
	d2 := engine.Evaluate("model overloaded", 2, tool)
	if d2.Action != RetryActionFallback {
		t.Fatalf("attempt 2: expected fallback, got %s", d2.Action)
	}
}

func TestEndToEnd_SideEffectfulNeverRetries(t *testing.T) {
	engine := NewRetryEngine(DefaultRetryPolicy())
	tool := "nostr_zap_send" // side-effectful

	// Transient failure on side-effectful tool → escalate (not retry)
	d := engine.Evaluate("connection reset", 1, tool)
	if d.Action != RetryActionEscalate {
		t.Fatalf("expected escalate for side-effectful tool, got %s", d.Action)
	}

	// Side-effect uncertainty → skip
	d2 := engine.Evaluate("side effect uncertain", 1, tool)
	if d2.Action != RetryActionSkip {
		t.Fatalf("expected skip for side-effect uncertainty, got %s", d2.Action)
	}
}
