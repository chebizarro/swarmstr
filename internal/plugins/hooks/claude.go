package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"metiq/internal/plugins/registry"
)

type ClaudeCommandHook struct {
	PluginRoot     string
	Command        string
	TimeoutSeconds int
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
	command := strings.TrimSpace(ExpandClaudePluginRoot(h.Command, h.PluginRoot))
	if command == "" {
		return ClaudeCommandResult{}, fmt.Errorf("Claude command hook missing command")
	}
	if h.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(h.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Env = append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+h.PluginRoot)
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return ClaudeCommandResult{}, fmt.Errorf("marshal Claude hook payload: %w", err)
		}
		cmd.Stdin = bytes.NewReader(data)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := ClaudeCommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("Claude command hook timed out: %w", ctx.Err())
	}
	if err != nil {
		return result, fmt.Errorf("Claude command hook failed: %w", err)
	}
	return result, nil
}
