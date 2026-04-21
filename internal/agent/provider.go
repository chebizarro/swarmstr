package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

func init() {
	getEnvFn = os.Getenv
}

type Provider interface {
	Generate(context.Context, Turn) (ProviderResult, error)
}

type ProviderResult struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// Usage reports token consumption, populated by providers that support it.
	Usage ProviderUsage `json:"usage,omitempty"`
	// HistoryDelta carries the ordered tool-call/tool-result history from
	// an agentic loop.  Propagated to TurnResult.HistoryDelta.
	HistoryDelta []ConversationMessage `json:"-"`
	// Outcome and StopReason classify the terminal shape of the turn.
	Outcome    TurnOutcome    `json:"outcome,omitempty"`
	StopReason TurnStopReason `json:"stop_reason,omitempty"`
}

// ProviderUsage holds token counts from the provider API response.
type ProviderUsage struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
	// CacheReadTokens is the number of input tokens served from the provider's
	// prompt cache. Non-zero means a cache hit on some/all of the prompt prefix.
	// Anthropic: usage.cache_read_input_tokens
	// OpenAI:    usage.prompt_tokens_details.cached_tokens
	// Gemini:    usageMetadata.cachedContentTokenCount
	CacheReadTokens int64 `json:"cache_read_tokens,omitempty"`
	// CacheCreationTokens is the number of input tokens written to cache.
	// Only reported by Anthropic (usage.cache_creation_input_tokens).
	// Zero for providers that don't report this separately.
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
}

// StreamingProvider extends Provider with incremental text delivery.
//
// Stream generates a response for turn, calling onChunk for each text token (or
// small token group) as it arrives from the LLM.  When generation completes the
// returned ProviderResult holds the full accumulated Text and any ToolCalls that
// were requested by the model.  Callers should not rely on Text in ProviderResult
// from onChunk — use only onChunk for incremental delivery.
type StreamingProvider interface {
	Provider
	Stream(ctx context.Context, turn Turn, onChunk func(text string)) (ProviderResult, error)
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
	contextText := buildPromptAssembly("", turn.StaticSystemPrompt, turn.Context).Combined()
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
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("METIQ_AGENT_PROVIDER")))
	switch mode {
	case "", "echo":
		// Fall through to key-based auto-detect: if ANTHROPIC_API_KEY is set,
		// use Anthropic as the default provider instead of the echo stub.
		if apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); apiKey != "" {
			model := strings.TrimSpace(os.Getenv("METIQ_AGENT_MODEL"))
			if model == "" {
				model = "claude-sonnet-4-5"
			}
			return &AnthropicProvider{Model: model, APIKey: apiKey}, nil
		}
		return EchoProvider{}, nil
	case "anthropic":
		apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
		if apiKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is required when METIQ_AGENT_PROVIDER=anthropic")
		}
		model := strings.TrimSpace(os.Getenv("METIQ_AGENT_MODEL"))
		if model == "" {
			model = "claude-sonnet-4-5"
		}
		return &AnthropicProvider{Model: model, APIKey: apiKey}, nil
	case "http":
		url := strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_URL"))
		if url == "" {
			return nil, fmt.Errorf("METIQ_AGENT_HTTP_URL is required when METIQ_AGENT_PROVIDER=http")
		}
		return &HTTPProvider{URL: url, APIKey: strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_API_KEY"))}, nil
	default:
		return nil, fmt.Errorf("unknown METIQ_AGENT_PROVIDER %q", mode)
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
	APIKey       string
	Model        string
	SystemPrompt string
	Client       *http.Client
}

func (p *AnthropicProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	return generateWithAgenticLoop(ctx, p.chatProvider(), turn, p.SystemPrompt, "anthropic")
}

// isAnthropicAuthError reports whether err indicates an authentication failure.
func isAnthropicAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "401") || strings.Contains(s, "authentication_error") || strings.Contains(s, "permission_error")
}

