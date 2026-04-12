package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// ── Review verdict ──────────────────────────────────────────────────────────

func TestReviewVerdict_Valid(t *testing.T) {
	for _, v := range []ReviewVerdict{ReviewApproved, ReviewRejected, ReviewRevise} {
		if !v.Valid() {
			t.Errorf("expected %q to be valid", v)
		}
	}
	if ReviewVerdict("unknown").Valid() {
		t.Error("expected 'unknown' to be invalid")
	}
}

func TestReviewVerdict_IsApproval(t *testing.T) {
	if !ReviewApproved.IsApproval() {
		t.Error("approved should be an approval")
	}
	if ReviewRejected.IsApproval() {
		t.Error("rejected should not be an approval")
	}
	if ReviewRevise.IsApproval() {
		t.Error("revise should not be an approval")
	}
}

// ── Review pipeline ─────────────────────────────────────────────────────────

func TestReviewPipeline_RequestReview(t *testing.T) {
	pipeline := NewReviewPipeline(nil)
	task := state.TaskSpec{
		TaskID: "task-1", Title: "test",
		AcceptanceCriteria: []state.TaskAcceptanceCriterion{
			{Description: "output is correct"},
		},
	}
	spec := ReviewSpec{ReviewerID: "reviewer-1", RequireSignoff: true}
	outputs := TaskOutputs{RawOutput: "hello"}
	req := pipeline.RequestReview(task, "run-1", "worker-1", outputs, spec, 12345)

	if req.TaskID != "task-1" || req.RunID != "run-1" {
		t.Fatal("task/run ID mismatch")
	}
	if req.ReviewerID != "reviewer-1" {
		t.Fatalf("expected reviewer-1, got %s", req.ReviewerID)
	}
	if req.WorkerID != "worker-1" {
		t.Fatalf("expected worker-1, got %s", req.WorkerID)
	}
	if len(req.AcceptanceCriteria) != 1 {
		t.Fatal("expected 1 acceptance criterion")
	}
}

func TestReviewPipeline_AutoAssignReviewer(t *testing.T) {
	pipeline := NewReviewPipeline(nil)
	task := state.TaskSpec{TaskID: "task-1", Title: "test"}
	spec := ReviewSpec{AutoAssign: true} // no explicit reviewer
	req := pipeline.RequestReview(task, "run-1", "w1", TaskOutputs{}, spec, 100)
	if req.ReviewerID != "auto" {
		t.Fatalf("expected auto reviewer, got %s", req.ReviewerID)
	}
}

func TestReviewPipeline_ExecuteReview_NoExecutor(t *testing.T) {
	pipeline := NewReviewPipeline(nil)
	_, err := pipeline.ExecuteReview(context.Background(), ReviewRequest{})
	if err == nil {
		t.Fatal("expected error with nil executor")
	}
}

func TestReviewPipeline_ExecuteReview_Approved(t *testing.T) {
	executor := func(_ context.Context, req ReviewRequest) (ReviewResult, error) {
		return ReviewResult{
			TaskID:     req.TaskID,
			RunID:      req.RunID,
			ReviewerID: req.ReviewerID,
			Verdict:    ReviewApproved,
			Comments:   "looks good",
			Confidence: 0.95,
			ReviewedAt: time.Now().Unix(),
		}, nil
	}
	pipeline := NewReviewPipeline(executor)
	req := ReviewRequest{TaskID: "t1", RunID: "r1", ReviewerID: "rev-1"}
	result, err := pipeline.ExecuteReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != ReviewApproved {
		t.Fatalf("expected approved, got %s", result.Verdict)
	}
	if result.Confidence != 0.95 {
		t.Fatalf("expected 0.95, got %f", result.Confidence)
	}
}

