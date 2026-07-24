package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// TaskResult carries the result of a dispatched ACP task.
type TaskResult struct {
	TaskID       string
	Text         string
	Error        string
	SenderPubKey string
	Worker       *WorkerMetadata
	TokensUsed   int
	CompletedAt  int64
	Artifacts    []ArtifactPayload
}

// RemoteCancelFunc sends a cancellation event to the worker that owns taskID.
type RemoteCancelFunc func(ctx context.Context, peerPubKey, taskID, reason string) error

type pendingTask struct {
	ch         chan TaskResult
	worker     string
	cancelOnce sync.Once
}

type executionCancellation struct {
	owner  string
	cancel context.CancelCauseFunc
}

// Dispatcher manages ACP task dispatches. In-flight wakeups remain channel
// based, while lifecycle state is mirrored to a TaskStore so tasks survive
// process restarts and can be queried after completion.
type Dispatcher struct {
	mu         sync.Mutex
	pending    map[string]*pendingTask
	executions map[string]*executionCancellation
	store      TaskStore
	now        func() time.Time
}

// NewDispatcher returns a ready-to-use Dispatcher backed by an in-memory task store.
func NewDispatcher() *Dispatcher {
	return NewDispatcherWithStore(nil)
}

// NewDispatcherWithStore returns a dispatcher using store for task persistence.
// A nil store is replaced with an in-memory store.
func NewDispatcherWithStore(store TaskStore) *Dispatcher {
	if store == nil {
		store = NewInMemoryTaskStore()
	}
	return &Dispatcher{
		pending:    make(map[string]*pendingTask),
		executions: make(map[string]*executionCancellation),
		store:      store,
		now:        time.Now,
	}
}

// TaskStore exposes the dispatcher's persistent task store.
func (d *Dispatcher) TaskStore() TaskStore { return d.store }

// GenerateTaskID returns a random hex task ID.
func GenerateTaskID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "task-" + hex.EncodeToString(b)
}

// Register reserves a slot for an in-flight task and returns the channel
// on which the caller should wait. The channel is buffered (capacity 1).
func (d *Dispatcher) Register(taskID string) chan TaskResult {
	return d.RegisterTask(context.Background(), TaskRecord{TaskID: taskID})
}

// RegisterTask reserves a dispatcher slot and creates a queued task record.
func (d *Dispatcher) RegisterTask(ctx context.Context, record TaskRecord) chan TaskResult {
	ch, _ := d.RegisterTaskWithError(ctx, record)
	return ch
}

// RegisterTaskWithError is the error-returning form of RegisterTask. It refuses
// to add an in-memory pending channel when the durable task record cannot be
// created, preventing live work from diverging from persisted state.
func (d *Dispatcher) RegisterTaskWithError(ctx context.Context, record TaskRecord) (chan TaskResult, error) {
	ch := make(chan TaskResult, 1)
	record.TaskID = strings.TrimSpace(record.TaskID)
	if record.TaskID == "" {
		record.TaskID = GenerateTaskID()
	}
	now := d.now()
	record.Status = TaskStatusQueued
	record.DeliveryStatus = DeliveryPending
	record.CreatedAt = now
	if err := d.store.Create(ctx, record); err != nil {
		close(ch)
		return ch, err
	}
	d.mu.Lock()
	worker := ""
	if record.Worker != nil {
		worker = strings.TrimSpace(record.Worker.PubKey)
	}
	d.pending[record.TaskID] = &pendingTask{ch: ch, worker: worker}
	d.mu.Unlock()
	return ch, nil
}

// MarkRunning records that a task was dispatched to a worker and is executing.
func (d *Dispatcher) MarkRunning(ctx context.Context, taskID string) {
	now := d.now()
	_ = d.store.Update(ctx, taskID, TaskPatch{Status: taskStatusPtr(TaskStatusRunning), StartedAt: timePtrPtr(&now), LastEventAt: timePtrPtr(&now)})
}

// RecordProgress records a non-terminal progress summary for taskID.
func (d *Dispatcher) RecordProgress(ctx context.Context, taskID, summary string) {
	_ = d.store.RecordProgress(ctx, taskID, summary)
}

