package planner

import (
	"encoding/json"
	"strings"
	"testing"

	"metiq/internal/store/state"
)

// ── NarrowAuthority tests ──────────────────────────────────────────────────────

func TestNarrowAuthority_AutonomyMoreRestrictive(t *testing.T) {
	parent := state.TaskAuthority{AutonomyMode: state.AutonomyFull, CanAct: true}
	child := state.TaskAuthority{AutonomyMode: state.AutonomyStepApproval, CanAct: true}
	got := NarrowAuthority(parent, child)
	if got.AutonomyMode != state.AutonomyStepApproval {
		t.Errorf("expected step_approval, got %q", got.AutonomyMode)
	}
}

func TestNarrowAuthority_AutonomyParentStricter(t *testing.T) {
	parent := state.TaskAuthority{AutonomyMode: state.AutonomySupervised, CanAct: true}
	child := state.TaskAuthority{AutonomyMode: state.AutonomyFull, CanAct: true}
	got := NarrowAuthority(parent, child)
	if got.AutonomyMode != state.AutonomySupervised {
		t.Errorf("parent supervised should win, got %q", got.AutonomyMode)
	}
}

func TestNarrowAuthority_AutonomyInheritedFromParent(t *testing.T) {
	parent := state.TaskAuthority{AutonomyMode: state.AutonomyPlanApproval}
	child := state.TaskAuthority{}
	got := NarrowAuthority(parent, child)
	if got.AutonomyMode != state.AutonomyPlanApproval {
		t.Errorf("expected inherited plan_approval, got %q", got.AutonomyMode)
	}
}

func TestNarrowAuthority_RiskHigherWins(t *testing.T) {
	parent := state.TaskAuthority{RiskClass: state.RiskClassMedium}
	child := state.TaskAuthority{RiskClass: state.RiskClassHigh}
	got := NarrowAuthority(parent, child)
	if got.RiskClass != state.RiskClassHigh {
		t.Errorf("expected high, got %q", got.RiskClass)
	}
}

func TestNarrowAuthority_RiskInherited(t *testing.T) {
	parent := state.TaskAuthority{RiskClass: state.RiskClassCritical}
	child := state.TaskAuthority{}
	got := NarrowAuthority(parent, child)
	if got.RiskClass != state.RiskClassCritical {
		t.Errorf("expected inherited critical, got %q", got.RiskClass)
	}
}

func TestNarrowAuthority_BooleanGates(t *testing.T) {
	parent := state.TaskAuthority{
		CanAct: true, CanDelegate: false, CanEscalate: true,
		EscalationRequired: false,
	}
	child := state.TaskAuthority{
		CanAct: true, CanDelegate: true, CanEscalate: false,
		EscalationRequired: true,
	}
	got := NarrowAuthority(parent, child)
	if got.CanAct != true {
		t.Error("CanAct: both true should remain true")
	}
	if got.CanDelegate != false {
		t.Error("CanDelegate: parent false should prevail")
	}
	if got.CanEscalate != false {
		t.Error("CanEscalate: child false should prevail")
	}
	if got.EscalationRequired != true {
		t.Error("EscalationRequired: either true should prevail")
	}
}

func TestNarrowAuthority_ParentCanActFalse(t *testing.T) {
	parent := state.TaskAuthority{CanAct: false}
	child := state.TaskAuthority{CanAct: true}
	got := NarrowAuthority(parent, child)
	if got.CanAct != false {
		t.Error("parent CanAct=false must override child true")
	}
}

func TestNarrowAuthority_AllowedToolsIntersection(t *testing.T) {
	parent := state.TaskAuthority{AllowedTools: []string{"read", "write", "exec"}}
	child := state.TaskAuthority{AllowedTools: []string{"read", "exec", "delete"}}
	got := NarrowAuthority(parent, child)
	if len(got.AllowedTools) != 2 {
		t.Fatalf("expected 2 tools, got %v", got.AllowedTools)
	}
	toolSet := map[string]bool{}
	for _, tool := range got.AllowedTools {
		toolSet[tool] = true
	}
	if !toolSet["read"] || !toolSet["exec"] {
		t.Errorf("expected {read, exec}, got %v", got.AllowedTools)
	}
}

