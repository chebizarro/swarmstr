package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newGeminiRewriteClient points the provider at the httptest server by rewriting
// the hard-coded generativelanguage.googleapis.com base in the request URL.
func newGeminiRewriteClient(srv *httptest.Server) *http.Client {
	return newRewriteClient(srv.Client(), "https://generativelanguage.googleapis.com", srv.URL)
}

// TestGeminiChatProvider_TextResponse exercises the real Chat request/response
// path: it asserts the outbound request JSON (system instruction + user
// contents) and parses a realistic text-only Gemini response with usage.
func TestGeminiChatProvider_TextResponse(t *testing.T) {
	var captured geminiRequest
	var capturedPath, capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query().Get("key")
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates":[{"content":{"parts":[{"text":"Hello from Gemini!"}]}}],
			"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":5,"cachedContentTokenCount":3}
		}`))
	}))
	defer srv.Close()

	p := &GeminiChatProvider{
		APIKey: "test-key",
		Model:  "gemini-2.0-flash",
		Client: newGeminiRewriteClient(srv),
	}
	resp, err := p.Chat(context.Background(), []LLMMessage{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Say hello"},
	}, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Outbound request assertions.
	if capturedPath != "/v1beta/models/gemini-2.0-flash:generateContent" {
		t.Errorf("unexpected request path: %q", capturedPath)
	}
	if capturedQuery != "test-key" {
		t.Errorf("expected API key in query, got %q", capturedQuery)
	}
	if captured.SystemInstruction == nil || len(captured.SystemInstruction.Parts) != 1 ||
		captured.SystemInstruction.Parts[0].Text != "You are helpful" {
		t.Fatalf("unexpected system instruction: %#v", captured.SystemInstruction)
	}
	if len(captured.Contents) != 1 || captured.Contents[0].Role != "user" ||
		len(captured.Contents[0].Parts) != 1 || captured.Contents[0].Parts[0].Text != "Say hello" {
		t.Fatalf("unexpected contents: %#v", captured.Contents)
	}

	// Response parsing assertions.
	if resp.Content != "Hello from Gemini!" {
		t.Errorf("content: got %q", resp.Content)
	}
	if len(resp.ToolCalls) != 0 || resp.NeedsToolResults {
		t.Errorf("expected no tool calls, got %#v", resp.ToolCalls)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 5 || resp.Usage.CacheReadTokens != 3 {
		t.Errorf("unexpected usage: %#v", resp.Usage)
	}
}

// TestGeminiChatProvider_ToolCallRequestAndResponse asserts that tool
// definitions and assistant/tool history are encoded into the request as
// functionDeclarations / functionCall / functionResponse parts, and that a
// Gemini functionCall response is converted back into a ToolCall.
func TestGeminiChatProvider_ToolCallRequestAndResponse(t *testing.T) {
	var captured geminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		// Realistic Gemini response requesting a tool call.
		_, _ = w.Write([]byte(`{
			"candidates":[{"content":{"parts":[
				{"functionCall":{"name":"search","args":{"query":"cats"}}}
			]}}]
		}`))
	}))
	defer srv.Close()

	tools := []ToolDefinition{{
		Name:        "search",
		Description: "Search the web",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolParamProp{
				"query": {Type: "string", Description: "search query"},
			},
			Required: []string{"query"},
		},
	}}

	p := &GeminiChatProvider{
		APIKey: "test-key",
		Model:  "gemini-2.0-flash",
		Client: newGeminiRewriteClient(srv),
	}
	resp, err := p.Chat(context.Background(), []LLMMessage{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Search for cats"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "search", Name: "search", Args: map[string]any{"query": "cats"}},
		}},
		{Role: "tool", ToolCallID: "search", Content: "Found: cats are great"},
	}, tools, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Assert tools were sent as functionDeclarations.
	if len(captured.Tools) != 1 || len(captured.Tools[0].FunctionDeclarations) != 1 ||
		captured.Tools[0].FunctionDeclarations[0].Name != "search" {
		t.Fatalf("unexpected tools payload: %#v", captured.Tools)
	}

	// Walk contents and verify the functionCall (assistant) and
	// functionResponse (tool result) parts round-tripped correctly.
	var sawFunctionCall, sawFunctionResponse bool
	for _, c := range captured.Contents {
		for _, part := range c.Parts {
			if part.FunctionCall != nil {
				sawFunctionCall = true
				if c.Role != "model" || part.FunctionCall.Name != "search" ||
					part.FunctionCall.Args["query"] != "cats" {
					t.Errorf("unexpected functionCall part: role=%s %#v", c.Role, part.FunctionCall)
				}
			}
			if part.FunctionResponse != nil {
				sawFunctionResponse = true
				if c.Role != "function" || part.FunctionResponse.Name != "search" {
					t.Errorf("unexpected functionResponse part: role=%s %#v", c.Role, part.FunctionResponse)
				}
				if part.FunctionResponse.Response["result"] != "Found: cats are great" {
					t.Errorf("unexpected functionResponse body: %#v", part.FunctionResponse.Response)
				}
			}
		}
	}
	if !sawFunctionCall {
		t.Error("expected a functionCall part in request contents")
	}
	if !sawFunctionResponse {
		t.Error("expected a functionResponse part in request contents")
	}

	// Assert the response functionCall was converted into a ToolCall.
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %#v", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.Name != "search" || tc.ID != "search" || tc.Args["query"] != "cats" {
		t.Errorf("unexpected converted tool call: %#v", tc)
	}
	if !resp.NeedsToolResults {
		t.Error("expected NeedsToolResults=true when the model requests a tool call")
	}
}

// TestGeminiChatProvider_APIError verifies a Gemini error envelope is surfaced.
func TestGeminiChatProvider_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"quota exceeded"}}`))
	}))
	defer srv.Close()

	p := &GeminiChatProvider{
		APIKey: "test-key",
		Model:  "gemini-2.0-flash",
		Client: newGeminiRewriteClient(srv),
	}
	_, err := p.Chat(context.Background(), []LLMMessage{{Role: "user", Content: "hi"}}, nil, ChatOptions{})
	if err == nil {
		t.Fatal("expected API error")
	}
	if got := err.Error(); !strings.Contains(got, "429") || !strings.Contains(got, "quota exceeded") {
		t.Errorf("unexpected error: %v", got)
	}
}

