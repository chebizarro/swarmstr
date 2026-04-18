package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
)

// ── OpenAI-compatible types ─────────────────────────────────────────────────

// openAIChatMessage represents a single message in the OpenAI chat format.
type openAIChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []contentPart
	Name    string `json:"name,omitempty"`
}

// openAIChatCompletionRequest is the incoming request body for
// POST /v1/chat/completions.
type openAIChatCompletionRequest struct {
	Model     string              `json:"model,omitempty"`
	Stream    bool                `json:"stream,omitempty"`
	Messages  []openAIChatMessage `json:"messages"`
	User      string              `json:"user,omitempty"`
	MaxTokens int                 `json:"max_tokens,omitempty"`
}

// openAIChatCompletionResponse is the non-streaming response envelope.
type openAIChatCompletionResponse struct {
	ID      string                      `json:"id"`
	Object  string                      `json:"object"`
	Created int64                       `json:"created"`
	Model   string                      `json:"model"`
	Choices []openAIChatCompletionChoice `json:"choices"`
	Usage   openAIChatCompletionUsage   `json:"usage"`
}

type openAIChatCompletionChoice struct {
	Index        int                        `json:"index"`
	Message      openAIChatCompletionMsg    `json:"message,omitempty"`
	Delta        *openAIChatCompletionDelta `json:"delta,omitempty"`
	FinishReason *string                    `json:"finish_reason"`
}

type openAIChatCompletionMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatCompletionDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openAIChatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// openAIChatCompletionChunk is a single SSE chunk in streaming mode.
type openAIChatCompletionChunk struct {
	ID      string                      `json:"id"`
	Object  string                      `json:"object"`
	Created int64                       `json:"created"`
	Model   string                      `json:"model"`
	Choices []openAIChatCompletionChoice `json:"choices"`
}

type openAIErrorResponse struct {
	Error openAIErrorBody `json:"error"`
}

type openAIErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// ── Prompt building ─────────────────────────────────────────────────────────

