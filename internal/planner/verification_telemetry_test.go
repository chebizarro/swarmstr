package planner

import (
	"encoding/json"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// ── Telemetry emitter ───────────────────────────────────────────────────────

func TestVerificationTelemetry_EmitAndCollect(t *testing.T) {
	tel := NewVerificationTelemetry(nil)
	tel.Emit(VerificationEvent{Type: VerifEventStarted, TaskID: "t1", CreatedAt: 100})
	tel.Emit(VerificationEvent{Type: VerifEventCompleted, TaskID: "t1", CreatedAt: 200})

	events := tel.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != VerifEventStarted {
		t.Fatalf("expected started, got %s", events[0].Type)
	}
}

func TestVerificationTelemetry_Sink(t *testing.T) {
	var received []VerificationEvent
	sink := func(e VerificationEvent) { received = append(received, e) }
	tel := NewVerificationTelemetry(sink)

	tel.Emit(VerificationEvent{Type: VerifEventStarted, TaskID: "t1"})
	tel.Emit(VerificationEvent{Type: VerifEventCheckPass, TaskID: "t1", CheckID: "c1"})

	if len(received) != 2 {
		t.Fatalf("expected 2 events sunk, got %d", len(received))
	}
}

func TestVerificationTelemetry_NilSink(t *testing.T) {
	tel := NewVerificationTelemetry(nil)
	// Should not panic with nil sink.
	tel.Emit(VerificationEvent{Type: VerifEventStarted})
	if len(tel.Events()) != 1 {
		t.Fatal("expected 1 event")
	}
}

// ── Nil telemetry safety ────────────────────────────────────────────────────

func TestEmitHelpers_NilTelemetry(t *testing.T) {
	// Should not panic with nil telemetry.
	EmitRuntimeEvents(nil, "t1", "r1", RuntimeResult{}, 100)
	EmitGateEvent(nil, "t1", "r1", GateResult{}, 100)
	EmitReviewEvent(nil, "t1", "r1", ReviewResult{}, 100)
}

// ── EmitRuntimeEvents ───────────────────────────────────────────────────────

func TestEmitRuntimeEvents_AllChecksPassed(t *testing.T) {
	tel := NewVerificationTelemetry(nil)
	result := RuntimeResult{
		Passed:  true,
		Summary: "2/2 passed",
		Duration: 50 * time.Millisecond,
		CheckResults: []CheckResult{
			{CheckID: "s1", Type: state.VerificationCheckSchema, Outcome: CheckOutcome{Passed: true, Result: "ok"}},
			{CheckID: "e1", Type: state.VerificationCheckEvidence, Outcome: CheckOutcome{Passed: true, Result: "ok"}},
		},
	}

	EmitRuntimeEvents(tel, "task-1", "run-1", result, 100)
	events := tel.Events()

	// Expected: started + 2 check passes + completed = 4
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	if events[0].Type != VerifEventStarted {
		t.Errorf("expected started, got %s", events[0].Type)
	}
	if events[1].Type != VerifEventCheckPass {
		t.Errorf("expected check pass, got %s", events[1].Type)
	}
	if events[3].Type != VerifEventCompleted {
		t.Errorf("expected completed, got %s", events[3].Type)
	}
	if events[3].Status != "passed" {
		t.Errorf("expected passed status, got %s", events[3].Status)
	}
}

func TestEmitRuntimeEvents_WithFailures(t *testing.T) {
	tel := NewVerificationTelemetry(nil)
	result := RuntimeResult{
		Passed:  false,
		Summary: "1/2 passed",
		CheckResults: []CheckResult{
			{CheckID: "s1", Outcome: CheckOutcome{Passed: true}},
			{CheckID: "s2", Outcome: CheckOutcome{Passed: false, Result: "missing field"}},
		},
	}

	EmitRuntimeEvents(tel, "t1", "r1", result, 100)
	events := tel.Events()

	var fails int
	for _, e := range events {
		if e.Type == VerifEventCheckFail {
			fails++
		}
	}
	if fails != 1 {
		t.Fatalf("expected 1 fail event, got %d", fails)
	}
}

func TestEmitRuntimeEvents_WithError(t *testing.T) {
	tel := NewVerificationTelemetry(nil)
	result := RuntimeResult{
		Passed: false,
		CheckResults: []CheckResult{
			{CheckID: "c1", Error: "executor crashed", Outcome: CheckOutcome{Passed: false}},
		},
	}

	EmitRuntimeEvents(tel, "t1", "r1", result, 100)
	events := tel.Events()

	var errors int
	for _, e := range events {
		if e.Type == VerifEventCheckErr {
			errors++
		}
	}
	if errors != 1 {
		t.Fatalf("expected 1 error event, got %d", errors)
	}
}

// ── EmitGateEvent ───────────────────────────────────────────────────────────

func TestEmitGateEvent_Allow(t *testing.T) {
	tel := NewVerificationTelemetry(nil)
	gate := GateResult{Decision: GateAllow, Reason: "all checks passed"}
	EmitGateEvent(tel, "t1", "r1", gate, 100)

	events := tel.Events()
	if len(events) != 1 || events[0].Type != VerifEventGateAllow {
		t.Fatal("expected gate allow event")
	}
}

func TestEmitGateEvent_Block(t *testing.T) {
	tel := NewVerificationTelemetry(nil)
	gate := GateResult{Decision: GateBlock, Reason: "schema failed", FailedChecks: []string{"s1"}}
	EmitGateEvent(tel, "t1", "r1", gate, 100)

	events := tel.Events()
	if len(events) != 1 || events[0].Type != VerifEventGateBlock {
		t.Fatal("expected gate block event")
	}
	if events[0].GateAction != "block" {
		t.Fatalf("expected block action, got %s", events[0].GateAction)
	}
}

// ── EmitReviewEvent ─────────────────────────────────────────────────────────

func TestEmitReviewEvent(t *testing.T) {
	tel := NewVerificationTelemetry(nil)
	review := ReviewResult{
		ReviewerID: "rev-1", Verdict: ReviewApproved,
		Comments: "LGTM", Confidence: 0.95,
	}
	EmitReviewEvent(tel, "t1", "r1", review, 100)

	events := tel.Events()
	if len(events) != 1 || events[0].Type != VerifEventReview {
		t.Fatal("expected review event")
	}
	if events[0].ReviewerID != "rev-1" {
		t.Fatalf("expected rev-1, got %s", events[0].ReviewerID)
	}
	if events[0].Confidence != 0.95 {
		t.Fatalf("expected 0.95, got %f", events[0].Confidence)
	}
}

// ── BuildVerificationSummary ────────────────────────────────────────────────

func TestBuildVerificationSummary_FromSpec(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "s1", Type: state.VerificationCheckSchema, Required: true, Status: state.VerificationStatusPassed, Result: "ok"},
			{CheckID: "e1", Type: state.VerificationCheckEvidence, Required: true, Status: state.VerificationStatusFailed, Result: "missing"},
			{CheckID: "r1", Type: state.VerificationCheckReview, Required: false, Status: state.VerificationStatusPending},
		},
		VerifiedBy: "agent-1",
		VerifiedAt: 12345,
	}
	summary := BuildVerificationSummary("t1", "r1", spec, nil, nil)
	if summary.TotalChecks != 3 {
		t.Fatalf("expected 3, got %d", summary.TotalChecks)
	}
	if summary.PassedChecks != 1 || summary.FailedChecks != 1 || summary.PendingChecks != 1 {
		t.Fatalf("counts wrong: passed=%d failed=%d pending=%d",
			summary.PassedChecks, summary.FailedChecks, summary.PendingChecks)
	}
	if summary.Passed {
		t.Fatal("expected not passed with failures")
	}
}

