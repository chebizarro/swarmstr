package subagent

import (
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Registry basics
// ---------------------------------------------------------------------------

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r.Len() != 0 {
		t.Fatalf("new registry len = %d, want 0", r.Len())
	}
	if r.CountActive() != 0 {
		t.Fatalf("new registry active = %d, want 0", r.CountActive())
	}
}

func TestRegister_Success(t *testing.T) {
	r := NewRegistry()
	err := r.Register(SubagentRunRecord{
		RunID:               "run-1",
		ChildSessionKey:     "child-1",
		RequesterSessionKey: "parent-1",
		Task:                "do stuff",
		Cleanup:             "keep",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if r.Len() != 1 {
		t.Errorf("len = %d, want 1", r.Len())
	}
	if r.CountActive() != 1 {
		t.Errorf("active = %d, want 1", r.CountActive())
	}
}

func TestRegister_DuplicateReturnsError(t *testing.T) {
	r := NewRegistry()
	rec := SubagentRunRecord{
		RunID:           "run-dup",
		ChildSessionKey: "child-1",
		Task:            "task",
		Cleanup:         "keep",
	}
	if err := r.Register(rec); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(rec); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestRegister_RequiresRunID(t *testing.T) {
	r := NewRegistry()
	err := r.Register(SubagentRunRecord{ChildSessionKey: "c"})
	if err == nil {
		t.Fatal("expected error for empty run_id")
	}
}

func TestRegister_RequiresChildSessionKey(t *testing.T) {
	r := NewRegistry()
	err := r.Register(SubagentRunRecord{RunID: "r"})
	if err == nil {
		t.Fatal("expected error for empty child_session_key")
	}
}

func TestRegister_AutofillsCreatedAt(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-auto",
		ChildSessionKey: "child-auto",
		Task:            "task",
		Cleanup:         "keep",
	})
	rec := r.Get("run-auto")
	if rec == nil {
		t.Fatal("get returned nil")
	}
	if rec.CreatedAt == 0 {
		t.Error("createdAt should be auto-filled")
	}
}

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

func TestGet_Exists(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-g",
		ChildSessionKey: "child-g",
		Task:            "task",
		Cleanup:         "delete",
		CreatedAt:       100,
	})
	rec := r.Get("run-g")
	if rec == nil {
		t.Fatal("expected record")
	}
	if rec.RunID != "run-g" || rec.Cleanup != "delete" {
		t.Errorf("got %+v", rec)
	}
}

func TestGet_NotFound(t *testing.T) {
	r := NewRegistry()
	if r.Get("missing") != nil {
		t.Error("expected nil for missing run")
	}
}

func TestGet_ReturnsCopy(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-cp",
		ChildSessionKey: "child-cp",
		Task:            "task",
		Cleanup:         "keep",
	})
	rec := r.Get("run-cp")
	rec.Task = "mutated"
	orig := r.Get("run-cp")
	if orig.Task == "mutated" {
		t.Error("Get should return a copy, not a reference")
	}
}

// ---------------------------------------------------------------------------
// End
// ---------------------------------------------------------------------------

func TestEnd_Success(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-e",
		ChildSessionKey: "child-e",
		Task:            "task",
		Cleanup:         "keep",
	})
	ok := r.End("run-e", RunOutcome{Status: "ok"})
	if !ok {
		t.Fatal("expected true")
	}
	rec := r.Get("run-e")
	if rec.EndedAt == 0 {
		t.Error("endedAt should be set")
	}
	if rec.Outcome == nil || rec.Outcome.Status != "ok" {
		t.Errorf("outcome = %+v", rec.Outcome)
	}
	if r.CountActive() != 0 {
		t.Errorf("active = %d, want 0", r.CountActive())
	}
}

func TestEnd_AlreadyEnded(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-ee",
		ChildSessionKey: "child-ee",
		Task:            "task",
		Cleanup:         "keep",
	})
	r.End("run-ee", RunOutcome{Status: "ok"})
	ok := r.End("run-ee", RunOutcome{Status: "error"})
	if ok {
		t.Error("expected false for already-ended run")
	}
}

func TestEnd_NotFound(t *testing.T) {
	r := NewRegistry()
	ok := r.End("missing", RunOutcome{Status: "ok"})
	if ok {
		t.Error("expected false for missing run")
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestDelete(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-d",
		ChildSessionKey: "child-d",
		Task:            "task",
		Cleanup:         "keep",
	})
	r.Delete("run-d")
	if r.Len() != 0 {
		t.Errorf("len = %d, want 0", r.Len())
	}
	if r.Get("run-d") != nil {
		t.Error("expected nil after delete")
	}
}

// ---------------------------------------------------------------------------
// GetByChildSessionKey
// ---------------------------------------------------------------------------

