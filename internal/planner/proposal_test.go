package planner

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"metiq/internal/store/state"
)

// ── Model validation tests ─────────────────────────────────────────────────────

func TestPolicyProposal_Validate_Valid(t *testing.T) {
	p := state.PolicyProposal{
		ProposalID:    "prop-1",
		Kind:          state.ProposalKindPrompt,
		Status:        state.ProposalStatusDraft,
		Title:         "Improve safety prompt",
		TargetField:   "system_prompt",
		ProposedValue: "You are a safe agent...",
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPolicyProposal_Validate_MissingID(t *testing.T) {
	p := state.PolicyProposal{
		Kind: state.ProposalKindPolicy, Status: state.ProposalStatusDraft,
		Title: "x", TargetField: "f", ProposedValue: "v",
	}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for missing proposal_id")
	}
}

func TestPolicyProposal_Validate_MissingTitle(t *testing.T) {
	p := state.PolicyProposal{
		ProposalID: "p1", Kind: state.ProposalKindPolicy, Status: state.ProposalStatusDraft,
		TargetField: "f", ProposedValue: "v",
	}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestPolicyProposal_Validate_MissingProposedValue(t *testing.T) {
	p := state.PolicyProposal{
		ProposalID: "p1", Kind: state.ProposalKindPolicy, Status: state.ProposalStatusDraft,
		Title: "x", TargetField: "f",
	}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for missing proposed_value")
	}
}

func TestPolicyProposal_Validate_MissingTargetField(t *testing.T) {
	p := state.PolicyProposal{
		ProposalID: "p1", Kind: state.ProposalKindPolicy, Status: state.ProposalStatusDraft,
		Title: "x", ProposedValue: "v",
	}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for missing target_field")
	}
}

func TestPolicyProposal_Normalize(t *testing.T) {
	p := state.PolicyProposal{
		ProposalID: "  p1  ",
		Title:      " title ",
		GoalID:     " g1 ",
		TaskID:     " t1 ",
		Kind:       "bogus",
		Status:     "bogus",
	}
	norm := p.Normalize()
	if norm.ProposalID != "p1" || norm.Title != "title" {
		t.Error("whitespace not trimmed")
	}
	if norm.GoalID != "g1" || norm.TaskID != "t1" {
		t.Error("linkage not trimmed")
	}
	if norm.Kind != "bogus" {
		t.Errorf("invalid kind should be preserved (caught by Validate), got: %q", norm.Kind)
	}
	if norm.Status != state.ProposalStatusDraft {
		t.Errorf("invalid status not defaulted: %q", norm.Status)
	}
	if norm.Version != 1 {
		t.Errorf("version = %d, want 1", norm.Version)
	}
}

func TestPolicyProposal_HasProvenance(t *testing.T) {
	if (state.PolicyProposal{}).HasProvenance() {
		t.Error("empty proposal should not have provenance")
	}
	if !(state.PolicyProposal{FeedbackIDs: []string{"fb-1"}}).HasProvenance() {
		t.Error("proposal with feedback should have provenance")
	}
	if !(state.PolicyProposal{EvidenceIDs: []string{"run-1"}}).HasProvenance() {
		t.Error("proposal with evidence should have provenance")
	}
	if !(state.PolicyProposal{Rationale: "reason"}).HasProvenance() {
		t.Error("proposal with rationale should have provenance")
	}
}

func TestValidProposalKind(t *testing.T) {
	if !state.ValidProposalKind(state.ProposalKindPrompt) || !state.ValidProposalKind(state.ProposalKindPolicy) {
		t.Error("valid kinds rejected")
	}
	if state.ValidProposalKind("bogus") {
		t.Error("bogus kind accepted")
	}
}

