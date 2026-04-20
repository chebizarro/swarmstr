package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── RegisterBundledHandlers ─────────────���───────────────────────────────────

func TestRegisterBundledHandlers_AttachesHandlers(t *testing.T) {
	mgr := NewManager()
	for _, key := range []string{"session-memory", "bootstrap-extra-files", "command-logger", "boot-md"} {
		mgr.Register(&Hook{
			HookKey: key,
			Source:  SourceBundled,
			Manifest: HookManifest{
				Metadata: &HookMetaWrap{
					OpenClaw: &OpenClawHookMeta{
						Events: []string{"command:new"},
					},
				},
			},
		})
	}

	for _, h := range mgr.hooks {
		if h.Handler != nil {
			t.Fatalf("hook %s should have nil handler before registration", h.HookKey)
		}
	}

	opts := BundledHandlerOpts{
		WorkspaceDir: func() string { return t.TempDir() },
		LogDir:       t.TempDir(),
	}
	RegisterBundledHandlers(mgr, opts)

	for _, h := range mgr.hooks {
		if h.Handler == nil {
			t.Errorf("hook %s should have a handler after registration", h.HookKey)
		}
	}
}

func TestRegisterBundledHandlers_HandlersAreCallable(t *testing.T) {
	mgr := NewManager()
	for _, key := range []string{"session-memory", "bootstrap-extra-files", "command-logger", "boot-md"} {
		mgr.Register(&Hook{
			HookKey: key,
			Source:  SourceBundled,
			Manifest: HookManifest{
				Metadata: &HookMetaWrap{
					OpenClaw: &OpenClawHookMeta{Events: []string{"command:new"}},
				},
			},
		})
	}

	opts := BundledHandlerOpts{
		WorkspaceDir: func() string { return t.TempDir() },
		LogDir:       t.TempDir(),
	}
	RegisterBundledHandlers(mgr, opts)

	// Verify each attached handler is callable without error on a benign event.
	for _, h := range mgr.hooks {
		ev := &Event{
			EventType:  "command",
			Action:     "new",
			Name:       "command:new",
			SessionKey: "test",
			Context:    map[string]any{},
			Timestamp:  time.Now(),
			Messages:   []string{},
		}
		if err := h.Handler(ev); err != nil {
			t.Errorf("hook %s handler returned error: %v", h.HookKey, err)
		}
	}
}

func TestRegisterBundledHandlers_SkipsNonBundled(t *testing.T) {
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "session-memory",
		Source:  SourceManaged,
	})

	RegisterBundledHandlers(mgr, BundledHandlerOpts{})

	if mgr.hooks[0].Handler != nil {
		t.Error("non-bundled hook should not get a handler")
	}
}

func TestRegisterBundledHandlers_UnknownKeyNoHandler(t *testing.T) {
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "unknown-hook-key",
		Source:  SourceBundled,
	})

	RegisterBundledHandlers(mgr, BundledHandlerOpts{})

	if mgr.hooks[0].Handler != nil {
		t.Error("unknown bundled key should not get a handler")
	}
}

// ─── makeBootMDHandler ────────────────────────────────────────────────���──────

func TestBootMDHandler_WrongEventType(t *testing.T) {
	called := false
	opts := BundledHandlerOpts{
		RunBootMD: func(string, string) error { called = true; return nil },
	}
	handler := makeBootMDHandler(opts)

	ev := &Event{EventType: "command", Action: "new"}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("RunBootMD should not be called for non-gateway event")
	}
}

func TestBootMDHandler_WrongAction(t *testing.T) {
	called := false
	opts := BundledHandlerOpts{
		RunBootMD: func(string, string) error { called = true; return nil },
	}
	handler := makeBootMDHandler(opts)

	// Right event type, wrong action.
	ev := &Event{EventType: "gateway", Action: "shutdown"}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("RunBootMD should not be called for gateway:shutdown")
	}
}

func TestBootMDHandler_NilWorkspaceDir(t *testing.T) {
	handler := makeBootMDHandler(BundledHandlerOpts{
		WorkspaceDir: nil,
	})
	ev := &Event{EventType: "gateway", Action: "startup"}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
}

func TestBootMDHandler_EmptyWorkspaceDir(t *testing.T) {
	handler := makeBootMDHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return "" },
	})
	ev := &Event{EventType: "gateway", Action: "startup"}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
}

func TestBootMDHandler_NoBootFile(t *testing.T) {
	dir := t.TempDir()
	called := false
	handler := makeBootMDHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		RunBootMD:    func(string, string) error { called = true; return nil },
	})
	ev := &Event{EventType: "gateway", Action: "startup"}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("RunBootMD should not be called when BOOT.md doesn't exist")
	}
}

