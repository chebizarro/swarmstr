// Package tasks provides a unified task ledger for tracking all detached work:
// ACP dispatches, cron executions, webhook-triggered turns, approvals, sandbox jobs,
// and workflow steps.
//
// The ledger provides:
//   - Durable persistence of task and run records
//   - Status-based queries and filtering
//   - Lineage tracking (parent/child relationships)
//   - Event emission for lifecycle changes
//   - CLI and gateway method surfaces
package tasks

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// TaskSource identifies where a task originated.
type TaskSource string

const (
	TaskSourceACP      TaskSource = "acp"
	TaskSourceCron     TaskSource = "cron"
	TaskSourceWebhook  TaskSource = "webhook"
	TaskSourceWorkflow TaskSource = "workflow"
	TaskSourceManual   TaskSource = "manual"
	TaskSourceDVM      TaskSource = "dvm"
	TaskSourceApproval TaskSource = "approval"
	TaskSourceSandbox  TaskSource = "sandbox"
)

// LedgerEntry wraps a TaskSpec with ledger-specific metadata.
type LedgerEntry struct {
	Task      state.TaskSpec  `json:"task"`
	Source    TaskSource      `json:"source"`
	SourceRef string          `json:"source_ref,omitempty"` // e.g., cron job ID, webhook ID
	Runs      []state.TaskRun `json:"runs,omitempty"`
	CreatedAt int64           `json:"created_at"`
	UpdatedAt int64           `json:"updated_at"`
}

// RunEntry wraps a TaskRun with ledger-specific metadata.
type RunEntry struct {
	Run       state.TaskRun `json:"run"`
	Source    TaskSource    `json:"source"`
	SourceRef string        `json:"source_ref,omitempty"`
	CreatedAt int64         `json:"created_at"`
	UpdatedAt int64         `json:"updated_at"`
}

// ListTasksOptions controls task listing queries.
type ListTasksOptions struct {
	Status       []state.TaskStatus // filter by status (OR)
	Source       []TaskSource       // filter by source (OR)
	AgentID      string             // filter by assigned agent
	GoalID       string             // filter by parent goal
	ParentTaskID string             // filter by parent task
	SessionID    string             // filter by session
	Limit        int                // max results (0 = default 100)
	Offset       int                // pagination offset
	OrderBy      string             // "created_at" | "updated_at" | "status"
	OrderDesc    bool               // descending order
}

// ListRunsOptions controls run listing queries.
type ListRunsOptions struct {
	TaskID      string                // filter by task
	Status      []state.TaskRunStatus // filter by status (OR)
	AgentID     string                // filter by agent
	Limit       int                   // max results (0 = default 100)
	Offset      int                   // pagination offset
	OrderBy     string                // "created_at" | "started_at" | "ended_at"
	OrderDesc   bool                  // descending order
	IncludeMeta bool                  // include full metadata
}

// TaskStats summarizes ledger contents.
type TaskStats struct {
	TotalTasks     int            `json:"total_tasks"`
	TotalRuns      int            `json:"total_runs"`
	ByStatus       map[string]int `json:"by_status"`
	BySource       map[string]int `json:"by_source"`
	ActiveRuns     int            `json:"active_runs"`
	CompletedToday int            `json:"completed_today"`
	FailedToday    int            `json:"failed_today"`
}

// Observer receives task lifecycle events.
type Observer interface {
	OnTaskCreated(ctx context.Context, entry LedgerEntry)
	OnTaskUpdated(ctx context.Context, entry LedgerEntry, transition state.TaskTransition)
	OnRunCreated(ctx context.Context, entry RunEntry)
	OnRunUpdated(ctx context.Context, entry RunEntry, transition state.TaskRunTransition)
}

// Ledger is the central task tracking subsystem.
type Ledger struct {
	mu        sync.RWMutex
	tasks     map[string]*LedgerEntry // by task_id
	runs      map[string]*RunEntry    // by run_id
	observers []Observer
	store     Store
}

