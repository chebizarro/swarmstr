package tasks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// ResumeDecision is retained as a compatibility alias for the canonical task
// approval decision vocabulary in the state model.
type ResumeDecision = state.TaskApprovalDecision

const (
	ResumeDecisionResume   = state.TaskApprovalDecisionResume
	ResumeDecisionApproved = state.TaskApprovalDecisionApproved
	ResumeDecisionRejected = state.TaskApprovalDecisionRejected
	ResumeDecisionAmended  = state.TaskApprovalDecisionAmended
)

// Service is the daemon-owned domain facade for task mutations and lookups. It
// keeps the ledger/store abstraction out of transport adapters while preserving
// DocsRepository as the durable backing store through Store implementations.
type Service struct {
	ledger *Ledger
	store  Store
	events *EventEmitter
	now    func() time.Time
}

// ServiceOption customizes a Service.
type ServiceOption func(*Service)

// WithServiceLedger uses an already-constructed ledger. When omitted, NewService
// creates one over the provided store.
func WithServiceLedger(ledger *Ledger) ServiceOption {
	return func(s *Service) {
		if ledger != nil {
			s.ledger = ledger
			if s.store == nil {
				s.store = ledger.store
			}
		}
	}
}

// WithServiceEvents attaches an EventEmitter through the existing ledger
// observer bridge. Observer registration stays optional so follow-on wiring can
// choose the daemon's lifecycle observers explicitly.
func WithServiceEvents(events *EventEmitter) ServiceOption {
	return func(s *Service) {
		s.events = events
	}
}

