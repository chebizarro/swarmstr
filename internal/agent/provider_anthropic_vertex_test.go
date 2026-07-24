package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicVertexProviderStreamsNativeProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer vertex-token" {
			t.Errorf("authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["anthropic_version"] != "vertex-2023-10-16" || payload["stream"] != true {
			t.Errorf("payload = %#v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":8,"cache_read_input_tokens":2}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"plan"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
			`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool-1","name":"search"}}`,
			`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"go\"}"}}`,
			`{"type":"message_delta","usage":{"output_tokens":5,"cache_creation_input_tokens":1}}`,
		}
		for _, frame := range frames {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", frame)
		}
	}))
	defer server.Close()

	provider := &AnthropicVertexProvider{Model: "anthropic-vertex/claude-sonnet-4@20250514", AccessToken: "vertex-token", BaseURL: server.URL, Client: server.Client()}
	var events []ProviderStreamEvent
	result, err := provider.StreamEvents(context.Background(), Turn{UserText: "hi", Tools: []ToolDefinition{{Name: "search", InputJSONSchema: map[string]any{"type": "object"}}}}, func(event ProviderStreamEvent) { events = append(events, event) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "answer" || len(result.ToolCalls) != 1 || result.ToolCalls[0].Args["q"] != "go" {
		t.Fatalf("result = %#v", result)
	}
	if result.Usage.InputTokens != 8 || result.Usage.OutputTokens != 5 || result.Usage.CacheReadTokens != 2 || result.Usage.CacheCreationTokens != 1 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	if len(events) < 7 || events[0].Type != ProviderStreamStart || events[len(events)-1].Type != ProviderStreamEnd {
		t.Fatalf("events = %#v", events)
	}
}