// Deliver routes a TaskResult to the waiting goroutine.
// Returns true if the task was pending and the result was delivered.
func (d *Dispatcher) Deliver(result TaskResult) bool {
	d.mu.Lock()
	pending, ok := d.pending[result.TaskID]
	if ok {
		delete(d.pending, result.TaskID)
	}
	d.mu.Unlock()

	status := classifyTaskResultStatus(result)
	endedAt := d.now()
	if result.CompletedAt > 0 {
		endedAt = time.Unix(result.CompletedAt, 0)
	}
	delivery := DeliveryDelivered
	if !ok {
		delivery = DeliveryFailed
	}
	_ = d.store.Update(context.Background(), result.TaskID, TaskPatch{
		Status:          taskStatusPtr(status),
		DeliveryStatus:  deliveryStatusPtr(delivery),
		EndedAt:         timePtrPtr(&endedAt),
		LastEventAt:     timePtrPtr(&endedAt),
		Error:           stringPtr(result.Error),
		TerminalSummary: stringPtr(firstNonEmpty(result.Error, result.Text)),
		ResultWorker:    workerMetadataPtrPtr(result.Worker),
		Artifacts:       artifactsPtr(result.Artifacts),
	})
	if ok {
		pending.ch <- result
		return true
	}
	return false
}

// Cancel removes a pending task and closes its channel (waking any waiter with
// a zero TaskResult).
func (d *Dispatcher) Cancel(taskID string) {
	d.finishPending(taskID, TaskStatusCancelled, "cancelled", true)
}

func classifyTaskResultStatus(result TaskResult) TaskStatus {
	if strings.TrimSpace(result.Error) != "" {
		if isBlockedTerminalText(result.Error) {
			return TaskStatusBlocked
		}
		return TaskStatusFailed
	}
	if result.Worker != nil && result.Worker.TurnResult != nil {
		outcome := strings.ToLower(strings.TrimSpace(string(result.Worker.TurnResult.Outcome)))
		stopReason := strings.ToLower(strings.TrimSpace(string(result.Worker.TurnResult.StopReason)))
		switch outcome {
		case "blocked":
			return TaskStatusBlocked
		case "failed":
			return TaskStatusFailed
		case "aborted", "cancelled", "canceled":
			return TaskStatusTimedOut
		}
		if strings.Contains(stopReason, "blocked") || strings.Contains(stopReason, "permission") || strings.Contains(stopReason, "authorization") {
			return TaskStatusBlocked
		}
	}
	if isProgressOnlyTerminalText(result.Text) {
		return TaskStatusBlocked
	}
	return TaskStatusSucceeded
}

func isProgressOnlyTerminalText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	progressMarkers := []string{
		"i'll ", "i will ", "i’m going to", "i am going to", "i need to", "i'm going to",
		"let me ", "next,", "next i", "plan:", "todo", "working on", "in progress", "i’ll now",
	}
	for _, marker := range progressMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isBlockedTerminalText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	blockedMarkers := []string{
		"permission denied", "permission required", "permission failure", "authorization failed", "authorization required",
		"not authorized", "unauthorized", "requires approval", "approval required", "tool authorization", "blocked",
	}
	for _, marker := range blockedMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (d *Dispatcher) finishPending(taskID string, status TaskStatus, reason string, closeChannel bool) {
	_, _ = d.finishPendingWithRemote(context.Background(), taskID, status, reason, closeChannel, nil)
}

func (d *Dispatcher) finishPendingWithRemote(ctx context.Context, taskID string, status TaskStatus, reason string, closeChannel bool, remoteCancel RemoteCancelFunc) (bool, error) {
	d.mu.Lock()
	pending, ok := d.pending[taskID]
	if ok {
		delete(d.pending, taskID)
		if closeChannel {
			close(pending.ch)
		}
	}
	d.mu.Unlock()
	if !ok {
		return false, nil
	}

	var cancelErr error
	if remoteCancel != nil && pending.worker != "" {
		pending.cancelOnce.Do(func() {
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			cancelErr = remoteCancel(cancelCtx, pending.worker, taskID, reason)
		})
	}
	now := d.now()
	delivery := DeliveryFailed
	if status == TaskStatusCancelled {
		delivery = DeliveryNotApplicable
	}
	_ = d.store.Update(context.Background(), taskID, TaskPatch{Status: taskStatusPtr(status), DeliveryStatus: deliveryStatusPtr(delivery), EndedAt: timePtrPtr(&now), LastEventAt: timePtrPtr(&now), Error: stringPtr(reason), TerminalSummary: stringPtr(reason)})
	return true, cancelErr
}

