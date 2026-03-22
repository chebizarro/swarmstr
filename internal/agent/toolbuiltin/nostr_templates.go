// Package toolbuiltin nostr_templates.go — event composition templates.
//
// Provides pre-built templates for common Nostr event patterns so the agent
// can compose events with minimal parameters instead of manually constructing
// tags. Each template produces a ready-to-sign event spec.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"metiq/internal/agent"
)

// ─── Tool Definition ─────────────────────────────────────────────────────────

// NostrComposeDef is the ToolDefinition for nostr_compose.
var NostrComposeDef = agent.ToolDefinition{
	Name:        "nostr_compose",
	Description: "Compose a Nostr event from a named template. Returns a ready-to-publish event spec (kind, content, tags) that can be passed to nostr_publish or nostr_publish_batch. Available templates: reply, quote_repost, repost, reaction, mention_note, tagged_note, labeled.",
	Parameters: agent.ToolParameters{
		Type:     "object",
		Required: []string{"template"},
		Properties: map[string]agent.ToolParamProp{
			"template": {
				Type:        "string",
				Description: "Template name: reply | quote_repost | repost | reaction | mention_note | tagged_note | labeled",
			},
			"content": {
				Type:        "string",
				Description: "Event content / text body",
			},
			"event_id": {
				Type:        "string",
				Description: "Target event ID (hex) — used by reply, quote_repost, repost, reaction",
			},
			"pubkey": {
				Type:        "string",
				Description: "Target pubkey (hex or npub) — used by reply, mention_note",
			},
			"relay_hint": {
				Type:        "string",
				Description: "Relay URL hint for the target event",
			},
			"hashtags": {
				Type:        "array",
				Description: "Hashtag strings (without #) — used by tagged_note",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"labels": {
				Type:        "array",
				Description: "Label strings — used by labeled (NIP-32 kind:1985)",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"label_namespace": {
				Type:        "string",
				Description: "Label namespace — used by labeled (default: 'ugc')",
			},
		},
	},
}

// ─── Tool Implementation ─────────────────────────────────────────────────────

// NostrComposeTool returns an agent tool that composes events from templates.
func NostrComposeTool() agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		template := strings.ToLower(strings.TrimSpace(stringArg(args, "template")))
		if template == "" {
			return "", nostrToolErr("nostr_compose", "invalid_input", "template name is required", nil)
		}

		content, _ := args["content"].(string)
		eventID, _ := args["event_id"].(string)
		pubkey, _ := args["pubkey"].(string)
		relayHint, _ := args["relay_hint"].(string)

		// Resolve npub if needed.
		if pubkey != "" {
			resolved, err := resolveNostrPubkey(pubkey)
			if err != nil {
				return "", nostrToolErr("nostr_compose", "invalid_input", fmt.Sprintf("invalid pubkey: %v", err), nil)
			}
			pubkey = resolved
		}

		type eventSpec struct {
			Kind    int        `json:"kind"`
			Content string     `json:"content"`
			Tags    [][]string `json:"tags"`
		}

		var spec eventSpec

		switch template {
		case "reply":
			// Reply to a note (kind:1 with e and p tags).
			if eventID == "" {
				return "", nostrToolErr("nostr_compose", "invalid_input", "reply template requires event_id", nil)
			}
			spec.Kind = 1
			spec.Content = content
			eTag := []string{"e", eventID}
			if relayHint != "" {
				eTag = append(eTag, relayHint, "reply")
			} else {
				eTag = append(eTag, "", "reply")
			}
			spec.Tags = [][]string{eTag}
			if pubkey != "" {
				spec.Tags = append(spec.Tags, []string{"p", pubkey})
			}

		case "quote_repost", "quote":
			// Quote repost (kind:1 with q tag, NIP-18).
			if eventID == "" {
				return "", nostrToolErr("nostr_compose", "invalid_input", "quote_repost template requires event_id", nil)
			}
			spec.Kind = 1
			if content == "" {
				content = fmt.Sprintf("nostr:%s", eventID)
			}
			spec.Content = content
			qTag := []string{"q", eventID}
			if relayHint != "" {
				qTag = append(qTag, relayHint)
			}
			if pubkey != "" {
				qTag = append(qTag, pubkey)
			}
			spec.Tags = [][]string{qTag}

		case "repost":
			// Repost (kind:6, NIP-18).
			if eventID == "" {
				return "", nostrToolErr("nostr_compose", "invalid_input", "repost template requires event_id", nil)
			}
			spec.Kind = 6
			spec.Content = content // usually the serialized original event JSON
			eTag := []string{"e", eventID}
			if relayHint != "" {
				eTag = append(eTag, relayHint)
			}
			spec.Tags = [][]string{eTag}
			if pubkey != "" {
				spec.Tags = append(spec.Tags, []string{"p", pubkey})
			}

		case "reaction":
			// Reaction (kind:7, NIP-25).
			if eventID == "" {
				return "", nostrToolErr("nostr_compose", "invalid_input", "reaction template requires event_id", nil)
			}
			if content == "" {
				content = "+"
			}
			spec.Kind = 7
			spec.Content = content
			eTag := []string{"e", eventID}
			if relayHint != "" {
				eTag = append(eTag, relayHint)
			}
			spec.Tags = [][]string{eTag}
			if pubkey != "" {
				spec.Tags = append(spec.Tags, []string{"p", pubkey})
			}

		case "mention_note", "mention":
			// Kind:1 note mentioning another user.
			spec.Kind = 1
			spec.Content = content
			spec.Tags = [][]string{}
			if pubkey != "" {
				spec.Tags = append(spec.Tags, []string{"p", pubkey})
			}
			if eventID != "" {
				eTag := []string{"e", eventID}
				if relayHint != "" {
					eTag = append(eTag, relayHint, "mention")
				}
				spec.Tags = append(spec.Tags, eTag)
			}

		case "tagged_note", "tagged":
			// Kind:1 note with hashtag t-tags.
			spec.Kind = 1
			spec.Content = content
			spec.Tags = [][]string{}
			hashtags := toStringSlice(args["hashtags"])
			for _, ht := range hashtags {
				ht = strings.TrimLeft(strings.TrimSpace(ht), "#")
				if ht != "" {
					spec.Tags = append(spec.Tags, []string{"t", strings.ToLower(ht)})
				}
			}

		case "labeled", "label":
			// NIP-32 label event (kind:1985).
			if eventID == "" {
				return "", nostrToolErr("nostr_compose", "invalid_input", "labeled template requires event_id", nil)
			}
			namespace := stringArg(args, "label_namespace")
			if namespace == "" {
				namespace = "ugc"
			}
			labels := toStringSlice(args["labels"])
			if len(labels) == 0 {
				return "", nostrToolErr("nostr_compose", "invalid_input", "labeled template requires at least one label", nil)
			}
			spec.Kind = 1985
			spec.Content = content
			spec.Tags = [][]string{
				{"e", eventID},
				{"L", namespace},
			}
			for _, l := range labels {
				spec.Tags = append(spec.Tags, []string{"l", l, namespace})
			}

		default:
			available := "reply, quote_repost, repost, reaction, mention_note, tagged_note, labeled"
			return "", nostrToolErr("nostr_compose", "invalid_input",
				fmt.Sprintf("unknown template %q — available: %s", template, available), nil)
		}

		out, _ := json.Marshal(spec)
		return string(out), nil
	}
}
