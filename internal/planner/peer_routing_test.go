package planner

import (
	"encoding/json"
	"testing"

	"metiq/internal/store/state"
)

// ── PeerCapability helpers ──────────────────────────────────────────────────

func TestPeerCapability_HasTool(t *testing.T) {
	p := PeerCapability{EnabledTools: []string{"nostr_fetch", "nostr_publish"}}
	if !p.HasTool("nostr_fetch") {
		t.Fatal("expected to have nostr_fetch")
	}
	if p.HasTool("nostr_zap") {
		t.Fatal("should not have nostr_zap")
	}
}

func TestPeerCapability_HasTool_EmptyMeansAll(t *testing.T) {
	p := PeerCapability{}
	if !p.HasTool("anything") {
		t.Fatal("empty EnabledTools should mean all tools available")
	}
}

func TestPeerCapability_HasTag(t *testing.T) {
	p := PeerCapability{Tags: []string{"Reviewer", "Coder"}}
	if !p.HasTag("reviewer") {
		t.Fatal("expected case-insensitive tag match")
	}
	if p.HasTag("designer") {
		t.Fatal("should not have designer tag")
	}
}

// ── Basic routing ───────────────────────────────────────────────────────────

func TestRoutePeers_NoPeers(t *testing.T) {
	result := RoutePeers(nil, TaskRequirements{})
	if result.Selected != nil {
		t.Fatal("expected no selection with no peers")
	}
	if result.Explanation != "no eligible peer found" {
		t.Fatalf("unexpected explanation: %s", result.Explanation)
	}
}

func TestRoutePeers_SingleEligible(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "agent-1", Available: true},
	}
	result := RoutePeers(peers, TaskRequirements{})
	if result.Selected == nil {
		t.Fatal("expected a selection")
	}
	if result.Selected.AgentID != "agent-1" {
		t.Fatalf("expected agent-1, got %s", result.Selected.AgentID)
	}
}

func TestRoutePeers_UnavailableRejected(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "offline", Available: false},
	}
	result := RoutePeers(peers, TaskRequirements{})
	if result.Selected != nil {
		t.Fatal("unavailable peer should not be selected")
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Eligible {
		t.Fatal("expected candidate to be ineligible")
	}
}

// ── Tool requirements ───────────────────────────────────────────────────────

func TestRoutePeers_RequiredTool_Match(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, EnabledTools: []string{"nostr_fetch", "nostr_publish"}},
	}
	reqs := TaskRequirements{RequiredTools: []string{"nostr_fetch"}}
	result := RoutePeers(peers, reqs)
	if result.Selected == nil {
		t.Fatal("expected selection")
	}
}

func TestRoutePeers_RequiredTool_Missing(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, EnabledTools: []string{"nostr_publish"}},
	}
	reqs := TaskRequirements{RequiredTools: []string{"nostr_zap"}}
	result := RoutePeers(peers, reqs)
	if result.Selected != nil {
		t.Fatal("peer missing required tool should not be selected")
	}
}

// ── Tag requirements ────────────────────────────────────────────────────────

func TestRoutePeers_RequiredTag_Match(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, Tags: []string{"reviewer", "coder"}},
	}
	reqs := TaskRequirements{RequiredTags: []string{"reviewer"}}
	result := RoutePeers(peers, reqs)
	if result.Selected == nil || result.Selected.AgentID != "a1" {
		t.Fatal("expected a1 selected")
	}
}

func TestRoutePeers_RequiredTag_Missing(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, Tags: []string{"coder"}},
	}
	reqs := TaskRequirements{RequiredTags: []string{"reviewer"}}
	result := RoutePeers(peers, reqs)
	if result.Selected != nil {
		t.Fatal("peer missing required tag should not be selected")
	}
}

// ── Autonomy requirements ───────────────────────────────────────────────────

func TestRoutePeers_Autonomy_Sufficient(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, AutonomyMode: state.AutonomyFull},
	}
	reqs := TaskRequirements{MinAutonomy: state.AutonomyPlanApproval}
	result := RoutePeers(peers, reqs)
	if result.Selected == nil {
		t.Fatal("full autonomy meets plan_approval requirement")
	}
}

func TestRoutePeers_Autonomy_Insufficient(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, AutonomyMode: state.AutonomySupervised},
	}
	reqs := TaskRequirements{MinAutonomy: state.AutonomyFull}
	result := RoutePeers(peers, reqs)
	if result.Selected != nil {
		t.Fatal("supervised should not meet full autonomy requirement")
	}
}

// ── Delegation requirement ──────────────────────────────────────────────────

func TestRoutePeers_NeedsDelegation(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, CanDelegate: false},
		{AgentID: "a2", Available: true, CanDelegate: true},
	}
	reqs := TaskRequirements{NeedsDelegation: true}
	result := RoutePeers(peers, reqs)
	if result.Selected == nil || result.Selected.AgentID != "a2" {
		t.Fatal("expected a2 (can delegate) selected")
	}
}

// ── Verification requirement ────────────────────────────────────────────────