func TestReviewPipeline_ExecuteReview_Rejected(t *testing.T) {
	executor := func(_ context.Context, req ReviewRequest) (ReviewResult, error) {
		return ReviewResult{
			ReviewerID: req.ReviewerID,
			Verdict:    ReviewRejected,
			Comments:   "output is incorrect",
			Evidence: []ReviewEvidence{
				{Ref: "output.summary", Excerpt: "wrong answer", Comment: "factually incorrect", Supports: false},
			},
		}, nil
	}
	pipeline := NewReviewPipeline(executor)
	result, err := pipeline.ExecuteReview(context.Background(), ReviewRequest{ReviewerID: "rev-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != ReviewRejected {
		t.Fatalf("expected rejected, got %s", result.Verdict)
	}
	if len(result.Evidence) != 1 {
		t.Fatal("expected 1 evidence item")
	}
}

func TestReviewPipeline_ExecuteReview_Error(t *testing.T) {
	executor := func(_ context.Context, _ ReviewRequest) (ReviewResult, error) {
		return ReviewResult{}, fmt.Errorf("reviewer unavailable")
	}
	pipeline := NewReviewPipeline(executor)
	_, err := pipeline.ExecuteReview(context.Background(), ReviewRequest{})
	if err == nil || err.Error() != "reviewer unavailable" {
		t.Fatalf("expected reviewer unavailable error, got %v", err)
	}
}

// ── Validate review result ──────────────────────────────────────────────────

func TestValidateReviewResult_OK(t *testing.T) {
	result := ReviewResult{Verdict: ReviewApproved}
	if err := ValidateReviewResult(result, ReviewConstraints{}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReviewResult_InvalidVerdict(t *testing.T) {
	result := ReviewResult{Verdict: "nonsense"}
	if err := ValidateReviewResult(result, ReviewConstraints{}); err == nil {
		t.Fatal("expected error for invalid verdict")
	}
}

func TestValidateReviewResult_RequireEvidence(t *testing.T) {
	result := ReviewResult{Verdict: ReviewApproved}
	err := ValidateReviewResult(result, ReviewConstraints{RequireEvidence: true})
	if err == nil {
		t.Fatal("expected error without evidence")
	}

	result.Evidence = []ReviewEvidence{{Ref: "output", Supports: true}}
	if err := ValidateReviewResult(result, ReviewConstraints{RequireEvidence: true}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReviewResult_RequireConfidence(t *testing.T) {
	result := ReviewResult{Verdict: ReviewApproved}
	err := ValidateReviewResult(result, ReviewConstraints{RequireConfidence: true})
	if err == nil {
		t.Fatal("expected error without confidence")
	}

	result.Confidence = 0.9
	if err := ValidateReviewResult(result, ReviewConstraints{RequireConfidence: true}); err != nil {
		t.Fatal(err)
	}
}

// ── ReviewCheckExecutor ─────────────────────────────────────────────────────

func TestReviewCheckExecutor_Approved(t *testing.T) {
	executor := func(_ context.Context, _ ReviewRequest) (ReviewResult, error) {
		return ReviewResult{ReviewerID: "rev-1", Verdict: ReviewApproved, Comments: "LGTM"}, nil
	}
	pipeline := NewReviewPipeline(executor)
	rce := NewReviewCheckExecutor(pipeline, ReviewSpec{ReviewerID: "rev-1"})

	if rce.Type() != state.VerificationCheckReview {
		t.Fatalf("expected review type, got %s", rce.Type())
	}

	check := state.VerificationCheck{CheckID: "r1", Type: state.VerificationCheckReview, Description: "needs review"}
	task := state.TaskSpec{TaskID: "t1", Title: "test", Instructions: "do it"}
	outcome, err := rce.Execute(context.Background(), check, task, TaskOutputs{})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Passed {
		t.Fatal("expected pass for approved review")
	}
}

func TestReviewCheckExecutor_Rejected(t *testing.T) {
	executor := func(_ context.Context, _ ReviewRequest) (ReviewResult, error) {
		return ReviewResult{ReviewerID: "rev-1", Verdict: ReviewRejected, Comments: "bad"}, nil
	}
	rce := NewReviewCheckExecutor(NewReviewPipeline(executor), ReviewSpec{ReviewerID: "rev-1"})
	check := state.VerificationCheck{CheckID: "r1", Type: state.VerificationCheckReview}
	task := state.TaskSpec{TaskID: "t1", Title: "test", Instructions: "do it"}
	outcome, err := rce.Execute(context.Background(), check, task, TaskOutputs{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Passed {
		t.Fatal("expected fail for rejected review")
	}
}

func TestReviewCheckExecutor_NoPipeline(t *testing.T) {
	rce := NewReviewCheckExecutor(nil, ReviewSpec{})
	check := state.VerificationCheck{CheckID: "r1", Type: state.VerificationCheckReview}
	task := state.TaskSpec{TaskID: "t1", Title: "test", Instructions: "do it"}
	_, err := rce.Execute(context.Background(), check, task, TaskOutputs{})
	if err == nil {
		t.Fatal("expected error with nil pipeline")
	}
}

// ── Apply review result to verification spec ────────────────────────────────

func TestApplyReviewResult_Approved(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "r1", Type: state.VerificationCheckReview, Required: true, Status: state.VerificationStatusPending},
		},
	}
	result := ReviewResult{ReviewerID: "rev-1", Verdict: ReviewApproved, Comments: "good"}
	updated := ApplyReviewResult(spec, result, 12345)
	if updated.Checks[0].Status != state.VerificationStatusPassed {
		t.Fatalf("expected passed, got %s", updated.Checks[0].Status)
	}
	if updated.Checks[0].EvaluatedBy != "rev-1" {
		t.Fatalf("expected rev-1, got %s", updated.Checks[0].EvaluatedBy)
	}
}

func TestApplyReviewResult_Rejected(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "r1", Type: state.VerificationCheckReview, Required: true, Status: state.VerificationStatusPending},
		},
	}
	result := ReviewResult{ReviewerID: "rev-1", Verdict: ReviewRejected, Comments: "needs work"}
	updated := ApplyReviewResult(spec, result, 12345)
	if updated.Checks[0].Status != state.VerificationStatusFailed {
		t.Fatalf("expected failed, got %s", updated.Checks[0].Status)
	}
}

func TestApplyReviewResult_SkipsTerminal(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "r1", Type: state.VerificationCheckReview, Required: true, Status: state.VerificationStatusPassed},
		},
	}
	result := ReviewResult{ReviewerID: "rev-1", Verdict: ReviewRejected}
	updated := ApplyReviewResult(spec, result, 12345)
	// Should not overwrite already-passed check.
	if updated.Checks[0].Status != state.VerificationStatusPassed {
		t.Fatalf("expected passed (unchanged), got %s", updated.Checks[0].Status)
	}
}

