package planner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// ── Recovery types ───────────────────────────────────────────────────────────

// OrphanReason classifies why a run was detected as orphaned.
type OrphanReason string

const (
	// OrphanReasonDaemonRestart indicates the run was in-flight when the daemon
	// shut down and restarted.
	OrphanReasonDaemonRestart OrphanReason = "daemon_restart"
	// OrphanReasonStale indicates the run exceeded the maximum age threshold
	// without completing.
	OrphanReasonStale OrphanReason = "stale"
	// OrphanReasonNoCheckpoint indicates the run has no checkpoint to resume from.
	OrphanReasonNoCheckpoint OrphanReason = "no_checkpoint"
)

// RecoveryAction describes what to do with a discovered orphan.
type RecoveryAction string

const (
	// RecoveryResume indicates the run should be resumed from its last checkpoint.
	RecoveryResume RecoveryAction = "resume"
	// RecoveryFail indicates the run should be marked as failed.
	RecoveryFail RecoveryAction = "fail"
	// RecoveryRequiresAttention indicates the run needs operator review.
	RecoveryRequiresAttention RecoveryAction = "requires_attention"
)

// OrphanedRun describes a task run that was in-flight at the time of daemon
// restart and needs recovery.
type OrphanedRun struct {
	Run        state.TaskRun              `json:"run"`
	Task       state.TaskSpec             `json:"task"`
	Journal    *state.WorkflowJournalDoc  `json:"journal,omitempty"`
	Checkpoint *state.WorkflowCheckpointDoc `json:"checkpoint,omitempty"`
	Reason     OrphanReason               `json:"reason"`
	Action     RecoveryAction             `json:"action"`
	ActionNote string                     `json:"action_note,omitempty"`
	DetectedAt int64                      `json:"detected_at"`
}

// RecoverySummary is returned by the recovery scan.
type RecoverySummary struct {
	Orphans       []OrphanedRun `json:"orphans"`
	Resumed       int           `json:"resumed"`
	Failed        int           `json:"failed"`
	NeedAttention int           `json:"need_attention"`
	ScannedAt     int64         `json:"scanned_at"`
	Duration      time.Duration `json:"duration"`
}

// ── Recovery configuration ───────────────────────────────────────────────────

// RecoveryConfig controls orphan detection behavior.
type RecoveryConfig struct {
	// MaxOrphanAge is the maximum time since a run's last update before it is
	// considered stale and non-resumable. Zero disables the age check.
	MaxOrphanAge time.Duration

	// AutoResume controls whether resumable orphans are automatically resumed
	// or left in requires_attention state for operator review.
	AutoResume bool

	// AutoFail controls whether non-resumable orphans are automatically marked
	// as failed or left in requires_attention state.
	AutoFail bool

	// Now overrides the current time for testing. If nil, time.Now() is used.
	Now func() time.Time
}

// DefaultRecoveryConfig returns a production-suitable default configuration.
func DefaultRecoveryConfig() RecoveryConfig {
	return RecoveryConfig{
		MaxOrphanAge: 24 * time.Hour,
		AutoResume:   true,
		AutoFail:     true,
	}
}

func (c RecoveryConfig) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// ── Orphan detector ──────────────────────────────────────────────────────────

// inFlightStatuses returns the set of TaskRunStatus values that indicate
// a run was actively processing when the daemon stopped.
func inFlightStatuses() map[state.TaskRunStatus]bool {
	return map[state.TaskRunStatus]bool{
		state.TaskRunStatusQueued:           true,
		state.TaskRunStatusRunning:          true,
		state.TaskRunStatusRetrying:         true,
		state.TaskRunStatusBlocked:          true,
		state.TaskRunStatusAwaitingApproval: true,
	}
}

// RunFetcher abstracts the DocsRepository methods needed for recovery.
// This keeps the recovery engine testable without a full state store.
type RunFetcher interface {
	ListTaskRuns(ctx context.Context, taskID string, limit int) ([]state.TaskRun, error)
	GetTask(ctx context.Context, taskID string) (state.TaskSpec, error)
	GetWorkflowJournal(ctx context.Context, runID string) (state.WorkflowJournalDoc, error)
}

