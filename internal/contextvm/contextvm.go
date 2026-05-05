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
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	nostruntime "metiq/internal/nostr/runtime"
)

// Event kinds.
const (
	KindMessage            = 25910
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

var sendContextVMRequest = sendRequest

func executeJSONRPC(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey string, msg map[string]any, encryption string) (json.RawMessage, error) {
	respRaw, err := sendContextVMRequest(ctx, pool, keyer, relays, serverPubKey, msg, encryption)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("contextvm server error: %s", resp.Error.Message)
	}
	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		return nil, fmt.Errorf("response missing result")
	}
	return resp.Result, nil
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
	for re := range pool.FetchMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
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
func ListTools(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey string, encryption string) ([]ToolDef, error) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{},
	}
	respRaw, err := sendRequest(ctx, pool, keyer, relays, serverPubKey, msg, encryption)
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

// ListResources sends a resources/list MCP request to a ContextVM server.
func ListResources(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey string, encryption string) ([]map[string]any, error) {
	resultRaw, err := executeJSONRPC(ctx, pool, keyer, relays, serverPubKey, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "resources/list",
		"params":  map[string]any{},
	}, encryption)
	if err != nil {
		return nil, fmt.Errorf("contextvm list resources: %w", err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		return nil, fmt.Errorf("contextvm list resources: parse result: %w", err)
	}
	resourcesRaw, ok := result["resources"]
	if !ok {
		return nil, fmt.Errorf("contextvm list resources: response missing resources")
	}
	var resources []map[string]any
	if err := json.Unmarshal(resourcesRaw, &resources); err != nil {
		return nil, fmt.Errorf("contextvm list resources: parse resources: %w", err)
	}
	return resources, nil
}

// ReadResource sends a resources/read MCP request to a ContextVM server.
func ReadResource(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey string, uri string, encryption string) (map[string]any, error) {
	resultRaw, err := executeJSONRPC(ctx, pool, keyer, relays, serverPubKey, map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "resources/read",
		"params":  map[string]any{"uri": uri},
	}, encryption)
	if err != nil {
		return nil, fmt.Errorf("contextvm read resource: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		return nil, fmt.Errorf("contextvm read resource: parse result: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("contextvm read resource: response missing result object")
	}
	return result, nil
}

// ListPrompts sends a prompts/list MCP request to a ContextVM server.
func ListPrompts(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey string, encryption string) ([]map[string]any, error) {
	resultRaw, err := executeJSONRPC(ctx, pool, keyer, relays, serverPubKey, map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "prompts/list",
		"params":  map[string]any{},
	}, encryption)
	if err != nil {
		return nil, fmt.Errorf("contextvm list prompts: %w", err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		return nil, fmt.Errorf("contextvm list prompts: parse result: %w", err)
	}
	promptsRaw, ok := result["prompts"]
	if !ok {
		return nil, fmt.Errorf("contextvm list prompts: response missing prompts")
	}
	var prompts []map[string]any
	if err := json.Unmarshal(promptsRaw, &prompts); err != nil {
		return nil, fmt.Errorf("contextvm list prompts: parse prompts: %w", err)
	}
	return prompts, nil
}

// GetPrompt sends a prompts/get MCP request to a ContextVM server.
func GetPrompt(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey string, name string, args map[string]any, encryption string) (map[string]any, error) {
	params := map[string]any{"name": name}
	if len(args) > 0 {
		params["arguments"] = args
	}
	resultRaw, err := executeJSONRPC(ctx, pool, keyer, relays, serverPubKey, map[string]any{
		"jsonrpc": "2.0",
		"id":      6,
		"method":  "prompts/get",
		"params":  params,
	}, encryption)
	if err != nil {
		return nil, fmt.Errorf("contextvm get prompt: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		return nil, fmt.Errorf("contextvm get prompt: parse result: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("contextvm get prompt: response missing result object")
	}
	return result, nil
}

// CallTool calls an MCP tool on a ContextVM server via kind 25910.
func CallTool(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey, toolName string, toolArgs map[string]any, encryption string) (*CallResult, error) {
	return CallToolWithTimeout(ctx, pool, keyer, relays, serverPubKey, toolName, toolArgs, 0, encryption)
}

// CallToolWithTimeout calls an MCP tool on a ContextVM server via kind 25910.
//
// Deprecated: timeout is ignored. ContextVM completion is driven by a response
// event referencing the request event; use ctx cancellation/deadlines to abort.
func CallToolWithTimeout(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey, toolName string, toolArgs map[string]any, _ time.Duration, encryption string) (*CallResult, error) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": toolArgs,
		},
	}
	resultRaw, err := executeJSONRPC(ctx, pool, keyer, relays, serverPubKey, msg, encryption)
	if err != nil {
		return nil, fmt.Errorf("contextvm call %s: %w", toolName, err)
	}
	var result CallResult
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		return nil, fmt.Errorf("contextvm call %s: parse result: %w", toolName, err)
	}
	return &result, nil
}