// Store defines the persistence interface for the ledger.
type Store interface {
	// SaveTask persists a task entry.
	SaveTask(ctx context.Context, entry *LedgerEntry) error
	// LoadTask retrieves a task by ID.
	LoadTask(ctx context.Context, taskID string) (*LedgerEntry, error)
	// ListTasks queries tasks with filters.
	ListTasks(ctx context.Context, opts ListTasksOptions) ([]*LedgerEntry, error)
	// DeleteTask removes a task and its runs.
	DeleteTask(ctx context.Context, taskID string) error

	// SaveRun persists a run entry.
	SaveRun(ctx context.Context, entry *RunEntry) error
	// LoadRun retrieves a run by ID.
	LoadRun(ctx context.Context, runID string) (*RunEntry, error)
	// ListRuns queries runs with filters.
	ListRuns(ctx context.Context, opts ListRunsOptions) ([]*RunEntry, error)

	// Stats returns aggregate statistics.
	Stats(ctx context.Context) (TaskStats, error)

	// Prune removes old completed/failed entries based on retention policy.
	Prune(ctx context.Context, olderThan time.Duration) (int, error)
}

// NewLedger creates a new task ledger with the given store.
func NewLedger(store Store) *Ledger {
	return &Ledger{
		tasks: make(map[string]*LedgerEntry),
		runs:  make(map[string]*RunEntry),
		store: store,
	}
}

// AddObserver registers an event observer.
func (l *Ledger) AddObserver(obs Observer) {
	l.mu.Lock()
	l.observers = append(l.observers, obs)
	l.mu.Unlock()
}

// CreateTask registers a new task in the ledger.
func (l *Ledger) CreateTask(ctx context.Context, task state.TaskSpec, source TaskSource, sourceRef string) (*LedgerEntry, error) {
	task = task.Normalize()
	if err := task.Validate(); err != nil {
		return nil, fmt.Errorf("invalid task: %w", err)
	}

	now := time.Now().Unix()
	if task.CreatedAt == 0 {
		task.CreatedAt = now
	}
	task.UpdatedAt = now

	entry := &LedgerEntry{
		Task:      task,
		Source:    source,
		SourceRef: sourceRef,
		CreatedAt: now,
		UpdatedAt: now,
	}

	l.mu.Lock()
	if _, exists := l.tasks[task.TaskID]; exists {
		l.mu.Unlock()
		return nil, fmt.Errorf("task %q already exists", task.TaskID)
	}
	l.tasks[task.TaskID] = entry
	observers := append([]Observer(nil), l.observers...)
	l.mu.Unlock()

	// Persist
	if l.store != nil {
		if err := l.store.SaveTask(ctx, entry); err != nil {
			l.mu.Lock()
			delete(l.tasks, task.TaskID)
			l.mu.Unlock()
			return nil, fmt.Errorf("persist task %q: %w", task.TaskID, err)
		}
	}

	// Notify observers
	for _, obs := range observers {
		obs.OnTaskCreated(ctx, *entry)
	}

	return entry, nil
}

// GetTask retrieves a task by ID.
func (l *Ledger) GetTask(ctx context.Context, taskID string) (*LedgerEntry, error) {
	l.mu.RLock()
	entry, ok := l.tasks[taskID]
	l.mu.RUnlock()

	if ok {
		return entry, nil
	}

	// Try store
	if l.store != nil {
		entry, err := l.store.LoadTask(ctx, taskID)
		if err != nil {
			return nil, err
		}
		if entry != nil {
			l.mu.Lock()
			l.tasks[taskID] = entry
			l.mu.Unlock()
			return entry, nil
		}
	}

	return nil, fmt.Errorf("task %q not found", taskID)
}

