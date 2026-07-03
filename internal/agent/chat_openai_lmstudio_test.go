package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests give deterministic, always-on coverage for the OpenAI-compatible
// request/response path that LM Studio speaks. They stand in for the live LM
// Studio integration tests (chat_openai_lmstudio_live_test.go, build tag
// `lmstudio_live`), which require a running localhost server and are excluded
// from the default build.

// TestLMStudioCompatChat_RequestSchemaAndChatResponse asserts the OpenAI-compat
// request body (model, messages, tools) and parses a plain chat response.
func TestLMStudioCompatChat_RequestSchemaAndChatResponse(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %q", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-1","object":"chat.completion",
			"choices":[{"index":0,"message":{"role":"assistant","content":"4"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":12,"completion_tokens":1,"total_tokens":13}
		}`))
	}))
	defer srv.Close()

	provider := &OpenAIChatProviderChat{
		BaseURL: srv.URL,
		Model:   "openai/gpt-oss-20b",
	}
	resp, err := provider.Chat(context.Background(), []LLMMessage{
		{Role: "system", Content: "You are terse."},
		{Role: "user", Content: "What is 2+2?"},
	}, []ToolDefinition{{
		Name:        "file_tree",
		Description: "Generate a recursive directory tree.",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolParamProp{
				"path":      {Type: "string", Description: "Root directory."},
				"max_depth": {Type: "integer", Description: "Max recursion depth."},
			},
			Required: []string{"path"},
		},
	}}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// ── Request schema assertions ──
	if body["model"] != "openai/gpt-oss-20b" {
		t.Errorf("model: got %v", body["model"])
	}
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %#v", body["messages"])
	}
	sys := msgs[0].(map[string]any)
	if sys["role"] != "system" || sys["content"] != "You are terse." {
		t.Errorf("unexpected system message: %#v", sys)
	}
	usr := msgs[1].(map[string]any)
	if usr["role"] != "user" || usr["content"] != "What is 2+2?" {
		t.Errorf("unexpected user message: %#v", usr)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %#v", body["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type: %#v", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "file_tree" {
		t.Errorf("tool name: %#v", fn["name"])
	}
	params := fn["parameters"].(map[string]any)
	props, ok := params["properties"].(map[string]any)
	if !ok || props["path"] == nil || props["max_depth"] == nil {
		t.Errorf("expected path/max_depth properties, got %#v", params["properties"])
	}

	// ── Response parsing assertions ──
	if resp.Content != "4" {
		t.Errorf("content: got %q", resp.Content)
	}
	if len(resp.ToolCalls) != 0 || resp.NeedsToolResults {
		t.Errorf("expected no tool calls, got %#v", resp.ToolCalls)
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 1 {
		t.Errorf("unexpected usage: %#v", resp.Usage)
	}
}

// TestLMStudioCompatChat_ToolCallResponse parses an OpenAI-compatible tool-call
// response into ToolCall values with decoded arguments.
func TestLMStudioCompatChat_ToolCallResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-2","object":"chat.completion",
			"choices":[{"index":0,"message":{"role":"assistant","content":"",
				"tool_calls":[{"id":"call_1","type":"function",
					"function":{"name":"file_tree","arguments":"{\"path\":\".\",\"max_depth\":2}"}}]},
				"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}
		}`))
	}))
	defer srv.Close()

	provider := &OpenAIChatProviderChat{BaseURL: srv.URL, Model: "openai/gpt-oss-20b"}
	resp, err := provider.Chat(context.Background(),
		[]LLMMessage{{Role: "user", Content: "list files"}},
		[]ToolDefinition{{Name: "file_tree", Description: "tree"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %#v", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "file_tree" {
		t.Errorf("unexpected tool call identity: %#v", tc)
	}
	if tc.Args["path"] != "." {
		t.Errorf("expected path=., got %#v", tc.Args["path"])
	}
	if d, ok := tc.Args["max_depth"].(float64); !ok || d != 2 {
		t.Errorf("expected max_depth=2, got %#v", tc.Args["max_depth"])
	}
	if !resp.NeedsToolResults {
		t.Error("expected NeedsToolResults=true for finish_reason=tool_calls")
	}
}

// TestLMStudioCompatChat_ErrorResponse verifies a non-2xx OpenAI-compatible
// error surfaces as an error from Chat.
func TestLMStudioCompatChat_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"model not found","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	provider := &OpenAIChatProviderChat{BaseURL: srv.URL, Model: "missing-model"}
	_, err := provider.Chat(context.Background(),
		[]LLMMessage{{Role: "user", Content: "hi"}}, nil, ChatOptions{})
	if err == nil {
		t.Fatal("expected error from 400 response")
	}
	if !strings.Contains(err.Error(), "openai") {
		t.Errorf("expected wrapped openai error, got %v", err)
	}
}
