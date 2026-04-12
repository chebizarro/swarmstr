package planner

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// ── Worker SLA ──────────────────────────────────────────────────────────────

// WorkerSLA defines operational expectations for a delegated worker.
type WorkerSLA struct {
	// MaxDuration is the hard wall-clock limit for the entire task.
	MaxDuration time.Duration `json:"max_duration"`
	// HeartbeatInterval is how often the worker must send heartbeats.
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
	// HeartbeatGrace is additional grace period beyond a missed heartbeat.
	HeartbeatGrace time.Duration `json:"heartbeat_grace,omitempty"`
	// MaxTokens is the token budget for this worker.
	MaxTokens int `json:"max_tokens,omitempty"`
	// MaxToolCalls is the tool call budget.
	MaxToolCalls int `json:"max_tool_calls,omitempty"`
	// AllowPartialResult permits the worker to return partial results on
	// cancellation or timeout rather than a hard fail.
	AllowPartialResult bool `json:"allow_partial_result,omitempty"`
}

// DefaultWorkerSLA returns conservative defaults.
func DefaultWorkerSLA() WorkerSLA {
	return WorkerSLA{
		MaxDuration:       5 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		HeartbeatGrace:    10 * time.Second,
		AllowPartialResult: true,
	}
}

// Validate checks the SLA for consistency.
func (s WorkerSLA) Validate() error {
	if s.MaxDuration <= 0 {
		return fmt.Errorf("max_duration must be positive")
	}
	if s.HeartbeatInterval <= 0 {
		return fmt.Errorf("heartbeat_interval must be positive")
	}
	if s.HeartbeatInterval > s.MaxDuration {
		return fmt.Errorf("heartbeat_interval (%s) exceeds max_duration (%s)",
			s.HeartbeatInterval, s.MaxDuration)
	}
	return nil
}

// EffectiveHeartbeatTimeout returns heartbeat interval + grace.
func (s WorkerSLA) EffectiveHeartbeatTimeout() time.Duration {
	return s.HeartbeatInterval + s.HeartbeatGrace
}

// ── Cancellation ────────────────────────────────────────────────────────────

// CancelReason describes why a worker task was cancelled.
type CancelReason string

const (
	CancelReasonParent      CancelReason = "parent_cancelled"
	CancelReasonTimeout     CancelReason = "timeout"
	CancelReasonBudget      CancelReason = "budget_exceeded"
	CancelReasonSuperseded  CancelReason = "superseded"
	CancelReasonOperator    CancelReason = "operator"
)

// CancelRequest is issued by the parent to cancel a delegated worker.
type CancelRequest struct {
	TaskID  string       `json:"task_id"`
	RunID   string       `json:"run_id"`
	Reason  CancelReason `json:"reason"`
	Message string       `json:"message,omitempty"`
	// GracePeriod allows the worker to wrap up and return partial results.
	GracePeriod time.Duration `json:"grace_period,omitempty"`
	IssuedAt    int64         `json:"issued_at"`
}

// ── Partial result ──────────────────────────────────────────────────────────

// PartialResult captures incomplete but useful output from a worker.
type PartialResult struct {
	WorkerID      string          `json:"worker_id"`
	RunID         string          `json:"run_id"`
	Output        string          `json:"output,omitempty"`
	ResultRef     string          `json:"result_ref,omitempty"`
	Progress      *ProgressInfo   `json:"progress,omitempty"`
	Usage         state.TaskUsage `json:"usage,omitempty"`
	Reason        string          `json:"reason,omitempty"` // why partial
	IsUsable      bool            `json:"is_usable"`        // parent can use this
	CompletedAt   int64           `json:"completed_at"`
	Meta          map[string]any  `json:"meta,omitempty"`
}

// ── Takeover ────────────────────────────────────────────────────────────────

// TakeoverReason describes why a task is being reassigned.
type TakeoverReason string

const (
	TakeoverReasonTimeout   TakeoverReason = "timeout"
	TakeoverReasonRejected  TakeoverReason = "rejected"
	TakeoverReasonFailed    TakeoverReason = "failed"
	TakeoverReasonPartial   TakeoverReason = "partial_insufficient"
)