func TestGetByChildSessionKey_PrefersActive(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-ended",
		ChildSessionKey: "child-x",
		Task:            "ended task",
		Cleanup:         "keep",
		CreatedAt:       100,
		EndedAt:         200,
		Outcome:         &RunOutcome{Status: "ok"},
	})
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-active",
		ChildSessionKey: "child-x",
		Task:            "active task",
		Cleanup:         "keep",
		CreatedAt:       150,
	})
	rec := r.GetByChildSessionKey("child-x")
	if rec == nil || rec.RunID != "run-active" {
		t.Errorf("expected active run, got %+v", rec)
	}
}

func TestGetByChildSessionKey_FallsBackToEnded(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-ended-only",
		ChildSessionKey: "child-y",
		Task:            "ended task",
		Cleanup:         "keep",
		CreatedAt:       100,
		EndedAt:         200,
		Outcome:         &RunOutcome{Status: "ok"},
	})
	rec := r.GetByChildSessionKey("child-y")
	if rec == nil || rec.RunID != "run-ended-only" {
		t.Errorf("expected ended run, got %+v", rec)
	}
}

func TestGetByChildSessionKey_NotFound(t *testing.T) {
	r := NewRegistry()
	if r.GetByChildSessionKey("missing") != nil {
		t.Error("expected nil")
	}
}

func TestGetByChildSessionKey_EmptyKey(t *testing.T) {
	r := NewRegistry()
	if r.GetByChildSessionKey("  ") != nil {
		t.Error("expected nil for blank key")
	}
}

func TestGetByChildSessionKey_LatestActive(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-old-active",
		ChildSessionKey: "child-z",
		Task:            "old active",
		Cleanup:         "keep",
		CreatedAt:       100,
	})
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-new-active",
		ChildSessionKey: "child-z",
		Task:            "new active",
		Cleanup:         "keep",
		CreatedAt:       200,
	})
	rec := r.GetByChildSessionKey("child-z")
	if rec == nil || rec.RunID != "run-new-active" {
		t.Errorf("expected newest active, got %+v", rec)
	}
}

// ---------------------------------------------------------------------------
// GetLatestByChildSessionKey
// ---------------------------------------------------------------------------

func TestGetLatestByChildSessionKey_IgnoresActiveVsEnded(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-active-old",
		ChildSessionKey: "child-l",
		Task:            "active old",
		Cleanup:         "keep",
		CreatedAt:       100,
	})
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-ended-new",
		ChildSessionKey: "child-l",
		Task:            "ended new",
		Cleanup:         "keep",
		CreatedAt:       200,
		EndedAt:         300,
		Outcome:         &RunOutcome{Status: "ok"},
	})
	rec := r.GetLatestByChildSessionKey("child-l")
	if rec == nil || rec.RunID != "run-ended-new" {
		t.Errorf("expected latest by createdAt, got %+v", rec)
	}
}

func TestGetLatestByChildSessionKey_EmptyKey(t *testing.T) {
	r := NewRegistry()
	if r.GetLatestByChildSessionKey("") != nil {
		t.Error("expected nil for empty key")
	}
}

// ---------------------------------------------------------------------------
// ListByRequester
// ---------------------------------------------------------------------------

func TestListByRequester(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:               "run-r1",
		ChildSessionKey:     "child-r1",
		RequesterSessionKey: "parent-a",
		Task:                "t1",
		Cleanup:             "keep",
		CreatedAt:           100,
	})
	_ = r.Register(SubagentRunRecord{
		RunID:               "run-r2",
		ChildSessionKey:     "child-r2",
		RequesterSessionKey: "parent-a",
		Task:                "t2",
		Cleanup:             "keep",
		CreatedAt:           200,
	})
	_ = r.Register(SubagentRunRecord{
		RunID:               "run-r3",
		ChildSessionKey:     "child-r3",
		RequesterSessionKey: "parent-b",
		Task:                "t3",
		Cleanup:             "keep",
		CreatedAt:           150,
	})

	runs := r.ListByRequester("parent-a")
	if len(runs) != 2 {
		t.Fatalf("count = %d, want 2", len(runs))
	}
	if runs[0].RunID != "run-r2" || runs[1].RunID != "run-r1" {
		t.Errorf("expected newest first: %v, %v", runs[0].RunID, runs[1].RunID)
	}
}

func TestListByRequester_EmptyKey(t *testing.T) {
	r := NewRegistry()
	if r.ListByRequester("") != nil {
		t.Error("expected nil for empty key")
	}
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

func TestConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "run-" + string(rune('A'+i%26)) + string(rune('0'+i/26))
			_ = r.Register(SubagentRunRecord{
				RunID:               id,
				ChildSessionKey:     "child-concurrent",
				RequesterSessionKey: "parent",
				Task:                "task",
				Cleanup:             "keep",
			})
			r.Get(id)
			r.GetByChildSessionKey("child-concurrent")
			r.GetLatestByChildSessionKey("child-concurrent")
			r.ListByRequester("parent")
			r.CountActive()
			r.Len()
		}(i)
	}
	wg.Wait()
	if r.Len() == 0 {
		t.Error("expected some registrations to succeed")
	}
}
