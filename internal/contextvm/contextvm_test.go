package contextvm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

func TestListResourcesUsesResourcesListMethod(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	var gotMsg map[string]any
	var gotServerPubKey string
	var gotTimeout time.Duration
	var gotEncryption string
	sendContextVMRequestWithTimeout = func(_ context.Context, _ *nostr.Pool, _ nostr.Keyer, _ []string, serverPubKey string, msg map[string]any, timeout time.Duration, encryption string) (json.RawMessage, error) {
		gotMsg = msg
		gotServerPubKey = serverPubKey
		gotTimeout = timeout
		gotEncryption = encryption
		return json.RawMessage(`{"jsonrpc":"2.0","result":{"resources":[{"uri":"file:///tmp/test.txt","name":"test"}]}}`), nil
	}

	resources, err := ListResources(context.Background(), nil, nil, []string{"wss://relay.example"}, "peer-pubkey", "nip44")
	if err != nil {
		t.Fatalf("ListResources error: %v", err)
	}
	if gotServerPubKey != "peer-pubkey" {
		t.Fatalf("server pubkey = %q, want peer-pubkey", gotServerPubKey)
	}
	if gotTimeout != 30*time.Second {
		t.Fatalf("timeout = %v, want 30s", gotTimeout)
	}
	if gotEncryption != "nip44" {
		t.Fatalf("encryption = %q, want nip44", gotEncryption)
	}
	if gotMsg["method"] != "resources/list" {
		t.Fatalf("method = %#v, want resources/list", gotMsg["method"])
	}
	if len(resources) != 1 || resources[0]["uri"] != "file:///tmp/test.txt" {
		t.Fatalf("resources = %#v", resources)
	}
}

func TestGetPromptSendsArgumentsAndParsesResult(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	var gotParams map[string]any
	sendContextVMRequestWithTimeout = func(_ context.Context, _ *nostr.Pool, _ nostr.Keyer, _ []string, _ string, msg map[string]any, _ time.Duration, _ string) (json.RawMessage, error) {
		if msg["method"] != "prompts/get" {
			t.Fatalf("method = %#v, want prompts/get", msg["method"])
		}
		params, ok := msg["params"].(map[string]any)
		if !ok {
			t.Fatalf("params = %#v, want map[string]any", msg["params"])
		}
		gotParams = params
		return json.RawMessage(`{"jsonrpc":"2.0","result":{"messages":[{"role":"user","content":{"type":"text","text":"hello"}}]}}`), nil
	}

	result, err := GetPrompt(context.Background(), nil, nil, nil, "peer-pubkey", "review", map[string]any{"repo": "swarmstr"}, "auto")
	if err != nil {
		t.Fatalf("GetPrompt error: %v", err)
	}
	if gotParams["name"] != "review" {
		t.Fatalf("name = %#v, want review", gotParams["name"])
	}
	args, ok := gotParams["arguments"].(map[string]any)
	if !ok || args["repo"] != "swarmstr" {
		t.Fatalf("arguments = %#v", gotParams["arguments"])
	}
	messages, ok := result["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestListPromptsSurfacesServerError(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = func(_ context.Context, _ *nostr.Pool, _ nostr.Keyer, _ []string, _ string, _ map[string]any, _ time.Duration, _ string) (json.RawMessage, error) {
		return json.RawMessage(`{"jsonrpc":"2.0","error":{"message":"boom"}}`), nil
	}

	_, err := ListPrompts(context.Background(), nil, nil, nil, "peer-pubkey", "auto")
	if err == nil || !strings.Contains(err.Error(), "contextvm server error: boom") {
		t.Fatalf("err = %v, want server error", err)
	}
}

// ─── mockRequestFn helper ─────────────────────────────────────────────────────

func mockRequestFn(resp string) func(context.Context, *nostr.Pool, nostr.Keyer, []string, string, map[string]any, time.Duration, string) (json.RawMessage, error) {
	return func(_ context.Context, _ *nostr.Pool, _ nostr.Keyer, _ []string, _ string, _ map[string]any, _ time.Duration, _ string) (json.RawMessage, error) {
		return json.RawMessage(resp), nil
	}
}

func mockRequestErr(errMsg string) func(context.Context, *nostr.Pool, nostr.Keyer, []string, string, map[string]any, time.Duration, string) (json.RawMessage, error) {
	return func(_ context.Context, _ *nostr.Pool, _ nostr.Keyer, _ []string, _ string, _ map[string]any, _ time.Duration, _ string) (json.RawMessage, error) {
		return nil, fmt.Errorf("%s", errMsg)
	}
}

