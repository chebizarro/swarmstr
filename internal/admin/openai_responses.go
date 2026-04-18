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

// ── Open Responses API types ────────────────────────────────────────────────
// See https://www.open-responses.com/

// responsesContentPart is a content part within a message item.
type responsesContentPart struct {
	Type string `json:"type"` // "input_text", "output_text", "input_image", "input_file"
	Text string `json:"text,omitempty"`
	// Source is used for input_image and input_file; ignored in Phase 1.
	Source json.RawMessage `json:"source,omitempty"`
}

// responsesItemParam is an input item in the request.
type responsesItemParam struct {
	Type string `json:"type"` // "message", "function_call", "function_call_output", "reasoning", "item_reference"
	// message fields
	Role    string          `json:"role,omitempty"` // "system", "developer", "user", "assistant"
	Content json.RawMessage `json:"content,omitempty"`
	// function_call fields
	ID        string `json:"id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	// function_call_output fields
	Output string `json:"output,omitempty"`
}

// responsesToolDefinition is a tool definition in the request.
type responsesToolDefinition struct {
	Type     string `json:"type"`
	Function *struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters,omitempty"`
	} `json:"function,omitempty"`
}

// responsesCreateBody is the request body for POST /v1/responses.
type responsesCreateBody struct {
	Model           string                    `json:"model"`
	Input           json.RawMessage           `json:"input"`
	Instructions    string                    `json:"instructions,omitempty"`
	Tools           []responsesToolDefinition `json:"tools,omitempty"`
	ToolChoice      json.RawMessage           `json:"tool_choice,omitempty"`
	Stream          bool                      `json:"stream,omitempty"`
	MaxOutputTokens int                       `json:"max_output_tokens,omitempty"`
	User            string                    `json:"user,omitempty"`
	// Accepted but ignored in Phase 1.
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	Metadata           map[string]any  `json:"metadata,omitempty"`
	Store              *bool           `json:"store,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	Reasoning          json.RawMessage `json:"reasoning,omitempty"`
	Truncation         string          `json:"truncation,omitempty"`
}

// responsesOutputTextPart is an output_text content part.
type responsesOutputTextPart struct {
	Type string `json:"type"` // always "output_text"
	Text string `json:"text"`
}

// responsesOutputItem represents an output item in a response.
type responsesOutputItem struct {
	Type    string                    `json:"type"`              // "message" or "function_call"
	ID      string                    `json:"id"`
	Role    string                    `json:"role,omitempty"`    // "assistant" for message items
	Content []responsesOutputTextPart `json:"content,omitempty"` // for message items
	Status  string                    `json:"status,omitempty"`  // "in_progress" | "completed"
	// function_call fields
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// responsesUsage is the token usage report.
type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// responsesResource is the full response object.
type responsesResource struct {
	ID        string                `json:"id"`
	Object    string                `json:"object"` // always "response"
	CreatedAt int64                 `json:"created_at"`
	Status    string                `json:"status"` // "in_progress", "completed", "failed", "cancelled", "incomplete"
	Model     string                `json:"model"`
	Output    []responsesOutputItem `json:"output"`
	Usage     responsesUsage        `json:"usage"`
	Error     *responsesErrorField  `json:"error,omitempty"`
}

type responsesErrorField struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ── SSE event types ─────────────────────────────────────────────────────────

type responsesSSEEvent struct {
	Type string `json:"type"`
	// Depending on event type, one or more of these fields is populated:
	Response     *responsesResource       `json:"response,omitempty"`
	OutputIndex  *int                     `json:"output_index,omitempty"`
	ContentIndex *int                     `json:"content_index,omitempty"`
	Item         *responsesOutputItem     `json:"item,omitempty"`
	ItemID       string                   `json:"item_id,omitempty"`
	Part         *responsesOutputTextPart `json:"part,omitempty"`
	Delta        string                   `json:"delta,omitempty"`
	Text         string                   `json:"text,omitempty"`
}

// ── Prompt building ─────────────────────────────────────────────────────────

// responsesExtractItemText extracts text from a content field that may be
// a string or an array of content parts.
func responsesExtractItemText(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Try array of content parts.
	var parts []responsesContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text":
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
	}
	return strings.Join(texts, "\n")
}

// responsesBuildPrompt builds an agent-compatible (message, context) pair
// from the Responses API input field.
func responsesBuildPrompt(input json.RawMessage) (message string, systemContext string) {
	input = json.RawMessage(strings.TrimSpace(string(input)))
	if len(input) == 0 {
		return "", ""
	}

	// If input is a plain string, use directly.
	var s string
	if err := json.Unmarshal(input, &s); err == nil {
		return strings.TrimSpace(s), ""
	}

	// Otherwise, parse as item array.
	var items []responsesItemParam
	if err := json.Unmarshal(input, &items); err != nil {
		return "", ""
	}

	var systemParts []string
	type entry struct {
		sender string
		body   string
	}
	var history []entry

	for _, item := range items {
		switch item.Type {
		case "message":
			content := strings.TrimSpace(responsesExtractItemText(item.Content))
			if content == "" {
				continue
			}
			role := strings.TrimSpace(item.Role)
			switch role {
			case "system", "developer":
				systemParts = append(systemParts, content)
			case "user":
				history = append(history, entry{sender: "User", body: content})
			case "assistant":
				history = append(history, entry{sender: "Assistant", body: content})
			}
		case "function_call_output":
			output := strings.TrimSpace(item.Output)
			if output == "" {
				continue
			}
			callID := strings.TrimSpace(item.CallID)
			sender := "Tool"
			if callID != "" {
				sender = "Tool:" + callID
			}
			history = append(history, entry{sender: sender, body: output})
		}
		// Skip reasoning, item_reference, function_call for prompt building.
	}

	systemContext = strings.Join(systemParts, "\n\n")

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

// ── Helpers ─────────────────────────────────────────────────────────────────

func intPtr(v int) *int { return &v }

func writeResponsesSSE(w http.ResponseWriter, evt responsesSSEEvent) {
	raw, err := json.Marshal(evt)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, raw)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func newResponsesResource(id, model, status string, output []responsesOutputItem, usage responsesUsage, respErr *responsesErrorField) responsesResource {
	return responsesResource{
		ID:        id,
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Status:    status,
		Model:     model,
		Output:    output,
		Usage:     usage,
		Error:     respErr,
	}
}

func newAssistantOutputItem(id, text, status string) responsesOutputItem {
	return responsesOutputItem{
		Type:    "message",
		ID:      id,
		Role:    "assistant",
		Content: []responsesOutputTextPart{{Type: "output_text", Text: text}},
		Status:  status,
	}
}

// ── Handler ─────────────────────────────────────────────────────────────────

const (
	responsesMaxBodyBytes = 4 << 20 // 4 MiB
	responsesDefaultModel = "metiq"
)

// handleOpenAIResponses returns a handler for POST /v1/responses.
func handleOpenAIResponses(opts ServerOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, openAIErrorResponse{
				Error: openAIErrorBody{Message: "method not allowed", Type: "invalid_request_error"},
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, responsesMaxBodyBytes)
		var req responsesCreateBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, openAIErrorResponse{
				Error: openAIErrorBody{Message: "invalid request body", Type: "invalid_request_error"},
			})
			return
		}

		model := strings.TrimSpace(req.Model)
		if model == "" {
			model = responsesDefaultModel
		}

		// Build prompt from input items.
		message, systemCtx := responsesBuildPrompt(req.Input)

		// Merge instructions into system context.
		instructions := strings.TrimSpace(req.Instructions)
		if instructions != "" {
			if systemCtx != "" {
				systemCtx = instructions + "\n\n" + systemCtx
			} else {
				systemCtx = instructions
			}
		}

		if message == "" {
			writeJSON(w, http.StatusBadRequest, openAIErrorResponse{
				Error: openAIErrorBody{Message: "missing user message in `input`", Type: "invalid_request_error"},
			})
			return
		}

		if opts.StartAgent == nil || opts.WaitAgent == nil {
			writeJSON(w, http.StatusNotImplemented, openAIErrorResponse{
				Error: openAIErrorBody{Message: "agent runtime not configured", Type: "api_error"},
			})
			return
		}

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

		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Now().Add(openAIWriteDeadlineExtend))

		startResult, err := opts.StartAgent(r.Context(), agentReq)
		if err != nil {
			log.Printf("openresponses: start agent failed: %v", err)
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

		responseID := "resp_" + runID
		outputItemID := "msg_" + runID

		if !req.Stream {
			handleResponsesNonStreaming(r.Context(), w, opts, runID, responseID, outputItemID, model)
			return
		}
		handleResponsesStreaming(r.Context(), w, opts, runID, responseID, outputItemID, model)
	}
}

// handleResponsesNonStreaming waits for the agent result and returns a
// ResponseResource JSON response.
func handleResponsesNonStreaming(ctx context.Context, w http.ResponseWriter, opts ServerOptions, runID, responseID, outputItemID, model string) {
	content, err := waitForAgentResult(ctx, opts, runID)
	if err != nil {
		log.Printf("openresponses: agent run failed: %v", err)
		resp := newResponsesResource(responseID, model, "failed", nil, responsesUsage{},
			&responsesErrorField{Code: "api_error", Message: "internal error"})
		writeJSON(w, http.StatusInternalServerError, resp)
		return
	}

	item := newAssistantOutputItem(outputItemID, content, "completed")
	resp := newResponsesResource(responseID, model, "completed",
		[]responsesOutputItem{item}, responsesUsage{}, nil)
	writeJSON(w, http.StatusOK, resp)
}

// handleResponsesStreaming emits SSE events in the Open Responses format.
func handleResponsesStreaming(ctx context.Context, w http.ResponseWriter, opts ServerOptions, runID, responseID, outputItemID, model string) {
	setSSEHeaders(w)

	// 1. response.created
	initialResp := newResponsesResource(responseID, model, "in_progress", nil, responsesUsage{}, nil)
	writeResponsesSSE(w, responsesSSEEvent{Type: "response.created", Response: &initialResp})

	// 2. response.in_progress
	writeResponsesSSE(w, responsesSSEEvent{Type: "response.in_progress", Response: &initialResp})

	// 3. output_item.added
	emptyItem := newAssistantOutputItem(outputItemID, "", "in_progress")
	writeResponsesSSE(w, responsesSSEEvent{
		Type: "response.output_item.added", OutputIndex: intPtr(0), Item: &emptyItem,
	})

	// 4. content_part.added
	emptyPart := responsesOutputTextPart{Type: "output_text", Text: ""}
	writeResponsesSSE(w, responsesSSEEvent{
		Type: "response.content_part.added", ItemID: outputItemID,
		OutputIndex: intPtr(0), ContentIndex: intPtr(0), Part: &emptyPart,
	})

	// Wait for agent result.
	content, err := waitForAgentResult(ctx, opts, runID)
	if err != nil {
		log.Printf("openresponses: streaming agent run failed: %v", err)
		failedResp := newResponsesResource(responseID, model, "failed", nil, responsesUsage{},
			&responsesErrorField{Code: "api_error", Message: "internal error"})
		writeResponsesSSE(w, responsesSSEEvent{Type: "response.failed", Response: &failedResp})
		writeSSEDone(w)
		return
	}

	// 5. output_text.delta
	writeResponsesSSE(w, responsesSSEEvent{
		Type: "response.output_text.delta", ItemID: outputItemID,
		OutputIndex: intPtr(0), ContentIndex: intPtr(0), Delta: content,
	})

	// 6. output_text.done
	writeResponsesSSE(w, responsesSSEEvent{
		Type: "response.output_text.done", ItemID: outputItemID,
		OutputIndex: intPtr(0), ContentIndex: intPtr(0), Text: content,
	})

	// 7. content_part.done
	donePart := responsesOutputTextPart{Type: "output_text", Text: content}
	writeResponsesSSE(w, responsesSSEEvent{
		Type: "response.content_part.done", ItemID: outputItemID,
		OutputIndex: intPtr(0), ContentIndex: intPtr(0), Part: &donePart,
	})

	// 8. output_item.done
	doneItem := newAssistantOutputItem(outputItemID, content, "completed")
	writeResponsesSSE(w, responsesSSEEvent{
		Type: "response.output_item.done", OutputIndex: intPtr(0), Item: &doneItem,
	})

	// 9. response.completed
	finalResp := newResponsesResource(responseID, model, "completed",
		[]responsesOutputItem{doneItem}, responsesUsage{}, nil)
	writeResponsesSSE(w, responsesSSEEvent{Type: "response.completed", Response: &finalResp})

	writeSSEDone(w)
}

// mountOpenAIResponses registers the /v1/responses endpoint.
func mountOpenAIResponses(mux *http.ServeMux, opts ServerOptions) {
	mux.HandleFunc("/v1/responses", withAuth(opts.Token, handleOpenAIResponses(opts)))
}