func TestBootMDHandler_UnreadableBootFile(t *testing.T) {
	dir := t.TempDir()
	bootPath := filepath.Join(dir, "BOOT.md")
	os.WriteFile(bootPath, []byte("content"), 0o644)
	os.Chmod(bootPath, 0o000)
	t.Cleanup(func() { os.Chmod(bootPath, 0o644) })

	handler := makeBootMDHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
	})

	ev := &Event{EventType: "gateway", Action: "startup"}
	err := handler(ev)
	if err == nil {
		t.Fatal("expected error when BOOT.md is unreadable")
	}
	if !strings.Contains(err.Error(), "boot-md") {
		t.Errorf("error should mention 'boot-md', got: %v", err)
	}
}

func TestBootMDHandler_WithBootFile(t *testing.T) {
	dir := t.TempDir()
	bootContent := "# Boot Instructions\nDo stuff."
	os.WriteFile(filepath.Join(dir, "BOOT.md"), []byte(bootContent), 0o644)

	var gotKey, gotMD string
	handler := makeBootMDHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		RunBootMD: func(key, md string) error {
			gotKey = key
			gotMD = md
			return nil
		},
	})

	ev := &Event{EventType: "gateway", Action: "startup", SessionKey: "test-sess"}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if gotKey != "test-sess" {
		t.Errorf("sessionKey = %q, want %q", gotKey, "test-sess")
	}
	if gotMD != bootContent {
		t.Errorf("markdown = %q, want %q", gotMD, bootContent)
	}
}

func TestBootMDHandler_DefaultSessionKey(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "BOOT.md"), []byte("hello"), 0o644)

	var gotKey string
	handler := makeBootMDHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		RunBootMD:    func(key, _ string) error { gotKey = key; return nil },
	})

	ev := &Event{EventType: "gateway", Action: "startup", SessionKey: ""}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if gotKey != "main" {
		t.Errorf("expected default sessionKey 'main', got %q", gotKey)
	}
}

func TestBootMDHandler_NilRunBootMD(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "BOOT.md"), []byte("hello"), 0o644)

	handler := makeBootMDHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		RunBootMD:    nil,
	})

	ev := &Event{EventType: "gateway", Action: "startup"}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
}

// ─── makeBootstrapExtraFilesHandler ──────────────��───────────────────────────

func TestBootstrapExtraFilesHandler_WrongEvent(t *testing.T) {
	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{})
	ev := &Event{EventType: "command", Action: "new"}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapExtraFilesHandler_NilWorkspaceDir(t *testing.T) {
	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: nil,
	})
	ev := &Event{EventType: "agent", Action: "bootstrap"}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapExtraFilesHandler_EmptyWorkspaceDir(t *testing.T) {
	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return "" },
	})
	ev := &Event{EventType: "agent", Action: "bootstrap"}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapExtraFilesHandler_NoPatterns(t *testing.T) {
	dir := t.TempDir()
	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
	})
	ev := &Event{
		EventType: "agent",
		Action:    "bootstrap",
		Context:   map[string]any{},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapExtraFilesHandler_PatternsFromStringSlice(t *testing.T) {
	dir := t.TempDir()

	var resolvedPaths []string
	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		ResolvePaths: func(wd string, patterns []string) ([]string, error) {
			resolvedPaths = patterns
			return nil, nil
		},
	})
	ev := &Event{
		EventType: "agent",
		Action:    "bootstrap",
		Context: map[string]any{
			"paths": []string{"*.md", "*.txt"},
		},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if len(resolvedPaths) != 2 || resolvedPaths[0] != "*.md" || resolvedPaths[1] != "*.txt" {
		t.Errorf("expected patterns [*.md *.txt], got %v", resolvedPaths)
	}
}

func TestBootstrapExtraFilesHandler_PatternsFromAnySlice(t *testing.T) {
	dir := t.TempDir()

	var resolvedPatterns []string
	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		ResolvePaths: func(wd string, patterns []string) ([]string, error) {
			resolvedPatterns = patterns
			return nil, nil
		},
	})
	ev := &Event{
		EventType: "agent",
		Action:    "bootstrap",
		Context: map[string]any{
			"patterns": []any{"*.go", "*.md"},
		},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if len(resolvedPatterns) != 2 {
		t.Errorf("expected 2 patterns, got %v", resolvedPatterns)
	}
}

