package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	mcppkg "metiq/internal/mcp"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ── MCP Loopback constants ──────────────────────────────────────────────────

const (
	mcpLoopbackServerName    = "metiq"
	mcpLoopbackServerVersion = "0.1.0"
	mcpLoopbackMaxBodyBytes  = 1 << 20 // 1 MiB
)

// mcpLoopbackSupportedProtocols lists the MCP protocol versions we advertise
// during the initialize handshake, most recent first.
var mcpLoopbackSupportedProtocols = []string{"2025-03-26", "2024-11-05"}

// ── JSON-RPC 2.0 types ─────────────────────────────────────────────────────

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // number | string | null
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
}

type jsonRPCResultResponse struct {
	jsonRPCResponse
	Result any `json:"result"`
}

type jsonRPCErrorResponse struct {
	jsonRPCResponse
	Error jsonRPCErrorObject `json:"error"`
}

type jsonRPCErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func jsonRPCResult(id any, result any) jsonRPCResultResponse {
	return jsonRPCResultResponse{
		jsonRPCResponse: jsonRPCResponse{JSONRPC: "2.0", ID: normalizeJSONRPCID(id)},
		Result:          result,
	}
}

func jsonRPCError(id any, code int, message string) jsonRPCErrorResponse {
	return jsonRPCErrorResponse{
		jsonRPCResponse: jsonRPCResponse{JSONRPC: "2.0", ID: normalizeJSONRPCID(id)},
		Error:           jsonRPCErrorObject{Code: code, Message: message},
	}
}

// normalizeJSONRPCID normalises the id field: if nil or missing, return null as
// per the JSON-RPC spec (error responses must include the id from the request,
// or null if unknown).
func normalizeJSONRPCID(v any) any {
	if v == nil {
		return nil
	}
	return v
}

// ── MCP tool schema entry ───────────────────────────────────────────────────

// mcpToolSchemaEntry is the JSON shape returned by tools/list.
type mcpToolSchemaEntry struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ── MCP Loopback Server Options ─────────────────────────────────────────────

// MCPLoopbackOptions holds the callbacks needed by the loopback MCP server.
type MCPLoopbackOptions struct {
	// Token is the bearer token required for authentication.
	// If empty, no auth is required (only safe on loopback).
	Token string

	// MCPManager provides tool resolution and invocation.
	MCPManager *mcppkg.Manager
}

// ── Tool cache ──────────────────────────────────────────────────────────────

// mcpLoopbackToolCache caches the resolved tool list from the MCP manager to
// avoid re-resolving on every request. The cache is invalidated when the manager
// pointer changes (simple equality check).
type mcpLoopbackToolCache struct {
	mu      sync.RWMutex
	mgr     *mcppkg.Manager
	tools   []mcpToolSchemaEntry
	toolMap map[string]mcpToolRef
}

type mcpToolRef struct {
	serverName string
	toolName   string
}

func (c *mcpLoopbackToolCache) resolve(mgr *mcppkg.Manager) ([]mcpToolSchemaEntry, map[string]mcpToolRef) {
	if mgr == nil {
		return nil, nil
	}
	c.mu.RLock()
	if c.mgr == mgr && c.tools != nil {
		tools, refs := c.tools, c.toolMap
		c.mu.RUnlock()
		return tools, refs
	}
	c.mu.RUnlock()

	allTools := mgr.GetAllTools()
	schema := make([]mcpToolSchemaEntry, 0, 32)
	refs := make(map[string]mcpToolRef, 32)
	for serverName, serverTools := range allTools {
		for _, tool := range serverTools {
			entry := mcpToolSchemaEntry{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: buildMCPToolInputSchema(tool),
			}
			schema = append(schema, entry)
			refs[tool.Name] = mcpToolRef{serverName: serverName, toolName: tool.Name}
		}
	}

	c.mu.Lock()
	c.mgr = mgr
	c.tools = schema
	c.toolMap = refs
	c.mu.Unlock()
	return schema, refs
}

// invalidate clears the cached tool resolution so the next request re-resolves.
func (c *mcpLoopbackToolCache) invalidate() {
	c.mu.Lock()
	c.tools = nil
	c.toolMap = nil
	c.mu.Unlock()
}

// ── Tool schema building ────────────────────────────────────────────────────