// WithServiceClock overrides the clock used for service-created IDs and
// transition metadata.
func WithServiceClock(now func() time.Time) ServiceOption {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// NewService constructs a task service backed by store. The returned service owns
// one ledger instance unless WithServiceLedger supplies an existing daemon-owned
// ledger.
func NewService(store Store, opts ...ServiceOption) *Service {
	s := &Service{
		store: store,
		now:   time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.ledger == nil {
		s.ledger = NewLedger(store)
	}
	if s.store == nil {
		s.store = s.ledger.store
	}
	if s.events != nil {
		s.ledger.AddObserver(NewEmitterObserver(s.events))
	}
	return s
}

// Ledger returns the service-owned ledger for daemon subsystems that need to
// register observers in follow-on milestones.
func (s *Service) Ledger() *Ledger {
	if s == nil {
		return nil
	}
	return s.ledger
}

// Store returns the durable backing store used by the service.
func (s *Service) Store() Store {
	if s == nil {
		return nil
	}
	return s.store
}

// Events returns the optional event emitter wired into the service ledger.
func (s *Service) Events() *EventEmitter {
	if s == nil {
		return nil
	}
	return s.events
}

// CreateTask persists a new task through the ledger. It normalizes missing IDs
// and records an initial pending transition before applying an optional requested
// non-pending status.
func (s *Service) CreateTask(ctx context.Context, task state.TaskSpec, source TaskSource, sourceRef, actor string) (*LedgerEntry, error) {
	if s == nil || s.ledger == nil {
		return nil, fmt.Errorf("task service is nil")
	}
	if source == "" {
		source = TaskSourceManual
	}
	actor = strings.TrimSpace(actor)
	now := s.clock().Unix()

	task = normalizeServiceTaskSpec(task)
	if task.TaskID == "" {
		task.TaskID = fmt.Sprintf("task-%d", s.clock().UnixNano())
	}
	requestedStatus := task.Status
	if !requestedStatus.Valid() {
		requestedStatus = state.TaskStatusPending
	}
	task.CurrentRunID = ""
	task.LastRunID = ""
	if task.CreatedAt == 0 {
		task.CreatedAt = now
	}
	task.UpdatedAt = now
	task.Status = state.TaskStatusPending
	task.Transitions = []state.TaskTransition{{
		To:     state.TaskStatusPending,
		At:     now,
		Actor:  actor,
		Source: string(source),
		Reason: "task created",
	}}
	if requestedStatus != state.TaskStatusPending {
		if err := task.ApplyTransition(requestedStatus, now, actor, string(source), "task created", nil); err != nil {
			return nil, err
		}
	}

	return s.ledger.CreateTask(ctx, task, source, sourceRef)
}

// GetTask returns a task and its runs without exposing ledger wrapper metadata.
func (s *Service) GetTask(ctx context.Context, taskID string, runsLimit int) (state.TaskSpec, []state.TaskRun, error) {
	if s == nil || s.ledger == nil {
		return state.TaskSpec{}, nil, fmt.Errorf("task service is nil")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return state.TaskSpec{}, nil, fmt.Errorf("task_id is required")
	}
	if runsLimit <= 0 {
		runsLimit = 20
	}

	entry, err := s.ledger.GetTask(ctx, taskID)
	if err != nil {
		return state.TaskSpec{}, nil, err
	}
	runEntries, err := s.ledger.ListRuns(ctx, ListRunsOptions{TaskID: taskID, Limit: runsLimit, OrderBy: "created_at", OrderDesc: true})
	if err != nil {
		return state.TaskSpec{}, nil, err
	}
	runs := make([]state.TaskRun, 0, len(runEntries))
	for _, runEntry := range runEntries {
		if runEntry != nil {
			runs = append(runs, runEntry.Run)
		}
	}
	return entry.Task, runs, nil
}

// ListTasks returns ledger entries for domain callers that need source metadata
// in addition to the canonical task document.
func (s *Service) ListTasks(ctx context.Context, opts ListTasksOptions) ([]*LedgerEntry, error) {
	if s == nil || s.ledger == nil {
		return nil, fmt.Errorf("task service is nil")
	}
	return s.ledger.ListTasks(ctx, opts)
}

// ResumeTask applies an operator resume/approval decision. Resume, approved,
// and amended decisions create or re-queue a run. Rejected decisions block the
// task and never create a run.
func (s *Service) ResumeTask(ctx context.Context, taskID string, decision ResumeDecision, actor, reason string) (*RunEntry, *LedgerEntry, error) {
	if s == nil || s.ledger == nil {
		return nil, nil, fmt.Errorf("task service is nil")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, nil, fmt.Errorf("task_id is required")
	}
	decision, ok := normalizeResumeDecision(decision)
	if !ok {
		return nil, nil, fmt.Errorf("resume decision %q is invalid", decision)
	}
	actor = strings.TrimSpace(actor)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = defaultResumeReason(decision)
	}

	taskEntry, err := s.ledger.GetTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}

	if decision == ResumeDecisionRejected {
		return s.rejectTaskResume(ctx, taskEntry, actor, reason, decision)
	}

	meta := approvalDecisionMeta(decision, actor, reason, s.clock().Unix())
	if taskEntry.Task.Status != state.TaskStatusReady {
		if !state.AllowedTaskTransition(taskEntry.Task.Status, state.TaskStatusReady) {
			return nil, nil, fmt.Errorf("cannot resume task %q from status %q", taskID, taskEntry.Task.Status)
		}
		var err error
		taskEntry, err = s.ledger.UpdateTaskStatusWithMeta(ctx, taskID, state.TaskStatusReady, actor, "tasks.service", reason, meta)
		if err != nil {
			return nil, nil, err
		}
		if decision == ResumeDecisionApproved || decision == ResumeDecisionAmended {
			taskEntry, err = s.annotateTaskDecision(ctx, taskEntry, decision, actor, reason)
			if err != nil {
				return nil, nil, err
			}
		}
	} else if decision == ResumeDecisionApproved || decision == ResumeDecisionAmended {
		var err error
		taskEntry, err = s.annotateTaskDecision(ctx, taskEntry, decision, actor, reason)
		if err != nil {
			return nil, nil, err
		}
	}

	var runEntry *RunEntry
	currentRunID := strings.TrimSpace(taskEntry.Task.CurrentRunID)
	if currentRunID != "" {
		if existing, err := s.ledger.GetRun(ctx, currentRunID); err == nil && existing != nil {
			if existing.Run.Status == state.TaskRunStatusQueued {
				runEntry = existing
			} else if state.AllowedTaskRunTransition(existing.Run.Status, state.TaskRunStatusQueued) {
				runEntry, err = s.ledger.UpdateRunStatus(ctx, currentRunID, state.TaskRunStatusQueued, actor, "tasks.service", reason)
				if err != nil {
					return nil, nil, err
				}
			}
		} else if err != nil {
			return nil, nil, err
		}
	}
	if runEntry == nil {
		runID := fmt.Sprintf("taskrun-%d", s.clock().UnixNano())
		runEntry, err = s.ledger.CreateRun(ctx, taskID, runID, string(decision), actor, "tasks.service")
		if err != nil {
			return nil, nil, err
		}
	}

	taskEntry, err = s.ledger.GetTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	return runEntry, taskEntry, nil
}

