package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcppkg "metiq/internal/mcp"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ── JSON-RPC helpers ────────────────────────────────────────────────────────

func TestParsedJSONRPCID_Types(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want any
	}{
		{"null bytes", json.RawMessage(`null`), nil},
		{"empty", nil, nil},
		{"number", json.RawMessage(`42`), float64(42)},
		{"string", json.RawMessage(`"abc"`), "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsedJSONRPCID(tt.raw)
			if got != tt.want {
				t.Errorf("parsedJSONRPCID(%s) = %v (%T), want %v (%T)", tt.raw, got, got, tt.want, tt.want)
			}
		})
	}
}

func TestJSONRPCResult(t *testing.T) {
	resp := jsonRPCResult(1, map[string]any{"ok": true})
	if resp.JSONRPC != "2.0" {
		t.Fatalf("JSONRPC = %q, want 2.0", resp.JSONRPC)
	}
	// ID should be preserved.
	if id, ok := resp.ID.(int); !ok || id != 1 {
		t.Fatalf("ID = %v (%T), want 1 (int)", resp.ID, resp.ID)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok || result["ok"] != true {
		t.Fatalf("unexpected result: %v", resp.Result)
	}
}

func TestJSONRPCError(t *testing.T) {
	resp := jsonRPCError(nil, -32601, "Method not found")
	if resp.JSONRPC != "2.0" {
		t.Fatalf("JSONRPC = %q, want 2.0", resp.JSONRPC)
	}
	if resp.ID != nil {
		t.Fatalf("ID = %v, want nil", resp.ID)
	}
	if resp.Error.Code != -32601 {
		t.Fatalf("Error.Code = %d, want -32601", resp.Error.Code)
	}
	if resp.Error.Message != "Method not found" {
		t.Fatalf("Error.Message = %q", resp.Error.Message)
	}
}

// ── flattenUnionSchema ──────────────────────────────────────────────────────

func TestFlattenUnionSchema_NoVariants(t *testing.T) {
	input := map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}}
	got := flattenUnionSchema(input)
	if got["type"] != "object" {
		t.Fatalf("type = %v, want object", got["type"])
	}
	props := got["properties"].(map[string]any)
	if _, ok := props["a"]; !ok {
		t.Fatalf("expected property a")
	}
}

func TestFlattenUnionSchema_AnyOf(t *testing.T) {
	input := map[string]any{
		"anyOf": []any{
			map[string]any{
				"properties": map[string]any{
					"mode": map[string]any{"const": "read"},
					"path": map[string]any{"type": "string"},
				},
				"required": []any{"mode", "path"},
			},
			map[string]any{
				"properties": map[string]any{
					"mode":    map[string]any{"const": "write"},
					"path":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
				},
				"required": []any{"mode", "path", "content"},
			},
		},
	}
	got := flattenUnionSchema(input)
	if got["type"] != "object" {
		t.Fatalf("type = %v, want object", got["type"])
	}
	if _, ok := got["anyOf"]; ok {
		t.Fatal("anyOf should be removed")
	}
	props := got["properties"].(map[string]any)
	if len(props) != 3 {
		t.Fatalf("expected 3 properties, got %d", len(props))
	}
	// "mode" const should have become enum.
	modeSchema, ok := props["mode"].(map[string]any)
	if !ok {
		t.Fatal("mode property missing")
	}
	enumValues, ok := modeSchema["enum"].([]any)
	if !ok || len(enumValues) != 2 {
		t.Fatalf("mode should be enum with 2 values, got %v", modeSchema)
	}
	// Required: only mode and path are in both variants.
	required, ok := got["required"].([]string)
	if !ok {
		t.Fatalf("required = %T, want []string", got["required"])
	}
	reqSet := make(map[string]bool)
	for _, r := range required {
		reqSet[r] = true
	}
	if !reqSet["mode"] || !reqSet["path"] {
		t.Fatalf("expected mode and path in required, got %v", required)
	}
	if reqSet["content"] {
		t.Fatal("content should NOT be in required (not in all variants)")
	}
}