// buildMCPToolInputSchema converts a gomcp.Tool's InputSchema into a
// map[string]any suitable for the MCP tools/list response. Union schemas
// (anyOf/oneOf) are flattened into a single merged object.
func buildMCPToolInputSchema(tool *gomcp.Tool) map[string]any {
	raw := mcppkg.ToolInputSchemaToMap(tool.InputSchema)
	if raw == nil {
		raw = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if _, ok := raw["anyOf"]; ok {
		raw = flattenUnionSchema(raw)
	} else if _, ok := raw["oneOf"]; ok {
		raw = flattenUnionSchema(raw)
	}
	if raw["type"] != "object" {
		raw["type"] = "object"
		if _, ok := raw["properties"]; !ok {
			raw["properties"] = map[string]any{}
		}
	}
	return raw
}

// flattenUnionSchema merges anyOf/oneOf variants into a single object schema,
// matching the openclaw MCP loopback behavior so MCP clients see a unified
// parameter set.
func flattenUnionSchema(raw map[string]any) map[string]any {
	var variants []any
	if v, ok := raw["anyOf"]; ok {
		if arr, ok := v.([]any); ok {
			variants = arr
		}
	}
	if variants == nil {
		if v, ok := raw["oneOf"]; ok {
			if arr, ok := v.([]any); ok {
				variants = arr
			}
		}
	}
	if len(variants) == 0 {
		return raw
	}

	mergedProps := make(map[string]any)
	var requiredSets []map[string]bool
	for _, v := range variants {
		variant, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if props, ok := variant["properties"].(map[string]any); ok {
			for key, schema := range props {
				if _, exists := mergedProps[key]; !exists {
					mergedProps[key] = schema
					continue
				}
				// Merge enum/const conflicts
				existing, eOk := mergedProps[key].(map[string]any)
				incoming, iOk := schema.(map[string]any)
				if !eOk || !iOk {
					continue
				}
				if eEnum, ok := existing["enum"].([]any); ok {
					if iEnum, ok := incoming["enum"].([]any); ok {
						seen := make(map[any]bool)
						merged := make([]any, 0, len(eEnum)+len(iEnum))
						for _, v := range eEnum {
							if !seen[v] {
								seen[v] = true
								merged = append(merged, v)
							}
						}
						for _, v := range iEnum {
							if !seen[v] {
								seen[v] = true
								merged = append(merged, v)
							}
						}
						cp := make(map[string]any, len(existing))
						for k, v := range existing {
							cp[k] = v
						}
						cp["enum"] = merged
						mergedProps[key] = cp
						continue
					}
				}
				if eConst, eHas := existing["const"]; eHas {
					if iConst, iHas := incoming["const"]; iHas && eConst != iConst {
						cp := make(map[string]any, len(existing))
						for k, v := range existing {
							if k != "const" {
								cp[k] = v
							}
						}
						cp["enum"] = []any{eConst, iConst}
						mergedProps[key] = cp
						continue
					}
				}
			}
		}
		reqSet := make(map[string]bool)
		if reqs, ok := variant["required"].([]any); ok {
			for _, r := range reqs {
				if s, ok := r.(string); ok {
					reqSet[s] = true
				}
			}
		}
		requiredSets = append(requiredSets, reqSet)
	}

	// Intersect required sets.
	var required []string
	if len(requiredSets) > 0 {
		for key := range requiredSets[0] {
			inAll := true
			for _, set := range requiredSets[1:] {
				if !set[key] {
					inAll = false
					break
				}
			}
			if inAll {
				required = append(required, key)
			}
		}
	}

	result := make(map[string]any, len(raw))
	for k, v := range raw {
		if k == "anyOf" || k == "oneOf" {
			continue
		}
		result[k] = v
	}
	result["type"] = "object"
	result["properties"] = mergedProps
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

// ── JSON-RPC handler ────────────────────────────────────────────────────────

// handleMCPJsonRPC dispatches a single JSON-RPC message and returns the
// response (or nil for notifications that don't need a response).
func handleMCPJsonRPC(
	ctx context.Context,
	msg jsonRPCRequest,
	schema []mcpToolSchemaEntry,
	toolRefs map[string]mcpToolRef,
	mgr *mcppkg.Manager,
) (any, error) {
	id := parsedJSONRPCID(msg.ID)

	switch msg.Method {
	case "initialize":
		// Negotiate protocol version.
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if msg.Params != nil {
			_ = json.Unmarshal(msg.Params, &params)
		}
		negotiated := mcpLoopbackSupportedProtocols[0]
		for _, v := range mcpLoopbackSupportedProtocols {
			if v == params.ProtocolVersion {
				negotiated = v
				break
			}
		}
		return jsonRPCResult(id, map[string]any{
			"protocolVersion": negotiated,
			"capabilities":   map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    mcpLoopbackServerName,
				"version": mcpLoopbackServerVersion,
			},
		}), nil

	case "notifications/initialized", "notifications/cancelled":
		return nil, nil

	case "tools/list":
		return jsonRPCResult(id, map[string]any{"tools": schema}), nil

	case "tools/call":
		return handleMCPToolCall(ctx, id, msg.Params, toolRefs, mgr)

	default:
		return jsonRPCError(id, -32601, fmt.Sprintf("Method not found: %s", msg.Method)), nil
	}
}

// mcpTextContent is the MCP tool response content format.
type mcpTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// handleMCPToolCall processes a tools/call JSON-RPC request.
func handleMCPToolCall(
	ctx context.Context,
	id any,
	rawParams json.RawMessage,
	toolRefs map[string]mcpToolRef,
	mgr *mcppkg.Manager,
) (any, error) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if rawParams != nil {
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return jsonRPCError(id, -32602, "Invalid params"), nil
		}
	}

	ref, ok := toolRefs[params.Name]
	if !ok {
		return jsonRPCResult(id, map[string]any{
			"content": []mcpTextContent{{Type: "text", Text: fmt.Sprintf("Tool not available: %s", params.Name)}},
			"isError": true,
		}), nil
	}

	if params.Arguments == nil {
		params.Arguments = make(map[string]any)
	}

	result, err := mgr.CallTool(ctx, ref.serverName, ref.toolName, params.Arguments)
	if err != nil {
		return jsonRPCResult(id, map[string]any{
			"content": []mcpTextContent{{Type: "text", Text: err.Error()}},
			"isError": true,
		}), nil
	}

	content := normalizeMCPToolCallContent(result)
	isError := result != nil && result.IsError
	return jsonRPCResult(id, map[string]any{
		"content": content,
		"isError": isError,
	}), nil
}

