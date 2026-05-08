package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ─── Anthropic ChatProvider (using official SDK) ─────────────────────────────
//
// AnthropicChatProvider implements ChatProvider using the official anthropic-sdk-go.
// It handles a single LLM call; the agentic tool loop is driven externally
// by RunAgenticLoop.

// AnthropicChatProvider makes a single Anthropic Messages API call.
type AnthropicChatProvider struct {
	client      *anthropic.Client
	model       string                 // model name from config, e.g. "claude-haiku-4-5"
	tokenSource func() (string, error) // for OAuth; nil for API key auth
	httpClient  *http.Client           // optional custom HTTP client (for testing)
	baseURL     string                 // optional custom base URL (for testing)
	promptCache *PromptCacheProfile
}

func (p *AnthropicChatProvider) PromptCacheProfile() PromptCacheProfile {
	return promptCacheProfileOrDefault(p.promptCache, PromptCacheProviderAnthropic)
}

// NewAnthropicChatProvider creates a ChatProvider for Anthropic using an API key.
func NewAnthropicChatProvider(apiKey string, opts ...AnthropicChatOption) *AnthropicChatProvider {
	p := &AnthropicChatProvider{}
	for _, o := range opts {
		o(p)
	}

	clientOpts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if p.httpClient != nil {
		clientOpts = append(clientOpts, option.WithHTTPClient(p.httpClient))
	}
	if p.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(p.baseURL))
	}
	client := anthropic.NewClient(clientOpts...)
	p.client = &client
	return p
}

// modelOrDefault returns the configured model, falling back to claude-haiku-4-5.
func (p *AnthropicChatProvider) modelOrDefault() string {
	if p.model != "" {
		return p.model
	}
	return "claude-haiku-4-5"
}

// AnthropicChatOption configures an AnthropicChatProvider.
type AnthropicChatOption func(*AnthropicChatProvider)

// WithHTTPClient sets a custom HTTP client (useful for testing).
func WithHTTPClient(c *http.Client) AnthropicChatOption {
	return func(p *AnthropicChatProvider) { p.httpClient = c }
}

// WithBaseURL sets a custom base URL (useful for testing).
func WithBaseURL(url string) AnthropicChatOption {
	return func(p *AnthropicChatProvider) { p.baseURL = url }
}

// WithModel sets the model for this provider.
func WithModel(model string) AnthropicChatOption {
	return func(p *AnthropicChatProvider) { p.model = model }
}

// WithAnthropicPromptCacheProfile sets the resolved prompt-cache policy.
func WithAnthropicPromptCacheProfile(profile PromptCacheProfile) AnthropicChatOption {
	return func(p *AnthropicChatProvider) { p.promptCache = promptCacheProfilePtr(profile) }
}

// NewAnthropicChatProviderOAuth creates a ChatProvider for Anthropic using OAuth.
// tokenSource is called before each request to get a fresh access token.
func NewAnthropicChatProviderOAuth(initialToken string, tokenSource func() (string, error), opts ...AnthropicChatOption) *AnthropicChatProvider {
	p := &AnthropicChatProvider{tokenSource: tokenSource}
	for _, o := range opts {
		o(p)
	}

	clientOpts := []option.RequestOption{option.WithAPIKey(initialToken)}
	if p.httpClient != nil {
		clientOpts = append(clientOpts, option.WithHTTPClient(p.httpClient))
	}
	if p.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(p.baseURL))
	}
	client := anthropic.NewClient(clientOpts...)
	p.client = &client
	return p
}

