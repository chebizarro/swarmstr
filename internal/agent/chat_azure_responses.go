package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type AzureResponsesProvider struct {
	BaseURL, APIKey, Model string
	Client                 *http.Client
	Transport              ResponsesTransportPolicy
	transportState         responsesTransportState
}

func (p *AzureResponsesProvider) endpoint() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("azure responses base URL is required")
	}
	endpoint := base + "/openai/responses?api-version=2025-04-01-preview"
	if u, err := url.Parse(base); err == nil && strings.Contains(u.Path, "/openai/") {
		endpoint = base
	}
	return endpoint, nil
}

func (p *AzureResponsesProvider) requestConfig() (responsesHTTPConfig, error) {
	endpoint, err := p.endpoint()
	if err != nil {
		return responsesHTTPConfig{}, err
	}
	return responsesHTTPConfig{
		Endpoint: endpoint, Client: p.Client, ErrorPrefix: "azure responses",
		ApplyAuth: func(req *http.Request) {
			if p.APIKey != "" {
				req.Header.Set("api-key", p.APIKey)
			}
		},
	}, nil
}

func (p *AzureResponsesProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	r, err := p.Chat(ctx, buildLLMMessagesFromTurnWithProfile(turn, "", disabledPromptCacheProfile()), turn.Tools, chatOptionsFromTurn(turn, disabledPromptCacheProfile()))
	if r == nil {
		return ProviderResult{}, err
	}
	return llmResponseToProviderResult(r), err
}

func (p *AzureResponsesProvider) Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	cfg, err := p.requestConfig()
	if err != nil {
		return nil, err
	}
	request := (&OpenAIResponsesProvider{Model: p.Model}).buildRequest(messages, tools, opts)
	resp, err := executeResponsesRequest(ctx, request, cfg)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("azure responses read: %w", err)
	}
	return parseResponsesAPI(body)
}

func (p *AzureResponsesProvider) Stream(ctx context.Context, turn Turn, onChunk func(string)) (ProviderResult, error) {
	return streamEventsAsLegacy(ctx, turn, onChunk, p)
}

func (p *AzureResponsesProvider) StreamEvents(ctx context.Context, turn Turn, emit ProviderStreamEventSink) (ProviderResult, error) {
	return runProviderEventStream(emit, func(emit ProviderStreamEventSink) (ProviderResult, error) {
		cfg, err := p.requestConfig()
		if err != nil {
			return ProviderResult{}, err
		}
		request := (&OpenAIResponsesProvider{Model: p.Model}).buildRequest(
			buildLLMMessagesFromTurnWithProfile(turn, "", disabledPromptCacheProfile()),
			turn.Tools,
			chatOptionsFromTurn(turn, disabledPromptCacheProfile()),
		)
		policy := p.Transport
		if strings.TrimSpace(string(policy)) == "" {
			policy = ResponsesTransportPolicy(strings.TrimSpace(getEnvFn("AZURE_OPENAI_RESPONSES_TRANSPORT")))
		}
		// Azure's Responses endpoint supports stored continuation, but native
		// Responses WebSocket is currently restricted to the official OpenAI API.
		return streamResponsesRequest(ctx, turn, request, cfg, policy, false, true, &p.transportState, emit)
	})
}
