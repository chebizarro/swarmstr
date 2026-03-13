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
	e.ProviderOverride = "anthropic"
	e.ThinkingLevel = "high"
	e.QueueCap = 20
	e.QueueDrop = "summarize"
	e.LastChannel = "nostr"
	e.FallbackFrom = "claude-sonnet"
	e.FallbackTo = "claude-haiku"
	e.FallbackReason = "rate_limit"
	e.FallbackAt = 123456
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
	if e2.ProviderOverride != "anthropic" {
		t.Fatalf("provider override mismatch: %q", e2.ProviderOverride)
	}
	if e2.ThinkingLevel != "high" {
		t.Fatalf("thinking level mismatch: %q", e2.ThinkingLevel)
	}
	if e2.QueueCap != 20 || e2.QueueDrop != "summarize" {
		t.Fatalf("queue fields mismatch: cap=%d drop=%q", e2.QueueCap, e2.QueueDrop)
	}
	if e2.LastChannel != "nostr" {
		t.Fatalf("last channel mismatch: %q", e2.LastChannel)
	}
	if e2.FallbackTo != "claude-haiku" || e2.FallbackFrom != "claude-sonnet" || e2.FallbackReason != "rate_limit" || e2.FallbackAt != 123456 {
		t.Fatalf("fallback fields mismatch: %+v", e2)
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
		SessionID:        "old",
		ProviderOverride: "anthropic",
		ModelOverride:    "claude-3",
		Verbose:          true,
		ThinkingLevel:    "high",
		QueueCap:         25,
		SendSuppressed:   true,
		InputTokens:      999,
	}
	e2 := e.CarryOverFlags("new")
	if e2.SessionID != "new" {
		t.Fatalf("id: got %q", e2.SessionID)
	}
	if e2.ModelOverride != "claude-3" {
		t.Fatal("model override not carried over")
	}
	if e2.ProviderOverride != "anthropic" {
		t.Fatal("provider override not carried over")
	}
	if e2.ThinkingLevel != "high" {
		t.Fatal("thinking level not carried over")
	}
	if e2.QueueCap != 25 {
		t.Fatal("queue cap not carried over")
	}
	if !e2.Verbose {
		t.Fatal("verbose not carried over")
	}
	if !e2.SendSuppressed {
		t.Fatal("send_suppressed should carry over to preserve user intent")
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

func TestSessionStore_LoadMigrationDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	legacy := `{"legacy":{"queue_drop":"old","model_override":"claude-3"}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	ss, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	e, ok := ss.Get("legacy")
	if !ok {
		t.Fatal("legacy key missing after load")
	}
	if e.SessionID != "legacy" {
		t.Fatalf("session id migration failed: %q", e.SessionID)
	}
	if e.QueueDrop != "oldest" {
		t.Fatalf("queue_drop migration failed: %q", e.QueueDrop)
	}
	if e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() {
		t.Fatal("timestamps should be defaulted on migration")
	}
}

func TestSessionStore_ListReturnsCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	ss, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	first := ss.GetOrNew("s1")
	first.Label = "alpha"
	if err := ss.Put("s1", first); err != nil {
		t.Fatalf("Put: %v", err)
	}
	second := ss.GetOrNew("s2")
	second.Label = "beta"
	if err := ss.Put("s2", second); err != nil {
		t.Fatalf("Put: %v", err)
	}

	listed := ss.List()
	if len(listed) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(listed))
	}
	mut := listed["s1"]
	mut.Label = "mutated"
	listed["s1"] = mut

	reloaded, ok := ss.Get("s1")
	if !ok {
		t.Fatal("s1 missing")
	}
	if reloaded.Label != "alpha" {
		t.Fatalf("store mutated via List() copy: got %q", reloaded.Label)
	}
}
