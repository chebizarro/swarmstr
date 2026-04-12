// proposal.go manages the creation and lifecycle of policy/prompt proposals
// derived from feedback, verification failures, and retrospective analysis.
//
// Proposals are distinct from feedback: feedback records observations,
// proposals record intended changes. A proposal carries provenance links
// back to the feedback or evidence that motivated it.
package planner

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// ── Proposal builder ───────────────────────────────────────────────────────────

// ProposalBuilder constructs proposals with required provenance.
type ProposalBuilder struct {
	mu     sync.Mutex
	nextID int
	prefix string
}

// NewProposalBuilder creates a builder with the given ID prefix.
func NewProposalBuilder(prefix string) *ProposalBuilder {
	if prefix == "" {
		prefix = "prop"
	}
	return &ProposalBuilder{prefix: prefix}
}

// generateIDLocked returns a new unique proposal ID.
// REQUIRES: b.mu must be held by the caller.
func (b *ProposalBuilder) generateIDLocked() string {
	b.nextID++
	return fmt.Sprintf("%s-%d", b.prefix, b.nextID)
}

// FromFeedback creates a proposal derived from one or more feedback records.
// The feedback IDs are recorded as provenance, and the rationale summarises
// why the change is warranted.
func (b *ProposalBuilder) FromFeedback(
	kind state.ProposalKind,
	title string,
	targetField string,
	currentValue string,
	proposedValue string,
	rationale string,
	feedback []state.FeedbackRecord,
) state.PolicyProposal {
	b.mu.Lock()
	id := b.generateIDLocked()
	b.mu.Unlock()

	feedbackIDs := make([]string, 0, len(feedback))
	for _, fb := range feedback {
		feedbackIDs = append(feedbackIDs, fb.FeedbackID)
	}

	// Inherit linkage from the first feedback record with linkage.
	var goalID, taskID, runID string
	for _, fb := range feedback {
		if goalID == "" {
			goalID = fb.GoalID
		}
		if taskID == "" {
			taskID = fb.TaskID
		}
		if runID == "" {
			runID = fb.RunID
		}
	}

	return state.PolicyProposal{
		Version:       1,
		ProposalID:    id,
		Kind:          kind,
		Status:        state.ProposalStatusDraft,
		Title:         title,
		TargetField:   targetField,
		CurrentValue:  currentValue,
		ProposedValue: proposedValue,
		Rationale:     rationale,
		FeedbackIDs:   feedbackIDs,
		GoalID:        goalID,
		TaskID:        taskID,
		RunID:         runID,
		CreatedAt:     time.Now().Unix(),
	}
}

// FromEvidence creates a proposal derived from verification or run evidence
// (not direct feedback records). EvidenceIDs are typically run IDs,
// check IDs, or audit bundle references.
func (b *ProposalBuilder) FromEvidence(
	kind state.ProposalKind,
	title string,
	targetField string,
	currentValue string,
	proposedValue string,
	rationale string,
	evidenceIDs []string,
) state.PolicyProposal {
	b.mu.Lock()
	id := b.generateIDLocked()
	b.mu.Unlock()

	return state.PolicyProposal{
		Version:       1,
		ProposalID:    id,
		Kind:          kind,
		Status:        state.ProposalStatusDraft,
		Title:         title,
		TargetField:   targetField,
		CurrentValue:  currentValue,
		ProposedValue: proposedValue,
		Rationale:     rationale,
		EvidenceIDs:   evidenceIDs,
		CreatedAt:     time.Now().Unix(),
	}
}

// ── Proposal lifecycle ─────────────────────────────────────────────────────────

// SubmitForReview transitions a draft proposal to pending.
func SubmitForReview(p state.PolicyProposal, now int64) (state.PolicyProposal, error) {
	if p.Status != state.ProposalStatusDraft {
		return p, fmt.Errorf("can only submit draft proposals, got %q", p.Status)
	}
	p.Status = state.ProposalStatusPending
	p.UpdatedAt = now
	return p, nil
}

// ApproveProposal marks a proposal as approved by a reviewer.
func ApproveProposal(p state.PolicyProposal, reviewer, note string, now int64) (state.PolicyProposal, error) {
	if p.Status != state.ProposalStatusPending {
		return p, fmt.Errorf("can only approve pending proposals, got %q", p.Status)
	}
	if strings.TrimSpace(reviewer) == "" {
		return p, fmt.Errorf("reviewer is required")
	}
	p.Status = state.ProposalStatusApproved
	p.ReviewedBy = reviewer
	p.ReviewedAt = now
	p.ReviewNote = note
	p.UpdatedAt = now
	return p, nil
}