// StartWorkerRun persists a daemon-executed worker task and marks its run as
// running through the ledger. Callers prepare transport-specific task fields
// before crossing this service boundary.
func (s *Service) StartWorkerRun(ctx context.Context, task state.TaskSpec, source TaskSource, sourceRef, runID, trigger, actor, reason, parentRunID string) (state.TaskSpec, state.TaskRun, error) {
	if s == nil || s.ledger == nil {
		return state.TaskSpec{}, state.TaskRun{}, fmt.Errorf("task service is nil")
	}
	if source == "" {
		source = TaskSourceManual
	}
	actor = strings.TrimSpace(actor)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "worker task started"
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		runID = fmt.Sprintf("taskrun-%d", s.clock().UnixNano())
	}
	task = normalizeServiceTaskSpec(task)
	if task.TaskID == "" {
		return state.TaskSpec{}, state.TaskRun{}, fmt.Errorf("task_id is required")
	}
	now := s.clock().Unix()
	if task.CreatedAt == 0 {
		task.CreatedAt = now
	}
	if task.UpdatedAt == 0 {
		task.UpdatedAt = now
	}

	entry, err := s.ledger.GetTask(ctx, task.TaskID)
	if err != nil && !isTaskNotFoundError(err, task.TaskID) {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	if err != nil {
		requestedStatus := task.Status
		if !requestedStatus.Valid() {
			requestedStatus = state.TaskStatusPending
		}
		task.Status = state.TaskStatusPending
		task.CurrentRunID = ""
		task.LastRunID = ""
		task.Transitions = nil
		entry, err = s.CreateTask(ctx, task, source, sourceRef, actor)
		if err != nil {
			return state.TaskSpec{}, state.TaskRun{}, err
		}
		if requestedStatus != state.TaskStatusPending && requestedStatus != entry.Task.Status && state.AllowedTaskTransition(entry.Task.Status, requestedStatus) {
			entry, err = s.ledger.UpdateTaskStatus(ctx, entry.Task.TaskID, requestedStatus, actor, string(source), "task created")
			if err != nil {
				return state.TaskSpec{}, state.TaskRun{}, err
			}
		}
	} else {
		// Preserve existing run linkage while saving caller-supplied task fields.
		task.CurrentRunID = entry.Task.CurrentRunID
		task.LastRunID = entry.Task.LastRunID
		if task.CreatedAt == 0 {
			task.CreatedAt = entry.Task.CreatedAt
		}
		if len(task.Transitions) == 0 {
			task.Transitions = append([]state.TaskTransition(nil), entry.Task.Transitions...)
		}
		entry, err = s.ledger.SaveTaskState(ctx, task, source, sourceRef)
		if err != nil {
			return state.TaskSpec{}, state.TaskRun{}, err
		}
	}

	if entry.Task.Status != state.TaskStatusInProgress && state.AllowedTaskTransition(entry.Task.Status, state.TaskStatusInProgress) {
		entry, err = s.ledger.UpdateTaskStatus(ctx, entry.Task.TaskID, state.TaskStatusInProgress, actor, string(source), reason)
		if err != nil {
			return state.TaskSpec{}, state.TaskRun{}, err
		}
	}

	runEntry, err := s.ledger.CreateRun(ctx, entry.Task.TaskID, runID, strings.TrimSpace(trigger), actor, string(source))
	if err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	if parentRunID = strings.TrimSpace(parentRunID); parentRunID != "" {
		run := runEntry.Run
		run.ParentRunID = parentRunID
		runEntry, err = s.ledger.SaveRunState(ctx, run, source, sourceRef)
		if err != nil {
			return state.TaskSpec{}, state.TaskRun{}, err
		}
	}
	if runEntry.Run.Status != state.TaskRunStatusRunning && state.AllowedTaskRunTransition(runEntry.Run.Status, state.TaskRunStatusRunning) {
		runEntry, err = s.ledger.UpdateRunStatus(ctx, runEntry.Run.RunID, state.TaskRunStatusRunning, actor, string(source), "worker run started")
		if err != nil {
			return state.TaskSpec{}, state.TaskRun{}, err
		}
	}
	entry, err = s.ledger.GetTask(ctx, entry.Task.TaskID)
	if err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	return entry.Task, runEntry.Run, nil
}