func TestFlattenUnionSchema_EnumMerge(t *testing.T) {
	input := map[string]any{
		"oneOf": []any{
			map[string]any{
				"properties": map[string]any{
					"color": map[string]any{"enum": []any{"red", "blue"}},
				},
			},
			map[string]any{
				"properties": map[string]any{
					"color": map[string]any{"enum": []any{"blue", "green"}},
				},
			},
		},
	}
	got := flattenUnionSchema(input)
	props := got["properties"].(map[string]any)
	colorSchema := props["color"].(map[string]any)
	enumValues := colorSchema["enum"].([]any)
	if len(enumValues) != 3 {
		t.Fatalf("expected 3 enum values (red,blue,green), got %v", enumValues)
	}
}

// ── buildMCPToolInputSchema ─────────────────────────────────────────────────

func TestBuildMCPToolInputSchema_NilSchema(t *testing.T) {
	// Import the go-sdk mcp types.
	tool := newTestMCPTool("test", "test tool", nil)
	got := buildMCPToolInputSchema(tool)
	if got["type"] != "object" {
		t.Fatalf("type = %v, want object", got["type"])
	}
}

// ── Tool cache ──────────────────────────────────────────────────────────────

func TestMCPLoopbackToolCache_NilManager(t *testing.T) {
	cache := &mcpLoopbackToolCache{}
	schema, refs := cache.resolve(nil)
	if schema != nil || refs != nil {
		t.Fatalf("expected nil for nil manager, got schema=%v refs=%v", schema, refs)
	}
}

func TestMCPLoopbackToolCache_Invalidate(t *testing.T) {
	cache := &mcpLoopbackToolCache{}
	cache.mu.Lock()
	cache.tools = []mcpToolSchemaEntry{{Name: "stale"}}
	cache.toolMap = map[string]mcpToolRef{"stale": {}}
	cache.mu.Unlock()
	cache.invalidate()
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if cache.tools != nil || cache.toolMap != nil {
		t.Fatal("invalidate did not clear cache")
	}
}

// ── MCP loopback runtime ────────────────────────────────────────────────────

func TestMCPLoopbackRuntime_SetGetClear(t *testing.T) {
	// Ensure clean state.
	activeMCPLoopbackMu.Lock()
	activeMCPLoopbackRuntime = nil
	activeMCPLoopbackMu.Unlock()

	if got := GetActiveMCPLoopbackRuntime(); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}

	SetActiveMCPLoopbackRuntime(MCPLoopbackRuntime{Port: 12345, Token: "tok123"})
	got := GetActiveMCPLoopbackRuntime()
	if got == nil || got.Port != 12345 || got.Token != "tok123" {
		t.Fatalf("unexpected runtime: %+v", got)
	}

	// Clear with wrong token — should not clear.
	ClearActiveMCPLoopbackRuntime("wrong")
	got = GetActiveMCPLoopbackRuntime()
	if got == nil {
		t.Fatal("should not have cleared with wrong token")
	}

	// Clear with correct token.
	ClearActiveMCPLoopbackRuntime("tok123")
	if got := GetActiveMCPLoopbackRuntime(); got != nil {
		t.Fatalf("expected nil after clear, got %+v", got)
	}
}

func TestGenerateMCPLoopbackToken(t *testing.T) {
	tok, err := GenerateMCPLoopbackToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 64 { // 32 bytes = 64 hex chars
		t.Fatalf("token length = %d, want 64", len(tok))
	}
	tok2, err := GenerateMCPLoopbackToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok == tok2 {
		t.Fatal("tokens should be unique")
	}
}

