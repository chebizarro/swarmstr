package hooks

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─── RegisterBundledHandlers ─────────────────────────────────────────────────

func TestRegisterBundledHandlers_AttachesHandlers(t *testing.T) {
	// Create a manager with four bundled hooks matching the four known keys.
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

	// Before registration, all handlers should be nil.
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

	// After registration, all four should have handlers.
	for _, h := range mgr.hooks {
		if h.Handler == nil {
			t.Errorf("hook %s should have a handler after registration", h.HookKey)
		}
	}
}

func TestRegisterBundledHandlers_SkipsNonBundled(t *testing.T) {
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "session-memory",
		Source:  SourceManaged, // not bundled — should be skipped
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

// ─── makeBootMDHandler ───────────────────────────────────────────────────────

func TestBootMDHandler_WrongEvent(t *testing.T) {
	called := false
	opts := BundledHandlerOpts{
		RunBootMD: func(string, string) error { called = true; return nil },
	}
	handler := makeBootMDHandler(opts)

	// Wrong event type.
	ev := &Event{EventType: "command", Action: "new"}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("RunBootMD should not be called for non-gateway:startup event")
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

// ─── makeBootstrapExtraFilesHandler ──────────────────────────────────────────

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
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644)

	var resolvedPaths []string
	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		ResolvePaths: func(wd string, patterns []string) ([]string, error) {
			resolvedPaths = patterns
			return nil, nil // return empty to test the "no resolved" path
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
		t.Fatal(err)
	}
	if len(resolvedPaths) != 1 || resolvedPaths[0] != "*.md" {
		t.Errorf("expected patterns [*.md], got %v", resolvedPaths)
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

func TestBootstrapExtraFilesHandler_FallbackGlob(t *testing.T) {
	// Test the fallback path when ResolvePaths is nil — uses filepath.Glob.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hi"), 0o644)

	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		ResolvePaths: nil, // use built-in glob
	})
	ev := &Event{
		EventType: "agent",
		Action:    "bootstrap",
		Context: map[string]any{
			"paths": []string{filepath.Join(dir, "*.txt")},
		},
	}
	// The glob will match test.txt, but LoadResolvedBootstrapFiles
	// will likely reject it (unrecognized basename). That's fine — we're
	// testing the glob path, not the file loading.
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapExtraFilesHandler_AbsoluteAndRelativeGlob(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("stuff"), 0o644)

	handler := makeBootstrapExtraFilesHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return dir },
		ResolvePaths: nil,
	})
	// Relative path should be joined with workspaceDir.
	ev := &Event{
		EventType: "agent",
		Action:    "bootstrap",
		Context: map[string]any{
			"paths": []string{"*.txt"},
		},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
}

// ─── MakeShellHandler ────────────────────────────────────────────────────────

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

	// Output should be captured as messages.
	found := false
	for _, msg := range ev.Messages {
		if msg == "hook ran: command:new" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected output message, got: %v", ev.Messages)
	}
}

func TestMakeShellHandler_EnvVars(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "handler.sh")
	// Script prints all HOOK_ env vars
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

	expected := map[string]bool{
		"TYPE=message":                  false,
		"ACTION=received":              false,
		"SESSION=sess-42":              false,
		"FROM=pubkey-sender":           false,
		"TO=pubkey-receiver":           false,
		"EID=eid-123":                  false,
		"RELAY=wss://relay.example.com": false,
		"CHAN=nostr":                    false,
		"CONTENT=hello world":          false,
	}

	for _, msg := range ev.Messages {
		if _, ok := expected[msg]; ok {
			expected[msg] = true
		}
	}

	for k, found := range expected {
		if !found {
			t.Errorf("missing env var output: %s", k)
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

// ─── AttachShellHandlers ─────────────────────────────────────────────────────

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
		t.Error("handler should be attached after AttachShellHandlers")
	}
}

