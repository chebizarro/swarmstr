package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

// ─── OpenAI ChatProvider ─────────────────────────────────────────────────────
//
// OpenAIChatProviderChat implements ChatProvider for OpenAI and compatible APIs
// using the official openai-go SDK. It handles a single LLM call; the agentic
// tool loop is driven externally by RunAgenticLoop.

// OpenAIChatProviderChat makes a single OpenAI Chat Completions API call.
type OpenAIChatProviderChat struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
	// ContextWindowTokens is passed as num_ctx to Ollama-compatible endpoints
	// to override the default 2048 context window. Zero means use provider default.
	ContextWindowTokens int
	// KeepAlive sets how long (in Go duration format, e.g. "30m") Ollama keeps
	// the model loaded in memory after the request. Empty means use server default.
	KeepAlive string
	// Store enables OpenAI's stored completions feature. When true, identical
	// requests may return cached responses. Opt-in because it has data retention
	// implications (stored completions are retained by OpenAI for 30 days).
	Store bool
}

// Chat implements ChatProvider.
func (p *OpenAIChatProviderChat) Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	messages = PrepareTranscriptMessages(messages, ResolveOpenAITranscriptPolicy(p.Model, p.BaseURL))
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "gpt-4o"
	}
	baseURL := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	} else {
		// Only normalize to /v1 for the real OpenAI host when the caller provided
		// a bare origin (e.g. https://api.openai.com). For custom/mock servers,
		// respect the exact base URL provided.
		if u, err := url.Parse(baseURL); err == nil {
			host := strings.ToLower(u.Host)
			if host == "api.openai.com" && (u.Path == "" || u.Path == "/") {
				baseURL = strings.TrimRight(baseURL, "/") + "/v1"
			}
		}
	}

	// Build SDK client options.
	clientOpts := []option.RequestOption{
		option.WithBaseURL(baseURL),
	}
	if p.APIKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(p.APIKey))
	}
	if p.Client != nil {
		clientOpts = append(clientOpts, option.WithHTTPClient(p.Client))
	}

	client := openai.NewClient(clientOpts...)

	// Convert LLMMessages to SDK message params.
	sdkMsgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "system":
			sdkMsgs = append(sdkMsgs, openai.SystemMessage(m.Content))

		case "user":
			sdkMsgs = append(sdkMsgs, buildOpenAISDKUserContent(m))

		case "assistant":
			if len(m.ToolCalls) > 0 {
				tcs := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					argsJSON, _ := json.Marshal(tc.Args)
					tcs = append(tcs, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: tc.ID,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tc.Name,
								Arguments: string(argsJSON),
							},
						},
					})
				}
				sdkMsgs = append(sdkMsgs, openai.ChatCompletionMessageParamUnion{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content: openai.ChatCompletionAssistantMessageParamContentUnion{
							OfString: openai.String(m.Content),
						},
						ToolCalls: tcs,
					},
				})
			} else {
				sdkMsgs = append(sdkMsgs, openai.AssistantMessage(m.Content))
			}

		case "tool":
			sdkMsgs = append(sdkMsgs, openai.ToolMessage(m.Content, m.ToolCallID))
		}
	}

	// Build request params.
	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: sdkMsgs,
	}
	// Enable stored completions for OpenAI response caching when configured.
	if p.Store {
		params.Store = openai.Bool(true)
	}

	// Convert tool definitions.
	if len(tools) > 0 {
		params.Tools = translateToolsToOpenAISDK(tools)
	}

	// Ollama-specific: inject num_ctx and keep_alive via extra body params.
	var extraOpts []option.RequestOption
	if isOllamaEndpoint(baseURL) {
		if p.ContextWindowTokens > 0 {
			extraOpts = append(extraOpts, option.WithJSONSet("options.num_ctx", p.ContextWindowTokens))
		}
		if p.KeepAlive != "" {
			extraOpts = append(extraOpts, option.WithJSONSet("keep_alive", p.KeepAlive))
		}
	}

	// Make the API call.
	completion, err := client.Chat.Completions.New(ctx, params, extraOpts...)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}

	return parseOpenAISDKResponse(completion), nil
}

// buildOpenAISDKUserContent converts a user LLMMessage to the SDK format.
// When images are present, it uses multi-modal content parts.
func buildOpenAISDKUserContent(msg LLMMessage) openai.ChatCompletionMessageParamUnion {
	if len(msg.Images) == 0 {
		return openai.UserMessage(msg.Content)
	}

	// Multi-modal: image parts + text part.
	parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(msg.Images)+1)
	for _, img := range msg.Images {
		var imageURL string
		if img.Base64 != "" {
			mt := img.MimeType
			if mt == "" {
				mt = "image/jpeg"
			}
			imageURL = "data:" + mt + ";base64," + img.Base64
		} else if img.URL != "" {
			imageURL = img.URL
		}
		if imageURL != "" {
			parts = append(parts, openai.ChatCompletionContentPartUnionParam{
				OfImageURL: &openai.ChatCompletionContentPartImageParam{
					ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
						URL: imageURL,
					},
				},
			})
		}
	}
	parts = append(parts, openai.ChatCompletionContentPartUnionParam{
		OfText: &openai.ChatCompletionContentPartTextParam{
			Text: msg.Content,
		},
	})

	return openai.UserMessage(parts)
}

// translateToolsToOpenAISDK converts ToolDefinition to the SDK's tool params.
func translateToolsToOpenAISDK(defs []ToolDefinition) []openai.ChatCompletionToolUnionParam {
	out := make([]openai.ChatCompletionToolUnionParam, 0, len(defs))
	for _, d := range defs {
		params := toolInputSchemaMap(d)

		// Convert params map to shared.FunctionParameters via JSON round-trip.
		paramsJSON, _ := json.Marshal(params)
		var funcParams shared.FunctionParameters
		_ = json.Unmarshal(paramsJSON, &funcParams)

		out = append(out, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        d.Name,
			Description: openai.String(d.Description),
			Parameters:  funcParams,
		}))
	}
	return out
}

// isOllamaEndpoint returns true if the base URL points to an Ollama server.
// Ollama typically runs on localhost:11434, but can be overridden via
// OLLAMA_BASE_URL. We detect it by checking for common Ollama host patterns.
func isOllamaEndpoint(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	// Default Ollama port.
	if strings.HasSuffix(host, ":11434") {
		return true
	}
	// Also check the hostname-only form (e.g. "ollama" in Docker).
	hostname := strings.ToLower(u.Hostname())
	return hostname == "ollama" || strings.HasPrefix(hostname, "ollama.")
}

// parseOpenAISDKResponse converts an SDK ChatCompletion to LLMResponse.
func parseOpenAISDKResponse(resp *openai.ChatCompletion) *LLMResponse {
	if len(resp.Choices) == 0 {
		return &LLMResponse{}
	}

	choice := resp.Choices[0]
	var toolCalls []ToolCall
	for _, tc := range choice.Message.ToolCalls {
		if tc.Type != "function" {
			continue
		}
		var args map[string]any
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		}
		toolCalls = append(toolCalls, ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: args,
		})
	}

	return &LLMResponse{
		Content:   strings.TrimSpace(choice.Message.Content),
		ToolCalls: toolCalls,
		Usage: ProviderUsage{
			InputTokens:     resp.Usage.PromptTokens,
			OutputTokens:    resp.Usage.CompletionTokens,
			CacheReadTokens: resp.Usage.PromptTokensDetails.CachedTokens,
		},
		NeedsToolResults: choice.FinishReason == "tool_calls",
	}
}
