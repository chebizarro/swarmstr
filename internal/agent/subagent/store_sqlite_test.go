package subagent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteRegistryRestartReconciliationAndDelivery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subagents.sqlite")
	reg, err := OpenSQLiteRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(SubagentRunRecord{RunID: "run-1", ChildSessionKey: "child", RequesterSessionKey: "parent", Task: "work", StartedAt: 10, ExecutionStatus: ExecutionRunning}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLiteRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	now := time.UnixMilli(1000)
	if count, err := reopened.ReconcileRestart(now); err != nil || count != 1 {
		t.Fatalf("reconcile count=%d err=%v", count, err)
	}
	rec := reopened.Get("run-1")
	if rec == nil || rec.Outcome == nil || rec.Outcome.Error != "daemon_restart" || rec.Delivery.Status != DeliveryPending {
		t.Fatalf("reconciled record = %+v", rec)
	}

	announcer := &recordingAnnouncer{}
	worker := CompletionDeliveryWorker{Registry: reopened, Announcer: announcer, Lease: time.Minute, MaxAttempts: 3}
	if err := worker.DeliverDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec = reopened.Get("run-1")
	if rec.Delivery.Status != DeliveryDelivered || len(announcer.envelopes) != 1 || announcer.envelopes[0].IdempotencyKey != "subagent:run-1:1" {
		t.Fatalf("delivery rec=%+v envelopes=%+v", rec, announcer.envelopes)
	}
}

func TestCompletionDeliveryFailurePersistsRetry(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(SubagentRunRecord{RunID: "run", ChildSessionKey: "child", RequesterSessionKey: "parent"}); err != nil {
		t.Fatal(err)
	}
	if ended, err := reg.EndWithError("run", RunOutcome{Status: "ok", Result: "done"}); err != nil || !ended {
		t.Fatalf("end=%v err=%v", ended, err)
	}
	announcer := &recordingAnnouncer{err: errors.New("relay rejected")}
	worker := CompletionDeliveryWorker{Registry: reg, Announcer: announcer, MaxAttempts: 3}
	if err := worker.DeliverDue(context.Background()); err == nil {
		t.Fatal("expected delivery error")
	}
	rec := reg.Get("run")
	if rec.Delivery.Status != DeliveryFailed || rec.Delivery.Attempts != 1 || rec.Delivery.NextAttemptAt == 0 {
		t.Fatalf("retry state = %+v", rec.Delivery)
	}
}

type recordingAnnouncer struct {
	envelopes []CompletionEnvelope
	err       error
}

func (a *recordingAnnouncer) AnnounceCompletion(_ context.Context, envelope CompletionEnvelope) error {
	a.envelopes = append(a.envelopes, envelope)
	return a.err
}
