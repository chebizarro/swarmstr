package acp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func TestDispatcher_RegisterAndDeliver(t *testing.T) {
	d := NewDispatcher()
	taskID := "t1"
	ch := d.Register(taskID)

	go func() {
		time.Sleep(10 * time.Millisecond)
		d.Deliver(TaskResult{TaskID: taskID, Text: "done"})
	}()

	result, err := d.Wait(context.Background(), taskID, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "done" {
		t.Fatalf("expected 'done', got %q", result.Text)
	}
	_ = ch
}

func TestDispatcher_DeliveryBeforeWaitWinsTerminalRace(t *testing.T) {
	d := NewDispatcher()
	taskID := "delivered-before-wait"
	d.Register(taskID)
	if !d.Deliver(TaskResult{TaskID: taskID, Text: "done"}) {
		t.Fatal("delivery was not accepted")
	}
	result, err := d.Wait(context.Background(), taskID, time.Hour)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Text != "done" {
		t.Fatalf("result text = %q", result.Text)
	}
	if d.PendingCount() != 0 {
		t.Fatalf("pending count = %d, want 0", d.PendingCount())
	}
}

func TestDispatcher_Timeout(t *testing.T) {
	d := NewDispatcher()
	taskID := "t2"
	d.Register(taskID)
	_, err := d.Wait(context.Background(), taskID, 30*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout does not wrap DeadlineExceeded: %v", err)
	}
	var timeoutErr AcpError
	if !errors.As(err, &timeoutErr) || timeoutErr.DetailCode != AcpDetailCodeTurnTimeout {
		t.Fatalf("timeout shape = %+v", err)
	}
}

func TestDispatcher_ContextCancel(t *testing.T) {
	d := NewDispatcher()
	taskID := "t3"
	d.Register(taskID)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := d.Wait(ctx, taskID, 5*time.Second)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestDispatcher_Cancel(t *testing.T) {
	d := NewDispatcher()
	taskID := "t4"
	d.Register(taskID)
	d.Cancel(taskID)
	if d.PendingCount() != 0 {
		t.Fatal("expected no pending tasks after cancel")
	}
}

func TestDispatcher_PendingCount(t *testing.T) {
	d := NewDispatcher()
	if d.PendingCount() != 0 {
		t.Fatal("expected 0 pending initially")
	}
	d.Register("x1")
	d.Register("x2")
	if d.PendingCount() != 2 {
		t.Fatalf("expected 2 pending, got %d", d.PendingCount())
	}
	d.Deliver(TaskResult{TaskID: "x1", Text: "ok"})
	// Allow the delivery goroutine to run.
	time.Sleep(5 * time.Millisecond)
	if d.PendingCount() != 1 {
		t.Fatalf("expected 1 pending after deliver, got %d", d.PendingCount())
	}
}

func TestDispatcher_UnknownTask(t *testing.T) {
	d := NewDispatcher()
	_, err := d.Wait(context.Background(), "nonexistent", time.Second)
	if err == nil {
		t.Fatal("expected error for unknown task")
	}
}

func TestPipeline_Sequential(t *testing.T) {
	d := NewDispatcher()
	var capturedTaskIDs []string

	sendFn := func(ctx context.Context, peerPubKey, taskID string, payload TaskPayload) error {
		capturedTaskIDs = append(capturedTaskIDs, taskID)
		if payload.Instructions == "" {
			t.Fatal("expected instructions in payload")
		}
		// Simulate async result delivery.
		go func() {
			time.Sleep(5 * time.Millisecond)
			d.Deliver(TaskResult{TaskID: taskID, Text: "result-" + peerPubKey})
		}()
		return nil
	}

	p := &Pipeline{Steps: []Step{
		{PeerPubKey: "peer1", Instructions: "task1"},
		{PeerPubKey: "peer2", Instructions: "task2"},
	}}

	results, err := p.RunSequential(context.Background(), d, sendFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Text != "result-peer1" {
		t.Fatalf("step 0 text: %q", results[0].Text)
	}
	if results[1].Text != "result-peer2" {
		t.Fatalf("step 1 text: %q", results[1].Text)
	}
}

func TestPipeline_Parallel(t *testing.T) {
	d := NewDispatcher()

	sendFn := func(ctx context.Context, peerPubKey, taskID string, payload TaskPayload) error {
		if payload.Instructions == "" {
			t.Fatal("expected instructions in payload")
		}
		go func() {
			time.Sleep(10 * time.Millisecond)
			d.Deliver(TaskResult{TaskID: taskID, Text: "par-" + peerPubKey})
		}()
		return nil
	}

	p := &Pipeline{Steps: []Step{
		{PeerPubKey: "pa", Instructions: "a"},
		{PeerPubKey: "pb", Instructions: "b"},
		{PeerPubKey: "pc", Instructions: "c"},
	}}

	results, err := p.RunParallel(context.Background(), d, sendFn)
	if err != nil {
		t.Fatalf("unexpected parallel error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
}

func TestPipeline_Parallel_PropagatesWorkerError(t *testing.T) {
	d := NewDispatcher()
	sendFn := func(ctx context.Context, peerPubKey, taskID string, payload TaskPayload) error {
		go func() {
			time.Sleep(5 * time.Millisecond)
			d.Deliver(TaskResult{TaskID: taskID, Text: "", Error: "worker failed"})
		}()
		return nil
	}

	p := &Pipeline{Steps: []Step{{PeerPubKey: "p1", Instructions: "a"}}}
	results, err := p.RunParallel(context.Background(), d, sendFn)
	if err == nil {
		t.Fatal("expected worker error from parallel pipeline")
	}
	if len(results) != 1 || results[0].Error != "worker failed" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestPipeline_Parallel_SendFailureCancelsDispatched(t *testing.T) {
	d := NewDispatcher()
	var callCount atomic.Int32

	sendFn := func(ctx context.Context, peerPubKey, taskID string, payload TaskPayload) error {
		if callCount.Add(1) == 2 {
			return context.DeadlineExceeded
		}
		return nil
	}

	p := &Pipeline{Steps: []Step{
		{PeerPubKey: "p1", Instructions: "a"},
		{PeerPubKey: "p2", Instructions: "b"},
		{PeerPubKey: "p3", Instructions: "c"},
	}}

	_, err := p.RunParallel(context.Background(), d, sendFn)
	if err == nil {
		t.Fatal("expected send failure")
	}
	if d.PendingCount() != 0 {
		t.Fatalf("expected all dispatched tasks cancelled, pending=%d", d.PendingCount())
	}
}

func TestPipeline_Sequential_PreservesRuntimeHints(t *testing.T) {
	d := NewDispatcher()
	var got TaskPayload

	sendFn := func(ctx context.Context, peerPubKey, taskID string, payload TaskPayload) error {
		got = payload
		go func() {
			time.Sleep(5 * time.Millisecond)
			d.Deliver(TaskResult{TaskID: taskID, Text: "ok"})
		}()
		return nil
	}

	p := &Pipeline{Steps: []Step{{
		PeerPubKey:      "peer1",
		Instructions:    "task1",
		ContextMessages: []map[string]any{{"role": "user", "content": "prior"}},
		MemoryScope:     state.AgentMemoryScopeProject,
		ToolProfile:     "coding",
		EnabledTools:    []string{"memory_search", "session_spawn"},
		ParentContext:   &ParentContext{SessionID: "session-a", AgentID: "main"},
	}}}

	if _, err := p.RunSequential(context.Background(), d, sendFn); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MemoryScope != state.AgentMemoryScopeProject {
		t.Fatalf("expected memory scope to propagate, got %#v", got.MemoryScope)
	}
	if got.ToolProfile != "coding" {
		t.Fatalf("expected tool profile to propagate, got %#v", got.ToolProfile)
	}
	if len(got.EnabledTools) != 2 || got.EnabledTools[0] != "memory_search" {
		t.Fatalf("expected enabled tools to propagate, got %#v", got.EnabledTools)
	}
	if got.ParentContext == nil || got.ParentContext.SessionID != "session-a" || got.ParentContext.AgentID != "main" {
		t.Fatalf("expected parent context to propagate, got %#v", got.ParentContext)
	}
	if len(got.ContextMessages) != 1 || got.ContextMessages[0]["content"] != "prior" {
		t.Fatalf("expected context messages to propagate, got %#v", got.ContextMessages)
	}
}

func TestPipeline_Sequential_PreservesWorkerMetadata(t *testing.T) {
	d := NewDispatcher()
	sendFn := func(ctx context.Context, peerPubKey, taskID string, payload TaskPayload) error {
		go func() {
			time.Sleep(5 * time.Millisecond)
			d.Deliver(TaskResult{
				TaskID:       taskID,
				Text:         "ok",
				SenderPubKey: peerPubKey,
				Worker: &WorkerMetadata{
					SessionID:       "acp:" + peerPubKey,
					AgentID:         "worker",
					HistoryEntryIDs: []string{"acp:task:seed:0", "turn:task:assistant:0"},
				},
			})
		}()
		return nil
	}

	p := &Pipeline{Steps: []Step{{PeerPubKey: "peer1", Instructions: "task1"}}}
	results, err := p.RunSequential(context.Background(), d, sendFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].SenderPubKey != "peer1" {
		t.Fatalf("sender_pubkey = %q, want peer1", results[0].SenderPubKey)
	}
	if results[0].Worker == nil || results[0].Worker.SessionID != "acp:peer1" || len(results[0].Worker.HistoryEntryIDs) != 2 {
		t.Fatalf("worker metadata = %#v", results[0].Worker)
	}
}

func TestDispatcher_WaitCancellationSendsRemoteCancelOnce(t *testing.T) {
	d := NewDispatcher()
	taskID := "remote-cancel"
	if _, err := d.RegisterTaskWithError(context.Background(), TaskRecord{
		TaskID: taskID,
		Worker: &WorkerTaskMetadata{PubKey: "worker-a"},
	}); err != nil {
		t.Fatalf("register task: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	remoteCancel := func(_ context.Context, peerPubKey, gotTaskID, reason string) error {
		calls.Add(1)
		if peerPubKey != "worker-a" || gotTaskID != taskID {
			t.Fatalf("remote cancel target = %q/%q", peerPubKey, gotTaskID)
		}
		if reason != context.Canceled.Error() {
			t.Fatalf("remote cancel reason = %q", reason)
		}
		return nil
	}
	if _, err := d.WaitWithRemoteCancel(ctx, taskID, time.Hour, remoteCancel); err == nil {
		t.Fatal("expected cancellation error")
	}
	if cancelled, err := d.CancelRemote(context.Background(), taskID, "duplicate", remoteCancel); err != nil || cancelled {
		t.Fatalf("duplicate CancelRemote = (%v, %v), want (false, nil)", cancelled, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("remote cancel calls = %d, want 1", got)
	}
}

func TestDispatcher_CancelRemoteConcurrentDeduplicates(t *testing.T) {
	d := NewDispatcher()
	taskID := "concurrent-cancel"
	if _, err := d.RegisterTaskWithError(context.Background(), TaskRecord{
		TaskID: taskID,
		Worker: &WorkerTaskMetadata{PubKey: "worker-a"},
	}); err != nil {
		t.Fatalf("register task: %v", err)
	}
	var calls atomic.Int32
	remoteCancel := func(context.Context, string, string, string) error {
		calls.Add(1)
		return nil
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = d.CancelRemote(context.Background(), taskID, "stop", remoteCancel)
		}()
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("remote cancel calls = %d, want 1", got)
	}
}

func TestDispatcher_CancelExecutionAuthenticatesRequester(t *testing.T) {
	d := NewDispatcher()
	ctx, cancel := context.WithCancelCause(context.Background())
	release := d.BindExecution("worker-task", "requester-a", cancel)
	defer release()

	if d.CancelExecution("worker-task", "requester-b", "forged") {
		t.Fatal("forged requester cancelled execution")
	}
	if context.Cause(ctx) != nil {
		t.Fatalf("forged cancellation changed cause: %v", context.Cause(ctx))
	}
	if !d.CancelExecution("worker-task", "requester-a", "requested stop") {
		t.Fatal("authorized requester did not cancel execution")
	}
	if got := context.Cause(ctx); got == nil || got.Error() != "requested stop" {
		t.Fatalf("cancellation cause = %v", got)
	}
	if d.CancelExecution("worker-task", "requester-a", "duplicate") {
		t.Fatal("duplicate cancellation was not idempotent")
	}
}

func TestPipeline_ParallelBoundsConcurrency(t *testing.T) {
	d := NewDispatcher()
	type startedTask struct {
		peer   string
		taskID string
	}
	started := make(chan startedTask, 4)
	sendFn := func(_ context.Context, peerPubKey, taskID string, _ TaskPayload) error {
		started <- startedTask{peer: peerPubKey, taskID: taskID}
		return nil
	}
	p := &Pipeline{MaxConcurrency: 2, Steps: []Step{
		{PeerPubKey: "p1", Instructions: "a"},
		{PeerPubKey: "p2", Instructions: "b"},
		{PeerPubKey: "p3", Instructions: "c"},
		{PeerPubKey: "p4", Instructions: "d"},
	}}
	type outcome struct {
		results []PipelineResult
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		results, err := p.RunParallel(context.Background(), d, sendFn)
		done <- outcome{results: results, err: err}
	}()

	first := <-started
	second := <-started
	select {
	case extra := <-started:
		t.Fatalf("step %q started above concurrency limit", extra.peer)
	default:
	}
	d.Deliver(TaskResult{TaskID: first.taskID, Text: first.peer})
	d.Deliver(TaskResult{TaskID: second.taskID, Text: second.peer})
	third := <-started
	fourth := <-started
	d.Deliver(TaskResult{TaskID: third.taskID, Text: third.peer})
	d.Deliver(TaskResult{TaskID: fourth.taskID, Text: fourth.peer})

	got := <-done
	if got.err != nil {
		t.Fatalf("RunParallel: %v", got.err)
	}
	if len(got.results) != 4 {
		t.Fatalf("result count = %d, want 4", len(got.results))
	}
}

func TestPipeline_ParallelFailureCancelsActiveSibling(t *testing.T) {
	d := NewDispatcher()
	type startedTask struct {
		peer   string
		taskID string
	}
	started := make(chan startedTask, 2)
	cancelled := make(chan startedTask, 2)
	p := &Pipeline{
		MaxConcurrency: 2,
		Steps: []Step{
			{PeerPubKey: "p1", Instructions: "a"},
			{PeerPubKey: "p2", Instructions: "b"},
		},
		RemoteCancel: func(_ context.Context, peerPubKey, taskID, _ string) error {
			cancelled <- startedTask{peer: peerPubKey, taskID: taskID}
			return nil
		},
	}
	sendFn := func(_ context.Context, peerPubKey, taskID string, _ TaskPayload) error {
		started <- startedTask{peer: peerPubKey, taskID: taskID}
		return nil
	}
	type outcome struct {
		results []PipelineResult
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		results, err := p.RunParallel(context.Background(), d, sendFn)
		done <- outcome{results: results, err: err}
	}()

	failed := <-started
	sibling := <-started
	d.Deliver(TaskResult{TaskID: failed.taskID, Error: "worker failed"})
	gotCancel := <-cancelled
	if gotCancel != sibling {
		t.Fatalf("cancelled task = %+v, want active sibling %+v", gotCancel, sibling)
	}
	got := <-done
	if got.err == nil {
		t.Fatal("expected pipeline failure")
	}
	if len(got.results) != 2 {
		t.Fatalf("result count = %d, want 2", len(got.results))
	}
}

func TestAggregateResults(t *testing.T) {
	results := []PipelineResult{
		{Text: "hello"},
		{Text: "", Error: "failed"},
		{Text: "world"},
	}
	got := AggregateResults(results)
	if got != "hello\n\nworld" {
		t.Fatalf("unexpected aggregate: %q", got)
	}
}
