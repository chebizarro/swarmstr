package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
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

func TestResponsesTransportPolicyAndFallbackDecision(t *testing.T) {
	for _, raw := range []string{"", "auto", "sse", "websocket", " WebSocket "} {
		if _, err := parseResponsesTransportPolicy(raw); err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
	}
	if _, err := parseResponsesTransportPolicy("poll"); err == nil {
		t.Fatal("expected invalid transport policy error")
	}
	transportErr := errors.New("dial failed")
	cases := []struct {
		policy ResponsesTransportPolicy
		seen   bool
		err    error
		want   bool
	}{
		{ResponsesTransportAuto, false, transportErr, true},
		{ResponsesTransportAuto, true, transportErr, false},
		{ResponsesTransportWebSocket, false, transportErr, false},
		{ResponsesTransportSSE, false, transportErr, false},
		{ResponsesTransportAuto, false, context.Canceled, false},
		{ResponsesTransportAuto, false, nil, false},
	}
	for _, tc := range cases {
		if got := shouldFallbackResponsesWebSocket(tc.policy, tc.seen, tc.err); got != tc.want {
			t.Errorf("policy=%q seen=%v err=%v: got %v want %v", tc.policy, tc.seen, tc.err, got, tc.want)
		}
	}
}

func TestResponsesContinuationDeltaAndInvalidation(t *testing.T) {
	base := responsesRequest{
		Model: "gpt-4.1", Stream: true, Store: true,
		Input: []map[string]any{{"role": "system", "content": "rules"}, {"role": "user", "content": "one"}},
		Tools: []map[string]any{{"type": "function", "name": "lookup"}},
	}
	cached := &responsesContinuation{
		lastRequest:       cloneResponsesRequest(base),
		lastResponseID:    "resp-1",
		lastResponseInput: []map[string]any{{"role": "assistant", "content": "answer"}},
	}
	next := cloneResponsesRequest(base)
	next.Input = append(next.Input,
		map[string]any{"role": "assistant", "content": "answer"},
		map[string]any{"role": "user", "content": "two"},
	)
	continued, ok := prepareResponsesContinuation(next, cached)
	if !ok || continued.PreviousResponseID != "resp-1" || len(continued.Input) != 1 || continued.Input[0]["content"] != "two" {
		t.Fatalf("continued=%+v ok=%v", continued, ok)
	}

	contextEdited := cloneResponsesRequest(next)
	contextEdited.Input[0]["content"] = "edited rules"
	if _, ok := prepareResponsesContinuation(contextEdited, cached); ok {
		t.Fatal("context edit must invalidate continuation")
	}
	compacted := cloneResponsesRequest(next)
	compacted.Input = compacted.Input[len(compacted.Input)-1:]
	if _, ok := prepareResponsesContinuation(compacted, cached); ok {
		t.Fatal("compaction must invalidate continuation")
	}
	modelSwitched := cloneResponsesRequest(next)
	modelSwitched.Model = "gpt-4.1-mini"
	if _, ok := prepareResponsesContinuation(modelSwitched, cached); ok {
		t.Fatal("model switch must invalidate continuation")
	}
	toolsEdited := cloneResponsesRequest(next)
	toolsEdited.Tools[0]["name"] = "other"
	if _, ok := prepareResponsesContinuation(toolsEdited, cached); ok {
		t.Fatal("request/tool edit must invalidate continuation")
	}
}

func TestOpenAIResponsesSSEContinuationPerSession(t *testing.T) {
	var requests []responsesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request responsesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requests = append(requests, request)
		index := len(requests)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"answer"}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp-` + fmt.Sprint(index) + `","usage":{"input_tokens":2,"output_tokens":1}}}` + "\n\n"))
	}))
	defer srv.Close()

	provider := &OpenAIResponsesProvider{BaseURL: srv.URL, Model: "gpt-4.1", Client: srv.Client(), Transport: ResponsesTransportSSE}
	if _, err := provider.StreamEvents(context.Background(), Turn{SessionID: "session-a", UserText: "one"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.StreamEvents(context.Background(), Turn{
		SessionID: "session-a", UserText: "two",
		History: []ConversationMessage{{Role: "user", Content: "one"}, {Role: "assistant", Content: "answer"}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || !requests[0].Store || requests[0].PreviousResponseID != "" {
		t.Fatalf("first request=%+v all=%d", requests[0], len(requests))
	}
	if requests[1].PreviousResponseID != "resp-1" || len(requests[1].Input) != 1 {
		t.Fatalf("continuation request=%+v", requests[1])
	}
}

func TestOpenAIResponsesWebSocketReusesSessionConnection(t *testing.T) {
	var mu sync.Mutex
	var connections int
	var requests []responsesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		mu.Lock()
		connections++
		mu.Unlock()
		for index := 1; index <= 2; index++ {
			_, raw, readErr := conn.Read(context.Background())
			if readErr != nil {
				t.Errorf("read websocket request: %v", readErr)
				return
			}
			var wire struct {
				Type string `json:"type"`
				responsesRequest
			}
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Errorf("decode websocket request: %v", err)
				return
			}
			if wire.Type != "response.create" {
				t.Errorf("websocket type=%q", wire.Type)
				return
			}
			mu.Lock()
			requests = append(requests, wire.responsesRequest)
			mu.Unlock()
			frames := []string{
				`{"type":"response.output_text.delta","delta":"answer"}`,
				`{"type":"response.completed","response":{"id":"resp-` + fmt.Sprint(index) + `"}}`,
			}
			for _, frame := range frames {
				if err := conn.Write(context.Background(), websocket.MessageText, []byte(frame)); err != nil {
					t.Errorf("write websocket frame: %v", err)
					return
				}
			}
		}
	}))
	defer srv.Close()

	provider := &OpenAIResponsesProvider{BaseURL: srv.URL, Model: "gpt-4.1", Client: srv.Client(), Transport: ResponsesTransportWebSocket}
	if _, err := provider.StreamEvents(context.Background(), Turn{SessionID: "session-ws", UserText: "one"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.StreamEvents(context.Background(), Turn{
		SessionID: "session-ws", UserText: "two",
		History: []ConversationMessage{{Role: "user", Content: "one"}, {Role: "assistant", Content: "answer"}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if connections != 1 || len(requests) != 2 {
		t.Fatalf("connections=%d requests=%d", connections, len(requests))
	}
	if requests[0].Stream || !requests[0].Store || requests[1].PreviousResponseID != "resp-1" || len(requests[1].Input) != 1 {
		t.Fatalf("websocket requests=%+v", requests)
	}
}

func TestOpenAIResponsesAutoFallsBackToSSEBeforeProviderEvent(t *testing.T) {
	var websocketAttempts, sseAttempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			websocketAttempts++
			http.Error(w, "websocket unavailable", http.StatusBadGateway)
			return
		}
		sseAttempts++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"sse\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-sse\"}}\n\n"))
	}))
	defer srv.Close()
	provider := &OpenAIResponsesProvider{BaseURL: srv.URL, Model: "gpt-4.1", Client: srv.Client(), Transport: ResponsesTransportAuto}
	result, err := provider.StreamEvents(context.Background(), Turn{UserText: "hello"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "sse" || websocketAttempts != 1 || sseAttempts != 1 {
		t.Fatalf("result=%+v websocket=%d sse=%d", result, websocketAttempts, sseAttempts)
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