// UpdateTaskStatus transitions a task to a new status.
func (l *Ledger) UpdateTaskStatus(ctx context.Context, taskID string, to state.TaskStatus, actor, source, reason string) (*LedgerEntry, error) {
	l.mu.Lock()
	entry, ok := l.tasks[taskID]
	if !ok {
		l.mu.Unlock()
		return nil, fmt.Errorf("task %q not found", taskID)
	}

	now := time.Now().Unix()
	prevTaskUpdatedAt := entry.Task.UpdatedAt
	prevEntryUpdatedAt := entry.UpdatedAt
	if err := entry.Task.ApplyTransition(to, now, actor, source, reason, nil); err != nil {
		l.mu.Unlock()
		return nil, err
	}
	entry.UpdatedAt = now

	observers := append([]Observer(nil), l.observers...)
	transition := entry.Task.Transitions[len(entry.Task.Transitions)-1]
	l.mu.Unlock()

	// Persist
	if l.store != nil {
		if err := l.store.SaveTask(ctx, entry); err != nil {
			l.mu.Lock()
			if len(entry.Task.Transitions) > 0 {
				entry.Task.Transitions = entry.Task.Transitions[:len(entry.Task.Transitions)-1]
			}
			entry.Task.Status = transition.From
			entry.Task.UpdatedAt = prevTaskUpdatedAt
			entry.UpdatedAt = prevEntryUpdatedAt
			l.mu.Unlock()
			return nil, fmt.Errorf("persist task status update %q: %w", taskID, err)
		}
	}

	// Notify observers
	for _, obs := range observers {
		obs.OnTaskUpdated(ctx, *entry, transition)
	}

	return entry, nil
}

// CreateRun starts a new execution attempt for a task.
func (l *Ledger) CreateRun(ctx context.Context, taskID, runID, trigger, actor, source string) (*RunEntry, error) {
	l.mu.Lock()
	taskEntry, ok := l.tasks[taskID]
	if !ok {
		l.mu.Unlock()
		return nil, fmt.Errorf("task %q not found", taskID)
	}

	now := time.Now().Unix()
	prevCurrentRunID := taskEntry.Task.CurrentRunID
	prevTaskUpdatedAt := taskEntry.UpdatedAt

	// Collect prior runs for attempt numbering
	var priorRuns []state.TaskRun
	for _, run := range taskEntry.Runs {
		priorRuns = append(priorRuns, run)
	}

	run, err := state.NewTaskRunAttempt(taskEntry.Task, runID, priorRuns, now, trigger, actor, source)
	if err != nil {
		l.mu.Unlock()
		return nil, err
	}

	runEntry := &RunEntry{
		Run:       run,
		Source:    taskEntry.Source,
		SourceRef: taskEntry.SourceRef,
		CreatedAt: now,
		UpdatedAt: now,
	}

	l.runs[runID] = runEntry
	taskEntry.Runs = append(taskEntry.Runs, run)
	taskEntry.Task.CurrentRunID = runID
	taskEntry.UpdatedAt = now

	observers := append([]Observer(nil), l.observers...)
	l.mu.Unlock()

	// Persist
	if l.store != nil {
		if err := l.store.SaveRun(ctx, runEntry); err != nil {
			l.mu.Lock()
			delete(l.runs, runID)
			if n := len(taskEntry.Runs); n > 0 {
				taskEntry.Runs = taskEntry.Runs[:n-1]
			}
			taskEntry.Task.CurrentRunID = prevCurrentRunID
			taskEntry.UpdatedAt = prevTaskUpdatedAt
			l.mu.Unlock()
			return nil, fmt.Errorf("persist run %q: %w", runID, err)
		}
		if err := l.store.SaveTask(ctx, taskEntry); err != nil {
			l.mu.Lock()
			delete(l.runs, runID)
			if n := len(taskEntry.Runs); n > 0 {
				taskEntry.Runs = taskEntry.Runs[:n-1]
			}
			taskEntry.Task.CurrentRunID = prevCurrentRunID
			taskEntry.UpdatedAt = prevTaskUpdatedAt
			l.mu.Unlock()
			return nil, fmt.Errorf("persist task %q with new run %q: %w", taskID, runID, err)
		}
	}

	// Notify observers
	for _, obs := range observers {
		obs.OnRunCreated(ctx, *runEntry)
	}

	return runEntry, nil
}

