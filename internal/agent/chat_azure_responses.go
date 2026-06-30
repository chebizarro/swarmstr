package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type AzureResponsesProvider struct {
	BaseURL, APIKey, Model string
	Client                 *http.Client
}

func (p *AzureResponsesProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	r, e := p.Chat(ctx, buildLLMMessagesFromTurnWithProfile(turn, "", disabledPromptCacheProfile()), nil, chatOptionsFromTurn(turn, disabledPromptCacheProfile()))
	if r == nil {
		return ProviderResult{}, e
	}
	return llmResponseToProviderResult(r), e
}
func (p *AzureResponsesProvider) Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	rb := (&OpenAIResponsesProvider{Model: p.Model}).buildRequest(messages, tools, opts)
	b, _ := json.Marshal(rb)
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("azure responses base URL is required")
	}
	endpoint := base + "/openai/responses?api-version=2025-04-01-preview"
	if u, err := url.Parse(base); err == nil && strings.Contains(u.Path, "/openai/") {
		endpoint = base
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("api-key", p.APIKey)
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
		return nil, fmt.Errorf("azure responses: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return parseResponsesAPI(body)
}
