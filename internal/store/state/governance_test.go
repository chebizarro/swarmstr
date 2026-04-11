package state

import (
	"encoding/json"
	"testing"
)

// --- AutonomyMode tests ---

func TestAutonomyMode_ParseAllValid(t *testing.T) {
	tests := []struct {
		input string
		want  AutonomyMode
	}{
		{"full", AutonomyFull},
		{"plan_approval", AutonomyPlanApproval},
		{"step_approval", AutonomyStepApproval},
		{"supervised", AutonomySupervised},
		{"", AutonomyFull},
	}
	for _, tt := range tests {
		got, ok := ParseAutonomyMode(tt.input)
		if !ok {
			t.Errorf("ParseAutonomyMode(%q) !ok", tt.input)
		}
		if got != tt.want {
			t.Errorf("ParseAutonomyMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAutonomyMode_ParseInvalid(t *testing.T) {
	_, ok := ParseAutonomyMode("yolo")
	if ok {
		t.Error("invalid mode should return !ok")
	}
}

func TestNormalizeAutonomyMode_InvalidReturnsFull(t *testing.T) {
	got := NormalizeAutonomyMode("bogus")
	if got != AutonomyFull {
		t.Errorf("NormalizeAutonomyMode(bogus) = %q, want full", got)
	}
}

func TestAutonomyMode_RequiresPlanApproval(t *testing.T) {
	cases := map[AutonomyMode]bool{
		AutonomyFull:         false,
		AutonomyPlanApproval: true,
		AutonomyStepApproval: true,
		AutonomySupervised:   true,
	}
	for mode, want := range cases {
		if got := mode.RequiresPlanApproval(); got != want {
			t.Errorf("%s.RequiresPlanApproval() = %v, want %v", mode, got, want)
		}
	}
}

func TestAutonomyMode_RequiresStepApproval(t *testing.T) {
	cases := map[AutonomyMode]bool{
		AutonomyFull:         false,
		AutonomyPlanApproval: false,
		AutonomyStepApproval: true,
		AutonomySupervised:   true,
	}
	for mode, want := range cases {
		if got := mode.RequiresStepApproval(); got != want {
			t.Errorf("%s.RequiresStepApproval() = %v, want %v", mode, got, want)
		}
	}
}

// --- RiskClass tests ---

func TestRiskClass_ParseAllValid(t *testing.T) {
	tests := []struct {
		input string
		want  RiskClass
	}{
		{"low", RiskClassLow},
		{"medium", RiskClassMedium},
		{"high", RiskClassHigh},
		{"critical", RiskClassCritical},
		{"", RiskClassLow},
	}
	for _, tt := range tests {
		got, ok := ParseRiskClass(tt.input)
		if !ok {
			t.Errorf("ParseRiskClass(%q) !ok", tt.input)
		}
		if got != tt.want {
			t.Errorf("ParseRiskClass(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRiskClass_ParseInvalid(t *testing.T) {
	_, ok := ParseRiskClass("nuclear")
	if ok {
		t.Error("invalid risk_class should return !ok")
	}
}

func TestNormalizeRiskClass_InvalidReturnsLow(t *testing.T) {
	got := NormalizeRiskClass("bogus")
	if got != RiskClassLow {
		t.Errorf("NormalizeRiskClass(bogus) = %q, want low", got)
	}
}

// --- TaskAuthority tests ---

func TestTaskAuthority_Normalize(t *testing.T) {
	a := TaskAuthority{
		AutonomyMode: "plan_approval",
		RiskClass:    "high",
	}
	n := a.Normalize()
	if n.AutonomyMode != AutonomyPlanApproval {
		t.Errorf("AutonomyMode = %q", n.AutonomyMode)
	}
	if n.RiskClass != RiskClassHigh {
		t.Errorf("RiskClass = %q", n.RiskClass)
	}
}

func TestTaskAuthority_Normalize_EmptyPreserved(t *testing.T) {
	a := TaskAuthority{}
	n := a.Normalize()
	// Empty fields should remain empty (not forced to defaults) — allows
	// distinction between "not set" and "explicitly set to full/low".
	if n.AutonomyMode != "" {
		t.Errorf("empty AutonomyMode should stay empty, got %q", n.AutonomyMode)
	}
	if n.RiskClass != "" {
		t.Errorf("empty RiskClass should stay empty, got %q", n.RiskClass)
	}
}

func TestTaskAuthority_Validate_Valid(t *testing.T) {
	a := TaskAuthority{
		AutonomyMode:       AutonomyFull,
		RiskClass:          RiskClassMedium,
		CanAct:             true,
		MaxDelegationDepth: 3,
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("valid authority: %v", err)
	}
}

func TestTaskAuthority_Validate_InvalidAutonomy(t *testing.T) {
	a := TaskAuthority{AutonomyMode: "yolo"}
	if err := a.Validate(); err == nil {
		t.Fatal("expected error for invalid autonomy mode")
	}
}

func TestTaskAuthority_Validate_InvalidRiskClass(t *testing.T) {
	a := TaskAuthority{RiskClass: "nuclear"}
	if err := a.Validate(); err == nil {
		t.Fatal("expected error for invalid risk class")
	}
}

func TestTaskAuthority_Validate_NegativeDelegation(t *testing.T) {
	a := TaskAuthority{MaxDelegationDepth: -1}
	if err := a.Validate(); err == nil {
		t.Fatal("expected error for negative max_delegation_depth")
	}
}

func TestTaskAuthority_Validate_EmptyIsValid(t *testing.T) {
	a := TaskAuthority{}
	if err := a.Validate(); err != nil {
		t.Fatalf("empty authority should be valid: %v", err)
	}
}

func TestTaskAuthority_EffectiveAutonomyMode(t *testing.T) {
	a := TaskAuthority{AutonomyMode: AutonomySupervised}
	if got := a.EffectiveAutonomyMode(AutonomyFull); got != AutonomySupervised {
		t.Errorf("explicit mode should be returned, got %q", got)
	}

	empty := TaskAuthority{}
	if got := empty.EffectiveAutonomyMode(AutonomyPlanApproval); got != AutonomyPlanApproval {
		t.Errorf("empty should fall back to default, got %q", got)
	}
}

func TestTaskAuthority_MayUseTool(t *testing.T) {
	// No restrictions — everything allowed.
	open := TaskAuthority{}
	if !open.MayUseTool("any_tool") {
		t.Error("no restrictions should allow all tools")
	}

	// Allowlist only.
	allowlist := TaskAuthority{AllowedTools: []string{"read_file", "write_file"}}
	if !allowlist.MayUseTool("read_file") {
		t.Error("allowed tool should pass")
	}
	if allowlist.MayUseTool("delete_all") {
		t.Error("unlisted tool should be denied")
	}

	// Denylist.
	denylist := TaskAuthority{DeniedTools: []string{"rm_rf"}}
	if denylist.MayUseTool("rm_rf") {
		t.Error("denied tool should be blocked")
	}
	if !denylist.MayUseTool("read_file") {
		t.Error("non-denied tool should be allowed")
	}

	// Deny overrides allow.
	both := TaskAuthority{
		AllowedTools: []string{"dangerous"},
		DeniedTools:  []string{"dangerous"},
	}
	if both.MayUseTool("dangerous") {
		t.Error("deny should override allow")
	}
}

func TestTaskAuthority_MayDelegateTo(t *testing.T) {
	// Can't delegate at all.
	noDelegation := TaskAuthority{CanDelegate: false}
	if noDelegation.MayDelegateTo("worker") {
		t.Error("CanDelegate=false should block all delegation")
	}

	// Can delegate to anyone.
	openDelegation := TaskAuthority{CanDelegate: true}
	if !openDelegation.MayDelegateTo("any-agent") {
		t.Error("CanDelegate=true with no restriction should allow all")
	}

	// Restricted delegation.
	restricted := TaskAuthority{
		CanDelegate:   true,
		AllowedAgents: []string{"builder", "reviewer"},
	}
	if !restricted.MayDelegateTo("builder") {
		t.Error("listed agent should be allowed")
	}
	if restricted.MayDelegateTo("hacker") {
		t.Error("unlisted agent should be denied")
	}
}

// --- DefaultAuthority tests ---

func TestDefaultAuthority_AllModes(t *testing.T) {
	modes := []AutonomyMode{AutonomyFull, AutonomyPlanApproval, AutonomyStepApproval, AutonomySupervised}
	for _, mode := range modes {
		auth := DefaultAuthority(mode)
		if auth.AutonomyMode != mode {
			t.Errorf("DefaultAuthority(%s).AutonomyMode = %q", mode, auth.AutonomyMode)
		}
		if err := auth.Validate(); err != nil {
			t.Errorf("DefaultAuthority(%s) invalid: %v", mode, err)
		}
	}
}

func TestDefaultAuthority_SupervisedRestrictions(t *testing.T) {
	auth := DefaultAuthority(AutonomySupervised)
	if auth.CanAct {
		t.Error("supervised should not allow autonomous action")
	}
	if auth.CanDelegate {
		t.Error("supervised should not allow delegation")
	}
	if !auth.EscalationRequired {
		t.Error("supervised should require escalation")
	}
	if auth.RiskClass != RiskClassHigh {
		t.Errorf("supervised risk_class = %q, want high", auth.RiskClass)
	}
}

func TestDefaultAuthority_FullPermissions(t *testing.T) {
	auth := DefaultAuthority(AutonomyFull)
	if !auth.CanAct || !auth.CanDelegate || !auth.CanEscalate {
		t.Error("full autonomy should allow act, delegate, escalate")
	}
	if auth.RiskClass != RiskClassLow {
		t.Errorf("full risk_class = %q, want low", auth.RiskClass)
	}
}

// --- AgentPolicy config defaults tests ---

func TestAgentPolicy_EffectiveDefaultAutonomy(t *testing.T) {
	// Empty config defaults to full.
	empty := AgentPolicy{}
	if got := empty.EffectiveDefaultAutonomy(); got != AutonomyFull {
		t.Errorf("empty policy default = %q, want full", got)
	}

	// Explicit config.
	explicit := AgentPolicy{DefaultAutonomy: AutonomySupervised}
	if got := explicit.EffectiveDefaultAutonomy(); got != AutonomySupervised {
		t.Errorf("explicit policy = %q, want supervised", got)
	}

	// Invalid config falls back.
	invalid := AgentPolicy{DefaultAutonomy: "bogus"}
	if got := invalid.EffectiveDefaultAutonomy(); got != AutonomyFull {
		t.Errorf("invalid policy = %q, want full fallback", got)
	}
}

func TestAgentPolicy_EffectiveDefaultAuthority(t *testing.T) {
	// With explicit authority.
	auth := TaskAuthority{AutonomyMode: AutonomySupervised, CanAct: false}
	policy := AgentPolicy{DefaultAuthority: &auth}
	got := policy.EffectiveDefaultAuthority()
	if got.AutonomyMode != AutonomySupervised || got.CanAct {
		t.Errorf("explicit authority not returned: %+v", got)
	}

	// Without explicit authority — derives from mode.
	derived := AgentPolicy{DefaultAutonomy: AutonomyPlanApproval}
	got = derived.EffectiveDefaultAuthority()
	if got.AutonomyMode != AutonomyPlanApproval {
		t.Errorf("derived authority mode = %q", got.AutonomyMode)
	}
}

// --- Legacy ApprovalMode migration tests ---

func TestTaskAuthority_Normalize_MigratesLegacyApprovalMode(t *testing.T) {
	tests := []struct {
		legacy string
		want   AutonomyMode
	}{
		{"act_with_approval", AutonomyPlanApproval},
		{"approval", AutonomyPlanApproval},
		{"plan_approval", AutonomyPlanApproval},
		{"step_approval", AutonomyStepApproval},
		{"supervised", AutonomySupervised},
		{"observe_only", AutonomySupervised},
		{"recommend_only", AutonomySupervised},
		{"autonomous", AutonomyFull},
		{"full", AutonomyFull},
		{"bounded_autonomous", AutonomyFull},
		{"fully_autonomous", AutonomyFull},
		{"unknown_legacy_value", AutonomyPlanApproval}, // safe default
	}
	for _, tt := range tests {
		a := TaskAuthority{ApprovalMode: tt.legacy}
		n := a.Normalize()
		if n.AutonomyMode != tt.want {
			t.Errorf("migrate(%q) = %q, want %q", tt.legacy, n.AutonomyMode, tt.want)
		}
		if n.ApprovalMode != "" {
			t.Errorf("ApprovalMode should be cleared after migration, got %q", n.ApprovalMode)
		}
	}
}

func TestTaskAuthority_Normalize_NewFieldTakesPrecedence(t *testing.T) {
	// When both AutonomyMode and ApprovalMode are set, AutonomyMode wins.
	a := TaskAuthority{
		AutonomyMode: AutonomySupervised,
		ApprovalMode: "act_with_approval",
	}
	n := a.Normalize()
	if n.AutonomyMode != AutonomySupervised {
		t.Errorf("AutonomyMode should take precedence, got %q", n.AutonomyMode)
	}
}

func TestTaskAuthority_LegacyJSON_Unmarshal(t *testing.T) {
	// Simulate persisted JSON from before the migration.
	legacyJSON := `{
		"role": "director",
		"approval_mode": "act_with_approval",
		"can_act": true,
		"can_delegate": true,
		"risk_class": "medium"
	}`
	var auth TaskAuthority
	if err := json.Unmarshal([]byte(legacyJSON), &auth); err != nil {
		t.Fatalf("Unmarshal legacy: %v", err)
	}
	// Before normalization, AutonomyMode is empty but ApprovalMode is populated.
	if auth.ApprovalMode != "act_with_approval" {
		t.Errorf("ApprovalMode = %q, want act_with_approval", auth.ApprovalMode)
	}
	if auth.AutonomyMode != "" {
		t.Errorf("AutonomyMode should be empty before normalization, got %q", auth.AutonomyMode)
	}
	// After normalization, legacy is migrated.
	auth = auth.Normalize()
	if auth.AutonomyMode != AutonomyPlanApproval {
		t.Errorf("migrated AutonomyMode = %q, want plan_approval", auth.AutonomyMode)
	}
	if auth.ApprovalMode != "" {
		t.Errorf("ApprovalMode should be cleared, got %q", auth.ApprovalMode)
	}
}

// --- JSON round-trip ---

func TestTaskAuthority_JSONRoundTrip(t *testing.T) {
	auth := TaskAuthority{
		AutonomyMode:       AutonomyPlanApproval,
		Role:               "engineer",
		RiskClass:          RiskClassMedium,
		CanAct:             true,
		CanDelegate:        true,
		AllowedAgents:      []string{"builder"},
		AllowedTools:       []string{"read_file"},
		DeniedTools:        []string{"rm_rf"},
		MaxDelegationDepth: 2,
	}
	blob, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded TaskAuthority
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.AutonomyMode != AutonomyPlanApproval {
		t.Errorf("AutonomyMode = %q", decoded.AutonomyMode)
	}
	if decoded.RiskClass != RiskClassMedium {
		t.Errorf("RiskClass = %q", decoded.RiskClass)
	}
	if len(decoded.AllowedTools) != 1 || decoded.AllowedTools[0] != "read_file" {
		t.Errorf("AllowedTools = %v", decoded.AllowedTools)
	}
	if len(decoded.DeniedTools) != 1 || decoded.DeniedTools[0] != "rm_rf" {
		t.Errorf("DeniedTools = %v", decoded.DeniedTools)
	}
}

func TestRiskClass_JSONRoundTrip(t *testing.T) {
	type wrapper struct {
		Risk RiskClass `json:"risk"`
	}
	w := wrapper{Risk: RiskClassCritical}
	blob, _ := json.Marshal(w)
	var decoded wrapper
	json.Unmarshal(blob, &decoded)
	if decoded.Risk != RiskClassCritical {
		t.Errorf("RiskClass = %q", decoded.Risk)
	}
}
