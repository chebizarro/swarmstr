package planner

import (
	"context"
	"encoding/json"
	"testing"

	"metiq/internal/store/state"
)

// ── Tool risk classification ────────────────────────────────────────────────

func TestDefaultToolRiskClassifier(t *testing.T) {
	cases := []struct {
		tool     string
		expected ActionRisk
	}{
		{"nostr_publish", ActionRiskHigh},
		{"nostr_send_dm", ActionRiskHigh},
		{"nostr_zap_send", ActionRiskHigh},
		{"nostr_fetch", ActionRiskLow},
		{"relay_info", ActionRiskLow},
		{"memory_search", ActionRiskLow},
		{"config_set", ActionRiskMedium},
		{"unknown_tool", ActionRiskMedium},
	}
	for _, tc := range cases {
		got := DefaultToolRiskClassifier(tc.tool, nil)
		if got != tc.expected {
			t.Errorf("tool=%s: expected %s, got %s", tc.tool, tc.expected, got)
		}
	}
}

// ── Pre-action gate: tool execution ─────────────────────────────────────────

func TestGate_NoPolicy_Allows(t *testing.T) {
	gate := NewVerificationGate(nil, nil)
	auth := state.TaskAuthority{CanAct: true, AutonomyMode: state.AutonomyFull}
	spec := state.VerificationSpec{Policy: state.VerificationPolicyNone}
	r := gate.MayExecuteTool(auth, spec, "nostr_publish", nil)
	if !r.Allowed() {
		t.Fatalf("expected allow with no policy, got %s: %s", r.Decision, r.Reason)
	}
}

func TestGate_LowRisk_AlwaysAllowed(t *testing.T) {
	gate := NewVerificationGate(nil, nil)
	auth := state.TaskAuthority{CanAct: true, AutonomyMode: state.AutonomySupervised}
	spec := state.VerificationSpec{Policy: state.VerificationPolicyRequired}
	r := gate.MayExecuteTool(auth, spec, "nostr_fetch", nil)
	if !r.Allowed() {
		t.Fatalf("expected allow for low risk, got %s", r.Decision)
	}
}

func TestGate_FullAutonomy_HighRisk_Allowed(t *testing.T) {
	gate := NewVerificationGate(nil, nil)
	auth := state.TaskAuthority{CanAct: true, AutonomyMode: state.AutonomyFull}
	spec := state.VerificationSpec{Policy: state.VerificationPolicyRequired}
	r := gate.MayExecuteTool(auth, spec, "nostr_publish", nil)
	if !r.Allowed() {
		t.Fatalf("expected allow for high risk in full autonomy, got %s", r.Decision)
	}
}

func TestGate_FullAutonomy_CriticalRisk_Escalates(t *testing.T) {
	gate := NewVerificationGate(nil, func(tool string, _ map[string]any) ActionRisk {
		return ActionRiskCritical
	})
	auth := state.TaskAuthority{CanAct: true, AutonomyMode: state.AutonomyFull}
	spec := state.VerificationSpec{Policy: state.VerificationPolicyRequired}
	r := gate.MayExecuteTool(auth, spec, "dangerous_tool", nil)
	if r.Decision != GateEscalate {
		t.Fatalf("expected escalate for critical in full autonomy, got %s", r.Decision)
	}
}

func TestGate_PlanApproval_HighRisk_VerificationPassed(t *testing.T) {
	gate := NewVerificationGate(nil, nil)
	auth := state.TaskAuthority{CanAct: true, AutonomyMode: state.AutonomyPlanApproval}
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "c1", Required: true, Status: state.VerificationStatusPassed},
		},
	}
	r := gate.MayExecuteTool(auth, spec, "nostr_publish", nil)
	if !r.Allowed() {
		t.Fatalf("expected allow with passed verification, got %s: %s", r.Decision, r.Reason)
	}
}

func TestGate_PlanApproval_MediumRisk_VerificationPending(t *testing.T) {
	gate := NewVerificationGate(nil, nil)
	auth := state.TaskAuthority{CanAct: true, AutonomyMode: state.AutonomyPlanApproval}
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "c1", Required: true, Status: state.VerificationStatusPending},
		},
	}
	r := gate.MayExecuteTool(auth, spec, "config_set", nil)
	if r.Decision != GateBlock {
		t.Fatalf("expected block for medium risk with pending verification, got %s", r.Decision)
	}
}

