package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMistralStreamDefaultsStayOnMistral(t *testing.T) {
	compat := (&MistralChatProvider{}).openAICompatibleProvider()
	if compat.BaseURL != "https://api.mistral.ai/v1" || compat.Model != "mistral-large-latest" {
		t.Fatalf("compat=%#v", compat)
	}
}

func TestMistralStreamDelegatesOpenAICompatibleSSE(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"mis"}}]}`)
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"tral"}}]}`)
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "data: [DONE]")
	}))
	defer srv.Close()
	p := &MistralChatProvider{BaseURL: srv.URL, APIKey: "secret", Model: "mistral-large-latest", Client: srv.Client()}
	var chunks []string
	result, err := p.Stream(context.Background(), Turn{UserText: "hi"}, func(chunk string) { chunks = append(chunks, chunk) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "mistral" || len(chunks) != 2 || auth != "Bearer secret" {
		t.Fatalf("result=%#v chunks=%#v auth=%q", result, chunks, auth)
	}
}

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
