package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"metiq/internal/gateway/methods"
)

// ── Prompt building ─────────────────────────────────────────────────────────

func TestResponsesBuildPrompt_StringInput(t *testing.T) {
	msg, ctx := responsesBuildPrompt(json.RawMessage(`"Hello world"`))
	if msg != "Hello world" {
		t.Fatalf("message = %q", msg)
	}
	if ctx != "" {
		t.Fatalf("context = %q", ctx)
	}
}

func TestResponsesBuildPrompt_SingleUserMessage(t *testing.T) {
	input := `[{"type":"message","role":"user","content":"What is Go?"}]`
	msg, ctx := responsesBuildPrompt(json.RawMessage(input))
	if msg != "What is Go?" {
		t.Fatalf("message = %q", msg)
	}
	if ctx != "" {
		t.Fatalf("context = %q", ctx)
	}
}

func TestResponsesBuildPrompt_SystemAndUser(t *testing.T) {
	input := `[
		{"type":"message","role":"system","content":"Be brief."},
		{"type":"message","role":"user","content":"Summarize Go."}
	]`
	msg, ctx := responsesBuildPrompt(json.RawMessage(input))
	if msg != "Summarize Go." {
		t.Fatalf("message = %q", msg)
	}
	if ctx != "Be brief." {
		t.Fatalf("context = %q", ctx)
	}
}

func TestResponsesBuildPrompt_MultiTurn(t *testing.T) {
	input := `[
		{"type":"message","role":"user","content":"Hi"},
		{"type":"message","role":"assistant","content":"Hello!"},
		{"type":"message","role":"user","content":"How are you?"}
	]`
	msg, _ := responsesBuildPrompt(json.RawMessage(input))
	if !strings.Contains(msg, "User: Hi") || !strings.Contains(msg, "Assistant: Hello!") {
		t.Fatalf("message = %q", msg)
	}
}

func TestResponsesBuildPrompt_ContentParts(t *testing.T) {
	input := `[{"type":"message","role":"user","content":[
		{"type":"input_text","text":"part one"},
		{"type":"input_text","text":"part two"}
	]}]`
	msg, _ := responsesBuildPrompt(json.RawMessage(input))
	if msg != "part one\npart two" {
		t.Fatalf("message = %q", msg)
	}
}

func TestResponsesBuildPrompt_FunctionCallOutput(t *testing.T) {
	input := `[
		{"type":"message","role":"user","content":"Use the tool"},
		{"type":"function_call_output","call_id":"call-1","output":"42"},
		{"type":"message","role":"user","content":"What was the result?"}
	]`
	msg, _ := responsesBuildPrompt(json.RawMessage(input))
	if !strings.Contains(msg, "Tool:call-1: 42") {
		t.Fatalf("message = %q", msg)
	}
}

func TestResponsesBuildPrompt_EmptyInput(t *testing.T) {
	msg, ctx := responsesBuildPrompt(json.RawMessage(`[]`))
	if msg != "" || ctx != "" {
		t.Fatalf("expected empty, got msg=%q ctx=%q", msg, ctx)
	}
}

func TestResponsesBuildPrompt_InstructionsAndDeveloper(t *testing.T) {
	input := `[
		{"type":"message","role":"developer","content":"Dev context"},
		{"type":"message","role":"user","content":"Go"}
	]`
	msg, ctx := responsesBuildPrompt(json.RawMessage(input))
	if msg != "Go" {
		t.Fatalf("message = %q", msg)
	}
	if ctx != "Dev context" {
		t.Fatalf("context = %q", ctx)
	}
}

// ── extractItemText ─────────────────────────────────────────────────────────

