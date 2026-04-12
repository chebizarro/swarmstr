// governance.go binds approvals, escalation paths, and risk classes to
// autonomy modes. It replaces ad hoc tool-name-only gating with a
// mode-aware, risk-aware policy engine.
package planner

import (
	"fmt"
	"strings"

	"metiq/internal/store/state"
)

// ── Action classification ──────────────────────────────────────────────────────

// ActionClass categorises the kind of action an agent wants to perform.
type ActionClass string

const (
	// ActionToolCall is a single tool invocation.
	ActionToolCall ActionClass = "tool_call"
	// ActionDelegation is spawning a sub-agent or delegating a sub-task.
	ActionDelegation ActionClass = "delegation"
	// ActionPlanExecution is executing a compiled plan.
	ActionPlanExecution ActionClass = "plan_execution"
	// ActionStepExecution is executing a single plan step.
	ActionStepExecution ActionClass = "step_execution"
	// ActionPublish is publishing externally visible content (Nostr events, etc.).
	ActionPublish ActionClass = "publish"
	// ActionFinancial is spending money (zaps, payments).
	ActionFinancial ActionClass = "financial"
)

// ── Governance decision ────────────────────────────────────────────────────────

// GovernanceVerdict is the outcome of a governance policy evaluation.
type GovernanceVerdict string

const (
	// VerdictAllow permits the action with no further checks.
	VerdictAllow GovernanceVerdict = "allow"
	// VerdictRequireApproval blocks until an operator approves.
	VerdictRequireApproval GovernanceVerdict = "require_approval"
	// VerdictRequireEscalation requires escalation to a higher authority.
	VerdictRequireEscalation GovernanceVerdict = "require_escalation"
	// VerdictDeny blocks the action outright.
	VerdictDeny GovernanceVerdict = "deny"
)

// GovernanceDecision is the fully resolved policy outcome for a proposed action.
type GovernanceDecision struct {
	// Verdict is the overall decision.
	Verdict GovernanceVerdict `json:"verdict"`
	// Reason explains why the verdict was reached.
	Reason string `json:"reason"`
	// Action is the action class that was evaluated.
	Action ActionClass `json:"action"`
	// RiskClass is the effective risk of the action.
	RiskClass state.RiskClass `json:"risk_class"`
	// AutonomyMode is the effective mode used for evaluation.
	AutonomyMode state.AutonomyMode `json:"autonomy_mode"`
}

// Allowed reports whether the action may proceed without operator intervention.
func (d GovernanceDecision) Allowed() bool {
	return d.Verdict == VerdictAllow
}

// ── Policy matrix ──────────────────────────────────────────────────────────────

// PolicyKey is a lookup key into the governance policy matrix.
type PolicyKey struct {
	Mode   state.AutonomyMode
	Risk   state.RiskClass
	Action ActionClass
}

// GovernancePolicy maps (autonomy_mode, risk_class, action) triples to verdicts.
// Absent keys fall through to escalation rules.
type GovernancePolicy struct {
	rules map[PolicyKey]GovernanceVerdict
}

// NewGovernancePolicy creates a policy from explicit rules.
func NewGovernancePolicy(rules map[PolicyKey]GovernanceVerdict) *GovernancePolicy {
	cp := make(map[PolicyKey]GovernanceVerdict, len(rules))
	for k, v := range rules {
		cp[k] = v
	}
	return &GovernancePolicy{rules: cp}
}

// Lookup returns the verdict for the given key, plus a bool indicating
// whether an explicit rule was found.
func (p *GovernancePolicy) Lookup(key PolicyKey) (GovernanceVerdict, bool) {
	v, ok := p.rules[key]
	return v, ok
}

// ── Default policy ─────────────────────────────────────────────────────────────

