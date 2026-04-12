// verification_telemetry.go persists verification results and emits
// lifecycle events for operator visibility. Verification data is
// stored alongside task/run state and surfaced in event streams.
package planner

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// ── Verification event types ────────────────────────────────────────────────

// VerificationEventType classifies verification lifecycle events.
type VerificationEventType string

const (
	VerifEventStarted   VerificationEventType = "verification.started"
	VerifEventCheckRun  VerificationEventType = "verification.check.run"
	VerifEventCheckPass VerificationEventType = "verification.check.pass"
	VerifEventCheckFail VerificationEventType = "verification.check.fail"
	VerifEventCheckErr  VerificationEventType = "verification.check.error"
	VerifEventCompleted VerificationEventType = "verification.completed"
	VerifEventGateBlock VerificationEventType = "verification.gate.block"
	VerifEventGateAllow VerificationEventType = "verification.gate.allow"
	VerifEventReview    VerificationEventType = "verification.review"
)

// ── Verification event ──────────────────────────────────────────────────────

// VerificationEvent is a structured lifecycle event for verification phases.
type VerificationEvent struct {
	Type       VerificationEventType `json:"type"`
	TaskID     string                `json:"task_id"`
	RunID      string                `json:"run_id,omitempty"`
	GoalID     string                `json:"goal_id,omitempty"`
	StepID     string                `json:"step_id,omitempty"`
	CheckID    string                `json:"check_id,omitempty"`
	CheckType  string                `json:"check_type,omitempty"`
	Status     string                `json:"status,omitempty"`
	Result     string                `json:"result,omitempty"`
	Evidence   string                `json:"evidence,omitempty"`
	ReviewerID string                `json:"reviewer_id,omitempty"`
	Confidence float64               `json:"confidence,omitempty"`
	Duration   time.Duration         `json:"duration,omitempty"`
	GateAction string                `json:"gate_action,omitempty"`
	CreatedAt  int64                 `json:"created_at"`
	Meta       map[string]any        `json:"meta,omitempty"`
}

// ── Verification summary ────────────────────────────────────────────────────

// VerificationSummary is the persisted verification state for a task/run.
type VerificationSummary struct {
	TaskID       string                `json:"task_id"`
	RunID        string                `json:"run_id"`
	Policy       state.VerificationPolicy `json:"policy"`
	TotalChecks  int                   `json:"total_checks"`
	PassedChecks int                   `json:"passed_checks"`
	FailedChecks int                   `json:"failed_checks"`
	PendingChecks int                  `json:"pending_checks"`
	ErrorChecks  int                   `json:"error_checks"`
	Passed       bool                  `json:"passed"`
	VerifiedAt   int64                 `json:"verified_at,omitempty"`
	VerifiedBy   string                `json:"verified_by,omitempty"`
	Duration     time.Duration         `json:"duration,omitempty"`
	CheckDetails []CheckDetail         `json:"check_details"`
	GateDecision string                `json:"gate_decision,omitempty"`
}

