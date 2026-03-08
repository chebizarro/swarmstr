package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func writeHookMD(t *testing.T, dir, hookName, content string) string {
	t.Helper()
	hookDir := filepath.Join(dir, hookName)
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(hookDir, "HOOK.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const basicHookMD = `---
name: test-hook
description: "A test hook"
homepage: https://example.com
metadata:
  {
    "openclaw":
      {
        "emoji": "🧪",
        "events": ["command:new", "command:reset"],
        "requires": { "bins": ["git"] },
        "install": [{ "id": "bundled", "kind": "bundled", "label": "Bundled" }],
      },
  }
---

# Test Hook

Body text here.
`

// ────────────────────────────────────────────────────────────────────────────
// LoadHookMD
// ────────────────────────────────────────────────────────────────────────────

func TestLoadHookMD_basic(t *testing.T) {
	dir := t.TempDir()
	p := writeHookMD(t, dir, "test-hook", basicHookMD)

	h, err := LoadHookMD(p, SourceBundled)
	if err != nil {
		t.Fatalf("LoadHookMD: %v", err)
	}

	if h.HookKey != "test-hook" {
		t.Errorf("HookKey = %q, want %q", h.HookKey, "test-hook")
	}
	if h.Manifest.Name != "test-hook" {
		t.Errorf("Name = %q, want %q", h.Manifest.Name, "test-hook")
	}
	if h.Emoji() != "🧪" {
		t.Errorf("Emoji = %q, want 🧪", h.Emoji())
	}
	events := h.Events()
	if len(events) != 2 {
		t.Errorf("Events len = %d, want 2", len(events))
	}
	if h.Source != SourceBundled {
		t.Errorf("Source = %q, want bundled", h.Source)
	}
	if !strings.Contains(h.Manifest.Body, "Test Hook") {
		t.Errorf("Body does not contain expected text, got: %q", h.Manifest.Body)
	}
}

func TestLoadHookMD_noFrontmatter(t *testing.T) {
	dir := t.TempDir()
	p := writeHookMD(t, dir, "bare-hook", "# Just a markdown file\n\nNo frontmatter.")

	h, err := LoadHookMD(p, SourceManaged)
	if err != nil {
		t.Fatalf("LoadHookMD: %v", err)
	}
	if h.HookKey != "bare-hook" {
		t.Errorf("HookKey = %q, want %q", h.HookKey, "bare-hook")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// ScanDir
// ────────────────────────────────────────────────────────────────────────────

func TestScanDir_findsHooks(t *testing.T) {
	dir := t.TempDir()
	writeHookMD(t, dir, "hook-a", basicHookMD)
	writeHookMD(t, dir, "hook-b", basicHookMD)
	// A subdir without HOOK.md should be ignored.
	os.MkdirAll(filepath.Join(dir, "not-a-hook"), 0o755)
	// A file (not dir) should be ignored.
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644)

	hooks, err := ScanDir(dir, SourceBundled)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(hooks) != 2 {
		t.Errorf("len = %d, want 2", len(hooks))
	}
}

func TestScanDir_missingDir(t *testing.T) {
	hooks, err := ScanDir("/nonexistent/path/xyz", SourceBundled)
	if err != nil {
		t.Errorf("ScanDir missing dir should not error, got: %v", err)
	}
	if hooks != nil {
		t.Errorf("expected nil hooks, got %d", len(hooks))
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Manager
// ────────────────────────────────────────────────────────────────────────────

func TestManager_fireAndList(t *testing.T) {
	dir := t.TempDir()
	p := writeHookMD(t, dir, "my-hook", basicHookMD)

	h, err := LoadHookMD(p, SourceBundled)
	if err != nil {
		t.Fatal(err)
	}

	fired := false
	h.Handler = func(ev *Event) error {
		fired = true
		return nil
	}

	mgr := NewManager()
	mgr.Register(h)

	// Bundled hooks default to enabled.
	errs := mgr.Fire("command:new", "sess1", nil)
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if !fired {
		t.Error("handler was not fired")
	}

	// List should return the hook.
	list := mgr.List()
	if len(list) != 1 {
		t.Errorf("List len = %d, want 1", len(list))
	}
	if !list[0].Enabled {
		t.Error("hook should be enabled by default")
	}
}

func TestManager_disable(t *testing.T) {
	dir := t.TempDir()
	p := writeHookMD(t, dir, "my-hook", basicHookMD)
	h, _ := LoadHookMD(p, SourceBundled)

	fired := false
	h.Handler = func(ev *Event) error { fired = true; return nil }

	mgr := NewManager()
	mgr.Register(h)
	mgr.SetEnabled("my-hook", false)

	mgr.Fire("command:new", "sess1", nil)
	if fired {
		t.Error("disabled hook should not fire")
	}
}

func TestManager_broadcastEvent(t *testing.T) {
	// "command" event name should match "command:new" because subscribes() allows prefix match.
	const loggerHookMD = `---
name: logger
description: "Logs commands"
metadata:
  {
    "openclaw":
      {
        "emoji": "📝",
        "events": ["command"],
        "install": [{ "id": "bundled", "kind": "bundled", "label": "Bundled" }],
      },
  }
---
`
	dir := t.TempDir()
	p := writeHookMD(t, dir, "logger", loggerHookMD)
	h, _ := LoadHookMD(p, SourceBundled)

	count := 0
	h.Handler = func(ev *Event) error { count++; return nil }

	mgr := NewManager()
	mgr.Register(h)

	mgr.Fire("command:new", "s", nil)
	mgr.Fire("command:reset", "s", nil)
	mgr.Fire("agent:bootstrap", "s", nil) // should NOT fire

	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestManager_info(t *testing.T) {
	dir := t.TempDir()
	p := writeHookMD(t, dir, "my-hook", basicHookMD)
	h, _ := LoadHookMD(p, SourceBundled)
	h.Handler = func(_ *Event) error { return nil }

	mgr := NewManager()
	mgr.Register(h)

	info := mgr.Info("my-hook")
	if info == nil {
		t.Fatal("Info returned nil")
	}
	if info.HookKey != "my-hook" {
		t.Errorf("HookKey = %q, want my-hook", info.HookKey)
	}

	// Unknown key.
	if mgr.Info("nonexistent") != nil {
		t.Error("expected nil for unknown hook")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Bundled hook handlers
// ────────────────────────────────────────────────────────────────────────────

func TestCommandLoggerHandler(t *testing.T) {
	logDir := t.TempDir()
	opts := BundledHandlerOpts{LogDir: logDir}
	handler := makeCommandLoggerHandler(opts)

	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "sess1",
		Context:    map[string]any{"source": "nostr"},
		Timestamp:  now(),
	}

	if err := handler(ev); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(logDir, "commands.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "command:new") {
		t.Errorf("log missing event name, got: %s", data)
	}
}

func TestSessionMemoryHandler_noTranscript(t *testing.T) {
	workDir := t.TempDir()
	opts := BundledHandlerOpts{
		WorkspaceDir: func() string { return workDir },
	}
	handler := makeSessionMemoryHandler(opts)
	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "sess1",
		Timestamp:  now(),
		Context:    map[string]any{},
	}

	if err := handler(ev); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	// No transcript → no file should be written.
	entries, _ := os.ReadDir(filepath.Join(workDir, "memory"))
	if len(entries) != 0 {
		t.Errorf("expected no memory files, got %d", len(entries))
	}
}

func TestSessionMemoryHandler_withTranscript(t *testing.T) {
	workDir := t.TempDir()
	opts := BundledHandlerOpts{
		WorkspaceDir: func() string { return workDir },
		GetTranscript: func(sessionKey string, limit int) ([]TranscriptMessage, error) {
			return []TranscriptMessage{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi there"},
			}, nil
		},
	}
	handler := makeSessionMemoryHandler(opts)
	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "sess1",
		Timestamp:  now(),
		Context:    map[string]any{},
	}

	if err := handler(ev); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(workDir, "memory"))
	if err != nil {
		t.Fatalf("read memory dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 memory file, got %d", len(entries))
	}
}

func now() time.Time { return time.Now() }