// FinishWorkerRun persists a worker result and terminal task/run transitions
// through the ledger-backed service path.
func (s *Service) FinishWorkerRun(ctx context.Context, taskID, runID string, result state.TaskResultRef, usage state.TaskUsage, actor string, turnErr error, historyEntryIDs []string) (state.TaskSpec, state.TaskRun, error) {
	if s == nil || s.ledger == nil {
		return state.TaskSpec{}, state.TaskRun{}, fmt.Errorf("task service is nil")
	}
	taskID = strings.TrimSpace(taskID)
	runID = strings.TrimSpace(runID)
	if taskID == "" {
		return state.TaskSpec{}, state.TaskRun{}, fmt.Errorf("task_id is required")
	}
	if runID == "" {
		return state.TaskSpec{}, state.TaskRun{}, fmt.Errorf("run_id is required")
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "worker"
	}
	now := s.clock().Unix()

	runEntry, err := s.ledger.GetRun(ctx, runID)
	if err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	run := runEntry.Run.Normalize()
	run.Result = result
	run.Usage = usage
	if turnErr != nil {
		run.Error = strings.TrimSpace(turnErr.Error())
	}
	targetRunStatus := state.TaskRunStatusCompleted
	runReason := "worker run completed"
	targetTaskStatus := state.TaskStatusCompleted
	taskReason := "worker task completed"
	if turnErr != nil {
		targetRunStatus = state.TaskRunStatusFailed
		runReason = run.Error
		targetTaskStatus = state.TaskStatusFailed
		taskReason = run.Error
	}

	taskEntry, err := s.ledger.GetTask(ctx, taskID)
	if err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	if taskEntry.Task.Status != targetTaskStatus && state.AllowedTaskTransition(taskEntry.Task.Status, targetTaskStatus) {
		preflight := taskEntry.Task
		if err := preflight.ApplyTransition(targetTaskStatus, now, actor, string(taskEntry.Source), taskReason, nil); err != nil {
			return state.TaskSpec{}, state.TaskRun{}, err
		}
	}

	runEntry, err = s.ledger.SaveRunState(ctx, run, runEntry.Source, runEntry.SourceRef)
	if err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}

	if runEntry.Run.Status != targetRunStatus && state.AllowedTaskRunTransition(runEntry.Run.Status, targetRunStatus) {
		runEntry, err = s.ledger.UpdateRunStatus(ctx, runID, targetRunStatus, actor, string(runEntry.Source), runReason)
		if err != nil {
			return state.TaskSpec{}, state.TaskRun{}, err
		}
	}

	task := taskEntry.Task.Normalize()
	if task.Meta == nil {
		task.Meta = map[string]any{}
	}
	if turnErr == nil {
		if _, ok := task.Meta["verification_status"]; !ok {
			task.Meta["verification_status"] = "pending"
		}
		if len(historyEntryIDs) > 0 {
			task.Meta["result_history_entry_id"] = historyEntryIDs[len(historyEntryIDs)-1]
		}
	}
	taskEntry, err = s.ledger.SaveTaskState(ctx, task, taskEntry.Source, taskEntry.SourceRef)
	if err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}

	if taskEntry.Task.Status != targetTaskStatus && state.AllowedTaskTransition(taskEntry.Task.Status, targetTaskStatus) {
		taskEntry, err = s.ledger.UpdateTaskStatus(ctx, taskID, targetTaskStatus, actor, string(taskEntry.Source), taskReason)
		if err != nil {
			return state.TaskSpec{}, state.TaskRun{}, err
		}
	}

	task = taskEntry.Task.Normalize()
	task.CurrentRunID = ""
	task.LastRunID = runID
	task.UpdatedAt = now
	taskEntry, err = s.ledger.SaveTaskState(ctx, task, taskEntry.Source, taskEntry.SourceRef)
	if err != nil {
		return state.TaskSpec{}, state.TaskRun{}, err
	}
	return taskEntry.Task, runEntry.Run, nil
}

// CancelTask cancels a task and any active runs through the ledger.
func (s *Service) CancelTask(ctx context.Context, taskID, actor, reason string) error {
	if s == nil || s.ledger == nil {
		return fmt.Errorf("task service is nil")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task_id is required")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "task cancelled"
	}
	return s.ledger.CancelTask(ctx, taskID, strings.TrimSpace(actor), reason)
}