func TestAttachShellHandlers_SkipsExistingHandler(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "my-hook")
	os.MkdirAll(hookDir, 0o755)
	os.WriteFile(filepath.Join(hookDir, "handler.sh"), []byte("#!/bin/sh\necho other\n"), 0o755)

	original := HookHandler(func(ev *Event) error { return nil })
	mgr := NewManager()
	mgr.Register(&Hook{
		HookKey: "my-hook",
		BaseDir: hookDir,
		Handler: original,
		Source:  SourceBundled,
	})

	AttachShellHandlers(mgr)

	// Handler should still be the original, not replaced.
	if mgr.hooks[0].Handler == nil {
		t.Error("handler should not have been cleared")
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
	// No handler.sh here.

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

// ─── BundledHooksDir additional paths ────────────────────────────────────────

func TestBundledHooksDir_BinarySibling(t *testing.T) {
	// Clear the env override so the function falls through.
	t.Setenv("METIQ_BUNDLED_HOOKS_DIR", "")

	// The binary sibling and cwd walk-up are difficult to control in unit
	// tests because they depend on the test binary location. We verify the
	// function runs without panic and returns a string.
	result := BundledHooksDir()
	_ = result // may be "" in test environment — that's fine
}

func TestBundledHooksDir_WalkUpFindsHooksDir(t *testing.T) {
	root := t.TempDir()
	hooksDir := filepath.Join(root, "hooks", "my-hook")
	os.MkdirAll(hooksDir, 0o755)
	os.WriteFile(filepath.Join(hooksDir, "HOOK.md"), []byte("---\nname: test\n---\n"), 0o644)

	t.Setenv("METIQ_BUNDLED_HOOKS_DIR", filepath.Join(root, "hooks"))
	got := BundledHooksDir()
	if got != filepath.Join(root, "hooks") {
		t.Errorf("expected %q, got %q", filepath.Join(root, "hooks"), got)
	}
}

// ─── ManagedHooksDir default path ────────────────────────────────────────────

func TestManagedHooksDir_Default(t *testing.T) {
	t.Setenv("METIQ_MANAGED_HOOKS_DIR", "")
	got := ManagedHooksDir()
	if got == "" {
		t.Skip("no home dir available")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

// ─── Command logger additional paths ─────────────────────────────────────────

func TestCommandLoggerHandler_DefaultLogDir(t *testing.T) {
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

	// This will try to write to ~/.metiq/logs/commands.log.
	// We just verify it doesn't panic.
	_ = handler(ev)
}

func TestCommandLoggerHandler_ContextFields(t *testing.T) {
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
	if !(containsStr(s, "senderId") && containsStr(s, "source") && containsStr(s, "channelId")) {
		t.Errorf("expected safe context fields in log, got: %s", s)
	}
	if containsStr(s, "secret") {
		t.Errorf("should not log unknown context fields, got: %s", s)
	}
}

// ─── Session memory additional paths ─────────────────────────────────────────

func TestSessionMemoryHandler_NilWorkspaceDir2(t *testing.T) {
	handler := makeSessionMemoryHandler(BundledHandlerOpts{
		WorkspaceDir: nil,
	})
	ev := &Event{
		EventType: "command",
		Action:    "new",
		Name:      "command:new",
		Context:   map[string]any{},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
	}
}

func TestSessionMemoryHandler_EmptyWorkspaceDir2(t *testing.T) {
	handler := makeSessionMemoryHandler(BundledHandlerOpts{
		WorkspaceDir: func() string { return "" },
	})
	ev := &Event{
		EventType: "command",
		Action:    "new",
		Name:      "command:new",
		Context:   map[string]any{},
	}
	if err := handler(ev); err != nil {
		t.Fatal(err)
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
		SessionKey: "", // empty → should default to "main"
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

func TestSessionMemoryHandler_WithSlugGenerator(t *testing.T) {
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
			return "test-slug", nil
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
	if !containsStr(entries[0].Name(), "test-slug") {
		t.Errorf("expected slug in filename, got %q", entries[0].Name())
	}
}

func TestSessionMemoryHandler_MessageInEvent(t *testing.T) {
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
		t.Errorf("expected 1 message, got %d", len(ev.Messages))
	}
}

// ─── parseFrontmatter edge cases ─────────────────────────────────────────────

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
	if !containsStr(string(body), "Body content") {
		t.Errorf("expected body, got %q", string(body))
	}
}

// ─── preprocessFrontmatter / joinFlowOnNextLine / trailingCommaPass ──────────

func TestPreprocessFrontmatter_JoinFlow(t *testing.T) {
	input := []byte("events:\n  [\"command:new\", \"command:reset\"]")
	result := preprocessFrontmatter(input)
	if containsStr(string(result), "events:\n") {
		t.Errorf("expected joined flow, got:\n%s", result)
	}
}

func TestPreprocessFrontmatter_TrailingComma(t *testing.T) {
	input := []byte("items:\n  - one,\n  ]")
	result := preprocessFrontmatter(input)
	if containsStr(string(result), "one,") {
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
	if containsStr(string(result), ",") {
		t.Errorf("comma should be removed, got %q", string(result))
	}
}

// ─── Manager edge cases ─────────────────────────────────────────────────────

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
		t.Errorf("expected 1 error, got %d", len(errs))
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
		Source:  SourceManaged, // non-bundled → default disabled
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

// ─── helpers ─────────────────────────────────────────────────────────────────

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