func TestGate_PlanApproval_HighRisk_VerificationPending(t *testing.T) {
	gate := NewVerificationGate(nil, nil)
	auth := state.TaskAuthority{CanAct: true, AutonomyMode: state.AutonomyPlanApproval}
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "c1", Required: true, Status: state.VerificationStatusPending},
		},
	}
	r := gate.MayExecuteTool(auth, spec, "nostr_publish", nil)
	if r.Decision != GateBlock {
		t.Fatalf("expected block with pending verification, got %s", r.Decision)
	}
}

func TestGate_StepApproval_MediumRisk_Escalates(t *testing.T) {
	gate := NewVerificationGate(nil, nil)
	auth := state.TaskAuthority{CanAct: true, AutonomyMode: state.AutonomyStepApproval}
	spec := state.VerificationSpec{Policy: state.VerificationPolicyRequired}
	r := gate.MayExecuteTool(auth, spec, "config_set", nil)
	if r.Decision != GateEscalate {
		t.Fatalf("expected escalate in step_approval for medium risk, got %s", r.Decision)
	}
}

func TestGate_Supervised_AllToolsEscalate(t *testing.T) {
	gate := NewVerificationGate(nil, nil)
	auth := state.TaskAuthority{CanAct: true, AutonomyMode: state.AutonomySupervised}
	spec := state.VerificationSpec{Policy: state.VerificationPolicyRequired}

	// Even medium risk escalates in supervised.
	r := gate.MayExecuteTool(auth, spec, "config_set", nil)
	if r.Decision != GateEscalate {
		t.Fatalf("expected escalate in supervised, got %s", r.Decision)
	}
}

// ── Completion gate ─────────────────────────────────────────────────────────

func TestGate_MayComplete_NoPolicy(t *testing.T) {
	gate := NewVerificationGate(nil, nil)
	task := state.TaskSpec{
		TaskID: "t1", Title: "test", Instructions: "do it",
		Verification: state.VerificationSpec{Policy: state.VerificationPolicyNone},
	}
	r := gate.MayComplete(context.Background(), task, TaskOutputs{}, "test", 100)
	if !r.Allowed() {
		t.Fatalf("expected allow, got %s", r.Decision)
	}
}

func TestGate_MayComplete_AllChecksPassed(t *testing.T) {
	gate := NewVerificationGate(DefaultVerifierRuntime(), nil)
	task := state.TaskSpec{
		TaskID: "t1", Title: "test", Instructions: "do it",
		Authority: state.TaskAuthority{AutonomyMode: state.AutonomyFull},
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyRequired,
			Checks: []state.VerificationCheck{
				{CheckID: "s1", Type: state.VerificationCheckSchema, Description: "has fields", Required: true},
			},
		},
	}
	outputs := TaskOutputs{StructuredOutput: map[string]any{"key": "val"}}
	r := gate.MayComplete(context.Background(), task, outputs, "test", 100)
	if !r.Allowed() {
		t.Fatalf("expected allow, got %s: %s", r.Decision, r.Reason)
	}
	if r.UpdatedSpec.VerifiedAt != 100 || r.UpdatedSpec.Checks[0].Status != state.VerificationStatusPassed {
		t.Fatalf("expected completion gate to return updated verification spec, got %+v", r.UpdatedSpec)
	}
}

func TestGate_MayComplete_FailedChecks_FullAutonomy_Replans(t *testing.T) {
	gate := NewVerificationGate(DefaultVerifierRuntime(), nil)
	task := state.TaskSpec{
		TaskID: "t1", Title: "test", Instructions: "do it",
		Authority: state.TaskAuthority{AutonomyMode: state.AutonomyFull},
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyRequired,
			Checks: []state.VerificationCheck{
				{CheckID: "s1", Type: state.VerificationCheckSchema, Description: "has fields", Required: true,
					Meta: map[string]any{"required_fields": []any{"missing_field"}}},
			},
		},
	}
	outputs := TaskOutputs{StructuredOutput: map[string]any{"wrong_field": "val"}}
	r := gate.MayComplete(context.Background(), task, outputs, "test", 100)
	if r.Decision != GateReplan {
		t.Fatalf("expected replan in full autonomy, got %s: %s", r.Decision, r.Reason)
	}
	if len(r.FailedChecks) == 0 {
		t.Fatal("expected failed checks")
	}
}