// RejectProposal marks a proposal as rejected by a reviewer.
func RejectProposal(p state.PolicyProposal, reviewer, reason string, now int64) (state.PolicyProposal, error) {
	if p.Status != state.ProposalStatusPending {
		return p, fmt.Errorf("can only reject pending proposals, got %q", p.Status)
	}
	if strings.TrimSpace(reviewer) == "" {
		return p, fmt.Errorf("reviewer is required")
	}
	if strings.TrimSpace(reason) == "" {
		return p, fmt.Errorf("rejection reason is required")
	}
	p.Status = state.ProposalStatusRejected
	p.ReviewedBy = reviewer
	p.ReviewedAt = now
	p.ReviewNote = reason
	p.UpdatedAt = now
	return p, nil
}

// MarkApplied records that an approved proposal has been applied to live config.
func MarkApplied(p state.PolicyProposal, appliedBy string, now int64) (state.PolicyProposal, error) {
	if p.Status != state.ProposalStatusApproved {
		return p, fmt.Errorf("can only apply approved proposals, got %q", p.Status)
	}
	p.Status = state.ProposalStatusApplied
	p.AppliedAt = now
	p.AppliedBy = appliedBy
	p.UpdatedAt = now
	return p, nil
}

// MarkReverted records that an applied proposal has been rolled back.
func MarkReverted(p state.PolicyProposal, revertedBy string, now int64) (state.PolicyProposal, error) {
	if p.Status != state.ProposalStatusApplied {
		return p, fmt.Errorf("can only revert applied proposals, got %q", p.Status)
	}
	p.Status = state.ProposalStatusReverted
	p.RevertedAt = now
	p.RevertedBy = revertedBy
	p.UpdatedAt = now
	return p, nil
}

// SupersedeProposal marks a proposal as superseded by another.
func SupersedeProposal(p state.PolicyProposal, supersededBy string, now int64) (state.PolicyProposal, error) {
	if state.IsProposalTerminal(p.Status) {
		return p, fmt.Errorf("cannot supersede terminal proposal (status=%q)", p.Status)
	}
	p.Status = state.ProposalStatusSuperseded
	if p.Meta == nil {
		p.Meta = map[string]any{}
	}
	p.Meta["superseded_by"] = supersededBy
	p.UpdatedAt = now
	return p, nil
}

// ── Formatting ─────────────────────────────────────────────────────────────────

// FormatProposal returns a human-readable summary of a proposal.
func FormatProposal(p state.PolicyProposal) string {
	var b strings.Builder
	icon := proposalStatusIcon(p.Status)
	fmt.Fprintf(&b, "%s [%s] %s\n", icon, p.Kind, p.Title)
	if p.Summary != "" {
		fmt.Fprintf(&b, "  %s\n", p.Summary)
	}
	fmt.Fprintf(&b, "  Target: %s", p.TargetField)
	if p.TargetAgent != "" {
		fmt.Fprintf(&b, " (agent=%s)", p.TargetAgent)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "  Status: %s\n", p.Status)

	if len(p.FeedbackIDs) > 0 {
		fmt.Fprintf(&b, "  Feedback: %s\n", strings.Join(p.FeedbackIDs, ", "))
	}
	if len(p.EvidenceIDs) > 0 {
		fmt.Fprintf(&b, "  Evidence: %s\n", strings.Join(p.EvidenceIDs, ", "))
	}
	if p.Rationale != "" {
		fmt.Fprintf(&b, "  Rationale: %s\n", p.Rationale)
	}
	if p.ReviewedBy != "" {
		fmt.Fprintf(&b, "  Reviewed by %s: %s\n", p.ReviewedBy, p.ReviewNote)
	}
	return b.String()
}

func proposalStatusIcon(s state.ProposalStatus) string {
	switch s {
	case state.ProposalStatusDraft:
		return "📝"
	case state.ProposalStatusPending:
		return "⏳"
	case state.ProposalStatusApproved:
		return "✅"
	case state.ProposalStatusRejected:
		return "❌"
	case state.ProposalStatusApplied:
		return "🚀"
	case state.ProposalStatusReverted:
		return "↩️"
	case state.ProposalStatusSuperseded:
		return "🔄"
	default:
		return "•"
	}
}

// FormatProposalSummary returns a brief summary of a list of proposals.
func FormatProposalSummary(proposals []state.PolicyProposal) string {
	if len(proposals) == 0 {
		return "No proposals."
	}
	counts := make(map[state.ProposalStatus]int)
	for _, p := range proposals {
		counts[p.Status]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Proposals: %d total", len(proposals))
	for _, s := range []state.ProposalStatus{
		state.ProposalStatusDraft,
		state.ProposalStatusPending,
		state.ProposalStatusApproved,
		state.ProposalStatusApplied,
		state.ProposalStatusRejected,
		state.ProposalStatusReverted,
		state.ProposalStatusSuperseded,
	} {
		if c := counts[s]; c > 0 {
			fmt.Fprintf(&b, " %s=%d", s, c)
		}
	}
	return b.String()
}
