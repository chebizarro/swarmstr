package state

import (
	"fmt"
	"strings"
)

type FeedbackSource string

const (
	FeedbackSourceOperator     FeedbackSource = "operator"
	FeedbackSourceVerification FeedbackSource = "verification"
	FeedbackSourceReview       FeedbackSource = "review"
	FeedbackSourceAgent        FeedbackSource = "agent"
	FeedbackSourceSystem       FeedbackSource = "system"
)

// ValidFeedbackSource reports whether s is a recognized feedback source.
func ValidFeedbackSource(s FeedbackSource) bool {
	switch s {
	case FeedbackSourceOperator, FeedbackSourceVerification,
		FeedbackSourceReview, FeedbackSourceAgent, FeedbackSourceSystem:
		return true
	}
	return false
}

// FeedbackSeverity indicates the urgency or importance of the feedback.
type FeedbackSeverity string

const (
	FeedbackSeverityInfo     FeedbackSeverity = "info"
	FeedbackSeverityWarning  FeedbackSeverity = "warning"
	FeedbackSeverityError    FeedbackSeverity = "error"
	FeedbackSeverityCritical FeedbackSeverity = "critical"
)

// ValidFeedbackSeverity reports whether s is a recognized feedback severity.
func ValidFeedbackSeverity(s FeedbackSeverity) bool {
	switch s {
	case FeedbackSeverityInfo, FeedbackSeverityWarning,
		FeedbackSeverityError, FeedbackSeverityCritical:
		return true
	}
	return false
}

// FeedbackCategory classifies what kind of feedback this is.
type FeedbackCategory string

const (
	FeedbackCategoryCorrectness FeedbackCategory = "correctness"
	FeedbackCategoryPerformance FeedbackCategory = "performance"
	FeedbackCategoryStyle       FeedbackCategory = "style"
	FeedbackCategoryPolicy      FeedbackCategory = "policy"
	FeedbackCategorySafety      FeedbackCategory = "safety"
	FeedbackCategoryGeneral     FeedbackCategory = "general"
)

// ValidFeedbackCategory reports whether c is a recognized feedback category.
func ValidFeedbackCategory(c FeedbackCategory) bool {
	switch c {
	case FeedbackCategoryCorrectness, FeedbackCategoryPerformance,
		FeedbackCategoryStyle, FeedbackCategoryPolicy,
		FeedbackCategorySafety, FeedbackCategoryGeneral:
		return true
	}
	return false
}

// FeedbackRecord is the durable structured feedback object.
// It is distinct from generic memory notes and links to the goal/task/run
// that triggered it.
type FeedbackRecord struct {
	Version    int    `json:"version"`
	FeedbackID string `json:"feedback_id"`

	// Linkage — at least one should be set for traceability.
	GoalID string `json:"goal_id,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`
	StepID string `json:"step_id,omitempty"`

	// Content.
	Source   FeedbackSource   `json:"source"`
	Severity FeedbackSeverity `json:"severity"`
	Category FeedbackCategory `json:"category"`
	Summary  string           `json:"summary"`
	Detail   string           `json:"detail,omitempty"`

	// Provenance.
	Author    string `json:"author,omitempty"` // pubkey or agent ID
	SessionID string `json:"session_id,omitempty"`

	// Timestamps.
	CreatedAt int64 `json:"created_at"`

	// Optional structured metadata.
	Meta map[string]any `json:"meta,omitempty"`
}

// Normalize fills in defaults and trims whitespace.
func (f FeedbackRecord) Normalize() FeedbackRecord {
	if f.Version == 0 {
		f.Version = 1
	}
	f.FeedbackID = strings.TrimSpace(f.FeedbackID)
	f.Summary = strings.TrimSpace(f.Summary)
	f.Detail = strings.TrimSpace(f.Detail)
	// Trim linkage and provenance fields to avoid silent tag mismatches.
	f.GoalID = strings.TrimSpace(f.GoalID)
	f.TaskID = strings.TrimSpace(f.TaskID)
	f.RunID = strings.TrimSpace(f.RunID)
	f.StepID = strings.TrimSpace(f.StepID)
	f.Author = strings.TrimSpace(f.Author)
	f.SessionID = strings.TrimSpace(f.SessionID)
	if !ValidFeedbackSource(f.Source) {
		f.Source = FeedbackSourceSystem
	}
	if !ValidFeedbackSeverity(f.Severity) {
		f.Severity = FeedbackSeverityInfo
	}
	if !ValidFeedbackCategory(f.Category) {
		f.Category = FeedbackCategoryGeneral
	}
	return f
}