func TestGeminiChatProvider_UsesFunctionNamesForHistoricalToolResults(t *testing.T) {
	var captured geminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"done"}]}}]}`))
	}))
	defer srv.Close()

	p := &GeminiChatProvider{
		APIKey: "test-key",
		Model:  "gemini-2.0-flash",
		Client: newRewriteClient(srv.Client(), "https://generativelanguage.googleapis.com", srv.URL),
	}
	_, err := p.Chat(context.Background(), []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tool:1", Name: "search", Args: map[string]any{"q": "cats"}}}},
		{Role: "tool", ToolCallID: "tool:1", Content: "Found cats"},
	}, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	// With Gemini transcript policy (EnforceRoleAlternation + RequireLeadingUser),
	// a synthetic user message is prepended, so contents are:
	// [0]=user(synthetic), [1]=model(assistant+toolcall), [2]=function(tool result)
	var functionResponseName string
	for _, c := range captured.Contents {
		for _, p := range c.Parts {
			if p.FunctionResponse != nil {
				functionResponseName = p.FunctionResponse.Name
			}
		}
	}
	if functionResponseName != "search" {
		t.Fatalf("expected function response name to use tool name, got %q", functionResponseName)
	}
}

func TestGeminiChatProvider_GenerateWithAgenticLoop(t *testing.T) {
	// Verify GoogleGeminiProvider.Generate delegates to generateWithAgenticLoop.
	p := &GoogleGeminiProvider{Model: "gemini-2.0-flash", APIKey: ""}

	// Without an API key, Generate should return an error about missing key.
	_, err := p.Generate(context.Background(), Turn{UserText: "hi"})
	if err == nil {
		t.Fatal("expected error with missing API key")
	}
	expected := "Gemini API key not configured"
	if got := err.Error(); len(got) < len(expected) || got[:len(expected)] != expected {
		t.Errorf("unexpected error: %v", err)
	}
}