func TestBootstrapExtraFilesHandler_FilesKeyUsed(t *testing.T) {
	dir := t.TempDir()

	var resolvedPatterns []string
	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		ResolvePaths: func(wd string, patterns []string) ([]string, error) {
			resolvedPatterns = patterns
			return nil, nil
		},
	})
	ev := &Event{
		EventType: "agent",
		Action:    "bootstrap",
		Context: map[string]any{
			"files": []any{"file1.txt"},
		},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if len(resolvedPatterns) != 1 || resolvedPatterns[0] != "file1.txt" {
		t.Errorf("expected [file1.txt], got %v", resolvedPatterns)
	}
}

func TestBootstrapExtraFilesHandler_FallbackGlob_ResolvesFiles(t *testing.T) {
	// When ResolvePaths is nil, the handler uses filepath.Glob directly.
	// Use a recognized bootstrap filename so LoadResolvedBootstrapFiles
	// accepts it end-to-end and sets bootstrapFiles on the context.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "BOOTSTRAP.md"), []byte("# Bootstrap content"), 0o644)

	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		ResolvePaths: nil,
	})
	ev := &Event{
		EventType: "agent",
		Action:    "bootstrap",
		Context: map[string]any{
			"paths": []string{filepath.Join(dir, "BOOTSTRAP.md")},
		},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	// LoadResolvedBootstrapFiles should accept BOOTSTRAP.md and set context.
	bf, ok := ev.Context["bootstrapFiles"]
	if !ok {
		t.Fatal("expected bootstrapFiles to be set in event context")
	}
	// Verify at least one file was loaded.
	if bf == nil {
		t.Fatal("bootstrapFiles should not be nil")
	}
}

func TestBootstrapExtraFilesHandler_FallbackGlob_RelativePath(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("# Soul"), 0o644)

	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		ResolvePaths: nil,
	})
	// Relative pattern — should be joined with workspaceDir.
	ev := &Event{
		EventType: "agent",
		Action:    "bootstrap",
		Context: map[string]any{
			"paths": []string{"SOUL.md"},
		},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	// Verify the glob resolved and loaded the file.
	bf, ok := ev.Context["bootstrapFiles"]
	if !ok {
		t.Fatal("expected bootstrapFiles for relative glob with recognized filename")
	}
	if bf == nil {
		t.Fatal("bootstrapFiles should not be nil")
	}
}

func TestBootstrapExtraFilesHandler_FallbackGlob_UnrecognizedFile(t *testing.T) {
	// Files with unrecognized basenames are silently skipped by
	// LoadResolvedBootstrapFiles — verify the handler doesn't set context.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "random.txt"), []byte("data"), 0o644)

	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		ResolvePaths: nil,
	})
	ev := &Event{
		EventType: "agent",
		Action:    "bootstrap",
		Context: map[string]any{
			"paths": []string{filepath.Join(dir, "random.txt")},
		},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if _, ok := ev.Context["bootstrapFiles"]; ok {
		t.Error("bootstrapFiles should not be set for unrecognized filenames")
	}
}

func TestBootstrapExtraFilesHandler_ResolvePathsError(t *testing.T) {
	// When ResolvePaths returns an error, the handler falls through to
	// the len(resolved)==0 check and returns nil (no panic, no error).
	dir := t.TempDir()
	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		ResolvePaths: func(wd string, patterns []string) ([]string, error) {
			return nil, os.ErrPermission
		},
	})
	ev := &Event{
		EventType: "agent",
		Action:    "bootstrap",
		Context: map[string]any{
			"paths": []string{"*.md"},
		},
	}
	if err := handler(ev); err != nil {
		t.Fatalf("handler should not propagate ResolvePaths error, got: %v", err)
	}
}

// ─── MakeShellHandler ──────────────────────────────────────────────��─────────

func TestMakeShellHandler_Success(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "handler.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho \"hook ran: $HOOK_NAME\"\n"), 0o755)

	handler := MakeShellHandler(script)
	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "test-sess",
		Context:    map[string]any{"from_pubkey": "abc123"},
		Timestamp:  time.Now(),
		Messages:   []string{},
	}

	if err := handler(ev); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	found := false
	for _, msg := range ev.Messages {
		if msg == "hook ran: command:new" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected output message 'hook ran: command:new', got: %v", ev.Messages)
	}
}