// TakeoverRequest represents a parent reassigning a task to a new worker.
type TakeoverRequest struct {
	TaskID         string         `json:"task_id"`
	RunID          string         `json:"run_id"`
	PreviousWorker string         `json:"previous_worker"`
	NewWorker      string         `json:"new_worker,omitempty"` // empty = auto-select
	Reason         TakeoverReason `json:"reason"`
	// PriorResult is any partial result from the previous worker that
	// the new worker can build upon.
	PriorResult  *PartialResult `json:"prior_result,omitempty"`
	Attempt      int            `json:"attempt"` // takeover attempt number
	MaxTakeovers int            `json:"max_takeovers"`
	IssuedAt     int64          `json:"issued_at"`
}

// ── SLA Monitor ─────────────────────────────────────────────────────────────

// SLAViolation describes a detected SLA breach.
type SLAViolation struct {
	Type        string `json:"type"` // "heartbeat_timeout", "duration_exceeded", "budget_exceeded"
	WorkerID    string `json:"worker_id"`
	TaskID      string `json:"task_id"`
	RunID       string `json:"run_id"`
	Message     string `json:"message"`
	DetectedAt  int64  `json:"detected_at"`
}

// SLAAction is the recommended response to an SLA violation.
type SLAAction string

const (
	SLAActionCancel   SLAAction = "cancel"
	SLAActionTakeover SLAAction = "takeover"
	SLAActionWarn     SLAAction = "warn"
	SLAActionIgnore   SLAAction = "ignore"
)

// SLAMonitor tracks SLA compliance for a set of workers.
type SLAMonitor struct {
	mu         sync.RWMutex
	sla        WorkerSLA
	trackers   map[string]*workerSLAState // workerID → state
	violations []SLAViolation
}

type workerSLAState struct {
	tracker   *WorkerTracker
	startedAt int64
	cancelled bool
	takeover  bool
}

// NewSLAMonitor creates a monitor for the given SLA.
func NewSLAMonitor(sla WorkerSLA) *SLAMonitor {
	return &SLAMonitor{
		sla:      sla,
		trackers: make(map[string]*workerSLAState),
	}
}

// Register adds a worker tracker to be monitored.
func (m *SLAMonitor) Register(tracker *WorkerTracker, startedAt int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trackers[tracker.WorkerID()] = &workerSLAState{
		tracker:   tracker,
		startedAt: startedAt,
	}
}

// Check evaluates all registered workers for SLA violations at the given time.
func (m *SLAMonitor) Check(now int64) []SLAViolation {
	m.mu.Lock()
	defer m.mu.Unlock()

	var violations []SLAViolation

	for workerID, ws := range m.trackers {
		if ws.tracker.State().IsTerminal() || ws.cancelled {
			continue
		}

		// Check heartbeat timeout.
		effectiveTimeout := m.sla.EffectiveHeartbeatTimeout()
		if effectiveTimeout > 0 {
			lastHB := ws.tracker.LastHeartbeat()
			elapsed := time.Duration(now-lastHB) * time.Second
			if elapsed > effectiveTimeout {
				v := SLAViolation{
					Type:       "heartbeat_timeout",
					WorkerID:   workerID,
					TaskID:     ws.tracker.TaskID(),
					RunID:      ws.tracker.RunID(),
					Message:    fmt.Sprintf("no heartbeat for %s (limit: %s)", elapsed, effectiveTimeout),
					DetectedAt: now,
				}
				violations = append(violations, v)
			}
		}

		// Check wall-clock duration.
		if m.sla.MaxDuration > 0 {
			elapsed := time.Duration(now-ws.startedAt) * time.Second
			if elapsed > m.sla.MaxDuration {
				v := SLAViolation{
					Type:       "duration_exceeded",
					WorkerID:   workerID,
					TaskID:     ws.tracker.TaskID(),
					RunID:      ws.tracker.RunID(),
					Message:    fmt.Sprintf("running for %s (limit: %s)", elapsed, m.sla.MaxDuration),
					DetectedAt: now,
				}
				violations = append(violations, v)
			}
		}
	}

	m.violations = append(m.violations, violations...)
	return violations
}

// Violations returns all recorded violations.
func (m *SLAMonitor) Violations() []SLAViolation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SLAViolation, len(m.violations))
	copy(out, m.violations)
	return out
}

