package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type MistralChatProvider struct {
	BaseURL, APIKey, Model string
	Client                 *http.Client
}

type mistralRequest struct {
	Model      string           `json:"model"`
	Messages   []map[string]any `json:"messages"`
	Tools      []map[string]any `json:"tools,omitempty"`
	ToolChoice any              `json:"tool_choice,omitempty"`
	MaxTokens  int              `json:"max_tokens,omitempty"`
	Stream     bool             `json:"stream"`
}

func (p *MistralChatProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	r, e := p.Chat(ctx, buildLLMMessagesFromTurnWithProfile(turn, "", disabledPromptCacheProfile()), nil, chatOptionsFromTurn(turn, disabledPromptCacheProfile()))
	if r == nil {
		return ProviderResult{}, e
	}
	return llmResponseToProviderResult(r), e
}
func (p *MistralChatProvider) Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	reqBody, err := p.buildRequest(messages, tools, opts)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(reqBody)
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://api.mistral.ai/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mistral: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return parseGenericChatCompletion(body)
}
func (p *MistralChatProvider) buildRequest(messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (mistralRequest, error) {
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "mistral-large-latest"
	}
	ms := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		mm := map[string]any{"role": m.Role, "content": m.Content}
		if m.ToolCallID != "" {
			mm["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			tcs := make([]map[string]any, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				if strings.TrimSpace(tc.ID) == "" {
					return mistralRequest{}, fmt.Errorf("mistral requires non-empty tool call ids")
				}
				args, _ := json.Marshal(tc.Args)
				tcs = append(tcs, map[string]any{"id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": string(args)}})
			}
			mm["tool_calls"] = tcs
		}
		ms = append(ms, mm)
	}
	req := mistralRequest{Model: model, Messages: ms, Stream: false}
	if opts.MaxTokens > 0 {
		req.MaxTokens = opts.MaxTokens
	}
	if len(tools) > 0 {
		req.Tools = toolsToGenericOpenAI(tools)
		req.ToolChoice = "auto"
	}
	return req, nil
}

func normalizeKimiModelID(model string) string {
	m := strings.TrimSpace(model)
	for _, p := range []string{"moonshot/", "kimi/", "kimicode/"} {
		if strings.HasPrefix(strings.ToLower(m), p) {
			return m[len(p):]
		}
	}
	return m
}
func moonshotPrepareRequest(req *http.Request) error {
	req.Header.Set("X-Moonshot-Thinking", "auto")
	return nil
}
