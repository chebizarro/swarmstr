package acp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type failoverTestRuntime struct {
	mu        sync.Mutex
	id        string
	ensureErr error
	runErr    error
	events    []RuntimeEvent
	blockRun  bool
	started   chan struct{}
	startOnce sync.Once
	ensures   int
	runs      int
	cancels   int
	closes    int
}

func (r *failoverTestRuntime) EnsureSession(_ context.Context, input EnsureInput) (RuntimeHandle, error) {
	r.mu.Lock()
	r.ensures++
	err := r.ensureErr
	r.mu.Unlock()
	if err != nil {
		return RuntimeHandle{}, err
	}
	return RuntimeHandle{SessionKey: input.SessionKey, Backend: r.id, RuntimeSessionName: r.id + "-" + input.SessionKey}, nil
}

func (r *failoverTestRuntime) RunTurn(ctx context.Context, _ TurnInput) (<-chan RuntimeEvent, error) {
	r.mu.Lock()
	r.runs++
	err := r.runErr
	events := append([]RuntimeEvent(nil), r.events...)
	block := r.blockRun
	started := r.started
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	ch := make(chan RuntimeEvent, len(events)+1)
	go func() {
		defer close(ch)
		if started != nil {
			r.startOnce.Do(func() { close(started) })
		}
		if block {
			<-ctx.Done()
			return
		}
		for _, event := range events {
			ch <- event
		}
	}()
	return ch, nil
}

func (r *failoverTestRuntime) Cancel(context.Context, CancelInput) error {
	r.mu.Lock()
	r.cancels++
	r.mu.Unlock()
	return nil
}

func (r *failoverTestRuntime) Close(context.Context, CloseInput) error {
	r.mu.Lock()
	r.closes++
	r.mu.Unlock()
	return nil
}

func (r *failoverTestRuntime) counts() (ensures, runs, cancels, closes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ensures, r.runs, r.cancels, r.closes
}

func newFailoverTestManager(t *testing.T, primary, fallback *failoverTestRuntime, opts ManagerOptions) *Manager {
	t.Helper()
	store, err := NewFileSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	registry := NewBackendRegistry()
	if err := registry.Register(BackendEntry{ID: primary.id, Runtime: primary}); err != nil {
		t.Fatalf("register primary: %v", err)
	}
	if fallback != nil {
		if err := registry.Register(BackendEntry{ID: fallback.id, Runtime: fallback}); err != nil {
			t.Fatalf("register fallback: %v", err)
		}
	}
	return NewManager(registry, store, nil, nil, opts)
}