// CheckDetail is a per-check summary for the verification record.
type CheckDetail struct {
	CheckID    string `json:"check_id"`
	Type       string `json:"type"`
	Required   bool   `json:"required"`
	Status     string `json:"status"`
	Result     string `json:"result,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	EvaluatedBy string `json:"evaluated_by,omitempty"`
	EvaluatedAt int64  `json:"evaluated_at,omitempty"`
}

// ── Telemetry emitter ───────────────────────────────────────────────────────

// VerificationEventSink receives verification events for persistence or streaming.
type VerificationEventSink func(event VerificationEvent)

// VerificationTelemetry collects and emits verification lifecycle events.
// All public methods are safe for concurrent use.
type VerificationTelemetry struct {
	mu     sync.Mutex
	sink   VerificationEventSink
	events []VerificationEvent
}

// NewVerificationTelemetry creates a telemetry collector. If sink is nil,
// events are only collected in-memory.
func NewVerificationTelemetry(sink VerificationEventSink) *VerificationTelemetry {
	return &VerificationTelemetry{sink: sink}
}

// Emit records and optionally forwards a verification event.
func (t *VerificationTelemetry) Emit(event VerificationEvent) {
	t.mu.Lock()
	t.events = append(t.events, event)
	t.mu.Unlock()
	if t.sink != nil {
		t.sink(event)
	}
}

// Events returns a snapshot of all collected events.
func (t *VerificationTelemetry) Events() []VerificationEvent {
	t.mu.Lock()
	out := make([]VerificationEvent, len(t.events))
	copy(out, t.events)
	t.mu.Unlock()
	return out
}

// ── Build events from runtime results ───────────────────────────────────────

// EmitRuntimeEvents converts a RuntimeResult into a stream of verification events.
// If telemetry is nil, this is a no-op.
func EmitRuntimeEvents(telemetry *VerificationTelemetry, taskID, runID string, result RuntimeResult, now int64) {
	if telemetry == nil {
		return
	}
	// Started event.
	telemetry.Emit(VerificationEvent{
		Type:      VerifEventStarted,
		TaskID:    taskID,
		RunID:     runID,
		CreatedAt: now,
		Meta:      map[string]any{"total_checks": len(result.CheckResults)},
	})

	// Per-check events.
	for _, cr := range result.CheckResults {
		eventType := VerifEventCheckRun
		if cr.Outcome.Passed {
			eventType = VerifEventCheckPass
		} else if cr.Error != "" {
			eventType = VerifEventCheckErr
		} else if !cr.Outcome.Passed {
			eventType = VerifEventCheckFail
		}

		telemetry.Emit(VerificationEvent{
			Type:      eventType,
			TaskID:    taskID,
			RunID:     runID,
			CheckID:   cr.CheckID,
			CheckType: string(cr.Type),
			Status:    statusFromOutcome(cr),
			Result:    cr.Outcome.Result,
			Evidence:  cr.Outcome.Evidence,
			Duration:  cr.Duration,
			CreatedAt: now,
		})
	}

	// Completed event.
	status := "passed"
	if !result.Passed {
		status = "failed"
	}
	telemetry.Emit(VerificationEvent{
		Type:      VerifEventCompleted,
		TaskID:    taskID,
		RunID:     runID,
		Status:    status,
		Result:    result.Summary,
		Duration:  result.Duration,
		CreatedAt: now,
	})
}

// EmitGateEvent records a verification gate decision.
// If telemetry is nil, this is a no-op.
func EmitGateEvent(telemetry *VerificationTelemetry, taskID, runID string, gate GateResult, now int64) {
	if telemetry == nil {
		return
	}
	eventType := VerifEventGateAllow
	if !gate.Allowed() {
		eventType = VerifEventGateBlock
	}
	telemetry.Emit(VerificationEvent{
		Type:       eventType,
		TaskID:     taskID,
		RunID:      runID,
		GateAction: string(gate.Decision),
		Result:     gate.Reason,
		CreatedAt:  now,
		Meta: map[string]any{
			"failed_checks": gate.FailedChecks,
			"suggestion":    gate.Suggestion,
		},
	})
}

// EmitReviewEvent records a reviewer decision.
// If telemetry is nil, this is a no-op.
func EmitReviewEvent(telemetry *VerificationTelemetry, taskID, runID string, review ReviewResult, now int64) {
	if telemetry == nil {
		return
	}
	telemetry.Emit(VerificationEvent{
		Type:       VerifEventReview,
		TaskID:     taskID,
		RunID:      runID,
		ReviewerID: review.ReviewerID,
		Status:     string(review.Verdict),
		Result:     review.Comments,
		Confidence: review.Confidence,
		Duration:   review.Duration,
		CreatedAt:  now,
	})
}

// ── Build summary from spec ─────────────────────────────────────────────────

// BuildVerificationSummary creates a summary from a verification spec and
// optional runtime result.
func BuildVerificationSummary(taskID, runID string, spec state.VerificationSpec, result *RuntimeResult, gate *GateResult) VerificationSummary {
	spec = spec.Normalize()
	summary := VerificationSummary{
		TaskID: taskID,
		RunID:  runID,
		Policy: spec.Policy,
	}

	for _, check := range spec.Checks {
		summary.TotalChecks++
		detail := CheckDetail{
			CheckID:     check.CheckID,
			Type:        string(check.Type),
			Required:    check.Required,
			Status:      string(check.Status),
			Result:      check.Result,
			Evidence:    check.Evidence,
			EvaluatedBy: check.EvaluatedBy,
			EvaluatedAt: check.EvaluatedAt,
		}
		summary.CheckDetails = append(summary.CheckDetails, detail)

		switch check.Status {
		case state.VerificationStatusPassed, state.VerificationStatusSkipped:
			summary.PassedChecks++
		case state.VerificationStatusFailed:
			summary.FailedChecks++
		case state.VerificationStatusError:
			summary.ErrorChecks++
		default:
			summary.PendingChecks++
		}
	}

	if result != nil {
		summary.Passed = result.Passed
		summary.Duration = result.Duration
		// Prefer runtime-updated values, fall back to spec values.
		summary.VerifiedAt = result.UpdatedSpec.VerifiedAt
		if summary.VerifiedAt == 0 {
			summary.VerifiedAt = spec.VerifiedAt
		}
		summary.VerifiedBy = result.UpdatedSpec.VerifiedBy
		if summary.VerifiedBy == "" {
			summary.VerifiedBy = spec.VerifiedBy
		}
	} else {
		summary.Passed = summary.FailedChecks == 0 && summary.ErrorChecks == 0 && summary.PendingChecks == 0
		summary.VerifiedAt = spec.VerifiedAt
		summary.VerifiedBy = spec.VerifiedBy
	}

	if gate != nil {
		summary.GateDecision = string(gate.Decision)
	}

	return summary
}

// ── Formatting ──────────────────────────────────────────────────────────────

// FormatVerificationSummary returns a human-readable verification summary.
func FormatVerificationSummary(s VerificationSummary) string {
	var b strings.Builder
	status := "PASSED"
	if !s.Passed {
		status = "FAILED"
	}
	fmt.Fprintf(&b, "Verification Summary: %s (policy=%s)\n", status, s.Policy)
	fmt.Fprintf(&b, "  Task: %s  Run: %s\n", s.TaskID, s.RunID)
	fmt.Fprintf(&b, "  Checks: %d total, %d passed, %d failed, %d pending, %d errors\n",
		s.TotalChecks, s.PassedChecks, s.FailedChecks, s.PendingChecks, s.ErrorChecks)
	if s.Duration > 0 {
		fmt.Fprintf(&b, "  Duration: %s\n", s.Duration.Round(time.Millisecond))
	}
	if s.VerifiedBy != "" {
		fmt.Fprintf(&b, "  Verified by: %s\n", s.VerifiedBy)
	}
	if s.GateDecision != "" {
		fmt.Fprintf(&b, "  Gate: %s\n", s.GateDecision)
	}
	for _, d := range s.CheckDetails {
		marker := "○"
		switch d.Status {
		case string(state.VerificationStatusPassed), string(state.VerificationStatusSkipped):
			marker = "✓"
		case string(state.VerificationStatusFailed):
			marker = "✗"
		case string(state.VerificationStatusError):
			marker = "⚠"
		}
		req := ""
		if d.Required {
			req = " [required]"
		}
		fmt.Fprintf(&b, "  %s [%s] %s%s: %s\n", marker, d.Type, d.CheckID, req, d.Result)
	}
	return b.String()
}

// FormatVerificationEvent returns a single-line event description.
func FormatVerificationEvent(e VerificationEvent) string {
	var parts []string
	parts = append(parts, string(e.Type))
	parts = append(parts, fmt.Sprintf("task=%s", e.TaskID))
	if e.CheckID != "" {
		parts = append(parts, fmt.Sprintf("check=%s", e.CheckID))
	}
	if e.Status != "" {
		parts = append(parts, fmt.Sprintf("status=%s", e.Status))
	}
	if e.GateAction != "" {
		parts = append(parts, fmt.Sprintf("gate=%s", e.GateAction))
	}
	if e.ReviewerID != "" {
		parts = append(parts, fmt.Sprintf("reviewer=%s", e.ReviewerID))
	}
	if e.Result != "" {
		r := e.Result
		if len(r) > 80 {
			r = r[:77] + "..."
		}
		parts = append(parts, fmt.Sprintf("result=%q", r))
	}
	return strings.Join(parts, " ")
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func statusFromOutcome(cr CheckResult) string {
	if cr.Error != "" {
		return "error"
	}
	if cr.Outcome.Passed {
		return "passed"
	}
	return "failed"
}