func TestBuildVerificationSummary_WithRuntimeResult(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "s1", Required: true, Status: state.VerificationStatusPassed},
		},
	}
	result := &RuntimeResult{
		Passed: true, Duration: 50 * time.Millisecond,
		UpdatedSpec: state.VerificationSpec{VerifiedAt: 200, VerifiedBy: "agent"},
	}
	summary := BuildVerificationSummary("t1", "r1", spec, result, nil)
	if !summary.Passed {
		t.Fatal("expected passed")
	}
	if summary.VerifiedAt != 200 {
		t.Fatalf("expected VerifiedAt=200, got %d", summary.VerifiedAt)
	}
}

func TestBuildVerificationSummary_WithGate(t *testing.T) {
	spec := state.VerificationSpec{Policy: state.VerificationPolicyRequired}
	gate := &GateResult{Decision: GateBlock}
	summary := BuildVerificationSummary("t1", "r1", spec, nil, gate)
	if summary.GateDecision != "block" {
		t.Fatalf("expected block, got %s", summary.GateDecision)
	}
}

// ── Formatting ──────────────────────────────────────────────────────────────

func TestFormatVerificationSummary(t *testing.T) {
	summary := VerificationSummary{
		TaskID: "t1", RunID: "r1", Policy: state.VerificationPolicyRequired,
		TotalChecks: 3, PassedChecks: 2, FailedChecks: 1,
		Passed: false, VerifiedBy: "agent",
		GateDecision: "block",
		CheckDetails: []CheckDetail{
			{CheckID: "s1", Type: "schema", Required: true, Status: "passed", Result: "ok"},
			{CheckID: "e1", Type: "evidence", Required: true, Status: "failed", Result: "missing"},
			{CheckID: "r1", Type: "review", Required: false, Status: "pending"},
		},
	}
	s := FormatVerificationSummary(summary)
	if s == "" {
		t.Fatal("expected non-empty format")
	}
}