func (s *Service) clock() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (s *Service) rejectTaskResume(ctx context.Context, taskEntry *LedgerEntry, actor, reason string, decision ResumeDecision) (*RunEntry, *LedgerEntry, error) {
	if taskEntry == nil {
		return nil, nil, fmt.Errorf("task is nil")
	}
	taskID := taskEntry.Task.TaskID
	meta := approvalDecisionMeta(decision, actor, reason, s.clock().Unix())
	var err error
	if taskEntry.Task.Status != state.TaskStatusBlocked {
		if !state.AllowedTaskTransition(taskEntry.Task.Status, state.TaskStatusBlocked) {
			return nil, nil, fmt.Errorf("cannot reject task %q from status %q", taskID, taskEntry.Task.Status)
		}
		taskEntry, err = s.ledger.UpdateTaskStatusWithMeta(ctx, taskID, state.TaskStatusBlocked, actor, "tasks.service", reason, meta)
		if err != nil {
			return nil, nil, err
		}
		taskEntry, err = s.annotateTaskDecision(ctx, taskEntry, decision, actor, reason)
		if err != nil {
			return nil, nil, err
		}
	} else {
		taskEntry, err = s.annotateTaskDecision(ctx, taskEntry, decision, actor, reason)
		if err != nil {
			return nil, nil, err
		}
	}

	currentRunID := strings.TrimSpace(taskEntry.Task.CurrentRunID)
	if currentRunID != "" {
		if existing, err := s.ledger.GetRun(ctx, currentRunID); err == nil && existing != nil && !isTerminalRunStatus(existing.Run.Status) {
			if existing.Run.Status != state.TaskRunStatusBlocked && state.AllowedTaskRunTransition(existing.Run.Status, state.TaskRunStatusBlocked) {
				if _, err := s.ledger.UpdateRunStatus(ctx, currentRunID, state.TaskRunStatusBlocked, actor, "tasks.service", reason); err != nil {
					return nil, nil, err
				}
			}
		} else if err != nil {
			return nil, nil, err
		}
	}

	taskEntry, err = s.ledger.GetTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	return nil, taskEntry, nil
}

func (s *Service) annotateTaskDecision(ctx context.Context, taskEntry *LedgerEntry, decision ResumeDecision, actor, reason string) (*LedgerEntry, error) {
	if taskEntry == nil {
		return nil, fmt.Errorf("task is nil")
	}
	task := taskEntry.Task.Normalize()
	if task.Meta == nil {
		task.Meta = map[string]any{}
	}
	at := s.clock().Unix()
	task.Meta["approval_decision"] = string(decision)
	task.Meta["approval_actor"] = strings.TrimSpace(actor)
	task.Meta["approval_reason"] = strings.TrimSpace(reason)
	task.Meta["approval_at"] = at
	if decision == ResumeDecisionAmended {
		task.Meta["amendment_note"] = strings.TrimSpace(reason)
	}
	if len(task.Transitions) > 0 {
		last := &task.Transitions[len(task.Transitions)-1]
		if last.Meta == nil {
			last.Meta = map[string]any{}
		}
		for k, v := range approvalDecisionMeta(decision, actor, reason, at) {
			last.Meta[k] = v
		}
	}
	task.UpdatedAt = at
	return s.ledger.SaveTaskState(ctx, task, taskEntry.Source, taskEntry.SourceRef)
}

func approvalDecisionMeta(decision ResumeDecision, actor, reason string, at int64) map[string]any {
	meta := map[string]any{
		"approval_decision": string(decision),
	}
	if actor = strings.TrimSpace(actor); actor != "" {
		meta["approval_actor"] = actor
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		meta["approval_reason"] = reason
	}
	if at > 0 {
		meta["approval_at"] = at
	}
	if decision == ResumeDecisionAmended && reason != "" {
		meta["amendment_note"] = reason
	}
	return meta
}

func defaultResumeReason(decision ResumeDecision) string {
	switch decision {
	case ResumeDecisionApproved:
		return "approved via control rpc"
	case ResumeDecisionRejected:
		return "rejected via control rpc"
	case ResumeDecisionAmended:
		return "amended via control rpc"
	default:
		return "resumed via control rpc"
	}
}

func normalizeResumeDecision(decision ResumeDecision) (ResumeDecision, bool) {
	parsed, ok := state.ParseTaskApprovalDecision(string(decision))
	return ResumeDecision(parsed), ok
}

func isTaskNotFoundError(err error, taskID string) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), fmt.Sprintf("task %q not found", strings.TrimSpace(taskID)))
}

func normalizeServiceTaskSpec(task state.TaskSpec) state.TaskSpec {
	task.Title = strings.TrimSpace(task.Title)
	task.Instructions = strings.TrimSpace(task.Instructions)
	task.TaskID = strings.TrimSpace(task.TaskID)
	task.GoalID = strings.TrimSpace(task.GoalID)
	task.ParentTaskID = strings.TrimSpace(task.ParentTaskID)
	task.PlanID = strings.TrimSpace(task.PlanID)
	task.SessionID = strings.TrimSpace(task.SessionID)
	task.AssignedAgent = strings.TrimSpace(task.AssignedAgent)
	task.ToolProfile = strings.TrimSpace(task.ToolProfile)
	task.CurrentRunID = strings.TrimSpace(task.CurrentRunID)
	task.LastRunID = strings.TrimSpace(task.LastRunID)
	return task.Normalize()
}