// SendRaw sends an arbitrary stringified JSON-RPC MCP message to a server
// and returns the raw response content.
func SendRaw(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey string, msg map[string]any, encryption string) (json.RawMessage, error) {
	return sendRequest(ctx, pool, keyer, relays, serverPubKey, msg, encryption)
}

// ── internal ──────────────────────────────────────────────────────────────────

// sendRequest publishes a kind 25910 MCP message to the server and completes
// only when a valid response event references the signed request event ID.
// Per the spec: request has p-tag = server pubkey; response has e-tag = request event ID.
func sendRequest(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, serverPubKey string, msg map[string]any, encryption string) (json.RawMessage, error) {
	if pool == nil {
		return nil, fmt.Errorf("contextvm request: nostr pool is required")
	}
	if keyer == nil {
		return nil, fmt.Errorf("contextvm request: signing keyer is required")
	}
	if len(relays) == 0 {
		return nil, fmt.Errorf("contextvm request: at least one relay is required")
	}
	msgJSON, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal message: %w", err)
	}

	serverPK, err := nostr.PubKeyFromHex(serverPubKey)
	if err != nil {
		return nil, fmt.Errorf("invalid server pubkey: %w", err)
	}
	mode := normalizeEncryptionMode(encryption)
	content := string(msgJSON)
	if mode != "none" {
		encContent, encErr := encryptForServer(ctx, keyer, serverPK, content, mode)
		if encErr != nil {
			return nil, encErr
		}
		content = encContent
	}

	evt := nostr.Event{
		Kind:      nostr.Kind(KindMessage),
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", serverPubKey}},
		Content:   content,
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}

	requestID := evt.ID.Hex()

	// Subscribe to responses referencing our request event ID before publishing.
	// This is a live subscription: EOSE is not treated as request completion;
	// completion is the application-level response event carrying the e-tag.
	respFilter := nostr.Filter{
		Kinds:   []nostr.Kind{nostr.Kind(KindMessage)},
		Authors: []nostr.PubKey{serverPK},
		Tags:    nostr.TagMap{"e": []string{requestID}},
	}
	respCtx, cancelResp := context.WithCancel(ctx)
	defer cancelResp()
	respCh, closedCh := pool.SubscribeManyNotifyClosed(respCtx, relays, respFilter, nostr.SubscriptionOptions{})

	if err := publishContextVMRequest(ctx, pool, relays, evt); err != nil {
		return nil, err
	}

	return awaitContextVMCompletion(ctx, keyer, serverPK, requestID, respCh, closedCh)
}

func publishContextVMRequest(ctx context.Context, pool *nostr.Pool, relays []string, evt nostr.Event) error {
	if pool == nil {
		return fmt.Errorf("contextvm publish request: nostr pool is required")
	}
	if len(relays) == 0 {
		return fmt.Errorf("contextvm publish request: at least one relay is required")
	}
	var accepted int
	var failures []string
	for result := range pool.PublishMany(ctx, relays, evt) {
		if result.Error != nil {
			relay := strings.TrimSpace(result.RelayURL)
			if relay == "" && result.Relay != nil {
				relay = result.Relay.URL
			}
			if relay == "" {
				relay = "unknown relay"
			}
			failures = append(failures, fmt.Sprintf("%s: %v", relay, result.Error))
			continue
		}
		accepted++
	}
	if accepted == 0 {
		if len(failures) == 0 {
			return fmt.Errorf("contextvm publish request: no relay accepted request")
		}
		return fmt.Errorf("contextvm publish request rejected by all relays: %s", strings.Join(failures, "; "))
	}
	return nil
}

func awaitContextVMCompletion(ctx context.Context, keyer nostr.Keyer, serverPK nostr.PubKey, requestID string, respCh <-chan nostr.RelayEvent, closedCh <-chan nostr.RelayClosed) (json.RawMessage, error) {
	var lastContentErr error
	var closedReasons []string
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("contextvm response wait canceled (request %s): %w", requestID, ctx.Err())
		case closed, ok := <-closedCh:
			if !ok {
				closedCh = nil
				continue
			}
			closedReasons = append(closedReasons, formatRelayClosed(closed))
		case re, ok := <-respCh:
			if !ok {
				if lastContentErr != nil {
					return nil, fmt.Errorf("received response but failed to decrypt or parse (request %s): %w", requestID, lastContentErr)
				}
				if len(closedReasons) > 0 {
					return nil, fmt.Errorf("contextvm response subscription closed before completion (request %s): %s", requestID, strings.Join(closedReasons, "; "))
				}
				return nil, fmt.Errorf("contextvm response subscription closed before completion (request %s)", requestID)
			}
			content, err := decodeContextVMResponseContent(ctx, keyer, serverPK, requestID, re.Event)
			if err != nil {
				lastContentErr = err
				continue
			}
			return content, nil
		}
	}
}

