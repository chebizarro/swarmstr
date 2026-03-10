// nostr_sign.go provides the nostr_sign tool: sign an arbitrary Nostr event
// (returns the signed JSON) without publishing it to any relay.
// This is useful for building complex event pipelines or verifying the agent's
// signing capability before publishing.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"swarmstr/internal/agent"
)

// NostrSignDef is the ToolDefinition for nostr_sign.
var NostrSignDef = agent.ToolDefinition{
	Name:        "nostr_sign",
	Description: "Sign a Nostr event with the agent's private key and return the complete signed event JSON (including id and sig). The event is NOT published. Use when you need a signed event to pass to another tool or external system.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"kind": {
				Type:        "integer",
				Description: "Nostr event kind number.",
			},
			"content": {
				Type:        "string",
				Description: "Event content string.",
			},
			"tags": {
				Type:        "array",
				Description: "NIP-01 tags as array-of-arrays, e.g. [[\"e\",\"<id>\"], [\"p\",\"<pubkey>\"]].",
				Items:       &agent.ToolParamProp{Type: "array"},
			},
			"created_at": {
				Type:        "integer",
				Description: "Unix timestamp for the event. Defaults to current time if omitted.",
			},
		},
		Required: []string{"kind", "content"},
	},
}

// NostrSignOpts carries the signing credentials for the nostr_sign tool.
// Either Keyer or PrivateKey must be set.  Keyer takes priority (supports
// NIP-46 bunker remote signing).
type NostrSignOpts struct {
	// PrivateKey is the hex-encoded 32-byte secret key.  Used when Keyer is nil.
	PrivateKey string
	// Keyer is an optional nostr.Keyer (e.g. keyer.BunkerSigner for NIP-46).
	Keyer nostr.Keyer
}

// NostrSignTool returns a ToolFunc that signs a Nostr event without publishing.
func NostrSignTool(opts NostrSignOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		// Build a signing function that works for both plain keys and bunker signers.
		var signFn func(context.Context, *nostr.Event) error
		if opts.Keyer != nil {
			signFn = func(ctx context.Context, ev *nostr.Event) error {
				return opts.Keyer.SignEvent(ctx, ev)
			}
		} else {
			if opts.PrivateKey == "" {
				return "", fmt.Errorf("nostr_sign: private key not configured")
			}
			sk, err := nostr.SecretKeyFromHex(strings.TrimSpace(opts.PrivateKey))
			if err != nil {
				return "", fmt.Errorf("nostr_sign: invalid private key: %w", err)
			}
			signFn = func(_ context.Context, ev *nostr.Event) error {
				return ev.Sign(sk)
			}
		}

		kind := agent.ArgInt(args, "kind", 1)
		content := agent.ArgString(args, "content")

		ev := nostr.Event{
			Kind:      nostr.Kind(kind),
			Content:   content,
			CreatedAt: nostr.Timestamp(time.Now().Unix()),
		}

		// Override created_at if provided.
		if ts := agent.ArgInt(args, "created_at", 0); ts > 0 {
			ev.CreatedAt = nostr.Timestamp(int64(ts))
		}

		// Parse tags if provided.
		if tagsRaw, ok := args["tags"]; ok && tagsRaw != nil {
			tagsJSON, err := json.Marshal(tagsRaw)
			if err != nil {
				return "", fmt.Errorf("nostr_sign: marshal tags: %w", err)
			}
			var tags nostr.Tags
			if err := json.Unmarshal(tagsJSON, &tags); err != nil {
				return "", fmt.Errorf("nostr_sign: parse tags: %w", err)
			}
			ev.Tags = tags
		}

		if err := signFn(ctx, &ev); err != nil {
			return "", fmt.Errorf("nostr_sign: sign: %w", err)
		}

		b, err := json.Marshal(ev)
		if err != nil {
			return "", fmt.Errorf("nostr_sign: marshal event: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
}
