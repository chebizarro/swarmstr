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

type Provider interface {
	Generate(context.Context, Turn) (ProviderResult, error)
}

type ProviderResult struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type EchoProvider struct{}

func (EchoProvider) Generate(_ context.Context, turn Turn) (ProviderResult, error) {
	return ProviderResult{Text: "ack: " + turn.UserText}, nil
}

type HTTPProvider struct {
	URL    string
	APIKey string
	Client *http.Client
}

type httpRequest struct {
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt"`
	Context   string `json:"context,omitempty"`
}

type httpResponse struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

func (p *HTTPProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	contextText := turn.Context
	const maxContextBytes = 16 * 1024
	if len(contextText) > maxContextBytes {
		contextText = truncateUTF8ByBytes(contextText, maxContextBytes)
	}
	body, err := json.Marshal(httpRequest{SessionID: turn.SessionID, Prompt: turn.UserText, Context: contextText})
	if err != nil {
		return ProviderResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL, bytes.NewReader(body))
	if err != nil {
		return ProviderResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProviderResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ProviderResult{}, fmt.Errorf("provider returned %s", resp.Status)
	}
	var out httpResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ProviderResult{}, err
	}
	if strings.TrimSpace(out.Text) == "" && len(out.ToolCalls) == 0 {
		return ProviderResult{}, fmt.Errorf("provider returned empty response")
	}
	return ProviderResult{Text: out.Text, ToolCalls: out.ToolCalls}, nil
}

func truncateUTF8ByBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	if cut == 0 {
		return ""
	}
	return s[:cut]
}

