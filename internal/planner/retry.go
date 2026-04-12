package planner

import (
	"fmt"
	"strings"
	"time"
)

// ── Failure classification ───────────────────────────────────────────────────

// FailureClass categorizes a failure for retry decisions.
type FailureClass string

const (
	// FailureTransient indicates a temporary failure that may succeed on retry
	// (e.g., network timeout, rate limit, temporary server error).
	FailureTransient FailureClass = "transient"
	// FailureProvider indicates a provider-level failure (e.g., model overloaded,
	// API key quota exceeded).
	FailureProvider FailureClass = "provider"
	// FailurePermanent indicates a non-recoverable failure that should not be retried
	// (e.g., invalid input, authorization denied, business logic error).
	FailurePermanent FailureClass = "permanent"
	// FailureBudget indicates a budget exhaustion (tokens, cost, runtime).
	FailureBudget FailureClass = "budget"
	// FailureSideEffect indicates a failure during a side-effectful operation
	// where the outcome is uncertain (e.g., network error after sending a zap).
	FailureSideEffect FailureClass = "side_effect"
)

// validFailureClasses for validation.
var validFailureClasses = map[FailureClass]bool{
	FailureTransient:  true,
	FailureProvider:   true,
	FailurePermanent:  true,
	FailureBudget:     true,
	FailureSideEffect: true,
}

// ValidFailureClass reports whether c is a recognized failure class.
func ValidFailureClass(c FailureClass) bool {
	return validFailureClasses[c]
}

// ClassifyFailure determines the failure class from an error string using
// keyword heuristics. Unknown failures default to transient.
func ClassifyFailure(errStr string) FailureClass {
	lower := strings.ToLower(errStr)

	// Budget exhaustion.
	if IsBudgetFailure(errStr) {
		return FailureBudget
	}

	// Permanent failures.
	for _, keyword := range []string{
		"unauthorized", "forbidden", "invalid", "not found",
		"permission denied", "not allowed", "bad request",
		"unsupported", "unrecognized", "validation failed",
	} {
		if strings.Contains(lower, keyword) {
			return FailurePermanent
		}
	}

	// Provider failures.
	for _, keyword := range []string{
		"overloaded", "quota exceeded", "rate limit",
		"model not available", "capacity", "throttl",
		"service unavailable", "503",
	} {
		if strings.Contains(lower, keyword) {
			return FailureProvider
		}
	}

	// Side-effect uncertainty.
	for _, keyword := range []string{
		"side effect uncertain", "partial execution",
		"idempotency conflict", "duplicate",
	} {
		if strings.Contains(lower, keyword) {
			return FailureSideEffect
		}
	}

	// Default: transient (network errors, timeouts, etc.)
	return FailureTransient
}

// ── Retry policy ─────────────────────────────────────────────────────────────

// RetryAction describes what should happen after a failure.
type RetryAction string

const (
	// RetryActionRetry means the operation should be retried after backoff.
	RetryActionRetry RetryAction = "retry"
	// RetryActionFallback means the operation should use an alternative
	// (e.g., different provider, degraded mode).
	RetryActionFallback RetryAction = "fallback"
	// RetryActionEscalate means the failure should be escalated to a human.
	RetryActionEscalate RetryAction = "escalate"
	// RetryActionFail means the operation should be marked as permanently failed.
	RetryActionFail RetryAction = "fail"
	// RetryActionSkip means the operation should be skipped (used for
	// side-effectful operations where the outcome is uncertain).
	RetryActionSkip RetryAction = "skip"
)

// RetryDecision describes the result of evaluating a failure against the retry policy.
type RetryDecision struct {
	Action        RetryAction     `json:"action"`
	Reason        string          `json:"reason"`
	BackoffDelay  time.Duration   `json:"backoff_delay,omitempty"`
	AttemptsUsed  int             `json:"attempts_used"`
	AttemptsMax   int             `json:"attempts_max"`
	FailureClass  FailureClass    `json:"failure_class"`
	SideEffectSafe bool           `json:"side_effect_safe"`
}

// ShouldRetry reports whether the decision recommends retrying.
func (d RetryDecision) ShouldRetry() bool {
	return d.Action == RetryActionRetry
}

// RetryPolicy defines per-class retry behavior.
type RetryPolicy struct {
	// MaxAttempts per failure class. 0 means no retries allowed.
	MaxAttempts map[FailureClass]int

	// BaseBackoff is the initial backoff duration for retries.
	BaseBackoff time.Duration

	// MaxBackoff caps the exponential backoff.
	MaxBackoff time.Duration

	// BackoffMultiplier is the exponential factor (default 2).
	BackoffMultiplier float64

	// AllowSideEffectRetry controls whether side-effectful operations may be
	// retried. When false, side-effect failures always escalate.
	AllowSideEffectRetry bool
}

// DefaultRetryPolicy returns a production-suitable retry policy.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: map[FailureClass]int{
			FailureTransient:  3,
			FailureProvider:   2,
			FailurePermanent:  0,
			FailureBudget:     0,
			FailureSideEffect: 0,
		},
		BaseBackoff:          1 * time.Second,
		MaxBackoff:           30 * time.Second,
		BackoffMultiplier:    2.0,
		AllowSideEffectRetry: false,
	}
}

