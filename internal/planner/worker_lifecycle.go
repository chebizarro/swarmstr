package planner

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// ── Worker states ────────────────────────────────────────────────────────────

// WorkerState describes the lifecycle state of a delegated worker.
type WorkerState string

const (
	WorkerStatePending   WorkerState = "pending"   // task sent, awaiting acceptance
	WorkerStateAccepted  WorkerState = "accepted"  // worker accepted the task
	WorkerStateRejected  WorkerState = "rejected"  // worker rejected the task
	WorkerStateRunning   WorkerState = "running"   // worker is actively executing
	WorkerStateProgress  WorkerState = "progress"  // worker reported intermediate progress
	WorkerStateBlocked   WorkerState = "blocked"   // worker is blocked (waiting on dependency/approval)
	WorkerStateCompleted WorkerState = "completed" // worker finished successfully
	WorkerStateFailed    WorkerState = "failed"    // worker failed
	WorkerStateCancelled WorkerState = "cancelled" // task was cancelled by parent
	WorkerStateTimedOut  WorkerState = "timed_out" // worker exceeded timeout without heartbeat
)

var validWorkerStates = map[WorkerState]bool{
	WorkerStatePending: true, WorkerStateAccepted: true, WorkerStateRejected: true,
	WorkerStateRunning: true, WorkerStateProgress: true, WorkerStateBlocked: true,
	WorkerStateCompleted: true, WorkerStateFailed: true, WorkerStateCancelled: true,
	WorkerStateTimedOut: true,
}

// ValidWorkerState reports whether s is a recognized worker state.
func ValidWorkerState(s WorkerState) bool { return validWorkerStates[s] }

// IsTerminal reports whether the state is terminal.
func (s WorkerState) IsTerminal() bool {
	switch s {
	case WorkerStateRejected, WorkerStateCompleted, WorkerStateFailed,
		WorkerStateCancelled, WorkerStateTimedOut:
		return true
	}
	return false
}

// ── Worker event ─────────────────────────────────────────────────────────────

// WorkerEvent records a lifecycle event for a delegated worker.
type WorkerEvent struct {
	EventID     string         `json:"event_id"`
	TaskID      string         `json:"task_id"`
	RunID       string         `json:"run_id"`
	WorkerID    string         `json:"worker_id"`
	State       WorkerState    `json:"state"`
	Message     string         `json:"message,omitempty"`
	Progress    *ProgressInfo  `json:"progress,omitempty"`
	RejectInfo  *RejectInfo    `json:"reject_info,omitempty"`
	ResultRef   string         `json:"result_ref,omitempty"`
	Error       string         `json:"error,omitempty"`
	Usage       state.TaskUsage `json:"usage,omitempty"`
	CreatedAt   int64          `json:"created_at"`
	Meta        map[string]any `json:"meta,omitempty"`
}

// ProgressInfo captures progress metadata.
type ProgressInfo struct {
	PercentComplete float64 `json:"percent_complete,omitempty"` // 0.0-1.0
	StepID          string  `json:"step_id,omitempty"`
	StepTotal       int     `json:"step_total,omitempty"`
	StepCurrent     int     `json:"step_current,omitempty"`
	Message         string  `json:"message,omitempty"`
}

// RejectInfo captures why a worker rejected a task.
type RejectInfo struct {
	Reason      string `json:"reason"`
	Recoverable bool   `json:"recoverable"` // can another worker handle it?
	Suggestion  string `json:"suggestion,omitempty"`
}

// ── Worker tracker ───────────────────────────────────────────────────────────

// WorkerTracker tracks the lifecycle of a single delegated worker.
//
// Thread safety: all public methods are safe for concurrent use.
type WorkerTracker struct {
	mu               sync.RWMutex
	taskID           string
	runID            string
	workerID         string
	state            WorkerState
	events           []WorkerEvent
	lastHeartbeat    int64
	heartbeatTimeout time.Duration
	nextEventID      int
}

// NewWorkerTracker creates a tracker for a delegated worker.
func NewWorkerTracker(taskID, runID, workerID string, heartbeatTimeout time.Duration) *WorkerTracker {
	return &WorkerTracker{
		taskID:           taskID,
		runID:            runID,
		workerID:         workerID,
		state:            WorkerStatePending,
		heartbeatTimeout: heartbeatTimeout,
		lastHeartbeat:    time.Now().Unix(),
	}
}

// RecordEvent records a lifecycle event and transitions the worker state.
// Returns an error if the transition is not allowed.
func (t *WorkerTracker) RecordEvent(state WorkerState, msg string, now int64, opts ...WorkerEventOption) (WorkerEvent, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state.IsTerminal() {
		return WorkerEvent{}, fmt.Errorf("worker %s is in terminal state %s", t.workerID, t.state)
	}

	if !allowedWorkerTransition(t.state, state) {
		return WorkerEvent{}, fmt.Errorf("illegal worker transition %s → %s", t.state, state)
	}

	t.nextEventID++
	event := WorkerEvent{
		EventID:   fmt.Sprintf("we-%s-%d", t.runID, t.nextEventID),
		TaskID:    t.taskID,
		RunID:     t.runID,
		WorkerID:  t.workerID,
		State:     state,
		Message:   msg,
		CreatedAt: now,
	}

	for _, opt := range opts {
		opt(&event)
	}

	t.state = state
	t.lastHeartbeat = now
	t.events = append(t.events, event)
	return event, nil
}