// Validate checks required fields.
func (f FeedbackRecord) Validate() error {
	if strings.TrimSpace(f.FeedbackID) == "" {
		return fmt.Errorf("feedback_id is required")
	}
	if strings.TrimSpace(f.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	if !ValidFeedbackSource(f.Source) {
		return fmt.Errorf("invalid source %q", f.Source)
	}
	if !ValidFeedbackSeverity(f.Severity) {
		return fmt.Errorf("invalid severity %q", f.Severity)
	}
	if !ValidFeedbackCategory(f.Category) {
		return fmt.Errorf("invalid category %q", f.Category)
	}
	// At least one linkage field should be set.
	if f.GoalID == "" && f.TaskID == "" && f.RunID == "" {
		return fmt.Errorf("at least one of goal_id, task_id, or run_id is required")
	}
	return nil
}

// HasLinkage reports whether the feedback is linked to a specific context.
func (f FeedbackRecord) HasLinkage() bool {
	return f.GoalID != "" || f.TaskID != "" || f.RunID != ""
}

// ── Policy / prompt proposals ──────────────────────────────────────────────────

// ProposalKind distinguishes what kind of change is being proposed.
type ProposalKind string

const (
	ProposalKindPrompt ProposalKind = "prompt"
	ProposalKindPolicy ProposalKind = "policy"
)

// ValidProposalKind reports whether k is a recognized proposal kind.
func ValidProposalKind(k ProposalKind) bool {
	return k == ProposalKindPrompt || k == ProposalKindPolicy
}

// ProposalStatus tracks the lifecycle of a proposal.
type ProposalStatus string

const (
	ProposalStatusDraft      ProposalStatus = "draft"
	ProposalStatusPending    ProposalStatus = "pending"
	ProposalStatusApproved   ProposalStatus = "approved"
	ProposalStatusRejected   ProposalStatus = "rejected"
	ProposalStatusApplied    ProposalStatus = "applied"
	ProposalStatusReverted   ProposalStatus = "reverted"
	ProposalStatusSuperseded ProposalStatus = "superseded"
)

// ValidProposalStatus reports whether s is a recognized proposal status.
func ValidProposalStatus(s ProposalStatus) bool {
	switch s {
	case ProposalStatusDraft, ProposalStatusPending, ProposalStatusApproved,
		ProposalStatusRejected, ProposalStatusApplied, ProposalStatusReverted,
		ProposalStatusSuperseded:
		return true
	}
	return false
}

// IsProposalTerminal reports whether the status is a terminal state.
func IsProposalTerminal(s ProposalStatus) bool {
	return s == ProposalStatusRejected || s == ProposalStatusReverted || s == ProposalStatusSuperseded
}

// PolicyProposal is a candidate change to a prompt or policy, derived from
// feedback, verification failures, or retrospective analysis. It is distinct
// from FeedbackRecord — feedback captures observations, proposals capture
// intended changes.
type PolicyProposal struct {
	Version    int    `json:"version"`
	ProposalID string `json:"proposal_id"`

	// What is being proposed.
	Kind    ProposalKind   `json:"kind"`
	Status  ProposalStatus `json:"status"`
	Title   string         `json:"title"`
	Summary string         `json:"summary"`

	// The proposed change.
	// For prompt proposals: CurrentValue is the existing prompt text,
	// ProposedValue is the replacement.
	// For policy proposals: CurrentValue/ProposedValue are JSON-encoded
	// policy fragments.
	TargetField   string `json:"target_field"`           // e.g. "system_prompt", "default_autonomy"
	TargetAgent   string `json:"target_agent,omitempty"` // agent ID (empty = global)
	CurrentValue  string `json:"current_value,omitempty"`
	ProposedValue string `json:"proposed_value"`

	// Provenance — why this proposal was created.
	FeedbackIDs []string `json:"feedback_ids,omitempty"` // linked feedback records
	EvidenceIDs []string `json:"evidence_ids,omitempty"` // verification results, run IDs, etc.
	Rationale   string   `json:"rationale,omitempty"`    // human or agent explanation

	// Linkage to workflow context.
	GoalID string `json:"goal_id,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`

	// Review trail.
	CreatedBy  string `json:"created_by,omitempty"`  // agent or operator who proposed
	ReviewedBy string `json:"reviewed_by,omitempty"` // who approved/rejected
	ReviewedAt int64  `json:"reviewed_at,omitempty"`
	ReviewNote string `json:"review_note,omitempty"`

	// Applied/reverted tracking.
	AppliedAt  int64  `json:"applied_at,omitempty"`
	AppliedBy  string `json:"applied_by,omitempty"`
	RevertedAt int64  `json:"reverted_at,omitempty"`
	RevertedBy string `json:"reverted_by,omitempty"`

	// Timestamps.
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at,omitempty"`

	// Optional structured metadata.
	Meta map[string]any `json:"meta,omitempty"`
}

// Normalize fills in defaults and trims whitespace.
func (p PolicyProposal) Normalize() PolicyProposal {
	if p.Version == 0 {
		p.Version = 1
	}
	p.ProposalID = strings.TrimSpace(p.ProposalID)
	p.Title = strings.TrimSpace(p.Title)
	p.Summary = strings.TrimSpace(p.Summary)
	p.TargetField = strings.TrimSpace(p.TargetField)
	p.TargetAgent = strings.TrimSpace(p.TargetAgent)
	p.Rationale = strings.TrimSpace(p.Rationale)
	p.GoalID = strings.TrimSpace(p.GoalID)
	p.TaskID = strings.TrimSpace(p.TaskID)
	p.RunID = strings.TrimSpace(p.RunID)
	p.CreatedBy = strings.TrimSpace(p.CreatedBy)
	p.ReviewedBy = strings.TrimSpace(p.ReviewedBy)
	p.ReviewNote = strings.TrimSpace(p.ReviewNote)
	// Don't default invalid Kind — let Validate() catch it to avoid
	// silently misclassifying prompt proposals as policy proposals.
	if !ValidProposalStatus(p.Status) {
		p.Status = ProposalStatusDraft
	}
	return p
}

// Validate checks required fields.
func (p PolicyProposal) Validate() error {
	if strings.TrimSpace(p.ProposalID) == "" {
		return fmt.Errorf("proposal_id is required")
	}
	if strings.TrimSpace(p.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if strings.TrimSpace(p.ProposedValue) == "" {
		return fmt.Errorf("proposed_value is required")
	}
	if !ValidProposalKind(p.Kind) {
		return fmt.Errorf("invalid kind %q", p.Kind)
	}
	if !ValidProposalStatus(p.Status) {
		return fmt.Errorf("invalid status %q", p.Status)
	}
	if strings.TrimSpace(p.TargetField) == "" {
		return fmt.Errorf("target_field is required")
	}
	return nil
}

// HasProvenance reports whether the proposal has linked evidence or feedback.
func (p PolicyProposal) HasProvenance() bool {
	return len(p.FeedbackIDs) > 0 || len(p.EvidenceIDs) > 0 || p.Rationale != ""
}

// ── Retrospective ────────────────────────────────────────────────────────────

// RetroTrigger describes what prompted a retrospective.
type RetroTrigger string

const (
	RetroTriggerRunCompleted    RetroTrigger = "run_completed"
	RetroTriggerRunFailed       RetroTrigger = "run_failed"
	RetroTriggerBudgetExhausted RetroTrigger = "budget_exhausted"
	RetroTriggerVerifyFailed    RetroTrigger = "verification_failed"
	RetroTriggerOperator        RetroTrigger = "operator_requested"
)

// ValidRetroTrigger reports whether t is a recognised trigger value.
func ValidRetroTrigger(t RetroTrigger) bool {
	switch t {
	case RetroTriggerRunCompleted, RetroTriggerRunFailed,
		RetroTriggerBudgetExhausted, RetroTriggerVerifyFailed,
		RetroTriggerOperator:
		return true
	}
	return false
}

// RetroOutcome classifies the overall result of the retrospective's subject run.
type RetroOutcome string

const (
	RetroOutcomeSuccess RetroOutcome = "success"
	RetroOutcomePartial RetroOutcome = "partial"
	RetroOutcomeFailure RetroOutcome = "failure"
)

// ValidRetroOutcome reports whether o is a recognised outcome value.
func ValidRetroOutcome(o RetroOutcome) bool {
	switch o {
	case RetroOutcomeSuccess, RetroOutcomePartial, RetroOutcomeFailure:
		return true
	}
	return false
}

// Retrospective is a structured post-run analysis record that captures
// what worked, what failed, and proposed improvements. It links back to
// feedback records and policy proposals without mutating live policy.
type Retrospective struct {
	Version      int            `json:"version"`
	RetroID      string         `json:"retro_id"`
	GoalID       string         `json:"goal_id,omitempty"`
	TaskID       string         `json:"task_id,omitempty"`
	RunID        string         `json:"run_id,omitempty"`
	AgentID      string         `json:"agent_id,omitempty"`
	Trigger      RetroTrigger   `json:"trigger"`
	Outcome      RetroOutcome   `json:"outcome"`
	Summary      string         `json:"summary"`
	WhatWorked   []string       `json:"what_worked,omitempty"`
	WhatFailed   []string       `json:"what_failed,omitempty"`
	Improvements []string       `json:"improvements,omitempty"`
	FeedbackIDs  []string       `json:"feedback_ids,omitempty"`
	ProposalIDs  []string       `json:"proposal_ids,omitempty"`
	Usage        TaskUsage      `json:"usage,omitempty"`
	DurationMS   int64          `json:"duration_ms,omitempty"`
	CreatedAt    int64          `json:"created_at"`
	CreatedBy    string         `json:"created_by,omitempty"`
	Meta         map[string]any `json:"meta,omitempty"`
}

// Normalize applies defaults and trims whitespace on key fields.
func (r Retrospective) Normalize() Retrospective {
	if r.Version == 0 {
		r.Version = 1
	}
	r.RetroID = strings.TrimSpace(r.RetroID)
	r.GoalID = strings.TrimSpace(r.GoalID)
	r.TaskID = strings.TrimSpace(r.TaskID)
	r.RunID = strings.TrimSpace(r.RunID)
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.Summary = strings.TrimSpace(r.Summary)
	r.CreatedBy = strings.TrimSpace(r.CreatedBy)
	return r
}

// Validate checks that required fields are present and valid.
func (r Retrospective) Validate() error {
	if r.RetroID == "" {
		return fmt.Errorf("retro_id is required")
	}
	if r.Summary == "" {
		return fmt.Errorf("summary is required")
	}
	if !ValidRetroTrigger(r.Trigger) {
		return fmt.Errorf("invalid trigger %q", r.Trigger)
	}
	if !ValidRetroOutcome(r.Outcome) {
		return fmt.Errorf("invalid outcome %q", r.Outcome)
	}
	if r.CreatedAt <= 0 {
		return fmt.Errorf("created_at must be positive")
	}
	return nil
}

// HasLinkage reports whether the retrospective is linked to any run context.
func (r Retrospective) HasLinkage() bool {
	return r.GoalID != "" || r.TaskID != "" || r.RunID != ""
}

// HasProposals reports whether the retrospective generated any policy proposals.
func (r Retrospective) HasProposals() bool {
	return len(r.ProposalIDs) > 0
}

// HasFeedback reports whether the retrospective references any feedback records.
func (r Retrospective) HasFeedback() bool {
	return len(r.FeedbackIDs) > 0
}