func NewProviderFromEnv() (Provider, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_PROVIDER")))
	switch mode {
	case "", "echo":
		return EchoProvider{}, nil
	case "http":
		url := strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_URL"))
		if url == "" {
			return nil, fmt.Errorf("SWARMSTR_AGENT_HTTP_URL is required when SWARMSTR_AGENT_PROVIDER=http")
		}
		return &HTTPProvider{URL: url, APIKey: strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_API_KEY"))}, nil
	default:
		return nil, fmt.Errorf("unknown SWARMSTR_AGENT_PROVIDER %q", mode)
	}
}

// NewProviderForModel constructs a Provider for the given model identifier.
//   - "" / "echo"                → EchoProvider (no external dependency)
//   - "http" / "http-default"    → HTTPProvider configured from env vars
//
// This is used by BuildRuntimeForModel and the agents.create RPC to spin up
// per-agent runtimes with model-specific providers.
// AnthropicProvider calls the Anthropic Messages API (POST /v1/messages).
// Set ANTHROPIC_API_KEY in the environment or use ProviderOverride.APIKey.
type AnthropicProvider struct {
	APIKey string
	Model  string
	Client *http.Client
}

type anthropicRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system,omitempty"`
	Messages  []anthropicMessage  `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *AnthropicProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}
	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	}
	if apiKey == "" {
		return ProviderResult{}, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}

	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: 4096,
		Messages:  []anthropicMessage{{Role: "user", Content: strings.TrimSpace(turn.UserText)}},
	}
	if context := strings.TrimSpace(turn.Context); context != "" {
		reqBody.System = context
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return ProviderResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return ProviderResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProviderResult{}, err
	}
	defer resp.Body.Close()

	var out anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ProviderResult{}, fmt.Errorf("anthropic decode: %w", err)
	}
	if out.Error != nil {
		return ProviderResult{}, fmt.Errorf("anthropic error %s: %s", out.Error.Type, out.Error.Message)
	}
	if resp.StatusCode >= 300 {
		return ProviderResult{}, fmt.Errorf("anthropic returned %s", resp.Status)
	}
	for _, c := range out.Content {
		if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
			return ProviderResult{Text: c.Text}, nil
		}
	}
	return ProviderResult{}, fmt.Errorf("anthropic returned no text content")
}

// OpenAIChatProvider calls the OpenAI Chat Completions API (POST /v1/chat/completions).
// Also compatible with Ollama and other OpenAI-compatible endpoints.
// Set OPENAI_API_KEY (or ANTHROPIC_API_KEY is not used for this provider).
type OpenAIChatProvider struct {
	BaseURL string // defaults to https://api.openai.com
	APIKey  string
	Model   string
	Client  *http.Client
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (p *OpenAIChatProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "gpt-4o"
	}
	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}

	baseURL := strings.TrimSpace(p.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	messages := make([]openAIMessage, 0, 2)
	if context := strings.TrimSpace(turn.Context); context != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: context})
	}
	messages = append(messages, openAIMessage{Role: "user", Content: strings.TrimSpace(turn.UserText)})

	reqBody := openAIRequest{Model: model, Messages: messages}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return ProviderResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ProviderResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProviderResult{}, err
	}
	defer resp.Body.Close()

	var out openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ProviderResult{}, fmt.Errorf("openai decode: %w", err)
	}
	if out.Error != nil {
		return ProviderResult{}, fmt.Errorf("openai error %s: %s", out.Error.Type, out.Error.Message)
	}
	if resp.StatusCode >= 300 {
		return ProviderResult{}, fmt.Errorf("openai returned %s", resp.Status)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return ProviderResult{}, fmt.Errorf("openai returned no content")
	}
	return ProviderResult{Text: out.Choices[0].Message.Content}, nil
}

// NewProviderForModel constructs a Provider for the given model identifier.
//   - "" / "echo"                      → EchoProvider
//   - "http" / "http-default"          → HTTPProvider from env vars
//   - "anthropic" / "claude-*"         → AnthropicProvider (requires ANTHROPIC_API_KEY)
//   - "openai" / "gpt-*" / "o1-*" / "o3-*" → OpenAIChatProvider (requires OPENAI_API_KEY)
func NewProviderForModel(model string) (Provider, error) {
	norm := strings.ToLower(strings.TrimSpace(model))
	switch {
	case norm == "" || norm == "echo":
		return EchoProvider{}, nil
	case norm == "http" || norm == "http-default":
		url := strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_URL"))
		if url == "" {
			return nil, fmt.Errorf("SWARMSTR_AGENT_HTTP_URL is required for http model")
		}
		return &HTTPProvider{URL: url, APIKey: strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_API_KEY"))}, nil
	case norm == "anthropic" || strings.HasPrefix(norm, "claude-"):
		return &AnthropicProvider{Model: strings.TrimSpace(model)}, nil
	case norm == "openai" || strings.HasPrefix(norm, "gpt-") || strings.HasPrefix(norm, "o1-") || strings.HasPrefix(norm, "o3-") || strings.HasPrefix(norm, "o4-"):
		return &OpenAIChatProvider{Model: strings.TrimSpace(model)}, nil
	default:
		return nil, fmt.Errorf("unsupported model %q: use \"echo\", \"http\", \"anthropic\", \"openai\", or a named model like \"claude-3-5-sonnet-20241022\" or \"gpt-4o\"", model)
	}
}

// BuildRuntimeForModel constructs a Runtime for the given model identifier.
// tools may be nil (tool calls will error gracefully).
func BuildRuntimeForModel(model string, tools ToolExecutor) (Runtime, error) {
	p, err := NewProviderForModel(model)
	if err != nil {
		return nil, err
	}
	return NewProviderRuntime(p, tools)
}

// ProviderOverride carries explicit credentials from the providers config section.
// Either BaseURL or APIKey (or both) can override the env-based defaults.
type ProviderOverride struct {
	BaseURL string
	APIKey  string
	Model   string
}

// BuildRuntimeWithOverride constructs a Runtime using explicit provider credentials
// from the providers[] config section, falling back to env vars when fields are empty.
// This enables OpenClaw-compatible provider configuration via config file.
func BuildRuntimeWithOverride(model string, override ProviderOverride, tools ToolExecutor) (Runtime, error) {
	// If no override is specified, delegate to the standard env-based builder.
	if override.BaseURL == "" && override.APIKey == "" {
		return BuildRuntimeForModel(model, tools)
	}
	// Resolve effective model: prefer override.Model, then the passed model arg.
	effectiveModel := strings.TrimSpace(override.Model)
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(model)
	}
	norm := strings.ToLower(effectiveModel)

	// When the model is Anthropic-class and an API key override is provided,
	// use AnthropicProvider with the overridden key.
	if norm == "anthropic" || strings.HasPrefix(norm, "claude-") {
		apiKey := strings.TrimSpace(override.APIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
		}
		return NewProviderRuntime(&AnthropicProvider{Model: effectiveModel, APIKey: apiKey}, tools)
	}

	// When the model is OpenAI-class and a base URL / API key override is provided,
	// use OpenAIChatProvider.
	if norm == "openai" || strings.HasPrefix(norm, "gpt-") || strings.HasPrefix(norm, "o1-") || strings.HasPrefix(norm, "o3-") || strings.HasPrefix(norm, "o4-") {
		apiKey := strings.TrimSpace(override.APIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		}
		return NewProviderRuntime(&OpenAIChatProvider{
			BaseURL: strings.TrimSpace(override.BaseURL),
			APIKey:  apiKey,
			Model:   effectiveModel,
		}, tools)
	}

	// Generic HTTP-compatible endpoint (Ollama, custom servers, etc.)
	baseURL := strings.TrimSpace(override.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_URL"))
	}
	if baseURL == "" {
		return nil, fmt.Errorf("provider base_url is required (set in providers config or SWARMSTR_AGENT_HTTP_URL)")
	}
	apiKey := strings.TrimSpace(override.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_API_KEY"))
	}
	return NewProviderRuntime(&HTTPProvider{URL: baseURL, APIKey: apiKey}, tools)
}
