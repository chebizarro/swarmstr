package planner

import (
	"encoding/json"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// ── SLA validation ──────────────────────────────────────────────────────────

func TestWorkerSLA_Validate_OK(t *testing.T) {
	sla := DefaultWorkerSLA()
	if err := sla.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkerSLA_Validate_NegativeDuration(t *testing.T) {
	sla := WorkerSLA{MaxDuration: -1, HeartbeatInterval: 10 * time.Second}
	if err := sla.Validate(); err == nil {
		t.Fatal("expected error for negative duration")
	}
}

func TestWorkerSLA_Validate_HeartbeatExceedsDuration(t *testing.T) {
	sla := WorkerSLA{MaxDuration: 10 * time.Second, HeartbeatInterval: 30 * time.Second}
	if err := sla.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestWorkerSLA_EffectiveHeartbeatTimeout(t *testing.T) {
	sla := WorkerSLA{
		HeartbeatInterval: 30 * time.Second,
		HeartbeatGrace:    10 * time.Second,
	}
	if got := sla.EffectiveHeartbeatTimeout(); got != 40*time.Second {
		t.Fatalf("expected 40s, got %s", got)
	}
}

// ── SLA Monitor ─────────────────────────────────────────────────────────────

func TestSLAMonitor_NoViolation(t *testing.T) {
	sla := WorkerSLA{
		MaxDuration:       5 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		HeartbeatGrace:    10 * time.Second,
	}
	mon := NewSLAMonitor(sla)
	tracker := NewWorkerTracker("task-1", "run-1", "w1", 40*time.Second)
	now := time.Now().Unix()
	tracker.Heartbeat(now)

	mon.Register(tracker, now)

	// Check just 5 seconds later — no violation.
	violations := mon.Check(now + 5)
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %d", len(violations))
	}
}

func TestSLAMonitor_HeartbeatViolation(t *testing.T) {
	sla := WorkerSLA{
		MaxDuration:       5 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		HeartbeatGrace:    10 * time.Second,
	}
	mon := NewSLAMonitor(sla)
	tracker := NewWorkerTracker("task-1", "run-1", "w1", 40*time.Second)
	now := time.Now().Unix()
	tracker.Heartbeat(now)

	mon.Register(tracker, now)
	tracker.RecordEvent(WorkerStateAccepted, "ok", now)
	tracker.RecordEvent(WorkerStateRunning, "go", now+1)

	// 50 seconds with no heartbeat: 50s > 40s effective timeout.
	violations := mon.Check(now + 50)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	if violations[0].Type != "heartbeat_timeout" {
		t.Fatalf("expected heartbeat_timeout, got %s", violations[0].Type)
	}
}

func TestSLAMonitor_DurationViolation(t *testing.T) {
	sla := WorkerSLA{
		MaxDuration:       60 * time.Second,
		HeartbeatInterval: 30 * time.Second,
		HeartbeatGrace:    10 * time.Second,
	}
	mon := NewSLAMonitor(sla)
	tracker := NewWorkerTracker("task-1", "run-1", "w1", 40*time.Second)
	now := time.Now().Unix()
	tracker.Heartbeat(now)
	mon.Register(tracker, now)
	tracker.RecordEvent(WorkerStateAccepted, "ok", now)
	tracker.RecordEvent(WorkerStateRunning, "go", now+1)

	// Keep heartbeating but exceed max duration.
	tracker.Heartbeat(now + 30)
	tracker.Heartbeat(now + 55)

	violations := mon.Check(now + 65)
	found := false
	for _, v := range violations {
		if v.Type == "duration_exceeded" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected duration_exceeded violation")
	}
}

func TestSLAMonitor_SkipsTerminalWorkers(t *testing.T) {
	sla := DefaultWorkerSLA()
	mon := NewSLAMonitor(sla)
	tracker := NewWorkerTracker("task-1", "run-1", "w1", 40*time.Second)
	now := time.Now().Unix()
	mon.Register(tracker, now)
	tracker.RecordEvent(WorkerStateAccepted, "ok", now)
	tracker.RecordEvent(WorkerStateRunning, "go", now+1)
	tracker.RecordEvent(WorkerStateCompleted, "done", now+2)

	// Way past timeout but worker is completed.
	violations := mon.Check(now + 1000)
	if len(violations) != 0 {
		t.Fatalf("expected no violations for terminal worker, got %d", len(violations))
	}
}

func TestSLAMonitor_SkipsCancelledWorkers(t *testing.T) {
	sla := WorkerSLA{
		MaxDuration:       60 * time.Second,
		HeartbeatInterval: 10 * time.Second,
	}
	mon := NewSLAMonitor(sla)
	tracker := NewWorkerTracker("task-1", "run-1", "w1", 10*time.Second)
	now := time.Now().Unix()
	mon.Register(tracker, now)
	tracker.RecordEvent(WorkerStateAccepted, "ok", now)
	tracker.RecordEvent(WorkerStateRunning, "go", now+1)
	mon.MarkCancelled("w1")

	violations := mon.Check(now + 100)
	if len(violations) != 0 {
		t.Fatalf("expected no violations for cancelled worker, got %d", len(violations))
	}
}

func TestSLAMonitor_ViolationsAccumulate(t *testing.T) {
	sla := WorkerSLA{
		MaxDuration:       60 * time.Second,
		HeartbeatInterval: 10 * time.Second,
	}
	mon := NewSLAMonitor(sla)
	tracker := NewWorkerTracker("task-1", "run-1", "w1", 10*time.Second)
	now := time.Now().Unix()
	mon.Register(tracker, now)
	tracker.RecordEvent(WorkerStateAccepted, "ok", now)
	tracker.RecordEvent(WorkerStateRunning, "go", now+1)

	mon.Check(now + 20)  // heartbeat violation
	mon.Check(now + 100) // heartbeat + duration violations

	all := mon.Violations()
	if len(all) < 2 {
		t.Fatalf("expected accumulated violations, got %d", len(all))
	}
}

// ── Cancellation ────────────────────────────────────────────────────────────

func TestBuildCancelRequest_Timeout(t *testing.T) {
	v := SLAViolation{Type: "heartbeat_timeout", TaskID: "t1", RunID: "r1", WorkerID: "w1", DetectedAt: 100}
	sla := WorkerSLA{AllowPartialResult: true}
	req := BuildCancelRequest(v, sla)
	if req.Reason != CancelReasonTimeout {
		t.Fatalf("expected timeout reason, got %s", req.Reason)
	}
	if req.GracePeriod != 10*time.Second {
		t.Fatalf("expected 10s grace, got %s", req.GracePeriod)
	}
}

func TestBuildCancelRequest_Budget(t *testing.T) {
	v := SLAViolation{Type: "budget_exceeded", TaskID: "t1", RunID: "r1", DetectedAt: 100}
	sla := WorkerSLA{AllowPartialResult: false}
	req := BuildCancelRequest(v, sla)
	if req.Reason != CancelReasonBudget {
		t.Fatalf("expected budget reason, got %s", req.Reason)
	}
	if req.GracePeriod != 0 {
		t.Fatalf("expected no grace without AllowPartialResult, got %s", req.GracePeriod)
	}
}

// ── Takeover ────────────────────────────────────────────────────────────────

func TestBuildTakeoverRequest(t *testing.T) {
	v := SLAViolation{Type: "heartbeat_timeout", TaskID: "t1", RunID: "r1", WorkerID: "w1", DetectedAt: 100}
	partial := &PartialResult{
		WorkerID:  "w1",
		Output:    "partial work",
		IsUsable:  true,
		Usage:     state.TaskUsage{TotalTokens: 500},
	}
	req := BuildTakeoverRequest(v, partial, 1, 3)
	if req.PreviousWorker != "w1" {
		t.Fatalf("expected w1, got %s", req.PreviousWorker)
	}
	if req.Attempt != 1 || req.MaxTakeovers != 3 {
		t.Fatal("unexpected attempt/max")
	}
	if req.PriorResult == nil || !req.PriorResult.IsUsable {
		t.Fatal("expected usable prior result")
	}
}

func TestBuildTakeoverRequest_NoPriorResult(t *testing.T) {
	v := SLAViolation{Type: "heartbeat_timeout", TaskID: "t1", RunID: "r1", WorkerID: "w1", DetectedAt: 100}
	req := BuildTakeoverRequest(v, nil, 0, 2)
	if req.PriorResult != nil {
		t.Fatal("expected nil prior result")
	}
}

// ── SLA decision engine ─────────────────────────────────────────────────────

func TestDecideSLAAction_HeartbeatTimeout_Takeover(t *testing.T) {
	v := SLAViolation{Type: "heartbeat_timeout"}
	action := DecideSLAAction(v, DefaultWorkerSLA(), 0, 2)
	if action != SLAActionTakeover {
		t.Fatalf("expected takeover, got %s", action)
	}
}

func TestDecideSLAAction_HeartbeatTimeout_MaxTakeovers(t *testing.T) {
	v := SLAViolation{Type: "heartbeat_timeout"}
	action := DecideSLAAction(v, DefaultWorkerSLA(), 2, 2)
	if action != SLAActionCancel {
		t.Fatalf("expected cancel after max takeovers, got %s", action)
	}
}

func TestDecideSLAAction_DurationExceeded(t *testing.T) {
	v := SLAViolation{Type: "duration_exceeded"}
	action := DecideSLAAction(v, DefaultWorkerSLA(), 0, 3)
	if action != SLAActionCancel {
		t.Fatalf("expected cancel, got %s", action)
	}
}

func TestDecideSLAAction_BudgetExceeded(t *testing.T) {
	v := SLAViolation{Type: "budget_exceeded"}
	action := DecideSLAAction(v, DefaultWorkerSLA(), 0, 3)
	if action != SLAActionCancel {
		t.Fatalf("expected cancel, got %s", action)
	}
}

func TestDecideSLAAction_Unknown(t *testing.T) {
	v := SLAViolation{Type: "custom_unknown"}
	action := DecideSLAAction(v, DefaultWorkerSLA(), 0, 3)
	if action != SLAActionWarn {
		t.Fatalf("expected warn, got %s", action)
	}
}

// ── Formatting ──────────────────────────────────────────────────────────────

func TestFormatSLAViolation(t *testing.T) {
	v := SLAViolation{Type: "heartbeat_timeout", WorkerID: "w1", TaskID: "t1", Message: "no heartbeat"}
	s := FormatSLAViolation(v)
	if s == "" {
		t.Fatal("expected non-empty format")
	}
}

func TestFormatCancelRequest(t *testing.T) {
	c := CancelRequest{TaskID: "t1", RunID: "r1", Reason: CancelReasonTimeout, GracePeriod: 10 * time.Second}
	s := FormatCancelRequest(c)
	if s == "" {
		t.Fatal("expected non-empty format")
	}
}

func TestFormatTakeoverRequest(t *testing.T) {
	tr := TakeoverRequest{
		TaskID: "t1", PreviousWorker: "w1", NewWorker: "w2",
		Reason: TakeoverReasonTimeout, Attempt: 1, MaxTakeovers: 3,
		PriorResult: &PartialResult{IsUsable: true},
	}
	s := FormatTakeoverRequest(tr)
	if s == "" {
		t.Fatal("expected non-empty format")
	}
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestWorkerSLA_JSON(t *testing.T) {
	sla := DefaultWorkerSLA()
	data, err := json.Marshal(sla)
	if err != nil {
		t.Fatal(err)
	}
	var sla2 WorkerSLA
	if err := json.Unmarshal(data, &sla2); err != nil {
		t.Fatal(err)
	}
	if sla2.MaxDuration != sla.MaxDuration || sla2.HeartbeatInterval != sla.HeartbeatInterval {
		t.Fatalf("round-trip mismatch")
	}
}

func TestCancelRequest_JSON(t *testing.T) {
	c := CancelRequest{
		TaskID: "t1", RunID: "r1", Reason: CancelReasonTimeout,
		Message: "timed out", GracePeriod: 10 * time.Second, IssuedAt: 12345,
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var c2 CancelRequest
	if err := json.Unmarshal(data, &c2); err != nil {
		t.Fatal(err)
	}
	if c2.Reason != CancelReasonTimeout || c2.TaskID != "t1" {
		t.Fatalf("round-trip mismatch")
	}
}

func TestPartialResult_JSON(t *testing.T) {
	pr := PartialResult{
		WorkerID: "w1", RunID: "r1", Output: "partial",
		IsUsable: true, Usage: state.TaskUsage{TotalTokens: 500},
		CompletedAt: 12345,
	}
	data, err := json.Marshal(pr)
	if err != nil {
		t.Fatal(err)
	}
	var pr2 PartialResult
	if err := json.Unmarshal(data, &pr2); err != nil {
		t.Fatal(err)
	}
	if pr2.WorkerID != "w1" || !pr2.IsUsable || pr2.Usage.TotalTokens != 500 {
		t.Fatalf("round-trip mismatch")
	}
}

func TestTakeoverRequest_JSON(t *testing.T) {
	tr := TakeoverRequest{
		TaskID: "t1", RunID: "r1", PreviousWorker: "w1", NewWorker: "w2",
		Reason: TakeoverReasonTimeout, PriorResult: &PartialResult{IsUsable: true},
		Attempt: 1, MaxTakeovers: 3, IssuedAt: 12345,
	}
	data, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var tr2 TakeoverRequest
	if err := json.Unmarshal(data, &tr2); err != nil {
		t.Fatal(err)
	}
	if tr2.PreviousWorker != "w1" || tr2.PriorResult == nil {
		t.Fatalf("round-trip mismatch")
	}
}

func TestSLAViolation_JSON(t *testing.T) {
	v := SLAViolation{
		Type: "heartbeat_timeout", WorkerID: "w1", TaskID: "t1",
		RunID: "r1", Message: "stale", DetectedAt: 12345,
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var v2 SLAViolation
	if err := json.Unmarshal(data, &v2); err != nil {
		t.Fatal(err)
	}
	if v2.Type != "heartbeat_timeout" || v2.WorkerID != "w1" {
		t.Fatalf("round-trip mismatch")
	}
}

// ── End-to-end ──────────────────────────────────────────────────────────────

func TestEndToEnd_SLAViolation_Cancel_Pipeline(t *testing.T) {
	sla := WorkerSLA{
		MaxDuration:        60 * time.Second,
		HeartbeatInterval:  10 * time.Second,
		HeartbeatGrace:     5 * time.Second,
		AllowPartialResult: true,
	}
	if err := sla.Validate(); err != nil {
		t.Fatal(err)
	}

	mon := NewSLAMonitor(sla)
	tracker := NewWorkerTracker("task-1", "run-1", "w1", sla.EffectiveHeartbeatTimeout())
	now := time.Now().Unix()
	mon.Register(tracker, now)
	tracker.RecordEvent(WorkerStateAccepted, "ok", now)
	tracker.RecordEvent(WorkerStateRunning, "go", now+1)
	tracker.Heartbeat(now + 5)

	// No violations yet.
	v := mon.Check(now + 10)
	if len(v) != 0 {
		t.Fatalf("expected no violations, got %d", len(v))
	}

	// Worker goes silent for 20s.
	v = mon.Check(now + 25)
	if len(v) != 1 || v[0].Type != "heartbeat_timeout" {
		t.Fatalf("expected heartbeat_timeout, got %v", v)
	}

	// Decide action.
	action := DecideSLAAction(v[0], sla, 0, 2)
	if action != SLAActionTakeover {
		t.Fatalf("expected takeover, got %s", action)
	}

	// Build cancel request.
	cancel := BuildCancelRequest(v[0], sla)
	if cancel.Reason != CancelReasonTimeout {
		t.Fatal("expected timeout reason")
	}
	if cancel.GracePeriod != 10*time.Second {
		t.Fatalf("expected grace period, got %s", cancel.GracePeriod)
	}

	// Build takeover.
	partial := &PartialResult{WorkerID: "w1", Output: "half done", IsUsable: true}
	takeover := BuildTakeoverRequest(v[0], partial, 0, 2)
	if takeover.PreviousWorker != "w1" {
		t.Fatal("expected previous worker w1")
	}
	if takeover.PriorResult == nil || !takeover.PriorResult.IsUsable {
		t.Fatal("expected usable prior result")
	}

	// Mark cancelled so future checks skip it.
	mon.MarkCancelled("w1")
	v = mon.Check(now + 100)
	if len(v) != 0 {
		t.Fatalf("expected no violations after cancel, got %d", len(v))
	}
}

func TestEndToEnd_DurationExceeded_NoTakeover(t *testing.T) {
	sla := WorkerSLA{
		MaxDuration:        30 * time.Second,
		HeartbeatInterval:  10 * time.Second,
		HeartbeatGrace:     5 * time.Second,
		AllowPartialResult: false,
	}

	mon := NewSLAMonitor(sla)
	tracker := NewWorkerTracker("task-1", "run-1", "w1", sla.EffectiveHeartbeatTimeout())
	now := time.Now().Unix()
	mon.Register(tracker, now)
	tracker.RecordEvent(WorkerStateAccepted, "ok", now)
	tracker.RecordEvent(WorkerStateRunning, "go", now+1)

	// Keep heartbeating but exceed duration.
	tracker.Heartbeat(now + 10)
	tracker.Heartbeat(now + 20)
	tracker.Heartbeat(now + 30)

	v := mon.Check(now + 35)
	var durationViolation *SLAViolation
	for i := range v {
		if v[i].Type == "duration_exceeded" {
			durationViolation = &v[i]
		}
	}
	if durationViolation == nil {
		t.Fatal("expected duration_exceeded violation")
	}

	action := DecideSLAAction(*durationViolation, sla, 0, 2)
	if action != SLAActionCancel {
		t.Fatalf("expected cancel, got %s", action)
	}

	cancel := BuildCancelRequest(*durationViolation, sla)
	if cancel.GracePeriod != 0 {
		t.Fatalf("expected no grace without AllowPartialResult, got %s", cancel.GracePeriod)
	}
}

func TestEndToEnd_MultipleTakeovers(t *testing.T) {
	sla := WorkerSLA{
		MaxDuration:        5 * time.Minute,
		HeartbeatInterval:  10 * time.Second,
		AllowPartialResult: true,
	}
	maxTakeovers := 2

	// First worker times out → takeover.
	v := SLAViolation{Type: "heartbeat_timeout", WorkerID: "w1"}
	action := DecideSLAAction(v, sla, 0, maxTakeovers)
	if action != SLAActionTakeover {
		t.Fatalf("expected takeover, got %s", action)
	}

	// Second worker times out → takeover.
	v.WorkerID = "w2"
	action = DecideSLAAction(v, sla, 1, maxTakeovers)
	if action != SLAActionTakeover {
		t.Fatalf("expected takeover, got %s", action)
	}

	// Third attempt: max reached → cancel.
	v.WorkerID = "w3"
	action = DecideSLAAction(v, sla, 2, maxTakeovers)
	if action != SLAActionCancel {
		t.Fatalf("expected cancel after max takeovers, got %s", action)
	}
}
