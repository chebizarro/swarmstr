// verification_gate.go connects verification policy to runtime behavior.
// It gates risky tool actions and final task outputs based on the
// verification spec, autonomy mode, and risk class. Verification failures
// produce escalation/blocked/replan outcomes rather than silent success.
package planner

import (
	"context"
	"fmt"
	"strings"

	"metiq/internal/store/state"
)

// ── Gate outcomes ───────────────────────────────────────────────────────────

// GateDecision describes the outcome of a verification gate check.
type GateDecision string

const (
	// GateAllow permits the action to proceed.
	GateAllow GateDecision = "allow"
	// GateBlock prevents the action. The task should be blocked until
	// verification passes or an operator intervenes.
	GateBlock GateDecision = "block"
	// GateEscalate prevents the action and requests escalation to a
	// higher authority or operator.
	GateEscalate GateDecision = "escalate"
	// GateReplan prevents the action and suggests replanning.
	GateReplan GateDecision = "replan"
)

// GateResult is the output of a verification gate check.
type GateResult struct {
	Decision     GateDecision           `json:"decision"`
	Reason       string                 `json:"reason"`
	FailedChecks []string               `json:"failed_checks,omitempty"`
	Suggestion   string                 `json:"suggestion,omitempty"`
	UpdatedSpec  state.VerificationSpec `json:"updated_spec,omitempty"`
}

// Allowed reports whether the gate permits proceeding.
func (g GateResult) Allowed() bool { return g.Decision == GateAllow }

// ── Action risk classification ──────────────────────────────────────────────

// ActionRisk classifies an action's risk level for gating purposes.
type ActionRisk string

const (
	ActionRiskNone     ActionRisk = "none"
	ActionRiskLow      ActionRisk = "low"
	ActionRiskMedium   ActionRisk = "medium"
	ActionRiskHigh     ActionRisk = "high"
	ActionRiskCritical ActionRisk = "critical"
)

// ToolRiskClassifier determines the risk level of a tool call.
// Implementations can use tool name, parameters, or side-effect class.
type ToolRiskClassifier func(toolName string, params map[string]any) ActionRisk

// knownToolRisks maps specific tool names to their risk level. Explicit
// entries override the substring heuristic fallback.
var knownToolRisks = map[string]ActionRisk{
	// High risk: external side effects.
	"nostr_publish":  ActionRiskHigh,
	"nostr_send_dm":  ActionRiskHigh,
	"nostr_zap_send": ActionRiskHigh,
	// Medium risk: local state changes.
	"config_set":     ActionRiskMedium,
	"config_unset":   ActionRiskMedium,
	"memory_compact": ActionRiskMedium,
	// Low risk: read-only.
	"nostr_fetch":         ActionRiskLow,
	"nostr_profile":       ActionRiskLow,
	"nostr_followers":     ActionRiskLow,
	"nostr_follows":       ActionRiskLow,
	"relay_info":          ActionRiskLow,
	"relay_list":          ActionRiskLow,
	"relay_ping":          ActionRiskLow,
	"memory_search":       ActionRiskLow,
	"nostr_resolve_nip05": ActionRiskLow,
	"nostr_relay_hints":   ActionRiskLow,
	"nostr_wot_distance":  ActionRiskLow,
	"nostr_watch_list":    ActionRiskLow,
}

// DefaultToolRiskClassifier checks the explicit registry first, then falls
// back to substring heuristics for unknown tools.
func DefaultToolRiskClassifier(toolName string, _ map[string]any) ActionRisk {
	if risk, ok := knownToolRisks[toolName]; ok {
		return risk
	}

	lower := strings.ToLower(toolName)

	// Substring fallback for unknown tools.
	for _, pattern := range []string{"publish", "send", "zap", "delete"} {
		if strings.Contains(lower, pattern) {
			return ActionRiskHigh
		}
	}
	for _, pattern := range []string{"set", "config", "unset", "update", "write", "create"} {
		if strings.Contains(lower, pattern) {
			return ActionRiskMedium
		}
	}
	for _, pattern := range []string{"fetch", "get", "list", "search", "read", "info"} {
		if strings.Contains(lower, pattern) {
			return ActionRiskLow
		}
	}

	return ActionRiskMedium // unknown defaults to medium
}