// DefaultGovernancePolicy returns the standard governance policy matrix.
//
// Design principles:
//   - full autonomy: allow low/medium, escalate high/critical
//   - plan_approval: plan execution needs approval; steps/tools follow risk
//   - step_approval: each step needs approval; tools follow risk
//   - supervised: everything requires approval or escalation
//   - financial and publish actions always require approval at medium+ risk
//   - critical risk always requires escalation regardless of mode
func DefaultGovernancePolicy() *GovernancePolicy {
	rules := map[PolicyKey]GovernanceVerdict{}

	modes := []state.AutonomyMode{
		state.AutonomyFull,
		state.AutonomyPlanApproval,
		state.AutonomyStepApproval,
		state.AutonomySupervised,
	}
	risks := []state.RiskClass{
		state.RiskClassLow,
		state.RiskClassMedium,
		state.RiskClassHigh,
		state.RiskClassCritical,
	}
	actions := []ActionClass{
		ActionToolCall,
		ActionDelegation,
		ActionPlanExecution,
		ActionStepExecution,
		ActionPublish,
		ActionFinancial,
	}

	// Fill the full matrix with sensible defaults.
	for _, mode := range modes {
		for _, risk := range risks {
			for _, action := range actions {
				verdict := resolveDefaultVerdict(mode, risk, action)
				rules[PolicyKey{Mode: mode, Risk: risk, Action: action}] = verdict
			}
		}
	}

	return NewGovernancePolicy(rules)
}

// resolveDefaultVerdict computes the default verdict for a single cell
// in the governance matrix.
func resolveDefaultVerdict(mode state.AutonomyMode, risk state.RiskClass, action ActionClass) GovernanceVerdict {
	// Critical risk always escalates regardless of mode.
	if risk == state.RiskClassCritical {
		return VerdictRequireEscalation
	}

	switch mode {
	case state.AutonomyFull:
		return fullModeVerdict(risk, action)
	case state.AutonomyPlanApproval:
		return planApprovalVerdict(risk, action)
	case state.AutonomyStepApproval:
		return stepApprovalVerdict(risk, action)
	case state.AutonomySupervised:
		return supervisedVerdict(risk, action)
	default:
		// Unknown mode → escalate for safety.
		return VerdictRequireEscalation
	}
}

func fullModeVerdict(risk state.RiskClass, action ActionClass) GovernanceVerdict {
	// Financial and publish are sensitive — require approval at high risk.
	if (action == ActionFinancial || action == ActionPublish) && risk == state.RiskClassHigh {
		return VerdictRequireApproval
	}
	// High-risk delegation requires approval.
	if action == ActionDelegation && risk == state.RiskClassHigh {
		return VerdictRequireApproval
	}
	// Everything else: allow.
	return VerdictAllow
}

func planApprovalVerdict(risk state.RiskClass, action ActionClass) GovernanceVerdict {
	// Plan execution always requires approval in this mode.
	if action == ActionPlanExecution {
		return VerdictRequireApproval
	}
	// High risk: escalate for tools, approve for delegation/publish/financial.
	if risk == state.RiskClassHigh {
		switch action {
		case ActionToolCall:
			return VerdictRequireEscalation
		default:
			return VerdictRequireApproval
		}
	}
	// Medium risk: financial/publish need approval.
	if risk == state.RiskClassMedium && (action == ActionFinancial || action == ActionPublish) {
		return VerdictRequireApproval
	}
	return VerdictAllow
}

func stepApprovalVerdict(risk state.RiskClass, action ActionClass) GovernanceVerdict {
	// Both plan and step execution need approval.
	if action == ActionPlanExecution || action == ActionStepExecution {
		return VerdictRequireApproval
	}
	// High risk: escalation for anything.
	if risk == state.RiskClassHigh {
		return VerdictRequireEscalation
	}
	// Medium risk: financial/publish/delegation need approval.
	if risk == state.RiskClassMedium {
		switch action {
		case ActionFinancial, ActionPublish, ActionDelegation:
			return VerdictRequireApproval
		}
	}
	return VerdictAllow
}

func supervisedVerdict(risk state.RiskClass, _ ActionClass) GovernanceVerdict {
	// Supervised mode: everything requires at least approval.
	// High risk → escalation.
	if risk == state.RiskClassHigh {
		return VerdictRequireEscalation
	}
	return VerdictRequireApproval
}

