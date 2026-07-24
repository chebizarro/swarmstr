package acp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func spawnBackgroundTestChild(t *testing.T, mgr *Manager) {
	t.Helper()
	ctx := context.Background()
	if _, err := mgr.InitializeSession(ctx, InitializeSessionInput{SessionKey: "requester", Backend: "test", Agent: "main"}); err != nil {
		t.Fatalf("initialize requester: %v", err)
	}
	if _, err := mgr.SpawnSession(ctx, SpawnSessionInput{ParentSessionKey: "requester", ChildSessionKey: "child", Agent: "worker"}); err != nil {
		t.Fatalf("spawn child: %v", err)
	}
}

func TestManagerBackgroundChildSuccessReconcilesRequesterTask(t *testing.T) {
	rt := &managerTestRuntime{}
	mgr, _ := newTestManager(t, rt, ManagerOptions{})
	spawnBackgroundTestChild(t, mgr)

	events, err := mgr.RunTurn(context.Background(), RunSessionTurnInput{SessionKey: "child", RequestID: "run-success", Text: "do work"})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("RunTurn returned no events")
	}
	record, err := mgr.dispatcher.TaskStore().Get(context.Background(), "run-success")
	if err != nil || record == nil {
		t.Fatalf("task record = %+v, err = %v", record, err)
	}
	if record.Status != TaskStatusSucceeded || record.DeliveryStatus != DeliveryDelivered {
		t.Fatalf("task terminal state = %s/%s", record.Status, record.DeliveryStatus)
	}
	if record.RequesterSessionKey != "requester" || record.WorkerSessionKey != "child" {
		t.Fatalf("task routing = requester %q worker %q", record.RequesterSessionKey, record.WorkerSessionKey)
	}
	if record.ResultWorker == nil || record.ResultWorker.SessionID != "child" || record.ResultWorker.ParentContext == nil || record.ResultWorker.ParentContext.SessionID != "requester" {
		t.Fatalf("task result worker = %+v", record.ResultWorker)
	}
	if got := mgr.dispatcher.PendingCount(); got != 0 {
		t.Fatalf("detached task leaked %d waiter entries", got)
	}
}

func TestManagerBackgroundChildFailureReconcilesExactlyOnce(t *testing.T) {
	rt := &managerTestRuntime{events: []RuntimeEvent{{Kind: EventError, Text: "backend exploded", Code: "BROKEN"}}}
	mgr, _ := newTestManager(t, rt, ManagerOptions{})
	spawnBackgroundTestChild(t, mgr)

	_, runErr := mgr.RunTurn(context.Background(), RunSessionTurnInput{SessionKey: "child", RequestID: "run-failed", Text: "do work"})
	if runErr == nil {
		t.Fatal("RunTurn succeeded, want failure")
	}
	record, err := mgr.dispatcher.TaskStore().Get(context.Background(), "run-failed")
	if err != nil || record == nil {
		t.Fatalf("task record = %+v, err = %v", record, err)
	}
	if record.Status != TaskStatusFailed || record.Error == "" {
		t.Fatalf("failed task = %+v", record)
	}
	updated, err := mgr.dispatcher.ReconcileBackgroundTask(context.Background(), "run-failed", TaskStatusSucceeded, TaskResult{TaskID: "run-failed", Text: "late success"})
	if err != nil || updated {
		t.Fatalf("late terminal reconciliation = (%v, %v), want (false, nil)", updated, err)
	}
	after, _ := mgr.dispatcher.TaskStore().Get(context.Background(), "run-failed")
	if after.Status != TaskStatusFailed || after.TerminalSummary != record.TerminalSummary {
		t.Fatalf("late terminal overwrote record: before=%+v after=%+v", record, after)
	}
}