func TestResponsesExtractItemText_String(t *testing.T) {
	if got := responsesExtractItemText(json.RawMessage(`"hello"`)); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestResponsesExtractItemText_Parts(t *testing.T) {
	raw := `[{"type":"input_text","text":"a"},{"type":"output_text","text":"b"},{"type":"input_image"}]`
	if got := responsesExtractItemText(json.RawMessage(raw)); got != "a\nb" {
		t.Fatalf("got %q", got)
	}
}

func TestResponsesExtractItemText_Nil(t *testing.T) {
	if got := responsesExtractItemText(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}

// ── HTTP handler: non-streaming ─────────────────────────────────────────────

func newResponsesRequest(t *testing.T, body responsesCreateBody) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
}

func TestHandleOpenAIResponses_NonStreaming(t *testing.T) {
	opts := ServerOptions{
		StartAgent: func(_ context.Context, req methods.AgentRequest) (map[string]any, error) {
			if req.Message != "Hello" {
				t.Errorf("message = %q", req.Message)
			}
			return map[string]any{"run_id": "run-r1", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, req methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-r1", "status": "completed", "result": "Hi there!"}, nil
		},
	}

	handler := handleOpenAIResponses(opts)
	rr := httptest.NewRecorder()
	req := newResponsesRequest(t, responsesCreateBody{
		Model: "test-model",
		Input: json.RawMessage(`[{"type":"message","role":"user","content":"Hello"}]`),
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var resp responsesResource
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "response" {
		t.Fatalf("object = %q", resp.Object)
	}
	if resp.Status != "completed" {
		t.Fatalf("status = %q", resp.Status)
	}
	if resp.Model != "test-model" {
		t.Fatalf("model = %q", resp.Model)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("output count = %d", len(resp.Output))
	}
	item := resp.Output[0]
	if item.Type != "message" || item.Role != "assistant" {
		t.Fatalf("item type=%q role=%q", item.Type, item.Role)
	}
	if len(item.Content) != 1 || item.Content[0].Text != "Hi there!" {
		t.Fatalf("content = %+v", item.Content)
	}
	if item.Status != "completed" {
		t.Fatalf("item status = %q", item.Status)
	}
	if !strings.HasPrefix(resp.ID, "resp_") {
		t.Fatalf("id = %q", resp.ID)
	}
}

func TestHandleOpenAIResponses_StringInput(t *testing.T) {
	opts := ServerOptions{
		StartAgent: func(_ context.Context, req methods.AgentRequest) (map[string]any, error) {
			if req.Message != "Direct string" {
				t.Errorf("message = %q", req.Message)
			}
			return map[string]any{"run_id": "run-r2", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-r2", "status": "completed", "result": "ok"}, nil
		},
	}

	handler := handleOpenAIResponses(opts)
	rr := httptest.NewRecorder()
	req := newResponsesRequest(t, responsesCreateBody{
		Model: "test",
		Input: json.RawMessage(`"Direct string"`),
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleOpenAIResponses_InstructionsMerged(t *testing.T) {
	var capturedCtx string
	opts := ServerOptions{
		StartAgent: func(_ context.Context, req methods.AgentRequest) (map[string]any, error) {
			capturedCtx = req.Context
			return map[string]any{"run_id": "run-r3", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-r3", "status": "completed", "result": "ok"}, nil
		},
	}

	handler := handleOpenAIResponses(opts)
	rr := httptest.NewRecorder()
	req := newResponsesRequest(t, responsesCreateBody{
		Model:        "test",
		Input:        json.RawMessage(`[{"type":"message","role":"system","content":"System msg"},{"type":"message","role":"user","content":"Go"}]`),
		Instructions: "Be concise.",
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	// Instructions should come first, then system message.
	if !strings.HasPrefix(capturedCtx, "Be concise.") || !strings.Contains(capturedCtx, "System msg") {
		t.Fatalf("context = %q", capturedCtx)
	}
}

// ── HTTP handler: streaming ─────────────────────────────────────────────────

func TestHandleOpenAIResponses_Streaming(t *testing.T) {
	opts := ServerOptions{
		StartAgent: func(_ context.Context, _ methods.AgentRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-rs1", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-rs1", "status": "completed", "result": "streamed!"}, nil
		},
	}

	handler := handleOpenAIResponses(opts)
	rr := httptest.NewRecorder()
	req := newResponsesRequest(t, responsesCreateBody{
		Model:  "test-stream",
		Stream: true,
		Input:  json.RawMessage(`[{"type":"message","role":"user","content":"Hello stream"}]`),
	})

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	body := rr.Body.String()

	// Check for required event types.
	requiredEvents := []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	}
	for _, evt := range requiredEvents {
		if !strings.Contains(body, "event: "+evt) {
			t.Errorf("missing event: %s", evt)
		}
	}

	// Check for [DONE] sentinel.
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("missing [DONE]")
	}

	// Parse the delta event to verify content.
	events := parseResponsesSSEEvents(t, body)
	for _, evt := range events {
		if evt.Type == "response.output_text.delta" && evt.Delta == "streamed!" {
			return // found the content
		}
	}
	t.Fatalf("did not find delta with expected content in:\n%s", body)
}

// ── HTTP handler: error cases ───────────────────────────────────────────────

func TestHandleOpenAIResponses_MethodNotAllowed(t *testing.T) {
	handler := handleOpenAIResponses(ServerOptions{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleOpenAIResponses_BadBody(t *testing.T) {
	handler := handleOpenAIResponses(ServerOptions{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("not json"))
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleOpenAIResponses_NoUserMessage(t *testing.T) {
	handler := handleOpenAIResponses(ServerOptions{})
	rr := httptest.NewRecorder()
	req := newResponsesRequest(t, responsesCreateBody{
		Model: "test",
		Input: json.RawMessage(`[{"type":"message","role":"system","content":"only system"}]`),
	})
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleOpenAIResponses_NotImplemented(t *testing.T) {
	handler := handleOpenAIResponses(ServerOptions{})
	rr := httptest.NewRecorder()
	req := newResponsesRequest(t, responsesCreateBody{
		Model: "test",
		Input: json.RawMessage(`"hello"`),
	})
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleOpenAIResponses_AgentError(t *testing.T) {
	opts := ServerOptions{
		StartAgent: func(_ context.Context, _ methods.AgentRequest) (map[string]any, error) {
			return nil, fmt.Errorf("boom")
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return nil, nil
		},
	}
	handler := handleOpenAIResponses(opts)
	rr := httptest.NewRecorder()
	req := newResponsesRequest(t, responsesCreateBody{
		Model: "test",
		Input: json.RawMessage(`"hello"`),
	})
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleOpenAIResponses_StreamingError(t *testing.T) {
	opts := ServerOptions{
		StartAgent: func(_ context.Context, _ methods.AgentRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-err", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return nil, fmt.Errorf("timeout")
		},
	}
	handler := handleOpenAIResponses(opts)
	rr := httptest.NewRecorder()
	req := newResponsesRequest(t, responsesCreateBody{
		Model:  "test",
		Stream: true,
		Input:  json.RawMessage(`"hello"`),
	})
	handler.ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "response.failed") {
		t.Fatalf("expected response.failed event in:\n%s", body)
	}
}

// ── Mount integration ───────────────────────────────────────────────────────

func TestMountOpenAIResponses(t *testing.T) {
	mux := http.NewServeMux()
	opts := ServerOptions{
		StartAgent: func(_ context.Context, _ methods.AgentRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-mount-r", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) {
			return map[string]any{"run_id": "run-mount-r", "status": "completed", "result": "mounted"}, nil
		},
	}
	mountOpenAIResponses(mux, opts)

	raw, _ := json.Marshal(responsesCreateBody{
		Model: "test",
		Input: json.RawMessage(`"test mount"`),
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp responsesResource
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Output[0].Content[0].Text != "mounted" {
		t.Fatalf("content = %q", resp.Output[0].Content[0].Text)
	}
}

// ── SSE event parsing helper ────────────────────────────────────────────────

func parseResponsesSSEEvents(t *testing.T, body string) []responsesSSEEvent {
	t.Helper()
	var events []responsesSSEEvent
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "event: ") {
			continue
		}
		// Next line should be data.
		if i+1 < len(lines) {
			dataLine := strings.TrimSpace(lines[i+1])
			if strings.HasPrefix(dataLine, "data: ") {
				data := strings.TrimPrefix(dataLine, "data: ")
				if data == "[DONE]" {
					continue
				}
				var evt responsesSSEEvent
				if err := json.Unmarshal([]byte(data), &evt); err != nil {
					t.Logf("skipping unparseable event: %s", data)
					continue
				}
				events = append(events, evt)
			}
		}
	}
	return events
}