// ── Verification gate ───────────────────────────────────────────────────────

// VerificationGate gates actions and outputs based on verification policy.
type VerificationGate struct {
	runtime    *VerifierRuntime
	classifier ToolRiskClassifier
}

// NewVerificationGate creates a gate backed by a verifier runtime.
// If classifier is nil, DefaultToolRiskClassifier is used.
func NewVerificationGate(runtime *VerifierRuntime, classifier ToolRiskClassifier) *VerificationGate {
	if runtime == nil {
		runtime = DefaultVerifierRuntime()
	}
	if classifier == nil {
		classifier = DefaultToolRiskClassifier
	}
	return &VerificationGate{
		runtime:    runtime,
		classifier: classifier,
	}
}

// ── Pre-action gate ─────────────────────────────────────────────────────────

// MayExecuteTool checks whether a tool call should proceed based on the
// task's verification policy, authority, and the tool's risk level.
func (g *VerificationGate) MayExecuteTool(
	auth state.TaskAuthority,
	spec state.VerificationSpec,
	toolName string,
	params map[string]any,
) GateResult {
	spec = spec.Normalize()

	// No verification policy → allow.
	if spec.Policy == state.VerificationPolicyNone {
		return GateResult{Decision: GateAllow, Reason: "no verification policy"}
	}

	risk := g.classifier(toolName, params)

	// Low/none risk actions pass regardless of policy.
	if risk == ActionRiskNone || risk == ActionRiskLow {
		return GateResult{Decision: GateAllow, Reason: fmt.Sprintf("tool %s is %s risk", toolName, risk)}
	}

	mode := auth.EffectiveAutonomyMode(state.AutonomyFull)

	// Full autonomy: high-risk tools proceed with advisory note.
	if mode == state.AutonomyFull {
		if risk == ActionRiskCritical {
			// Even full autonomy gates critical actions.
			return GateResult{
				Decision:   GateEscalate,
				Reason:     fmt.Sprintf("tool %s is critical risk — escalation required even in full autonomy", toolName),
				Suggestion: "request operator approval for critical actions",
			}
		}
		return GateResult{Decision: GateAllow, Reason: fmt.Sprintf("full autonomy permits %s risk tool %s", risk, toolName)}
	}

	// Plan approval: medium+ risk actions require verification to have passed.
	if mode == state.AutonomyPlanApproval {
		if risk == ActionRiskMedium || risk == ActionRiskHigh || risk == ActionRiskCritical {
			if !spec.AllRequiredPassed() {
				return GateResult{
					Decision:   GateBlock,
					Reason:     fmt.Sprintf("tool %s is %s risk and verification not complete", toolName, risk),
					Suggestion: "complete verification checks before executing risky tools",
				}
			}
		}
		return GateResult{Decision: GateAllow, Reason: fmt.Sprintf("plan_approval permits %s risk tool %s (verification passed)", risk, toolName)}
	}

	// Step approval: all medium+ actions gated.
	if mode == state.AutonomyStepApproval {
		if risk == ActionRiskMedium || risk == ActionRiskHigh || risk == ActionRiskCritical {
			return GateResult{
				Decision:   GateEscalate,
				Reason:     fmt.Sprintf("tool %s is %s risk — step approval requires operator review", toolName, risk),
				Suggestion: "request step-level approval",
			}
		}
		return GateResult{Decision: GateAllow, Reason: fmt.Sprintf("step_approval permits low risk tool %s", toolName)}
	}

	// Supervised: everything gated.
	if mode == state.AutonomySupervised {
		return GateResult{
			Decision:   GateEscalate,
			Reason:     fmt.Sprintf("supervised mode requires approval for tool %s", toolName),
			Suggestion: "request operator approval",
		}
	}

	return GateResult{Decision: GateAllow, Reason: "default allow"}
}

// ── Completion gate ─────────────────────────────────────────────────────────

