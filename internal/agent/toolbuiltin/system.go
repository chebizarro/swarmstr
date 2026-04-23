// Package toolbuiltin/system provides basic system-awareness tools:
//   - current_time   → returns ISO-8601 UTC timestamp + local offset
//   - my_identity    → returns agent's configured identity (name, pubkey, model)
//   - bash_exec      → executes a shell command (gated by exec approval policy)
package toolbuiltin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
	"metiq/internal/agent"
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
	NPub   string // bech32 npub
	Model  string
}

var (
	identityInfoMu sync.RWMutex
	identityInfo   IdentityInfo
)

// NostrNPubFromHex converts a hex pubkey to its bech32 npub form.
func NostrNPubFromHex(hexPubkey string) string {
	hexPubkey = strings.TrimSpace(hexPubkey)
	if hexPubkey == "" {
		return ""
	}
	pk, err := nostr.PubKeyFromHex(hexPubkey)
	if err != nil {
		return ""
	}
	return nip19.EncodeNpub(pk)
}

// SetIdentityInfo configures the identity returned by my_identity.
// Call this once from main after config is loaded.
func SetIdentityInfo(info IdentityInfo) {
	info.Name = strings.TrimSpace(info.Name)
	info.Pubkey = strings.TrimSpace(info.Pubkey)
	info.NPub = strings.TrimSpace(info.NPub)
	info.Model = strings.TrimSpace(info.Model)
	if info.NPub == "" && info.Pubkey != "" {
		info.NPub = NostrNPubFromHex(info.Pubkey)
	}
	identityInfoMu.Lock()
	identityInfo = info
	identityInfoMu.Unlock()
}

// MyIdentityTool returns the agent's configured identity metadata.
func MyIdentityTool(_ context.Context, _ map[string]any) (string, error) {
	identityInfoMu.RLock()
	info := identityInfo
	identityInfoMu.RUnlock()
	var parts []string
	if info.Name != "" {
		parts = append(parts, "name: "+info.Name)
	}
	if info.Pubkey != "" {
		parts = append(parts, "nostr_pubkey: "+info.Pubkey)
	}
	if info.NPub != "" {
		parts = append(parts, "nostr_npub: "+info.NPub)
	}
	if info.Model != "" {
		parts = append(parts, "model: "+info.Model)
	}
	if len(parts) == 0 {
		return "identity not configured", nil
	}
	return strings.Join(parts, "\n"), nil
}

var MyIdentityDef = agent.ToolDefinition{
	Name:        "my_identity",
	Description: "Returns this agent's own identity: its name, Nostr public key in both hex and npub form, and the LLM model it uses. Useful for self-referential tasks like publishing notes signed as this agent or routing replies.",
	Parameters:  agent.ToolParameters{Type: "object"},
}

// ─── bash_exec ────────────────────────────────────────────────────────────────

// bashExecResult is the structured response from bash_exec.
type bashExecResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out,omitempty"`
}

// BashExecTool executes a shell command and returns structured output.
// The command is run via /bin/sh -c. Execution is time-bounded to 30 seconds.
// Returns JSON with separated stdout, stderr, exit_code, and duration_ms.
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

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.CommandContext(execCtx, "/bin/sh", "-c", command)
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	result := bashExecResult{
		Stdout:     strings.TrimRight(stdoutBuf.String(), "\n"),
		Stderr:     strings.TrimRight(stderrBuf.String(), "\n"),
		DurationMs: elapsed.Milliseconds(),
	}

	if err != nil {
		if execCtx.Err() != nil {
			result.TimedOut = true
			result.ExitCode = -1
			raw, _ := json.Marshal(result)
			return string(raw), fmt.Errorf("bash_exec: command timed out after %v", timeout)
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			raw, _ := json.Marshal(result)
			return string(raw), fmt.Errorf("exit status %d", result.ExitCode)
		} else {
			// Could not even start the process.
			return "", fmt.Errorf("bash_exec: %v", err)
		}
	}

	raw, _ := json.Marshal(result)
	return string(raw), nil
}

var BashExecDef = agent.ToolDefinition{
	Name:        "bash_exec",
	Description: "Execute a shell command via /bin/sh and return structured JSON output with separated stdout, stderr, exit_code, and duration_ms. Use for running scripts, inspecting files, calling system tools, or any task requiring shell access. Commands are time-limited (default 30s, max 300s). A non-zero exit_code indicates the command failed; check stderr for details.",
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
	ParamAliases: map[string]string{
		"timeout": "timeout_seconds",
		"cmd":     "command",
	},
}
