package hooks

import (
	"context"
	"strings"
	"testing"
	"time"

	"metiq/internal/plugins/registry"
	"metiq/internal/sandbox"
)

type recordingHookSandbox struct {
	driver string
	result sandbox.Result
	run    func(context.Context) (sandbox.Result, error)
	cmd    []string
	env    []string
	dir    string
}

func (s *recordingHookSandbox) Driver() string { return s.driver }
func (s *recordingHookSandbox) Run(ctx context.Context, cmd, env []string, dir string) (sandbox.Result, error) {
	s.cmd = append([]string{}, cmd...)
	s.env = append([]string{}, env...)
	s.dir = dir
	if s.run != nil {
		return s.run(ctx)
	}
	return s.result, nil
}

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

func TestClaudeCommandHookInvokeUsesSandboxAndSanitizedEnv(t *testing.T) {
	t.Setenv("DAEMON_SECRET_TOKEN", "must-not-leak")
	runner := &recordingHookSandbox{
		driver: "docker",
		result: sandbox.Result{Stdout: "/plugin", ExitCode: 0, Driver: "docker"},
	}
	hook := ClaudeCommandHook{
		PluginRoot:     t.TempDir(),
		Command:        "printf '%s' \"${CLAUDE_PLUGIN_ROOT}\"",
		TimeoutSeconds: 5,
		SandboxRunner:  runner,
	}
	result, err := hook.Invoke(context.Background(), map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "/plugin" {
		t.Fatalf("stdout=%q", result.Stdout)
	}
	if runner.dir != "/plugin" || len(runner.cmd) != 3 || runner.cmd[0] != "/bin/sh" || runner.cmd[1] != "-c" {
		t.Fatalf("sandbox invocation cmd=%q dir=%q", runner.cmd, runner.dir)
	}
	joined := strings.Join(runner.env, "\n")
	for _, required := range []string{"CLAUDE_PLUGIN_ROOT=/plugin", `METIQ_HOOK_PAYLOAD={"ok":true}`, "METIQ_HOOK_COMMAND=printf"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("sanitized env missing %q: %q", required, runner.env)
		}
	}
	if strings.Contains(joined, "DAEMON_SECRET_TOKEN") || strings.Contains(joined, "must-not-leak") {
		t.Fatalf("daemon environment leaked into hook: %q", runner.env)
	}
}

func TestClaudeCommandHookRejectsUnsafeRunner(t *testing.T) {
	hook := ClaudeCommandHook{Command: "true", SandboxRunner: &recordingHookSandbox{driver: "nop"}}
	_, err := hook.Invoke(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "unsafe nop") {
		t.Fatalf("expected unsafe driver error, got %v", err)
	}
}

func TestClaudeCommandHookRejectsUnsafeResult(t *testing.T) {
	hook := ClaudeCommandHook{Command: "true", SandboxRunner: &recordingHookSandbox{
		driver: "custom",
		result: sandbox.Result{Driver: "custom", Unsafe: true},
	}}
	_, err := hook.Invoke(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "unsafe sandbox result") {
		t.Fatalf("expected unsafe result error, got %v", err)
	}
}

func TestClaudeCommandHookTimeout(t *testing.T) {
	runner := &recordingHookSandbox{driver: "docker", run: func(ctx context.Context) (sandbox.Result, error) {
		<-ctx.Done()
		return sandbox.Result{Driver: "docker", TimedOut: true, ExitCode: -1}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := (ClaudeCommandHook{Command: "sleep 2", SandboxRunner: runner}).Invoke(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestClaudeCommandHookNonZeroExitFails(t *testing.T) {
	hook := ClaudeCommandHook{Command: "false", SandboxRunner: &recordingHookSandbox{
		driver: "docker",
		result: sandbox.Result{Driver: "docker", ExitCode: 17, Stderr: "denied"},
	}}
	result, err := hook.Invoke(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "exit code 17") || result.Stderr != "denied" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
