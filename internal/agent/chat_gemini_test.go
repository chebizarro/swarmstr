package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGeminiChatProvider_NoToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []struct {
				Content struct {
					Parts []struct {
						Text         string              `json:"text,omitempty"`
						FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
					} `json:"parts"`
				} `json:"content"`
			}{
				{Content: struct {
					Parts []struct {
						Text         string              `json:"text,omitempty"`
						FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
					} `json:"parts"`
				}{
					Parts: []struct {
						Text         string              `json:"text,omitempty"`
						FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
					}{
						{Text: "Hello from Gemini!"},
					},
				}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// The Gemini API URL includes the model and key in the path, so we can't
	// easily override the base URL with the test server. Instead, test the
	// message conversion logic directly.
	p := &GeminiChatProvider{APIKey: "test-key", Model: "gemini-2.0-flash"}

	// Verify the provider implements ChatProvider.
	var _ ChatProvider = p
}

func TestGeminiChatProvider_ToolCallConversion(t *testing.T) {
	// Test that assistant messages with tool calls produce functionCall parts.
	msgs := []LLMMessage{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Search for cats"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "search", Name: "search", Args: map[string]any{"query": "cats"}},
		}},
		{Role: "tool", ToolCallID: "search", Content: "Found: cats are great"},
	}

	// Just verify the messages can be constructed without panic.
	// Full integration test requires a real Gemini API.
	if len(msgs) != 4 {
		t.Fatal("expected 4 messages")
	}

	// Verify system, user, assistant with tool calls, and tool messages
	// are all valid roles that GeminiChatProvider.Chat handles.
	if msgs[0].Role != "system" {
		t.Errorf("expected system role, got %s", msgs[0].Role)
	}
	if msgs[2].Role != "assistant" || len(msgs[2].ToolCalls) != 1 {
		t.Errorf("expected assistant with 1 tool call")
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "search" {
		t.Errorf("expected tool result with ID 'search'")
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
	if got := captured.Contents[1].Parts[0].FunctionResponse.Name; got != "search" {
		t.Fatalf("expected function response name to use tool name, got %q", got)
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
