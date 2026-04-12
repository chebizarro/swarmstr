package planner

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"metiq/internal/store/state"
)

// ── Model validation tests ─────────────────────────────────────────────────────

func TestFeedbackRecord_Validate_Valid(t *testing.T) {
	rec := state.FeedbackRecord{
		FeedbackID: "fb-1",
		TaskID:     "task-1",
		Source:     state.FeedbackSourceOperator,
		Severity:   state.FeedbackSeverityInfo,
		Category:   state.FeedbackCategoryGeneral,
		Summary:    "good work",
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFeedbackRecord_Validate_MissingID(t *testing.T) {
	rec := state.FeedbackRecord{
		TaskID:   "t1",
		Source:   state.FeedbackSourceAgent,
		Severity: state.FeedbackSeverityInfo,
		Category: state.FeedbackCategoryGeneral,
		Summary:  "x",
	}
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error for missing feedback_id")
	}
}

func TestFeedbackRecord_Validate_MissingSummary(t *testing.T) {
	rec := state.FeedbackRecord{
		FeedbackID: "fb-1",
		TaskID:     "t1",
		Source:     state.FeedbackSourceAgent,
		Severity:   state.FeedbackSeverityInfo,
		Category:   state.FeedbackCategoryGeneral,
	}
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error for missing summary")
	}
}

func TestFeedbackRecord_Validate_NoLinkage(t *testing.T) {
	rec := state.FeedbackRecord{
		FeedbackID: "fb-1",
		Source:     state.FeedbackSourceAgent,
		Severity:   state.FeedbackSeverityInfo,
		Category:   state.FeedbackCategoryGeneral,
		Summary:    "orphan feedback",
	}
	if err := rec.Validate(); err == nil {
		t.Fatal("expected error for no linkage")
	}
}

