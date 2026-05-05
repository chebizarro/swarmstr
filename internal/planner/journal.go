package planner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"metiq/internal/store/state"
)

// ── Journal entry types ──────────────────────────────────────────────────────

// JournalEntryType classifies a workflow journal entry.
type JournalEntryType string

const (
	// JournalCheckpoint records a resumable state snapshot.
	JournalCheckpoint JournalEntryType = "checkpoint"
	// JournalStateTransition records a task or run status change.
	JournalStateTransition JournalEntryType = "state_transition"
	// JournalStepStart records the beginning of a plan step execution.
	JournalStepStart JournalEntryType = "step_start"
	// JournalStepComplete records successful completion of a plan step.
	JournalStepComplete JournalEntryType = "step_complete"
	// JournalStepFail records a failed plan step.
	JournalStepFail JournalEntryType = "step_fail"
	// JournalToolDispatch records a tool call being dispatched.
	JournalToolDispatch JournalEntryType = "tool_dispatch"
	// JournalToolResult records the result of a tool call.
	JournalToolResult JournalEntryType = "tool_result"
	// JournalDelegation records a sub-task delegation event.
	JournalDelegation JournalEntryType = "delegation"
	// JournalError records a non-fatal error during execution.
	JournalError JournalEntryType = "error"
	// JournalPendingAction records an action queued for future execution.
	JournalPendingAction JournalEntryType = "pending_action"
)

// validJournalEntryTypes enumerates all known entry types for validation.
var validJournalEntryTypes = map[JournalEntryType]bool{
	JournalCheckpoint:      true,
	JournalStateTransition: true,
	JournalStepStart:       true,
	JournalStepComplete:    true,
	JournalStepFail:        true,
	JournalToolDispatch:    true,
	JournalToolResult:      true,
	JournalDelegation:      true,
	JournalError:           true,
	JournalPendingAction:   true,
}

// ValidJournalEntryType reports whether t is a recognized entry type.
func ValidJournalEntryType(t JournalEntryType) bool {
	return validJournalEntryTypes[t]
}

// ── Journal entry ────────────────────────────────────────────────────────────

// JournalEntry is an in-memory workflow journal entry.
type JournalEntry struct {
	EntryID   string           `json:"entry_id"`
	TaskID    string           `json:"task_id"`
	RunID     string           `json:"run_id"`
	Sequence  int64            `json:"sequence"`
	Type      JournalEntryType `json:"type"`
	CreatedAt int64            `json:"created_at"`
	Summary   string           `json:"summary,omitempty"`
	Data      map[string]any   `json:"data,omitempty"`
}

// ── Workflow checkpoint ──────────────────────────────────────────────────────

// WorkflowCheckpoint captures the resumable state of a task run.
// It records accumulated progress and pending work so a crashed run
// can resume from the last checkpoint.
type WorkflowCheckpoint struct {
	StepID         string                 `json:"step_id,omitempty"`
	Attempt        int                    `json:"attempt"`
	Status         string                 `json:"status"`
	Usage          state.TaskUsage        `json:"usage,omitempty"`
	Verification   state.VerificationSpec `json:"verification,omitempty"`
	PendingActions []PendingAction        `json:"pending_actions,omitempty"`
	CreatedAt      int64                  `json:"created_at"`
	Meta           map[string]any         `json:"meta,omitempty"`
}