func TestFormatVerificationEvent(t *testing.T) {
	e := VerificationEvent{
		Type: VerifEventCheckPass, TaskID: "t1", CheckID: "s1",
		Status: "passed", Result: "all fields present",
	}
	s := FormatVerificationEvent(e)
	if s == "" {
		t.Fatal("expected non-empty format")
	}
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestVerificationEvent_JSON(t *testing.T) {
	e := VerificationEvent{
		Type: VerifEventCheckPass, TaskID: "t1", RunID: "r1",
		CheckID: "s1", CheckType: "schema", Status: "passed",
		Result: "ok", Evidence: "fields: name, age",
		Duration: 10 * time.Millisecond, CreatedAt: 12345,
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var e2 VerificationEvent
	if err := json.Unmarshal(data, &e2); err != nil {
		t.Fatal(err)
	}
	if e2.Type != VerifEventCheckPass || e2.CheckID != "s1" {
		t.Fatalf("round-trip mismatch: %+v", e2)
	}
}

func TestVerificationSummary_JSON(t *testing.T) {
	s := VerificationSummary{
		TaskID: "t1", RunID: "r1", Policy: state.VerificationPolicyRequired,
		TotalChecks: 2, PassedChecks: 1, FailedChecks: 1,
		Passed: false, Duration: 100 * time.Millisecond,
		CheckDetails: []CheckDetail{
			{CheckID: "s1", Status: "passed"},
			{CheckID: "e1", Status: "failed"},
		},
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var s2 VerificationSummary
	if err := json.Unmarshal(data, &s2); err != nil {
		t.Fatal(err)
	}
	if s2.TotalChecks != 2 || s2.Passed {
		t.Fatalf("round-trip mismatch: %+v", s2)
	}
}

// ── End-to-end ──────────────────────────────────────────────────────────────

func TestEndToEnd_TelemetryPipeline(t *testing.T) {
	// Simulate: runtime evaluates → gate decides → review completes → telemetry records all.
	var sunkEvents []VerificationEvent
	sink := func(e VerificationEvent) { sunkEvents = append(sunkEvents, e) }
	tel := NewVerificationTelemetry(sink)

	// 1. Runtime evaluation.
	runtimeResult := RuntimeResult{
		Passed:  false,
		Summary: "1/2 passed",
		Duration: 50 * time.Millisecond,
		CheckResults: []CheckResult{
			{CheckID: "s1", Type: state.VerificationCheckSchema, Outcome: CheckOutcome{Passed: true, Result: "ok"}, Duration: 10 * time.Millisecond},
			{CheckID: "r1", Type: state.VerificationCheckReview, Outcome: CheckOutcome{Passed: false, Result: "pending"}, Duration: 0},
		},
	}
	EmitRuntimeEvents(tel, "task-1", "run-1", runtimeResult, 100)

	// 2. Gate decision.
	gate := GateResult{Decision: GateBlock, Reason: "review pending", FailedChecks: []string{"r1"}}
	EmitGateEvent(tel, "task-1", "run-1", gate, 101)

	// 3. Review completes.
	review := ReviewResult{ReviewerID: "rev-1", Verdict: ReviewApproved, Comments: "approved", Confidence: 0.9}
	EmitReviewEvent(tel, "task-1", "run-1", review, 200)

	// Verify all events were collected.
	allEvents := tel.Events()
	if len(allEvents) != 6 {
		// started + 2 checks + completed + gate + review = 6
		t.Fatalf("expected 6 events, got %d", len(allEvents))
	}

	// Verify sink received everything.
	if len(sunkEvents) != 6 {
		t.Fatalf("expected 6 sunk events, got %d", len(sunkEvents))
	}

	// Build summary.
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "s1", Type: state.VerificationCheckSchema, Required: true, Status: state.VerificationStatusPassed, Result: "ok"},
			{CheckID: "r1", Type: state.VerificationCheckReview, Required: true, Status: state.VerificationStatusPassed, Result: "approved: approved"},
		},
		VerifiedAt: 200,
		VerifiedBy: "rev-1",
	}
	summary := BuildVerificationSummary("task-1", "run-1", spec, nil, &gate)
	if !summary.Passed {
		t.Fatal("expected passed after review")
	}
	if summary.GateDecision != "block" {
		t.Fatalf("expected block gate decision, got %s", summary.GateDecision)
	}
	if len(summary.CheckDetails) != 2 {
		t.Fatalf("expected 2 check details, got %d", len(summary.CheckDetails))
	}

	// Format works.
	s := FormatVerificationSummary(summary)
	if s == "" {
		t.Fatal("expected non-empty format")
	}

	// Event formatting works.
	for _, e := range allEvents {
		s := FormatVerificationEvent(e)
		if s == "" {
			t.Fatalf("empty format for event type %s", e.Type)
		}
	}
}

func TestEndToEnd_CorrelatedEvents(t *testing.T) {
	tel := NewVerificationTelemetry(nil)

	// All events should correlate via taskID/runID.
	EmitRuntimeEvents(tel, "task-42", "run-7", RuntimeResult{
		Passed: true,
		CheckResults: []CheckResult{
			{CheckID: "c1", Outcome: CheckOutcome{Passed: true}},
		},
	}, 100)

	for _, e := range tel.Events() {
		if e.TaskID != "task-42" {
			t.Errorf("expected task-42, got %s", e.TaskID)
		}
		if e.RunID != "run-7" {
			t.Errorf("expected run-7, got %s", e.RunID)
		}
	}
}
