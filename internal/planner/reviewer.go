// reviewer.go implements the reviewer-worker pattern for second-pass
// critique and signoff. A reviewer evaluates a worker's output and produces
// a durable decision (approve/reject/revise) linked to the task/run.
package planner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// ── Reviewer verdict ────────────────────────────────────────────────────────

// ReviewVerdict is the reviewer's decision.
type ReviewVerdict string

const (
	ReviewApproved ReviewVerdict = "approved"
	ReviewRejected ReviewVerdict = "rejected"
	ReviewRevise   ReviewVerdict = "revise" // approved with required changes
)

var validReviewVerdicts = map[ReviewVerdict]bool{
	ReviewApproved: true,
	ReviewRejected: true,
	ReviewRevise:   true,
}

// Valid reports whether v is a recognised verdict.
func (v ReviewVerdict) Valid() bool { return validReviewVerdicts[v] }

// IsApproval reports whether the verdict allows completion.
func (v ReviewVerdict) IsApproval() bool {
	return v == ReviewApproved
}

// ── Review request ──────────────────────────────────────────────────────────

// ReviewRequest describes what the reviewer should evaluate.
type ReviewRequest struct {
	// TaskID is the task being reviewed.
	TaskID string `json:"task_id"`
	// RunID is the run being reviewed.
	RunID string `json:"run_id"`
	// ReviewerID is the assigned reviewer (agent ID or pubkey).
	ReviewerID string `json:"reviewer_id"`
	// WorkerID is the agent that produced the output.
	WorkerID string `json:"worker_id"`
	// Outputs are the task outputs to review.
	Outputs TaskOutputs `json:"outputs"`
	// AcceptanceCriteria are the criteria the reviewer should evaluate against.
	AcceptanceCriteria []state.TaskAcceptanceCriterion `json:"acceptance_criteria,omitempty"`
	// VerificationSpec is the task's verification contract.
	VerificationSpec state.VerificationSpec `json:"verification_spec,omitempty"`
	// Instructions are additional reviewer-specific instructions.
	Instructions string `json:"instructions,omitempty"`
	// Constraints limit what the reviewer may do.
	Constraints ReviewConstraints `json:"constraints,omitempty"`
	// RequestedAt is when the review was requested.
	RequestedAt int64 `json:"requested_at"`
}

// ReviewConstraints limit reviewer scope.
type ReviewConstraints struct {
	// MaxDuration is how long the reviewer has to respond.
	MaxDuration time.Duration `json:"max_duration,omitempty"`
	// RequireEvidence forces the reviewer to cite specific evidence.
	RequireEvidence bool `json:"require_evidence,omitempty"`
	// RequireConfidence forces a confidence score.
	RequireConfidence bool `json:"require_confidence,omitempty"`
}

// ── Review result ───────────────────────────────────────────────────────────

// ReviewResult is the reviewer's durable decision.
type ReviewResult struct {
	// TaskID links the review to its task.
	TaskID string `json:"task_id"`
	// RunID links the review to its run.
	RunID string `json:"run_id"`
	// ReviewerID is who performed the review.
	ReviewerID string `json:"reviewer_id"`
	// Verdict is the reviewer's decision.
	Verdict ReviewVerdict `json:"verdict"`
	// Comments is the reviewer's written feedback.
	Comments string `json:"comments,omitempty"`
	// Confidence is the reviewer's confidence in the verdict (0.0-1.0).
	Confidence float64 `json:"confidence,omitempty"`
	// Evidence cites specific outputs/artifacts supporting the verdict.
	Evidence []ReviewEvidence `json:"evidence,omitempty"`
	// CriteriaResults maps each acceptance criterion to pass/fail.
	CriteriaResults []CriterionResult `json:"criteria_results,omitempty"`
	// ReviewedAt is when the review was completed.
	ReviewedAt int64 `json:"reviewed_at"`
	// Duration is how long the review took.
	Duration time.Duration `json:"duration,omitempty"`
	// Meta holds additional reviewer metadata.
	Meta map[string]any `json:"meta,omitempty"`
}