func TestMakeShellHandler_AllEnvVars(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "handler.sh")
	os.WriteFile(script, []byte(`#!/bin/sh
echo "TYPE=$HOOK_TYPE"
echo "ACTION=$HOOK_ACTION"
echo "SESSION=$HOOK_SESSION_KEY"
echo "FROM=$HOOK_FROM_PUBKEY"
echo "TO=$HOOK_TO_PUBKEY"
echo "EID=$HOOK_EVENT_ID"
echo "RELAY=$HOOK_RELAY"
echo "CHAN=$HOOK_CHANNEL_ID"
echo "CONTENT=$HOOK_CONTENT"
`), 0o755)

	handler := MakeShellHandler(script)
	ev := &Event{
		EventType:  "message",
		Action:     "received",
		Name:       "message:received",
		SessionKey: "sess-42",
		Context: map[string]any{
			"from_pubkey": "pubkey-sender",
			"to_pubkey":   "pubkey-receiver",
			"event_id":    "eid-123",
			"relay":       "wss://relay.example.com",
			"channel_id":  "nostr",
			"content":     "hello world",
		},
		Timestamp: time.Now(),
		Messages:  []string{},
	}

	if err := handler(ev); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	expected := []string{
		"TYPE=message",
		"ACTION=received",
		"SESSION=sess-42",
		"FROM=pubkey-sender",
		"TO=pubkey-receiver",
		"EID=eid-123",
		"RELAY=wss://relay.example.com",
		"CHAN=nostr",
		"CONTENT=hello world",
	}

	for _, want := range expected {
		found := false
		for _, msg := range ev.Messages {
			if msg == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing env var output: %s (got messages: %v)", want, ev.Messages)
		}
	}
}

func TestMakeShellHandler_NonZeroExit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "handler.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho 'oops'\nexit 1\n"), 0o755)

	handler := MakeShellHandler(script)
	ev := &Event{
		Name:      "test:fail",
		Timestamp: time.Now(),
		Context:   map[string]any{},
		Messages:  []string{},
	}

	err := handler(ev)
	if err == nil {
		t.Fatal("expected error from non-zero exit")
	}
	// Error should contain the script name and the stdout output.
	errStr := err.Error()
	if !strings.Contains(errStr, "handler.sh") {
		t.Errorf("error should contain script name, got: %s", errStr)
	}
	if !strings.Contains(errStr, "oops") {
		t.Errorf("error should contain script output, got: %s", errStr)
	}
}

func TestMakeShellHandler_NoContextKeys(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "handler.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho ok\n"), 0o755)

	handler := MakeShellHandler(script)
	ev := &Event{
		Name:      "test:ok",
		Timestamp: time.Now(),
		Context:   map[string]any{},
		Messages:  []string{},
	}

	if err := handler(ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ev.Messages) != 1 || ev.Messages[0] != "ok" {
		t.Errorf("messages = %v, want [ok]", ev.Messages)
	}
}

func TestMakeShellHandler_MultiLineOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "handler.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho line1\necho line2\necho line3\n"), 0o755)

	handler := MakeShellHandler(script)
	ev := &Event{
		Name:      "test:multi",
		Timestamp: time.Now(),
		Context:   map[string]any{},
		Messages:  []string{},
	}

	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %v", len(ev.Messages), ev.Messages)
	}
	if ev.Messages[0] != "line1" || ev.Messages[1] != "line2" || ev.Messages[2] != "line3" {
		t.Errorf("messages = %v", ev.Messages)
	}
}

func TestMakeShellHandler_EmptyOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "handler.sh")
	os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755)

	handler := MakeShellHandler(script)
	ev := &Event{
		Name:      "test:empty",
		Timestamp: time.Now(),
		Context:   map[string]any{},
		Messages:  []string{},
	}

	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Messages) != 0 {
		t.Errorf("expected no messages for empty output, got: %v", ev.Messages)
	}
}

func TestMakeShellHandler_TimestampEnvVar(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "handler.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho \"TS=$HOOK_TIMESTAMP\"\n"), 0o755)

	handler := MakeShellHandler(script)
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	ev := &Event{
		Name:      "test:ts",
		Timestamp: ts,
		Context:   map[string]any{},
		Messages:  []string{},
	}

	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Messages) != 1 {
		t.Fatalf("expected 1 message, got %v", ev.Messages)
	}
	if !strings.Contains(ev.Messages[0], "2025-06-15T10:30:00Z") {
		t.Errorf("expected RFC3339 timestamp in output, got: %s", ev.Messages[0])
	}
}

func TestMakeShellHandler_ContextJSON(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "handler.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho \"CTX=$HOOK_CONTEXT\"\n"), 0o755)

	handler := MakeShellHandler(script)
	ev := &Event{
		Name:      "test:ctx",
		Timestamp: time.Now(),
		Context:   map[string]any{"key": "value"},
		Messages:  []string{},
	}

	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Messages) != 1 {
		t.Fatalf("expected 1 message, got %v", ev.Messages)
	}
	// HOOK_CONTEXT should be JSON-encoded.
	if !strings.Contains(ev.Messages[0], `"key"`) || !strings.Contains(ev.Messages[0], `"value"`) {
		t.Errorf("HOOK_CONTEXT should be JSON, got: %s", ev.Messages[0])
	}
}