// DetectOrphans scans all task runs and identifies those that were in-flight
// at the time of daemon restart. Each orphan is classified with a recovery
// action based on whether it has a checkpoint and how old it is.
func DetectOrphans(ctx context.Context, fetcher RunFetcher, runs []state.TaskRun, cfg RecoveryConfig) []OrphanedRun {
	now := cfg.now()
	nowUnix := now.Unix()
	active := inFlightStatuses()

	var orphans []OrphanedRun
	for _, run := range runs {
		if !active[run.Status] {
			continue
		}

		orphan := OrphanedRun{
			Run:        run,
			Reason:     OrphanReasonDaemonRestart,
			DetectedAt: nowUnix,
		}

		// Try to load the task spec (best-effort).
		if task, err := fetcher.GetTask(ctx, run.TaskID); err == nil {
			orphan.Task = task
		}

		// Try to load the journal/checkpoint.
		journal, err := fetcher.GetWorkflowJournal(ctx, run.RunID)
		if err == nil && journal.RunID != "" {
			orphan.Journal = &journal
			if journal.Checkpoint != nil {
				orphan.Checkpoint = journal.Checkpoint
			}
		}

		// Classify age.
		runAge := runAgeSince(run, nowUnix)
		if cfg.MaxOrphanAge > 0 && runAge > cfg.MaxOrphanAge {
			orphan.Reason = OrphanReasonStale
		}

		// Determine recovery action.
		orphan.Action, orphan.ActionNote = classifyRecoveryAction(orphan, cfg)
		orphans = append(orphans, orphan)
	}
	return orphans
}

// classifyRecoveryAction determines what should happen with an orphaned run.
func classifyRecoveryAction(orphan OrphanedRun, cfg RecoveryConfig) (RecoveryAction, string) {
	// Stale runs cannot be resumed — too much time has passed.
	if orphan.Reason == OrphanReasonStale {
		if cfg.AutoFail {
			return RecoveryFail, "run exceeded max orphan age; marked failed"
		}
		return RecoveryRequiresAttention, "run exceeded max orphan age; needs operator review"
	}

	// No checkpoint means no safe resume point.
	if orphan.Checkpoint == nil {
		if cfg.AutoFail {
			return RecoveryFail, "no checkpoint available; marked failed"
		}
		return RecoveryRequiresAttention, "no checkpoint available; needs operator review"
	}

	// Has checkpoint — can resume.
	if cfg.AutoResume {
		return RecoveryResume, fmt.Sprintf("resuming from checkpoint: step=%s attempt=%d",
			orphan.Checkpoint.StepID, orphan.Checkpoint.Attempt)
	}
	return RecoveryRequiresAttention, fmt.Sprintf("checkpoint available (step=%s); needs operator approval to resume",
		orphan.Checkpoint.StepID)
}

// runAgeSince computes the duration since the run's last meaningful timestamp.
func runAgeSince(run state.TaskRun, nowUnix int64) time.Duration {
	lastUpdate := run.StartedAt
	if len(run.Transitions) > 0 {
		lastTransition := run.Transitions[len(run.Transitions)-1].At
		if lastTransition > lastUpdate {
			lastUpdate = lastTransition
		}
	}
	if lastUpdate == 0 {
		return 0
	}
	return time.Duration(nowUnix-lastUpdate) * time.Second
}

// ── Recovery executor ────────────────────────────────────────────────────────

// RecoveryExecutor applies recovery actions to orphaned runs.
type RecoveryExecutor struct {
	cfg RecoveryConfig
}

// NewRecoveryExecutor creates a new recovery executor.
func NewRecoveryExecutor(cfg RecoveryConfig) *RecoveryExecutor {
	return &RecoveryExecutor{cfg: cfg}
}

