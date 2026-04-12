package planner

import (
	"encoding/json"
	"testing"

	"metiq/internal/store/state"
)

// ── Default policy matrix tests ────────────────────────────────────────────────

func TestDefaultPolicy_CriticalAlwaysEscalates(t *testing.T) {
	engine := NewGovernanceEngine(nil) // uses default policy
	modes := []state.AutonomyMode{
		state.AutonomyFull,
		state.AutonomyPlanApproval,
		state.AutonomyStepApproval,
		state.AutonomySupervised,
	}
	actions := []ActionClass{
		ActionToolCall,
		ActionDelegation,
		ActionPlanExecution,
		ActionStepExecution,
		ActionPublish,
		ActionFinancial,
	}
	for _, mode := range modes {
		for _, action := range actions {
			auth := state.TaskAuthority{
				AutonomyMode: mode,
				RiskClass:    state.RiskClassCritical,
				CanAct:       true,
				CanDelegate:  true,
			}
			dec := engine.Evaluate(ActionRequest{
				Action:    action,
				Authority: auth,
			})
			if dec.Verdict != VerdictRequireEscalation {
				t.Errorf("mode=%s action=%s: critical risk should escalate, got %s",
					mode, action, dec.Verdict)
			}
		}
	}
}

func TestDefaultPolicy_FullMode_LowRisk_AllowsAll(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	actions := []ActionClass{ActionToolCall, ActionDelegation, ActionPlanExecution, ActionStepExecution, ActionPublish, ActionFinancial}
	for _, action := range actions {
		auth := state.TaskAuthority{
			AutonomyMode: state.AutonomyFull,
			RiskClass:    state.RiskClassLow,
			CanAct:       true,
			CanDelegate:  true,
		}
		dec := engine.Evaluate(ActionRequest{Action: action, Authority: auth})
		if dec.Verdict != VerdictAllow {
			t.Errorf("full/low/%s: expected allow, got %s", action, dec.Verdict)
		}
	}
}

func TestDefaultPolicy_FullMode_HighRisk_SensitiveActionsNeedApproval(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	sensitive := []ActionClass{ActionFinancial, ActionPublish, ActionDelegation}
	for _, action := range sensitive {
		auth := state.TaskAuthority{
			AutonomyMode: state.AutonomyFull,
			RiskClass:    state.RiskClassHigh,
			CanAct:       true,
			CanDelegate:  true,
		}
		dec := engine.Evaluate(ActionRequest{Action: action, Authority: auth})
		if dec.Verdict != VerdictRequireApproval {
			t.Errorf("full/high/%s: expected require_approval, got %s", action, dec.Verdict)
		}
	}
}

func TestDefaultPolicy_PlanApproval_PlanExecutionNeedsApproval(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	risks := []state.RiskClass{state.RiskClassLow, state.RiskClassMedium, state.RiskClassHigh}
	for _, risk := range risks {
		auth := state.TaskAuthority{
			AutonomyMode: state.AutonomyPlanApproval,
			RiskClass:    risk,
			CanAct:       true,
			CanDelegate:  true,
		}
		dec := engine.Evaluate(ActionRequest{Action: ActionPlanExecution, Authority: auth})
		if dec.Verdict != VerdictRequireApproval {
			t.Errorf("plan_approval/%s/plan_execution: expected require_approval, got %s", risk, dec.Verdict)
		}
	}
}

func TestDefaultPolicy_PlanApproval_HighRiskToolEscalates(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyPlanApproval,
		RiskClass:    state.RiskClassHigh,
		CanAct:       true,
	}
	dec := engine.Evaluate(ActionRequest{Action: ActionToolCall, Authority: auth})
	if dec.Verdict != VerdictRequireEscalation {
		t.Errorf("plan_approval/high/tool_call: expected escalation, got %s", dec.Verdict)
	}
}

func TestDefaultPolicy_StepApproval_StepNeedsApproval(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyStepApproval,
		RiskClass:    state.RiskClassLow,
		CanAct:       true,
	}
	dec := engine.Evaluate(ActionRequest{Action: ActionStepExecution, Authority: auth})
	if dec.Verdict != VerdictRequireApproval {
		t.Errorf("step_approval/low/step_execution: expected require_approval, got %s", dec.Verdict)
	}
}

