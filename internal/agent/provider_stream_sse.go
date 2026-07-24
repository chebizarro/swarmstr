package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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