// ── Governance engine ──────────────────────────────────────────────────────────

// GovernanceEngine evaluates proposed actions against the effective authority
// and the governance policy matrix.
type GovernanceEngine struct {
	policy *GovernancePolicy
}

// NewGovernanceEngine creates an engine with the given policy.
// A nil policy falls back to DefaultGovernancePolicy().
func NewGovernanceEngine(policy *GovernancePolicy) *GovernanceEngine {
	if policy == nil {
		policy = DefaultGovernancePolicy()
	}
	return &GovernanceEngine{policy: policy}
}

// ActionRequest describes a proposed action for governance evaluation.
type ActionRequest struct {
	// Action is the class of operation being attempted.
	Action ActionClass
	// Tool is the specific tool name (for ActionToolCall).
	Tool string
	// TargetAgent is the delegation target (for ActionDelegation).
	TargetAgent string
	// Authority is the effective resolved authority for the current context.
	Authority state.TaskAuthority
	// RiskOverride allows the caller to override the authority's risk class
	// for a specific action (e.g. tool-specific risk).
	RiskOverride state.RiskClass
}

// Evaluate checks whether the proposed action is permitted under the
// effective authority and returns a governance decision.
func (e *GovernanceEngine) Evaluate(req ActionRequest) GovernanceDecision {
	auth := req.Authority.Normalize()
	mode := auth.EffectiveAutonomyMode(state.AutonomyFull)
	risk := req.RiskOverride
	if risk == "" {
		risk = auth.RiskClass
	}
	if risk == "" {
		risk = state.RiskClassLow
	}

	// Check hard authority gates first.
	if denied := e.checkHardGates(req, auth); denied != nil {
		return *denied
	}

	// Check escalation override.
	if auth.EscalationRequired {
		return GovernanceDecision{
			Verdict:      VerdictRequireEscalation,
			Reason:       "escalation_required is set on effective authority",
			Action:       req.Action,
			RiskClass:    risk,
			AutonomyMode: mode,
		}
	}

	// Look up the policy matrix.
	key := PolicyKey{Mode: mode, Risk: risk, Action: req.Action}
	verdict, found := e.policy.Lookup(key)
	if !found {
		// No explicit rule → escalate for safety.
		verdict = VerdictRequireEscalation
	}

	return GovernanceDecision{
		Verdict:      verdict,
		Reason:       e.buildReason(verdict, mode, risk, req.Action),
		Action:       req.Action,
		RiskClass:    risk,
		AutonomyMode: mode,
	}
}

// checkHardGates evaluates non-negotiable authority constraints that
// override the policy matrix entirely.
func (e *GovernanceEngine) checkHardGates(req ActionRequest, auth state.TaskAuthority) *GovernanceDecision {
	mode := auth.EffectiveAutonomyMode(state.AutonomyFull)
	risk := req.RiskOverride
	if risk == "" {
		risk = auth.RiskClass
	}
	if risk == "" {
		risk = state.RiskClassLow
	}

	// CanAct gate: if the agent cannot act, deny tool calls and publish.
	if !auth.CanAct && (req.Action == ActionToolCall || req.Action == ActionPublish || req.Action == ActionFinancial) {
		return &GovernanceDecision{
			Verdict:      VerdictDeny,
			Reason:       "can_act is false in effective authority",
			Action:       req.Action,
			RiskClass:    risk,
			AutonomyMode: mode,
		}
	}

	// Tool allowlist/denylist.
	if req.Action == ActionToolCall && req.Tool != "" && !auth.MayUseTool(req.Tool) {
		return &GovernanceDecision{
			Verdict:      VerdictDeny,
			Reason:       fmt.Sprintf("tool %q not permitted by authority", req.Tool),
			Action:       req.Action,
			RiskClass:    risk,
			AutonomyMode: mode,
		}
	}

	// Delegation gates.
	if req.Action == ActionDelegation {
		if !auth.CanDelegate {
			return &GovernanceDecision{
				Verdict:      VerdictDeny,
				Reason:       "can_delegate is false in effective authority",
				Action:       req.Action,
				RiskClass:    risk,
				AutonomyMode: mode,
			}
		}
		if req.TargetAgent != "" && !auth.MayDelegateTo(req.TargetAgent) {
			return &GovernanceDecision{
				Verdict:      VerdictDeny,
				Reason:       fmt.Sprintf("delegation to %q not permitted by authority", req.TargetAgent),
				Action:       req.Action,
				RiskClass:    risk,
				AutonomyMode: mode,
			}
		}
	}

	return nil // no hard gate triggered
}

