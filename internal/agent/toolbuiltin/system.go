// Package toolbuiltin/system provides basic system-awareness tools:
//   - current_time   → returns ISO-8601 UTC timestamp + local offset
//   - my_identity    → returns agent's configured identity (name, pubkey, model)
//   - bash_exec      → executes a shell command (gated by exec approval policy)
package toolbuiltin

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"swarmstr/internal/agent"
)

// ─── current_time ─────────────────────────────────────────────────────────────

// CurrentTimeTool returns the current UTC time and Unix timestamp.
func CurrentTimeTool(_ context.Context, _ map[string]any) (string, error) {
	now := time.Now().UTC()
	return fmt.Sprintf("UTC: %s  Unix: %d", now.Format(time.RFC3339), now.Unix()), nil
}

var CurrentTimeDef = agent.ToolDefinition{
	Name:        "current_time",
	Description: "Returns the current UTC date/time and Unix timestamp. Use this whenever you need to know the current time, date, or need to compute time differences.",
	Parameters:  agent.ToolParameters{Type: "object"},
}

// ─── my_identity ──────────────────────────────────────────────────────────────

// IdentityInfo is populated by main during startup so the tool can return live values.
type IdentityInfo struct {
	Name   string
	Pubkey string // hex nostr pubkey
	Model  string
}

var identityInfo IdentityInfo

// SetIdentityInfo configures the identity returned by my_identity.
// Call this once from main after config is loaded.
func SetIdentityInfo(info IdentityInfo) {
	identityInfo = info
}

// MyIdentityTool returns the agent's configured identity metadata.
func MyIdentityTool(_ context.Context, _ map[string]any) (string, error) {
	var parts []string
	if identityInfo.Name != "" {
		parts = append(parts, "name: "+identityInfo.Name)
	}
	if identityInfo.Pubkey != "" {
		parts = append(parts, "nostr_pubkey: "+identityInfo.Pubkey)
	}
	if identityInfo.Model != "" {
		parts = append(parts, "model: "+identityInfo.Model)
	}
	if len(parts) == 0 {
		return "identity not configured", nil
	}
	return strings.Join(parts, "\n"), nil
}

var MyIdentityDef = agent.ToolDefinition{
	Name:        "my_identity",
	Description: "Returns this agent's own identity: its name, Nostr public key (hex), and the LLM model it uses. Useful for self-referential tasks like publishing notes signed as this agent or routing replies.",
	Parameters:  agent.ToolParameters{Type: "object"},
}

// ─── bash_exec ────────────────────────────────────────────────────────────────

// BashExecTool executes a shell command and returns combined stdout+stderr.
// The command is run via /bin/sh -c. Execution is time-bounded to 30 seconds.
// IMPORTANT: this tool should only be registered when exec_approval is enabled
// in the agent config — the policy gate is enforced at the ToolMiddleware level.
func BashExecTool(ctx context.Context, args map[string]any) (string, error) {
	command := agent.ArgString(args, "command")
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("bash_exec: 'command' argument is required")
	}

	// Use a timeout derived from args or default 30s.
	timeout := 30 * time.Second
	if t := agent.ArgInt(args, "timeout_seconds", 0); t > 0 && t <= 300 {
		timeout = time.Duration(t) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("bash_exec: command timed out after %v", timeout)
		}
		if result != "" {
			return result, fmt.Errorf("exit error: %v", err)
		}
		return "", fmt.Errorf("bash_exec: %v", err)
	}
	if result == "" {
		return "(no output)", nil
	}
	return result, nil
}

var BashExecDef = agent.ToolDefinition{
	Name:        "bash_exec",
	Description: "Execute a shell command via /bin/sh and return the combined stdout+stderr output. Use for running scripts, inspecting files, calling system tools, or any task requiring shell access. Commands are time-limited (default 30s, max 300s).",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"command": {
				Type:        "string",
				Description: "The shell command to execute, e.g. \"ls -la /tmp\" or \"python3 script.py\"",
			},
			"timeout_seconds": {
				Type:        "integer",
				Description: "Maximum execution time in seconds (1–300). Defaults to 30.",
			},
		},
		Required: []string{"command"},
	},
}