func TestDefaultPolicy_StepApproval_HighRiskEscalates(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyStepApproval,
		RiskClass:    state.RiskClassHigh,
		CanAct:       true,
	}
	dec := engine.Evaluate(ActionRequest{Action: ActionToolCall, Authority: auth})
	if dec.Verdict != VerdictRequireEscalation {
		t.Errorf("step_approval/high/tool_call: expected escalation, got %s", dec.Verdict)
	}
}

func TestDefaultPolicy_Supervised_EverythingNeedsApproval(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	actions := []ActionClass{ActionToolCall, ActionDelegation, ActionPlanExecution, ActionStepExecution, ActionPublish, ActionFinancial}
	for _, action := range actions {
		auth := state.TaskAuthority{
			AutonomyMode: state.AutonomySupervised,
			RiskClass:    state.RiskClassLow,
			CanAct:       true,
			CanDelegate:  true,
		}
		dec := engine.Evaluate(ActionRequest{Action: action, Authority: auth})
		if dec.Verdict != VerdictRequireApproval {
			t.Errorf("supervised/low/%s: expected require_approval, got %s", action, dec.Verdict)
		}
	}
}

func TestDefaultPolicy_Supervised_HighRiskEscalates(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomySupervised,
		RiskClass:    state.RiskClassHigh,
		CanAct:       true,
	}
	dec := engine.Evaluate(ActionRequest{Action: ActionToolCall, Authority: auth})
	if dec.Verdict != VerdictRequireEscalation {
		t.Errorf("supervised/high/tool_call: expected escalation, got %s", dec.Verdict)
	}
}

// ── Hard gate tests ────────────────────────────────────────────────────────────

func TestHardGate_CanActFalse_DeniesTool(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		RiskClass:    state.RiskClassLow,
		CanAct:       false,
	}
	dec := engine.Evaluate(ActionRequest{Action: ActionToolCall, Authority: auth})
	if dec.Verdict != VerdictDeny {
		t.Errorf("CanAct=false should deny tool_call, got %s", dec.Verdict)
	}
}

func TestHardGate_CanActFalse_DeniesPublish(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{CanAct: false}
	dec := engine.Evaluate(ActionRequest{Action: ActionPublish, Authority: auth})
	if dec.Verdict != VerdictDeny {
		t.Errorf("CanAct=false should deny publish, got %s", dec.Verdict)
	}
}

func TestHardGate_CanActFalse_DeniesFinancial(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{CanAct: false}
	dec := engine.Evaluate(ActionRequest{Action: ActionFinancial, Authority: auth})
	if dec.Verdict != VerdictDeny {
		t.Errorf("CanAct=false should deny financial, got %s", dec.Verdict)
	}
}

func TestHardGate_ToolDenied(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		RiskClass:    state.RiskClassLow,
		CanAct:       true,
		DeniedTools:  []string{"dangerous_tool"},
	}
	dec := engine.Evaluate(ActionRequest{
		Action:    ActionToolCall,
		Tool:      "dangerous_tool",
		Authority: auth,
	})
	if dec.Verdict != VerdictDeny {
		t.Errorf("denied tool should be blocked, got %s", dec.Verdict)
	}
}

func TestHardGate_ToolNotInAllowlist(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		CanAct:       true,
		AllowedTools: []string{"read_file"},
	}
	dec := engine.Evaluate(ActionRequest{
		Action:    ActionToolCall,
		Tool:      "write_file",
		Authority: auth,
	})
	if dec.Verdict != VerdictDeny {
		t.Errorf("tool not in allowlist should be denied, got %s", dec.Verdict)
	}
}

func TestHardGate_ToolInAllowlist(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		RiskClass:    state.RiskClassLow,
		CanAct:       true,
		AllowedTools: []string{"read_file"},
	}
	dec := engine.Evaluate(ActionRequest{
		Action:    ActionToolCall,
		Tool:      "read_file",
		Authority: auth,
	})
	if dec.Verdict != VerdictAllow {
		t.Errorf("tool in allowlist should be allowed, got %s", dec.Verdict)
	}
}

