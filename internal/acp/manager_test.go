package acp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type managerTestRuntime struct {
	mu       sync.Mutex
	ensures  int
	runs     int
	cancels  int
	closes   int
	controls int

	blockRun bool
	started  chan struct{}
}

func (r *managerTestRuntime) EnsureSession(_ context.Context, input EnsureInput) (RuntimeHandle, error) {
	r.mu.Lock()
	r.ensures++
	r.mu.Unlock()
	return RuntimeHandle{SessionKey: input.SessionKey, Backend: "test", RuntimeSessionName: "rt-" + input.SessionKey, CWD: input.CWD, AcpxRecordID: "rec-" + input.SessionKey}, nil
}

func (r *managerTestRuntime) RunTurn(ctx context.Context, input TurnInput) (<-chan RuntimeEvent, error) {
	r.mu.Lock()
	r.runs++
	started := r.started
	block := r.blockRun
	r.mu.Unlock()
	ch := make(chan RuntimeEvent, 3)
	go func() {
		defer close(ch)
		if started != nil {
			close(started)
		}
		if block {
			<-ctx.Done()
			return
		}
		ch <- RuntimeEvent{Kind: EventStatus, Text: "running"}
		ch <- RuntimeEvent{Kind: EventTextDelta, Text: input.Text, Stream: "output"}
		ch <- RuntimeEvent{Kind: EventDone, StopReason: "complete"}
	}()
	return ch, nil
}

func (r *managerTestRuntime) Cancel(_ context.Context, _ CancelInput) error {
	r.mu.Lock()
	r.cancels++
	r.mu.Unlock()
	return nil
}

func (r *managerTestRuntime) Close(_ context.Context, _ CloseInput) error {
	r.mu.Lock()
	r.closes++
	r.mu.Unlock()
	return nil
}

func (r *managerTestRuntime) ApplyRuntimeControls(_ context.Context, input RuntimeControlInput) error {
	r.mu.Lock()
	r.controls += len(input.Controls)
	r.mu.Unlock()
	return nil
}

func (r *managerTestRuntime) counts() (ensures, runs, cancels, closes, controls int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ensures, r.runs, r.cancels, r.closes, r.controls
}

func newTestManager(t *testing.T, rt *managerTestRuntime, opts ManagerOptions) (*Manager, *FileSessionStore) {
	t.Helper()
	store, err := NewFileSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	backends := NewBackendRegistry()
	if err := backends.Register(BackendEntry{ID: "test", Runtime: rt}); err != nil {
		t.Fatalf("register backend: %v", err)
	}
	return NewManager(backends, store, nil, nil, opts), store
}

func TestManagerInitializeRunAndStatus(t *testing.T) {
	rt := &managerTestRuntime{}
	mgr, store := newTestManager(t, rt, ManagerOptions{})
	ctx := context.Background()

	handle, err := mgr.InitializeSession(ctx, InitializeSessionInput{
		SessionKey: "sess-1",
		Backend:    "test",
		Agent:      "Planner",
		CWD:        "/workspace",
		Controls:   []RuntimeControl{{Name: "mode", Options: map[string]any{"value": "plan"}}},
	})
	if err != nil {
		t.Fatalf("InitializeSession: %v", err)
	}
	if handle.SessionKey != "sess-1" || handle.Backend != "test" || handle.CWD != "/workspace" {
		t.Fatalf("unexpected handle: %+v", handle)
	}

	events, err := mgr.RunTurn(ctx, RunSessionTurnInput{SessionKey: "sess-1", Text: "hello"})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(events) != 3 || events[2].Kind != EventDone {
		t.Fatalf("unexpected events: %+v", events)
	}
	ensures, runs, _, _, controls := rt.counts()
	if ensures != 1 || runs != 1 || controls != 1 {
		t.Fatalf("counts ensure=%d runs=%d controls=%d", ensures, runs, controls)
	}

	status, err := mgr.GetSessionStatus(ctx, "sess-1")
	if err != nil {
		t.Fatalf("GetSessionStatus: %v", err)
	}
	if !status.Cached || status.ActiveTurn || status.State != "idle" || status.Agent != "planner" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if rec, err := store.Load(ctx, "sess-1"); err != nil || rec == nil {
		t.Fatalf("expected persisted record, rec=%+v err=%v", rec, err)
	}
	obs := mgr.Status(ctx)
	if obs.RuntimeCacheSize != 1 || obs.Counters.TurnsCompleted != 1 || obs.Counters.SessionsInitialized != 1 {
		t.Fatalf("unexpected manager status: %+v", obs)
	}
}

