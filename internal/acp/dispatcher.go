package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// TaskResult carries the result of a dispatched ACP task.
type TaskResult struct {
	TaskID string
	Text   string
	Error  string
}

// Dispatcher manages in-flight ACP task dispatches.
// The director calls Dispatch() to send a task and block until the result
// arrives; the receiver calls Deliver() when a result DM comes in.
type Dispatcher struct {
	mu      sync.Mutex
	pending map[string]chan TaskResult
}

// NewDispatcher returns a ready-to-use Dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{pending: make(map[string]chan TaskResult)}
}

// GenerateTaskID returns a random hex task ID.
func GenerateTaskID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "task-" + hex.EncodeToString(b)
}

// Register reserves a slot for an in-flight task and returns the channel
// on which the caller should wait.  The channel is buffered (capacity 1).
func (d *Dispatcher) Register(taskID string) chan TaskResult {
	ch := make(chan TaskResult, 1)
	d.mu.Lock()
	d.pending[taskID] = ch
	d.mu.Unlock()
	return ch
}

// Deliver routes a TaskResult to the waiting goroutine.
// Returns true if the task was pending and the result was delivered.
func (d *Dispatcher) Deliver(result TaskResult) bool {
	d.mu.Lock()
	ch, ok := d.pending[result.TaskID]
	if ok {
		delete(d.pending, result.TaskID)
	}
	d.mu.Unlock()
	if ok {
		ch <- result
		return true
	}
	return false
}

// Cancel removes a pending task and closes its channel (waking any waiter with
// a zero TaskResult).
func (d *Dispatcher) Cancel(taskID string) {
	d.mu.Lock()
	ch, ok := d.pending[taskID]
	if ok {
		delete(d.pending, taskID)
		close(ch)
	}
	d.mu.Unlock()
}

// PendingCount returns the number of in-flight tasks.
func (d *Dispatcher) PendingCount() int {
	d.mu.Lock()
	n := len(d.pending)
	d.mu.Unlock()
	return n
}

// Wait blocks until the result for taskID arrives or the context expires.
func (d *Dispatcher) Wait(ctx context.Context, taskID string, timeout time.Duration) (TaskResult, error) {
	d.mu.Lock()
	ch, ok := d.pending[taskID]
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
	case res, ok := <-ch:
		if !ok {
			return TaskResult{}, fmt.Errorf("acp dispatcher: task %q cancelled", taskID)
		}
		return res, nil
	case <-ctx.Done():
		d.Cancel(taskID)
		return TaskResult{}, ctx.Err()
	case <-timer:
		d.Cancel(taskID)
		return TaskResult{}, fmt.Errorf("acp dispatcher: task %q timed out after %v", taskID, timeout)
	}
}
