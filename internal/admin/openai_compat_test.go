package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"metiq/internal/gateway/methods"
)

// ── buildPromptFromMessages ─────────────────────────────────────────────────

func TestBuildPromptFromMessages_SingleUser(t *testing.T) {
	msg, ctx := buildPromptFromMessages([]openAIChatMessage{
		{Role: "user", Content: "Hello"},
	})
	if msg != "Hello" {
		t.Fatalf("message = %q, want %q", msg, "Hello")
	}
	if ctx != "" {
		t.Fatalf("context = %q, want empty", ctx)
	}
}

func TestBuildPromptFromMessages_SystemAndUser(t *testing.T) {
	msg, ctx := buildPromptFromMessages([]openAIChatMessage{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "What is 2+2?"},
	})
	if msg != "What is 2+2?" {
		t.Fatalf("message = %q", msg)
	}
	if ctx != "You are a helpful assistant." {
		t.Fatalf("context = %q", ctx)
	}
}

func TestBuildPromptFromMessages_MultiTurn(t *testing.T) {
	msg, _ := buildPromptFromMessages([]openAIChatMessage{
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello!"},
		{Role: "user", Content: "How are you?"},
	})
	if !strings.Contains(msg, "User: Hi") {
		t.Fatalf("expected multi-turn format, got %q", msg)
	}
	if !strings.Contains(msg, "Assistant: Hello!") {
		t.Fatalf("expected assistant turn, got %q", msg)
	}
	if !strings.Contains(msg, "User: How are you?") {
		t.Fatalf("expected second user turn, got %q", msg)
	}
}

func TestBuildPromptFromMessages_MultipleSystemMessages(t *testing.T) {
	_, ctx := buildPromptFromMessages([]openAIChatMessage{
		{Role: "system", Content: "Rule 1"},
		{Role: "developer", Content: "Rule 2"},
		{Role: "user", Content: "Go"},
	})
	if ctx != "Rule 1\n\nRule 2" {
		t.Fatalf("context = %q", ctx)
	}
}

func TestBuildPromptFromMessages_ToolMessage(t *testing.T) {
	msg, _ := buildPromptFromMessages([]openAIChatMessage{
		{Role: "user", Content: "Use the tool"},
		{Role: "tool", Content: "tool result", Name: "search"},
		{Role: "user", Content: "Thanks"},
	})
	if !strings.Contains(msg, "Tool:search: tool result") {
		t.Fatalf("expected tool output, got %q", msg)
	}
}

func TestBuildPromptFromMessages_ContentParts(t *testing.T) {
	content := []any{
		map[string]any{"type": "text", "text": "part one"},
		map[string]any{"type": "text", "text": "part two"},
	}
	msg, _ := buildPromptFromMessages([]openAIChatMessage{
		{Role: "user", Content: content},
	})
	if msg != "part one\npart two" {
		t.Fatalf("message = %q", msg)
	}
}

func TestBuildPromptFromMessages_EmptyMessages(t *testing.T) {
	msg, ctx := buildPromptFromMessages(nil)
	if msg != "" || ctx != "" {
		t.Fatalf("expected empty, got msg=%q ctx=%q", msg, ctx)
	}
}

// ── extractTextContent ──────────────────────────────────────────────────────

func TestExtractTextContent_String(t *testing.T) {
	if got := extractTextContent("hello"); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractTextContent_Parts(t *testing.T) {
	parts := []any{
		map[string]any{"type": "text", "text": "a"},
		map[string]any{"type": "image_url", "image_url": "http://img"},
		map[string]any{"type": "input_text", "text": "b"},
	}
	got := extractTextContent(parts)
	if got != "a\nb" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractTextContent_Nil(t *testing.T) {
	if got := extractTextContent(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}

// ── openAISessionKey ────────────────────────────────────────────────────────

func TestOpenAISessionKey(t *testing.T) {
	if got := openAISessionKey("alice"); got != "openai-alice" {
		t.Fatalf("got %q", got)
	}
	if got := openAISessionKey(""); got != "" {
		t.Fatalf("got %q", got)
	}
}

// ── HTTP handler: non-streaming ─────────────────────────────────────────────

func newOpenAIChatRequest(t *testing.T, body openAIChatCompletionRequest) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw))
}

