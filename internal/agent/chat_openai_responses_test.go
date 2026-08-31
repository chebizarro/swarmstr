package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIResponsesBuildRequest(t *testing.T) {
	p := &OpenAIResponsesProvider{Model: "gpt-4.1"}
	req := p.buildRequest([]LLMMessage{{Role: "user", Content: "hi"}}, []ToolDefinition{{Name: "lookup"}}, ChatOptions{MaxTokens: 7, ThinkingBudget: 1})
	if req.Model != "gpt-4.1" || req.MaxOutputTokens != 7 {
		t.Fatalf("bad request: %+v", req)
	}
	if len(req.Input) != 1 || len(req.Tools) != 1 || req.Reasoning == nil {
		t.Fatalf("missing mapping: %+v", req)
	}
}

func TestOpenAIResponsesStreamEvents(t *testing.T) {
	var gotAuth, gotAccept string
	var gotBody responsesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"type":"response.reasoning_summary_text.delta","delta":"plan "}`,
			`{"type":"response.output_text.delta","delta":"hel"}`,
			`{"type":"response.output_text.delta","delta":"lo"}`,
			`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"lookup"}}`,
			`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"q\":"}`,
			`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"\"x\"}"}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call-1","name":"lookup","arguments":"{\"q\":\"x\"}"}}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":12,"output_tokens":5,"input_tokens_details":{"cached_tokens":4}}}}`,
		}
		for _, frame := range frames {
			_, _ = w.Write([]byte("data: " + frame + "\n\n"))
		}
	}))
	defer srv.Close()

	provider := &OpenAIResponsesProvider{BaseURL: srv.URL, APIKey: "test-key", Model: "gpt-4.1", Client: srv.Client()}
	var events []ProviderStreamEvent
	result, err := provider.StreamEvents(context.Background(), Turn{UserText: "hi", Tools: []ToolDefinition{{Name: "lookup"}}}, func(event ProviderStreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-key" || gotAccept != "text/event-stream" || !gotBody.Stream || len(gotBody.Tools) != 1 {
		t.Fatalf("request auth=%q accept=%q body=%+v", gotAuth, gotAccept, gotBody)
	}
	if result.Text != "hello" || len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call-1" || result.ToolCalls[0].Name != "lookup" || result.ToolCalls[0].Args["q"] != "x" {
		t.Fatalf("result=%#v", result)
	}
	if result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 5 || result.Usage.CacheReadTokens != 4 {
		t.Fatalf("usage=%#v", result.Usage)
	}
	if len(events) < 4 || events[0].Type != ProviderStreamStart || events[len(events)-1].Type != ProviderStreamEnd {
		t.Fatalf("events=%#v", events)
	}
	seenThinking, seenUsage := false, false
	for _, event := range events {
		seenThinking = seenThinking || event.Type == ProviderStreamThinkingDelta
		seenUsage = seenUsage || event.Type == ProviderStreamUsage
	}
	if !seenThinking || !seenUsage {
		t.Fatalf("events missing thinking/usage: %#v", events)
	}
}

func TestAzureResponsesStreamEvents(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.RequestURI(), r.Header.Get("api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"azure\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n"))
	}))
	defer srv.Close()
	provider := &AzureResponsesProvider{BaseURL: srv.URL, APIKey: "azure-key", Model: "gpt-4.1", Client: srv.Client()}
	result, err := provider.StreamEvents(context.Background(), Turn{UserText: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/openai/responses?api-version=2025-04-01-preview" || gotKey != "azure-key" || result.Text != "azure" {
		t.Fatalf("path=%q key=%q result=%#v", gotPath, gotKey, result)
	}
}

func TestResponsesStreamFailureHasNoEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"bad\",\"message\":\"rejected\"}}}\n\n"))
	}))
	defer srv.Close()
	provider := &OpenAIResponsesProvider{BaseURL: srv.URL, Client: srv.Client()}
	var events []ProviderStreamEvent
	_, err := provider.StreamEvents(context.Background(), Turn{UserText: "hi"}, func(event ProviderStreamEvent) { events = append(events, event) })
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("err=%v", err)
	}
	if len(events) != 2 || events[0].Type != ProviderStreamStart || events[1].Type != ProviderStreamError {
		t.Fatalf("events=%#v", events)
	}
}

func TestResponsesStreamRequiresCompleted(t *testing.T) {
	_, err := consumeResponsesStream(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: [DONE]\n\n"), nil)
	if err == nil || !strings.Contains(err.Error(), "before response.completed") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseResponsesAPINestedContentAndUsage(t *testing.T) {
	resp, err := parseResponsesAPI([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"nested"}]},{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{\"x\":1}"}],"usage":{"input_tokens":7,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "nested" || len(resp.ToolCalls) != 1 || resp.Usage.CacheReadTokens != 2 {
		t.Fatalf("resp=%#v", resp)
	}
}