// ReviewEvidence is a specific citation in the review.
type ReviewEvidence struct {
	Ref     string `json:"ref"`               // artifact name, output section, etc.
	Excerpt string `json:"excerpt,omitempty"`  // relevant snippet
	Comment string `json:"comment,omitempty"`  // reviewer's note on this evidence
	Supports bool  `json:"supports"`           // does this evidence support the verdict?
}

// CriterionResult is the reviewer's assessment of a single acceptance criterion.
type CriterionResult struct {
	Description string `json:"description"`
	Passed      bool   `json:"passed"`
	Comment     string `json:"comment,omitempty"`
}

// ── ReviewSpec on VerificationSpec ───────────────────────────────────────────

// ReviewSpec extends VerificationSpec with reviewer assignment.
type ReviewSpec struct {
	// ReviewerID is the assigned reviewer.
	ReviewerID string `json:"reviewer_id,omitempty"`
	// ReviewerType is "local" (same daemon) or "acp" (remote peer).
	ReviewerType string `json:"reviewer_type,omitempty"` // "local", "acp"
	// AutoAssign lets the system pick a reviewer.
	AutoAssign bool `json:"auto_assign,omitempty"`
	// RequireSignoff gates completion on reviewer approval.
	RequireSignoff bool `json:"require_signoff,omitempty"`
}

// ── Reviewer executor ───────────────────────────────────────────────────────

// ReviewerFunc is called to execute a review. It receives the request and
// returns the result. Implementations can be local (in-process agent call)
// or remote (ACP-based reviewer delegation).
type ReviewerFunc func(ctx context.Context, req ReviewRequest) (ReviewResult, error)

// ── Review pipeline ─────────────────────────────────────────────────────────

// ReviewPipeline orchestrates the reviewer-worker pattern.
type ReviewPipeline struct {
	executor ReviewerFunc
}

// NewReviewPipeline creates a pipeline with the given reviewer executor.
func NewReviewPipeline(executor ReviewerFunc) *ReviewPipeline {
	return &ReviewPipeline{executor: executor}
}

// RequestReview builds a review request from a task and its outputs.
func (p *ReviewPipeline) RequestReview(
	task state.TaskSpec,
	runID string,
	workerID string,
	outputs TaskOutputs,
	reviewSpec ReviewSpec,
	now int64,
) ReviewRequest {
	reviewerID := reviewSpec.ReviewerID
	if reviewerID == "" {
		reviewerID = "auto" // placeholder for auto-assignment
	}

	return ReviewRequest{
		TaskID:             task.TaskID,
		RunID:              runID,
		ReviewerID:         reviewerID,
		WorkerID:           workerID,
		Outputs:            outputs,
		AcceptanceCriteria: task.AcceptanceCriteria,
		VerificationSpec:   task.Verification,
		RequestedAt:        now,
	}
}

// ExecuteReview runs the review and returns the result.
func (p *ReviewPipeline) ExecuteReview(ctx context.Context, req ReviewRequest) (ReviewResult, error) {
	if p.executor == nil {
		return ReviewResult{}, fmt.Errorf("no reviewer executor configured")
	}
	return p.executor(ctx, req)
}

// ValidateResult checks a review result for completeness.
func ValidateReviewResult(result ReviewResult, constraints ReviewConstraints) error {
	if !result.Verdict.Valid() {
		return fmt.Errorf("invalid review verdict %q", result.Verdict)
	}
	if constraints.RequireEvidence && len(result.Evidence) == 0 {
		return fmt.Errorf("review verdict requires evidence")
	}
	if constraints.RequireConfidence && result.Confidence <= 0 {
		return fmt.Errorf("review verdict requires confidence score")
	}
	return nil
}

// ── Review as verification check ────────────────────────────────────────────

// ReviewCheckExecutor implements CheckExecutor for "review" type checks,
// delegating to a ReviewPipeline.
type ReviewCheckExecutor struct {
	pipeline *ReviewPipeline
	spec     ReviewSpec
}

// NewReviewCheckExecutor creates a review executor that delegates to the
// given pipeline.
func NewReviewCheckExecutor(pipeline *ReviewPipeline, spec ReviewSpec) *ReviewCheckExecutor {
	return &ReviewCheckExecutor{pipeline: pipeline, spec: spec}
}

func (e *ReviewCheckExecutor) Type() state.VerificationCheckType {
	return state.VerificationCheckReview
}