func TestNarrowAuthority_AllowedToolsInherited(t *testing.T) {
	parent := state.TaskAuthority{AllowedTools: []string{"read"}}
	child := state.TaskAuthority{}
	got := NarrowAuthority(parent, child)
	if len(got.AllowedTools) != 1 || got.AllowedTools[0] != "read" {
		t.Errorf("expected inherited [read], got %v", got.AllowedTools)
	}
}

func TestNarrowAuthority_DeniedToolsUnion(t *testing.T) {
	parent := state.TaskAuthority{DeniedTools: []string{"rm", "exec"}}
	child := state.TaskAuthority{DeniedTools: []string{"exec", "deploy"}}
	got := NarrowAuthority(parent, child)
	if len(got.DeniedTools) != 3 {
		t.Fatalf("expected 3 denied tools, got %v", got.DeniedTools)
	}
}

func TestNarrowAuthority_AllowedAgentsIntersection(t *testing.T) {
	parent := state.TaskAuthority{AllowedAgents: []string{"a", "b", "c"}, CanDelegate: true}
	child := state.TaskAuthority{AllowedAgents: []string{"b", "c", "d"}, CanDelegate: true}
	got := NarrowAuthority(parent, child)
	if len(got.AllowedAgents) != 2 {
		t.Fatalf("expected 2 agents, got %v", got.AllowedAgents)
	}
}

func TestNarrowAuthority_MaxDelegationDepth(t *testing.T) {
	tests := []struct {
		name           string
		parent, child  int
		want           int
	}{
		{"both set, parent smaller", 2, 5, 2},
		{"both set, child smaller", 5, 3, 3},
		{"parent unlimited", 0, 4, 4},
		{"child unlimited", 3, 0, 3},
		{"both unlimited", 0, 0, 0},
		{"equal", 3, 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NarrowAuthority(
				state.TaskAuthority{MaxDelegationDepth: tt.parent},
				state.TaskAuthority{MaxDelegationDepth: tt.child},
			)
			if got.MaxDelegationDepth != tt.want {
				t.Errorf("narrowDepth(%d, %d) = %d, want %d",
					tt.parent, tt.child, got.MaxDelegationDepth, tt.want)
			}
		})
	}
}

func TestNarrowAuthority_RoleInherited(t *testing.T) {
	parent := state.TaskAuthority{Role: "engineer"}
	child := state.TaskAuthority{}
	got := NarrowAuthority(parent, child)
	if got.Role != "engineer" {
		t.Errorf("expected inherited role, got %q", got.Role)
	}
}

func TestNarrowAuthority_RoleChildOverrides(t *testing.T) {
	parent := state.TaskAuthority{Role: "engineer"}
	child := state.TaskAuthority{Role: "reviewer"}
	got := NarrowAuthority(parent, child)
	if got.Role != "reviewer" {
		t.Errorf("child role should override, got %q", got.Role)
	}
}

func TestNarrowAuthority_ClearsDeprecatedApprovalMode(t *testing.T) {
	parent := state.TaskAuthority{ApprovalMode: "legacy"}
	child := state.TaskAuthority{ApprovalMode: "old"}
	got := NarrowAuthority(parent, child)
	if got.ApprovalMode != "" {
		t.Errorf("ApprovalMode should be cleared, got %q", got.ApprovalMode)
	}
}

func TestNarrowAuthority_BothEmpty(t *testing.T) {
	got := NarrowAuthority(state.TaskAuthority{}, state.TaskAuthority{})
	if got.AutonomyMode != "" || got.RiskClass != "" {
		t.Errorf("narrowing two empty authorities should be empty: %+v", got)
	}
}

// ── Set operation tests ────────────────────────────────────────────────────────

