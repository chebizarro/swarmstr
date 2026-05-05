package state

import (
	"path/filepath"
	"testing"
)

func TestSessionStore_CRUD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	ss, err := NewSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// List empty
	list := ss.List()
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}

	// GetOrNew creates new entry
	e := ss.GetOrNew("sess-1")
	if e.SessionID != "sess-1" {
		t.Errorf("expected sess-1, got %s", e.SessionID)
	}
	if e.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}

	// GetOrNew returns existing
	e2 := ss.GetOrNew("sess-1")
	if e2.SessionID != e.SessionID {
		t.Error("expected same session")
	}

	// Put and Get
	e.Label = "test-label"
	if err := ss.Put("sess-1", e); err != nil {
		t.Fatal(err)
	}
	got, ok := ss.Get("sess-1")
	if !ok {
		t.Fatal("expected to find sess-1")
	}
	if got.Label != "test-label" {
		t.Errorf("expected test-label, got %s", got.Label)
	}

	// List with entry
	list = ss.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list))
	}

	// Delete
	if err := ss.Delete("sess-1"); err != nil {
		t.Fatal(err)
	}
	_, ok = ss.Get("sess-1")
	if ok {
		t.Error("expected sess-1 to be deleted")
	}
	reloaded, err := NewSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get("sess-1"); ok {
		t.Error("expected journaled delete to survive reload")
	}
}

func TestSessionStore_AddTokens(t *testing.T) {
	dir := t.TempDir()
	ss, err := NewSessionStore(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}

	// AddTokens on non-existing key creates entry
	if err := ss.AddTokens("sess-1", 100, 50, 10, 5); err != nil {
		t.Fatal(err)
	}
	e, ok := ss.Get("sess-1")
	if !ok {
		t.Fatal("expected sess-1 after AddTokens")
	}
	if e.InputTokens != 100 || e.OutputTokens != 50 || e.TotalTokens != 150 {
		t.Errorf("tokens mismatch: in=%d out=%d total=%d", e.InputTokens, e.OutputTokens, e.TotalTokens)
	}
	if e.CacheRead != 10 || e.CacheWrite != 5 {
		t.Errorf("cache mismatch: read=%d write=%d", e.CacheRead, e.CacheWrite)
	}

	// AddTokens accumulates
	if err := ss.AddTokens("sess-1", 200, 100, 20, 10); err != nil {
		t.Fatal(err)
	}
	e, _ = ss.Get("sess-1")
	if e.TotalTokens != 450 {
		t.Errorf("expected 450 total, got %d", e.TotalTokens)
	}
	if e.CacheRead != 30 || e.CacheWrite != 15 {
		t.Errorf("cache accumulate mismatch: read=%d write=%d", e.CacheRead, e.CacheWrite)
	}
}

func TestSessionStore_Path(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	ss, err := NewSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if ss.Path() != path {
		t.Errorf("expected %s, got %s", path, ss.Path())
	}

	// Nil store
	var nilStore *SessionStore
	if nilStore.Path() != "" {
		t.Error("nil store should return empty path")
	}
}

func TestSessionStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	ss, err := NewSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ss.Put("sess-1", SessionEntry{SessionID: "sess-1", Label: "persisted"}); err != nil {
		t.Fatal(err)
	}

	// Load from same file
	ss2, err := NewSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := ss2.Get("sess-1")
	if !ok {
		t.Fatal("expected to find persisted session")
	}
	if e.Label != "persisted" {
		t.Errorf("expected persisted, got %s", e.Label)
	}
}

func TestDefaultSessionStorePath(t *testing.T) {
	p := DefaultSessionStorePath()
	if p == "" {
		t.Fatal("path should not be empty")
	}
	if filepath.Base(p) != "sessions.json" {
		t.Errorf("expected sessions.json, got %s", filepath.Base(p))
	}
}

func TestCarryOverFlags(t *testing.T) {
	e := SessionEntry{
		SessionID: "old",
		Label:     "my-label",
		AgentID:   "agent-1",
		Verbose:   true,
		FastMode:  true,
	}
	newE := e.CarryOverFlags("new-sess")
	if newE.SessionID != "new-sess" {
		t.Errorf("expected new-sess, got %s", newE.SessionID)
	}
	if newE.Label != "my-label" {
		t.Errorf("expected label carried over")
	}
	if newE.AgentID != "agent-1" {
		t.Errorf("expected agent carried over")
	}
	if !newE.Verbose || !newE.FastMode {
		t.Error("expected flags carried over")
	}
	if newE.CreatedAt.IsZero() {
		t.Error("expected new CreatedAt")
	}
}
