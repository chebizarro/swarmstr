package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// StatusToMap converts a HookStatus to a map[string]any for JSON-RPC responses.
func StatusToMap(s HookStatus) map[string]any {
	install := make([]map[string]any, 0, len(s.Install))
	for _, sp := range s.Install {
		install = append(install, map[string]any{
			"id":    sp.ID,
			"kind":  sp.Kind,
			"label": sp.Label,
		})
	}

	var req map[string]any
	if s.Requires != nil {
		req = map[string]any{
			"bins":    s.Requires.Bins,
			"anyBins": s.Requires.AnyBins,
			"env":     s.Requires.Env,
			"config":  s.Requires.Config,
			"os":      s.Requires.OS,
		}
	}

	m := map[string]any{
		"hookKey":     s.HookKey,
		"name":        s.Name,
		"description": s.Description,
		"source":      string(s.Source),
		"emoji":       s.Emoji,
		"homepage":    s.Homepage,
		"events":      s.Events,
		"always":      s.Always,
		"enabled":     s.Enabled,
		"eligible":    s.Eligible,
		"filePath":    s.FilePath,
		"install":     install,
	}
	if req != nil {
		m["requires"] = req
	}
	return m
}

// MarshalStatus is a convenience wrapper for JSON encoding.
// MarshalStatus is a convenience wrapper for JSON encoding.
func MarshalStatus(statuses []HookStatus) (json.RawMessage, error) {
	list := make([]map[string]any, len(statuses))
	for i, s := range statuses {
		list[i] = StatusToMap(s)
	}
	return json.Marshal(list)
}

// ────────────────────────────────────────────────────────────────────────────
// Shell hook execution
// ────────────────────────────────────────────────────────────────────────────

// ShellHandlerTimeout is the maximum time a shell hook handler may run.
// Override via the METIQ_HOOK_TIMEOUT_SEC environment variable.
// ShellHandlerTimeout is the maximum time a shell hook handler may run.
// Override via the METIQ_HOOK_TIMEOUT_SEC environment variable.
const ShellHandlerTimeout = 30 * time.Second

// MakeShellHandler returns a HookHandler that executes scriptPath as a shell
// script, passing event data via environment variables:
//
//   - HOOK_NAME         — full event name (e.g. "command:new")
//   - HOOK_TYPE         — event type (e.g. "command")
//   - HOOK_ACTION       — event action (e.g. "new")
//   - HOOK_SESSION_KEY  — session key identifier
//   - HOOK_CONTEXT      — JSON-encoded event context map
//   - HOOK_TIMESTAMP    — RFC3339 timestamp of the event
//
// If the event context map contains known Nostr message fields, they are also
// exported as dedicated variables:
//
//   - HOOK_FROM_PUBKEY  — sender Nostr pubkey hex (context["from_pubkey"])
//   - HOOK_TO_PUBKEY    — recipient Nostr pubkey hex (context["to_pubkey"])
//   - HOOK_EVENT_ID     — Nostr event ID hex (context["event_id"])
//   - HOOK_RELAY        — relay URL the event arrived from (context["relay"])
//   - HOOK_CHANNEL_ID   — channel identifier e.g. "nostr" (context["channel_id"])
//   - HOOK_CONTENT      — message content (context["content"])
//
// If the script exits with a non-zero code, the error is returned.
// stdout and stderr are collected but not returned (the manager logs them at
// debug level when the handler returns an error).
// MakeShellHandler returns a HookHandler that executes scriptPath as a shell
// script, passing event data via environment variables:
//
//   - HOOK_NAME         — full event name (e.g. "command:new")
//   - HOOK_TYPE         — event type (e.g. "command")
//   - HOOK_ACTION       — event action (e.g. "new")
//   - HOOK_SESSION_KEY  — session key identifier
//   - HOOK_CONTEXT      — JSON-encoded event context map
//   - HOOK_TIMESTAMP    — RFC3339 timestamp of the event
//
// If the event context map contains known Nostr message fields, they are also
// exported as dedicated variables:
//
//   - HOOK_FROM_PUBKEY  — sender Nostr pubkey hex (context["from_pubkey"])
//   - HOOK_TO_PUBKEY    — recipient Nostr pubkey hex (context["to_pubkey"])
//   - HOOK_EVENT_ID     — Nostr event ID hex (context["event_id"])
//   - HOOK_RELAY        — relay URL the event arrived from (context["relay"])
//   - HOOK_CHANNEL_ID   — channel identifier e.g. "nostr" (context["channel_id"])
//   - HOOK_CONTENT      — message content (context["content"])
//
// If the script exits with a non-zero code, the error is returned.
// stdout and stderr are collected but not returned (the manager logs them at
// debug level when the handler returns an error).
func MakeShellHandler(scriptPath string) HookHandler {
	return func(event *Event) error {
		ctx, cancel := context.WithTimeout(context.Background(), ShellHandlerTimeout)
		defer cancel()

		ctxJSON, _ := json.Marshal(event.Context)
		env := append(os.Environ(),
			"HOOK_NAME="+event.Name,
			"HOOK_TYPE="+event.EventType,
			"HOOK_ACTION="+event.Action,
			"HOOK_SESSION_KEY="+event.SessionKey,
			"HOOK_CONTEXT="+string(ctxJSON),
			"HOOK_TIMESTAMP="+event.Timestamp.UTC().Format(time.RFC3339),
		)

		// Promote well-known context fields to dedicated env vars for convenience.
		for _, kv := range []struct{ key, envVar string }{
			{"from_pubkey", "HOOK_FROM_PUBKEY"},
			{"to_pubkey", "HOOK_TO_PUBKEY"},
			{"event_id", "HOOK_EVENT_ID"},
			{"relay", "HOOK_RELAY"},
			{"channel_id", "HOOK_CHANNEL_ID"},
			{"content", "HOOK_CONTENT"},
		} {
			if v, ok := event.Context[kv.key]; ok {
				env = append(env, kv.envVar+"="+fmt.Sprintf("%v", v))
			}
		}

		cmd := exec.CommandContext(ctx, "sh", scriptPath) //nolint:gosec // scriptPath is admin-controlled
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("shell hook %s: %w\n%s", filepath.Base(scriptPath), err, strings.TrimSpace(string(out)))
		}
		// Append any output lines as hook messages.
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				event.Messages = append(event.Messages, line)
			}
		}
		return nil
	}
}

// AttachShellHandlers scans each registered hook whose Handler is nil for a
// handler.sh file in its BaseDir.  When found, a shell handler is attached.
// This is called after LoadBundledHooks / ScanDir so that managed and workspace
// hooks with a handler.sh automatically become executable.
// AttachShellHandlers scans each registered hook whose Handler is nil for a
// handler.sh file in its BaseDir.  When found, a shell handler is attached.
// This is called after LoadBundledHooks / ScanDir so that managed and workspace
// hooks with a handler.sh automatically become executable.
func AttachShellHandlers(mgr *Manager) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	for _, h := range mgr.hooks {
		if h.Handler != nil {
			continue // already has a Go handler
		}
		if h.BaseDir == "" {
			continue
		}
		scriptPath := filepath.Join(h.BaseDir, "handler.sh")
		if _, err := os.Stat(scriptPath); err == nil {
			h.Handler = MakeShellHandler(scriptPath)
		}
	}
}
