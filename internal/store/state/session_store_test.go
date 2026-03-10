package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionStore_GetPutDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	ss, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	// Missing key.
	_, ok := ss.Get("s1")
	if ok {
		t.Fatal("expected not found")
	}

	// GetOrNew creates.
	e := ss.GetOrNew("s1")
	if e.SessionID != "s1" {
		t.Fatalf("got %q want s1", e.SessionID)
	}

	// Put persists.
	e.ModelOverride = "claude-3"
	if err := ss.Put("s1", e); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Reload from disk.
	ss2, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	e2, ok := ss2.Get("s1")
	if !ok {
		t.Fatal("not found after reload")
	}
	if e2.ModelOverride != "claude-3" {
		t.Fatalf("got %q want claude-3", e2.ModelOverride)
	}

	// Delete.
	if err := ss2.Delete("s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok = ss2.Get("s1")
	if ok {
		t.Fatal("expected not found after delete")
	}
}

func TestSessionStore_AddTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	ss, _ := NewSessionStore(path)
	ss.GetOrNew("s1")

	if err := ss.AddTokens("s1", 100, 50); err != nil {
		t.Fatalf("AddTokens: %v", err)
	}
	if err := ss.AddTokens("s1", 200, 80); err != nil {
		t.Fatalf("AddTokens: %v", err)
	}

	e, _ := ss.Get("s1")
	if e.InputTokens != 300 {
		t.Fatalf("input: got %d want 300", e.InputTokens)
	}
	if e.OutputTokens != 130 {
		t.Fatalf("output: got %d want 130", e.OutputTokens)
	}
	if e.TotalTokens != 430 {
		t.Fatalf("total: got %d want 430", e.TotalTokens)
	}
}

func TestSessionStore_CarryOverFlags(t *testing.T) {
	e := SessionEntry{
		SessionID:     "old",
		ModelOverride: "claude-3",
		Verbose:       true,
		InputTokens:   999,
	}
	e2 := e.CarryOverFlags("new")
	if e2.SessionID != "new" {
		t.Fatalf("id: got %q", e2.SessionID)
	}
	if e2.ModelOverride != "claude-3" {
		t.Fatal("model override not carried over")
	}
	if !e2.Verbose {
		t.Fatal("verbose not carried over")
	}
	if e2.InputTokens != 0 {
		t.Fatal("tokens should not carry over")
	}
}

func TestSessionStore_MissingDir(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "a", "b", "sessions.json")
	ss, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("NewSessionStore with nested dir: %v", err)
	}
	ss.GetOrNew("x")
	if err := ss.Put("x", ss.GetOrNew("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}