// extractTextContent extracts text from a message content field that may be
// a plain string or an array of content parts (OpenAI multi-modal format).
func extractTextContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, part := range v {
			m, ok := part.(map[string]any)
			if !ok {
				continue
			}
			partType, _ := m["type"].(string)
			switch partType {
			case "text":
				if text, ok := m["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			case "input_text":
				if text, ok := m["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
				if text, ok := m["input_text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// buildPromptFromMessages converts an array of OpenAI chat messages into
// an AgentRequest-compatible (message, context) pair.
// System/developer messages become context; the conversation history becomes
// the message.  The last user message is the primary input, with prior
// turns formatted as context.
func buildPromptFromMessages(messages []openAIChatMessage) (message string, systemContext string) {
	var systemParts []string
	type entry struct {
		sender string
		body   string
	}
	var history []entry

	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		content := strings.TrimSpace(extractTextContent(msg.Content))
		if role == "" {
			continue
		}

		switch role {
		case "system", "developer":
			if content != "" {
				systemParts = append(systemParts, content)
			}
		case "user":
			if content != "" {
				history = append(history, entry{sender: "User", body: content})
			}
		case "assistant":
			if content != "" {
				history = append(history, entry{sender: "Assistant", body: content})
			}
		case "tool", "function":
			name := strings.TrimSpace(msg.Name)
			sender := "Tool"
			if name != "" {
				sender = "Tool:" + name
			}
			if content != "" {
				history = append(history, entry{sender: sender, body: content})
			}
		}
	}

	systemContext = strings.Join(systemParts, "\n\n")

	// Build the conversation text.  If there is only a single user message
	// (the common case), pass it directly as the message for cleaner prompt
	// injection into the agent runtime.  With multiple turns, format as a
	// labelled conversation so the agent sees the full context.
	if len(history) == 0 {
		return "", systemContext
	}
	if len(history) == 1 && history[0].sender == "User" {
		return history[0].body, systemContext
	}

	var sb strings.Builder
	for i, e := range history {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(e.sender)
		sb.WriteString(": ")
		sb.WriteString(e.body)
	}
	return sb.String(), systemContext
}

// ── Session key derivation ──────────────────────────────────────────────────

func openAISessionKey(user string) string {
	user = strings.TrimSpace(user)
	if user != "" {
		return "openai-" + user
	}
	return ""
}

// ── SSE helpers ─────────────────────────────────────────────────────────────

func writeSSE(w http.ResponseWriter, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", raw)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeSSEDone(w http.ResponseWriter) {
	fmt.Fprint(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// ── Handler ─────────────────────────────────────────────────────────────────

const (
	openAIMaxBodyBytes         = 2 << 20 // 2 MiB
	openAIDefaultModel         = "metiq"
	openAIAgentTimeoutMS       = 120_000    // 2 minutes
	openAIWriteDeadlineExtend  = 3 * time.Minute
)

func finishReasonStop() *string {
	s := "stop"
	return &s
}

// handleOpenAIChatCompletions returns a handler for POST /v1/chat/completions
// that translates OpenAI-format requests into AgentRequest calls and returns
// responses in the OpenAI chat completion format.
func handleOpenAIChatCompletions(opts ServerOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, openAIErrorResponse{
				Error: openAIErrorBody{Message: "method not allowed", Type: "invalid_request_error"},
			})
			return
		}

		// Parse request body.
		r.Body = http.MaxBytesReader(w, r.Body, openAIMaxBodyBytes)
		var req openAIChatCompletionRequest
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, openAIErrorResponse{
				Error: openAIErrorBody{Message: "invalid request body", Type: "invalid_request_error"},
			})
			return
		}

		model := strings.TrimSpace(req.Model)
		if model == "" {
			model = openAIDefaultModel
		}

		message, systemCtx := buildPromptFromMessages(req.Messages)
		if message == "" {
			writeJSON(w, http.StatusBadRequest, openAIErrorResponse{
				Error: openAIErrorBody{Message: "missing user message in `messages`", Type: "invalid_request_error"},
			})
			return
		}

		if opts.StartAgent == nil || opts.WaitAgent == nil {
			writeJSON(w, http.StatusNotImplemented, openAIErrorResponse{
				Error: openAIErrorBody{Message: "agent runtime not configured", Type: "api_error"},
			})
			return
		}

		// Build agent request.
		agentReq := methods.AgentRequest{
			SessionID: openAISessionKey(req.User),
			Message:   message,
			Context:   systemCtx,
			TimeoutMS: openAIAgentTimeoutMS,
		}
		agentReq, err := agentReq.Normalize()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, openAIErrorResponse{
				Error: openAIErrorBody{Message: err.Error(), Type: "invalid_request_error"},
			})
			return
		}

		// Extend write deadline so the agent has time to complete.
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Now().Add(openAIWriteDeadlineExtend))

		// Start the agent run.
		startResult, err := opts.StartAgent(r.Context(), agentReq)
		if err != nil {
			log.Printf("openai-compat: start agent failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, openAIErrorResponse{
				Error: openAIErrorBody{Message: "internal error", Type: "api_error"},
			})
			return
		}

		runID, _ := startResult["run_id"].(string)
		if runID == "" {
			writeJSON(w, http.StatusInternalServerError, openAIErrorResponse{
				Error: openAIErrorBody{Message: "agent did not return run_id", Type: "api_error"},
			})
			return
		}

		chatCmplID := "chatcmpl-" + runID
		now := time.Now().Unix()

		if !req.Stream {
			handleOpenAINonStreaming(r.Context(), w, opts, runID, chatCmplID, model, now)
			return
		}
		handleOpenAIStreaming(r.Context(), w, opts, runID, chatCmplID, model, now)
	}
}

