package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const maxProviderSSEEventBytes = 8 << 20

// decodeProviderSSE decodes bounded SSE frames, including CRLF, comments, and
// multi-line data fields. Completion is driven by EOF/[DONE], never a timer.
func decodeProviderSSE(r io.Reader, handle func([]byte) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxProviderSSEEventBytes+64*1024)
	var data strings.Builder
	dispatch := func() error {
		if data.Len() == 0 {
			return nil
		}
		raw := data.String()
		data.Reset()
		if raw == "[DONE]" {
			return nil
		}
		return handle([]byte(raw))
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		value := strings.TrimPrefix(line, "data:")
		value = strings.TrimPrefix(value, " ")
		if data.Len() > 0 {
			data.WriteByte('\n')
		}
		if data.Len()+len(value) > maxProviderSSEEventBytes {
			return fmt.Errorf("provider SSE event exceeds %d bytes", maxProviderSSEEventBytes)
		}
		data.WriteString(value)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("provider SSE read: %w", err)
	}
	return dispatch()
}

var errResponsesStreamComplete = errors.New("responses stream complete")

type responsesStreamItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responsesUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	InputTokenDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

type responsesStreamEnvelope struct {
	Type        string              `json:"type"`
	Delta       string              `json:"delta"`
	OutputIndex int                 `json:"output_index"`
	ItemID      string              `json:"item_id"`
	Item        responsesStreamItem `json:"item"`
	Error       *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Response struct {
		ID    string         `json:"id"`
		Usage responsesUsage `json:"usage"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
}

func providerUsageFromResponses(usage responsesUsage) ProviderUsage {
	return ProviderUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheReadTokens: usage.InputTokenDetails.CachedTokens}
}

type responsesToolState struct {
	index     int
	id        string
	name      string
	arguments strings.Builder
}

func consumeResponsesStream(r io.Reader, emit ProviderStreamEventSink) (ProviderResult, error) {
	var text strings.Builder
	var usage ProviderUsage
	var responseID string
	tools := map[int]*responsesToolState{}
	completed := false
	stateFor := func(index int) *responsesToolState {
		state := tools[index]
		if state == nil {
			state = &responsesToolState{index: index}
			tools[index] = state
		}
		return state
	}
	err := decodeProviderSSE(r, func(raw []byte) error {
		var envelope responsesStreamEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("responses stream decode: %w", err)
		}
		switch envelope.Type {
		case "error":
			if envelope.Error != nil {
				return fmt.Errorf("responses websocket error %s: %s", envelope.Error.Code, envelope.Error.Message)
			}
			return errors.New("responses websocket error")
		case "response.output_text.delta":
			text.WriteString(envelope.Delta)
			if emit != nil && envelope.Delta != "" {
				emit(ProviderStreamEvent{Type: ProviderStreamTextDelta, TextDelta: envelope.Delta})
			}
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			if emit != nil && envelope.Delta != "" {
				emit(ProviderStreamEvent{Type: ProviderStreamThinkingDelta, ThinkingDelta: envelope.Delta})
			}
		case "response.output_item.added":
			if envelope.Item.Type == "function_call" {
				state := stateFor(envelope.OutputIndex)
				state.id = firstNonEmptyString(envelope.Item.CallID, envelope.Item.ID)
				state.name = envelope.Item.Name
				if emit != nil && (state.id != "" || state.name != "") {
					emit(ProviderStreamEvent{Type: ProviderStreamToolCallDelta, ToolCallDelta: ProviderToolCallDelta{Index: state.index, ID: state.id, Name: state.name}})
				}
			}
		case "response.function_call_arguments.delta":
			state := stateFor(envelope.OutputIndex)
			state.arguments.WriteString(envelope.Delta)
			if emit != nil {
				emit(ProviderStreamEvent{Type: ProviderStreamToolCallDelta, ToolCallDelta: ProviderToolCallDelta{Index: state.index, ID: state.id, Name: state.name, ArgumentsDelta: envelope.Delta}})
			}
		case "response.output_item.done":
			if envelope.Item.Type == "function_call" {
				state := stateFor(envelope.OutputIndex)
				state.id = firstNonEmptyString(envelope.Item.CallID, envelope.Item.ID, state.id)
				if envelope.Item.Name != "" {
					state.name = envelope.Item.Name
				}
				if state.arguments.Len() == 0 && envelope.Item.Arguments != "" {
					state.arguments.WriteString(envelope.Item.Arguments)
				}
			}
		case "response.completed":
			completed = true
			responseID = strings.TrimSpace(envelope.Response.ID)
			next := providerUsageFromResponses(envelope.Response.Usage)
			if hasProviderUsage(next) && !providerUsageEqual(next, usage) {
				usage = next
				if emit != nil {
					emit(ProviderStreamEvent{Type: ProviderStreamUsage, Usage: usage})
				}
			}
			return errResponsesStreamComplete
		case "response.failed":
			if envelope.Response.Error != nil {
				return fmt.Errorf("responses stream failed %s: %s", envelope.Response.Error.Code, envelope.Response.Error.Message)
			}
			return errors.New("responses stream failed")
		case "response.incomplete":
			reason := "unknown"
			if envelope.Response.IncompleteDetails != nil && envelope.Response.IncompleteDetails.Reason != "" {
				reason = envelope.Response.IncompleteDetails.Reason
			}
			return fmt.Errorf("responses stream incomplete: %s", reason)
		}
		return nil
	})
	if err != nil && !errors.Is(err, errResponsesStreamComplete) {
		return ProviderResult{Text: text.String(), Usage: usage, responseID: responseID}, err
	}
	if !completed {
		return ProviderResult{Text: text.String(), Usage: usage, responseID: responseID}, errors.New("responses stream ended before response.completed")
	}
	indices := make([]int, 0, len(tools))
	for index := range tools {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	toolCalls := make([]ToolCall, 0, len(indices))
	for _, index := range indices {
		state := tools[index]
		args := map[string]any{}
		if raw := strings.TrimSpace(state.arguments.String()); raw != "" {
			if err := json.Unmarshal([]byte(raw), &args); err != nil {
				return ProviderResult{Text: text.String(), Usage: usage, responseID: responseID}, fmt.Errorf("responses tool %q arguments: %w", state.name, err)
			}
		}
		toolCalls = append(toolCalls, ToolCall{ID: state.id, Name: state.name, Args: args})
	}
	return ProviderResult{Text: text.String(), ToolCalls: toolCalls, Usage: usage, responseID: responseID}, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func consumeGeminiLikeStream(r io.Reader, emit ProviderStreamEventSink) (ProviderResult, error) {
	var text strings.Builder
	var toolCalls []ToolCall
	var usage ProviderUsage
	err := decodeProviderSSE(r, func(raw []byte) error {
		var envelope geminiResponse
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("gemini stream decode: %w", err)
		}
		if envelope.Error != nil {
			return fmt.Errorf("gemini API error %d: %s", envelope.Error.Code, envelope.Error.Message)
		}
		for _, candidate := range envelope.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					if part.Thought {
						if emit != nil {
							emit(ProviderStreamEvent{Type: ProviderStreamThinkingDelta, ThinkingDelta: part.Text})
						}
					} else {
						text.WriteString(part.Text)
						if emit != nil {
							emit(ProviderStreamEvent{Type: ProviderStreamTextDelta, TextDelta: part.Text})
						}
					}
				}
				if part.FunctionCall != nil && part.FunctionCall.Name != "" {
					args := part.FunctionCall.Args
					if args == nil {
						args = map[string]any{}
					}
					idx := len(toolCalls)
					toolCalls = append(toolCalls, ToolCall{ID: part.FunctionCall.Name, Name: part.FunctionCall.Name, Args: args})
					argJSON, _ := json.Marshal(args)
					if emit != nil {
						emit(ProviderStreamEvent{Type: ProviderStreamToolCallDelta, ToolCallDelta: ProviderToolCallDelta{Index: idx, ID: part.FunctionCall.Name, Name: part.FunctionCall.Name, ArgumentsDelta: string(argJSON)}})
					}
				}
			}
		}
		if envelope.UsageMetadata != nil {
			next := ProviderUsage{InputTokens: envelope.UsageMetadata.PromptTokenCount, OutputTokens: envelope.UsageMetadata.CandidatesTokenCount, CacheReadTokens: envelope.UsageMetadata.CachedContentTokenCount}
			if hasProviderUsage(next) && !providerUsageEqual(next, usage) {
				usage = next
				if emit != nil {
					emit(ProviderStreamEvent{Type: ProviderStreamUsage, Usage: usage})
				}
			}
		}
		return nil
	})
	if err != nil {
		return ProviderResult{}, err
	}
	if text.Len() == 0 && len(toolCalls) == 0 {
		return ProviderResult{}, fmt.Errorf("gemini stream: empty response")
	}
	return ProviderResult{Text: text.String(), ToolCalls: toolCalls, Usage: usage}, nil
}