func TestGate_MayComplete_FailedChecks_PlanApproval_Blocks(t *testing.T) {
	gate := NewVerificationGate(DefaultVerifierRuntime(), nil)
	task := state.TaskSpec{
		TaskID: "t1", Title: "test", Instructions: "do it",
		Authority: state.TaskAuthority{AutonomyMode: state.AutonomyPlanApproval},
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyRequired,
			Checks: []state.VerificationCheck{
				{CheckID: "s1", Type: state.VerificationCheckSchema, Description: "has fields", Required: true},
			},
		},
	}
	// No structured output → schema check fails.
	r := gate.MayComplete(context.Background(), task, TaskOutputs{}, "test", 100)
	if r.Decision != GateBlock {
		t.Fatalf("expected block in plan_approval, got %s", r.Decision)
	}
}

func TestGate_MayComplete_FailedChecks_Supervised_Escalates(t *testing.T) {
	gate := NewVerificationGate(DefaultVerifierRuntime(), nil)
	task := state.TaskSpec{
		TaskID: "t1", Title: "test", Instructions: "do it",
		Authority: state.TaskAuthority{AutonomyMode: state.AutonomySupervised},
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyRequired,
			Checks: []state.VerificationCheck{
				{CheckID: "s1", Type: state.VerificationCheckSchema, Description: "has fields", Required: true},
			},
		},
	}
	r := gate.MayComplete(context.Background(), task, TaskOutputs{}, "test", 100)
	if r.Decision != GateEscalate {
		t.Fatalf("expected escalate in supervised, got %s", r.Decision)
	}
}

func TestGate_MayComplete_Advisory_AllowsDespiteFailure(t *testing.T) {
	gate := NewVerificationGate(DefaultVerifierRuntime(), nil)
	task := state.TaskSpec{
		TaskID: "t1", Title: "test", Instructions: "do it",
		Authority: state.TaskAuthority{AutonomyMode: state.AutonomyFull},
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyAdvisory,
			Checks: []state.VerificationCheck{
				{CheckID: "s1", Type: state.VerificationCheckSchema, Description: "has fields", Required: true},
			},
		},
	}
	// Fails schema but advisory → allow.
	r := gate.MayComplete(context.Background(), task, TaskOutputs{}, "test", 100)
	if !r.Allowed() {
		t.Fatalf("expected allow with advisory, got %s", r.Decision)
	}
}

// ── Helpers (using spec.AllRequiredPassed from state package) ────────────────

func TestAllRequiredChecksPassed_True(t *testing.T) {
	spec := state.VerificationSpec{
		Checks: []state.VerificationCheck{
			{CheckID: "c1", Required: true, Status: state.VerificationStatusPassed},
			{CheckID: "c2", Required: false, Status: state.VerificationStatusFailed},
		},
	}
	if !spec.AllRequiredPassed() {
		t.Fatal("expected true — only required check passed")
	}
}

func TestAllRequiredChecksPassed_False(t *testing.T) {
	spec := state.VerificationSpec{
		Checks: []state.VerificationCheck{
			{CheckID: "c1", Required: true, Status: state.VerificationStatusPending},
		},
	}
	if spec.AllRequiredPassed() {
		t.Fatal("expected false — required check pending")
	}
}

// ── Formatting ──────────────────────────────────────────────────────────────

