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

type OpenAIResponsesProvider struct {
	BaseURL, APIKey, Model string
	Client                 *http.Client
}
type responsesRequest struct {
	Model           string           `json:"model"`
	Input           []map[string]any `json:"input"`
	Tools           []map[string]any `json:"tools,omitempty"`
	MaxOutputTokens int              `json:"max_output_tokens,omitempty"`
	Reasoning       map[string]any   `json:"reasoning,omitempty"`
}

func (p *OpenAIResponsesProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	r, e := p.Chat(ctx, buildLLMMessagesFromTurnWithProfile(turn, "", disabledPromptCacheProfile()), nil, chatOptionsFromTurn(turn, disabledPromptCacheProfile()))
	if r == nil {
		return ProviderResult{}, e
	}
	return llmResponseToProviderResult(r), e
}
func (p *OpenAIResponsesProvider) Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	rb := p.buildRequest(messages, tools, opts)
	b, _ := json.Marshal(rb)
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/responses", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	c := p.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai responses: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return parseResponsesAPI(body)
}
func (p *OpenAIResponsesProvider) buildRequest(messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) responsesRequest {
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "gpt-4.1"
	}
	in := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		in = append(in, map[string]any{"role": m.Role, "content": m.Content})
	}
	r := responsesRequest{Model: model, Input: in}
	if len(tools) > 0 {
		r.Tools = toolsToResponses(tools)
	}
	if opts.MaxTokens > 0 {
		r.MaxOutputTokens = opts.MaxTokens
	}
	if opts.ThinkingBudget > 0 {
		r.Reasoning = map[string]any{"effort": "medium"}
	}
	return r
}