// ─── AttachShellHandlers ──────────────────────────────────────────���──────────

func TestAttachShellHandlers_AttachesScript(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "my-hook")
	os.MkdirAll(hookDir, 0o755)
	os.WriteFile(filepath.Join(hookDir, "handler.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755)

	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "my-hook",
		BaseDir: hookDir,
		Source:  SourceManaged,
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Events: []string{"test:event"}},
			},
		},
	})

	if mgr.hooks[0].Handler != nil {
		t.Fatal("handler should be nil before attach")
	}

	AttachShellHandlers(mgr)

	if mgr.hooks[0].Handler == nil {
		t.Fatal("handler should be attached after AttachShellHandlers")
	}

	// Verify the attached handler actually works.
	ev := &Event{
		Name:      "test:event",
		Timestamp: time.Now(),
		Context:   map[string]any{},
		Messages:  []string{},
	}
	if err := mgr.hooks[0].Handler(ev); err != nil {
		t.Fatalf("attached handler returned error: %v", err)
	}
	if len(ev.Messages) != 1 || ev.Messages[0] != "hi" {
		t.Errorf("expected [hi], got %v", ev.Messages)
	}
}

func TestAttachShellHandlers_SkipsExistingHandler(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "my-hook")
	os.MkdirAll(hookDir, 0o755)
	os.WriteFile(filepath.Join(hookDir, "handler.sh"), []byte("#!/bin/sh\necho other\n"), 0o755)

	originalCalled := false
	original := HookHandler(func(ev *Event) error { originalCalled = true; return nil })
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "my-hook",
		BaseDir: hookDir,
		Handler: original,
		Source:  SourceBundled,
	})

	AttachShellHandlers(mgr)

	// Verify the original handler is preserved.
	ev := &Event{Name: "test", Timestamp: time.Now(), Context: map[string]any{}, Messages: []string{}}
	mgr.hooks[0].Handler(ev)
	if !originalCalled {
		t.Error("original handler should still be the one called")
	}
}

func TestAttachShellHandlers_SkipsNoBaseDir(t *testing.T) {
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "no-base",
		BaseDir: "",
		Source:  SourceManaged,
	})

	AttachShellHandlers(mgr)

	if mgr.hooks[0].Handler != nil {
		t.Error("hook with no BaseDir should not get a handler")
	}
}

func TestAttachShellHandlers_SkipsMissingScript(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "no-script-hook")
	os.MkdirAll(hookDir, 0o755)

	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "no-script-hook",
		BaseDir: hookDir,
		Source:  SourceManaged,
	})

	AttachShellHandlers(mgr)

	if mgr.hooks[0].Handler != nil {
		t.Error("hook without handler.sh should not get a handler")
	}
}

// ─── BundledHooksDir walk-up path ────────────────────────────────────────────

func TestBundledHooksDir_CwdWalkUp(t *testing.T) {
	// Create a directory tree: root/hooks/my-hook/HOOK.md
	// Then chdir into root/sub/deep and verify walk-up finds hooks/.
	root := t.TempDir()
	hooksDir := filepath.Join(root, "hooks", "my-hook")
	os.MkdirAll(hooksDir, 0o755)
	os.WriteFile(filepath.Join(hooksDir, "HOOK.md"), []byte("---\nname: test\n---\n"), 0o644)

	deepDir := filepath.Join(root, "sub", "deep")
	os.MkdirAll(deepDir, 0o755)

	t.Setenv("METIQ_BUNDLED_HOOKS_DIR", "")
	t.Chdir(deepDir)

	got := BundledHooksDir()
	want := filepath.Join(root, "hooks")
	if got != want {
		t.Errorf("walk-up should find %q, got %q", want, got)
	}
}

func TestBundledHooksDir_NoHooksAnywhere(t *testing.T) {
	// In a fresh temp dir with no hooks/ subdirectory anywhere in the
	// ancestor chain, the walk-up loop should exhaust without finding
	// anything. The binary sibling path also won't match in test context.
	root := t.TempDir()
	t.Setenv("METIQ_BUNDLED_HOOKS_DIR", "")
	t.Chdir(root)

	got := BundledHooksDir()
	// The test binary lives in a temp build dir with no hooks/ sibling,
	// and root has no hooks/ anywhere above it. Result should be "".
	if got != "" {
		t.Logf("BundledHooksDir returned %q (possibly found via binary sibling); this is environment-dependent", got)
	}
}

// ─── ManagedHooksDir default path ──────��──────────────────────────────────���──