// OpenAIChatProvider calls the OpenAI Chat Completions API (POST /v1/chat/completions).
// Also compatible with Ollama and other OpenAI-compatible endpoints.
// Set OPENAI_API_KEY (or ANTHROPIC_API_KEY is not used for this provider).
type OpenAIChatProvider struct {
	BaseURL string // defaults to https://api.openai.com
	APIKey  string
	Model   string
	Client  *http.Client
	// KeepAlive is an Ollama-specific parameter controlling how long the model
	// stays loaded in memory after a request (e.g. "30m", "1h"). Empty means
	// use the Ollama server default (typically 5m).
	KeepAlive string
	// Store enables OpenAI's stored completions feature (store:true in API).
	// Opt-in via config; stored completions are retained by OpenAI for 30 days.
	// Read from OPENAI_STORE_COMPLETIONS env var if not set explicitly.
	Store bool
}

func (p *OpenAIChatProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	keepAlive := p.KeepAlive
	if keepAlive == "" {
		keepAlive = strings.TrimSpace(os.Getenv("OLLAMA_KEEP_ALIVE"))
	}
	store := p.Store
	if !store && strings.EqualFold(strings.TrimSpace(os.Getenv("OPENAI_STORE_COMPLETIONS")), "true") {
		store = true
	}
	chatProvider := &OpenAIChatProviderChat{
		BaseURL:             p.BaseURL,
		APIKey:              p.resolveAPIKey(),
		Model:               p.resolveModel(),
		Client:              p.Client,
		ContextWindowTokens: turn.ContextWindowTokens,
		KeepAlive:           keepAlive,
		Store:               store,
	}
	return generateWithAgenticLoop(ctx, chatProvider, turn, "", "openai")
}

// resolveModel returns the effective model name.
func (p *OpenAIChatProvider) resolveModel() string {
	model := strings.TrimSpace(p.Model)
	if model == "" {
		return "gpt-4o"
	}
	return model
}

// resolveAPIKey returns the effective API key.
func (p *OpenAIChatProvider) resolveAPIKey() string {
	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	return apiKey
}

// Stream implements StreamingProvider for OpenAIChatProvider.
// It uses the openai-go SDK's streaming API to deliver text tokens incrementally
// via onChunk and accumulates any tool_call deltas for return in ProviderResult.
func (p *OpenAIChatProvider) Stream(ctx context.Context, turn Turn, onChunk func(text string)) (ProviderResult, error) {
	model := p.resolveModel()
	apiKey := p.resolveAPIKey()
	baseURL := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	} else {
		if u, err := url.Parse(baseURL); err == nil {
			host := strings.ToLower(u.Host)
			if host == "api.openai.com" && (u.Path == "" || u.Path == "/") {
				baseURL = strings.TrimRight(baseURL, "/") + "/v1"
			}
		}
	}

	clientOpts := []option.RequestOption{
		option.WithBaseURL(baseURL),
	}
	if apiKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(apiKey))
	}
	if p.Client != nil {
		clientOpts = append(clientOpts, option.WithHTTPClient(p.Client))
	}

	client := openai.NewClient(clientOpts...)

	// Build messages using the same converter as the ChatProvider.
	llmMsgs := buildLLMMessagesFromTurn(turn, "")
	sdkMsgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(llmMsgs))
	for _, m := range llmMsgs {
		switch m.Role {
		case "system":
			sdkMsgs = append(sdkMsgs, openai.SystemMessage(m.Content))
		case "user":
			sdkMsgs = append(sdkMsgs, buildOpenAISDKUserContent(m))
		case "assistant":
			sdkMsgs = append(sdkMsgs, openai.AssistantMessage(m.Content))
		case "tool":
			sdkMsgs = append(sdkMsgs, openai.ToolMessage(m.Content, m.ToolCallID))
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: sdkMsgs,
	}
	if len(turn.Tools) > 0 {
		params.Tools = translateToolsToOpenAISDK(turn.Tools)
	}

	// Ollama-specific: inject num_ctx and keep_alive via extra body params.
	var streamOpts []option.RequestOption
	if isOllamaEndpoint(baseURL) {
		if turn.ContextWindowTokens > 0 {
			streamOpts = append(streamOpts, option.WithJSONSet("options.num_ctx", turn.ContextWindowTokens))
		}
		if p.KeepAlive != "" {
			streamOpts = append(streamOpts, option.WithJSONSet("keep_alive", p.KeepAlive))
		}
	}

	stream := client.Chat.Completions.NewStreaming(ctx, params, streamOpts...)
	defer stream.Close()

	// Accumulate text and tool calls from the stream.
	type toolCallAcc struct {
		ID        string
		Name      string
		Arguments strings.Builder
	}
	toolAcc := map[int]*toolCallAcc{}
	maxToolIndex := -1
	var textBuf strings.Builder

	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			textBuf.WriteString(delta.Content)
			if onChunk != nil {
				onChunk(delta.Content)
			}
		}
		for _, tc := range delta.ToolCalls {
			idx := int(tc.Index)
			if idx > maxToolIndex {
				maxToolIndex = idx
			}
			acc, ok := toolAcc[idx]
			if !ok {
				acc = &toolCallAcc{}
				toolAcc[idx] = acc
			}
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Function.Name != "" {
				acc.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				acc.Arguments.WriteString(tc.Function.Arguments)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return ProviderResult{}, fmt.Errorf("openai stream: %w", err)
	}

	// Assemble tool calls from accumulators.
	var toolCalls []ToolCall
	for idx := 0; idx <= maxToolIndex; idx++ {
		acc, ok := toolAcc[idx]
		if !ok {
			continue
		}
		var args map[string]any
		if argStr := acc.Arguments.String(); argStr != "" {
			_ = json.Unmarshal([]byte(argStr), &args)
		}
		toolCalls = append(toolCalls, ToolCall{
			ID:   acc.ID,
			Name: acc.Name,
			Args: args,
		})
	}

	return ProviderResult{
		Text:      textBuf.String(),
		ToolCalls: toolCalls,
	}, nil
}