// UpdateRunStatus transitions a run to a new status.
func (l *Ledger) UpdateRunStatus(ctx context.Context, runID string, to state.TaskRunStatus, actor, source, reason string) (*RunEntry, error) {
	l.mu.Lock()
	entry, ok := l.runs[runID]
	if !ok {
		l.mu.Unlock()
		return nil, fmt.Errorf("run %q not found", runID)
	}

	now := time.Now().Unix()
	originalRun := cloneRunEntry(entry)
	if err := entry.Run.ApplyTransition(to, now, actor, source, reason, nil); err != nil {
		l.mu.Unlock()
		return nil, err
	}
	entry.UpdatedAt = now

	// Update the task's run record as well
	var taskEntryToPersist *LedgerEntry
	var originalTask *LedgerEntry
	if taskEntry, ok := l.tasks[entry.Run.TaskID]; ok {
		taskEntryToPersist = taskEntry
		originalTask = cloneLedgerEntry(taskEntry)
		for i, run := range taskEntry.Runs {
			if run.RunID == runID {
				taskEntry.Runs[i] = entry.Run
				break
			}
		}
		taskEntry.Task.LastRunID = runID
		taskEntry.UpdatedAt = now
	}

	runSnapshot := cloneRunEntry(entry)
	var taskSnapshot *LedgerEntry
	if taskEntryToPersist != nil {
		taskSnapshot = cloneLedgerEntry(taskEntryToPersist)
	}
	observers := append([]Observer(nil), l.observers...)
	transition := entry.Run.Transitions[len(entry.Run.Transitions)-1]
	l.mu.Unlock()

	// Persist both the run and the task entry because the task embeds run
	// snapshots and last-run bookkeeping that were updated above.
	if l.store != nil {
		if err := l.store.SaveRun(ctx, runSnapshot); err != nil {
			l.mu.Lock()
			entry.Run = originalRun.Run
			entry.UpdatedAt = originalRun.UpdatedAt
			if originalTask != nil {
				if taskEntry, ok := l.tasks[entry.Run.TaskID]; ok {
					*taskEntry = *originalTask
				}
			}
			l.mu.Unlock()
			return nil, fmt.Errorf("persist run status update %q: %w", runID, err)
		}
		if taskSnapshot != nil {
			if err := l.store.SaveTask(ctx, taskSnapshot); err != nil {
				l.mu.Lock()
				entry.Run = originalRun.Run
				entry.UpdatedAt = originalRun.UpdatedAt
				if originalTask != nil {
					if taskEntry, ok := l.tasks[entry.Run.TaskID]; ok {
						*taskEntry = *originalTask
					}
				}
				l.mu.Unlock()
				return nil, fmt.Errorf("persist task snapshot for run %q: %w", runID, err)
			}
		}
	}

	// Notify observers
	for _, obs := range observers {
		obs.OnRunUpdated(ctx, *entry, transition)
	}

	return entry, nil
}

// GetRun retrieves a run by ID.
func (l *Ledger) GetRun(ctx context.Context, runID string) (*RunEntry, error) {
	l.mu.RLock()
	entry, ok := l.runs[runID]
	l.mu.RUnlock()

	if ok {
		return entry, nil
	}

	// Try store
	if l.store != nil {
		entry, err := l.store.LoadRun(ctx, runID)
		if err != nil {
			return nil, err
		}
		if entry != nil {
			l.mu.Lock()
			l.runs[runID] = entry
			l.mu.Unlock()
			return entry, nil
		}
	}

	return nil, fmt.Errorf("run %q not found", runID)
}

// ListTasks returns tasks matching the given filters.
func (l *Ledger) ListTasks(ctx context.Context, opts ListTasksOptions) ([]*LedgerEntry, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}

	l.mu.RLock()
	var results []*LedgerEntry
	for _, entry := range l.tasks {
		if matchesTaskFilter(entry, opts) {
			results = append(results, entry)
		}
	}
	l.mu.RUnlock()

	// Sort
	sortTasks(results, opts.OrderBy, opts.OrderDesc)

	// Apply pagination
	if opts.Offset >= len(results) {
		return []*LedgerEntry{}, nil
	}
	results = results[opts.Offset:]
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

// ListRuns returns runs matching the given filters.
func (l *Ledger) ListRuns(ctx context.Context, opts ListRunsOptions) ([]*RunEntry, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}

	l.mu.RLock()
	var results []*RunEntry
	for _, entry := range l.runs {
		if matchesRunFilter(entry, opts) {
			results = append(results, entry)
		}
	}
	l.mu.RUnlock()

	// Sort
	sortRuns(results, opts.OrderBy, opts.OrderDesc)

	// Apply pagination
	if opts.Offset >= len(results) {
		return []*RunEntry{}, nil
	}
	results = results[opts.Offset:]
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