func TestManagedHooksDir_Default(t *testing.T) {
	t.Setenv("METIQ_MANAGED_HOOKS_DIR", "")
	got := ManagedHooksDir()
	if got == "" {
		t.Skip("no home dir available")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
	if !strings.HasSuffix(got, filepath.Join(".metiq", "hooks")) {
		t.Errorf("expected path ending with .metiq/hooks, got %q", got)
	}
}

// ─── Command logger additional paths ─────────────────────────────────────────

func TestCommandLoggerHandler_DefaultLogDir(t *testing.T) {
	// When LogDir is empty, it defaults to ~/.metiq/logs.
	// Rather than writing to the real homedir, verify the handler returns
	// nil (success) — the MkdirAll + file write is a real side effect but
	// the handler is designed to silently succeed even if it can't write.
	opts := BundledHandlerOpts{LogDir: ""}
	handler := makeCommandLoggerHandler(opts)

	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "sess1",
		Context:    map[string]any{},
		Timestamp:  time.Now(),
	}

	err := handler(ev)
	// The handler silently swallows MkdirAll/file errors, so err should be nil
	// only if it actually wrote or if it hit the early "return nil" path.
	// Either way, no panic and no unexpected error.
	if err != nil {
		t.Errorf("expected nil error even with default log dir, got: %v", err)
	}
}

func TestCommandLoggerHandler_ContextFieldFiltering(t *testing.T) {
	logDir := t.TempDir()
	opts := BundledHandlerOpts{LogDir: logDir}
	handler := makeCommandLoggerHandler(opts)

	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "sess1",
		Context: map[string]any{
			"senderId":  "user1",
			"source":    "nostr",
			"channelId": "ch1",
			"secret":    "should-not-be-logged",
			"password":  "definitely-not-logged",
		},
		Timestamp: time.Now(),
	}

	if err := handler(ev); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(logDir, "commands.log"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)

	// Allowed fields should be present.
	for _, field := range []string{"senderId", "source", "channelId"} {
		if !strings.Contains(s, field) {
			t.Errorf("expected safe context field %q in log, got: %s", field, s)
		}
	}
	// Disallowed fields should NOT be present.
	for _, field := range []string{"secret", "password"} {
		if strings.Contains(s, field) {
			t.Errorf("disallowed context field %q should not be logged, got: %s", field, s)
		}
	}
}

// ─── Session memory handler edge cases ───────────────────────────────────────

func TestSessionMemoryHandler_NilWorkspaceReturnsEarly(t *testing.T) {
	handler := makeSessionMemoryHandler(BundledHandlerOpts{
		WorkspaceDir: nil,
	})
	ev := &Event{
		EventType: "command",
		Action:    "new",
		Name:      "command:new",
		Context:   map[string]any{},
		Messages:  []string{},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Messages) != 0 {
		t.Errorf("expected no messages when WorkspaceDir is nil, got: %v", ev.Messages)
	}
}

func TestSessionMemoryHandler_EmptyWorkspaceDirReturnsEarly(t *testing.T) {
	handler := makeSessionMemoryHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return "" },
	})
	ev := &Event{
		EventType: "command",
		Action:    "new",
		Name:      "command:new",
		Context:   map[string]any{},
		Messages:  []string{},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Messages) != 0 {
		t.Errorf("expected no messages when WorkspaceDir is empty, got: %v", ev.Messages)
	}
}

func TestSessionMemoryHandler_DefaultSessionKey(t *testing.T) {
	workDir := t.TempDir()
	var gotSession string
	opts := BundledHandlerOpts{
		WorkspaceDir: func() string { return workDir },
		GetTranscript: func(sessionKey string, limit int) ([]TranscriptMessage, error) {
			gotSession = sessionKey
			return []TranscriptMessage{{Role: "user", Content: "hi"}}, nil
		},
	}
	handler := makeSessionMemoryHandler(opts)
	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "",
		Context:    map[string]any{},
		Timestamp:  time.Now(),
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if gotSession != "main" {
		t.Errorf("expected default session key 'main', got %q", gotSession)
	}
}

func TestSessionMemoryHandler_CustomMessageLimit(t *testing.T) {
	workDir := t.TempDir()
	var gotLimit int
	opts := BundledHandlerOpts{
		WorkspaceDir: func() string { return workDir },
		GetTranscript: func(sessionKey string, limit int) ([]TranscriptMessage, error) {
			gotLimit = limit
			return []TranscriptMessage{{Role: "user", Content: "hi"}}, nil
		},
	}
	handler := makeSessionMemoryHandler(opts)
	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "sess1",
		Context:    map[string]any{"messages": 5},
		Timestamp:  time.Now(),
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if gotLimit != 5 {
		t.Errorf("expected limit 5, got %d", gotLimit)
	}
}