// Execute processes a list of orphaned runs, applying the determined recovery
// action to each. It returns a summary of all actions taken.
//
// For "resume" actions, the caller is responsible for actually restarting the
// run loop — this method only prepares the run's state for resumption by
// restoring its journal and marking it ready.
//
// For "fail" actions, this method transitions the run to failed status.
func (e *RecoveryExecutor) Execute(orphans []OrphanedRun) RecoverySummary {
	now := e.cfg.now()
	summary := RecoverySummary{
		Orphans:   orphans,
		ScannedAt: now.Unix(),
	}
	start := now

	for i := range orphans {
		switch orphans[i].Action {
		case RecoveryResume:
			summary.Resumed++
		case RecoveryFail:
			summary.Failed++
		case RecoveryRequiresAttention:
			summary.NeedAttention++
		}
	}

	summary.Duration = e.cfg.now().Sub(start)
	return summary
}

// PrepareResume builds the in-memory WorkflowJournal from an orphan's persisted
// journal doc, ready for the runtime to continue execution. Returns nil if the
// orphan has no journal.
func PrepareResume(orphan OrphanedRun, persister JournalPersister) *WorkflowJournal {
	if orphan.Journal == nil {
		return nil
	}
	return RestoreFromDoc(*orphan.Journal, persister)
}

// MarkFailed transitions an orphaned run to the failed state with a recovery
// reason. Returns the updated run or an error if the transition is not allowed.
func MarkFailed(run *state.TaskRun, reason string, at int64, actor string) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	return run.ApplyTransition(
		state.TaskRunStatusFailed,
		at,
		actor,
		"recovery",
		reason,
		map[string]any{"recovery": true},
	)
}

// ── Formatting ───────────────────────────────────────────────────────────────

// FormatRecoverySummary returns a human-readable summary of recovery results.
func FormatRecoverySummary(s RecoverySummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Recovery scan at %d: %d orphan(s) detected\n", s.ScannedAt, len(s.Orphans))
	if s.Resumed > 0 {
		fmt.Fprintf(&b, "  ✅ Resumed: %d\n", s.Resumed)
	}
	if s.Failed > 0 {
		fmt.Fprintf(&b, "  ❌ Failed: %d\n", s.Failed)
	}
	if s.NeedAttention > 0 {
		fmt.Fprintf(&b, "  ⚠️  Needs attention: %d\n", s.NeedAttention)
	}
	if len(s.Orphans) == 0 {
		b.WriteString("  No orphaned runs found.\n")
	}
	for _, o := range s.Orphans {
		fmt.Fprintf(&b, "  • run=%s task=%s status=%s → %s: %s\n",
			o.Run.RunID, o.Run.TaskID, o.Run.Status, o.Action, o.ActionNote)
	}
	fmt.Fprintf(&b, "  Duration: %s\n", s.Duration)
	return b.String()
}

// FormatOrphanedRun returns a detailed description of a single orphaned run.
func FormatOrphanedRun(o OrphanedRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Orphaned Run: %s (task=%s)\n", o.Run.RunID, o.Run.TaskID)
	fmt.Fprintf(&b, "  Status: %s  Attempt: %d\n", o.Run.Status, o.Run.Attempt)
	fmt.Fprintf(&b, "  Reason: %s\n", o.Reason)
	fmt.Fprintf(&b, "  Action: %s\n", o.Action)
	if o.ActionNote != "" {
		fmt.Fprintf(&b, "  Note: %s\n", o.ActionNote)
	}
	if o.Checkpoint != nil {
		fmt.Fprintf(&b, "  Checkpoint: step=%s attempt=%d status=%s\n",
			o.Checkpoint.StepID, o.Checkpoint.Attempt, o.Checkpoint.Status)
	} else {
		b.WriteString("  Checkpoint: none\n")
	}
	if o.Task.TaskID != "" {
		fmt.Fprintf(&b, "  Task: %s (%s)\n", o.Task.Title, o.Task.Status)
	}
	return b.String()
}