func decodeContextVMResponseContent(ctx context.Context, keyer nostr.Keyer, serverPK nostr.PubKey, requestID string, ev nostr.Event) (json.RawMessage, error) {
	if err := validateContextVMResponseEvent(ev, serverPK, requestID, time.Now()); err != nil {
		return nil, err
	}
	if strings.TrimSpace(ev.Content) == "" {
		return nil, fmt.Errorf("empty response content")
	}
	content := strings.TrimSpace(ev.Content)
	if json.Valid([]byte(content)) {
		return json.RawMessage(content), nil
	}
	dec, decErr := decryptFromServer(ctx, keyer, ev.PubKey, content)
	if decErr != nil {
		return nil, decErr
	}
	dec = strings.TrimSpace(dec)
	if !json.Valid([]byte(dec)) {
		return nil, fmt.Errorf("decrypted response is not valid JSON")
	}
	return json.RawMessage(dec), nil
}

func validateContextVMResponseEvent(ev nostr.Event, serverPK nostr.PubKey, requestID string, now time.Time) error {
	if ev.Kind != nostr.Kind(KindMessage) {
		return fmt.Errorf("response kind %d does not match contextvm message kind %d", ev.Kind, KindMessage)
	}
	if ev.PubKey != serverPK {
		return fmt.Errorf("response pubkey %s does not match server pubkey %s", ev.PubKey.Hex(), serverPK.Hex())
	}
	if !eventReferencesRequest(ev, requestID) {
		return fmt.Errorf("response missing request e-tag %s", requestID)
	}
	if !ev.CheckID() {
		return fmt.Errorf("response event id mismatch")
	}
	if !ev.VerifySignature() {
		return fmt.Errorf("response event signature invalid")
	}
	createdAt := int64(ev.CreatedAt)
	nowUnix := now.Unix()
	if createdAt > nowUnix+600 {
		return fmt.Errorf("response timestamp too far in future")
	}
	if createdAt < nowUnix-365*24*60*60 {
		return fmt.Errorf("response timestamp too far in past")
	}
	return nil
}

func eventReferencesRequest(ev nostr.Event, requestID string) bool {
	for _, tag := range ev.Tags {
		if len(tag) >= 2 && tag[0] == "e" && tag[1] == requestID {
			return true
		}
	}
	return false
}

func formatRelayClosed(closed nostr.RelayClosed) string {
	relay := "unknown relay"
	if closed.Relay != nil && strings.TrimSpace(closed.Relay.URL) != "" {
		relay = closed.Relay.URL
	}
	reason := strings.TrimSpace(closed.Reason)
	if reason == "" {
		reason = "closed"
	}
	if closed.HandledAuth {
		reason += " (auth handled)"
	}
	return relay + ": " + reason
}

func normalizeEncryptionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "none", "plaintext":
		return "none"
	case "nip44", "nip-44":
		return "nip44"
	case "nip04", "nip-04":
		return "nip04"
	case "auto":
		return "auto"
	default:
		return "auto"
	}
}

func encryptForServer(ctx context.Context, keyer nostr.Keyer, serverPubKey nostr.PubKey, plaintext, mode string) (string, error) {
	// mode is already normalized by caller
	switch mode {
	case "none":
		return plaintext, nil
	case "nip44":
		ct, err := keyer.Encrypt(ctx, plaintext, serverPubKey)
		if err != nil {
			return "", fmt.Errorf("contextvm encrypt nip44: %w", err)
		}
		return ct, nil
	case "nip04":
		enc, ok := keyer.(nostruntime.NIP04Encrypter)
		if !ok {
			return "", fmt.Errorf("contextvm encrypt nip04: keyer does not support NIP-04")
		}
		ct, err := enc.EncryptNIP04(ctx, plaintext, serverPubKey)
		if err != nil {
			return "", fmt.Errorf("contextvm encrypt nip04: %w", err)
		}
		return ct, nil
	case "auto":
		if ct, err := keyer.Encrypt(ctx, plaintext, serverPubKey); err == nil {
			return ct, nil
		}
		if enc, ok := keyer.(nostruntime.NIP04Encrypter); ok {
			ct, err := enc.EncryptNIP04(ctx, plaintext, serverPubKey)
			if err == nil {
				return ct, nil
			}
		}
		return "", fmt.Errorf("contextvm encrypt auto: no supported encryption path")
	default:
		return "", fmt.Errorf("contextvm encrypt: unsupported mode %q", mode)
	}
}

func decryptFromServer(ctx context.Context, keyer nostr.Keyer, senderPubKey nostr.PubKey, ciphertext string) (string, error) {
	if pt, err := keyer.Decrypt(ctx, ciphertext, senderPubKey); err == nil {
		return pt, nil
	}
	if dec, ok := keyer.(nostruntime.NIP04Decrypter); ok {
		if pt04, err04 := dec.DecryptNIP04(ctx, ciphertext, senderPubKey); err04 == nil {
			return pt04, nil
		}
	}
	return "", fmt.Errorf("contextvm decrypt: unable to decrypt response with nip44 or nip04")
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