// normalizeMCPToolCallContent converts a gomcp.CallToolResult into
// MCP content blocks.
func normalizeMCPToolCallContent(result *gomcp.CallToolResult) []mcpTextContent {
	if result == nil {
		return []mcpTextContent{{Type: "text", Text: ""}}
	}
	var blocks []mcpTextContent
	for _, c := range result.Content {
		switch v := c.(type) {
		case *gomcp.TextContent:
			blocks = append(blocks, mcpTextContent{Type: "text", Text: v.Text})
		case *gomcp.ImageContent:
			blocks = append(blocks, mcpTextContent{Type: "text", Text: fmt.Sprintf("[Image: %s]", v.MIMEType)})
		default:
			data, _ := json.Marshal(c)
			blocks = append(blocks, mcpTextContent{Type: "text", Text: string(data)})
		}
	}
	if len(blocks) == 0 {
		blocks = []mcpTextContent{{Type: "text", Text: ""}}
	}
	return blocks
}

// ── HTTP handler ────────────────────────────────────────────────────────────

// handleMCPLoopback returns an http.HandlerFunc for the MCP loopback endpoint.
func handleMCPLoopback(loopOpts MCPLoopbackOptions) http.HandlerFunc {
	cache := &mcpLoopbackToolCache{}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}

		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": "unsupported_media_type"})
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, mcpLoopbackMaxBodyBytes+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, jsonRPCError(nil, -32700, "Parse error"))
			return
		}
		if len(body) > mcpLoopbackMaxBodyBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, jsonRPCError(nil, -32700, "Request body too large"))
			return
		}

		// Detect batch vs single request.
		trimmed := strings.TrimSpace(string(body))
		isBatch := len(trimmed) > 0 && trimmed[0] == '['

		var messages []jsonRPCRequest
		if isBatch {
			if err := json.Unmarshal(body, &messages); err != nil {
				writeJSON(w, http.StatusBadRequest, jsonRPCError(nil, -32700, "Parse error"))
				return
			}
		} else {
			var single jsonRPCRequest
			if err := json.Unmarshal(body, &single); err != nil {
				writeJSON(w, http.StatusBadRequest, jsonRPCError(nil, -32700, "Parse error"))
				return
			}
			messages = []jsonRPCRequest{single}
		}

		// Resolve tools.
		schema, toolRefs := cache.resolve(loopOpts.MCPManager)

		// Process all messages.
		responses := make([]any, 0, len(messages))
		for _, msg := range messages {
			resp, err := handleMCPJsonRPC(r.Context(), msg, schema, toolRefs, loopOpts.MCPManager)
			if err != nil {
				log.Printf("mcp loopback: handler error: %v", err)
				responses = append(responses, jsonRPCError(parsedJSONRPCID(msg.ID), -32603, "Internal error"))
				continue
			}
			if resp != nil {
				responses = append(responses, resp)
			}
		}

		if len(responses) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if isBatch {
			_ = json.NewEncoder(w).Encode(responses)
		} else {
			_ = json.NewEncoder(w).Encode(responses[0])
		}
	}
}