// ─── Google Gemini provider ───────────────────────────────────────────────────

// GoogleGeminiProvider calls the Google AI Gemini API.
// API key is read from GEMINI_API_KEY (or GOOGLE_API_KEY / GOOGLE_GENERATIVE_AI_API_KEY).
// Default model: "gemini-2.0-flash".
type GoogleGeminiProvider struct {
	Model  string
	APIKey string
	Client *http.Client
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

// geminiPart supports text, inline_data (base64 image), file_data (URL image),
// functionCall (tool request), and functionResponse (tool result).
// Fields are omitempty so the JSON output only includes the relevant shape.
type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inline_data,omitempty"`
	FileData         *geminiFileData         `json:"file_data,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

// geminiFunctionCall represents a model-requested function call.
type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

// geminiFunctionResponse carries a function execution result back to the model.
type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiInlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"` // base64
}

type geminiFileData struct {
	MimeType string `json:"mime_type,omitempty"`
	FileURI  string `json:"file_uri"`
}

type geminiRequest struct {
	SystemInstruction *geminiContent     `json:"systemInstruction,omitempty"`
	Contents          []geminiContent    `json:"contents"`
	GenerationConfig  map[string]any     `json:"generationConfig,omitempty"`
	Tools             []geminiToolBundle `json:"tools,omitempty"`
	// CachedContent references a pre-created CachedContent resource by name
	// (e.g. "cachedContents/abc123"). When set, the system instruction and
	// tools from the cached resource are used, and those fields should be
	// omitted from this request to avoid conflicts.
	CachedContent string `json:"cachedContent,omitempty"`
}

// geminiToolBundle wraps a list of function declarations for the Gemini tools field.
type geminiToolBundle struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

type geminiFuncDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text         string              `json:"text,omitempty"`
				FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata,omitempty"`
	Error         *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// geminiUsageMetadata holds token counts from the Gemini API response.