// ─── Constants ────────────────────────────────────────────────────────────────

func TestKindConstants(t *testing.T) {
	tests := []struct{ name string; got, want int }{
		{"Message", KindMessage, 25910},
		{"ServerAnnouncement", KindServerAnnouncement, 11316},
		{"ToolsList", KindToolsList, 11317},
		{"ResourcesList", KindResourcesList, 11318},
		{"ResourceTemplates", KindResourceTemplates, 11319},
		{"PromptsList", KindPromptsList, 11320},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, tt.got, tt.want)
		}
	}
}

// ─── normalizeEncryptionMode ──────────────────────────────────────────────────

func TestNormalizeEncryptionMode(t *testing.T) {
	tests := []struct{ input, want string }{
		{"", "none"},
		{"none", "none"},
		{"plaintext", "none"},
		{"nip44", "nip44"},
		{"NIP44", "nip44"},
		{"nip-44", "nip44"},
		{"nip04", "nip04"},
		{"NIP-04", "nip04"},
		{"auto", "auto"},
		{"AUTO", "auto"},
		{"unknown", "auto"},
		{"  nip44  ", "nip44"},
	}
	for _, tt := range tests {
		got := normalizeEncryptionMode(tt.input)
		if got != tt.want {
			t.Errorf("normalizeEncryptionMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ─── decodeServerEvent ────────────────────────────────────────────────────────

func TestDecodeServerEvent_Basic(t *testing.T) {
	ev := nostr.Event{
		Kind:      nostr.Kind(KindServerAnnouncement),
		CreatedAt: nostr.Timestamp(1700000000),
		Content:   `{"serverInfo":{"name":"TestServer"},"capabilities":{"tools":true}}`,
		Tags: nostr.Tags{
			{"about", "A test ContextVM server"},
			{"picture", "https://example.com/pic.png"},
			{"website", "https://example.com"},
			{"support_encryption", "nip44"},
		},
	}

	s := decodeServerEvent(ev)
	if s.Name != "TestServer" {
		t.Errorf("name: %q", s.Name)
	}
	if s.About != "A test ContextVM server" {
		t.Errorf("about: %q", s.About)
	}
	if s.Picture != "https://example.com/pic.png" {
		t.Errorf("picture: %q", s.Picture)
	}
	if s.Website != "https://example.com" {
		t.Errorf("website: %q", s.Website)
	}
	if !s.Encrypted {
		t.Error("expected encrypted=true")
	}
	if s.CreatedAt != 1700000000 {
		t.Errorf("created_at: %d", s.CreatedAt)
	}
	if s.Capabilities == nil {
		t.Error("expected capabilities")
	}
}

func TestDecodeServerEvent_NameFromTag(t *testing.T) {
	ev := nostr.Event{
		Kind: nostr.Kind(KindServerAnnouncement),
		Tags: nostr.Tags{{"name", "TagName"}},
	}
	s := decodeServerEvent(ev)
	if s.Name != "TagName" {
		t.Errorf("name from tag: %q", s.Name)
	}
}

func TestDecodeServerEvent_ContentOverridesTagName(t *testing.T) {
	ev := nostr.Event{
		Kind:    nostr.Kind(KindServerAnnouncement),
		Content: `{"serverInfo":{"name":"ContentName"}}`,
		Tags:    nostr.Tags{{"name", "TagName"}},
	}
	s := decodeServerEvent(ev)
	if s.Name != "ContentName" {
		t.Errorf("name should come from content: %q", s.Name)
	}
}

func TestDecodeServerEvent_EmptyContent(t *testing.T) {
	ev := nostr.Event{Kind: nostr.Kind(KindServerAnnouncement)}
	s := decodeServerEvent(ev)
	if s.Name != "" || s.About != "" || s.Capabilities != nil {
		t.Errorf("expected empty: %+v", s)
	}
}

func TestDecodeServerEvent_InvalidContentJSON(t *testing.T) {
	ev := nostr.Event{
		Kind:    nostr.Kind(KindServerAnnouncement),
		Content: "not json",
	}
	s := decodeServerEvent(ev)
	if s.Name != "" {
		t.Errorf("expected empty name for invalid JSON, got %q", s.Name)
	}
}

// ─── executeJSONRPC ───────────────────────────────────────────────────────────

func TestExecuteJSONRPC_Success(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","result":{"tools":[]}}`)

	result, err := executeJSONRPC(context.Background(), nil, nil, nil, "pk", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}, 30*time.Second, "none")
	if err != nil {
		t.Fatal(err)
	}
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteJSONRPC_ServerError(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","error":{"message":"internal error"}}`)

	_, err := executeJSONRPC(context.Background(), nil, nil, nil, "pk", map[string]any{}, 30*time.Second, "none")
	if err == nil || !strings.Contains(err.Error(), "internal error") {
		t.Errorf("expected server error, got: %v", err)
	}
}

func TestExecuteJSONRPC_NullResult(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","result":null}`)

	_, err := executeJSONRPC(context.Background(), nil, nil, nil, "pk", map[string]any{}, 30*time.Second, "none")
	if err == nil || !strings.Contains(err.Error(), "missing result") {
		t.Errorf("expected missing result error, got: %v", err)
	}
}

func TestExecuteJSONRPC_NetworkError(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestErr("connection refused")

	_, err := executeJSONRPC(context.Background(), nil, nil, nil, "pk", map[string]any{}, 30*time.Second, "none")
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected network error, got: %v", err)
	}
}

func TestExecuteJSONRPC_InvalidJSON(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`not json`)

	_, err := executeJSONRPC(context.Background(), nil, nil, nil, "pk", map[string]any{}, 30*time.Second, "none")
	if err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

// ─── ListTools ────────────────────────────────────────────────────────────────

// Note: ListTools uses sendRequest directly (not executeJSONRPC), so it goes
// through the real sendRequestWithTimeout which validates the pubkey. We can't
// mock it via the sendContextVMRequestWithTimeout variable. Test only what's
// reachable without network.

// ─── CallTool / CallToolWithTimeout ───────────────────────────────────────────

func TestCallTool_Success(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	var gotMethod string
	var gotParams map[string]any
	sendContextVMRequestWithTimeout = func(_ context.Context, _ *nostr.Pool, _ nostr.Keyer, _ []string, _ string, msg map[string]any, _ time.Duration, _ string) (json.RawMessage, error) {
		gotMethod, _ = msg["method"].(string)
		gotParams, _ = msg["params"].(map[string]any)
		return json.RawMessage(`{"jsonrpc":"2.0","result":{"content":[{"type":"text","text":"hi"}],"isError":false}}`), nil
	}

	result, err := CallTool(context.Background(), nil, nil, nil, "pk", "echo", map[string]any{"input": "test"}, "none")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "tools/call" {
		t.Errorf("method: %q", gotMethod)
	}
	if gotParams["name"] != "echo" {
		t.Errorf("tool name: %v", gotParams["name"])
	}
	if len(result.Content) != 1 {
		t.Errorf("content: %+v", result.Content)
	}
	if result.IsError {
		t.Error("expected isError=false")
	}
}

func TestCallToolWithTimeout_Propagates(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	var gotTimeout time.Duration
	sendContextVMRequestWithTimeout = func(_ context.Context, _ *nostr.Pool, _ nostr.Keyer, _ []string, _ string, _ map[string]any, timeout time.Duration, _ string) (json.RawMessage, error) {
		gotTimeout = timeout
		return json.RawMessage(`{"jsonrpc":"2.0","result":{"content":[],"isError":false}}`), nil
	}

	_, err := CallToolWithTimeout(context.Background(), nil, nil, nil, "pk", "tool", nil, 5*time.Second, "none")
	if err != nil {
		t.Fatal(err)
	}
	if gotTimeout != 5*time.Second {
		t.Errorf("timeout: %v", gotTimeout)
	}
}

// ─── ReadResource ─────────────────────────────────────────────────────────────

func TestReadResource_Success(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","result":{"contents":[{"uri":"file:///test","text":"hello"}]}}`)

	result, err := ReadResource(context.Background(), nil, nil, nil, "pk", "file:///test", "none")
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestReadResource_Error(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestErr("timeout")

	_, err := ReadResource(context.Background(), nil, nil, nil, "pk", "file:///test", "none")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── SendRaw ──────────────────────────────────────────────────────────────────

// Note: SendRaw uses sendRequest directly, which validates the server pubkey.
// Cannot mock without a valid hex pubkey and network. Covered via integration tests.

// ─── ListResources error paths ────────────────────────────────────────────────

func TestListResources_NetworkError(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()
	sendContextVMRequestWithTimeout = mockRequestErr("timeout")

	_, err := ListResources(context.Background(), nil, nil, nil, "pk", "none")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListResources_MissingResourcesKey(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()
	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","result":{"other":"data"}}`)

	_, err := ListResources(context.Background(), nil, nil, nil, "pk", "none")
	if err == nil || !strings.Contains(err.Error(), "missing resources") {
		t.Errorf("expected missing resources error, got: %v", err)
	}
}

// ─── ReadResource via executeJSONRPC ──────────────────────────────────────────

func TestReadResource_NetworkError(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()
	sendContextVMRequestWithTimeout = mockRequestErr("timeout")

	_, err := ReadResource(context.Background(), nil, nil, nil, "pk", "file:///x", "none")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── ListPrompts additional ───────────────────────────────────────────────────

func TestListPrompts_Success(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()
	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","result":{"prompts":[{"name":"review"}]}}`)

	prompts, err := ListPrompts(context.Background(), nil, nil, nil, "pk", "none")
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 {
		t.Errorf("prompts: %+v", prompts)
	}
}

func TestListPrompts_MissingPromptsKey(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()
	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","result":{"other":"data"}}`)

	_, err := ListPrompts(context.Background(), nil, nil, nil, "pk", "none")
	if err == nil || !strings.Contains(err.Error(), "missing prompts") {
		t.Errorf("expected missing prompts error, got: %v", err)
	}
}

// ─── CallTool error path ──────────────────────────────────────────────────────

func TestCallTool_ServerError(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()
	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","error":{"message":"tool not found"}}`)

	_, err := CallTool(context.Background(), nil, nil, nil, "pk", "unknown_tool", nil, "none")
	if err == nil || !strings.Contains(err.Error(), "tool not found") {
		t.Errorf("expected error, got: %v", err)
	}
}

// ─── GetPrompt additional ─────────────────────────────────────────────────────

func TestGetPrompt_NoArgs(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	var gotParams map[string]any
	sendContextVMRequestWithTimeout = func(_ context.Context, _ *nostr.Pool, _ nostr.Keyer, _ []string, _ string, msg map[string]any, _ time.Duration, _ string) (json.RawMessage, error) {
		gotParams, _ = msg["params"].(map[string]any)
		return json.RawMessage(`{"jsonrpc":"2.0","result":{"messages":[]}}`), nil
	}

	_, err := GetPrompt(context.Background(), nil, nil, nil, "pk", "simple", nil, "none")
	if err != nil {
		t.Fatal(err)
	}
	if _, hasArgs := gotParams["arguments"]; hasArgs {
		t.Error("should not include arguments key when args is nil")
	}
}

// ─── encryptForServer ─────────────────────────────────────────────────────────

func TestEncryptForServer_NoneMode(t *testing.T) {
	// "none" mode should return plaintext unchanged
	var zeroPK nostr.PubKey
	result, err := encryptForServer(context.Background(), nil, zeroPK, "hello", "none")
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello" {
		t.Errorf("expected passthrough, got %q", result)
	}
}

func TestEncryptForServer_UnsupportedMode(t *testing.T) {
	var zeroPK nostr.PubKey
	_, err := encryptForServer(context.Background(), nil, zeroPK, "hello", "unsupported-mode")
	if err == nil {
		t.Fatal("expected error for unsupported mode")
	}
}

// ─── JSON struct tests ────────────────────────────────────────────────────────

func TestServerInfo_JSONRoundTrip(t *testing.T) {
	s := ServerInfo{
		PubKey: "pk1", Name: "Test", About: "about",
		Encrypted: true, CreatedAt: 1700000000,
	}
	b, _ := json.Marshal(s)
	var decoded ServerInfo
	json.Unmarshal(b, &decoded)
	if decoded.PubKey != s.PubKey || decoded.Name != s.Name || decoded.Encrypted != s.Encrypted {
		t.Errorf("mismatch: %+v", decoded)
	}
}

func TestToolDef_JSONRoundTrip(t *testing.T) {
	td := ToolDef{Name: "echo", Description: "echoes", InputSchema: map[string]any{"type": "object"}}
	b, _ := json.Marshal(td)
	var decoded ToolDef
	json.Unmarshal(b, &decoded)
	if decoded.Name != td.Name {
		t.Errorf("mismatch: %+v", decoded)
	}
}

func TestCallResult_JSONRoundTrip(t *testing.T) {
	cr := CallResult{
		Content: []map[string]any{{"type": "text", "text": "hi"}},
		IsError: false,
	}
	b, _ := json.Marshal(cr)
	var decoded CallResult
	json.Unmarshal(b, &decoded)
	if len(decoded.Content) != 1 || decoded.IsError {
		t.Errorf("mismatch: %+v", decoded)
	}
}