func TestHardGate_CanDelegateFalse(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{CanDelegate: false}
	dec := engine.Evaluate(ActionRequest{
		Action:    ActionDelegation,
		Authority: auth,
	})
	if dec.Verdict != VerdictDeny {
		t.Errorf("CanDelegate=false should deny delegation, got %s", dec.Verdict)
	}
}

func TestHardGate_DelegationTargetNotAllowed(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		CanDelegate:   true,
		AllowedAgents: []string{"worker-a"},
	}
	dec := engine.Evaluate(ActionRequest{
		Action:      ActionDelegation,
		TargetAgent: "worker-b",
		Authority:   auth,
	})
	if dec.Verdict != VerdictDeny {
		t.Errorf("delegation to non-allowed agent should be denied, got %s", dec.Verdict)
	}
}

func TestHardGate_EscalationRequired(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode:       state.AutonomyFull,
		RiskClass:          state.RiskClassLow,
		CanAct:             true,
		EscalationRequired: true,
	}
	dec := engine.Evaluate(ActionRequest{Action: ActionToolCall, Authority: auth})
	if dec.Verdict != VerdictRequireEscalation {
		t.Errorf("EscalationRequired should force escalation, got %s", dec.Verdict)
	}
}

// ── RiskOverride tests ─────────────────────────────────────────────────────────

func TestRiskOverride_OverridesAuthorityRisk(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		RiskClass:    state.RiskClassLow,
		CanAct:       true,
	}
	// Low-risk authority, but the specific action is critical.
	dec := engine.Evaluate(ActionRequest{
		Action:       ActionToolCall,
		Authority:    auth,
		RiskOverride: state.RiskClassCritical,
	})
	if dec.Verdict != VerdictRequireEscalation {
		t.Errorf("critical risk override should escalate, got %s", dec.Verdict)
	}
	if dec.RiskClass != state.RiskClassCritical {
		t.Errorf("decision should reflect override risk, got %s", dec.RiskClass)
	}
}

// ── ClassifyToolRisk tests ─────────────────────────────────────────────────────

func TestClassifyToolRisk_Financial(t *testing.T) {
	cases := []string{"nostr_zap_send", "pay_invoice", "zap_user"}
	for _, tool := range cases {
		if got := ClassifyToolRisk(tool); got != state.RiskClassHigh {
			t.Errorf("ClassifyToolRisk(%q) = %s, want high", tool, got)
		}
	}
}

func TestClassifyToolRisk_Publish(t *testing.T) {
	cases := []string{"nostr_publish", "nostr_send_dm", "broadcast_event"}
	for _, tool := range cases {
		if got := ClassifyToolRisk(tool); got != state.RiskClassMedium {
			t.Errorf("ClassifyToolRisk(%q) = %s, want medium", tool, got)
		}
	}
}

func TestClassifyToolRisk_Destructive(t *testing.T) {
	cases := []string{"delete_event", "remove_file", "drop_table"}
	for _, tool := range cases {
		if got := ClassifyToolRisk(tool); got != state.RiskClassHigh {
			t.Errorf("ClassifyToolRisk(%q) = %s, want high", tool, got)
		}
	}
}

func TestClassifyToolRisk_ReadOnly(t *testing.T) {
	cases := []string{"nostr_fetch", "relay_list", "get_profile", "nostr_search", "relay_info"}
	for _, tool := range cases {
		if got := ClassifyToolRisk(tool); got != state.RiskClassLow {
			t.Errorf("ClassifyToolRisk(%q) = %s, want low", tool, got)
		}
	}
}

func TestClassifyToolRisk_Unknown(t *testing.T) {
	if got := ClassifyToolRisk("custom_thing"); got != state.RiskClassMedium {
		t.Errorf("ClassifyToolRisk(unknown) = %s, want medium", got)
	}
}

// ── EvaluateToolCall integration tests ─────────────────────────────────────────

