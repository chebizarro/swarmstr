package agent

import "testing"

func TestMistralBuildRequestRequiresToolCallIDs(t *testing.T) {
	p := &MistralChatProvider{Model: "mistral-large-latest"}
	_, err := p.buildRequest([]LLMMessage{{Role: "assistant", ToolCalls: []ToolCall{{Name: "lookup"}}}}, nil, ChatOptions{})
	if err == nil {
		t.Fatal("expected missing tool id error")
	}
}

func TestMistralBuildRequestMapsTools(t *testing.T) {
	p := &MistralChatProvider{Model: "mistral-large-latest"}
	req, err := p.buildRequest([]LLMMessage{{Role: "user", Content: "hi"}}, []ToolDefinition{{Name: "lookup", Description: "d"}}, ChatOptions{MaxTokens: 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Model != "mistral-large-latest" || req.Stream {
		t.Fatalf("bad request: %+v", req)
	}
	if len(req.Tools) != 1 || req.ToolChoice != "auto" {
		t.Fatalf("tools not mapped: %+v", req)
	}
}

func TestKimiModelNormalization(t *testing.T) {
	if got := normalizeKimiModelID("kimi/kimi-k2"); got != "kimi-k2" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeKimiModelID("kimicode/k2"); got != "k2" {
		t.Fatalf("got %q", got)
	}
}