// ── Formatting ──────────────────────────────────────────────────────────────

func TestFormatReviewResult(t *testing.T) {
	result := ReviewResult{
		ReviewerID: "rev-1",
		Verdict:    ReviewApproved,
		Comments:   "well done",
		Confidence: 0.9,
		Evidence: []ReviewEvidence{
			{Ref: "output.summary", Comment: "accurate", Supports: true},
		},
		CriteriaResults: []CriterionResult{
			{Description: "output is correct", Passed: true},
			{Description: "includes citations", Passed: false, Comment: "no citations found"},
		},
	}
	s := FormatReviewResult(result)
	if s == "" {
		t.Fatal("expected non-empty format")
	}
}

func TestFormatReviewRequest(t *testing.T) {
	req := ReviewRequest{
		TaskID: "t1", RunID: "r1", ReviewerID: "rev-1", WorkerID: "w1",
		Instructions: "check correctness",
		AcceptanceCriteria: []state.TaskAcceptanceCriterion{{Description: "is correct"}},
	}
	s := FormatReviewRequest(req)
	if s == "" {
		t.Fatal("expected non-empty format")
	}
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestReviewResult_JSON(t *testing.T) {
	r := ReviewResult{
		TaskID: "t1", RunID: "r1", ReviewerID: "rev-1",
		Verdict: ReviewApproved, Comments: "good", Confidence: 0.95,
		Evidence: []ReviewEvidence{{Ref: "output", Supports: true}},
		CriteriaResults: []CriterionResult{{Description: "ok", Passed: true}},
		ReviewedAt: 12345, Duration: 5 * time.Second,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var r2 ReviewResult
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatal(err)
	}
	if r2.Verdict != ReviewApproved || r2.Confidence != 0.95 {
		t.Fatalf("round-trip mismatch: %+v", r2)
	}
}

func TestReviewRequest_JSON(t *testing.T) {
	req := ReviewRequest{
		TaskID: "t1", RunID: "r1", ReviewerID: "rev-1",
		WorkerID: "w1", RequestedAt: 12345,
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var req2 ReviewRequest
	if err := json.Unmarshal(data, &req2); err != nil {
		t.Fatal(err)
	}
	if req2.TaskID != "t1" || req2.ReviewerID != "rev-1" {
		t.Fatalf("round-trip mismatch: %+v", req2)
	}
}

func TestReviewSpec_JSON(t *testing.T) {
	s := ReviewSpec{ReviewerID: "rev-1", ReviewerType: "local", RequireSignoff: true}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var s2 ReviewSpec
	if err := json.Unmarshal(data, &s2); err != nil {
		t.Fatal(err)
	}
	if s2.ReviewerID != "rev-1" || !s2.RequireSignoff {
		t.Fatalf("round-trip mismatch: %+v", s2)
	}
}

// ── End-to-end ──────────────────────────────────────────────────────────────

func TestEndToEnd_ReviewApprovalFlow(t *testing.T) {
	// Simulate: worker produces output → reviewer approves → spec updated.
	approver := func(_ context.Context, req ReviewRequest) (ReviewResult, error) {
		return ReviewResult{
			TaskID:     req.TaskID,
			RunID:      req.RunID,
			ReviewerID: req.ReviewerID,
			Verdict:    ReviewApproved,
			Comments:   "output meets all criteria",
			Confidence: 0.92,
			Evidence: []ReviewEvidence{
				{Ref: "output.summary", Comment: "accurate summary", Supports: true},
			},
			CriteriaResults: []CriterionResult{
				{Description: "output is correct", Passed: true},
				{Description: "includes evidence", Passed: true},
			},
			ReviewedAt: 200,
		}, nil
	}

	pipeline := NewReviewPipeline(approver)
	task := state.TaskSpec{
		TaskID: "task-1", Title: "analyze data", Instructions: "do it",
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyRequired,
			Checks: []state.VerificationCheck{
				{CheckID: "r1", Type: state.VerificationCheckReview, Description: "reviewer signoff", Required: true},
			},
		},
	}
	outputs := TaskOutputs{RawOutput: "analysis complete"}
	spec := ReviewSpec{ReviewerID: "reviewer-agent", RequireSignoff: true}

	// Build request.
	req := pipeline.RequestReview(task, "run-1", "worker-1", outputs, spec, 100)
	if req.ReviewerID != "reviewer-agent" {
		t.Fatal("wrong reviewer")
	}

	// Execute review.
	result, err := pipeline.ExecuteReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verdict.IsApproval() {
		t.Fatal("expected approval")
	}

	// Apply to verification spec.
	updated := ApplyReviewResult(task.Verification, result, 200)
	if updated.Checks[0].Status != state.VerificationStatusPassed {
		t.Fatalf("expected passed, got %s", updated.Checks[0].Status)
	}

	// Validate result.
	if err := ValidateReviewResult(result, ReviewConstraints{RequireEvidence: true, RequireConfidence: true}); err != nil {
		t.Fatal(err)
	}
}

func TestEndToEnd_ReviewRejectionReworkFlow(t *testing.T) {
	callCount := 0
	reviewer := func(_ context.Context, req ReviewRequest) (ReviewResult, error) {
		callCount++
		if callCount == 1 {
			return ReviewResult{
				ReviewerID: req.ReviewerID,
				Verdict:    ReviewRejected,
				Comments:   "missing citations",
			}, nil
		}
		return ReviewResult{
			ReviewerID: req.ReviewerID,
			Verdict:    ReviewApproved,
			Comments:   "citations added, approved",
		}, nil
	}

	pipeline := NewReviewPipeline(reviewer)
	task := state.TaskSpec{
		TaskID: "task-1", Title: "research", Instructions: "do it",
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyRequired,
			Checks: []state.VerificationCheck{
				{CheckID: "r1", Type: state.VerificationCheckReview, Required: true},
			},
		},
	}
	spec := ReviewSpec{ReviewerID: "rev-1"}

	// Round 1: rejected.
	req1 := pipeline.RequestReview(task, "run-1", "w1", TaskOutputs{RawOutput: "draft"}, spec, 100)
	result1, _ := pipeline.ExecuteReview(context.Background(), req1)
	if result1.Verdict != ReviewRejected {
		t.Fatal("expected rejection")
	}
	updated1 := ApplyReviewResult(task.Verification, result1, 100)
	if updated1.Checks[0].Status != state.VerificationStatusFailed {
		t.Fatal("expected failed")
	}

	// Round 2: worker reworks, fresh spec with pending check.
	task.Verification = state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "r1", Type: state.VerificationCheckReview, Required: true, Status: state.VerificationStatusPending},
		},
	}
	req2 := pipeline.RequestReview(task, "run-1", "w1", TaskOutputs{RawOutput: "improved with citations"}, spec, 200)
	result2, _ := pipeline.ExecuteReview(context.Background(), req2)
	if result2.Verdict != ReviewApproved {
		t.Fatal("expected approval on rework")
	}
	updated2 := ApplyReviewResult(task.Verification, result2, 200)
	if updated2.Checks[0].Status != state.VerificationStatusPassed {
		t.Fatal("expected passed after rework")
	}
}