func TestResolveBackendCandidatePlanDeduplicatesOrder(t *testing.T) {
	got := resolveBackendCandidatePlan("Primary", "resolved", []string{"primary", "Fallback", "fallback", ""})
	want := []string{"primary", "fallback"}
	if len(got) != len(want) {
		t.Fatalf("candidate plan = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerBackendFailoverAfterEarlyTransientTurnFailure(t *testing.T) {
	primary := &failoverTestRuntime{id: "primary", runErr: errors.New("backend temporarily overloaded")}
	fallback := &failoverTestRuntime{id: "fallback", events: []RuntimeEvent{{Kind: EventDone, StopReason: "complete"}}}
	mgr := newFailoverTestManager(t, primary, fallback, ManagerOptions{FallbackBackends: []string{"fallback"}})
	ctx := context.Background()
	if _, err := mgr.InitializeSession(ctx, InitializeSessionInput{SessionKey: "failover-success", Backend: "primary"}); err != nil {
		t.Fatal(err)
	}
	events, err := mgr.RunTurn(ctx, RunSessionTurnInput{SessionKey: "failover-success", Text: "hello"})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventDone {
		t.Fatalf("fallback events = %+v", events)
	}
	_, primaryRuns, _, primaryCloses := primary.counts()
	_, fallbackRuns, _, _ := fallback.counts()
	if primaryRuns != 1 || fallbackRuns != 1 || primaryCloses != 1 {
		t.Fatalf("primary runs/closes=%d/%d fallback runs=%d", primaryRuns, primaryCloses, fallbackRuns)
	}
}

func TestManagerBackendFailoverFromInitializationFailure(t *testing.T) {
	primary := &failoverTestRuntime{id: "primary", ensureErr: errors.New("backend unavailable")}
	fallback := &failoverTestRuntime{id: "fallback", events: []RuntimeEvent{{Kind: EventDone, StopReason: "complete"}}}
	mgr := newFailoverTestManager(t, primary, fallback, ManagerOptions{})
	events, err := mgr.RunTurn(context.Background(), RunSessionTurnInput{
		SessionKey:       "failover-init",
		Backend:          "primary",
		FallbackBackends: []string{"fallback"},
		Text:             "hello",
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventDone {
		t.Fatalf("fallback events = %+v", events)
	}
	primaryEnsures, _, _, _ := primary.counts()
	fallbackEnsures, fallbackRuns, _, _ := fallback.counts()
	if primaryEnsures != 1 || fallbackEnsures != 1 || fallbackRuns != 1 {
		t.Fatalf("ensure/run counts primary=%d fallback=%d/%d", primaryEnsures, fallbackEnsures, fallbackRuns)
	}
}

func TestManagerBackendFailoverExhaustionRetainsAttempts(t *testing.T) {
	primary := &failoverTestRuntime{id: "primary", runErr: errors.New("backend temporarily unavailable")}
	fallback := &failoverTestRuntime{id: "fallback", runErr: errors.New("quota exhausted")}
	mgr := newFailoverTestManager(t, primary, fallback, ManagerOptions{FallbackBackends: []string{"fallback"}})
	ctx := context.Background()
	if _, err := mgr.InitializeSession(ctx, InitializeSessionInput{SessionKey: "failover-exhausted", Backend: "primary"}); err != nil {
		t.Fatal(err)
	}
	_, err := mgr.RunTurn(ctx, RunSessionTurnInput{SessionKey: "failover-exhausted", Text: "hello"})
	var exhausted BackendFailoverError
	if !errors.As(err, &exhausted) {
		t.Fatalf("RunTurn err = %T %v", err, err)
	}
	if len(exhausted.Attempts) != 2 || exhausted.Attempts[0].Backend != "primary" || exhausted.Attempts[1].Backend != "fallback" {
		t.Fatalf("attempts = %+v", exhausted.Attempts)
	}
	for _, attempt := range exhausted.Attempts {
		if attempt.Code != AcpCodeTurnFailed || attempt.Error == "" {
			t.Fatalf("incomplete attempt = %+v", attempt)
		}
	}
}

func TestManagerBackendFailoverStopsOnNonTransientFailure(t *testing.T) {
	primary := &failoverTestRuntime{id: "primary", runErr: errors.New("invalid backend configuration")}
	fallback := &failoverTestRuntime{id: "fallback", events: []RuntimeEvent{{Kind: EventDone, StopReason: "complete"}}}
	mgr := newFailoverTestManager(t, primary, fallback, ManagerOptions{FallbackBackends: []string{"fallback"}})
	ctx := context.Background()
	if _, err := mgr.InitializeSession(ctx, InitializeSessionInput{SessionKey: "failover-nontransient", Backend: "primary"}); err != nil {
		t.Fatal(err)
	}
	_, err := mgr.RunTurn(ctx, RunSessionTurnInput{SessionKey: "failover-nontransient", Text: "hello"})
	var stopped BackendFailoverError
	if !errors.As(err, &stopped) || len(stopped.Attempts) != 1 {
		t.Fatalf("RunTurn err/attempts = %v / %+v", err, stopped.Attempts)
	}
	_, fallbackRuns, _, _ := fallback.counts()
	if fallbackRuns != 0 {
		t.Fatalf("fallback runs = %d, want 0 for non-transient failure", fallbackRuns)
	}
}

func TestManagerBackendFailoverStopsAfterOutput(t *testing.T) {
	primary := &failoverTestRuntime{id: "primary", events: []RuntimeEvent{
		{Kind: EventTextDelta, Text: "partial", Stream: "output"},
		{Kind: EventError, Text: "backend temporarily overloaded", Code: "OVERLOADED", Retryable: true},
	}}
	fallback := &failoverTestRuntime{id: "fallback", events: []RuntimeEvent{{Kind: EventDone, StopReason: "complete"}}}
	mgr := newFailoverTestManager(t, primary, fallback, ManagerOptions{FallbackBackends: []string{"fallback"}})
	ctx := context.Background()
	if _, err := mgr.InitializeSession(ctx, InitializeSessionInput{SessionKey: "failover-output", Backend: "primary"}); err != nil {
		t.Fatal(err)
	}
	events, err := mgr.RunTurn(ctx, RunSessionTurnInput{SessionKey: "failover-output", Text: "hello"})
	var stopped BackendFailoverError
	if !errors.As(err, &stopped) || len(stopped.Attempts) != 1 || !stopped.Attempts[0].SawOutput {
		t.Fatalf("RunTurn err/attempts = %v / %+v", err, stopped.Attempts)
	}
	if len(events) != 2 || events[0].Kind != EventTextDelta {
		t.Fatalf("primary events = %+v", events)
	}
	_, fallbackRuns, _, _ := fallback.counts()
	if fallbackRuns != 0 {
		t.Fatalf("fallback runs = %d, want 0 after output", fallbackRuns)
	}
}

func TestManagerBackendFailoverStopsAfterApprovalRequest(t *testing.T) {
	primary := &failoverTestRuntime{id: "primary", events: []RuntimeEvent{
		{Kind: EventApprovalRequest, ApprovalRequest: &ApprovalRequest{ID: "approval-1", Action: "write", Path: "file.txt"}},
		{Kind: EventError, Text: "backend temporarily overloaded", Code: "OVERLOADED", Retryable: true},
	}}
	fallback := &failoverTestRuntime{id: "fallback", events: []RuntimeEvent{{Kind: EventDone, StopReason: "complete"}}}
	mgr := newFailoverTestManager(t, primary, fallback, ManagerOptions{FallbackBackends: []string{"fallback"}})
	ctx := context.Background()
	if _, err := mgr.InitializeSession(ctx, InitializeSessionInput{SessionKey: "failover-approval", Backend: "primary"}); err != nil {
		t.Fatal(err)
	}
	events, err := mgr.RunTurn(ctx, RunSessionTurnInput{SessionKey: "failover-approval", Text: "hello"})
	var stopped BackendFailoverError
	if !errors.As(err, &stopped) || len(stopped.Attempts) != 1 || !stopped.Attempts[0].SawOutput {
		t.Fatalf("RunTurn err/attempts = %v / %+v", err, stopped.Attempts)
	}
	if len(events) != 2 || events[0].Kind != EventApprovalRequest {
		t.Fatalf("primary events = %+v", events)
	}
	_, fallbackRuns, _, _ := fallback.counts()
	if fallbackRuns != 0 {
		t.Fatalf("fallback runs = %d, want 0 after approval request", fallbackRuns)
	}
}

func TestManagerBackendFailoverStopsOnCancellation(t *testing.T) {
	started := make(chan struct{})
	primary := &failoverTestRuntime{id: "primary", blockRun: true, started: started}
	fallback := &failoverTestRuntime{id: "fallback", events: []RuntimeEvent{{Kind: EventDone, StopReason: "complete"}}}
	mgr := newFailoverTestManager(t, primary, fallback, ManagerOptions{FallbackBackends: []string{"fallback"}, DefaultTurnTimeout: time.Minute})
	if _, err := mgr.InitializeSession(context.Background(), InitializeSessionInput{SessionKey: "failover-cancel", Backend: "primary"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := mgr.RunTurn(ctx, RunSessionTurnInput{SessionKey: "failover-cancel", Text: "hello"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("primary turn did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunTurn err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled turn did not finish")
	}
	_, fallbackRuns, _, _ := fallback.counts()
	if fallbackRuns != 0 {
		t.Fatalf("fallback runs = %d, want 0 on cancellation", fallbackRuns)
	}
}