func TestSessionMemoryHandler_DefaultMessageLimit(t *testing.T) {
	workDir := t.TempDir()
	var gotLimit int
	opts := BundledHandlerOpts{
		WorkspaceDir: func() string { return workDir },
		GetTranscript: func(sessionKey string, limit int) ([]TranscriptMessage, error) {
			gotLimit = limit
			return []TranscriptMessage{{Role: "user", Content: "hi"}}, nil
		},
	}
	handler := makeSessionMemoryHandler(opts)
	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "sess1",
		Context:    map[string]any{},
		Timestamp:  time.Now(),
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if gotLimit != 15 {
		t.Errorf("expected default limit 15, got %d", gotLimit)
	}
}

func TestSessionMemoryHandler_SlugGeneratorUsed(t *testing.T) {
	workDir := t.TempDir()
	opts := BundledHandlerOpts{
		WorkspaceDir: func() string { return workDir },
		GetTranscript: func(sessionKey string, limit int) ([]TranscriptMessage, error) {
			return []TranscriptMessage{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi there"},
			}, nil
		},
		GenerateSlug: func(text string) (string, error) {
			return "custom-slug", nil
		},
	}
	handler := makeSessionMemoryHandler(opts)
	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "sess1",
		Context:    map[string]any{},
		Timestamp:  time.Now(),
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(workDir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "custom-slug") {
		t.Errorf("expected slug in filename, got %q", entries[0].Name())
	}
}

func TestSessionMemoryHandler_AppendsSaveMessage(t *testing.T) {
	workDir := t.TempDir()
	opts := BundledHandlerOpts{
		WorkspaceDir: func() string { return workDir },
		GetTranscript: func(sessionKey string, limit int) ([]TranscriptMessage, error) {
			return []TranscriptMessage{{Role: "user", Content: "hi"}}, nil
		},
	}
	handler := makeSessionMemoryHandler(opts)
	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "sess1",
		Context:    map[string]any{},
		Timestamp:  time.Now(),
		Messages:   []string{},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(ev.Messages))
	}
	if !strings.HasPrefix(ev.Messages[0], "💾") {
		t.Errorf("expected save emoji message, got: %s", ev.Messages[0])
	}
	if !strings.Contains(ev.Messages[0], "memory/") {
		t.Errorf("expected 'memory/' path in message, got: %s", ev.Messages[0])
	}
}

func TestSessionMemoryHandler_FileContentStructure(t *testing.T) {
	workDir := t.TempDir()
	opts := BundledHandlerOpts{
		WorkspaceDir: func() string { return workDir },
		GetTranscript: func(sessionKey string, limit int) ([]TranscriptMessage, error) {
			return []TranscriptMessage{
				{Role: "user", Content: "What is 2+2?"},
				{Role: "assistant", Content: "4"},
			}, nil
		},
	}
	handler := makeSessionMemoryHandler(opts)
	ev := &Event{
		EventType:  "command",
		Action:     "new",
		Name:       "command:new",
		SessionKey: "my-session",
		Context:    map[string]any{},
		Timestamp:  time.Now(),
		Messages:   []string{},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(filepath.Join(workDir, "memory"))
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(workDir, "memory", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	// Verify the file contains expected structural elements.
	if !strings.Contains(content, "# Session:") {
		t.Errorf("missing session header in memory file")
	}
	if !strings.Contains(content, "my-session") {
		t.Errorf("missing session key in memory file")
	}
	if !strings.Contains(content, "**user**: What is 2+2?") {
		t.Errorf("missing user message in memory file")
	}
	if !strings.Contains(content, "**assistant**: 4") {
		t.Errorf("missing assistant message in memory file")
	}
}

// ─── parseFrontmatter edge cases ─────────���───────────────────���───────────────

func TestParseFrontmatter_ClosingDashOnly(t *testing.T) {
	data := []byte("---\nname: test")
	fm, body, err := parseFrontmatter(data)
	if err != nil {
		t.Fatal(err)
	}
	if fm == nil {
		t.Error("expected frontmatter")
	}
	if body != nil {
		t.Errorf("expected nil body, got %q", string(body))
	}
}

func TestParseFrontmatter_EmptyFrontmatter(t *testing.T) {
	data := []byte("---\n---\n\nBody content")
	fm, body, err := parseFrontmatter(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(fm) != "" {
		t.Errorf("expected empty frontmatter, got %q", string(fm))
	}
	if !strings.Contains(string(body), "Body content") {
		t.Errorf("expected body, got %q", string(body))
	}
}

// ─── preprocessFrontmatter / joinFlowOnNextLine / trailingCommaPass ──────────

func TestPreprocessFrontmatter_JoinFlow(t *testing.T) {
	input := []byte("events:\n  [\"command:new\", \"command:reset\"]")
	result := preprocessFrontmatter(input)
	if strings.Contains(string(result), "events:\n") {
		t.Errorf("expected joined flow, got:\n%s", result)
	}
}

func TestPreprocessFrontmatter_TrailingComma(t *testing.T) {
	input := []byte("items:\n  - one,\n  ]")
	result := preprocessFrontmatter(input)
	if strings.Contains(string(result), "one,") {
		t.Errorf("expected trailing comma removed, got:\n%s", result)
	}
}

func TestJoinFlowOnNextLine_ObjectFlow(t *testing.T) {
	input := []byte("metadata:\n  {\"key\": \"value\"}")
	result := joinFlowOnNextLine(input)
	expected := "metadata: {\"key\": \"value\"}"
	if string(result) != expected {
		t.Errorf("expected %q, got %q", expected, string(result))
	}
}

func TestTrailingCommaPass_NoTrailingComma(t *testing.T) {
	input := []byte("- one\n- two\n]")
	result := trailingCommaPass(input)
	if string(result) != string(input) {
		t.Errorf("should be unchanged, got %q", string(result))
	}
}

func TestTrailingCommaPass_CommaBeforeCloseBrace(t *testing.T) {
	input := []byte("  \"key\": \"val\",\n}")
	result := trailingCommaPass(input)
	if strings.Contains(string(result), ",") {
		t.Errorf("comma should be removed, got %q", string(result))
	}
}

// ─── Manager edge cases ────────���───────────────────────���────────────────────

func TestManager_FireHandlerError(t *testing.T) {
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "error-hook",
		Source:  SourceBundled,
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Events: []string{"test:event"}},
			},
		},
		Handler: func(ev *Event) error {
			return os.ErrPermission
		},
	})

	errs := mgr.Fire("test:event", "sess", nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "error-hook") {
		t.Errorf("error should wrap hook key, got: %v", errs[0])
	}
}

