package main

import (
	"context"
	"errors"
	"testing"

	"metiq/internal/agent"
	suspendpkg "metiq/internal/gateway/suspend"
)

type suspendBlockingRuntime struct {
	started chan struct{}
	release chan struct{}
}

func (r *suspendBlockingRuntime) ProcessTurn(context.Context, agent.Turn) (agent.TurnResult, error) {
	close(r.started)
	<-r.release
	return agent.TurnResult{}, nil
}

func TestSuspendAdmissionRuntimeDrainsInFlightTurnWithoutPolling(t *testing.T) {
	coordinator := suspendpkg.NewCoordinator()
	base := &suspendBlockingRuntime{started: make(chan struct{}), release: make(chan struct{})}
	wrapped := wrapRuntimeForSuspend(base, coordinator)
	result := make(chan error, 1)
	go func() {
		_, err := wrapped.ProcessTurn(context.Background(), agent.Turn{})
		result <- err
	}()
	<-base.started
	if got := coordinator.ActiveWork(); got != 1 {
		t.Fatalf("active leases=%d, want 1", got)
	}

	rec, err := coordinator.Prepare("maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != suspendpkg.StatePreparing {
		t.Fatalf("prepare state=%q, want preparing", rec.State)
	}
	if _, err := wrapped.ProcessTurn(context.Background(), agent.Turn{}); !errors.Is(err, suspendpkg.ErrAdmissionClosed) {
		t.Fatalf("new turn error=%v, want admission closed", err)
	}

	close(base.release)
	if err := <-result; err != nil {
		t.Fatalf("in-flight turn: %v", err)
	}
	if rec := coordinator.State(); rec.State != suspendpkg.StateSuspended {
		t.Fatalf("state after drain=%q, want suspended", rec.State)
	}
	if got := coordinator.ActiveWork(); got != 0 {
		t.Fatalf("active leases after drain=%d", got)
	}
}

type suspendStreamingRuntime struct {
	streamCalls int
}

func (r *suspendStreamingRuntime) ProcessTurn(context.Context, agent.Turn) (agent.TurnResult, error) {
	return agent.TurnResult{}, errors.New("non-streaming path called")
}

func (r *suspendStreamingRuntime) ProcessTurnStreaming(_ context.Context, _ agent.Turn, onChunk func(string)) (agent.TurnResult, error) {
	r.streamCalls++
	onChunk("chunk")
	return agent.TurnResult{}, nil
}

func TestSuspendAdmissionRuntimePreservesStreaming(t *testing.T) {
	coordinator := suspendpkg.NewCoordinator()
	base := &suspendStreamingRuntime{}
	wrapped := wrapRuntimeForSuspend(base, coordinator)
	streaming, ok := wrapped.(agent.StreamingRuntime)
	if !ok {
		t.Fatal("wrapped runtime lost streaming interface")
	}
	var chunk string
	if _, err := streaming.ProcessTurnStreaming(context.Background(), agent.Turn{}, func(value string) { chunk = value }); err != nil {
		t.Fatal(err)
	}
	if base.streamCalls != 1 || chunk != "chunk" {
		t.Fatalf("stream calls=%d chunk=%q", base.streamCalls, chunk)
	}
	if coordinator.ActiveWork() != 0 {
		t.Fatalf("streaming lease was not released: %d", coordinator.ActiveWork())
	}
}