func TestManagerBackgroundChildExplicitCancelReconcilesCancelled(t *testing.T) {
	started := make(chan struct{})
	rt := &managerTestRuntime{blockRun: true, started: started}
	mgr, _ := newTestManager(t, rt, ManagerOptions{DefaultTurnTimeout: time.Minute})
	spawnBackgroundTestChild(t, mgr)

	done := make(chan error, 1)
	go func() {
		_, err := mgr.RunTurn(context.Background(), RunSessionTurnInput{SessionKey: "child", RequestID: "run-cancel", Text: "do work"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("child turn did not start")
	}
	if err := mgr.CancelSession(context.Background(), CancelSessionInput{SessionKey: "child", Reason: "requester cancelled"}); err != nil {
		t.Fatalf("CancelSession: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunTurn error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled child turn did not finish")
	}
	record, _ := mgr.dispatcher.TaskStore().Get(context.Background(), "run-cancel")
	if record == nil || record.Status != TaskStatusCancelled {
		t.Fatalf("cancelled task = %+v", record)
	}
}

func TestManagerBackgroundChildTimeoutReconcilesTimedOut(t *testing.T) {
	rt := &managerTestRuntime{blockRun: true}
	mgr, _ := newTestManager(t, rt, ManagerOptions{
		DefaultTurnTimeout:      10 * time.Millisecond,
		TurnTimeoutGrace:        -1,
		TurnTimeoutCleanupGrace: 10 * time.Millisecond,
	})
	spawnBackgroundTestChild(t, mgr)

	_, err := mgr.RunTurn(context.Background(), RunSessionTurnInput{SessionKey: "child", RequestID: "run-timeout", Text: "do work"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunTurn error = %v, want deadline exceeded", err)
	}
	record, _ := mgr.dispatcher.TaskStore().Get(context.Background(), "run-timeout")
	if record == nil || record.Status != TaskStatusTimedOut {
		t.Fatalf("timed out task = %+v", record)
	}
}

func TestManagerBackgroundChildSurvivesDisconnectReconciliation(t *testing.T) {
	started := make(chan struct{})
	rt := &managerTestRuntime{blockRun: true, started: started}
	mgr, _ := newTestManager(t, rt, ManagerOptions{DefaultTurnTimeout: time.Minute})
	spawnBackgroundTestChild(t, mgr)

	turnCtx, disconnect := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := mgr.RunTurn(turnCtx, RunSessionTurnInput{SessionKey: "child", RequestID: "run-reconnect", Text: "resume me"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("child turn did not start")
	}
	disconnect()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("disconnected RunTurn error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnected child turn did not return")
	}
	record, _ := mgr.dispatcher.TaskStore().Get(context.Background(), "run-reconnect")
	if record == nil || record.Status != TaskStatusRunning {
		t.Fatalf("disconnected task should remain running: %+v", record)
	}

	rt.mu.Lock()
	rt.blockRun = false
	rt.started = nil
	rt.mu.Unlock()
	if _, err := mgr.ReconcilePendingPrompt(context.Background(), "child"); err != nil {
		t.Fatalf("ReconcilePendingPrompt: %v", err)
	}
	record, _ = mgr.dispatcher.TaskStore().Get(context.Background(), "run-reconnect")
	if record == nil || record.Status != TaskStatusSucceeded {
		t.Fatalf("reconciled task = %+v", record)
	}
}

func TestDispatcherBackgroundTerminalRaceUpdatesOnce(t *testing.T) {
	dispatcher := NewDispatcher()
	active, err := dispatcher.BeginBackgroundTask(context.Background(), TaskRecord{
		TaskID:              "terminal-race",
		RequesterSessionKey: "requester",
		WorkerSessionKey:    "child",
	})
	if err != nil || !active {
		t.Fatalf("BeginBackgroundTask = (%v, %v)", active, err)
	}
	var updates atomic.Int32
	var wg sync.WaitGroup
	for _, status := range []TaskStatus{TaskStatusSucceeded, TaskStatusFailed, TaskStatusCancelled, TaskStatusTimedOut} {
		status := status
		wg.Add(1)
		go func() {
			defer wg.Done()
			updated, err := dispatcher.ReconcileBackgroundTask(context.Background(), "terminal-race", status, TaskResult{TaskID: "terminal-race", Error: string(status)})
			if err != nil {
				t.Errorf("ReconcileBackgroundTask(%s): %v", status, err)
			}
			if updated {
				updates.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := updates.Load(); got != 1 {
		t.Fatalf("terminal updates = %d, want 1", got)
	}
	record, _ := dispatcher.TaskStore().Get(context.Background(), "terminal-race")
	if record == nil || !record.Status.Terminal() {
		t.Fatalf("terminal record = %+v", record)
	}
	if got := dispatcher.PendingCount(); got != 0 {
		t.Fatalf("background task leaked %d pending waiters", got)
	}
}