// Chat implements ChatProvider. It converts LLMMessage to the Anthropic format,
// makes a single API call, and converts the response back.
func (p *AnthropicChatProvider) Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	messages = PrepareTranscriptMessages(messages, ResolveAnthropicTranscriptPolicy(p.modelOrDefault()))
	var system []anthropic.TextBlockParam
	var anthropicMessages []anthropic.MessageParam

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			if len(msg.SystemParts) > 0 && opts.CacheSystem {
				for _, part := range msg.SystemParts {
					block := anthropic.TextBlockParam{Text: part.Text}
					if part.CacheControl != nil && part.CacheControl.Type == "ephemeral" {
						block.CacheControl = anthropic.NewCacheControlEphemeralParam()
					}
					system = append(system, block)
				}
			} else if msg.Content != "" {
				block := anthropic.TextBlockParam{Text: msg.Content}
				if opts.CacheSystem {
					block.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
				system = append(system, block)
			}

		case "user":
			content := buildAnthropicSDKUserContent(msg)
			anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(content...))

		case "assistant":
			blocks := buildAnthropicSDKAssistantContent(msg)
			anthropicMessages = append(anthropicMessages, anthropic.NewAssistantMessage(blocks...))

		case "tool":
			anthropicMessages = append(anthropicMessages,
				anthropic.NewUserMessage(
					anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false),
				),
			)
		}
	}

	// Build request params.
	maxTokens := int64(opts.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.modelOrDefault()),
		Messages:  anthropicMessages,
		MaxTokens: maxTokens,
	}

	if len(system) > 0 {
		params.System = system
	}

	// Tools
	if len(tools) > 0 {
		params.Tools = translateToolsToSDK(tools, opts.CacheTools)
	}

	// Extended thinking
	if opts.ThinkingBudget > 0 {
		budget := int64(opts.ThinkingBudget)
		if budget >= maxTokens {
			budget = maxTokens - 1
		}
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budget)
	}

	// Build request options.
	var reqOpts []option.RequestOption

	if p.tokenSource != nil {
		// OAuth path: use Bearer token auth and include the full set of beta
		// flags required for Claude Code OAuth sessions.  The old HTTP-based
		// implementation set "claude-code-20250219,oauth-2025-04-20" plus the
		// x-app and user-agent headers — replicate that here so the API
		// doesn't reject the request with a generic 400 "Error".
		tok, err := p.tokenSource()
		if err != nil {
			return nil, fmt.Errorf("anthropic oauth: refreshing token: %w", err)
		}
		reqOpts = append(reqOpts,
			option.WithAuthToken(tok),
			option.WithHeader("anthropic-beta", anthropicOAuthBeta+","+buildAnthropicBetaHeader(opts)),
			option.WithHeader("x-app", "cli"),
			option.WithHeader("user-agent", anthropicOAuthUserAgent),
		)
	} else {
		// API-key path: standard beta header only.
		reqOpts = append(reqOpts,
			option.WithHeader("anthropic-beta", buildAnthropicBetaHeader(opts)),
		)
	}

	// Make the API call.
	resp, err := p.client.Messages.New(ctx, params, reqOpts...)
	if err != nil {
		// Dump the raw request/response for debugging invalid_request_error.
		var apierr *anthropic.Error
		if errors.As(err, &apierr) {
			if f, ferr := os.OpenFile("/tmp/strand-api-debug.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); ferr == nil {
				fmt.Fprintf(f, "=== REQUEST ===\n%s\n\n=== RESPONSE ===\n%s\n", apierr.DumpRequest(true), apierr.DumpResponse(true))
				f.Close()
			}
			log.Printf("anthropic DEBUG: request/response dumped to /tmp/strand-api-debug.txt")
		}
		return nil, fmt.Errorf("anthropic API: %w", err)
	}

	return parseAnthropicSDKResponse(resp), nil
}

// ─── SDK format converters ───────────────────────────────────────────────────

// buildAnthropicSDKUserContent converts user message content to SDK blocks.
func buildAnthropicSDKUserContent(msg LLMMessage) []anthropic.ContentBlockParamUnion {
	if len(msg.Images) == 0 {
		return []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(msg.Content)}
	}

	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Images)+1)
	for _, img := range msg.Images {
		if img.Base64 != "" {
			mt := img.MimeType
			if mt == "" {
				mt = "image/jpeg"
			}
			blocks = append(blocks, anthropic.NewImageBlockBase64(mt, img.Base64))
		} else if img.URL != "" {
			blocks = append(blocks, anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: img.URL}))
		}
	}
	blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
	return blocks
}

// buildAnthropicSDKAssistantContent converts assistant message to SDK blocks.
func buildAnthropicSDKAssistantContent(msg LLMMessage) []anthropic.ContentBlockParamUnion {
	var blocks []anthropic.ContentBlockParamUnion
	if msg.Content != "" {
		blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
	}
	for _, tc := range msg.ToolCalls {
		args := tc.Args
		if args == nil {
			args = map[string]any{}
		}
		blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, args, tc.Name))
	}
	if len(blocks) == 0 {
		// Anthropic requires at least one content block.
		blocks = append(blocks, anthropic.NewTextBlock(""))
	}
	return blocks
}

