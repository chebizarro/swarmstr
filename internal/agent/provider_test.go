package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8ByBytes_DoesNotSplitRunes(t *testing.T) {
	input := strings.Repeat("🙂", 5000)
	out := truncateUTF8ByBytes(input, 16*1024)
	if len(out) > 16*1024 {
		t.Fatalf("len(out) = %d, want <= %d", len(out), 16*1024)
	}
	if !utf8.ValidString(out) {
		t.Fatal("output is not valid UTF-8")
	}
}

func TestTruncateUTF8ByBytes_PreservesASCIIPrefix(t *testing.T) {
	input := "hello world"
	out := truncateUTF8ByBytes(input, 5)
	if out != "hello" {
		t.Fatalf("out = %q, want %q", out, "hello")
	}
}

func TestHTTPProvider_ToolCallReEntersInference(t *testing.T) {
	var mu sync.Mutex
	var bodies []httpChatRequest
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req httpChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, req)
		callCount++
		n := callCount
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			// First inference pass: request a tool call, no final text.
			_ = json.NewEncoder(w).Encode(httpResponse{
				ToolCalls: []ToolCall{{ID: "tc1", Name: "lookup", Args: map[string]any{"q": "weather"}}},
			})
			return
		}
		// Second inference pass: the request must carry the tool result so the
		// provider can reason over it. Return the final answer.
		_ = json.NewEncoder(w).Encode(httpResponse{Text: "It is sunny."})
	}))
	defer srv.Close()

	provider := &HTTPProvider{URL: srv.URL, Client: srv.Client()}
	executor := &mockToolExecutor{results: map[string]string{"lookup": "weather=sunny"}}

	res, err := provider.Generate(context.Background(), Turn{
		SessionID: "sess-1",
		UserText:  "what is the weather?",
		Tools:     []ToolDefinition{{Name: "lookup"}},
		Executor:  executor,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Text != "It is sunny." {
		t.Fatalf("final text = %q, want the summary produced by the second inference pass", res.Text)
	}

	mu.Lock()
	defer mu.Unlock()
	if callCount != 2 {
		t.Fatalf("expected exactly 2 provider inference calls (tool call then re-inference), got %d", callCount)
	}
	// The second request must carry the tool result content, proving the tool
	// output was fed back for a follow-up inference pass rather than summarised
	// locally without re-entering inference.
	second := bodies[1]
	foundToolResult := false
	for _, m := range second.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "weather=sunny") {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatalf("second request did not carry the tool result; messages=%#v", second.Messages)
	}
	// The second request must also re-advertise the tool definitions.
	if len(second.Tools) == 0 || second.Tools[0].Name != "lookup" {
		t.Fatalf("second request missing tool definitions: %#v", second.Tools)
	}
	if got := executor.execCount.Load(); got != 1 {
		t.Fatalf("tool exec count = %d, want 1", got)
	}
}

func TestHTTPProvider_NoToolsSingleCall(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(httpResponse{Text: "direct answer"})
	}))
	defer srv.Close()

	provider := &HTTPProvider{URL: srv.URL, Client: srv.Client()}
	res, err := provider.Generate(context.Background(), Turn{SessionID: "s", UserText: "hi"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Text != "direct answer" {
		t.Fatalf("text = %q, want direct answer", res.Text)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call for a no-tool turn, got %d", callCount)
	}
}

func TestOpenAIChatProvider_Stream_LargeSSEChunk(t *testing.T) {
	longText := strings.Repeat("x", 120*1024)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", longText)
		_, _ = fmt.Fprintln(w, "data: [DONE]")
	}))
	defer srv.Close()

	p := &OpenAIChatProvider{BaseURL: srv.URL, Model: "gpt-4o", Client: srv.Client()}
	res, err := p.Stream(context.Background(), Turn{UserText: "hi"}, nil)
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if len(res.Text) != len(longText) {
		t.Fatalf("streamed text length=%d want=%d", len(res.Text), len(longText))
	}
}
