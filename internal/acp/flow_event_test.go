package acp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestFlowRegistryRevisionAndStateMachine(t *testing.T) {
	ctx := context.Background()
	registry := NewFlowRegistry(nil)
	rec, err := registry.Create(ctx, FlowRecord{FlowID: "flow-a", OwnerSessionKey: "owner", Goal: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.Status != FlowStatusQueued || rec.Revision != 0 {
		t.Fatalf("unexpected initial flow: %+v", rec)
	}
	running, err := registry.Start(ctx, "flow-a", 1)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if running.Status != FlowStatusRunning || running.CurrentStep != 1 || running.Revision != 1 {
		t.Fatalf("unexpected running flow: %+v", running)
	}
	stale := int64(0)
	_, err = registry.Store().Update(ctx, "flow-a", FlowPatch{ExpectedRevision: &stale, Status: flowStatusPtr(FlowStatusWaiting)})
	if err == nil {
		t.Fatal("expected stale revision conflict")
	}
	waitJSON := json.RawMessage(`{"reason":"approval"}`)
	waiting, err := registry.SetWaiting(ctx, "flow-a", waitJSON)
	if err != nil {
		t.Fatalf("SetWaiting: %v", err)
	}
	if waiting.Status != FlowStatusWaiting || string(waiting.WaitJSON) != string(waitJSON) {
		t.Fatalf("unexpected waiting flow: %+v", waiting)
	}
	finished, err := registry.Finish(ctx, "flow-a", []ArtifactPayload{{Type: "text", Text: "ok"}})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if finished.Status != FlowStatusSucceeded || finished.EndedAt == nil || len(finished.Artifacts) != 1 {
		t.Fatalf("unexpected finished flow: %+v", finished)
	}
}

func TestEventLedgerClonesApprovalRequest(t *testing.T) {
	ctx := context.Background()
	ledger := NewInMemoryEventLedger(EventLedgerOptions{})
	req := &ApprovalRequest{ID: "approval-1", Action: "write", Metadata: map[string]any{"path": "a"}}
	if err := ledger.RecordEvent(ctx, "s1", "r1", RuntimeEvent{Kind: EventApprovalRequest, ApprovalRequest: req}); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	req.Metadata["path"] = "mutated"
	req.Action = "delete"
	replay, err := ledger.Replay(ctx, "s1")
	if err != nil || len(replay) != 1 {
		t.Fatalf("Replay len=%d err=%v", len(replay), err)
	}
	if replay[0].Event.ApprovalRequest.Action != "write" || replay[0].Event.ApprovalRequest.Metadata["path"] != "a" {
		t.Fatalf("ledger event was mutated: %+v", replay[0].Event.ApprovalRequest)
	}
	replay[0].Event.ApprovalRequest.Metadata["path"] = "replay-mutated"
	again, _ := ledger.Replay(ctx, "s1")
	if again[0].Event.ApprovalRequest.Metadata["path"] != "a" {
		t.Fatalf("replay mutation leaked into ledger: %+v", again[0].Event.ApprovalRequest)
	}
}

func TestEventLedgerReplayAndTrim(t *testing.T) {
	ctx := context.Background()
	ledger := NewInMemoryEventLedger(EventLedgerOptions{MaxSessions: 1, MaxEventsPerSession: 2})
	if err := ledger.StartSession(ctx, "s1", "/repo"); err != nil {
		t.Fatalf("StartSession s1: %v", err)
	}
	_ = ledger.RecordEvent(ctx, "s1", "r1", RuntimeEvent{Kind: EventStatus, Text: "one"})
	_ = ledger.RecordEvent(ctx, "s1", "r1", RuntimeEvent{Kind: EventTextDelta, Text: "two"})
	_ = ledger.RecordEvent(ctx, "s1", "r1", RuntimeEvent{Kind: EventDone, StopReason: "complete"})
	replay, err := ledger.Replay(ctx, "s1")
	if err != nil || len(replay) != 2 {
		t.Fatalf("Replay len=%d err=%v", len(replay), err)
	}
	if replay[0].Event.Text != "two" || replay[1].Event.Kind != EventDone {
		t.Fatalf("unexpected replay: %+v", replay)
	}
	if err := ledger.StartSession(ctx, "s2", ""); err != nil {
		t.Fatalf("StartSession s2: %v", err)
	}
	replay, _ = ledger.Replay(ctx, "s1")
	if len(replay) != 0 {
		t.Fatalf("expected s1 trimmed, got %+v", replay)
	}
}

func TestManagerTimeoutGraceRecordsTerminalEvent(t *testing.T) {
	rt := &graceRuntime{started: make(chan struct{}), cancelCh: make(chan struct{})}
	store, err := NewFileSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	backends := NewBackendRegistry()
	if err := backends.Register(BackendEntry{ID: "test", Runtime: rt}); err != nil {
		t.Fatalf("register backend: %v", err)
	}
	mgr := NewManager(backends, store, nil, nil, ManagerOptions{DefaultTurnTimeout: 20 * time.Millisecond, TurnTimeoutGrace: time.Second, TurnTimeoutCleanupGrace: time.Second, EventLedger: NewInMemoryEventLedger(EventLedgerOptions{})})
	ctx := context.Background()
	if _, err := mgr.InitializeSession(ctx, InitializeSessionInput{SessionKey: "grace", Backend: "test"}); err != nil {
		t.Fatal(err)
	}
	events, err := mgr.RunTurn(ctx, RunSessionTurnInput{SessionKey: "grace", RequestID: "task-grace", Text: "wait"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunTurn err=%v, want DeadlineExceeded", err)
	}
	var timeoutErr AcpError
	if !errors.As(err, &timeoutErr) || timeoutErr.Code != AcpCodeTurnFailed || timeoutErr.DetailCode != AcpDetailCodeTurnTimeout {
		t.Fatalf("RunTurn timeout shape = %+v", err)
	}
	if len(events) == 0 || events[len(events)-1].Kind != EventError {
		t.Fatalf("expected grace terminal error event, got %+v", events)
	}
	obs := mgr.Status(ctx)
	if obs.Counters.TurnsTimedOut != 1 || obs.Counters.TurnsFailed != 0 || obs.EventLedger == nil || obs.EventLedger.Events == 0 {
		t.Fatalf("unexpected status: %+v", obs)
	}
	if obs.ErrorsByCode[AcpDetailCodeTurnTimeout] != 1 {
		t.Fatalf("timeout errors_by_code = %+v", obs.ErrorsByCode)
	}
}

type graceRuntime struct {
	started  chan struct{}
	cancelCh chan struct{}
	once     sync.Once
}

func (r *graceRuntime) EnsureSession(_ context.Context, input EnsureInput) (RuntimeHandle, error) {
	return RuntimeHandle{SessionKey: input.SessionKey, Backend: "test", RuntimeSessionName: "rt-" + input.SessionKey}, nil
}
func (r *graceRuntime) RunTurn(ctx context.Context, input TurnInput) (<-chan RuntimeEvent, error) {
	ch := make(chan RuntimeEvent, 2)
	go func() {
		defer close(ch)
		close(r.started)
		select {
		case <-ctx.Done():
		case <-r.cancelCh:
		}
		ch <- RuntimeEvent{Kind: EventError, Code: "cancelled", Text: "cleaned up"}
	}()
	return ch, nil
}
func (r *graceRuntime) Cancel(_ context.Context, _ CancelInput) error {
	r.once.Do(func() { close(r.cancelCh) })
	return nil
}
func (r *graceRuntime) Close(_ context.Context, _ CloseInput) error { return nil }