func TestEvaluateToolCall_FinancialInFullMode(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		CanAct:       true,
	}
	// Financial tool → classified as high risk → full mode financial actions need approval.
	dec := engine.EvaluateToolCall(auth, "nostr_zap_send")
	// ClassifyToolRisk("nostr_zap_send") = high, but action is tool_call not financial.
	// In full mode, high-risk tool calls are allowed (only financial/publish/delegation need approval).
	// The tool *risk* is high, but since EvaluateToolCall classifies as ActionToolCall
	// (not ActionFinancial), the policy allows it. This is correct — to gate financial
	// operations, the caller should use ActionFinancial explicitly.
	if dec.Verdict != VerdictAllow {
		t.Errorf("zap tool call in full mode: expected allow, got %s", dec.Verdict)
	}

	// But as an explicit financial action, it requires approval.
	finDec := engine.Evaluate(ActionRequest{
		Action:       ActionFinancial,
		Authority:    auth,
		RiskOverride: state.RiskClassHigh,
	})
	if finDec.Verdict != VerdictRequireApproval {
		t.Errorf("explicit financial/high in full mode: expected require_approval, got %s", finDec.Verdict)
	}
}

func TestEvaluateToolCall_ReadOnlyInFullMode(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		CanAct:       true,
	}
	dec := engine.EvaluateToolCall(auth, "nostr_fetch")
	if dec.Verdict != VerdictAllow {
		t.Errorf("fetch in full mode: expected allow, got %s", dec.Verdict)
	}
}

func TestEvaluateToolCall_PublishInSupervised(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomySupervised,
		CanAct:       true,
	}
	dec := engine.EvaluateToolCall(auth, "nostr_publish")
	if dec.Verdict != VerdictRequireApproval {
		t.Errorf("publish in supervised: expected require_approval, got %s", dec.Verdict)
	}
}

// ── MayAct / MayDelegate convenience tests ─────────────────────────────────────

func TestMayAct_AllowedInFullLowRisk(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		RiskClass:    state.RiskClassLow,
		CanAct:       true,
	}
	if !engine.MayAct(auth, "read_file") {
		t.Error("full/low should allow tool call")
	}
}

func TestMayAct_DeniedWhenCanActFalse(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{CanAct: false}
	if engine.MayAct(auth, "any_tool") {
		t.Error("CanAct=false should deny")
	}
}

func TestMayDelegate_AllowedInFullLowRisk(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		RiskClass:    state.RiskClassLow,
		CanDelegate:  true,
	}
	if !engine.MayDelegate(auth, "worker") {
		t.Error("full/low should allow delegation")
	}
}

func TestMayDelegate_DeniedWhenCanDelegateFalse(t *testing.T) {
	engine := NewGovernanceEngine(nil)
	auth := state.TaskAuthority{CanDelegate: false}
	if engine.MayDelegate(auth, "worker") {
		t.Error("CanDelegate=false should deny")
	}
}

// ── Custom policy tests ────────────────────────────────────────────────────────

func TestCustomPolicy_OverridesDefault(t *testing.T) {
	// Custom policy that allows everything even at high risk.
	rules := map[PolicyKey]GovernanceVerdict{
		{Mode: state.AutonomyFull, Risk: state.RiskClassHigh, Action: ActionToolCall}: VerdictAllow,
	}
	engine := NewGovernanceEngine(NewGovernancePolicy(rules))
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		RiskClass:    state.RiskClassHigh,
		CanAct:       true,
	}
	dec := engine.Evaluate(ActionRequest{Action: ActionToolCall, Authority: auth})
	if dec.Verdict != VerdictAllow {
		t.Errorf("custom policy should allow, got %s", dec.Verdict)
	}
}

func TestCustomPolicy_MissingRuleFallsToEscalation(t *testing.T) {
	// Empty custom policy — no rules match.
	engine := NewGovernanceEngine(NewGovernancePolicy(nil))
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		RiskClass:    state.RiskClassLow,
		CanAct:       true,
	}
	dec := engine.Evaluate(ActionRequest{Action: ActionToolCall, Authority: auth})
	if dec.Verdict != VerdictRequireEscalation {
		t.Errorf("missing rule should escalate, got %s", dec.Verdict)
	}
}

// ── GovernanceDecision tests ───────────────────────────────────────────────────

