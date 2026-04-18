package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func tempSessionStore(t *testing.T) *FileSessionStore {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions")
	s, err := NewFileSessionStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFileSessionStore_SaveAndLoad(t *testing.T) {
	s := tempSessionStore(t)
	ctx := context.Background()

	rec := &SessionRecord{
		AgentID:    "codex",
		SessionKey: "agent:codex:session:1",
		State:      json.RawMessage(`{"hello":"world"}`),
	}
	if err := s.Save(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if rec.ID == "" {
		t.Fatal("expected ID to be set")
	}
	if rec.CreatedAt == 0 {
		t.Fatal("expected CreatedAt to be set")
	}
	if rec.UpdatedAt == 0 {
		t.Fatal("expected UpdatedAt to be set")
	}

	loaded, err := s.Load(ctx, "agent:codex:session:1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil record")
	}
	if loaded.AgentID != "codex" {
		t.Fatalf("agent_id = %q", loaded.AgentID)
	}
	if string(loaded.State) != `{"hello":"world"}` {
		t.Fatalf("state = %s", loaded.State)
	}
}

func TestFileSessionStore_LoadNotFound(t *testing.T) {
	s := tempSessionStore(t)
	ctx := context.Background()

	rec, err := s.Load(ctx, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		t.Fatal("expected nil for missing session")
	}
}

func TestFileSessionStore_LoadEmptyKey(t *testing.T) {
	s := tempSessionStore(t)
	ctx := context.Background()

	rec, err := s.Load(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		t.Fatal("expected nil for empty key")
	}
}

func TestFileSessionStore_Delete(t *testing.T) {
	s := tempSessionStore(t)
	ctx := context.Background()

	_ = s.Save(ctx, &SessionRecord{SessionKey: "to-delete"})
	if err := s.Delete(ctx, "to-delete"); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Load(ctx, "to-delete")
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestFileSessionStore_DeleteNonexistent(t *testing.T) {
	s := tempSessionStore(t)
	if err := s.Delete(context.Background(), "no-such-key"); err != nil {
		t.Fatalf("delete nonexistent should not error: %v", err)
	}
}

func TestFileSessionStore_List(t *testing.T) {
	s := tempSessionStore(t)
	ctx := context.Background()

	_ = s.Save(ctx, &SessionRecord{SessionKey: "a"})
	_ = s.Save(ctx, &SessionRecord{SessionKey: "b"})
	_ = s.Save(ctx, &SessionRecord{SessionKey: "c"})

	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("list len = %d, want 3", len(list))
	}
}

func TestFileSessionStore_MarkFresh(t *testing.T) {
	s := tempSessionStore(t)
	ctx := context.Background()

	// Save a session first.
	_ = s.Save(ctx, &SessionRecord{SessionKey: "session-1", State: json.RawMessage(`{"stale":true}`)})

	// Verify it loads normally.
	loaded, _ := s.Load(ctx, "session-1")
	if loaded == nil {
		t.Fatal("expected record before MarkFresh")
	}

	// Mark fresh — Load should now return nil without hitting disk.
	s.MarkFresh("session-1")
	if !s.IsFresh("session-1") {
		t.Fatal("expected IsFresh=true")
	}
	loaded, _ = s.Load(ctx, "session-1")
	if loaded != nil {
		t.Fatal("expected nil after MarkFresh")
	}

	// Save a new record — should clear fresh mark.
	_ = s.Save(ctx, &SessionRecord{SessionKey: "session-1", State: json.RawMessage(`{"fresh":true}`)})
	if s.IsFresh("session-1") {
		t.Fatal("expected IsFresh=false after Save")
	}
	loaded, _ = s.Load(ctx, "session-1")
	if loaded == nil {
		t.Fatal("expected record after fresh Save")
	}
	if string(loaded.State) != `{"fresh":true}` {
		t.Fatalf("state = %s", loaded.State)
	}
}

func TestFileSessionStore_MarkFreshThenDelete(t *testing.T) {
	s := tempSessionStore(t)
	ctx := context.Background()

	_ = s.Save(ctx, &SessionRecord{SessionKey: "key"})
	s.MarkFresh("key")
	_ = s.Delete(ctx, "key")
	if s.IsFresh("key") {
		t.Fatal("delete should clear fresh mark")
	}
}

func TestFileSessionStore_SaveNilRecord(t *testing.T) {
	s := tempSessionStore(t)
	if err := s.Save(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil record")
	}
}

func TestFileSessionStore_SaveEmptyKey(t *testing.T) {
	s := tempSessionStore(t)
	err := s.Save(context.Background(), &SessionRecord{SessionKey: ""})
	if err == nil {
		t.Fatal("expected error for empty session key")
	}
}

func TestFileSessionStore_UpdateOverwrite(t *testing.T) {
	s := tempSessionStore(t)
	ctx := context.Background()

	_ = s.Save(ctx, &SessionRecord{SessionKey: "key", State: json.RawMessage(`"v1"`)})
	_ = s.Save(ctx, &SessionRecord{SessionKey: "key", State: json.RawMessage(`"v2"`)})

	loaded, _ := s.Load(ctx, "key")
	if loaded == nil {
		t.Fatal("expected record")
	}
	if string(loaded.State) != `"v2"` {
		t.Fatalf("state = %s, want v2", loaded.State)
	}
}

func TestFileSessionStore_DirectoryCreation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deep", "sessions")
	s, err := NewFileSessionStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), &SessionRecord{SessionKey: "test"}); err != nil {
		t.Fatal(err)
	}
	// Verify directory was created.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("directory should exist")
	}
}

func TestFileSessionStore_ConcurrentAccess(t *testing.T) {
	s := tempSessionStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "session"
			_ = s.Save(ctx, &SessionRecord{SessionKey: key, State: json.RawMessage(`"data"`)})
			s.Load(ctx, key)
			s.MarkFresh(key)
			s.IsFresh(key)
			s.List(ctx)
		}(i)
	}
	wg.Wait()
}