func TestHandleOpenAIChatCompletions_NonStreaming(t *testing.T) {
	opts := ServerOptions{
		StartAgent: func(_ context.Context, req methods.AgentRequest) (map[string]any, error) {
			if req.Message != "Hello" {
				t.Errorf("message = %q", req.Message)
			}
			return map[string]any{"run_id": "run-1", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, req methods.AgentWaitRequest) (map[string]any, error) {
			if req.RunID != "run-1" {
				t.Errorf("run_id = %q", req.RunID)
			}
			return map[string]any{"run_id": "run-1", "status": "completed", "result": "Hi there!"}, nil
		},
	}

	handler := handleOpenAIChatCompletions(opts)
	rr := httptest.NewRecorder()
	req := newOpenAIChatRequest(t, openAIChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []openAIChatMessage{{Role: "user", Content: "Hello"}},
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var resp openAIChatCompletionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "chat.completion" {
		t.Fatalf("object = %q", resp.Object)
	}
	if resp.Model != "gpt-test" {
		t.Fatalf("model = %q", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Fatalf("role = %q", resp.Choices[0].Message.Role)
	}
	if resp.Choices[0].Message.Content != "Hi there!" {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %v", resp.Choices[0].FinishReason)
	}
	if !strings.HasPrefix(resp.ID, "chatcmpl-") {
		t.Fatalf("id = %q", resp.ID)
	}
}

func TestHandleOpenAIChatCompletions_SystemContext(t *testing.T) {
	var capturedCtx string
	opts := ServerOptions{
		StartAgent: func(_ context.Context, req methods.AgentRequest) (map[string]any, error) {
			capturedCtx = req.Context
			return map[string]any{"run_id": "run-2", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-2", "status": "completed", "result": "ok"}, nil
		},
	}

	handler := handleOpenAIChatCompletions(opts)
	rr := httptest.NewRecorder()
	req := newOpenAIChatRequest(t, openAIChatCompletionRequest{
		Messages: []openAIChatMessage{
			{Role: "system", Content: "Be concise."},
			{Role: "user", Content: "Summarize Go."},
		},
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if capturedCtx != "Be concise." {
		t.Fatalf("agent context = %q", capturedCtx)
	}
}

func TestHandleOpenAIChatCompletions_UserSessionKey(t *testing.T) {
	var capturedSession string
	opts := ServerOptions{
		StartAgent: func(_ context.Context, req methods.AgentRequest) (map[string]any, error) {
			capturedSession = req.SessionID
			return map[string]any{"run_id": "run-3", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-3", "status": "completed", "result": "ok"}, nil
		},
	}

	handler := handleOpenAIChatCompletions(opts)
	rr := httptest.NewRecorder()
	req := newOpenAIChatRequest(t, openAIChatCompletionRequest{
		Messages: []openAIChatMessage{{Role: "user", Content: "hi"}},
		User:     "bob",
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if capturedSession != "openai-bob" {
		t.Fatalf("session = %q", capturedSession)
	}
}

// ── HTTP handler: streaming ─────────────────────────────────────────────────

func TestHandleOpenAIChatCompletions_Streaming(t *testing.T) {
	opts := ServerOptions{
		StartAgent: func(_ context.Context, req methods.AgentRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-s1", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-s1", "status": "completed", "result": "streamed response"}, nil
		},
	}

	handler := handleOpenAIChatCompletions(opts)
	rr := httptest.NewRecorder()
	req := newOpenAIChatRequest(t, openAIChatCompletionRequest{
		Model:    "gpt-stream",
		Stream:   true,
		Messages: []openAIChatMessage{{Role: "user", Content: "Hello stream"}},
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	body := rr.Body.String()

	// Should contain at least: role chunk, content chunk, finish chunk, [DONE]
	chunks := parseSSEChunks(t, body)
	if len(chunks) < 3 {
		t.Fatalf("expected >= 3 SSE chunks, got %d: %s", len(chunks), body)
	}

	// First chunk should have role delta.
	if role := chunks[0].Choices[0].Delta.Role; role != "assistant" {
		t.Fatalf("first chunk role = %q", role)
	}

	// Second chunk should have content.
	if content := chunks[1].Choices[0].Delta.Content; content != "streamed response" {
		t.Fatalf("content chunk = %q", content)
	}

	// Last chunk should have finish_reason.
	last := chunks[len(chunks)-1]
	if last.Choices[0].FinishReason == nil || *last.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason missing in last chunk")
	}

	// Should end with [DONE].
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("missing [DONE] sentinel")
	}
}

// ── HTTP handler: error cases ───────────────────────────────────────────────

func TestHandleOpenAIChatCompletions_MethodNotAllowed(t *testing.T) {
	handler := handleOpenAIChatCompletions(ServerOptions{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleOpenAIChatCompletions_BadBody(t *testing.T) {
	handler := handleOpenAIChatCompletions(ServerOptions{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader("not json"))

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleOpenAIChatCompletions_NoUserMessage(t *testing.T) {
	handler := handleOpenAIChatCompletions(ServerOptions{})
	rr := httptest.NewRecorder()
	req := newOpenAIChatRequest(t, openAIChatCompletionRequest{
		Messages: []openAIChatMessage{{Role: "system", Content: "just system"}},
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleOpenAIChatCompletions_NotImplemented(t *testing.T) {
	handler := handleOpenAIChatCompletions(ServerOptions{})
	rr := httptest.NewRecorder()
	req := newOpenAIChatRequest(t, openAIChatCompletionRequest{
		Messages: []openAIChatMessage{{Role: "user", Content: "hi"}},
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleOpenAIChatCompletions_AgentError(t *testing.T) {
	opts := ServerOptions{
		StartAgent: func(_ context.Context, _ methods.AgentRequest) (map[string]any, error) {
			return nil, fmt.Errorf("boom")
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return nil, nil
		},
	}

	handler := handleOpenAIChatCompletions(opts)
	rr := httptest.NewRecorder()
	req := newOpenAIChatRequest(t, openAIChatCompletionRequest{
		Messages: []openAIChatMessage{{Role: "user", Content: "hi"}},
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleOpenAIChatCompletions_WaitTimeout(t *testing.T) {
	opts := ServerOptions{
		StartAgent: func(_ context.Context, _ methods.AgentRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-t1", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-t1", "status": "timeout"}, nil
		},
	}

	handler := handleOpenAIChatCompletions(opts)
	rr := httptest.NewRecorder()
	req := newOpenAIChatRequest(t, openAIChatCompletionRequest{
		Messages: []openAIChatMessage{{Role: "user", Content: "hi"}},
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleOpenAIChatCompletions_DefaultModel(t *testing.T) {
	opts := ServerOptions{
		StartAgent: func(_ context.Context, _ methods.AgentRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-m1", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-m1", "status": "completed", "result": "ok"}, nil
		},
	}

	handler := handleOpenAIChatCompletions(opts)
	rr := httptest.NewRecorder()
	req := newOpenAIChatRequest(t, openAIChatCompletionRequest{
		Messages: []openAIChatMessage{{Role: "user", Content: "hi"}},
	})

	handler.ServeHTTP(rr, req)

	var resp openAIChatCompletionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Model != "metiq" {
		t.Fatalf("model = %q, want %q", resp.Model, "metiq")
	}
}

// ── SSE parsing helper ──────────────────────────────────────────────────────

func parseSSEChunks(t *testing.T, body string) []openAIChatCompletionChunk {
	t.Helper()
	var chunks []openAIChatCompletionChunk
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}
		var chunk openAIChatCompletionChunk
		if err := json.NewDecoder(io.NopCloser(strings.NewReader(data))).Decode(&chunk); err != nil {
			t.Fatalf("decode SSE chunk: %v\nraw: %s", err, data)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

// ── Mount integration ───────────────────────────────────────────────────────

func TestMountOpenAIChatCompletions(t *testing.T) {
	mux := http.NewServeMux()
	opts := ServerOptions{
		StartAgent: func(_ context.Context, _ methods.AgentRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-mount", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-mount", "status": "completed", "result": "mounted"}, nil
		},
	}
	mountOpenAIChatCompletions(mux, opts)

	raw, _ := json.Marshal(openAIChatCompletionRequest{
		Messages: []openAIChatMessage{{Role: "user", Content: "test mount"}},
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp openAIChatCompletionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Choices[0].Message.Content != "mounted" {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
}

func TestMountOpenAIChatCompletions_WithAuth(t *testing.T) {
	mux := http.NewServeMux()
	opts := ServerOptions{
		Token: "secret-token",
		StartAgent: func(_ context.Context, _ methods.AgentRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-auth", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-auth", "status": "completed", "result": "authed"}, nil
		},
	}
	mountOpenAIChatCompletions(mux, opts)

	raw, _ := json.Marshal(openAIChatCompletionRequest{
		Messages: []openAIChatMessage{{Role: "user", Content: "auth test"}},
	})

	// Without auth — should fail.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw))
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want 401", rr.Code)
	}

	// With auth — should succeed.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer secret-token")
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("authed status = %d, body = %s", rr.Code, rr.Body.String())
	}
}