func TestGovernanceDecision_Allowed(t *testing.T) {
	if !(GovernanceDecision{Verdict: VerdictAllow}).Allowed() {
		t.Error("allow verdict should report Allowed() = true")
	}
	if (GovernanceDecision{Verdict: VerdictRequireApproval}).Allowed() {
		t.Error("require_approval should report Allowed() = false")
	}
	if (GovernanceDecision{Verdict: VerdictDeny}).Allowed() {
		t.Error("deny should report Allowed() = false")
	}
}

func TestGovernanceDecision_JSONRoundTrip(t *testing.T) {
	dec := GovernanceDecision{
		Verdict:      VerdictRequireApproval,
		Reason:       "test reason",
		Action:       ActionFinancial,
		RiskClass:    state.RiskClassHigh,
		AutonomyMode: state.AutonomyPlanApproval,
	}
	blob, err := json.Marshal(dec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded GovernanceDecision
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Verdict != dec.Verdict || decoded.Action != dec.Action {
		t.Errorf("round-trip mismatch: got %+v", decoded)
	}
}

// ── End-to-end: authority resolution + governance ──────────────────────────────

func TestEndToEnd_ResolvedAuthorityInGovernance(t *testing.T) {
	// Config: full autonomy, low risk, all tools.
	config := AuthorityLayer{
		Source: AuthSourceConfig,
		Authority: state.TaskAuthority{
			AutonomyMode:  state.AutonomyFull,
			CanAct:        true,
			CanDelegate:   true,
			RiskClass:     state.RiskClassLow,
			AllowedTools:  []string{"read", "write", "deploy"},
		},
	}
	// Goal: narrows to plan_approval, medium risk, denies deploy.
	goal := AuthorityLayer{
		Source: AuthSourceGoal,
		Authority: state.TaskAuthority{
			AutonomyMode: state.AutonomyPlanApproval,
			RiskClass:    state.RiskClassMedium,
			CanAct:       true,
			CanDelegate:  true,
			DeniedTools:  []string{"deploy"},
		},
	}

	trace := ResolveAuthority(config, goal)
	engine := NewGovernanceEngine(nil)

	// read tool: allowed (plan_approval + medium risk + tool_call = allow for low-medium)
	readDec := engine.EvaluateToolCall(trace.Effective, "read")
	if readDec.Verdict != VerdictAllow {
		t.Errorf("read: expected allow, got %s (%s)", readDec.Verdict, readDec.Reason)
	}

	// deploy tool: denied by authority (denied tool list).
	deployDec := engine.Evaluate(ActionRequest{
		Action:    ActionToolCall,
		Tool:      "deploy",
		Authority: trace.Effective,
	})
	if deployDec.Verdict != VerdictDeny {
		t.Errorf("deploy: expected deny, got %s", deployDec.Verdict)
	}

	// plan execution: requires approval (plan_approval mode).
	planDec := engine.Evaluate(ActionRequest{
		Action:    ActionPlanExecution,
		Authority: trace.Effective,
	})
	if planDec.Verdict != VerdictRequireApproval {
		t.Errorf("plan execution: expected require_approval, got %s", planDec.Verdict)
	}
}

func TestEndToEnd_HighRiskCannotSilentlyExecute(t *testing.T) {
	// Acceptance criterion: high-risk actions cannot silently execute in restricted modes.
	restrictedModes := []state.AutonomyMode{
		state.AutonomyPlanApproval,
		state.AutonomyStepApproval,
		state.AutonomySupervised,
	}
	engine := NewGovernanceEngine(nil)
	for _, mode := range restrictedModes {
		auth := state.TaskAuthority{
			AutonomyMode: mode,
			RiskClass:    state.RiskClassHigh,
			CanAct:       true,
			CanDelegate:  true,
		}
		actions := []ActionClass{ActionToolCall, ActionDelegation, ActionPlanExecution, ActionStepExecution, ActionPublish, ActionFinancial}
		for _, action := range actions {
			dec := engine.Evaluate(ActionRequest{Action: action, Authority: auth})
			if dec.Verdict == VerdictAllow {
				t.Errorf("mode=%s/high/%s: high-risk action should NOT silently execute, got allow", mode, action)
			}
		}
	}
}