func TestMCPLoopbackServerConfig(t *testing.T) {
	cfg := MCPLoopbackServerConfig(23119)
	servers, ok := cfg["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("missing mcpServers")
	}
	entry, ok := servers["metiq"].(map[string]any)
	if !ok {
		t.Fatal("missing metiq server entry")
	}
	if entry["url"] != "http://127.0.0.1:23119/mcp" {
		t.Fatalf("unexpected url: %v", entry["url"])
	}
	headers, ok := entry["headers"].(map[string]string)
	if !ok {
		t.Fatal("missing headers")
	}
	if headers["Authorization"] != "Bearer ${METIQ_MCP_TOKEN}" {
		t.Fatalf("unexpected Authorization: %v", headers["Authorization"])
	}
}

// ── HTTP handler tests ──────────────────────────────────────────────────────

func mcpLoopbackRequest(t *testing.T, handler http.Handler, body any, token string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		bodyReader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bodyReader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Result()
}

func TestHandleMCPLoopback_MethodNotAllowed(t *testing.T) {
	handler := handleMCPLoopback(MCPLoopbackOptions{})
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleMCPLoopback_UnsupportedMediaType(t *testing.T) {
	handler := handleMCPLoopback(MCPLoopbackOptions{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnsupportedMediaType)
	}
}

func TestHandleMCPLoopback_ParseError(t *testing.T) {
	handler := handleMCPLoopback(MCPLoopbackOptions{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleMCPLoopback_Initialize(t *testing.T) {
	handler := handleMCPLoopback(MCPLoopbackOptions{})
	rpcReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2024-11-05"}`),
	}
	resp := mcpLoopbackRequest(t, http.HandlerFunc(handler), rpcReq, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result jsonRPCResultResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.JSONRPC != "2.0" {
		t.Fatalf("JSONRPC = %q", result.JSONRPC)
	}
	resMap, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", result.Result)
	}
	if resMap["protocolVersion"] != "2024-11-05" {
		t.Fatalf("protocolVersion = %v, want 2024-11-05", resMap["protocolVersion"])
	}
	serverInfo, ok := resMap["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo type = %T", resMap["serverInfo"])
	}
	if serverInfo["name"] != "metiq" {
		t.Fatalf("serverInfo.name = %v, want metiq", serverInfo["name"])
	}
}

func TestHandleMCPLoopback_InitializeDefaultVersion(t *testing.T) {
	handler := handleMCPLoopback(MCPLoopbackOptions{})
	rpcReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"unknown-version"}`),
	}
	resp := mcpLoopbackRequest(t, http.HandlerFunc(handler), rpcReq, "")
	var result jsonRPCResultResponse
	_ = json.NewDecoder(resp.Body).Decode(&result)
	resMap := result.Result.(map[string]any)
	if resMap["protocolVersion"] != "2025-03-26" {
		t.Fatalf("should default to latest, got %v", resMap["protocolVersion"])
	}
}

func TestHandleMCPLoopback_Notification(t *testing.T) {
	handler := handleMCPLoopback(MCPLoopbackOptions{})
	// Notifications have no ID and produce no response → HTTP 202.
	rpcReq := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	resp := mcpLoopbackRequest(t, http.HandlerFunc(handler), rpcReq, "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
}

func TestHandleMCPLoopback_ToolsList_NoManager(t *testing.T) {
	handler := handleMCPLoopback(MCPLoopbackOptions{})
	rpcReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/list",
	}
	resp := mcpLoopbackRequest(t, http.HandlerFunc(handler), rpcReq, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result jsonRPCResultResponse
	_ = json.NewDecoder(resp.Body).Decode(&result)
	resMap := result.Result.(map[string]any)
	// With nil manager, tools should be nil/empty.
	tools := resMap["tools"]
	if tools != nil {
		// If it's an array, it should be empty.
		if arr, ok := tools.([]any); ok && len(arr) > 0 {
			t.Fatalf("expected empty tools, got %v", tools)
		}
	}
}

func TestHandleMCPLoopback_ToolsCall_NoManager(t *testing.T) {
	handler := handleMCPLoopback(MCPLoopbackOptions{})
	rpcReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"nonexistent","arguments":{}}`),
	}
	resp := mcpLoopbackRequest(t, http.HandlerFunc(handler), rpcReq, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result jsonRPCResultResponse
	_ = json.NewDecoder(resp.Body).Decode(&result)
	resMap := result.Result.(map[string]any)
	if resMap["isError"] != true {
		t.Fatalf("expected isError=true for unknown tool, got %v", resMap["isError"])
	}
}