func TestValidProposalStatus(t *testing.T) {
	for _, s := range []state.ProposalStatus{
		state.ProposalStatusDraft, state.ProposalStatusPending,
		state.ProposalStatusApproved, state.ProposalStatusRejected,
		state.ProposalStatusApplied, state.ProposalStatusReverted,
		state.ProposalStatusSuperseded,
	} {
		if !state.ValidProposalStatus(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
	if state.ValidProposalStatus("bogus") {
		t.Error("bogus status accepted")
	}
}

func TestIsProposalTerminal(t *testing.T) {
	for _, s := range []state.ProposalStatus{
		state.ProposalStatusRejected, state.ProposalStatusReverted, state.ProposalStatusSuperseded,
	} {
		if !state.IsProposalTerminal(s) {
			t.Errorf("expected %q to be terminal", s)
		}
	}
	for _, s := range []state.ProposalStatus{
		state.ProposalStatusDraft, state.ProposalStatusPending,
		state.ProposalStatusApproved, state.ProposalStatusApplied,
	} {
		if state.IsProposalTerminal(s) {
			t.Errorf("expected %q to be non-terminal", s)
		}
	}
}

func TestPolicyProposal_JSON(t *testing.T) {
	p := state.PolicyProposal{
		Version: 1, ProposalID: "prop-99",
		Kind: state.ProposalKindPrompt, Status: state.ProposalStatusApproved,
		Title: "Better prompt", TargetField: "system_prompt",
		ProposedValue: "new prompt", FeedbackIDs: []string{"fb-1", "fb-2"},
		CreatedAt: 1000,
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded state.PolicyProposal
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ProposalID != "prop-99" || len(decoded.FeedbackIDs) != 2 {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
}

// ── Builder tests ──────────────────────────────────────────────────────────────

func TestBuilder_FromFeedback(t *testing.T) {
	b := NewProposalBuilder("test")
	feedback := []state.FeedbackRecord{
		{FeedbackID: "fb-1", GoalID: "g1", TaskID: "t1", RunID: "r1"},
		{FeedbackID: "fb-2", TaskID: "t1"},
	}
	p := b.FromFeedback(
		state.ProposalKindPrompt,
		"Improve safety",
		"system_prompt",
		"old prompt",
		"new prompt",
		"multiple safety concerns",
		feedback,
	)
	if p.ProposalID != "test-1" {
		t.Errorf("proposal_id = %q, want test-1", p.ProposalID)
	}
	if p.Kind != state.ProposalKindPrompt {
		t.Errorf("kind = %q", p.Kind)
	}
	if p.Status != state.ProposalStatusDraft {
		t.Errorf("status = %q, want draft", p.Status)
	}
	if len(p.FeedbackIDs) != 2 || p.FeedbackIDs[0] != "fb-1" {
		t.Errorf("feedback_ids = %v", p.FeedbackIDs)
	}
	// Should inherit linkage from first feedback.
	if p.GoalID != "g1" || p.TaskID != "t1" || p.RunID != "r1" {
		t.Errorf("linkage not inherited: goal=%q task=%q run=%q", p.GoalID, p.TaskID, p.RunID)
	}
	if p.CreatedAt == 0 {
		t.Error("created_at should be auto-set")
	}
}

func TestBuilder_FromEvidence(t *testing.T) {
	b := NewProposalBuilder("prop")
	p := b.FromEvidence(
		state.ProposalKindPolicy,
		"Reduce autonomy for risky tasks",
		"default_autonomy",
		"full",
		"plan_approval",
		"3 verification failures in last week",
		[]string{"run-1", "run-2", "run-3"},
	)
	if p.Kind != state.ProposalKindPolicy {
		t.Errorf("kind = %q", p.Kind)
	}
	if len(p.EvidenceIDs) != 3 {
		t.Errorf("evidence_ids = %v", p.EvidenceIDs)
	}
	if !p.HasProvenance() {
		t.Error("should have provenance")
	}
}

func TestBuilder_IDsAreSequential(t *testing.T) {
	b := NewProposalBuilder("seq")
	p1 := b.FromEvidence(state.ProposalKindPolicy, "A", "f", "", "v", "", nil)
	p2 := b.FromEvidence(state.ProposalKindPolicy, "B", "f", "", "v", "", nil)
	if p1.ProposalID != "seq-1" || p2.ProposalID != "seq-2" {
		t.Errorf("ids: %q, %q", p1.ProposalID, p2.ProposalID)
	}
}

// ── Lifecycle tests ────────────────────────────────────────────────────────────

func newDraftProposal() state.PolicyProposal {
	return state.PolicyProposal{
		Version: 1, ProposalID: "prop-1",
		Kind: state.ProposalKindPolicy, Status: state.ProposalStatusDraft,
		Title: "Test", TargetField: "f", ProposedValue: "v", CreatedAt: 100,
	}
}

func TestSubmitForReview(t *testing.T) {
	p, err := SubmitForReview(newDraftProposal(), 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status != state.ProposalStatusPending {
		t.Errorf("status = %q, want pending", p.Status)
	}
	if p.UpdatedAt != 200 {
		t.Errorf("updated_at = %d, want 200", p.UpdatedAt)
	}
}

func TestSubmitForReview_NotDraft(t *testing.T) {
	p := newDraftProposal()
	p.Status = state.ProposalStatusApproved
	_, err := SubmitForReview(p, 200)
	if err == nil {
		t.Fatal("expected error for non-draft")
	}
}

func TestApproveProposal(t *testing.T) {
	p := newDraftProposal()
	p.Status = state.ProposalStatusPending
	approved, err := ApproveProposal(p, "alice", "looks good", 300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved.Status != state.ProposalStatusApproved {
		t.Errorf("status = %q", approved.Status)
	}
	if approved.ReviewedBy != "alice" || approved.ReviewNote != "looks good" {
		t.Error("reviewer info not set")
	}
}

func TestApproveProposal_NotPending(t *testing.T) {
	_, err := ApproveProposal(newDraftProposal(), "alice", "", 300)
	if err == nil {
		t.Fatal("expected error for non-pending")
	}
}

func TestApproveProposal_MissingReviewer(t *testing.T) {
	p := newDraftProposal()
	p.Status = state.ProposalStatusPending
	_, err := ApproveProposal(p, "", "note", 300)
	if err == nil {
		t.Fatal("expected error for missing reviewer")
	}
}

func TestRejectProposal(t *testing.T) {
	p := newDraftProposal()
	p.Status = state.ProposalStatusPending
	rejected, err := RejectProposal(p, "bob", "too risky", 300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rejected.Status != state.ProposalStatusRejected {
		t.Errorf("status = %q", rejected.Status)
	}
	if rejected.ReviewNote != "too risky" {
		t.Errorf("review_note = %q", rejected.ReviewNote)
	}
}

func TestRejectProposal_MissingReason(t *testing.T) {
	p := newDraftProposal()
	p.Status = state.ProposalStatusPending
	_, err := RejectProposal(p, "bob", "", 300)
	if err == nil {
		t.Fatal("expected error for missing reason")
	}
}

func TestMarkApplied(t *testing.T) {
	p := newDraftProposal()
	p.Status = state.ProposalStatusApproved
	applied, err := MarkApplied(p, "deployer", 400)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied.Status != state.ProposalStatusApplied {
		t.Errorf("status = %q", applied.Status)
	}
	if applied.AppliedAt != 400 || applied.AppliedBy != "deployer" {
		t.Error("applied info not set")
	}
}

func TestMarkApplied_NotApproved(t *testing.T) {
	_, err := MarkApplied(newDraftProposal(), "deployer", 400)
	if err == nil {
		t.Fatal("expected error for non-approved")
	}
}

func TestMarkReverted(t *testing.T) {
	p := newDraftProposal()
	p.Status = state.ProposalStatusApplied
	reverted, err := MarkReverted(p, "ops", 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reverted.Status != state.ProposalStatusReverted {
		t.Errorf("status = %q", reverted.Status)
	}
	if reverted.RevertedAt != 500 || reverted.RevertedBy != "ops" {
		t.Error("reverted info not set")
	}
}

func TestMarkReverted_NotApplied(t *testing.T) {
	_, err := MarkReverted(newDraftProposal(), "ops", 500)
	if err == nil {
		t.Fatal("expected error for non-applied")
	}
}

func TestSupersedeProposal(t *testing.T) {
	p := newDraftProposal()
	p.Status = state.ProposalStatusPending
	sup, err := SupersedeProposal(p, "prop-2", 600)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sup.Status != state.ProposalStatusSuperseded {
		t.Errorf("status = %q", sup.Status)
	}
	if sup.Meta["superseded_by"] != "prop-2" {
		t.Errorf("meta = %v", sup.Meta)
	}
}

func TestSupersedeProposal_TerminalFails(t *testing.T) {
	p := newDraftProposal()
	p.Status = state.ProposalStatusRejected
	_, err := SupersedeProposal(p, "prop-2", 600)
	if err == nil {
		t.Fatal("expected error for terminal status")
	}
}

// ── Full lifecycle ─────────────────────────────────────────────────────────────

func TestFullLifecycle_DraftToAppliedToReverted(t *testing.T) {
	p := newDraftProposal()

	p, err := SubmitForReview(p, 200)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	p, err = ApproveProposal(p, "reviewer", "approved", 300)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	p, err = MarkApplied(p, "deployer", 400)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	p, err = MarkReverted(p, "ops", 500)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if p.Status != state.ProposalStatusReverted {
		t.Errorf("final status = %q, want reverted", p.Status)
	}
}

func TestFullLifecycle_DraftToRejected(t *testing.T) {
	p := newDraftProposal()
	p, _ = SubmitForReview(p, 200)
	p, err := RejectProposal(p, "reviewer", "not needed", 300)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if p.Status != state.ProposalStatusRejected {
		t.Errorf("final status = %q, want rejected", p.Status)
	}
}

// ── Formatting ─────────────────────────────────────────────────────────────────

func TestFormatProposal(t *testing.T) {
	p := state.PolicyProposal{
		ProposalID:    "prop-1",
		Kind:          state.ProposalKindPrompt,
		Status:        state.ProposalStatusPending,
		Title:         "Improve safety",
		TargetField:   "system_prompt",
		TargetAgent:   "agent-1",
		FeedbackIDs:   []string{"fb-1", "fb-2"},
		Rationale:     "safety concerns",
		ReviewedBy:    "",
		ProposedValue: "new prompt",
	}
	out := FormatProposal(p)
	for _, want := range []string{"⏳", "prompt", "Improve safety", "system_prompt", "agent-1", "fb-1", "safety concerns"} {
		if !strings.Contains(out, want) {
			t.Errorf("format missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatProposal_WithReviewer(t *testing.T) {
	p := state.PolicyProposal{
		ProposalID: "prop-1", Kind: state.ProposalKindPolicy,
		Status: state.ProposalStatusApproved, Title: "Reduce autonomy",
		TargetField: "default_autonomy", ProposedValue: "plan_approval",
		ReviewedBy: "alice", ReviewNote: "agreed",
	}
	out := FormatProposal(p)
	if !strings.Contains(out, "alice") || !strings.Contains(out, "agreed") {
		t.Errorf("reviewer info missing in:\n%s", out)
	}
}

func TestFormatProposalSummary_Empty(t *testing.T) {
	if got := FormatProposalSummary(nil); got != "No proposals." {
		t.Errorf("got %q", got)
	}
}

func TestFormatProposalSummary_WithProposals(t *testing.T) {
	proposals := []state.PolicyProposal{
		{Status: state.ProposalStatusDraft},
		{Status: state.ProposalStatusPending},
		{Status: state.ProposalStatusPending},
		{Status: state.ProposalStatusApplied},
	}
	out := FormatProposalSummary(proposals)
	if !strings.Contains(out, "4 total") {
		t.Errorf("missing count in %q", out)
	}
	if !strings.Contains(out, "pending=2") {
		t.Errorf("missing pending count in %q", out)
	}
}

// ── Concurrency ────────────────────────────────────────────────────────────────

func TestBuilder_ConcurrentAccess(t *testing.T) {
	b := NewProposalBuilder("conc")
	var wg sync.WaitGroup
	proposals := make([]state.PolicyProposal, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			proposals[idx] = b.FromEvidence(
				state.ProposalKindPolicy, "test", "f", "", "v", "", nil,
			)
		}(i)
	}
	wg.Wait()

	// All IDs should be unique.
	seen := make(map[string]bool)
	for _, p := range proposals {
		if seen[p.ProposalID] {
			t.Fatalf("duplicate proposal ID: %s", p.ProposalID)
		}
		seen[p.ProposalID] = true
	}
}
