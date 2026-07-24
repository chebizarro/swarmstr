package acp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const backgroundTaskPersistTimeout = 2 * time.Second

// BeginBackgroundTask mirrors detached child work into the durable requester
// task registry without allocating a pending waiter channel. Existing matching
// non-terminal records are resumed; terminal records remain immutable.
func (d *Dispatcher) BeginBackgroundTask(ctx context.Context, record TaskRecord) (bool, error) {
	if d == nil || d.store == nil {
		return false, nil
	}
	now := d.now()
	record = record.Normalize(now)
	if record.TaskID == "" {
		return false, fmt.Errorf("acp background task: task_id required")
	}
	record.Runtime = firstNonEmpty(record.Runtime, "acp")
	record.Status = TaskStatusRunning
	record.DeliveryStatus = DeliveryPending
	record.StartedAt = &now
	record.LastEventAt = &now

	d.mu.Lock()
	defer d.mu.Unlock()
	existing, err := d.store.Get(ctx, record.TaskID)
	if err != nil {
		return false, err
	}
	if existing != nil {
		if err := validateBackgroundTaskIdentity(*existing, record); err != nil {
			return false, err
		}
		if existing.Status.Terminal() {
			return false, nil
		}
		startedAt := existing.StartedAt
		if startedAt == nil {
			startedAt = &now
		}
		if err := d.store.Update(ctx, record.TaskID, TaskPatch{
			Status:      taskStatusPtr(TaskStatusRunning),
			StartedAt:   timePtrPtr(startedAt),
			LastEventAt: timePtrPtr(&now),
		}); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := d.store.Create(ctx, record); err != nil {
		return false, err
	}
	return true, nil
}

// ReconcileBackgroundTask applies one terminal child outcome to the durable
// requester record. It is compare-before-update and therefore idempotent for
// concurrent completion, failure, timeout, and cancellation paths.
func (d *Dispatcher) ReconcileBackgroundTask(ctx context.Context, taskID string, status TaskStatus, result TaskResult) (bool, error) {
	if d == nil || d.store == nil {
		return false, nil
	}
	if !status.Terminal() {
		return false, fmt.Errorf("acp background task: terminal status required, got %q", status)
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false, fmt.Errorf("acp background task: task_id required")
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	record, err := d.store.Get(ctx, taskID)
	if err != nil {
		return false, err
	}
	if record == nil {
		return false, fmt.Errorf("acp background task: task %q not found", taskID)
	}
	if record.Status.Terminal() {
		return false, nil
	}

	endedAt := d.now()
	if result.CompletedAt > 0 {
		endedAt = time.Unix(result.CompletedAt, 0)
	}
	progress := strings.TrimSpace(result.Text)
	if progress == "" {
		progress = record.ProgressSummary
	}
	terminal := firstNonEmpty(strings.TrimSpace(result.Error), progress)
	delivery := DeliveryDelivered
	if err := d.store.Update(ctx, taskID, TaskPatch{
		Status:          taskStatusPtr(status),
		DeliveryStatus:  deliveryStatusPtr(delivery),
		EndedAt:         timePtrPtr(&endedAt),
		LastEventAt:     timePtrPtr(&endedAt),
		Error:           stringPtr(strings.TrimSpace(result.Error)),
		ProgressSummary: stringPtr(progress),
		TerminalSummary: stringPtr(terminal),
		ResultWorker:    workerMetadataPtrPtr(result.Worker),
		Artifacts:       artifactsPtr(result.Artifacts),
	}); err != nil {
		return false, err
	}
	return true, nil
}

func validateBackgroundTaskIdentity(existing, requested TaskRecord) error {
	if existing.Runtime != "" && requested.Runtime != "" && existing.Runtime != requested.Runtime {
		return fmt.Errorf("acp background task: task %q runtime mismatch", existing.TaskID)
	}
	if existing.RequesterSessionKey != "" && requested.RequesterSessionKey != "" && existing.RequesterSessionKey != requested.RequesterSessionKey {
		return fmt.Errorf("acp background task: task %q requester mismatch", existing.TaskID)
	}
	if existing.WorkerSessionKey != "" && requested.WorkerSessionKey != "" && existing.WorkerSessionKey != requested.WorkerSessionKey {
		return fmt.Errorf("acp background task: task %q worker mismatch", existing.TaskID)
	}
	return nil
}

type managerBackgroundTask struct {
	dispatcher       *Dispatcher
	taskID           string
	workerSessionKey string
	requesterKey     string
	once             sync.Once
}

func (m *Manager) beginBackgroundTask(ctx context.Context, sessionKey string, input RunSessionTurnInput) *managerBackgroundTask {
	if m == nil || m.dispatcher == nil || strings.TrimSpace(input.RequestID) == "" || m.sessions == nil {
		return nil
	}
	record, err := m.loadRecord(ctx, sessionKey)
	if err != nil || record == nil {
		return nil
	}
	meta := decodeSessionRuntimeMeta(record)
	requesterKey := canonicalSessionKey(meta.ParentSessionKey)
	if requesterKey == "" {
		return nil
	}
	taskID := strings.TrimSpace(input.RequestID)
	active, err := m.dispatcher.BeginBackgroundTask(ctx, TaskRecord{
		TaskID:              taskID,
		Runtime:             "acp",
		RequesterSessionKey: requesterKey,
		WorkerSessionKey:    sessionKey,
		Instructions:        input.Text,
		Label:               firstNonEmpty(strings.TrimSpace(input.Agent), meta.Agent),
		Worker: &WorkerTaskMetadata{
			AgentID:    firstNonEmpty(strings.TrimSpace(input.Agent), meta.Agent),
			SessionKey: sessionKey,
			Backend:    firstNonEmpty(normalizeBackendID(input.Backend), meta.Backend),
		},
	})
	if err != nil || !active {
		return nil
	}
	return &managerBackgroundTask{dispatcher: m.dispatcher, taskID: taskID, workerSessionKey: sessionKey, requesterKey: requesterKey}
}

func (t *managerBackgroundTask) finish(events []RuntimeEvent, err error, explicitCancel bool) {
	if t == nil || t.dispatcher == nil {
		return
	}
	if errors.Is(err, context.Canceled) && !explicitCancel {
		// A disconnected requester leaves the pending prompt and durable task alive;
		// ReconcilePendingPrompt resumes the same request ID after reconnect.
		persistCtx, cancel := context.WithTimeout(context.Background(), backgroundTaskPersistTimeout)
		t.dispatcher.RecordProgress(persistCtx, t.taskID, "requester disconnected; awaiting reconciliation")
		cancel()
		return
	}
	t.once.Do(func() {
		status := TaskStatusSucceeded
		errorText := ""
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			status = TaskStatusTimedOut
			errorText = err.Error()
		case errors.Is(err, context.Canceled):
			status = TaskStatusCancelled
			errorText = err.Error()
		case err != nil && isBlockedTerminalText(err.Error()):
			status = TaskStatusBlocked
			errorText = err.Error()
		case err != nil:
			status = TaskStatusFailed
			errorText = err.Error()
		}
		progress := backgroundTaskProgress(events)
		result := TaskResult{
			TaskID: t.taskID,
			Text:   progress,
			Error:  errorText,
			Worker: &WorkerMetadata{
				TaskID:    t.taskID,
				SessionID: t.workerSessionKey,
				ParentContext: &ParentContext{
					SessionID: t.requesterKey,
				},
			},
		}
		persistCtx, cancel := context.WithTimeout(context.Background(), backgroundTaskPersistTimeout)
		defer cancel()
		_, _ = t.dispatcher.ReconcileBackgroundTask(persistCtx, t.taskID, status, result)
	})
}

func backgroundTaskProgress(events []RuntimeEvent) string {
	var summary string
	for _, event := range events {
		chunk := firstNonEmpty(event.Text, event.Title)
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		combined := strings.TrimSpace(strings.Join([]string{summary, chunk}, " "))
		combined = strings.Join(strings.Fields(combined), " ")
		runes := []rune(combined)
		if len(runes) > 240 {
			combined = string(runes[:239]) + "…"
		}
		summary = combined
	}
	return summary
}