func TestManagerCancelActiveTurn(t *testing.T) {
	started := make(chan struct{})
	rt := &managerTestRuntime{blockRun: true, started: started}
	mgr, _ := newTestManager(t, rt, ManagerOptions{DefaultTurnTimeout: time.Minute})
	ctx := context.Background()
	if _, err := mgr.InitializeSession(ctx, InitializeSessionInput{SessionKey: "sess-cancel", Backend: "test"}); err != nil {
		t.Fatal(err)
	}

	runDone := make(chan error, 1)
	go func() {
		_, err := mgr.RunTurn(ctx, RunSessionTurnInput{SessionKey: "sess-cancel", Text: "wait"})
		runDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("turn did not start")
	}
	if err := mgr.CancelSession(ctx, CancelSessionInput{SessionKey: "sess-cancel", Reason: "test"}); err != nil {
		t.Fatalf("CancelSession: %v", err)
	}
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after cancel")
	}
	_, _, cancels, _, _ := rt.counts()
	if cancels != 1 {
		t.Fatalf("cancels = %d, want 1", cancels)
	}
	obs := mgr.Status(ctx)
	if obs.ActiveTurns != 0 || obs.Counters.TurnsCanceled == 0 {
		t.Fatalf("unexpected manager status after cancel: %+v", obs)
	}
}

func TestManagerCleanupIdleRuntimeHandles(t *testing.T) {
	now := time.Unix(100, 0)
	rt := &managerTestRuntime{}
	mgr, _ := newTestManager(t, rt, ManagerOptions{RuntimeIdleTTL: time.Second, Now: func() time.Time { return now }})
	ctx := context.Background()
	if _, err := mgr.InitializeSession(ctx, InitializeSessionInput{SessionKey: "sess-idle", Backend: "test"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if err := mgr.CleanupIdleRuntimeHandles(ctx); err != nil {
		t.Fatalf("CleanupIdleRuntimeHandles: %v", err)
	}
	_, _, _, closes, _ := rt.counts()
	if closes != 1 {
		t.Fatalf("closes = %d, want 1", closes)
	}
	obs := mgr.Status(ctx)
	if obs.RuntimeCacheSize != 0 || obs.Counters.RuntimeEvicted != 1 {
		t.Fatalf("unexpected status after cleanup: %+v", obs)
	}
}

func TestManagerSpawnSessionPersistsAncestry(t *testing.T) {
	rt := &managerTestRuntime{}
	mgr, store := newTestManager(t, rt, ManagerOptions{})
	ctx := context.Background()
	if _, err := mgr.InitializeSession(ctx, InitializeSessionInput{SessionKey: "parent", Backend: "test", Agent: "planner", CWD: "/repo"}); err != nil {
		t.Fatal(err)
	}
	res, err := mgr.SpawnSession(ctx, SpawnSessionInput{ParentSessionKey: "parent", ChildSessionKey: "child", Agent: "worker", ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("SpawnSession: %v", err)
	}
	if res.ChildSessionKey != "child" || res.ParentSessionKey != "parent" || res.Depth != 1 || res.ThreadID != "thread-1" {
		t.Fatalf("unexpected spawn result: %+v", res)
	}
	childRec, err := store.Load(ctx, "child")
	if err != nil || childRec == nil {
		t.Fatalf("load child rec=%+v err=%v", childRec, err)
	}
	childMeta := decodeSessionRuntimeMeta(childRec)
	if childMeta.ParentSessionKey != "parent" || childMeta.SpawnDepth != 1 || childMeta.ThreadID != "thread-1" || childMeta.CWD != "/repo" {
		t.Fatalf("unexpected child meta: %+v", childMeta)
	}
	parentRec, _ := store.Load(ctx, "parent")
	parentMeta := decodeSessionRuntimeMeta(parentRec)
	if len(parentMeta.ChildSessionKeys) != 1 || parentMeta.ChildSessionKeys[0] != "child" {
		t.Fatalf("unexpected parent children: %+v", parentMeta.ChildSessionKeys)
	}
	if obs := mgr.Status(ctx); obs.Counters.SessionsSpawned != 1 {
		t.Fatalf("sessions spawned = %d, want 1", obs.Counters.SessionsSpawned)
	}
}

func TestManagerSpawnSessionEnforcesLimits(t *testing.T) {
	rt := &managerTestRuntime{}
	mgr, _ := newTestManager(t, rt, ManagerOptions{MaxSpawnDepth: 1, MaxChildrenPerSession: 1})
	ctx := context.Background()
	if _, err := mgr.InitializeSession(ctx, InitializeSessionInput{SessionKey: "parent", Backend: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.SpawnSession(ctx, SpawnSessionInput{ParentSessionKey: "parent", ChildSessionKey: "child-1"}); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	if _, err := mgr.SpawnSession(ctx, SpawnSessionInput{ParentSessionKey: "parent", ChildSessionKey: "child-2"}); err == nil {
		t.Fatal("second child spawn succeeded, want child limit error")
	}
	if _, err := mgr.SpawnSession(ctx, SpawnSessionInput{ParentSessionKey: "child-1", ChildSessionKey: "grandchild"}); err == nil {
		t.Fatal("grandchild spawn succeeded, want depth limit error")
	}
}
