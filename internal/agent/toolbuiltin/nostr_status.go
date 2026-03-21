// Package toolbuiltin – nostr_status_set tool.
//
// Allows agents to explicitly set their NIP-38 status event,
// signalling to Nostr clients what the agent is currently doing.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/nostr/nip38"
)

// NostrStatusToolOpts configures the nostr_status_set tool.
type NostrStatusToolOpts struct {
	// Heartbeat is the NIP-38 heartbeat controller.
	// When nil, the tool returns an error.
	Heartbeat *nip38.Heartbeat
}

// NostrStatusSetDef is the tool definition for nostr_status_set.
var NostrStatusSetDef = agent.ToolDefinition{
	Name:        "nostr_status_set",
	Description: "Set your NIP-38 user status on Nostr. Broadcasts a kind:30315 replaceable event so other clients can see your current activity.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"status": {
				Type:        "string",
				Description: "Status value: idle, typing, updating, dnd, or offline.",
			},
			"content": {
				Type:        "string",
				Description: "Optional free-form note shown alongside the status.",
			},
			"expiry_seconds": {
				Type:        "integer",
				Description: "Optional: number of seconds until the status expires (0 = no expiry).",
			},
		},
		Required: []string{"status"},
	},
}

// NostrStatusTool returns a ToolFunc for nostr_status_set.
func NostrStatusTool(opts NostrStatusToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		status, _ := args["status"].(string)
		status = strings.TrimSpace(status)
		if status == "" {
			status = nip38.StatusIdle
		}

		// Validate status value.
		validStatuses := map[string]bool{
			nip38.StatusIdle:     true,
			nip38.StatusTyping:   true,
			nip38.StatusUpdating: true,
			nip38.StatusDND:      true,
			nip38.StatusOffline:  true,
		}
		if !validStatuses[status] {
			return "", fmt.Errorf("nostr_status_set: invalid status %q (valid: idle, typing, updating, dnd, offline)", status)
		}

		if opts.Heartbeat == nil {
			return "", fmt.Errorf("nostr_status_set: NIP-38 heartbeat not configured")
		}

		content, _ := args["content"].(string)
		var expiry int64
		if v, ok := args["expiry_seconds"].(float64); ok && v > 0 {
			expiry = time.Now().Unix() + int64(v)
		}

		opts.Heartbeat.SetStatus(ctx, status, content, expiry)

		result := map[string]any{
			"ok":     true,
			"status": status,
		}
		if content != "" {
			result["content"] = content
		}
		if expiry > 0 {
			result["expires_at"] = expiry
		}
		out, _ := json.Marshal(result)
		return string(out), nil
	}
}