// Stats returns aggregate statistics about the ledger.
func (l *Ledger) Stats(ctx context.Context) TaskStats {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return computeTaskStats(l.tasks, l.runs, time.Now())
}

// CancelTask cancels a task and any active runs.
func (l *Ledger) CancelTask(ctx context.Context, taskID, actor, reason string) error {
	l.mu.Lock()
	entry, ok := l.tasks[taskID]
	if !ok {
		l.mu.Unlock()
		return fmt.Errorf("task %q not found", taskID)
	}

	now := time.Now().Unix()
	originalTask := cloneLedgerEntry(entry)
	originalRuns := make(map[string]*RunEntry)
	for _, run := range l.runs {
		if run.Run.TaskID == taskID {
			originalRuns[run.Run.RunID] = cloneRunEntry(run)
		}
	}

	// Cancel any active runs first, keeping both the canonical run map and the
	// task's embedded run snapshots in sync for persistence/restart.
	var runsToPersist []*RunEntry
	for i := range entry.Runs {
		runID := entry.Runs[i].RunID
		if runEntry, ok := l.runs[runID]; ok {
			if !isTerminalRunStatus(runEntry.Run.Status) {
				_ = runEntry.Run.ApplyTransition(state.TaskRunStatusCancelled, now, actor, "ledger", reason, nil)
				runEntry.UpdatedAt = now
				entry.Runs[i] = runEntry.Run
				runsToPersist = append(runsToPersist, runEntry)
			}
		}
	}

	// Cancel the task
	if state.AllowedTaskTransition(entry.Task.Status, state.TaskStatusCancelled) {
		_ = entry.Task.ApplyTransition(state.TaskStatusCancelled, now, actor, "ledger", reason, nil)
		entry.UpdatedAt = now
	}

	runSnapshots := make([]*RunEntry, len(runsToPersist))
	for i, runEntry := range runsToPersist {
		runSnapshots[i] = cloneRunEntry(runEntry)
	}
	taskSnapshot := cloneLedgerEntry(entry)
	l.mu.Unlock()

	// Persist
	if l.store != nil {
		for _, runEntry := range runSnapshots {
			if err := l.store.SaveRun(ctx, runEntry); err != nil {
				l.mu.Lock()
				if originalTask != nil {
					*entry = *originalTask
				}
				for runID, originalRun := range originalRuns {
					if currentRun, ok := l.runs[runID]; ok {
						*currentRun = *originalRun
					}
				}
				l.mu.Unlock()
				return fmt.Errorf("persist cancelled run %q: %w", runEntry.Run.RunID, err)
			}
		}
		if err := l.store.SaveTask(ctx, taskSnapshot); err != nil {
			l.mu.Lock()
			if originalTask != nil {
				*entry = *originalTask
			}
			for runID, originalRun := range originalRuns {
				if currentRun, ok := l.runs[runID]; ok {
					*currentRun = *originalRun
				}
			}
			l.mu.Unlock()
			return fmt.Errorf("persist cancelled task %q: %w", taskID, err)
		}
	}

	return nil
}

// GetTaskLineage returns a task and all its descendants.
func (l *Ledger) GetTaskLineage(ctx context.Context, taskID string) ([]*LedgerEntry, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var lineage []*LedgerEntry
	visited := make(map[string]bool)

	var collect func(id string)
	collect = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true

		entry, ok := l.tasks[id]
		if !ok {
			return
		}
		lineage = append(lineage, entry)

		// Find children
		for _, e := range l.tasks {
			if e.Task.ParentTaskID == id {
				collect(e.Task.TaskID)
			}
		}
	}

	collect(taskID)
	return lineage, nil
}

