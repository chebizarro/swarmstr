package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// ─── Gemini ChatProvider ─────────────────────────────────────────────────────
//
// GeminiChatProvider implements ChatProvider for the Google Gemini API.
// It handles a single LLM call; the agentic tool loop is driven externally.

// GeminiChatProvider makes a single Google Gemini API call.
type GeminiChatProvider struct {
	APIKey      string
	Model       string
	Client      *http.Client
	PromptCache *PromptCacheProfile
}

func (p *GeminiChatProvider) PromptCacheProfile() PromptCacheProfile {
	return promptCacheProfileOrDefault(p.PromptCache, PromptCacheProviderGemini)
}

// Chat implements ChatProvider.
func (p *GeminiChatProvider) Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	messages = PrepareTranscriptMessages(messages, ResolveGeminiTranscriptPolicy(p.Model))
	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		for _, envKey := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"} {
			if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
				apiKey = v
				break
			}
		}
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Gemini API key not configured (set GEMINI_API_KEY)")
	}

	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "gemini-2.0-flash"
	}

	// Build contents and system instruction from LLMMessages.
	var systemInstruction *geminiContent
	contents := make([]geminiContent, 0, len(messages))
	toolNamesByID := make(map[string]string)

	for _, m := range messages {
		switch m.Role {
		case "system":
			systemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: m.Content}},
			}

		case "user":
			contents = append(contents, geminiContent{
				Role:  "user",
				Parts: buildGeminiParts(m.Content, m.Images),
			})

		case "assistant":
			gc := geminiContent{
				Role:  "model",
				Parts: []geminiPart{},
			}
			if m.Content != "" {
				gc.Parts = append(gc.Parts, geminiPart{Text: m.Content})
			}
			// Encode tool calls as functionCall parts.
			for _, tc := range m.ToolCalls {
				toolNamesByID[tc.ID] = tc.Name
				gc.Parts = append(gc.Parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Name,
						Args: tc.Args,
					},
				})
			}
			if len(gc.Parts) > 0 {
				contents = append(contents, gc)
			}

		case "tool":
			// Gemini function responses key on function name rather than opaque tool IDs.
			responseName := m.ToolCallID
			if name := toolNamesByID[m.ToolCallID]; name != "" {
				responseName = name
			}
			contents = append(contents, geminiContent{
				Role: "function",
				Parts: []geminiPart{
					{
						FunctionResponse: &geminiFunctionResponse{
							Name: responseName,
							Response: map[string]any{
								"result": m.Content,
							},
						},
					},
				},
			})
		}
	}

	geminiTools := toolDefsToGemini(tools)

	// Attempt Gemini CachedContent for the system instruction + tools only
	// when the resolved prompt-cache profile enables Gemini's native cache.
	// When a cached content resource is active, we omit systemInstruction and
	// tools from the request body (the server uses the cached versions).
	req := geminiRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
		Tools:             geminiTools,
	}
	profile := p.PromptCacheProfile()
	if profile.Enabled && profile.UseGeminiCachedContent {
		if cachedName := resolveGeminiCache(ctx, apiKey, model, systemInstruction, geminiTools, p.Client); cachedName != "" {
			req.CachedContent = cachedName
			req.SystemInstruction = nil
			req.Tools = nil
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	var out geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("gemini decode: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("gemini API error %d: %s", out.Error.Code, out.Error.Message)
	}
	if len(out.Candidates) == 0 || len(out.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("gemini: empty response")
	}

	// Collect text and any function calls from response parts.
	var textBuf strings.Builder
	var toolCalls []ToolCall
	for _, part := range out.Candidates[0].Content.Parts {
		if part.Text != "" {
			textBuf.WriteString(part.Text)
		}
		if part.FunctionCall != nil && part.FunctionCall.Name != "" {
			args := part.FunctionCall.Args
			if args == nil {
				args = map[string]any{}
			}
			// For Gemini, the tool call ID is the function name (used for matching results).
			toolCalls = append(toolCalls, ToolCall{
				ID:   part.FunctionCall.Name,
				Name: part.FunctionCall.Name,
				Args: args,
			})
		}
	}

	text := strings.TrimSpace(textBuf.String())
	if text == "" && len(toolCalls) == 0 {
		return nil, fmt.Errorf("gemini: empty response")
	}

	// If there are function calls, the model wants tool results.
	needsToolResults := len(toolCalls) > 0

	var usage ProviderUsage
	if out.UsageMetadata != nil {
		usage.InputTokens = out.UsageMetadata.PromptTokenCount
		usage.OutputTokens = out.UsageMetadata.CandidatesTokenCount
		usage.CacheReadTokens = out.UsageMetadata.CachedContentTokenCount
	}

	return &LLMResponse{
		Content:          text,
		ToolCalls:        toolCalls,
		Usage:            usage,
		NeedsToolResults: needsToolResults,
	}, nil
}