func TestRoutePeers_NeedsVerification(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, CanVerify: false},
		{AgentID: "a2", Available: true, CanVerify: true},
	}
	reqs := TaskRequirements{NeedsVerification: true}
	result := RoutePeers(peers, reqs)
	if result.Selected == nil || result.Selected.AgentID != "a2" {
		t.Fatal("expected a2 (can verify) selected")
	}
}

// ── Budget requirements ─────────────────────────────────────────────────────

func TestRoutePeers_TokenBudget_Sufficient(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, MaxTokens: 10000},
	}
	reqs := TaskRequirements{MinTokenBudget: 5000}
	result := RoutePeers(peers, reqs)
	if result.Selected == nil {
		t.Fatal("expected selection with sufficient budget")
	}
}

func TestRoutePeers_TokenBudget_Insufficient(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, MaxTokens: 1000},
	}
	reqs := TaskRequirements{MinTokenBudget: 5000}
	result := RoutePeers(peers, reqs)
	if result.Selected != nil {
		t.Fatal("expected no selection with insufficient budget")
	}
}

func TestRoutePeers_TokenBudget_ZeroMeansUnlimited(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, MaxTokens: 0}, // unknown/unlimited
	}
	reqs := TaskRequirements{MinTokenBudget: 5000}
	result := RoutePeers(peers, reqs)
	if result.Selected == nil {
		t.Fatal("zero MaxTokens should not reject (unknown capacity)")
	}
}

// ── Risk class ───────────────────────────────────────────────────���──────────

func TestRoutePeers_RiskClass_OK(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, RiskClass: state.RiskClassLow},
	}
	reqs := TaskRequirements{MaxRiskClass: state.RiskClassMedium}
	result := RoutePeers(peers, reqs)
	if result.Selected == nil {
		t.Fatal("low risk should pass medium max")
	}
}

func TestRoutePeers_RiskClass_TooHigh(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true, RiskClass: state.RiskClassCritical},
	}
	reqs := TaskRequirements{MaxRiskClass: state.RiskClassMedium}
	result := RoutePeers(peers, reqs)
	if result.Selected != nil {
		t.Fatal("critical risk should not pass medium max")
	}
}

// ── AllowedAgents filter ────────────────────────────────────────────────────

func TestRoutePeers_AllowedAgents(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "a1", Available: true},
		{AgentID: "a2", Available: true},
	}
	reqs := TaskRequirements{AllowedAgents: []string{"a2"}}
	result := RoutePeers(peers, reqs)
	if result.Selected == nil || result.Selected.AgentID != "a2" {
		t.Fatal("expected only a2 eligible")
	}
	// a1 should be rejected.
	for _, c := range result.Candidates {
		if c.AgentID == "a1" && c.Eligible {
			t.Fatal("a1 should be rejected by AllowedAgents")
		}
	}
}

// ── Multi-peer selection ────────────────────────────────────────────────────

func TestRoutePeers_SelectsBest(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "basic", Available: true},
		{AgentID: "capable", Available: true, CanDelegate: true, CanVerify: true},
	}
	reqs := TaskRequirements{}
	result := RoutePeers(peers, reqs)
	if result.Selected == nil {
		t.Fatal("expected selection")
	}
	// "capable" should score higher due to bonus capabilities.
	if result.Selected.AgentID != "capable" {
		t.Fatalf("expected capable (higher score), got %s", result.Selected.AgentID)
	}
}

func TestRoutePeers_Compatible_Incompatible_Unknown(t *testing.T) {
	peers := []PeerCapability{
		{AgentID: "compatible", Available: true, Tags: []string{"reviewer"},
			EnabledTools: []string{"verify"}, CanVerify: true},
		{AgentID: "incompatible", Available: true, Tags: []string{"coder"},
			EnabledTools: []string{"code"}},
		{AgentID: "unknown", Available: true}, // no tools/tags declared
	}
	reqs := TaskRequirements{
		RequiredTools: []string{"verify"},
		RequiredTags:  []string{"reviewer"},
	}
	result := RoutePeers(peers, reqs)
	if result.Selected == nil || result.Selected.AgentID != "compatible" {
		t.Fatal("expected compatible selected")
	}

	for _, c := range result.Candidates {
		switch c.AgentID {
		case "compatible":
			if !c.Eligible {
				t.Fatal("compatible should be eligible")
			}
		case "incompatible":
			if c.Eligible {
				t.Fatal("incompatible should be rejected")
			}
		case "unknown":
			// Unknown has no EnabledTools (= all) and no tags.
			// Should be rejected for missing tag.
			if c.Eligible {
				t.Fatal("unknown should be rejected for missing tag")
			}
		}
	}
}

// ── Formatting ──────────────────────────────────────────────────────────────

func TestFormatRoutingResult(t *testing.T) {
	result := RoutingResult{
		Selected: &RoutingDecision{AgentID: "a1", Score: 0.8, Eligible: true},
		Candidates: []RoutingDecision{
			{AgentID: "a1", Score: 0.8, Eligible: true, Reasons: []string{"has tool"}},
			{AgentID: "a2", Score: 0, Eligible: false, Rejections: []string{"missing tool"}},
		},
		Explanation: "selected a1 (score=0.80)",
	}
	s := FormatRoutingResult(result)
	if s == "" {
		t.Fatal("expected non-empty")
	}
}