func TestIntersectStrings_BothEmpty(t *testing.T) {
	got := intersectStrings(nil, nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestIntersectStrings_OneEmpty(t *testing.T) {
	got := intersectStrings([]string{"a"}, nil)
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("expected [a], got %v", got)
	}
}

func TestIntersectStrings_NoOverlap(t *testing.T) {
	got := intersectStrings([]string{"a"}, []string{"b"})
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestUnionStrings_NoDuplicates(t *testing.T) {
	got := unionStrings([]string{"a", "b"}, []string{"b", "c"})
	if len(got) != 3 {
		t.Errorf("expected 3 elements, got %v", got)
	}
}

func TestUnionStrings_BothEmpty(t *testing.T) {
	got := unionStrings(nil, nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// ── ResolveAuthority tests ─────────────────────────────────────────────────────

func TestResolveAuthority_EmptyLayers(t *testing.T) {
	trace := ResolveAuthority()
	if len(trace.Layers) != 0 {
		t.Error("expected empty layers")
	}
	if trace.Effective.AutonomyMode != "" {
		t.Error("expected zero effective authority")
	}
}

func TestResolveAuthority_SingleLayer(t *testing.T) {
	layer := AuthorityLayer{
		Source: AuthSourceConfig,
		Authority: state.TaskAuthority{
			AutonomyMode: state.AutonomyFull,
			CanAct:       true,
			RiskClass:    state.RiskClassLow,
		},
	}
	trace := ResolveAuthority(layer)
	if trace.Effective.AutonomyMode != state.AutonomyFull {
		t.Errorf("single layer should pass through, got %q", trace.Effective.AutonomyMode)
	}
	if len(trace.Narrowed) != 0 {
		t.Errorf("no narrowing expected, got %d records", len(trace.Narrowed))
	}
}

func TestResolveAuthority_ThreeLayerNarrowing(t *testing.T) {
	config := AuthorityLayer{
		Source: AuthSourceConfig,
		Authority: state.TaskAuthority{
			AutonomyMode:       state.AutonomyFull,
			CanAct:             true,
			CanDelegate:        true,
			RiskClass:          state.RiskClassLow,
			MaxDelegationDepth: 5,
			AllowedTools:       []string{"read", "write", "exec", "deploy"},
		},
	}
	goal := AuthorityLayer{
		Source: AuthSourceGoal,
		Authority: state.TaskAuthority{
			AutonomyMode: state.AutonomyPlanApproval,
			RiskClass:    state.RiskClassMedium,
			AllowedTools: []string{"read", "write", "exec"},
			DeniedTools:  []string{"deploy"},
		},
	}
	task := AuthorityLayer{
		Source: AuthSourceTask,
		Authority: state.TaskAuthority{
			AutonomyMode:       state.AutonomyStepApproval,
			AllowedTools:       []string{"read", "write"},
			MaxDelegationDepth: 2,
		},
	}

	trace := ResolveAuthority(config, goal, task)

	eff := trace.Effective
	if eff.AutonomyMode != state.AutonomyStepApproval {
		t.Errorf("autonomy: want step_approval, got %q", eff.AutonomyMode)
	}
	if eff.RiskClass != state.RiskClassMedium {
		t.Errorf("risk: want medium, got %q", eff.RiskClass)
	}
	if len(eff.AllowedTools) != 2 {
		t.Errorf("allowed tools: want 2, got %v", eff.AllowedTools)
	}
	if len(eff.DeniedTools) != 1 || eff.DeniedTools[0] != "deploy" {
		t.Errorf("denied tools: want [deploy], got %v", eff.DeniedTools)
	}
	if eff.MaxDelegationDepth != 2 {
		t.Errorf("depth: want 2, got %d", eff.MaxDelegationDepth)
	}
	if len(trace.Narrowed) == 0 {
		t.Error("expected narrowing records")
	}
}

func TestResolveAuthority_DelegationLayerApplied(t *testing.T) {
	config := AuthorityLayer{
		Source: AuthSourceConfig,
		Authority: state.TaskAuthority{
			AutonomyMode: state.AutonomyFull,
			CanAct:       true,
			CanDelegate:  true,
			RiskClass:    state.RiskClassLow,
		},
	}
	delegation := AuthorityLayer{
		Source: AuthSourceDelegation,
		Label:  "remote-parent",
		Authority: state.TaskAuthority{
			AutonomyMode: state.AutonomySupervised,
			CanDelegate:  false,
			RiskClass:    state.RiskClassHigh,
		},
	}
	trace := ResolveAuthority(config, delegation)

	if trace.Effective.AutonomyMode != state.AutonomySupervised {
		t.Errorf("delegation should narrow to supervised, got %q", trace.Effective.AutonomyMode)
	}
	if trace.Effective.CanDelegate != false {
		t.Error("delegation should prevent further delegation")
	}
	if trace.Effective.RiskClass != state.RiskClassHigh {
		t.Errorf("risk should be high, got %q", trace.Effective.RiskClass)
	}
}

func TestResolveAuthority_NormalizesLegacy(t *testing.T) {
	layer := AuthorityLayer{
		Source: AuthSourceConfig,
		Authority: state.TaskAuthority{
			ApprovalMode: "act_with_approval",
			CanAct:       true,
		},
	}
	trace := ResolveAuthority(layer)
	if trace.Effective.AutonomyMode != state.AutonomyPlanApproval {
		t.Errorf("legacy approval_mode should be migrated, got %q", trace.Effective.AutonomyMode)
	}
}

// ── BuildAuthorityChain tests ──────────────────────────────────────────────────

func TestBuildAuthorityChain_AllLayers(t *testing.T) {
	config := state.TaskAuthority{AutonomyMode: state.AutonomyFull, CanAct: true}
	goal := &state.GoalSpec{
		GoalID:    "g1",
		Authority: state.TaskAuthority{RiskClass: state.RiskClassMedium},
	}
	parent := &state.TaskSpec{
		TaskID:    "t-parent",
		Authority: state.TaskAuthority{AutonomyMode: state.AutonomyPlanApproval},
	}
	task := state.TaskSpec{
		TaskID:    "t-child",
		Authority: state.TaskAuthority{MaxDelegationDepth: 2},
	}
	layers := BuildAuthorityChain(config, goal, parent, task)
	if len(layers) != 4 {
		t.Fatalf("expected 4 layers, got %d", len(layers))
	}
	if layers[0].Source != AuthSourceConfig {
		t.Errorf("layer 0: want config, got %s", layers[0].Source)
	}
	if layers[1].Source != AuthSourceGoal {
		t.Errorf("layer 1: want goal, got %s", layers[1].Source)
	}
	if layers[2].Source != AuthSourceParent {
		t.Errorf("layer 2: want parent_task, got %s", layers[2].Source)
	}
	if layers[3].Source != AuthSourceTask {
		t.Errorf("layer 3: want task, got %s", layers[3].Source)
	}
}

func TestBuildAuthorityChain_SkipsZeroLayers(t *testing.T) {
	config := state.TaskAuthority{AutonomyMode: state.AutonomyFull}
	task := state.TaskSpec{TaskID: "t1"} // zero authority
	layers := BuildAuthorityChain(config, nil, nil, task)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer (config only), got %d", len(layers))
	}
}

func TestBuildAuthorityChain_NilGoalAndParent(t *testing.T) {
	config := state.TaskAuthority{AutonomyMode: state.AutonomyFull}
	task := state.TaskSpec{
		TaskID:    "t1",
		Authority: state.TaskAuthority{RiskClass: state.RiskClassHigh},
	}
	layers := BuildAuthorityChain(config, nil, nil, task)
	if len(layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(layers))
	}
}

// ── End-to-end: BuildAuthorityChain + ResolveAuthority ─────────────────────────

func TestResolveAuthority_EndToEnd_InheritanceNarrowing(t *testing.T) {
	config := state.TaskAuthority{
		AutonomyMode:       state.AutonomyFull,
		CanAct:             true,
		CanDelegate:        true,
		CanEscalate:        true,
		RiskClass:          state.RiskClassLow,
		MaxDelegationDepth: 5,
		AllowedTools:       []string{"read", "write", "exec", "deploy"},
	}
	goal := &state.GoalSpec{
		GoalID: "g1",
		Authority: state.TaskAuthority{
			AutonomyMode: state.AutonomyPlanApproval,
			RiskClass:    state.RiskClassMedium,
			DeniedTools:  []string{"deploy"},
		},
	}
	parent := &state.TaskSpec{
		TaskID: "t-parent",
		Authority: state.TaskAuthority{
			MaxDelegationDepth: 3,
			AllowedTools:       []string{"read", "write", "exec"},
		},
	}
	task := state.TaskSpec{
		TaskID: "t-child",
		Authority: state.TaskAuthority{
			AllowedTools: []string{"read", "write"},
		},
	}

	layers := BuildAuthorityChain(config, goal, parent, task)
	trace := ResolveAuthority(layers...)

	eff := trace.Effective
	// Autonomy: config=full → goal=plan_approval → result=plan_approval
	if eff.AutonomyMode != state.AutonomyPlanApproval {
		t.Errorf("autonomy: want plan_approval, got %q", eff.AutonomyMode)
	}
	// Risk: config=low → goal=medium → result=medium
	if eff.RiskClass != state.RiskClassMedium {
		t.Errorf("risk: want medium, got %q", eff.RiskClass)
	}
	// Tools: intersection(config 4, parent 3, child 2) = {read, write}
	if len(eff.AllowedTools) != 2 {
		t.Errorf("allowed tools: want 2, got %v", eff.AllowedTools)
	}
	// Denied: union of all = {deploy}
	if len(eff.DeniedTools) != 1 {
		t.Errorf("denied tools: want 1, got %v", eff.DeniedTools)
	}
	// Depth: min(5, 3) = 3
	if eff.MaxDelegationDepth != 3 {
		t.Errorf("depth: want 3, got %d", eff.MaxDelegationDepth)
	}
}

func TestResolveAuthority_CannotWidenParent(t *testing.T) {
	// Child tries to escalate permissions the parent doesn't have.
	parent := AuthorityLayer{
		Source: AuthSourceConfig,
		Authority: state.TaskAuthority{
			AutonomyMode:       state.AutonomySupervised,
			CanAct:             false,
			CanDelegate:        false,
			RiskClass:          state.RiskClassCritical,
			MaxDelegationDepth: 1,
			AllowedTools:       []string{"read"},
		},
	}
	child := AuthorityLayer{
		Source: AuthSourceTask,
		Authority: state.TaskAuthority{
			AutonomyMode:       state.AutonomyFull, // tries to widen
			CanAct:             true,                // tries to widen
			CanDelegate:        true,                // tries to widen
			RiskClass:          state.RiskClassLow,  // tries to widen
			MaxDelegationDepth: 10,                  // tries to widen
			AllowedTools:       []string{"read", "write", "exec"}, // tries to widen
		},
	}
	trace := ResolveAuthority(parent, child)
	eff := trace.Effective

	// None of the child's widenings should take effect.
	if eff.AutonomyMode != state.AutonomySupervised {
		t.Errorf("autonomy should stay supervised, got %q", eff.AutonomyMode)
	}
	if eff.CanAct {
		t.Error("CanAct should remain false")
	}
	if eff.CanDelegate {
		t.Error("CanDelegate should remain false")
	}
	if eff.RiskClass != state.RiskClassCritical {
		t.Errorf("risk should stay critical, got %q", eff.RiskClass)
	}
	if eff.MaxDelegationDepth != 1 {
		t.Errorf("depth should stay 1, got %d", eff.MaxDelegationDepth)
	}
	// AllowedTools intersection: {read} ∩ {read, write, exec} = {read}
	if len(eff.AllowedTools) != 1 || eff.AllowedTools[0] != "read" {
		t.Errorf("tools should stay [read], got %v", eff.AllowedTools)
	}
}

// ── isZeroAuthority tests ──────────────────────────────────────────────────────

func TestIsZeroAuthority_Empty(t *testing.T) {
	if !isZeroAuthority(state.TaskAuthority{}) {
		t.Error("empty authority should be zero")
	}
}

func TestIsZeroAuthority_NotEmpty(t *testing.T) {
	cases := []state.TaskAuthority{
		{AutonomyMode: state.AutonomyFull},
		{Role: "x"},
		{RiskClass: state.RiskClassLow},
		{CanAct: true},
		{AllowedTools: []string{"a"}},
		{DeniedTools: []string{"b"}},
		{AllowedAgents: []string{"c"}},
		{MaxDelegationDepth: 1},
	}
	for i, c := range cases {
		if isZeroAuthority(c) {
			t.Errorf("case %d should not be zero: %+v", i, c)
		}
	}
}

// ── Autonomy ranking tests ─────────────────────────────────────────────────────

func TestMoreRestrictiveAutonomy_AllPairs(t *testing.T) {
	ordered := []state.AutonomyMode{
		state.AutonomyFull,
		state.AutonomyPlanApproval,
		state.AutonomyStepApproval,
		state.AutonomySupervised,
	}
	for i := 0; i < len(ordered); i++ {
		for j := i; j < len(ordered); j++ {
			got := moreRestrictiveAutonomy(ordered[i], ordered[j])
			if got != ordered[j] {
				t.Errorf("moreRestrictive(%s, %s) = %s, want %s",
					ordered[i], ordered[j], got, ordered[j])
			}
			// And the reverse.
			got2 := moreRestrictiveAutonomy(ordered[j], ordered[i])
			if got2 != ordered[j] {
				t.Errorf("moreRestrictive(%s, %s) = %s, want %s",
					ordered[j], ordered[i], got2, ordered[j])
			}
		}
	}
}

func TestMoreRestrictiveAutonomy_UnknownMode(t *testing.T) {
	got := moreRestrictiveAutonomy(state.AutonomyFull, "unknown")
	// Unknown should be treated as supervised (maximally restrictive).
	if got != "unknown" {
		t.Errorf("unknown mode should win as most restrictive, got %q", got)
	}
}

func TestHigherRisk_AllPairs(t *testing.T) {
	ordered := []state.RiskClass{
		state.RiskClassLow,
		state.RiskClassMedium,
		state.RiskClassHigh,
		state.RiskClassCritical,
	}
	for i := 0; i < len(ordered); i++ {
		for j := i; j < len(ordered); j++ {
			got := higherRisk(ordered[i], ordered[j])
			if got != ordered[j] {
				t.Errorf("higherRisk(%s, %s) = %s, want %s",
					ordered[i], ordered[j], got, ordered[j])
			}
		}
	}
}

// ── FormatTrace tests ──────────────────────────────────────────────────────────

func TestFormatTrace_IncludesKeyInfo(t *testing.T) {
	trace := ResolveAuthority(
		AuthorityLayer{
			Source: AuthSourceConfig,
			Label:  "system defaults",
			Authority: state.TaskAuthority{
				AutonomyMode: state.AutonomyFull,
				CanAct:       true,
				RiskClass:    state.RiskClassLow,
			},
		},
		AuthorityLayer{
			Source: AuthSourceGoal,
			Label:  "goal-1",
			Authority: state.TaskAuthority{
				AutonomyMode: state.AutonomyPlanApproval,
				RiskClass:    state.RiskClassMedium,
			},
		},
	)
	output := FormatTrace(trace)
	if !strings.Contains(output, "config") {
		t.Error("should mention config source")
	}
	if !strings.Contains(output, "goal") {
		t.Error("should mention goal source")
	}
	if !strings.Contains(output, "Narrowing") {
		t.Error("should include narrowing section")
	}
	if !strings.Contains(output, "plan_approval") {
		t.Error("should show effective autonomy mode")
	}
}

func TestFormatTrace_NoNarrowingWhenSingleLayer(t *testing.T) {
	trace := ResolveAuthority(AuthorityLayer{
		Source:    AuthSourceConfig,
		Authority: state.TaskAuthority{AutonomyMode: state.AutonomyFull},
	})
	output := FormatTrace(trace)
	if strings.Contains(output, "Narrowing") {
		t.Error("single layer should have no narrowing section")
	}
}

// ── JSON round-trip tests ──────────────────────────────────────────────────────

func TestAuthorityTrace_JSONRoundTrip(t *testing.T) {
	trace := ResolveAuthority(
		AuthorityLayer{
			Source: AuthSourceConfig,
			Label:  "defaults",
			Authority: state.TaskAuthority{
				AutonomyMode: state.AutonomyFull,
				CanAct:       true,
				CanDelegate:  true,
				RiskClass:    state.RiskClassLow,
			},
		},
		AuthorityLayer{
			Source: AuthSourceTask,
			Label:  "task-1",
			Authority: state.TaskAuthority{
				AutonomyMode: state.AutonomyStepApproval,
				RiskClass:    state.RiskClassHigh,
			},
		},
	)

	blob, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded AuthorityTrace
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Effective.AutonomyMode != trace.Effective.AutonomyMode {
		t.Errorf("round-trip mismatch: got %q, want %q",
			decoded.Effective.AutonomyMode, trace.Effective.AutonomyMode)
	}
	if len(decoded.Layers) != len(trace.Layers) {
		t.Errorf("layers count mismatch: got %d, want %d",
			len(decoded.Layers), len(trace.Layers))
	}
	if len(decoded.Narrowed) != len(trace.Narrowed) {
		t.Errorf("narrowed count mismatch: got %d, want %d",
			len(decoded.Narrowed), len(trace.Narrowed))
	}
}

func TestNarrowingRecord_JSONRoundTrip(t *testing.T) {
	nr := NarrowingRecord{
		Source: AuthSourceGoal,
		Field:  "autonomy_mode",
		From:   "full",
		To:     "supervised",
	}
	blob, err := json.Marshal(nr)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded NarrowingRecord
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != nr {
		t.Errorf("round-trip mismatch: got %+v, want %+v", decoded, nr)
	}
}
