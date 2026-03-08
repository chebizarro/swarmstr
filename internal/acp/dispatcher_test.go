package acp

import (
	"context"
	"testing"
	"time"
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

func TestDispatcher_Timeout(t *testing.T) {
	d := NewDispatcher()
	taskID := "t2"
	d.Register(taskID)
	_, err := d.Wait(context.Background(), taskID, 30*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
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

	sendFn := func(ctx context.Context, peerPubKey, instructions, taskID string) error {
		capturedTaskIDs = append(capturedTaskIDs, taskID)
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

	sendFn := func(ctx context.Context, peerPubKey, instructions, taskID string) error {
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

func TestPipeline_Parallel_SendFailureCancelsDispatched(t *testing.T) {
	d := NewDispatcher()
	callCount := 0

	sendFn := func(ctx context.Context, peerPubKey, instructions, taskID string) error {
		callCount++
		if callCount == 2 {
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