func TestFormatRoutingDecision_Eligible(t *testing.T) {
	d := RoutingDecision{AgentID: "a1", Eligible: true, Score: 0.8, Reasons: []string{"has tool"}}
	s := FormatRoutingDecision(d)
	if s == "" {
		t.Fatal("expected non-empty")
	}
}

func TestFormatRoutingDecision_Rejected(t *testing.T) {
	d := RoutingDecision{AgentID: "a1", Eligible: false, Rejections: []string{"missing tool"}}
	s := FormatRoutingDecision(d)
	if s == "" {
		t.Fatal("expected non-empty")
	}
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestPeerCapability_JSON(t *testing.T) {
	p := PeerCapability{
		AgentID: "a1", Name: "Agent One", Model: "claude-4",
		EnabledTools: []string{"fetch", "publish"}, Tags: []string{"reviewer"},
		CanDelegate: true, CanVerify: true, MaxTokens: 10000,
		Available: true, AutonomyMode: state.AutonomyFull,
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var p2 PeerCapability
	if err := json.Unmarshal(data, &p2); err != nil {
		t.Fatal(err)
	}
	if p2.AgentID != "a1" || !p2.CanDelegate || p2.MaxTokens != 10000 {
		t.Fatalf("round-trip mismatch: %+v", p2)
	}
}

func TestTaskRequirements_JSON(t *testing.T) {
	r := TaskRequirements{
		RequiredTools:     []string{"verify"},
		RequiredTags:      []string{"reviewer"},
		MinAutonomy:       state.AutonomyPlanApproval,
		NeedsDelegation:   true,
		MinTokenBudget:    5000,
		MaxRiskClass:      state.RiskClassMedium,
		AllowedAgents:     []string{"a1", "a2"},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var r2 TaskRequirements
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatal(err)
	}
	if len(r2.RequiredTools) != 1 || r2.MinAutonomy != state.AutonomyPlanApproval {
		t.Fatalf("round-trip mismatch: %+v", r2)
	}
}

func TestRoutingResult_JSON(t *testing.T) {
	r := RoutingResult{
		Selected: &RoutingDecision{AgentID: "a1", Eligible: true, Score: 0.9},
		Candidates: []RoutingDecision{
			{AgentID: "a1", Eligible: true, Score: 0.9},
		},
		Explanation: "selected a1",
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var r2 RoutingResult
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatal(err)
	}
	if r2.Selected == nil || r2.Selected.AgentID != "a1" {
		t.Fatalf("round-trip mismatch")
	}
}

// ── End-to-end ──────────────────────────────────────────────────────────────

func TestEndToEnd_RoutingPipeline(t *testing.T) {
	// 4 peers with varying capabilities.
	peers := []PeerCapability{
		{
			AgentID: "researcher", Available: true,
			Tags: []string{"researcher"}, EnabledTools: []string{"web_search", "nostr_fetch"},
			AutonomyMode: state.AutonomyFull, MaxTokens: 50000,
		},
		{
			AgentID: "reviewer", Available: true,
			Tags: []string{"reviewer"}, EnabledTools: []string{"verify", "nostr_fetch"},
			CanVerify: true, AutonomyMode: state.AutonomyPlanApproval, MaxTokens: 20000,
		},
		{
			AgentID: "coder", Available: true,
			Tags: []string{"coder"}, EnabledTools: []string{"code", "test"},
			CanDelegate: true, AutonomyMode: state.AutonomyFull, MaxTokens: 100000,
		},
		{
			AgentID: "offline", Available: false,
			Tags: []string{"reviewer", "coder"}, CanVerify: true,
		},
	}

	// Task needs a reviewer with verification capability.
	reqs := TaskRequirements{
		RequiredTags:      []string{"reviewer"},
		NeedsVerification: true,
		MinTokenBudget:    10000,
	}

	result := RoutePeers(peers, reqs)
	if result.Selected == nil {
		t.Fatal("expected selection")
	}
	if result.Selected.AgentID != "reviewer" {
		t.Fatalf("expected reviewer, got %s", result.Selected.AgentID)
	}

	// Verify decisions are explainable.
	for _, c := range result.Candidates {
		switch c.AgentID {
		case "reviewer":
			if !c.Eligible {
				t.Fatal("reviewer should be eligible")
			}
		case "researcher":
			if c.Eligible {
				t.Fatal("researcher should be rejected (no reviewer tag, no verify)")
			}
		case "coder":
			if c.Eligible {
				t.Fatal("coder should be rejected (no reviewer tag, no verify)")
			}
		case "offline":
			if c.Eligible {
				t.Fatal("offline should be rejected")
			}
		}
	}

	// Formatting works.
	s := FormatRoutingResult(result)
	if s == "" {
		t.Fatal("expected non-empty format")
	}
}