func TestFormatGateResult(t *testing.T) {
	r := GateResult{
		Decision:     GateBlock,
		Reason:       "verification failed",
		FailedChecks: []string{"s1", "e1"},
		Suggestion:   "fix failing checks",
	}
	s := FormatGateResult(r)
	if s == "" {
		t.Fatal("expected non-empty format")
	}
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestGateResult_JSON(t *testing.T) {
	r := GateResult{
		Decision:     GateReplan,
		Reason:       "schema failed",
		FailedChecks: []string{"s1"},
		Suggestion:   "fix output",
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var r2 GateResult
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatal(err)
	}
	if r2.Decision != GateReplan || len(r2.FailedChecks) != 1 {
		t.Fatalf("round-trip mismatch: %+v", r2)
	}
}

// ── End-to-end ──────────────────────────────────────────────────────────────

func TestEndToEnd_ToolGate_AcrossAutonomyModes(t *testing.T) {
	gate := NewVerificationGate(nil, nil)
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "c1", Required: true, Status: state.VerificationStatusPending},
		},
	}

	modes := []struct {
		mode     state.AutonomyMode
		tool     string
		decision GateDecision
	}{
		{state.AutonomyFull, "nostr_publish", GateAllow},         // full: high risk allowed
		{state.AutonomyPlanApproval, "nostr_publish", GateBlock}, // plan_approval: high risk + pending → block
		{state.AutonomyStepApproval, "config_set", GateEscalate}, // step_approval: medium → escalate
		{state.AutonomySupervised, "config_set", GateEscalate},   // supervised: all → escalate
		{state.AutonomyFull, "nostr_fetch", GateAllow},           // full: low risk → allow
		{state.AutonomySupervised, "nostr_fetch", GateAllow},     // supervised: low risk → still allowed
	}

	for _, tc := range modes {
		auth := state.TaskAuthority{CanAct: true, AutonomyMode: tc.mode}
		r := gate.MayExecuteTool(auth, spec, tc.tool, nil)
		if r.Decision != tc.decision {
			t.Errorf("mode=%s tool=%s: expected %s, got %s (%s)", tc.mode, tc.tool, tc.decision, r.Decision, r.Reason)
		}
	}
}

func TestEndToEnd_CompletionGate_Pipeline(t *testing.T) {
	gate := NewVerificationGate(DefaultVerifierRuntime(), nil)

	// Task with schema + evidence checks.
	task := state.TaskSpec{
		TaskID: "t1", Title: "analysis", Instructions: "analyze data",
		Authority: state.TaskAuthority{AutonomyMode: state.AutonomyFull},
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyRequired,
			Checks: []state.VerificationCheck{
				{
					CheckID: "s1", Type: state.VerificationCheckSchema,
					Description: "has result fields", Required: true,
					Meta: map[string]any{"required_fields": []any{"summary", "confidence"}},
				},
				{
					CheckID: "e1", Type: state.VerificationCheckEvidence,
					Description: "has report artifact", Required: true,
					Meta: map[string]any{"required_artifacts": []any{"report.md"}},
				},
			},
		},
	}

	// Attempt 1: incomplete output → replan.
	outputs1 := TaskOutputs{
		StructuredOutput: map[string]any{"summary": "results"}, // missing confidence
	}
	r1 := gate.MayComplete(context.Background(), task, outputs1, "agent", 100)
	if r1.Decision != GateReplan {
		t.Fatalf("expected replan, got %s: %s", r1.Decision, r1.Reason)
	}

	// Attempt 2: complete output → allow.
	task2 := state.TaskSpec{
		TaskID: "t1", Title: "analysis", Instructions: "analyze data",
		Authority: state.TaskAuthority{AutonomyMode: state.AutonomyFull},
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyRequired,
			Checks: []state.VerificationCheck{
				{
					CheckID: "s1", Type: state.VerificationCheckSchema,
					Description: "has result fields", Required: true,
					Meta: map[string]any{"required_fields": []any{"summary", "confidence"}},
				},
				{
					CheckID: "e1", Type: state.VerificationCheckEvidence,
					Description: "has report artifact", Required: true,
					Meta: map[string]any{"required_artifacts": []any{"report.md"}},
				},
			},
		},
	}
	outputs2 := TaskOutputs{
		StructuredOutput: map[string]any{"summary": "results", "confidence": 0.95},
		Artifacts:        []TaskArtifact{{Name: "report.md", Content: "# Report"}},
	}
	r2 := gate.MayComplete(context.Background(), task2, outputs2, "agent", 200)
	if !r2.Allowed() {
		t.Fatalf("expected allow with complete output, got %s: %s", r2.Decision, r2.Reason)
	}
}
