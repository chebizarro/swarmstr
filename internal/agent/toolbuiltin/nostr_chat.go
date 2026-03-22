// Package toolbuiltin nostr_chat.go — NIP-C7 kind:9 chat tools.
//
// These tools let an agent send and fetch NIP-C7 chat messages (kind:9).
// The special "-" tag convention identifies the relay's root/ambient chat.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
)

// KindChat is the NIP-C7 chat message kind.
const KindChat nostr.Kind = 9

// ─── Tool Definitions ────────────────────────────────────────────────────────

var NostrChatSendDef = agent.ToolDefinition{
	Name:        "nostr_chat_send",
	Description: "Send a NIP-C7 kind:9 chat message. Use root_tag=\"-\" for the relay's ambient/root chat. Optionally quote-reply to a parent message by providing parent_event_id.",
	Parameters: agent.ToolParameters{
		Type:     "object",
		Required: []string{"text"},
		Properties: map[string]agent.ToolParamProp{
			"text":            {Type: "string", Description: "Chat message text"},
			"root_tag":        {Type: "string", Description: "Root tag for the chat room. \"-\" = relay root chat (default). Any other string = topic-scoped chat."},
			"parent_event_id": {Type: "string", Description: "Event ID to quote-reply to (optional, adds q tag)"},
			"parent_pubkey":   {Type: "string", Description: "Hex pubkey of the author being replied to (optional, used as 4th element of q tag)"},
			"relays":          {Type: "array", Description: "Relay URLs to publish to (optional, uses default relays if omitted)", Items: &agent.ToolParamProp{Type: "string"}},
		},
	},
}

var NostrChatFetchDef = agent.ToolDefinition{
	Name:        "nostr_chat_fetch",
	Description: "Fetch recent NIP-C7 kind:9 chat messages from a relay. Use root_tag=\"-\" for the relay's ambient/root chat.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"root_tag": {Type: "string", Description: "Root tag to filter by. \"-\" = relay root chat (default)."},
			"limit":    {Type: "number", Description: "Max messages to return (default 20, max 100)"},
			"since":    {Type: "number", Description: "Unix timestamp to fetch from (optional)"},
			"relays":   {Type: "array", Description: "Relay URLs to query (optional)", Items: &agent.ToolParamProp{Type: "string"}},
		},
	},
}

// ─── Tool Implementations ────────────────────────────────────────────────────

// NostrChatSendTool sends a NIP-C7 kind:9 chat message.
func NostrChatSendTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		signFn, err := opts.signerFunc()
		if err != nil {
			return "", nostrToolErr("nostr_chat_send", "no_keyer", err.Error(), nil)
		}

		text := strings.TrimSpace(stringArg(args, "text"))
		if text == "" {
			return "", nostrToolErr("nostr_chat_send", "missing_text", "text is required", nil)
		}

		rootTag := strings.TrimSpace(stringArg(args, "root_tag"))
		if rootTag == "" {
			rootTag = "-"
		}

		parentEventID := strings.TrimSpace(stringArg(args, "parent_event_id"))
		parentPubKey := strings.TrimSpace(stringArg(args, "parent_pubkey"))

		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", nostrToolErr("nostr_chat_send", "no_relays", "no relays configured", nil)
		}

		// Build tags.
		tags := nostr.Tags{}
		if rootTag == "-" {
			tags = append(tags, nostr.Tag{"-"})
		} else {
			tags = append(tags, nostr.Tag{"-", rootTag})
		}

		if parentEventID != "" {
			relay := ""
			if len(relays) > 0 {
				relay = relays[0]
			}
			tags = append(tags, nostr.Tag{"q", parentEventID, relay, parentPubKey})
		}

		evt := nostr.Event{
			Kind:      KindChat,
			Content:   text,
			CreatedAt: nostr.Now(),
			Tags:      tags,
		}
		if err := opts.checkOutboundEvent(&evt); err != nil {
			return "", nostrToolErr("nostr_chat_send", "content_blocked", err.Error(), nil)
		}
		if err := signFn(ctx, &evt); err != nil {
			return "", nostrToolErr("nostr_chat_send", "sign_failed", err.Error(), nil)
		}

		ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		pool, releasePool := opts.AcquirePool("chat_send done")
		defer releasePool()

		published := 0
		var lastErr error
		for result := range pool.PublishMany(ctx2, relays, evt) {
			if result.Error == nil {
				published++
			} else {
				lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
			}
		}
		if published == 0 {
			errMsg := "no relay accepted publish"
			if lastErr != nil {
				errMsg = lastErr.Error()
			}
			return "", nostrToolErr("nostr_chat_send", "publish_failed", errMsg, nil)
		}

		return nostrWriteSuccessEnvelope("nostr_chat_send", evt.ID.Hex(), int(KindChat), map[string]any{
			"root_tag":        rootTag,
			"parent_event_id": parentEventID,
		}, map[string]any{
			"published": published,
		}, nil), nil
	}
}

// NostrChatFetchTool fetches recent NIP-C7 kind:9 chat messages.
func NostrChatFetchTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		rootTag := strings.TrimSpace(stringArg(args, "root_tag"))
		if rootTag == "" {
			rootTag = "-"
		}

		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		if limit > 100 {
			limit = 100
		}

		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", nostrToolErr("nostr_chat_fetch", "no_relays", "no relays configured", nil)
		}

		filter := nostr.Filter{
			Kinds: []nostr.Kind{KindChat},
			Limit: limit,
		}
		if rootTag == "-" {
			filter.Tags = nostr.TagMap{"-": []string{}}
		} else {
			filter.Tags = nostr.TagMap{"-": []string{rootTag}}
		}

		if v, ok := args["since"].(float64); ok && v > 0 {
			filter.Since = nostr.Timestamp(int64(v))
		}

		ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		pool, releasePool := opts.AcquirePool("chat_fetch done")
		defer releasePool()

		type chatMsg struct {
			EventID       string `json:"event_id"`
			PubKey        string `json:"pubkey"`
			Content       string `json:"content"`
			CreatedAt     int64  `json:"created_at"`
			RootTag       string `json:"root_tag"`
			ParentEventID string `json:"parent_event_id,omitempty"`
			Relay         string `json:"relay,omitempty"`
		}

		seen := make(map[string]bool)
		var messages []chatMsg
		for re := range pool.SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
			id := re.Event.ID.Hex()
			if seen[id] {
				continue
			}
			seen[id] = true
			ev := re.Event
			msg := chatMsg{
				EventID:   ev.ID.Hex(),
				PubKey:    ev.PubKey.Hex(),
				Content:   ev.Content,
				CreatedAt: int64(ev.CreatedAt),
				RootTag:   rootTag,
			}
			if re.Relay != nil {
				msg.Relay = re.Relay.URL
			}
			// Extract parent event from q tag.
			for _, tag := range ev.Tags {
				if len(tag) >= 2 && tag[0] == "q" {
					msg.ParentEventID = tag[1]
					break
				}
			}
			messages = append(messages, msg)
		}

		out, _ := json.Marshal(map[string]any{
			"root_tag": rootTag,
			"count":    len(messages),
			"messages": messages,
		})
		return string(out), nil
	}
}

// RegisterChatTools registers NIP-C7 chat tools with the tool registry.
func RegisterChatTools(tools *agent.ToolRegistry, opts NostrToolOpts) {
	tools.RegisterWithDef("nostr_chat_send", NostrChatSendTool(opts), NostrChatSendDef)
	tools.RegisterWithDef("nostr_chat_fetch", NostrChatFetchTool(opts), NostrChatFetchDef)
}