// MarkCancelled records that a worker has been cancelled.
func (m *SLAMonitor) MarkCancelled(workerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ws, ok := m.trackers[workerID]; ok {
		ws.cancelled = true
	}
}

// MarkTakeover records that a worker's task has been taken over.
func (m *SLAMonitor) MarkTakeover(workerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ws, ok := m.trackers[workerID]; ok {
		ws.takeover = true
	}
}

// ── SLA decision engine ─────────────────────────────────────────────────────

// DecideSLAAction recommends an action for an SLA violation.
func DecideSLAAction(violation SLAViolation, sla WorkerSLA, takeoverAttempt int, maxTakeovers int) SLAAction {
	switch violation.Type {
	case "heartbeat_timeout":
		if takeoverAttempt < maxTakeovers {
			return SLAActionTakeover
		}
		return SLAActionCancel
	case "duration_exceeded":
		if sla.AllowPartialResult {
			return SLAActionCancel // cancel with grace → collect partial
		}
		return SLAActionCancel
	case "budget_exceeded":
		return SLAActionCancel
	default:
		return SLAActionWarn
	}
}

// ── Build cancellation ──────────────────────────────────────────────────────

// BuildCancelRequest constructs a cancellation request from an SLA violation.
func BuildCancelRequest(violation SLAViolation, sla WorkerSLA) CancelRequest {
	reason := CancelReasonTimeout
	switch violation.Type {
	case "heartbeat_timeout":
		reason = CancelReasonTimeout
	case "duration_exceeded":
		reason = CancelReasonTimeout
	case "budget_exceeded":
		reason = CancelReasonBudget
	}

	var grace time.Duration
	if sla.AllowPartialResult {
		grace = 10 * time.Second
	}

	return CancelRequest{
		TaskID:      violation.TaskID,
		RunID:       violation.RunID,
		Reason:      reason,
		Message:     violation.Message,
		GracePeriod: grace,
		IssuedAt:    violation.DetectedAt,
	}
}

// ── Build takeover ──────────────────────────────────────────────────────────

// BuildTakeoverRequest constructs a takeover request from a violation and optional
// partial result from the timed-out worker.
func BuildTakeoverRequest(violation SLAViolation, partial *PartialResult, attempt, maxTakeovers int) TakeoverRequest {
	reason := TakeoverReasonTimeout
	switch violation.Type {
	case "heartbeat_timeout":
		reason = TakeoverReasonTimeout
	}

	return TakeoverRequest{
		TaskID:         violation.TaskID,
		RunID:          violation.RunID,
		PreviousWorker: violation.WorkerID,
		Reason:         reason,
		PriorResult:    partial,
		Attempt:        attempt,
		MaxTakeovers:   maxTakeovers,
		IssuedAt:       violation.DetectedAt,
	}
}

// ── Formatting ──────────────────────────────────────────────────────────────

// FormatSLAViolation returns a human-readable description.
func FormatSLAViolation(v SLAViolation) string {
	return fmt.Sprintf("[SLA] type=%s worker=%s task=%s: %s",
		v.Type, v.WorkerID, v.TaskID, v.Message)
}

// FormatCancelRequest returns a human-readable cancellation summary.
func FormatCancelRequest(c CancelRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Cancel: task=%s run=%s reason=%s", c.TaskID, c.RunID, c.Reason)
	if c.GracePeriod > 0 {
		fmt.Fprintf(&b, " grace=%s", c.GracePeriod)
	}
	if c.Message != "" {
		fmt.Fprintf(&b, " msg=%q", c.Message)
	}
	return b.String()
}

// FormatTakeoverRequest returns a human-readable takeover summary.
func FormatTakeoverRequest(t TakeoverRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Takeover: task=%s prev=%s reason=%s attempt=%d/%d",
		t.TaskID, t.PreviousWorker, t.Reason, t.Attempt, t.MaxTakeovers)
	if t.NewWorker != "" {
		fmt.Fprintf(&b, " new=%s", t.NewWorker)
	}
	if t.PriorResult != nil {
		fmt.Fprintf(&b, " has_prior_result=true")
	}
	return b.String()
}
