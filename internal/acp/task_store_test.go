package acp

import (
	"context"
	"testing"
	"time"
)

func TestTaskStoreLifecycleAndPersistence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileTaskStore(dir)
	if err != nil {
		t.Fatalf("NewFileTaskStore: %v", err)
	}
	created := time.Unix(100, 0)
	if err := store.Create(ctx, TaskRecord{TaskID: "task-a", Status: TaskStatusQueued, DeliveryStatus: DeliveryPending, CreatedAt: created, RequesterSessionKey: "parent"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	started := time.Unix(101, 0)
	if err := store.Update(ctx, "task-a", TaskPatch{Status: taskStatusPtr(TaskStatusRunning), StartedAt: timePtrPtr(&started)}); err != nil {
		t.Fatalf("running update: %v", err)
	}
	ended := time.Unix(102, 0)
	artifact := ArtifactPayload{Type: "json", Data: []byte(`{"ok":true}`)}
	if err := store.Update(ctx, "task-a", TaskPatch{Status: taskStatusPtr(TaskStatusSucceeded), DeliveryStatus: deliveryStatusPtr(DeliveryDelivered), EndedAt: timePtrPtr(&ended), Artifacts: artifactsPtr([]ArtifactPayload{artifact})}); err != nil {
		t.Fatalf("terminal update: %v", err)
	}

	reloaded, err := NewFileTaskStore(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	rec, err := reloaded.Get(ctx, "task-a")
	if err != nil || rec == nil {
		t.Fatalf("Get reloaded rec=%+v err=%v", rec, err)
	}
	if rec.Status != TaskStatusSucceeded || rec.DeliveryStatus != DeliveryDelivered || len(rec.Artifacts) != 1 {
		t.Fatalf("unexpected reloaded task: %+v", rec)
	}
	listed, err := reloaded.List(ctx, TaskFilter{Statuses: []TaskStatus{TaskStatusSucceeded}, RequesterSessionKey: "parent"})
	if err != nil || len(listed) != 1 {
		t.Fatalf("List got %d err=%v", len(listed), err)
	}
}

func TestDispatcherRegisterTaskWithErrorRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	d := NewDispatcherWithStore(NewInMemoryTaskStore())
	if _, err := d.RegisterTaskWithError(ctx, TaskRecord{TaskID: "dup"}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if _, err := d.RegisterTaskWithError(ctx, TaskRecord{TaskID: "dup"}); err == nil {
		t.Fatal("expected duplicate task registration error")
	}
	if d.PendingCount() != 1 {
		t.Fatalf("pending count = %d, want only first task pending", d.PendingCount())
	}
}

func TestDispatcherUpdatesTaskStore(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryTaskStore()
	d := NewDispatcherWithStore(store)
	d.RegisterTask(ctx, TaskRecord{TaskID: "task-dispatch", Instructions: "do it"})
	d.MarkRunning(ctx, "task-dispatch")
	if !d.Deliver(TaskResult{TaskID: "task-dispatch", Text: "done", Artifacts: []ArtifactPayload{{Type: "text", Text: "done"}}}) {
		t.Fatal("Deliver returned false")
	}
	rec, err := store.Get(ctx, "task-dispatch")
	if err != nil || rec == nil {
		t.Fatalf("Get rec=%+v err=%v", rec, err)
	}
	if rec.Status != TaskStatusSucceeded || rec.DeliveryStatus != DeliveryDelivered || rec.StartedAt == nil || rec.EndedAt == nil {
		t.Fatalf("unexpected record: %+v", rec)
	}
}