func cloneLedgerEntry(entry *LedgerEntry) *LedgerEntry {
	if entry == nil {
		return nil
	}
	out := *entry
	out.Runs = append([]state.TaskRun(nil), entry.Runs...)
	out.Task.Inputs = cloneAnyMap(entry.Task.Inputs)
	out.Task.ExpectedOutputs = append([]state.TaskOutputSpec(nil), entry.Task.ExpectedOutputs...)
	out.Task.AcceptanceCriteria = append([]state.TaskAcceptanceCriterion(nil), entry.Task.AcceptanceCriteria...)
	out.Task.Dependencies = append([]string(nil), entry.Task.Dependencies...)
	out.Task.EnabledTools = append([]string(nil), entry.Task.EnabledTools...)
	out.Task.Transitions = append([]state.TaskTransition(nil), entry.Task.Transitions...)
	out.Task.Verification = cloneVerificationSpec(entry.Task.Verification)
	out.Task.Meta = cloneAnyMap(entry.Task.Meta)
	return &out
}

func cloneRunEntry(entry *RunEntry) *RunEntry {
	if entry == nil {
		return nil
	}
	out := *entry
	out.Run.Transitions = append([]state.TaskRunTransition(nil), entry.Run.Transitions...)
	out.Run.Verification = cloneVerificationSpec(entry.Run.Verification)
	out.Run.Meta = cloneAnyMap(entry.Run.Meta)
	return &out
}

func cloneVerificationSpec(spec state.VerificationSpec) state.VerificationSpec {
	spec.Meta = cloneAnyMap(spec.Meta)
	if len(spec.Checks) > 0 {
		spec.Checks = make([]state.VerificationCheck, len(spec.Checks))
		for i, check := range spec.Checks {
			check.Meta = cloneAnyMap(check.Meta)
			spec.Checks[i] = check
		}
	}
	return spec
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// Helper functions

func matchesTaskFilter(entry *LedgerEntry, opts ListTasksOptions) bool {
	if len(opts.Status) > 0 {
		found := false
		for _, s := range opts.Status {
			if entry.Task.Status == s {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if len(opts.Source) > 0 {
		found := false
		for _, s := range opts.Source {
			if entry.Source == s {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if opts.AgentID != "" && entry.Task.AssignedAgent != opts.AgentID {
		return false
	}

	if opts.GoalID != "" && entry.Task.GoalID != opts.GoalID {
		return false
	}

	if opts.ParentTaskID != "" && entry.Task.ParentTaskID != opts.ParentTaskID {
		return false
	}

	if opts.SessionID != "" && entry.Task.SessionID != opts.SessionID {
		return false
	}

	return true
}

func matchesRunFilter(entry *RunEntry, opts ListRunsOptions) bool {
	if opts.TaskID != "" && entry.Run.TaskID != opts.TaskID {
		return false
	}

	if len(opts.Status) > 0 {
		found := false
		for _, s := range opts.Status {
			if entry.Run.Status == s {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if opts.AgentID != "" && entry.Run.AgentID != opts.AgentID {
		return false
	}

	return true
}

func sortTasks(entries []*LedgerEntry, orderBy string, desc bool) {
	sort.Slice(entries, func(i, j int) bool {
		var cmp int
		switch strings.ToLower(orderBy) {
		case "updated_at":
			cmp = int(entries[i].UpdatedAt - entries[j].UpdatedAt)
		case "status":
			cmp = strings.Compare(string(entries[i].Task.Status), string(entries[j].Task.Status))
		default: // created_at
			cmp = int(entries[i].CreatedAt - entries[j].CreatedAt)
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
}

func sortRuns(entries []*RunEntry, orderBy string, desc bool) {
	sort.Slice(entries, func(i, j int) bool {
		var cmp int
		switch strings.ToLower(orderBy) {
		case "started_at":
			cmp = int(entries[i].Run.StartedAt - entries[j].Run.StartedAt)
		case "ended_at":
			cmp = int(entries[i].Run.EndedAt - entries[j].Run.EndedAt)
		default: // created_at
			cmp = int(entries[i].CreatedAt - entries[j].CreatedAt)
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
}

func isTerminalRunStatus(status state.TaskRunStatus) bool {
	switch status {
	case state.TaskRunStatusCompleted, state.TaskRunStatusFailed, state.TaskRunStatusCancelled:
		return true
	}
	return false
}