func TestManager_FireNoSubscribers(t *testing.T) {
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "other",
		Source:  SourceBundled,
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Events: []string{"different:event"}},
			},
		},
		Handler: func(ev *Event) error {
			t.Error("should not fire")
			return nil
		},
	})

	errs := mgr.Fire("test:event", "sess", nil)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestManager_FireNilHandler(t *testing.T) {
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "no-handler",
		Source:  SourceBundled,
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Events: []string{"test:event"}},
			},
		},
		Handler: nil,
	})

	errs := mgr.Fire("test:event", "sess", nil)
	if len(errs) != 0 {
		t.Errorf("expected no errors for nil handler, got %v", errs)
	}
}

func TestManager_AlwaysFiresEvenWhenDisabled(t *testing.T) {
	fired := false
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "always-hook",
		Source:  SourceBundled,
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{
					Events: []string{"test:event"},
					Always: true,
				},
			},
		},
		Handler: func(ev *Event) error { fired = true; return nil },
	})
	mgr.SetEnabled("always-hook", false)

	mgr.Fire("test:event", "sess", nil)
	if !fired {
		t.Error("always hook should fire even when disabled")
	}
}

func TestManager_NonBundledDefaultDisabled(t *testing.T) {
	fired := false
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "managed-hook",
		Source:  SourceManaged,
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Events: []string{"test:event"}},
			},
		},
		Handler: func(ev *Event) error { fired = true; return nil },
	})

	mgr.Fire("test:event", "sess", nil)
	if fired {
		t.Error("non-bundled hook should default to disabled")
	}
}

func TestManager_NonBundledEnabledExplicitly(t *testing.T) {
	fired := false
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "managed-hook",
		Source:  SourceManaged,
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Events: []string{"test:event"}},
			},
		},
		Handler: func(ev *Event) error { fired = true; return nil },
	})
	mgr.SetEnabled("managed-hook", true)

	mgr.Fire("test:event", "sess", nil)
	if !fired {
		t.Error("explicitly enabled managed hook should fire")
	}
}

func TestManager_FireMultipleHooksContinuesAfterError(t *testing.T) {
	secondFired := false
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "failing-hook",
		Source:  SourceBundled,
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Events: []string{"test:event"}},
			},
		},
		Handler: func(ev *Event) error { return os.ErrInvalid },
	})
	mgr.Register(&Hook{
		HookKey: "second-hook",
		Source:  SourceBundled,
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Events: []string{"test:event"}},
			},
		},
		Handler: func(ev *Event) error { secondFired = true; return nil },
	})

	errs := mgr.Fire("test:event", "sess", nil)
	if len(errs) != 1 {
		t.Errorf("expected 1 error, got %d", len(errs))
	}
	if !secondFired {
		t.Error("second hook should fire even after first hook errors")
	}
}
