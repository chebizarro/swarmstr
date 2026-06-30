package hooks

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"metiq/internal/plugins/registry"
)

func TestMapClaudeHookEventAndExpansion(t *testing.T) {
	tests := map[string]registry.HookEvent{
		"PreToolUse":       registry.HookBeforeToolCall,
		"PostToolUse":      registry.HookAfterToolCall,
		"Stop":             registry.HookAgentEnd,
		"UserPromptSubmit": registry.HookAgentTurnPrepare,
	}
	for in, want := range tests {
		got, ok := MapClaudeHookEvent(in)
		if !ok || got != want {
			t.Fatalf("MapClaudeHookEvent(%q)=(%q,%v), want (%q,true)", in, got, ok, want)
		}
	}
	if got := ExpandClaudePluginRoot("${CLAUDE_PLUGIN_ROOT}/hooks/a.sh", "/tmp/plugin"); got != "/tmp/plugin/hooks/a.sh" {
		t.Fatalf("expanded=%q", got)
	}
}

func TestClaudeCommandHookInvoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell quoting differs on windows")
	}
	root := filepath.Join(t.TempDir(), "plugin root")
	hook := ClaudeCommandHook{PluginRoot: root, Command: "printf '%s' \"${CLAUDE_PLUGIN_ROOT}\"", TimeoutSeconds: 5}
	result, err := hook.Invoke(context.Background(), map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != root {
		t.Fatalf("stdout=%q want %q", result.Stdout, root)
	}
}

func TestClaudeCommandHookTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command differs on windows")
	}
	hook := ClaudeCommandHook{Command: "sleep 2", TimeoutSeconds: 1}
	_, err := hook.Invoke(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}
