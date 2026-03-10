// Package contextvm implements the ContextVM protocol:
// MCP (Model Context Protocol) transported over Nostr using kind 25910 ephemeral events.
//
// Spec: https://docs.contextvm.org/spec/ctxvm-draft-spec
//
// All MCP messages are stringified JSON embedded in the content field of kind 25910 events.
// Requests include a p-tag pointing to the recipient's pubkey.
// Responses include an e-tag referencing the request event ID.
//
// Server discovery uses replaceable events (CEP-6):
//
//	11316 – Server Announcement
//	11317 – Tools List
//	11318 – Resources List
//	11319 – Resource Templates List
//	11320 – Prompts List
package contextvm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	nostr "fiatjaf.com/nostr"
)

// Event kinds.
const (
	KindMessage           = 25910
	KindServerAnnouncement = 11316
	KindToolsList          = 11317
	KindResourcesList      = 11318
	KindResourceTemplates  = 11319
	KindPromptsList        = 11320
)

// ServerInfo holds data from a ContextVM server announcement (kind 11316, CEP-6).
type ServerInfo struct {
	PubKey       string         `json:"pubkey"`
	Name         string         `json:"name,omitempty"`
	About        string         `json:"about,omitempty"`
	Picture      string         `json:"picture,omitempty"`
	Website      string         `json:"website,omitempty"`
	Encrypted    bool           `json:"encryption_supported,omitempty"`
	Capabilities map[string]any `json:"capabilities,omitempty"`
	EventID      string         `json:"event_id,omitempty"`
	CreatedAt    int64          `json:"created_at,omitempty"`
}

// ToolDef describes a single MCP tool.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// CallResult holds the response from a tools/call MCP operation.
type CallResult struct {
	Content []map[string]any `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

// DiscoverServers fetches ContextVM server announcements (kind 11316) from relays.
func DiscoverServers(ctx context.Context, pool *nostr.Pool, relays []string, limit int) ([]ServerInfo, error) {
	if limit <= 0 {
		limit = 20
	}
	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.Kind(KindServerAnnouncement)},
		Limit: limit,
	}

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var servers []ServerInfo
	// Deduplicate by pubkey (replaceable events – keep latest per pubkey).
	byPubkey := make(map[string]ServerInfo)
	for re := range pool.SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
		pk := re.Event.PubKey.Hex()
		existing, ok := byPubkey[pk]
		if !ok || re.Event.CreatedAt > nostr.Timestamp(existing.CreatedAt) {
			byPubkey[pk] = decodeServerEvent(re.Event)
		}
	}
	for _, s := range byPubkey {
		servers = append(servers, s)
	}
	sort.Slice(servers, func(i, j int) bool {
		return servers[i].CreatedAt > servers[j].CreatedAt
	})
	return servers, nil
}

// ListTools sends a tools/list MCP request to a ContextVM server and returns the tool list.
func ListTools(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey string) ([]ToolDef, error) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{},
	}
	respRaw, err := sendRequest(ctx, pool, keyer, relays, serverPubKey, msg)
	if err != nil {
		return nil, fmt.Errorf("contextvm list tools: %w", err)
	}

	var resp struct {
		Result struct {
			Tools []ToolDef `json:"tools"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		return nil, fmt.Errorf("contextvm list tools: parse response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("contextvm server error: %s", resp.Error.Message)
	}
	return resp.Result.Tools, nil
}

// CallTool calls an MCP tool on a ContextVM server via kind 25910.
func CallTool(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey, toolName string, toolArgs map[string]any) (*CallResult, error) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": toolArgs,
		},
	}
	respRaw, err := sendRequest(ctx, pool, keyer, relays, serverPubKey, msg)
	if err != nil {
		return nil, fmt.Errorf("contextvm call %s: %w", toolName, err)
	}

	var resp struct {
		Result CallResult `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		return nil, fmt.Errorf("contextvm call %s: parse response: %w", toolName, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("contextvm tool error: %s", resp.Error.Message)
	}
	return &resp.Result, nil
}

// SendRaw sends an arbitrary stringified JSON-RPC MCP message to a server
// and returns the raw response content.
func SendRaw(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey string, msg map[string]any) (json.RawMessage, error) {
	return sendRequest(ctx, pool, keyer, relays, serverPubKey, msg)
}

// ── internal ──────────────────────────────────────────────────────────────────

// sendRequest publishes a kind 25910 MCP message to the server and waits for the response.
// Per the spec: request has p-tag = server pubkey; response has e-tag = request event ID.
func sendRequest(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey string, msg map[string]any) (json.RawMessage, error) {
	msgJSON, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal message: %w", err)
	}

	evt := nostr.Event{
		Kind:      nostr.Kind(KindMessage),
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", serverPubKey}},
		Content:   string(msgJSON),
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}

	requestID := evt.ID.Hex()

	// Subscribe to responses referencing our request event ID before publishing.
	respFilter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.Kind(KindMessage)},
		Tags:  nostr.TagMap{"e": []string{requestID}},
		Limit: 1,
	}
	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	respCh := pool.SubscribeMany(ctx2, relays, respFilter, nostr.SubscriptionOptions{})

	// Publish the request.
	for result := range pool.PublishMany(ctx, relays, evt) {
		_ = result // best-effort delivery
	}

	// Wait for the server response.
	for re := range respCh {
		if re.Event.Content == "" {
			continue
		}
		return json.RawMessage(re.Event.Content), nil
	}
	return nil, fmt.Errorf("timed out waiting for ContextVM server response (request %s)", requestID)
}

func decodeServerEvent(ev nostr.Event) ServerInfo {
	s := ServerInfo{
		PubKey:    ev.PubKey.Hex(),
		EventID:   ev.ID.Hex(),
		CreatedAt: int64(ev.CreatedAt),
	}
	// Content is the MCP server info (protocolVersion, capabilities, serverInfo).
	if ev.Content != "" {
		var content map[string]any
		if err := json.Unmarshal([]byte(ev.Content), &content); err == nil {
			s.Capabilities, _ = content["capabilities"].(map[string]any)
			if si, ok := content["serverInfo"].(map[string]any); ok {
				if n, ok := si["name"].(string); ok {
					s.Name = n
				}
			}
		}
	}
	// Tags hold metadata (CEP-6).
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "name":
			if s.Name == "" {
				s.Name = tag[1]
			}
		case "about":
			s.About = tag[1]
		case "picture":
			s.Picture = tag[1]
		case "website":
			s.Website = tag[1]
		case "support_encryption":
			s.Encrypted = true
		}
	}
	return s
}