// translateToolsToSDK converts ToolDefinition to the SDK's tool format.
// When cacheLastTool is true, the last tool gets cache_control for prompt caching.
func translateToolsToSDK(defs []ToolDefinition, cacheLastTool bool) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, 0, len(defs))
	for i, d := range defs {
		tool := anthropic.ToolParam{
			Name:        d.Name,
			InputSchema: buildSDKInputSchema(d),
		}
		if d.Description != "" {
			tool.Description = anthropic.String(d.Description)
		}
		// Mark the last tool for caching.
		if cacheLastTool && i == len(defs)-1 {
			tool.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
		result = append(result, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return result
}

// buildSDKInputSchema converts ToolParameters to the SDK's input schema format.
//
// NOTE: Properties is always set (even as an empty map) to ensure the returned
// ToolInputSchemaParam is never the zero value.  The SDK tags InputSchema with
// `json:"input_schema,omitzero"`, so a fully-zero struct would be silently
// dropped from the JSON payload, causing Anthropic to return 400
// "input_schema: Field required" for tools that take no parameters.
func buildSDKInputSchema(d ToolDefinition) anthropic.ToolInputSchemaParam {
	schemaMap := toolInputSchemaMap(d)
	if _, ok := schemaMap["properties"]; !ok {
		schemaMap["properties"] = map[string]any{}
	}
	b, _ := json.Marshal(schemaMap)
	var schema anthropic.ToolInputSchemaParam
	_ = json.Unmarshal(b, &schema)
	if schema.Properties == nil {
		schema.Properties = map[string]any{}
	}
	return schema
}

// buildAnthropicBetaHeader returns the beta features header value.
func buildAnthropicBetaHeader(opts ChatOptions) string {
	headers := []string{"prompt-caching-2024-07-31"}
	if opts.ThinkingBudget > 0 {
		headers = append(headers, "interleaved-thinking-2025-05-14")
	}
	return strings.Join(headers, ",")
}

// parseAnthropicSDKResponse converts an SDK response to an LLMResponse.
func parseAnthropicSDKResponse(resp *anthropic.Message) *LLMResponse {
	var content strings.Builder
	var toolCalls []ToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "thinking":
			// Extended-thinking internal reasoning — intentionally skipped.
		case "text":
			tb := block.AsText()
			content.WriteString(tb.Text)
		case "tool_use":
			tu := block.AsToolUse()
			var args map[string]any
			if err := json.Unmarshal(tu.Input, &args); err != nil {
				log.Printf("anthropic: failed to decode tool input for %q: %v", tu.Name, err)
				args = map[string]any{"raw": string(tu.Input)}
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   tu.ID,
				Name: tu.Name,
				Args: args,
			})
		}
	}

	needsToolResults := resp.StopReason == anthropic.StopReasonToolUse

	var usage ProviderUsage
	usage.InputTokens = int64(resp.Usage.InputTokens)
	usage.OutputTokens = int64(resp.Usage.OutputTokens)
	usage.CacheReadTokens = int64(resp.Usage.CacheReadInputTokens)
	usage.CacheCreationTokens = int64(resp.Usage.CacheCreationInputTokens)

	return &LLMResponse{
		Content:          content.String(),
		ToolCalls:        toolCalls,
		Usage:            usage,
		NeedsToolResults: needsToolResults,
	}
}

// ─── AnthropicProvider integration ───────────────────────────────────────────
//
// These methods add ChatProvider support to the existing AnthropicProvider,
// allowing it to use the shared agentic loop while keeping the Provider
// interface backwards-compatible.

// chatProvider returns a ChatProvider for this AnthropicProvider.
// It handles both API-key and OAuth authentication.
func (p *AnthropicProvider) chatProvider() ChatProvider {
	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(getEnvFn("ANTHROPIC_API_KEY"))
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(getEnvFn("ANTHROPIC_OAUTH_TOKEN"))
	}

	// Build options (e.g. custom HTTP client for testing).
	var opts []AnthropicChatOption
	if p.Client != nil {
		opts = append(opts, WithHTTPClient(p.Client))
	}
	// Pass model from config; falls back to "claude-haiku-4-5" if empty.
	if m := strings.TrimSpace(p.Model); m != "" {
		opts = append(opts, WithModel(m))
	}
	opts = append(opts, WithAnthropicPromptCacheProfile(p.PromptCacheProfile()))

	// Check for OAuth credentials.
	if access, refresh, isOAuth := ParseAnthropicOAuthCredential(apiKey); isOAuth {
		return NewAnthropicChatProviderOAuth(access, func() (string, error) {
			return p.resolveOAuthToken(access, refresh)
		}, opts...)
	}

	return NewAnthropicChatProvider(apiKey, opts...)
}

// resolveOAuthToken returns a valid access token, refreshing if needed.
func (p *AnthropicProvider) resolveOAuthToken(access, refresh string) (string, error) {
	oauthTokenCache.mu.Lock()
	if oauthTokenCache.access != "" && time.Now().Before(oauthTokenCache.expiry) {
		tok := oauthTokenCache.access
		oauthTokenCache.mu.Unlock()
		return tok, nil
	}
	oauthTokenCache.mu.Unlock()

	if refresh == "" {
		return access, nil
	}

	newAccess, newRefresh, err := AnthropicOAuthRefresh(context.Background(), refresh)
	if err != nil {
		return access, nil // fallback to original token
	}

	oauthTokenCache.mu.Lock()
	oauthTokenCache.access = newAccess
	oauthTokenCache.refresh = newRefresh
	oauthTokenCache.expiry = time.Now().Add(55 * time.Minute)
	oauthTokenCache.mu.Unlock()

	return newAccess, nil
}

// getEnvFn is a package-level function for reading environment variables.
// Assigned in provider.go init to os.Getenv.
var getEnvFn = func(key string) string { return "" }
