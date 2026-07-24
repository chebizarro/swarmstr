package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVertexStreamEvents(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.RequestURI(), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"vertex\"}]}}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":1}}\n\n"))
	}))
	defer srv.Close()
	p := &VertexChatProvider{BaseURL: srv.URL, APIKey: "token", Model: normalizeVertexModel("vertex/gemini-2.5-flash"), Client: srv.Client()}
	var events []ProviderStreamEvent
	result, err := p.StreamEvents(context.Background(), Turn{UserText: "hi"}, func(evt ProviderStreamEvent) { events = append(events, evt) })
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse" || gotAuth != "Bearer token" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if result.Text != "vertex" || result.Usage.InputTokens != 2 || len(events) != 4 || events[0].Type != ProviderStreamStart || events[3].Type != ProviderStreamEnd {
		t.Fatalf("result=%#v events=%#v", result, events)
	}
}

func TestVertexBuildRequestAndModel(t *testing.T) {
	p := &VertexChatProvider{Model: normalizeVertexModel("vertex/gemini-2.5-flash")}
	req := p.buildRequest([]LLMMessage{{Role: "system", Content: "sys"}, {Role: "assistant", Content: "ok"}}, nil, ChatOptions{MaxTokens: 5})
	if p.Model != "publishers/google/models/gemini-2.5-flash" {
		t.Fatalf("bad model: %s", p.Model)
	}
	if req.SystemInstruction == nil || len(req.Contents) != 1 || req.Contents[0].Role != "model" {
		t.Fatalf("bad request: %+v", req)
	}
}