// MayComplete checks whether a task's outputs pass verification and the task
// may transition to completed status. Returns a block/escalate/replan decision
// if verification fails, never silent success.
func (g *VerificationGate) MayComplete(
	ctx context.Context,
	task state.TaskSpec,
	outputs TaskOutputs,
	actor string,
	now int64,
) GateResult {
	spec := task.Verification.Normalize()

	// No verification → allow.
	if spec.Policy == state.VerificationPolicyNone || len(spec.Checks) == 0 {
		return GateResult{Decision: GateAllow, Reason: "no verification required for completion"}
	}

	// Run the verifier runtime.
	result := g.runtime.EvaluateAll(ctx, task, outputs, actor, now)

	if result.Passed {
		return GateResult{Decision: GateAllow, Reason: "all verification checks passed", UpdatedSpec: result.UpdatedSpec}
	}

	// Verification failed — determine the appropriate response.
	var failedChecks, pendingChecks, errorChecks []string
	for _, cr := range result.CheckResults {
		switch {
		case cr.Pending:
			pendingChecks = append(pendingChecks, cr.CheckID)
		case cr.Error != "":
			errorChecks = append(errorChecks, cr.CheckID)
		case !cr.Outcome.Passed:
			failedChecks = append(failedChecks, cr.CheckID)
		}
	}
	// Combine all blocking checks for the gate result.
	allBlocking := append(append(failedChecks, pendingChecks...), errorChecks...)

	mode := task.Authority.EffectiveAutonomyMode(state.AutonomyFull)

	// Advisory policy: warn but allow.
	if spec.Policy == state.VerificationPolicyAdvisory {
		return GateResult{
			Decision:     GateAllow,
			Reason:       fmt.Sprintf("advisory: %d checks failed but completion allowed", len(failedChecks)),
			FailedChecks: failedChecks,
			UpdatedSpec:  result.UpdatedSpec,
		}
	}

	// Required policy with failures → decide based on autonomy mode.
	gateResult := g.decideFailureResponse(mode, allBlocking, result.Summary)
	gateResult.UpdatedSpec = result.UpdatedSpec
	return gateResult
}

// decideFailureResponse maps a verification failure to the right gate action
// based on the autonomy mode.
func (g *VerificationGate) decideFailureResponse(mode state.AutonomyMode, failedChecks []string, summary string) GateResult {
	switch mode {
	case state.AutonomyFull:
		// Full autonomy: agent should try to fix and replan, not silently succeed.
		return GateResult{
			Decision:     GateReplan,
			Reason:       fmt.Sprintf("verification failed: %s — replanning to address failures", summary),
			FailedChecks: failedChecks,
			Suggestion:   "review failed checks and adjust approach",
		}

	case state.AutonomyPlanApproval:
		// Plan approval: block and wait for operator or replanning.
		return GateResult{
			Decision:     GateBlock,
			Reason:       fmt.Sprintf("verification failed: %s — blocked until resolved", summary),
			FailedChecks: failedChecks,
			Suggestion:   "fix failing checks or request operator override",
		}

	case state.AutonomyStepApproval, state.AutonomySupervised:
		// Restrictive modes: escalate.
		return GateResult{
			Decision:     GateEscalate,
			Reason:       fmt.Sprintf("verification failed: %s — escalating to operator", summary),
			FailedChecks: failedChecks,
			Suggestion:   "operator review required for failed verification",
		}

	default:
		return GateResult{
			Decision:     GateBlock,
			Reason:       fmt.Sprintf("verification failed: %s", summary),
			FailedChecks: failedChecks,
		}
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// allRequiredChecksPassed is delegated to spec.AllRequiredPassed() in state.

// ── Formatting ──────────────────────────────────────────────────────────────

// FormatGateResult returns a human-readable gate decision.
func FormatGateResult(r GateResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Gate: %s — %s", r.Decision, r.Reason)
	if len(r.FailedChecks) > 0 {
		fmt.Fprintf(&b, " (failed: %s)", strings.Join(r.FailedChecks, ", "))
	}
	if r.Suggestion != "" {
		fmt.Fprintf(&b, " → %s", r.Suggestion)
	}
	return b.String()
}