// PendingAction describes a deferred action that was scheduled but not
// yet executed when a checkpoint was taken.
type PendingAction struct {
	ActionID    string         `json:"action_id"`
	Type        string         `json:"type"` // tool_call, delegation, step_execution
	Description string         `json:"description,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
	CreatedAt   int64          `json:"created_at"`
}

// ── Persist callback ─────────────────────────────────────────────────────────

// JournalPersister is the function signature used to persist a journal snapshot.
// It is called on every append and checkpoint with the current doc state.
type JournalPersister func(ctx context.Context, doc state.WorkflowJournalDoc) error

// ── Workflow journal ─────────────────────────────────────────────────────────

const (
	// DefaultMaxJournalEntries caps the in-memory journal to prevent unbounded
	// growth. Older entries are evicted FIFO when this limit is reached, but
	// the latest checkpoint is always preserved.
	DefaultMaxJournalEntries = 500
)

// WorkflowJournal is an append-only, in-memory execution journal for a single
// task run. It supports periodic checkpointing and persistence via a pluggable
// JournalPersister callback.
//
// Thread safety: all public methods are safe for concurrent use. The journal's
// own sync.RWMutex protects entries, checkpoint, and sequence state. The
// persister callback is invoked outside the lock to avoid holding the lock
// during I/O.
type WorkflowJournal struct {
	mu         sync.RWMutex
	taskID     string
	runID      string
	entries    []JournalEntry
	checkpoint *WorkflowCheckpoint
	nextSeq    atomic.Int64
	maxEntries int
	persister  JournalPersister
}

// NewWorkflowJournal creates an empty journal for the given task run.
func NewWorkflowJournal(taskID, runID string) *WorkflowJournal {
	return &WorkflowJournal{
		taskID:     taskID,
		runID:      runID,
		maxEntries: DefaultMaxJournalEntries,
	}
}

// NewWorkflowJournalWithPersister creates a journal with a persistence callback.
// The persister is called (outside the lock) after every Append and Checkpoint.
func NewWorkflowJournalWithPersister(taskID, runID string, persister JournalPersister) *WorkflowJournal {
	j := NewWorkflowJournal(taskID, runID)
	j.persister = persister
	return j
}

// SetMaxEntries overrides the default max entries cap. Values <= 0 are ignored.
func (j *WorkflowJournal) SetMaxEntries(n int) {
	if n > 0 {
		j.mu.Lock()
		j.maxEntries = n
		j.mu.Unlock()
	}
}

// Append adds a journal entry and optionally persists the journal.
// The entry's Sequence and CreatedAt are assigned automatically if zero.
// Returns the assigned entry ID.
func (j *WorkflowJournal) Append(ctx context.Context, entryType JournalEntryType, summary string, data map[string]any) (string, error) {
	if !ValidJournalEntryType(entryType) {
		return "", fmt.Errorf("invalid journal entry type %q", entryType)
	}

	seq := j.nextSeq.Add(1)
	now := time.Now().Unix()
	entryID := fmt.Sprintf("je-%s-%d", j.runID, seq)

	entry := JournalEntry{
		EntryID:   entryID,
		TaskID:    j.taskID,
		RunID:     j.runID,
		Sequence:  seq,
		Type:      entryType,
		CreatedAt: now,
		Summary:   summary,
		Data:      cloneData(data),
	}

	j.mu.Lock()
	j.entries = append(j.entries, entry)
	j.trimLocked()
	j.mu.Unlock()

	if err := j.persist(ctx); err != nil {
		return entryID, fmt.Errorf("journal persist: %w", err)
	}
	return entryID, nil
}

// Checkpoint saves a resumable state snapshot and persists the journal.
// The checkpoint entry is also appended to the journal log.
func (j *WorkflowJournal) Checkpoint(ctx context.Context, cp WorkflowCheckpoint) (string, error) {
	if cp.CreatedAt == 0 {
		cp.CreatedAt = time.Now().Unix()
	}

	seq := j.nextSeq.Add(1)
	entryID := fmt.Sprintf("je-%s-%d", j.runID, seq)

	entry := JournalEntry{
		EntryID:   entryID,
		TaskID:    j.taskID,
		RunID:     j.runID,
		Sequence:  seq,
		Type:      JournalCheckpoint,
		CreatedAt: cp.CreatedAt,
		Summary:   fmt.Sprintf("checkpoint: step=%s attempt=%d status=%s", cp.StepID, cp.Attempt, cp.Status),
		Data:      checkpointToData(cp),
	}

	cpCopy := cp // snapshot
	cpCopy.Verification = cloneVerificationSpec(cp.Verification)
	cpCopy.Meta = cloneData(cp.Meta)
	if len(cp.PendingActions) > 0 {
		cpCopy.PendingActions = make([]PendingAction, len(cp.PendingActions))
		for i, a := range cp.PendingActions {
			cpCopy.PendingActions[i] = a
			cpCopy.PendingActions[i].Params = cloneData(a.Params)
		}
	}
	j.mu.Lock()
	j.entries = append(j.entries, entry)
	j.checkpoint = &cpCopy
	j.trimLocked()
	j.mu.Unlock()

	if err := j.persist(ctx); err != nil {
		return entryID, fmt.Errorf("journal persist: %w", err)
	}
	return entryID, nil
}

// LatestCheckpoint returns a deep copy of the most recent checkpoint, or nil if none.
func (j *WorkflowJournal) LatestCheckpoint() *WorkflowCheckpoint {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.checkpoint == nil {
		return nil
	}
	cp := *j.checkpoint
	cp.Verification = cloneVerificationSpec(j.checkpoint.Verification)
	cp.Meta = cloneData(j.checkpoint.Meta)
	if len(j.checkpoint.PendingActions) > 0 {
		cp.PendingActions = make([]PendingAction, len(j.checkpoint.PendingActions))
		for i, pa := range j.checkpoint.PendingActions {
			cp.PendingActions[i] = pa
			cp.PendingActions[i].Params = cloneData(pa.Params)
		}
	}
	return &cp
}

// Entries returns a deep-copied snapshot of all journal entries in order.
func (j *WorkflowJournal) Entries() []JournalEntry {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return deepCopyEntries(j.entries)
}

// EntriesByType returns deep-copied entries matching the given type.
func (j *WorkflowJournal) EntriesByType(t JournalEntryType) []JournalEntry {
	j.mu.RLock()
	defer j.mu.RUnlock()
	var matching []JournalEntry
	for _, e := range j.entries {
		if e.Type == t {
			matching = append(matching, e)
		}
	}
	return deepCopyEntries(matching)
}

// EntriesSince returns deep-copied entries with sequence > afterSeq.
func (j *WorkflowJournal) EntriesSince(afterSeq int64) []JournalEntry {
	j.mu.RLock()
	defer j.mu.RUnlock()
	var matching []JournalEntry
	for _, e := range j.entries {
		if e.Sequence > afterSeq {
			matching = append(matching, e)
		}
	}
	return deepCopyEntries(matching)
}

// Len returns the number of journal entries.
func (j *WorkflowJournal) Len() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return len(j.entries)
}

// TaskID returns the journal's task ID.
func (j *WorkflowJournal) TaskID() string { return j.taskID }

// RunID returns the journal's run ID.
func (j *WorkflowJournal) RunID() string { return j.runID }

// ── Snapshot / restore ───────────────────────────────────────────────────────

// Snapshot returns the current journal state as a persistable doc.
func (j *WorkflowJournal) Snapshot() state.WorkflowJournalDoc {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.snapshotLocked()
}

func (j *WorkflowJournal) snapshotLocked() state.WorkflowJournalDoc {
	doc := state.WorkflowJournalDoc{
		Version:   1,
		TaskID:    j.taskID,
		RunID:     j.runID,
		Entries:   make([]state.WorkflowJournalEntryDoc, len(j.entries)),
		NextSeq:   j.nextSeq.Load(),
		UpdatedAt: time.Now().Unix(),
	}
	for i, e := range j.entries {
		doc.Entries[i] = state.WorkflowJournalEntryDoc{
			EntryID:   e.EntryID,
			Sequence:  e.Sequence,
			Type:      string(e.Type),
			CreatedAt: e.CreatedAt,
			Summary:   e.Summary,
			Data:      cloneData(e.Data),
		}
	}
	if j.checkpoint != nil {
		doc.Checkpoint = checkpointToDoc(j.checkpoint)
	}
	return doc
}

// RestoreFromDoc rebuilds an in-memory journal from a persisted doc.
// This is the primary mechanism for resuming a journal after daemon restart.
func RestoreFromDoc(doc state.WorkflowJournalDoc, persister JournalPersister) *WorkflowJournal {
	j := &WorkflowJournal{
		taskID:     doc.TaskID,
		runID:      doc.RunID,
		maxEntries: DefaultMaxJournalEntries,
		persister:  persister,
	}
	j.nextSeq.Store(doc.NextSeq)

	j.entries = make([]JournalEntry, len(doc.Entries))
	for i, e := range doc.Entries {
		j.entries[i] = JournalEntry{
			EntryID:   e.EntryID,
			TaskID:    doc.TaskID,
			RunID:     doc.RunID,
			Sequence:  e.Sequence,
			Type:      JournalEntryType(e.Type),
			CreatedAt: e.CreatedAt,
			Summary:   e.Summary,
			Data:      cloneData(e.Data),
		}
	}

	if doc.Checkpoint != nil {
		j.checkpoint = checkpointFromDoc(doc.Checkpoint)
	}
	return j
}

// ── Format ───────────────────────────────────────────────────────────────────

// FormatJournal returns a human-readable summary of the journal state.
func FormatJournal(j *WorkflowJournal) string {
	if j == nil {
		return "<nil journal>"
	}
	j.mu.RLock()
	defer j.mu.RUnlock()

	var b strings.Builder
	fmt.Fprintf(&b, "Workflow Journal: task=%s run=%s entries=%d\n", j.taskID, j.runID, len(j.entries))

	if j.checkpoint != nil {
		fmt.Fprintf(&b, "  Latest checkpoint: step=%s attempt=%d status=%s at=%d\n",
			j.checkpoint.StepID, j.checkpoint.Attempt, j.checkpoint.Status, j.checkpoint.CreatedAt)
		if len(j.checkpoint.PendingActions) > 0 {
			fmt.Fprintf(&b, "  Pending actions: %d\n", len(j.checkpoint.PendingActions))
		}
	} else {
		b.WriteString("  No checkpoint\n")
	}

	// Show last 5 entries
	start := 0
	if len(j.entries) > 5 {
		start = len(j.entries) - 5
		fmt.Fprintf(&b, "  ... (%d earlier entries omitted)\n", start)
	}
	for _, e := range j.entries[start:] {
		fmt.Fprintf(&b, "  [%d] %s: %s\n", e.Sequence, e.Type, e.Summary)
	}
	return b.String()
}

// ── Internal helpers ─────────────────────────────────────────────────────────

// trimLocked evicts oldest entries when the cap is exceeded.
// The latest checkpoint is preserved as a separate field (j.checkpoint)
// but its corresponding entry may be evicted from the log.
// Must be called under j.mu write lock.
func (j *WorkflowJournal) trimLocked() {
	if j.maxEntries > 0 && len(j.entries) > j.maxEntries {
		excess := len(j.entries) - j.maxEntries
		// Shift entries forward
		copy(j.entries, j.entries[excess:])
		j.entries = j.entries[:j.maxEntries]
	}
}

// persist calls the persister callback (if set) outside the lock.
func (j *WorkflowJournal) persist(ctx context.Context) error {
	if j.persister == nil {
		return nil
	}
	doc := j.Snapshot()
	return j.persister(ctx, doc)
}

// deepCopyEntries returns a deep copy of journal entries, cloning Data maps.
func deepCopyEntries(entries []JournalEntry) []JournalEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]JournalEntry, len(entries))
	for i, e := range entries {
		out[i] = e
		out[i].Data = cloneData(e.Data)
	}
	return out
}

func cloneData(data map[string]any) map[string]any {
	if len(data) == 0 {
		return nil
	}
	out := make(map[string]any, len(data))
	for k, v := range data {
		out[k] = v
	}
	return out
}

func checkpointToData(cp WorkflowCheckpoint) map[string]any {
	data := map[string]any{
		"step_id": cp.StepID,
		"attempt": cp.Attempt,
		"status":  cp.Status,
	}
	if len(cp.Verification.Checks) > 0 || cp.Verification.Policy != "" || cp.Verification.VerifiedAt != 0 || cp.Verification.VerifiedBy != "" || len(cp.Verification.Meta) > 0 {
		data["verification"] = cloneVerificationSpec(cp.Verification)
	}
	return data
}

func checkpointToDoc(cp *WorkflowCheckpoint) *state.WorkflowCheckpointDoc {
	if cp == nil {
		return nil
	}
	doc := &state.WorkflowCheckpointDoc{
		StepID:       cp.StepID,
		Attempt:      cp.Attempt,
		Status:       cp.Status,
		Usage:        cp.Usage,
		Verification: cloneVerificationSpec(cp.Verification),
		CreatedAt:    cp.CreatedAt,
		Meta:         cloneData(cp.Meta),
	}
	if len(cp.PendingActions) > 0 {
		doc.PendingActions = make([]state.PendingActionDoc, len(cp.PendingActions))
		for i, a := range cp.PendingActions {
			doc.PendingActions[i] = state.PendingActionDoc{
				ActionID:    a.ActionID,
				Type:        a.Type,
				Description: a.Description,
				Params:      cloneData(a.Params),
				CreatedAt:   a.CreatedAt,
			}
		}
	}
	return doc
}

func checkpointFromDoc(doc *state.WorkflowCheckpointDoc) *WorkflowCheckpoint {
	if doc == nil {
		return nil
	}
	cp := &WorkflowCheckpoint{
		StepID:       doc.StepID,
		Attempt:      doc.Attempt,
		Status:       doc.Status,
		Usage:        doc.Usage,
		Verification: cloneVerificationSpec(doc.Verification),
		CreatedAt:    doc.CreatedAt,
		Meta:         cloneData(doc.Meta),
	}
	if len(doc.PendingActions) > 0 {
		cp.PendingActions = make([]PendingAction, len(doc.PendingActions))
		for i, a := range doc.PendingActions {
			cp.PendingActions[i] = PendingAction{
				ActionID:    a.ActionID,
				Type:        a.Type,
				Description: a.Description,
				Params:      cloneData(a.Params),
				CreatedAt:   a.CreatedAt,
			}
		}
	}
	return cp
}