// CancelRemote terminates local pending state and sends at most one remote
// cancellation event for taskID.
func (d *Dispatcher) CancelRemote(ctx context.Context, taskID, reason string, remoteCancel RemoteCancelFunc) (bool, error) {
	if strings.TrimSpace(reason) == "" {
		reason = "cancelled"
	}
	return d.finishPendingWithRemote(ctx, taskID, TaskStatusCancelled, reason, true, remoteCancel)
}

// BindExecution registers the cancel function for an inbound worker turn. The
// returned cleanup only removes the same binding, so a reused task ID cannot
// accidentally unbind a newer execution.
func (d *Dispatcher) BindExecution(taskID, ownerPubKey string, cancel context.CancelCauseFunc) func() {
	taskID = strings.TrimSpace(taskID)
	ownerPubKey = strings.TrimSpace(ownerPubKey)
	if taskID == "" || cancel == nil {
		return func() {}
	}
	entry := &executionCancellation{owner: ownerPubKey, cancel: cancel}
	d.mu.Lock()
	if d.executions == nil {
		d.executions = make(map[string]*executionCancellation)
	}
	d.executions[taskID] = entry
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		if d.executions[taskID] == entry {
			delete(d.executions, taskID)
		}
		d.mu.Unlock()
	}
}

// CancelExecution interrupts an inbound worker turn only when the cancellation
// sender matches the requester that created it. Deleting before invoking cancel
// makes duplicate cancel events idempotent.
func (d *Dispatcher) CancelExecution(taskID, requesterPubKey, reason string) bool {
	taskID = strings.TrimSpace(taskID)
	requesterPubKey = strings.TrimSpace(requesterPubKey)
	d.mu.Lock()
	entry, ok := d.executions[taskID]
	if ok && entry.owner != "" && entry.owner != requesterPubKey {
		ok = false
	}
	if ok {
		delete(d.executions, taskID)
	}
	d.mu.Unlock()
	if !ok {
		return false
	}
	if strings.TrimSpace(reason) == "" {
		reason = "remote requester cancelled task"
	}
	entry.cancel(errors.New(reason))
	return true
}

// PendingCount returns the number of in-flight tasks.
func (d *Dispatcher) PendingCount() int {
	d.mu.Lock()
	n := len(d.pending)
	d.mu.Unlock()
	return n
}

// HasPending reports whether taskID currently has a waiting dispatcher slot.
func (d *Dispatcher) HasPending(taskID string) bool {
	d.mu.Lock()
	_, ok := d.pending[taskID]
	d.mu.Unlock()
	return ok
}

// Wait blocks until the result for taskID arrives or the context expires.
func (d *Dispatcher) Wait(ctx context.Context, taskID string, timeout time.Duration) (TaskResult, error) {
	return d.WaitWithRemoteCancel(ctx, taskID, timeout, nil)
}

// WaitWithRemoteCancel is Wait plus one-shot remote worker cancellation on
// context cancellation or timeout.
func (d *Dispatcher) WaitWithRemoteCancel(ctx context.Context, taskID string, timeout time.Duration, remoteCancel RemoteCancelFunc) (TaskResult, error) {
	d.mu.Lock()
	pending, ok := d.pending[taskID]
	d.mu.Unlock()
	if !ok {
		return TaskResult{}, fmt.Errorf("acp dispatcher: no pending task %q", taskID)
	}

	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}

	select {
	case res, ok := <-pending.ch:
		if !ok {
			return TaskResult{}, fmt.Errorf("acp dispatcher: task %q cancelled", taskID)
		}
		return res, nil
	case <-ctx.Done():
		_, cancelErr := d.finishPendingWithRemote(ctx, taskID, TaskStatusCancelled, ctx.Err().Error(), true, remoteCancel)
		if cancelErr != nil {
			return TaskResult{}, errors.Join(ctx.Err(), fmt.Errorf("remote cancel: %w", cancelErr))
		}
		return TaskResult{}, ctx.Err()
	case <-timer:
		reason := fmt.Sprintf("acp dispatcher: task %q timed out after %v", taskID, timeout)
		_, cancelErr := d.finishPendingWithRemote(ctx, taskID, TaskStatusTimedOut, reason, true, remoteCancel)
		timeoutErr := errors.New(reason)
		if cancelErr != nil {
			return TaskResult{}, errors.Join(timeoutErr, fmt.Errorf("remote cancel: %w", cancelErr))
		}
		return TaskResult{}, timeoutErr
	}
}