// handleOpenAINonStreaming waits for the agent result and returns a single
// JSON response in the OpenAI chat.completion format.
func handleOpenAINonStreaming(ctx context.Context, w http.ResponseWriter, opts ServerOptions, runID, chatCmplID, model string, created int64) {
	content, err := waitForAgentResult(ctx, opts, runID)
	if err != nil {
		log.Printf("openai-compat: agent run failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, openAIErrorResponse{
			Error: openAIErrorBody{Message: "internal error", Type: "api_error"},
		})
		return
	}

	writeJSON(w, http.StatusOK, openAIChatCompletionResponse{
		ID:      chatCmplID,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []openAIChatCompletionChoice{
			{
				Index:        0,
				Message:      openAIChatCompletionMsg{Role: "assistant", Content: content},
				FinishReason: finishReasonStop(),
			},
		},
		Usage: openAIChatCompletionUsage{},
	})
}

// handleOpenAIStreaming waits for the agent result and streams it back as
// SSE chunks in the OpenAI chat.completion.chunk format.
//
// Note: this is "buffered streaming" — the full result is collected first,
// then emitted as a role chunk, content chunk, and [DONE] sentinel.  True
// token-by-token streaming requires an agent event bus (future work).
func handleOpenAIStreaming(ctx context.Context, w http.ResponseWriter, opts ServerOptions, runID, chatCmplID, model string, created int64) {
	setSSEHeaders(w)

	content, err := waitForAgentResult(ctx, opts, runID)
	if err != nil {
		log.Printf("openai-compat: streaming agent run failed: %v", err)
		writeSSE(w, openAIChatCompletionChunk{
			ID: chatCmplID, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []openAIChatCompletionChoice{{
				Index:        0,
				Delta:        &openAIChatCompletionDelta{Content: "Error: internal error"},
				FinishReason: finishReasonStop(),
			}},
		})
		writeSSEDone(w)
		return
	}

	// Role chunk.
	writeSSE(w, openAIChatCompletionChunk{
		ID: chatCmplID, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []openAIChatCompletionChoice{{
			Index: 0,
			Delta: &openAIChatCompletionDelta{Role: "assistant"},
		}},
	})

	// Content chunk.
	writeSSE(w, openAIChatCompletionChunk{
		ID: chatCmplID, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []openAIChatCompletionChoice{{
			Index: 0,
			Delta: &openAIChatCompletionDelta{Content: content},
		}},
	})

	// Final chunk with finish_reason.
	writeSSE(w, openAIChatCompletionChunk{
		ID: chatCmplID, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []openAIChatCompletionChoice{{
			Index:        0,
			Delta:        &openAIChatCompletionDelta{},
			FinishReason: finishReasonStop(),
		}},
	})

	writeSSEDone(w)
}

// waitForAgentResult calls WaitAgent and extracts the response text from the
// agent result map.
func waitForAgentResult(ctx context.Context, opts ServerOptions, runID string) (string, error) {
	waitReq := methods.AgentWaitRequest{
		RunID:     runID,
		TimeoutMS: openAIAgentTimeoutMS,
	}
	waitReq, err := waitReq.Normalize()
	if err != nil {
		return "", err
	}

	result, err := opts.WaitAgent(ctx, waitReq)
	if err != nil {
		return "", err
	}

	status, _ := result["status"].(string)
	if status == "timeout" {
		return "", fmt.Errorf("agent run timed out")
	}
	if status != "" && status != "completed" && status != "ok" {
		if errMsg, _ := result["error"].(string); errMsg != "" {
			return "", fmt.Errorf("agent run failed: %s", errMsg)
		}
		return "", fmt.Errorf("agent run status: %s", status)
	}

	// The result text may come through the "result" key.
	if text, ok := result["result"].(string); ok && text != "" {
		return text, nil
	}

	return "No response.", nil
}

// mountOpenAIChatCompletions registers the /v1/chat/completions endpoint.
func mountOpenAIChatCompletions(mux *http.ServeMux, opts ServerOptions) {
	mux.HandleFunc("/v1/chat/completions", withAuth(opts.Token, handleOpenAIChatCompletions(opts)))
}