func TestFeedbackRecord_Validate_GoalOnlyLinkage(t *testing.T) {
	rec := state.FeedbackRecord{
		FeedbackID: "fb-1",
		GoalID:     "goal-1",
		Source:     state.FeedbackSourceOperator,
		Severity:   state.FeedbackSeverityWarning,
		Category:   state.FeedbackCategorySafety,
		Summary:    "goal-level feedback",
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFeedbackRecord_Normalize(t *testing.T) {
	rec := state.FeedbackRecord{
		FeedbackID: "  fb-1  ",
		TaskID:     "t1",
		Source:     "bogus",
		Severity:   "bogus",
		Category:   "bogus",
		Summary:    "  trimmed  ",
	}
	norm := rec.Normalize()
	if norm.Version != 1 {
		t.Errorf("version = %d, want 1", norm.Version)
	}
	if norm.FeedbackID != "fb-1" {
		t.Errorf("feedback_id = %q, want trimmed", norm.FeedbackID)
	}
	if norm.Summary != "trimmed" {
		t.Errorf("summary = %q, want trimmed", norm.Summary)
	}
	if norm.Source != state.FeedbackSourceSystem {
		t.Errorf("source = %q, want system", norm.Source)
	}
	if norm.Severity != state.FeedbackSeverityInfo {
		t.Errorf("severity = %q, want info", norm.Severity)
	}
	if norm.Category != state.FeedbackCategoryGeneral {
		t.Errorf("category = %q, want general", norm.Category)
	}
}

func TestFeedbackRecord_Normalize_TrimsLinkage(t *testing.T) {
	rec := state.FeedbackRecord{
		FeedbackID: "fb-1",
		GoalID:     "  goal-1  ",
		TaskID:     "  task-1 ",
		RunID:      " run-1  ",
		StepID:     " step-1 ",
		Author:     " alice ",
		SessionID:  " sess-1 ",
		Source:     state.FeedbackSourceAgent,
		Severity:   state.FeedbackSeverityInfo,
		Category:   state.FeedbackCategoryGeneral,
		Summary:    "test",
	}
	norm := rec.Normalize()
	if norm.GoalID != "goal-1" || norm.TaskID != "task-1" || norm.RunID != "run-1" {
		t.Errorf("linkage not trimmed: goal=%q task=%q run=%q", norm.GoalID, norm.TaskID, norm.RunID)
	}
	if norm.StepID != "step-1" || norm.Author != "alice" || norm.SessionID != "sess-1" {
		t.Errorf("provenance not trimmed: step=%q author=%q session=%q", norm.StepID, norm.Author, norm.SessionID)
	}
}

func TestFeedbackRecord_HasLinkage(t *testing.T) {
	if (state.FeedbackRecord{}).HasLinkage() {
		t.Error("empty record should not have linkage")
	}
	if !(state.FeedbackRecord{RunID: "r1"}).HasLinkage() {
		t.Error("run-linked record should have linkage")
	}
}

func TestFeedbackRecord_JSON(t *testing.T) {
	rec := state.FeedbackRecord{
		Version:    1,
		FeedbackID: "fb-99",
		TaskID:     "task-1",
		RunID:      "run-1",
		Source:     state.FeedbackSourceVerification,
		Severity:   state.FeedbackSeverityError,
		Category:   state.FeedbackCategoryCorrectness,
		Summary:    "check failed",
		Detail:     "output mismatch",
		CreatedAt:  1000,
		Meta:       map[string]any{"check_id": "chk-1"},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded state.FeedbackRecord
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.FeedbackID != "fb-99" || decoded.Summary != "check failed" {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
	if decoded.Meta["check_id"] != "chk-1" {
		t.Errorf("meta round-trip: %v", decoded.Meta)
	}
}

// ── Validation helpers ─────────────────────────────────────────────────────────

func TestValidFeedbackSource(t *testing.T) {
	for _, s := range []state.FeedbackSource{
		state.FeedbackSourceOperator, state.FeedbackSourceVerification,
		state.FeedbackSourceReview, state.FeedbackSourceAgent, state.FeedbackSourceSystem,
	} {
		if !state.ValidFeedbackSource(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
	if state.ValidFeedbackSource("unknown") {
		t.Error("unknown should be invalid")
	}
}

func TestValidFeedbackSeverity(t *testing.T) {
	for _, s := range []state.FeedbackSeverity{
		state.FeedbackSeverityInfo, state.FeedbackSeverityWarning,
		state.FeedbackSeverityError, state.FeedbackSeverityCritical,
	} {
		if !state.ValidFeedbackSeverity(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
	if state.ValidFeedbackSeverity("unknown") {
		t.Error("unknown should be invalid")
	}
}

func TestValidFeedbackCategory(t *testing.T) {
	for _, c := range []state.FeedbackCategory{
		state.FeedbackCategoryCorrectness, state.FeedbackCategoryPerformance,
		state.FeedbackCategoryStyle, state.FeedbackCategoryPolicy,
		state.FeedbackCategorySafety, state.FeedbackCategoryGeneral,
	} {
		if !state.ValidFeedbackCategory(c) {
			t.Errorf("expected %q to be valid", c)
		}
	}
	if state.ValidFeedbackCategory("unknown") {
		t.Error("unknown should be invalid")
	}
}

// ── Collector tests ────────────────────────────────────────────────────────────

func TestCollector_Capture_AutoID(t *testing.T) {
	c := NewFeedbackCollector("test")
	rec := c.Capture(state.FeedbackRecord{
		TaskID:   "t1",
		Source:   state.FeedbackSourceAgent,
		Severity: state.FeedbackSeverityInfo,
		Category: state.FeedbackCategoryGeneral,
		Summary:  "auto id",
	})
	if rec.FeedbackID != "test-1" {
		t.Errorf("feedback_id = %q, want test-1", rec.FeedbackID)
	}
	if rec.CreatedAt == 0 {
		t.Error("created_at should be auto-set")
	}
}

func TestCollector_CaptureValidated_RejectsInvalid(t *testing.T) {
	c := NewFeedbackCollector("fb")
	// No linkage → should fail validation.
	_, err := c.CaptureValidated(state.FeedbackRecord{
		Source:   state.FeedbackSourceAgent,
		Severity: state.FeedbackSeverityInfo,
		Category: state.FeedbackCategoryGeneral,
		Summary:  "orphan",
	})
	if err == nil {
		t.Fatal("expected validation error for no linkage")
	}
	// Collector should not retain the invalid record.
	if c.Count() != 0 {
		t.Errorf("count = %d, want 0 after rejected capture", c.Count())
	}
}

func TestCollector_CaptureValidated_AcceptsValid(t *testing.T) {
	c := NewFeedbackCollector("fb")
	rec, err := c.CaptureValidated(state.FeedbackRecord{
		TaskID:   "t1",
		Source:   state.FeedbackSourceAgent,
		Severity: state.FeedbackSeverityInfo,
		Category: state.FeedbackCategoryGeneral,
		Summary:  "valid",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.FeedbackID == "" {
		t.Error("expected auto-generated ID")
	}
	if c.Count() != 1 {
		t.Errorf("count = %d, want 1", c.Count())
	}
}

func TestCollector_Capture_ExplicitID(t *testing.T) {
	c := NewFeedbackCollector("fb")
	rec := c.Capture(state.FeedbackRecord{
		FeedbackID: "custom-id",
		TaskID:     "t1",
		Source:     state.FeedbackSourceOperator,
		Severity:   state.FeedbackSeverityWarning,
		Category:   state.FeedbackCategoryPolicy,
		Summary:    "explicit",
	})
	if rec.FeedbackID != "custom-id" {
		t.Errorf("feedback_id = %q, want custom-id", rec.FeedbackID)
	}
}

func TestCollector_Records(t *testing.T) {
	c := NewFeedbackCollector("fb")
	c.Capture(state.FeedbackRecord{TaskID: "t1", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityInfo, Category: state.FeedbackCategoryGeneral, Summary: "a"})
	c.Capture(state.FeedbackRecord{TaskID: "t2", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityInfo, Category: state.FeedbackCategoryGeneral, Summary: "b"})
	recs := c.Records()
	if len(recs) != 2 {
		t.Fatalf("len = %d, want 2", len(recs))
	}
	// Verify snapshot isolation.
	recs[0].Summary = "mutated"
	if c.Records()[0].Summary == "mutated" {
		t.Error("records should be a snapshot copy")
	}
}

func TestCollector_Count(t *testing.T) {
	c := NewFeedbackCollector("fb")
	if c.Count() != 0 {
		t.Error("empty collector should have count 0")
	}
	c.Capture(state.FeedbackRecord{TaskID: "t1", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityInfo, Category: state.FeedbackCategoryGeneral, Summary: "x"})
	if c.Count() != 1 {
		t.Errorf("count = %d, want 1", c.Count())
	}
}

func TestCollector_FilterByTask(t *testing.T) {
	c := NewFeedbackCollector("fb")
	c.Capture(state.FeedbackRecord{TaskID: "t1", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityInfo, Category: state.FeedbackCategoryGeneral, Summary: "a"})
	c.Capture(state.FeedbackRecord{TaskID: "t2", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityInfo, Category: state.FeedbackCategoryGeneral, Summary: "b"})
	c.Capture(state.FeedbackRecord{TaskID: "t1", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityInfo, Category: state.FeedbackCategoryGeneral, Summary: "c"})
	got := c.FilterByTask("t1")
	if len(got) != 2 {
		t.Fatalf("filter by task t1 = %d, want 2", len(got))
	}
}

func TestCollector_FilterByRun(t *testing.T) {
	c := NewFeedbackCollector("fb")
	c.Capture(state.FeedbackRecord{RunID: "r1", GoalID: "g1", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityInfo, Category: state.FeedbackCategoryGeneral, Summary: "a"})
	c.Capture(state.FeedbackRecord{RunID: "r2", GoalID: "g1", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityInfo, Category: state.FeedbackCategoryGeneral, Summary: "b"})
	got := c.FilterByRun("r1")
	if len(got) != 1 {
		t.Fatalf("filter by run r1 = %d, want 1", len(got))
	}
}

func TestCollector_FilterByGoal(t *testing.T) {
	c := NewFeedbackCollector("fb")
	c.Capture(state.FeedbackRecord{GoalID: "g1", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityInfo, Category: state.FeedbackCategoryGeneral, Summary: "a"})
	c.Capture(state.FeedbackRecord{GoalID: "g2", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityInfo, Category: state.FeedbackCategoryGeneral, Summary: "b"})
	got := c.FilterByGoal("g1")
	if len(got) != 1 {
		t.Fatalf("filter by goal g1 = %d, want 1", len(got))
	}
}

func TestCollector_FilterBySeverity(t *testing.T) {
	c := NewFeedbackCollector("fb")
	c.Capture(state.FeedbackRecord{TaskID: "t1", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityInfo, Category: state.FeedbackCategoryGeneral, Summary: "info"})
	c.Capture(state.FeedbackRecord{TaskID: "t1", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityWarning, Category: state.FeedbackCategoryGeneral, Summary: "warn"})
	c.Capture(state.FeedbackRecord{TaskID: "t1", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityError, Category: state.FeedbackCategoryGeneral, Summary: "err"})
	c.Capture(state.FeedbackRecord{TaskID: "t1", Source: state.FeedbackSourceAgent, Severity: state.FeedbackSeverityCritical, Category: state.FeedbackCategoryGeneral, Summary: "crit"})

	got := c.FilterBySeverity(state.FeedbackSeverityError)
	if len(got) != 2 {
		t.Fatalf("filter ≥error = %d, want 2 (error+critical)", len(got))
	}
	got = c.FilterBySeverity(state.FeedbackSeverityInfo)
	if len(got) != 4 {
		t.Fatalf("filter ≥info = %d, want 4", len(got))
	}
}

// ── Convenience helpers ────────────────────────────────────────────────────────

func TestCaptureOperatorFeedback(t *testing.T) {
	c := NewFeedbackCollector("fb")
	rec := CaptureOperatorFeedback(c, "task-1", "run-1", "goal-1", "looks good", state.FeedbackSeverityInfo, "alice")
	if rec.Source != state.FeedbackSourceOperator {
		t.Errorf("source = %q, want operator", rec.Source)
	}
	if rec.Author != "alice" {
		t.Errorf("author = %q, want alice", rec.Author)
	}
	if rec.TaskID != "task-1" || rec.RunID != "run-1" || rec.GoalID != "goal-1" {
		t.Error("linkage mismatch")
	}
}

func TestCaptureVerificationFailure(t *testing.T) {
	c := NewFeedbackCollector("fb")
	rec := CaptureVerificationFailure(c, "task-1", "run-1", "goal-1", "chk-output", "output mismatch")
	if rec.Source != state.FeedbackSourceVerification {
		t.Errorf("source = %q, want verification", rec.Source)
	}
	if rec.Severity != state.FeedbackSeverityError {
		t.Errorf("severity = %q, want error", rec.Severity)
	}
	if rec.Category != state.FeedbackCategoryCorrectness {
		t.Errorf("category = %q, want correctness", rec.Category)
	}
	if !strings.Contains(rec.Summary, "chk-output") {
		t.Errorf("summary should contain check ID: %q", rec.Summary)
	}
	if rec.Meta["check_id"] != "chk-output" {
		t.Errorf("meta check_id = %v", rec.Meta["check_id"])
	}
}

func TestCaptureReviewFeedback(t *testing.T) {
	c := NewFeedbackCollector("fb")
	rec := CaptureReviewFeedback(c, "task-1", "run-1", "goal-1",
		"could be faster", "optimize loop", state.FeedbackCategoryPerformance,
		state.FeedbackSeverityWarning, "bob")
	if rec.Source != state.FeedbackSourceReview {
		t.Errorf("source = %q, want review", rec.Source)
	}
	if rec.Detail != "optimize loop" {
		t.Errorf("detail = %q", rec.Detail)
	}
	if rec.Category != state.FeedbackCategoryPerformance {
		t.Errorf("category = %q, want performance", rec.Category)
	}
	if rec.Author != "bob" {
		t.Errorf("author = %q, want bob", rec.Author)
	}
}

// ── Formatting ─────────────────────────────────────────────────────────────────

func TestFormatFeedbackRecord(t *testing.T) {
	rec := state.FeedbackRecord{
		FeedbackID: "fb-1",
		TaskID:     "task-1",
		GoalID:     "goal-1",
		RunID:      "run-1",
		Source:     state.FeedbackSourceOperator,
		Severity:   state.FeedbackSeverityWarning,
		Category:   state.FeedbackCategoryPolicy,
		Summary:    "policy violation",
		Detail:     "used unauthorized tool",
	}
	out := FormatFeedbackRecord(rec)
	for _, want := range []string{"⚠️", "operator", "policy violation", "used unauthorized tool", "task=task-1", "goal=goal-1", "run=run-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("format missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatFeedbackSummary_Empty(t *testing.T) {
	if got := FormatFeedbackSummary(nil); got != "No feedback captured." {
		t.Errorf("got %q", got)
	}
}

func TestFormatFeedbackSummary_WithRecords(t *testing.T) {
	records := []state.FeedbackRecord{
		{Severity: state.FeedbackSeverityInfo},
		{Severity: state.FeedbackSeverityError},
		{Severity: state.FeedbackSeverityError},
		{Severity: state.FeedbackSeverityCritical},
	}
	out := FormatFeedbackSummary(records)
	if !strings.Contains(out, "4 records") {
		t.Errorf("missing count in %q", out)
	}
	if !strings.Contains(out, "error=2") {
		t.Errorf("missing error count in %q", out)
	}
	if !strings.Contains(out, "critical=1") {
		t.Errorf("missing critical count in %q", out)
	}
}

// ── Concurrency ────────────────────────────────────────────────────────────────

func TestCollector_ConcurrentCapture(t *testing.T) {
	c := NewFeedbackCollector("fb")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Capture(state.FeedbackRecord{
				TaskID:   "t1",
				Source:   state.FeedbackSourceAgent,
				Severity: state.FeedbackSeverityInfo,
				Category: state.FeedbackCategoryGeneral,
				Summary:  "concurrent",
			})
		}()
	}
	wg.Wait()
	if c.Count() != 100 {
		t.Errorf("count = %d, want 100", c.Count())
	}
}