func (e *ReviewCheckExecutor) Execute(ctx context.Context, check state.VerificationCheck, task state.TaskSpec, outputs TaskOutputs) (CheckOutcome, error) {
	if e.pipeline == nil {
		return CheckOutcome{}, fmt.Errorf("no review pipeline configured")
	}

	workerID := task.AssignedAgent
	if workerID == "" {
		workerID = task.TaskID // fallback: use task ID as producer identity
	}
	req := e.pipeline.RequestReview(task, task.CurrentRunID, workerID, outputs, e.spec, time.Now().Unix())
	req.Instructions = check.Description

	result, err := e.pipeline.ExecuteReview(ctx, req)
	if err != nil {
		return CheckOutcome{}, fmt.Errorf("review execution failed: %w", err)
	}

	return CheckOutcome{
		Passed:   result.Verdict.IsApproval(),
		Result:   fmt.Sprintf("reviewer %s: %s", result.ReviewerID, result.Verdict),
		Evidence: result.Comments,
		Details: map[string]any{
			"verdict":    string(result.Verdict),
			"reviewer":   result.ReviewerID,
			"confidence": result.Confidence,
		},
	}, nil
}

// ── Apply review to verification spec ───────────────────────────────────────

// ApplyReviewResult updates a VerificationSpec's review check based on the
// review result. Returns the updated spec.
func ApplyReviewResult(spec state.VerificationSpec, result ReviewResult, now int64) state.VerificationSpec {
	spec = spec.Normalize()
	for i, check := range spec.Checks {
		if check.Type != state.VerificationCheckReview {
			continue
		}
		if check.Status.IsTerminal() {
			continue
		}
		if result.Verdict.IsApproval() {
			spec.Checks[i].Status = state.VerificationStatusPassed
		} else {
			spec.Checks[i].Status = state.VerificationStatusFailed
		}
		spec.Checks[i].Result = fmt.Sprintf("%s: %s", result.Verdict, result.Comments)
		spec.Checks[i].Evidence = formatReviewEvidence(result.Evidence)
		spec.Checks[i].EvaluatedAt = now
		spec.Checks[i].EvaluatedBy = result.ReviewerID
		break // apply to first pending review check
	}
	return spec
}

// ── Formatting ──────────────────────────────────────────────────────────────

// FormatReviewResult returns a human-readable review summary.
func FormatReviewResult(r ReviewResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review: %s by %s", r.Verdict, r.ReviewerID)
	if r.Confidence > 0 {
		fmt.Fprintf(&b, " (confidence=%.0f%%)", r.Confidence*100)
	}
	fmt.Fprintln(&b)
	if r.Comments != "" {
		fmt.Fprintf(&b, "  Comments: %s\n", r.Comments)
	}
	for _, e := range r.Evidence {
		marker := "+"
		if !e.Supports {
			marker = "-"
		}
		fmt.Fprintf(&b, "  %s [%s] %s\n", marker, e.Ref, e.Comment)
	}
	for _, cr := range r.CriteriaResults {
		marker := "✓"
		if !cr.Passed {
			marker = "✗"
		}
		fmt.Fprintf(&b, "  %s %s", marker, cr.Description)
		if cr.Comment != "" {
			fmt.Fprintf(&b, " — %s", cr.Comment)
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}

// FormatReviewRequest returns a human-readable review request summary.
func FormatReviewRequest(r ReviewRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review Request: task=%s run=%s\n", r.TaskID, r.RunID)
	fmt.Fprintf(&b, "  Reviewer: %s, Worker: %s\n", r.ReviewerID, r.WorkerID)
	if len(r.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&b, "  Criteria: %d\n", len(r.AcceptanceCriteria))
	}
	if r.Instructions != "" {
		fmt.Fprintf(&b, "  Instructions: %s\n", r.Instructions)
	}
	return b.String()
}

func formatReviewEvidence(evidence []ReviewEvidence) string {
	if len(evidence) == 0 {
		return ""
	}
	var parts []string
	for _, e := range evidence {
		parts = append(parts, fmt.Sprintf("[%s] %s", e.Ref, e.Comment))
	}
	return strings.Join(parts, "; ")
}
