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
	Transport              ResponsesTransportPolicy
	transportState         responsesTransportState
}

type responsesRequest struct {
	Model              string           `json:"model"`
	Input              []map[string]any `json:"input"`
	Tools              []map[string]any `json:"tools,omitempty"`
	MaxOutputTokens    int              `json:"max_output_tokens,omitempty"`
	Reasoning          map[string]any   `json:"reasoning,omitempty"`
	Stream             bool             `json:"stream,omitempty"`
	Store              bool             `json:"store,omitempty"`
	PreviousResponseID string           `json:"previous_response_id,omitempty"`
}

type responsesHTTPConfig struct {
	Endpoint    string
	Client      *http.Client
	ErrorPrefix string
	ApplyAuth   func(*http.Request)
}

func executeResponsesRequest(ctx context.Context, body responsesRequest, cfg responsesHTTPConfig) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%s request encode: %w", cfg.ErrorPrefix, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if body.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if cfg.ApplyAuth != nil {
		cfg.ApplyAuth(req)
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	errBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if readErr != nil {
		return nil, fmt.Errorf("%s: %s (read error: %v)", cfg.ErrorPrefix, resp.Status, readErr)
	}
	return nil, fmt.Errorf("%s: %s: %s", cfg.ErrorPrefix, resp.Status, strings.TrimSpace(string(errBody)))
}

func (p *OpenAIResponsesProvider) endpoint() string {
	base := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return base + "/responses"
}

func (p *OpenAIResponsesProvider) requestConfig() responsesHTTPConfig {
	return responsesHTTPConfig{
		Endpoint: p.endpoint(), Client: p.Client, ErrorPrefix: "openai responses",
		ApplyAuth: func(req *http.Request) {
			if p.APIKey != "" {
				req.Header.Set("Authorization", "Bearer "+p.APIKey)
			}
		},
	}
}

func (p *OpenAIResponsesProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	r, err := p.Chat(ctx, buildLLMMessagesFromTurnWithProfile(turn, "", disabledPromptCacheProfile()), turn.Tools, chatOptionsFromTurn(turn, disabledPromptCacheProfile()))
	if r == nil {
		return ProviderResult{}, err
	}
	return llmResponseToProviderResult(r), err
}

func (p *OpenAIResponsesProvider) Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	resp, err := executeResponsesRequest(ctx, p.buildRequest(messages, tools, opts), p.requestConfig())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("openai responses read: %w", err)
	}
	return parseResponsesAPI(body)
}

func (p *OpenAIResponsesProvider) Stream(ctx context.Context, turn Turn, onChunk func(string)) (ProviderResult, error) {
	return streamEventsAsLegacy(ctx, turn, onChunk, p)
}

func (p *OpenAIResponsesProvider) StreamEvents(ctx context.Context, turn Turn, emit ProviderStreamEventSink) (ProviderResult, error) {
	return runProviderEventStream(emit, func(emit ProviderStreamEventSink) (ProviderResult, error) {
		request := p.buildRequest(
			buildLLMMessagesFromTurnWithProfile(turn, "", disabledPromptCacheProfile()),
			turn.Tools,
			chatOptionsFromTurn(turn, disabledPromptCacheProfile()),
		)
		policy := p.Transport
		if strings.TrimSpace(string(policy)) == "" {
			policy = ResponsesTransportPolicy(strings.TrimSpace(getEnvFn("OPENAI_RESPONSES_TRANSPORT")))
		}
		return streamResponsesRequest(ctx, turn, request, p.requestConfig(), policy, true, true, &p.transportState, emit)
	})
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
