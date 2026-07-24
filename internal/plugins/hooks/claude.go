package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"metiq/internal/plugins/registry"
	"metiq/internal/sandbox"
)

const defaultClaudePluginSandboxRoot = "/plugin"

type ClaudeCommandHook struct {
	PluginRoot     string
	Command        string
	TimeoutSeconds int

	// SandboxRunner may be injected by the host. When nil, Invoke constructs a
	// hardened one-shot Docker sandbox from SandboxConfig. The nop driver is
	// always rejected for command hooks because it provides no isolation.
	SandboxRunner sandbox.SandboxRunner
	SandboxConfig sandbox.Config
	// PluginRootInSandbox is the read-only mount path visible to the hook.
	// It defaults to /plugin.
	PluginRootInSandbox string
}

type ClaudeCommandResult struct {
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
}

func MapClaudeHookEvent(event string) (registry.HookEvent, bool) {
	switch strings.TrimSpace(event) {
	case "PreToolUse":
		return registry.HookBeforeToolCall, true
	case "PostToolUse":
		return registry.HookAfterToolCall, true
	case "Stop":
		return registry.HookAgentEnd, true
	case "UserPromptSubmit":
		return registry.HookAgentTurnPrepare, true
	case "SessionStart":
		return registry.HookSessionStart, true
	default:
		return "", false
	}
}

func ExpandClaudePluginRoot(command, pluginRoot string) string {
	return strings.ReplaceAll(command, "${CLAUDE_PLUGIN_ROOT}", pluginRoot)
}

func (h ClaudeCommandHook) Invoke(ctx context.Context, payload any) (ClaudeCommandResult, error) {
	pluginRoot := strings.TrimSpace(h.PluginRootInSandbox)
	if pluginRoot == "" {
		pluginRoot = defaultClaudePluginSandboxRoot
	}
	command := strings.TrimSpace(ExpandClaudePluginRoot(h.Command, pluginRoot))
	if command == "" {
		return ClaudeCommandResult{}, fmt.Errorf("Claude command hook missing command")
	}
	if h.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(h.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	runner, err := h.sandboxRunner(pluginRoot)
	if err != nil {
		return ClaudeCommandResult{}, err
	}
	if strings.EqualFold(strings.TrimSpace(runner.Driver()), "nop") {
		return ClaudeCommandResult{}, fmt.Errorf("Claude command hook refused unsafe nop sandbox driver")
	}

	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/tmp",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"CLAUDE_PLUGIN_ROOT=" + pluginRoot,
		"METIQ_HOOK_COMMAND=" + command,
	}
	shellProgram := `exec /bin/sh -c "$METIQ_HOOK_COMMAND"`
	if payload != nil {
		data, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return ClaudeCommandResult{}, fmt.Errorf("marshal Claude hook payload: %w", marshalErr)
		}
		env = append(env, "METIQ_HOOK_PAYLOAD="+string(data))
		shellProgram = `printf '%s' "$METIQ_HOOK_PAYLOAD" | /bin/sh -c "$METIQ_HOOK_COMMAND"`
	}

	res, err := runner.Run(ctx, []string{"/bin/sh", "-c", shellProgram}, env, pluginRoot)
	result := ClaudeCommandResult{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode}
	if err != nil {
		return result, fmt.Errorf("Claude command hook sandbox failed: %w", err)
	}
	if res.Unsafe {
		return result, fmt.Errorf("Claude command hook refused unsafe sandbox result")
	}
	if res.TimedOut || ctx.Err() != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("Claude command hook timed out: %w", ctx.Err())
		}
		return result, fmt.Errorf("Claude command hook timed out")
	}
	if res.ExitCode != 0 {
		return result, fmt.Errorf("Claude command hook failed with exit code %d", res.ExitCode)
	}
	return result, nil
}

func (h ClaudeCommandHook) sandboxRunner(pluginRoot string) (sandbox.SandboxRunner, error) {
	if h.SandboxRunner != nil {
		return h.SandboxRunner, nil
	}
	cfg := h.SandboxConfig
	if strings.EqualFold(strings.TrimSpace(cfg.Driver), "nop") || cfg.AllowUnsafeNop {
		return nil, fmt.Errorf("Claude command hook refused unsafe nop sandbox configuration")
	}
	cfg.Driver = "docker"
	cfg.AllowUnsafeNop = false
	cfg.PersistentRuntime = false
	cfg.WritableRootFS = false
	cfg.ReadOnlyRootFS = true
	cfg.NetworkDisabled = true
	cfg.AllowNetwork = false
	cfg.WorkspaceAccess = "read_only"
	cfg.ContainerWorkdir = pluginRoot
	if strings.TrimSpace(h.PluginRoot) != "" {
		cfg.WorkspaceDir = h.PluginRoot
	}
	if h.TimeoutSeconds > 0 {
		cfg.TimeoutSeconds = h.TimeoutSeconds
	}
	runner, err := sandbox.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("create Claude command hook sandbox: %w", err)
	}
	return runner, nil
}