type geminiUsageMetadata struct {
	PromptTokenCount     int64 `json:"promptTokenCount"`
	CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	TotalTokenCount      int64 `json:"totalTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
}

// toolDefsToGemini converts ToolDefinition slice to the Gemini functionDeclarations format.
func toolDefsToGemini(defs []ToolDefinition) []geminiToolBundle {
	decls := make([]geminiFuncDecl, 0, len(defs))
	for _, d := range defs {
		params, _ := geminiSchemaMap(toolInputSchemaMap(d)).(map[string]any)
		decls = append(decls, geminiFuncDecl{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  params,
		})
	}
	if len(decls) == 0 {
		return nil
	}
	return []geminiToolBundle{{FunctionDeclarations: decls}}
}

func geminiSchemaMap(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if k == "type" {
				if s, ok := val.(string); ok {
					out[k] = strings.ToUpper(s)
					continue
				}
			}
			out[k] = geminiSchemaMap(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = geminiSchemaMap(val)
		}
		return out
	default:
		return v
	}
}

// buildGeminiParts constructs the parts slice for a user turn.
// When images are present they appear before the text part (Gemini multi-modal format).
func buildGeminiParts(text string, images []ImageRef) []geminiPart {
	if len(images) == 0 {
		return []geminiPart{{Text: text}}
	}
	parts := make([]geminiPart, 0, len(images)+1)
	for _, img := range images {
		if img.Base64 != "" {
			mt := img.MimeType
			if mt == "" {
				mt = "image/jpeg"
			}
			parts = append(parts, geminiPart{InlineData: &geminiInlineData{MimeType: mt, Data: img.Base64}})
		} else if img.URL != "" {
			parts = append(parts, geminiPart{FileData: &geminiFileData{MimeType: img.MimeType, FileURI: img.URL}})
		}
	}
	parts = append(parts, geminiPart{Text: text})
	return parts
}

func (p *GoogleGeminiProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	chatProvider := &GeminiChatProvider{
		APIKey: p.APIKey,
		Model:  p.Model,
		Client: p.Client,
	}
	return generateWithAgenticLoop(ctx, chatProvider, turn, "", "gemini")
}

// ─── Provider table for OpenAI-compatible endpoints ───────────────────────────

// openAICompatProviders maps model prefixes / aliases to their base URL and env key name.
// All of these use the OpenAI Chat Completions API format.
var openAICompatProviders = []struct {
	prefix  string // lowercase prefix (or exact alias) to match
	alias   string // exact alias (e.g. "groq", "mistral")
	base    string // default base URL
	envKey  string // primary env var name for the API key
	baseEnv string // optional env var that overrides base URL (for local servers)
}{
	{prefix: "grok-", alias: "xai", base: "https://api.x.ai/v1", envKey: "XAI_API_KEY"},
	{prefix: "", alias: "groq", base: "https://api.groq.com/openai/v1", envKey: "GROQ_API_KEY"},
	{prefix: "groq/", alias: "", base: "https://api.groq.com/openai/v1", envKey: "GROQ_API_KEY"},
	{prefix: "mistral-", alias: "mistral", base: "https://api.mistral.ai/v1", envKey: "MISTRAL_API_KEY"},
	{prefix: "", alias: "together", base: "https://api.together.xyz/v1", envKey: "TOGETHER_API_KEY"},
	{prefix: "together/", alias: "", base: "https://api.together.xyz/v1", envKey: "TOGETHER_API_KEY"},
	{prefix: "", alias: "openrouter", base: "https://openrouter.ai/api/v1", envKey: "OPENROUTER_API_KEY"},
	{prefix: "openrouter/", alias: "", base: "https://openrouter.ai/api/v1", envKey: "OPENROUTER_API_KEY"},
	// Ollama: local inference server with OpenAI-compatible API.
	// Base URL defaults to http://localhost:11434/v1; override with OLLAMA_BASE_URL.
	// No API key required for local Ollama (OLLAMA_API_KEY optional for remote).
	{prefix: "ollama/", alias: "ollama", base: "http://localhost:11434/v1", envKey: "OLLAMA_API_KEY", baseEnv: "OLLAMA_BASE_URL"},
	// LM Studio: OpenAI-compatible local server, typically on :1234.
	{prefix: "lmstudio/", alias: "lmstudio", base: "http://localhost:1234/v1", envKey: "", baseEnv: "LMSTUDIO_BASE_URL"},
	// Fireworks AI: fast inference for open-source models.
	{prefix: "fireworks/", alias: "fireworks", base: "https://api.fireworks.ai/inference/v1", envKey: "FIREWORKS_API_KEY"},
	// DeepInfra: affordable hosted inference.
	{prefix: "deepinfra/", alias: "deepinfra", base: "https://api.deepinfra.com/v1/openai", envKey: "DEEPINFRA_API_KEY"},
	// Perplexity: search-augmented chat.
	{prefix: "pplx-", alias: "perplexity", base: "https://api.perplexity.ai", envKey: "PERPLEXITY_API_KEY"},
}

// resolveOpenAICompat checks whether a model string matches one of the known
// OpenAI-compatible provider aliases/prefixes.  On match it returns the base URL
// and env key name; otherwise returns empty strings.
func resolveOpenAICompat(norm string) (baseURL, envKey string) {
	for _, p := range openAICompatProviders {
		matched := (p.alias != "" && norm == p.alias) ||
			(p.prefix != "" && strings.HasPrefix(norm, p.prefix))
		if !matched {
			continue
		}
		base := p.base
		// Allow base URL override via environment variable (e.g. OLLAMA_BASE_URL).
		if p.baseEnv != "" {
			if override := strings.TrimRight(strings.TrimSpace(os.Getenv(p.baseEnv)), "/"); override != "" {
				base = override
			}
		}
		return base, p.envKey
	}
	return "", ""
}

// NewProviderForModel constructs a Provider for the given model identifier.
//
//   - "" / "echo"                         → EchoProvider
//   - "http" / "http-default"             → HTTPProvider (METIQ_AGENT_HTTP_URL)
//   - "anthropic" / "claude-*"            → AnthropicProvider (ANTHROPIC_API_KEY)
//   - "openai" / "gpt-*" / "o1-*" …      → OpenAIChatProvider (OPENAI_API_KEY)
//   - "gemini" / "gemini-*"              → GoogleGeminiProvider (GEMINI_API_KEY)
//   - "grok-*" / "xai"                   → OpenAIChatProvider → api.x.ai (XAI_API_KEY)
//   - "groq" / "groq/*"                  → OpenAIChatProvider → api.groq.com (GROQ_API_KEY)
//   - "mistral" / "mistral-*"            → OpenAIChatProvider → api.mistral.ai (MISTRAL_API_KEY)
//   - "together" / "together/*"          → OpenAIChatProvider → api.together.xyz (TOGETHER_API_KEY)
//   - "openrouter" / "openrouter/*"      → OpenAIChatProvider → openrouter.ai (OPENROUTER_API_KEY)
func NewProviderForModel(model string) (Provider, error) {
	norm := strings.ToLower(strings.TrimSpace(model))
	switch {
	case norm == "" || norm == "echo":
		return EchoProvider{}, nil
	case norm == "http" || norm == "http-default":
		url := strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_URL"))
		if url == "" {
			return nil, fmt.Errorf("METIQ_AGENT_HTTP_URL is required for http model")
		}
		return &HTTPProvider{URL: url, APIKey: strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_API_KEY"))}, nil
	case norm == "github-copilot":
		// GitHub Copilot: OpenAI-compatible API with OAuth device-flow auth.
		tok, _, _ := FetchOAuthToken(context.Background(), "github-copilot")
		return &OpenAIChatProvider{
			BaseURL: "https://api.githubcopilot.com",
			APIKey:  tok,
			Model:   "gpt-4o", // default Copilot model; override via ProviderOverride.Model
		}, nil
	case norm == "copilot-cli" || strings.HasPrefix(norm, "copilot-cli/"):
		// GitHub Copilot CLI SDK: connects to a local Copilot CLI server via JSON-RPC.
		// Model can be specified as "copilot-cli/gpt-4.1" or set via ProviderOverride.
		cliModel := "gpt-4.1"
		if strings.HasPrefix(norm, "copilot-cli/") {
			cliModel = strings.TrimPrefix(norm, "copilot-cli/")
		}
		return &CopilotCLIProvider{
			ChatProvider: &CopilotCLIChatProvider{
				Model:  cliModel,
				CLIURL: strings.TrimSpace(os.Getenv("COPILOT_CLI_URL")),
			},
		}, nil
	case norm == "anthropic" || strings.HasPrefix(norm, "claude-"):
		return &AnthropicProvider{Model: strings.TrimSpace(model)}, nil
	case norm == "openai" || strings.HasPrefix(norm, "gpt-") || strings.HasPrefix(norm, "o1-") || strings.HasPrefix(norm, "o3-") || strings.HasPrefix(norm, "o4-"):
		return &OpenAIChatProvider{Model: strings.TrimSpace(model)}, nil
	case norm == "gemini" || strings.HasPrefix(norm, "gemini-"):
		return &GoogleGeminiProvider{Model: strings.TrimSpace(model)}, nil
	case norm == "cohere" || strings.HasPrefix(norm, "command-"):
		return &CohereProvider{Model: strings.TrimSpace(model)}, nil
	}
	// OpenAI-compatible providers by prefix/alias.
	if baseURL, envKey := resolveOpenAICompat(norm); baseURL != "" {
		apiKey := ""
		if envKey != "" {
			apiKey = strings.TrimSpace(os.Getenv(envKey))
		}
		return &OpenAIChatProvider{
			BaseURL: baseURL,
			APIKey:  apiKey,
			Model:   strings.TrimSpace(model),
		}, nil
	}
	// Provide a targeted hint for local model files (.gguf, .bin, etc.).
	if strings.HasSuffix(norm, ".gguf") || strings.HasSuffix(norm, ".bin") {
		return nil, fmt.Errorf("unsupported model %q — local model files require a provider config entry with base_url (e.g. ollama/%s or add [providers.my-local] with base_url)", model, model)
	}
	return nil, fmt.Errorf("unsupported model %q — try: echo, claude-*, gpt-*, gemini-*, grok-*, groq/*, mistral-*, together/*, openrouter/*, cohere/command-*, ollama/*, lmstudio/*, or http; for custom servers add a provider with base_url", model)
}

// ─── Cohere provider ──────────────────────────────────────────────────────────

// CohereProvider implements Provider using the Cohere Chat API v2.
// Docs: https://docs.cohere.com/reference/chat
// Reads COHERE_API_KEY from the environment at call time.
type CohereProvider struct {
	Model  string
	APIKey string
}

type cohereMessage struct {
	Role    string `json:"role"`    // "user" | "assistant"
	Content string `json:"content"` // plain text
}

type cohereRequest struct {
	Model    string          `json:"model"`
	Messages []cohereMessage `json:"messages"`
	Preamble string          `json:"preamble,omitempty"` // system prompt equivalent
	Stream   bool            `json:"stream"`
}

type cohereResponse struct {
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *CohereProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("COHERE_API_KEY"))
	}
	if apiKey == "" {
		return ProviderResult{}, fmt.Errorf("COHERE_API_KEY is not set")
	}
	model := strings.TrimSpace(p.Model)
	if model == "" || model == "cohere" {
		model = "command-r-plus"
	}

	req := cohereRequest{
		Model:  model,
		Stream: false,
		Messages: []cohereMessage{
			{Role: "user", Content: strings.TrimSpace(turn.UserText)},
		},
	}
	if preamble := buildPromptAssembly("", turn.StaticSystemPrompt, turn.Context).Combined(); preamble != "" {
		req.Preamble = preamble
	}

	body, err := json.Marshal(req)
	if err != nil {
		return ProviderResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.cohere.com/v2/chat", bytes.NewReader(body))
	if err != nil {
		return ProviderResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return ProviderResult{}, fmt.Errorf("call Cohere API: %w", err)
	}
	defer resp.Body.Close()

	var cohereResp cohereResponse
	if err := json.NewDecoder(resp.Body).Decode(&cohereResp); err != nil {
		return ProviderResult{}, fmt.Errorf("decode Cohere response: %w", err)
	}
	if cohereResp.Error != nil {
		return ProviderResult{}, fmt.Errorf("Cohere API error: %s", cohereResp.Error.Message)
	}
	if resp.StatusCode >= 300 {
		return ProviderResult{}, fmt.Errorf("Cohere API HTTP %d", resp.StatusCode)
	}

	var sb strings.Builder
	for _, block := range cohereResp.Message.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return ProviderResult{}, fmt.Errorf("Cohere API returned empty response")
	}
	return ProviderResult{Text: text}, nil
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
	BaseURL      string
	APIKey       string
	Model        string
	SystemPrompt string // injected as system context for every turn
}

// BuildProviderWithOverride constructs a Provider using explicit provider
// credentials from the providers[] config section, falling back to env vars
// when fields are empty.
func BuildProviderWithOverride(model string, override ProviderOverride) (Provider, error) {
	// If no override is specified, delegate to the standard env-based builder.
	if override.BaseURL == "" && override.APIKey == "" {
		return NewProviderForModel(model)
	}
	// GitHub Copilot OAuth: detect via base URL or model prefix.
	effectiveModel := strings.TrimSpace(model)
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(override.Model)
	}
	normModel := strings.ToLower(effectiveModel)
	isGHCopilot := strings.Contains(strings.ToLower(override.BaseURL), "githubcopilot.com") ||
		normModel == "github-copilot"
	if isGHCopilot && strings.TrimSpace(override.APIKey) == "" {
		if tok, found, tokErr := FetchOAuthToken(context.Background(), "github-copilot"); found && tokErr == nil {
			override.APIKey = tok
		}
		if override.BaseURL == "" {
			override.BaseURL = "https://api.githubcopilot.com"
		}
	}
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(override.Model)
	}
	norm := strings.ToLower(effectiveModel)

	// Anthropic (claude-* models).
	if norm == "anthropic" || strings.HasPrefix(norm, "claude-") {
		apiKey := strings.TrimSpace(override.APIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
		}
		return &AnthropicProvider{Model: effectiveModel, APIKey: apiKey, SystemPrompt: override.SystemPrompt}, nil
	}

	// Google Gemini.
	if norm == "gemini" || strings.HasPrefix(norm, "gemini-") {
		apiKey := strings.TrimSpace(override.APIKey)
		if apiKey == "" {
			for _, k := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"} {
				if v := strings.TrimSpace(os.Getenv(k)); v != "" {
					apiKey = v
					break
				}
			}
		}
		return &GoogleGeminiProvider{Model: effectiveModel, APIKey: apiKey}, nil
	}

	// OpenAI and OpenAI-compatible providers.
	// If an explicit base URL is provided in the override, use it; otherwise
	// look up the default base URL from the model prefix table.
	overrideBaseURL := strings.TrimSpace(override.BaseURL)
	overrideAPIKey := strings.TrimSpace(override.APIKey)

	isOpenAIClass := norm == "openai" || strings.HasPrefix(norm, "gpt-") ||
		strings.HasPrefix(norm, "o1-") || strings.HasPrefix(norm, "o3-") || strings.HasPrefix(norm, "o4-")

	compatBase, compatEnvKey := resolveOpenAICompat(norm)
	if isOpenAIClass || overrideBaseURL != "" || compatBase != "" {
		baseURL := overrideBaseURL
		apiKey := overrideAPIKey
		if baseURL == "" {
			baseURL = compatBase
		}
		if apiKey == "" {
			envKey := "OPENAI_API_KEY"
			if compatEnvKey != "" {
				envKey = compatEnvKey
			}
			apiKey = strings.TrimSpace(os.Getenv(envKey))
		}
		return &OpenAIChatProvider{
			BaseURL: baseURL,
			APIKey:  apiKey,
			Model:   effectiveModel,
		}, nil
	}

	// Generic HTTP-compatible endpoint (Ollama, custom servers, etc.)
	baseURL := overrideBaseURL
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_URL"))
	}
	if baseURL == "" {
		return nil, fmt.Errorf("provider base_url is required (set in providers config or METIQ_AGENT_HTTP_URL)")
	}
	apiKey := overrideAPIKey
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_API_KEY"))
	}
	return &HTTPProvider{URL: baseURL, APIKey: apiKey}, nil
}

// BuildRuntimeWithOverride constructs a Runtime using explicit provider credentials
// from the providers[] config section, falling back to env vars when fields are empty.
// This enables OpenClaw-compatible provider configuration via config file.
func BuildRuntimeWithOverride(model string, override ProviderOverride, tools ToolExecutor) (Runtime, error) {
	provider, err := BuildProviderWithOverride(model, override)
	if err != nil {
		return nil, err
	}
	return NewProviderRuntime(provider, tools)
}