func TestHandleMCPLoopback_UnknownMethod(t *testing.T) {
	handler := handleMCPLoopback(MCPLoopbackOptions{})
	rpcReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`4`),
		Method:  "resources/list",
	}
	resp := mcpLoopbackRequest(t, http.HandlerFunc(handler), rpcReq, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var errResp struct {
		JSONRPC string `json:"jsonrpc"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error.Code != -32601 {
		t.Fatalf("error code = %d, want -32601", errResp.Error.Code)
	}
	if !strings.Contains(errResp.Error.Message, "resources/list") {
		t.Fatalf("error message = %q, should mention the method", errResp.Error.Message)
	}
}

func TestHandleMCPLoopback_BatchRequest(t *testing.T) {
	handler := handleMCPLoopback(MCPLoopbackOptions{})
	batch := []jsonRPCRequest{
		{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: json.RawMessage(`{"protocolVersion":"2024-11-05"}`)},
		{JSONRPC: "2.0", Method: "notifications/initialized"},
		{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"},
	}
	resp := mcpLoopbackRequest(t, http.HandlerFunc(handler), batch, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var responses []json.RawMessage
	if err := json.Unmarshal(body, &responses); err != nil {
		t.Fatalf("expected array response, got: %s", body)
	}
	// Notification doesn't produce a response, so we should get 2.
	if len(responses) != 2 {
		t.Fatalf("expected 2 responses, got %d: %s", len(responses), body)
	}
}

func TestHandleMCPLoopback_BatchAllNotifications(t *testing.T) {
	handler := handleMCPLoopback(MCPLoopbackOptions{})
	batch := []jsonRPCRequest{
		{JSONRPC: "2.0", Method: "notifications/initialized"},
		{JSONRPC: "2.0", Method: "notifications/cancelled"},
	}
	resp := mcpLoopbackRequest(t, http.HandlerFunc(handler), batch, "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 for all-notification batch", resp.StatusCode)
	}
}

// ── Mount test ──────────────────────────────────────────────────────────────

func TestMountMCPLoopback_NilManager(t *testing.T) {
	mux := http.NewServeMux()
	mountMCPLoopback(mux, ServerOptions{})
	// With nil MCPManager, /mcp should not be mounted → 404.
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when MCPManager is nil", rec.Code)
	}
}

func TestMountMCPLoopback_WithAuth(t *testing.T) {
	mux := http.NewServeMux()
	mgr := newTestMCPManager()
	defer mgr.Close()
	mountMCPLoopback(mux, ServerOptions{Token: "secret-tok", MCPManager: mgr})

	// No auth → 401.
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without auth", rec.Code)
	}

	// With auth → 200.
	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-tok")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with auth", rec.Code)
	}
}

// ── normalizeMCPToolCallContent ─────────────────────────────────────────────

func TestNormalizeMCPToolCallContent_Nil(t *testing.T) {
	blocks := normalizeMCPToolCallContent(nil)
	if len(blocks) != 1 || blocks[0].Type != "text" {
		t.Fatalf("expected 1 text block, got %v", blocks)
	}
}

// ── Test helpers ────────────────────────────────────────────────────────────

func newTestMCPManager() *mcppkg.Manager {
	return mcppkg.NewManager()
}

// newTestMCPTool creates a gomcp.Tool for testing. We use the same interface
// the real code expects.
func newTestMCPTool(name, description string, inputSchema any) *gomcp.Tool {
	tool := &gomcp.Tool{}
	tool.Name = name
	tool.Description = description
	// InputSchema is typically an interface from the go-sdk; for tests with nil
	// we just leave it zero-valued.
	return tool
}