// Heartbeat updates the last heartbeat timestamp without a state change.
func (t *WorkerTracker) Heartbeat(now int64) {
	t.mu.Lock()
	t.lastHeartbeat = now
	t.mu.Unlock()
}

// CheckTimeout reports whether the worker has exceeded its heartbeat timeout.
func (t *WorkerTracker) CheckTimeout(now int64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.state.IsTerminal() || t.heartbeatTimeout == 0 {
		return false
	}
	elapsed := time.Duration(now-t.lastHeartbeat) * time.Second
	return elapsed > t.heartbeatTimeout
}

// MarkTimedOut transitions the worker to timed_out if it hasn't already
// reached a terminal state.
func (t *WorkerTracker) MarkTimedOut(now int64) (WorkerEvent, error) {
	return t.RecordEvent(WorkerStateTimedOut, "heartbeat timeout exceeded", now)
}

// State returns the current worker state.
func (t *WorkerTracker) State() WorkerState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}

// Events returns a snapshot of all events.
func (t *WorkerTracker) Events() []WorkerEvent {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]WorkerEvent, len(t.events))
	copy(out, t.events)
	return out
}

// LastHeartbeat returns the last heartbeat timestamp.
func (t *WorkerTracker) LastHeartbeat() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastHeartbeat
}

// TaskID returns the tracker's task ID.
func (t *WorkerTracker) TaskID() string { return t.taskID }

// RunID returns the tracker's run ID.
func (t *WorkerTracker) RunID() string { return t.runID }

// WorkerID returns the worker identifier.
func (t *WorkerTracker) WorkerID() string { return t.workerID }

// ── Event options ────────────────────────────────────────────────────────────

// WorkerEventOption configures optional fields on a WorkerEvent.
type WorkerEventOption func(*WorkerEvent)

// WithProgress attaches progress info to the event.
func WithProgress(info ProgressInfo) WorkerEventOption {
	return func(e *WorkerEvent) { e.Progress = &info }
}

// WithRejectInfo attaches rejection details.
func WithRejectInfo(info RejectInfo) WorkerEventOption {
	return func(e *WorkerEvent) { e.RejectInfo = &info }
}

// WithResultRef attaches a result reference.
func WithResultRef(ref string) WorkerEventOption {
	return func(e *WorkerEvent) { e.ResultRef = ref }
}

// WithError attaches an error message.
func WithError(err string) WorkerEventOption {
	return func(e *WorkerEvent) { e.Error = err }
}

// WithUsage attaches usage stats.
func WithUsage(usage state.TaskUsage) WorkerEventOption {
	return func(e *WorkerEvent) { e.Usage = usage }
}

// WithMeta attaches metadata.
func WithMeta(meta map[string]any) WorkerEventOption {
	return func(e *WorkerEvent) { e.Meta = meta }
}

// ── State machine ────────────────────────────────────────────────────────────

// allowedWorkerTransition defines the valid state transitions for a worker.
func allowedWorkerTransition(from, to WorkerState) bool {
	switch from {
	case WorkerStatePending:
		switch to {
		case WorkerStateAccepted, WorkerStateRejected, WorkerStateCancelled, WorkerStateTimedOut:
			return true
		}
	case WorkerStateAccepted:
		switch to {
		case WorkerStateRunning, WorkerStateBlocked, WorkerStateFailed, WorkerStateCancelled, WorkerStateTimedOut:
			return true
		}
	case WorkerStateRunning:
		switch to {
		case WorkerStateProgress, WorkerStateBlocked, WorkerStateCompleted,
			WorkerStateFailed, WorkerStateCancelled, WorkerStateTimedOut:
			return true
		}
	case WorkerStateProgress:
		switch to {
		case WorkerStateProgress, WorkerStateRunning, WorkerStateBlocked,
			WorkerStateCompleted, WorkerStateFailed, WorkerStateCancelled, WorkerStateTimedOut:
			return true
		}
	case WorkerStateBlocked:
		switch to {
		case WorkerStateRunning, WorkerStateAccepted, WorkerStateFailed,
			WorkerStateCancelled, WorkerStateTimedOut:
			return true
		}
	}
	return false
}

// ── Formatting ───────────────────────────────────────────────────────────────

// FormatWorkerEvent returns a human-readable description.
func FormatWorkerEvent(e WorkerEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] worker=%s state=%s", e.EventID, e.WorkerID, e.State)
	if e.Message != "" {
		fmt.Fprintf(&b, " msg=%q", e.Message)
	}
	if e.Progress != nil {
		fmt.Fprintf(&b, " progress=%.0f%%", e.Progress.PercentComplete*100)
	}
	if e.RejectInfo != nil {
		fmt.Fprintf(&b, " reject=%q recoverable=%v", e.RejectInfo.Reason, e.RejectInfo.Recoverable)
	}
	if e.ResultRef != "" {
		fmt.Fprintf(&b, " result=%s", e.ResultRef)
	}
	if e.Error != "" {
		fmt.Fprintf(&b, " error=%q", e.Error)
	}
	return b.String()
}

// FormatWorkerTracker returns a summary of the worker tracker.
func FormatWorkerTracker(t *WorkerTracker) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var b strings.Builder
	fmt.Fprintf(&b, "Worker %s: state=%s events=%d\n", t.workerID, t.state, len(t.events))
	for _, e := range t.events {
		fmt.Fprintf(&b, "  %s\n", FormatWorkerEvent(e))
	}
	return b.String()
}