// parsedJSONRPCID converts a json.RawMessage ID into a native Go value
// (string, float64, or nil) for use in response construction.
func parsedJSONRPCID(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

// ── MCP Loopback runtime state ──────────────────────────────────────────────

// MCPLoopbackRuntime holds the connection details for the active MCP loopback
// server, allowing agents to discover and connect to it.
type MCPLoopbackRuntime struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}

var (
	activeMCPLoopbackMu      sync.RWMutex
	activeMCPLoopbackRuntime *MCPLoopbackRuntime
)

// GetActiveMCPLoopbackRuntime returns the active loopback runtime info, or nil
// if no loopback server is running.
func GetActiveMCPLoopbackRuntime() *MCPLoopbackRuntime {
	activeMCPLoopbackMu.RLock()
	defer activeMCPLoopbackMu.RUnlock()
	if activeMCPLoopbackRuntime == nil {
		return nil
	}
	cp := *activeMCPLoopbackRuntime
	return &cp
}

// SetActiveMCPLoopbackRuntime stores the current loopback runtime info.
func SetActiveMCPLoopbackRuntime(rt MCPLoopbackRuntime) {
	activeMCPLoopbackMu.Lock()
	defer activeMCPLoopbackMu.Unlock()
	activeMCPLoopbackRuntime = &MCPLoopbackRuntime{Port: rt.Port, Token: rt.Token}
}

// ClearActiveMCPLoopbackRuntime clears the active runtime if the token matches.
func ClearActiveMCPLoopbackRuntime(token string) {
	activeMCPLoopbackMu.Lock()
	defer activeMCPLoopbackMu.Unlock()
	if activeMCPLoopbackRuntime != nil && activeMCPLoopbackRuntime.Token == token {
		activeMCPLoopbackRuntime = nil
	}
}

// GenerateMCPLoopbackToken generates a random 32-byte hex token for MCP auth.
func GenerateMCPLoopbackToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// MCPLoopbackServerConfig builds a server configuration block that external
// MCP clients can use to connect to the loopback server.
func MCPLoopbackServerConfig(port int) map[string]any {
	return map[string]any{
		"mcpServers": map[string]any{
			"metiq": map[string]any{
				"type": "http",
				"url":  fmt.Sprintf("http://127.0.0.1:%d/mcp", port),
				"headers": map[string]string{
					"Authorization":               "Bearer ${METIQ_MCP_TOKEN}",
					"x-session-key":                "${METIQ_MCP_SESSION_KEY}",
					"x-metiq-agent-id":             "${METIQ_MCP_AGENT_ID}",
					"x-metiq-account-id":           "${METIQ_MCP_ACCOUNT_ID}",
					"x-metiq-message-channel":      "${METIQ_MCP_MESSAGE_CHANNEL}",
					"x-metiq-sender-is-owner":      "${METIQ_MCP_SENDER_IS_OWNER}",
				},
			},
		},
	}
}

// ── Mount on admin mux ──────────────────────────────────────────────────────

// mountMCPLoopback registers the MCP loopback endpoint on the admin mux.
// The endpoint is mounted at POST /mcp with bearer token auth.
func mountMCPLoopback(mux *http.ServeMux, opts ServerOptions) {
	if opts.MCPManager == nil {
		return
	}

	loopOpts := MCPLoopbackOptions{
		Token:      opts.Token,
		MCPManager: opts.MCPManager,
	}
	mux.HandleFunc("/mcp", withAuth(opts.Token, handleMCPLoopback(loopOpts)))
}