// AggressiveRetryPolicy allows more retries for transient/provider failures.
func AggressiveRetryPolicy() RetryPolicy {
	p := DefaultRetryPolicy()
	p.MaxAttempts[FailureTransient] = 5
	p.MaxAttempts[FailureProvider] = 3
	return p
}

// ConservativeRetryPolicy limits retries more tightly.
func ConservativeRetryPolicy() RetryPolicy {
	p := DefaultRetryPolicy()
	p.MaxAttempts[FailureTransient] = 1
	p.MaxAttempts[FailureProvider] = 1
	return p
}

// ── Retry engine ─────────────────────────────────────────────────────────────

// RetryEngine evaluates failures against a retry policy.
type RetryEngine struct {
	policy RetryPolicy
}

// NewRetryEngine creates a retry engine with the given policy.
func NewRetryEngine(policy RetryPolicy) *RetryEngine {
	return &RetryEngine{policy: policy}
}

// Evaluate determines the retry action for a failure.
//
// Parameters:
//   - errStr: the error message from the failed operation
//   - attempt: the current attempt number (1-indexed)
//   - tool: the tool that failed (used for side-effect classification)
func (e *RetryEngine) Evaluate(errStr string, attempt int, tool string) RetryDecision {
	class := ClassifyFailure(errStr)
	sideEffectSafe := IsSafeToReplay(tool)

	maxAttempts := e.policy.MaxAttempts[class]

	dec := RetryDecision{
		AttemptsUsed:   attempt,
		AttemptsMax:    maxAttempts,
		FailureClass:   class,
		SideEffectSafe: sideEffectSafe,
	}

	// Permanent and budget failures never retry.
	if class == FailurePermanent {
		dec.Action = RetryActionFail
		dec.Reason = "permanent failure; not retryable"
		return dec
	}
	if class == FailureBudget {
		dec.Action = RetryActionEscalate
		dec.Reason = "budget exhausted; escalating"
		return dec
	}

	// Side-effect failures: only retry if policy allows AND tool is safe.
	if class == FailureSideEffect {
		if !e.policy.AllowSideEffectRetry || !sideEffectSafe {
			dec.Action = RetryActionSkip
			dec.Reason = "side-effectful failure with uncertain outcome; skipping to avoid duplicate"
			return dec
		}
	}

	// For side-effectful tools with transient/provider failures,
	// block retry unless the tool is safe to replay.
	if !sideEffectSafe && (class == FailureTransient || class == FailureProvider) {
		dec.Action = RetryActionEscalate
		dec.Reason = fmt.Sprintf("tool %s is side-effectful; escalating instead of retrying", tool)
		return dec
	}

	// Check attempt budget.
	if attempt >= maxAttempts {
		if class == FailureProvider {
			dec.Action = RetryActionFallback
			dec.Reason = fmt.Sprintf("provider failure after %d/%d attempts; falling back", attempt, maxAttempts)
		} else {
			dec.Action = RetryActionFail
			dec.Reason = fmt.Sprintf("max attempts reached (%d/%d)", attempt, maxAttempts)
		}
		return dec
	}

	// Allow retry with backoff.
	dec.Action = RetryActionRetry
	dec.BackoffDelay = e.computeBackoff(attempt)
	dec.Reason = fmt.Sprintf("retrying attempt %d/%d after %s", attempt+1, maxAttempts, dec.BackoffDelay)
	return dec
}

// computeBackoff calculates exponential backoff for the given attempt.
func (e *RetryEngine) computeBackoff(attempt int) time.Duration {
	multiplier := e.policy.BackoffMultiplier
	if multiplier <= 0 {
		multiplier = 2.0
	}
	backoff := e.policy.BaseBackoff
	for i := 1; i < attempt; i++ {
		backoff = time.Duration(float64(backoff) * multiplier)
		if backoff > e.policy.MaxBackoff {
			backoff = e.policy.MaxBackoff
			break
		}
	}
	return backoff
}

// ── Formatting ───────────────────────────────────────────────────────────────

// FormatRetryDecision returns a human-readable description.
func FormatRetryDecision(d RetryDecision) string {
	var b strings.Builder
	switch d.Action {
	case RetryActionRetry:
		fmt.Fprintf(&b, "🔄 RETRY: %s", d.Reason)
	case RetryActionFallback:
		fmt.Fprintf(&b, "🔀 FALLBACK: %s", d.Reason)
	case RetryActionEscalate:
		fmt.Fprintf(&b, "⬆️  ESCALATE: %s", d.Reason)
	case RetryActionFail:
		fmt.Fprintf(&b, "❌ FAIL: %s", d.Reason)
	case RetryActionSkip:
		fmt.Fprintf(&b, "⏭️  SKIP: %s", d.Reason)
	}
	fmt.Fprintf(&b, " [class=%s attempts=%d/%d safe=%v]", d.FailureClass, d.AttemptsUsed, d.AttemptsMax, d.SideEffectSafe)
	return b.String()
}