// buildReason constructs a human-readable explanation for a verdict.
func (e *GovernanceEngine) buildReason(verdict GovernanceVerdict, mode state.AutonomyMode, risk state.RiskClass, action ActionClass) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("mode=%s", mode))
	parts = append(parts, fmt.Sprintf("risk=%s", risk))
	parts = append(parts, fmt.Sprintf("action=%s", action))

	switch verdict {
	case VerdictAllow:
		return fmt.Sprintf("allowed: %s", strings.Join(parts, ", "))
	case VerdictRequireApproval:
		return fmt.Sprintf("approval required: %s", strings.Join(parts, ", "))
	case VerdictRequireEscalation:
		return fmt.Sprintf("escalation required: %s", strings.Join(parts, ", "))
	case VerdictDeny:
		return fmt.Sprintf("denied: %s", strings.Join(parts, ", "))
	default:
		return fmt.Sprintf("unknown verdict: %s", strings.Join(parts, ", "))
	}
}

// ── Convenience helpers ────────────────────────────────────────────────────────

// MayAct is a shorthand that evaluates a tool call against an authority
// and returns whether it's allowed without any human intervention.
func (e *GovernanceEngine) MayAct(auth state.TaskAuthority, tool string) bool {
	return e.Evaluate(ActionRequest{
		Action:    ActionToolCall,
		Tool:      tool,
		Authority: auth,
	}).Allowed()
}

// MayDelegate is a shorthand that evaluates a delegation against an authority.
func (e *GovernanceEngine) MayDelegate(auth state.TaskAuthority, target string) bool {
	return e.Evaluate(ActionRequest{
		Action:      ActionDelegation,
		TargetAgent: target,
		Authority:   auth,
	}).Allowed()
}

// ClassifyToolRisk returns a risk class for a tool based on its name.
// This is the default heuristic — callers can override with RiskOverride.
func ClassifyToolRisk(tool string) state.RiskClass {
	lower := strings.ToLower(tool)

	// Financial tools.
	if strings.Contains(lower, "zap") || strings.Contains(lower, "pay") || strings.Contains(lower, "invoice") {
		return state.RiskClassHigh
	}
	// Publishing tools.
	if strings.Contains(lower, "publish") || strings.Contains(lower, "send_dm") || strings.Contains(lower, "broadcast") {
		return state.RiskClassMedium
	}
	// Destructive tools.
	if strings.Contains(lower, "delete") || strings.Contains(lower, "remove") || strings.Contains(lower, "drop") {
		return state.RiskClassHigh
	}
	// Read-only / informational.
	if strings.Contains(lower, "fetch") || strings.Contains(lower, "list") || strings.Contains(lower, "get") ||
		strings.Contains(lower, "info") || strings.Contains(lower, "search") || strings.Contains(lower, "profile") {
		return state.RiskClassLow
	}

	return state.RiskClassMedium // default for unknown tools
}

// EvaluateToolCall is a convenience that resolves tool risk and evaluates
// in a single call, using the default tool risk heuristic.
func (e *GovernanceEngine) EvaluateToolCall(auth state.TaskAuthority, tool string) GovernanceDecision {
	return e.Evaluate(ActionRequest{
		Action:       ActionToolCall,
		Tool:         tool,
		Authority:    auth,
		RiskOverride: ClassifyToolRisk(tool),
	})
}
