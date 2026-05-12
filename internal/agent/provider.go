package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"metiq/internal/store/state"
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

// IsEchoRuntime reports whether rt is a ProviderRuntime backed by EchoProvider.
// Used after config loading to detect the startup stub and auto-promote a real
// agent as the default runtime.
func IsEchoRuntime(rt Runtime) bool {
	pr, ok := rt.(*ProviderRuntime)
	if !ok {
		return false
	}
	_, echo := pr.provider.(EchoProvider)
	return echo
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

func boolEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func echoProviderAllowed() bool {
	return boolEnv("METIQ_AGENT_ALLOW_ECHO")
}

func requireEchoProviderOptIn() error {
	if echoProviderAllowed() {
		return nil
	}
	return fmt.Errorf("METIQ_AGENT_PROVIDER=echo requires METIQ_AGENT_ALLOW_ECHO=true for explicit dev/test echo opt-in")
}

func firstEnv(keys ...string) (value, key string) {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v, k
		}
	}
	return "", ""
}

func requireProviderCredential(providerName, model string, apiKey string, envKeys ...string) (string, error) {
	if key := strings.TrimSpace(apiKey); key != "" {
		return key, nil
	}
	if key, _ := firstEnv(envKeys...); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("%s credential is required for hosted model %q (set %s or configure provider api_key)", providerName, model, strings.Join(envKeys, " or "))
}

func isLocalBaseURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		u, err = url.Parse("http://" + raw)
		if err != nil {
			return false
		}
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

func requireOpenAICompatibleCredential(providerName, model string, apiKey string, envKey string, baseURL string) (string, error) {
	if key := strings.TrimSpace(apiKey); key != "" {
		return key, nil
	}
	if envKey != "" {
		apiKey = strings.TrimSpace(os.Getenv(envKey))
	}
	if apiKey != "" || isLocalBaseURL(baseURL) {
		return apiKey, nil
	}
	if envKey != "" {
		return "", fmt.Errorf("%s API key is required for hosted model %q (set %s or configure provider api_key)", providerName, model, envKey)
	}
	return "", fmt.Errorf("%s API key is required for non-local base_url %q (configure provider api_key or use a local endpoint)", providerName, baseURL)
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
		apiKey, err := requireProviderCredential("Anthropic", strings.TrimSpace(os.Getenv("METIQ_AGENT_MODEL")), "", "ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN")
		if err != nil {
			return nil, fmt.Errorf("%w when METIQ_AGENT_PROVIDER=anthropic", err)
		}
		model := strings.TrimSpace(os.Getenv("METIQ_AGENT_MODEL"))
		if model == "" {
			model = "claude-sonnet-4-5"
		}
		log.Printf("agent provider: using Anthropic provider model=%q", model)
		return &AnthropicProvider{Model: model, APIKey: apiKey}, nil
	case "http":
		providerURL := strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_URL"))
		if providerURL == "" {
			return nil, fmt.Errorf("METIQ_AGENT_HTTP_URL is required when METIQ_AGENT_PROVIDER=http")
		}
		apiKey := strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_API_KEY"))
		if apiKey == "" && !isLocalBaseURL(providerURL) {
			return nil, fmt.Errorf("METIQ_AGENT_HTTP_API_KEY is required for non-local METIQ_AGENT_HTTP_URL %q", providerURL)
		}
		log.Printf("agent provider: using HTTP provider url=%q", providerURL)
		return &HTTPProvider{URL: providerURL, APIKey: apiKey}, nil
	default:
		return nil, fmt.Errorf("unknown METIQ_AGENT_PROVIDER %q", mode)
	}
}

// AnthropicProvider calls the Anthropic Messages API (POST /v1/messages).
// Set ANTHROPIC_API_KEY or ANTHROPIC_OAUTH_TOKEN in the environment, or use
// ProviderOverride.APIKey.
type AnthropicProvider struct {
	APIKey       string
	Model        string
	SystemPrompt string
	Client       *http.Client
	PromptCache  *PromptCacheProfile
}

func (p *AnthropicProvider) PromptCacheProfile() PromptCacheProfile {
	return promptCacheProfileOrDefault(p.PromptCache, PromptCacheProviderAnthropic)
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
	BaseURL     string // defaults to https://api.openai.com
	APIKey      string
	Model       string
	Client      *http.Client
	PromptCache *PromptCacheProfile
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
		PromptCache:         promptCacheProfilePtr(p.PromptCacheProfile()),
	}
	return generateWithAgenticLoop(ctx, chatProvider, turn, "", "openai")
}

func (p *OpenAIChatProvider) PromptCacheProfile() PromptCacheProfile {
	return promptCacheProfileOrDefault(p.PromptCache, PromptCacheProviderOpenAICompatible)
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

	profile := p.PromptCacheProfile()
	// Build messages using the same converter as the ChatProvider.
	llmMsgs := buildLLMMessagesFromTurnWithProfile(turn, "", profile)
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

	// Provider-specific extra body params. llama-server accepts cache_prompt;
	// vLLM prefix caching is layout-only and sends no extra request field.
	var streamOpts []option.RequestOption
	if profile.Enabled && profile.SendLlamaCachePrompt {
		streamOpts = append(streamOpts, option.WithJSONSet("cache_prompt", true))
	}

	// Ollama-specific: inject num_ctx and keep_alive via extra body params.
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
	Model       string
	APIKey      string
	Client      *http.Client
	PromptCache *PromptCacheProfile
}

func (p *GoogleGeminiProvider) PromptCacheProfile() PromptCacheProfile {
	return promptCacheProfileOrDefault(p.PromptCache, PromptCacheProviderGemini)
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
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
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
		APIKey:      p.APIKey,
		Model:       p.Model,
		Client:      p.Client,
		PromptCache: promptCacheProfilePtr(p.PromptCacheProfile()),
	}
	return generateWithAgenticLoop(ctx, chatProvider, turn, "", "gemini")
}

// OpenAI-compatible provider descriptors live in provider_registry.go.
// NewProviderForModel constructs a Provider for the given model identifier.
//
//   - "echo"                              → EchoProvider (explicit dev/test construction)
//   - "http" / "http-default"             → HTTPProvider (METIQ_AGENT_HTTP_URL)
//   - "anthropic" / "claude-*"            → AnthropicProvider (ANTHROPIC_API_KEY or ANTHROPIC_OAUTH_TOKEN)
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
	case norm == "":
		return nil, fmt.Errorf("model is required; refusing to default to EchoProvider implicitly (use explicit model %q only for dev/test)", "echo")
	case norm == "echo":
		return EchoProvider{}, nil
	case norm == "http" || norm == "http-default":
		providerURL := strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_URL"))
		if providerURL == "" {
			return nil, fmt.Errorf("METIQ_AGENT_HTTP_URL is required for http model")
		}
		apiKey := strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_API_KEY"))
		if apiKey == "" && !isLocalBaseURL(providerURL) {
			return nil, fmt.Errorf("METIQ_AGENT_HTTP_API_KEY is required for non-local http model URL %q", providerURL)
		}
		return &HTTPProvider{URL: providerURL, APIKey: apiKey}, nil
	case norm == "github-copilot":
		// GitHub Copilot: OpenAI-compatible API with OAuth device-flow auth.
		tok, found, tokErr := FetchOAuthToken(context.Background(), "github-copilot")
		if tokErr != nil {
			return nil, fmt.Errorf("github-copilot OAuth token: %w", tokErr)
		}
		if strings.TrimSpace(tok) == "" {
			if found {
				return nil, fmt.Errorf("github-copilot OAuth token fetcher returned an empty token")
			}
			return nil, fmt.Errorf("github-copilot OAuth token is required")
		}
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
		apiKey, err := requireProviderCredential("Anthropic", strings.TrimSpace(model), "", "ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN")
		if err != nil {
			return nil, err
		}
		return &AnthropicProvider{Model: strings.TrimSpace(model), APIKey: apiKey}, nil
	case norm == "openai" || strings.HasPrefix(norm, "gpt-") || strings.HasPrefix(norm, "o1-") || strings.HasPrefix(norm, "o3-") || strings.HasPrefix(norm, "o4-"):
		apiKey, err := requireOpenAICompatibleCredential("OpenAI", strings.TrimSpace(model), "", "OPENAI_API_KEY", "https://api.openai.com/v1")
		if err != nil {
			return nil, err
		}
		return &OpenAIChatProvider{Model: strings.TrimSpace(model), APIKey: apiKey}, nil
	case norm == "gemini" || strings.HasPrefix(norm, "gemini-"):
		apiKey, err := requireProviderCredential("Gemini", strings.TrimSpace(model), "", "GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY")
		if err != nil {
			return nil, err
		}
		return &GoogleGeminiProvider{Model: strings.TrimSpace(model), APIKey: apiKey}, nil
	case norm == "cohere" || strings.HasPrefix(norm, "command-"):
		apiKey, err := requireProviderCredential("Cohere", strings.TrimSpace(model), "", "COHERE_API_KEY")
		if err != nil {
			return nil, err
		}
		return &CohereProvider{Model: strings.TrimSpace(model), APIKey: apiKey}, nil
	}
	// OpenAI-compatible providers by registry descriptor prefix/alias.
	if provider, matched, err := DefaultProviderRegistry().Build(model, ProviderOverride{}); matched {
		return provider, err
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
	PromptCache  *state.ProviderPromptCacheConfig
}

func resolvePromptCacheProfileValue(providerClass PromptCacheProviderClass, cfg *state.ProviderPromptCacheConfig) (PromptCacheProfile, error) {
	return ResolvePromptCacheProfile(providerClass, cfg)
}

// BuildProviderWithOverride constructs a Provider using explicit provider
// credentials from the providers[] config section, falling back to env vars
// when fields are empty.
func BuildProviderWithOverride(model string, override ProviderOverride) (Provider, error) {
	overrideBaseURL := strings.TrimSpace(override.BaseURL)
	overrideAPIKey := strings.TrimSpace(override.APIKey)

	// If no override is specified, delegate to the standard env-based builder.
	if overrideBaseURL == "" && overrideAPIKey == "" && strings.TrimSpace(override.Model) == "" && strings.TrimSpace(override.SystemPrompt) == "" && override.PromptCache == nil {
		return NewProviderForModel(model)
	}

	effectiveModel := strings.TrimSpace(model)
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(override.Model)
	}
	norm := strings.ToLower(effectiveModel)

	if norm == "echo" {
		if _, err := ResolvePromptCacheProfile(PromptCacheProviderUnsupported, override.PromptCache); err != nil {
			return nil, err
		}
		return EchoProvider{}, nil
	}

	// GitHub Copilot OAuth: detect via base URL or model prefix.
	isGHCopilot := strings.Contains(strings.ToLower(overrideBaseURL), "githubcopilot.com") || norm == "github-copilot"
	if isGHCopilot {
		apiKey := overrideAPIKey
		if apiKey == "" {
			tok, found, tokErr := FetchOAuthToken(context.Background(), "github-copilot")
			if tokErr != nil {
				return nil, fmt.Errorf("github-copilot OAuth token: %w", tokErr)
			}
			if found {
				apiKey = strings.TrimSpace(tok)
			}
		}
		if apiKey == "" {
			return nil, fmt.Errorf("github-copilot OAuth token is required for hosted model %q", effectiveModel)
		}
		baseURL := overrideBaseURL
		if baseURL == "" {
			baseURL = "https://api.githubcopilot.com"
		}
		profile, err := resolvePromptCacheProfileValue(PromptCacheProviderOpenAICompatible, override.PromptCache)
		if err != nil {
			return nil, err
		}
		return &OpenAIChatProvider{BaseURL: baseURL, APIKey: apiKey, Model: effectiveModel, PromptCache: promptCacheProfilePtr(profile)}, nil
	}

	// Anthropic (claude-* models).
	if norm == "anthropic" || strings.HasPrefix(norm, "claude-") {
		apiKey, err := requireProviderCredential("Anthropic", effectiveModel, overrideAPIKey, "ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN")
		if err != nil {
			return nil, err
		}
		profile, err := resolvePromptCacheProfileValue(PromptCacheProviderAnthropic, override.PromptCache)
		if err != nil {
			return nil, err
		}
		return &AnthropicProvider{Model: effectiveModel, APIKey: apiKey, SystemPrompt: override.SystemPrompt, PromptCache: promptCacheProfilePtr(profile)}, nil
	}

	// Google Gemini.
	if norm == "gemini" || strings.HasPrefix(norm, "gemini-") {
		apiKey, err := requireProviderCredential("Gemini", effectiveModel, overrideAPIKey, "GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY")
		if err != nil {
			return nil, err
		}
		profile, err := resolvePromptCacheProfileValue(PromptCacheProviderGemini, override.PromptCache)
		if err != nil {
			return nil, err
		}
		return &GoogleGeminiProvider{Model: effectiveModel, APIKey: apiKey, PromptCache: promptCacheProfilePtr(profile)}, nil
	}

	// Cohere.
	if norm == "cohere" || strings.HasPrefix(norm, "command-") {
		apiKey, err := requireProviderCredential("Cohere", effectiveModel, overrideAPIKey, "COHERE_API_KEY")
		if err != nil {
			return nil, err
		}
		if _, err := ResolvePromptCacheProfile(PromptCacheProviderUnsupported, override.PromptCache); err != nil {
			return nil, err
		}
		return &CohereProvider{Model: effectiveModel, APIKey: apiKey}, nil
	}

	// OpenAI and OpenAI-compatible providers.
	// If an explicit base URL is provided in the override, use it; otherwise
	// look up the default base URL from the model prefix table.
	isOpenAIClass := norm == "openai" || strings.HasPrefix(norm, "gpt-") ||
		strings.HasPrefix(norm, "o1-") || strings.HasPrefix(norm, "o3-") || strings.HasPrefix(norm, "o4-")

	compatDesc, compatMatched := DefaultProviderRegistry().Match(norm)
	if isOpenAIClass || compatMatched {
		baseURL := overrideBaseURL
		if baseURL == "" {
			if compatMatched {
				baseURL = compatDesc.resolvedBaseURL()
			} else {
				baseURL = "https://api.openai.com/v1"
			}
		}
		envKey := "OPENAI_API_KEY"
		providerName := "OpenAI"
		if compatMatched {
			envKey = compatDesc.APIKeyEnv
			providerName = compatDesc.Name
		}
		apiKey, err := requireOpenAICompatibleCredential(providerName, effectiveModel, overrideAPIKey, envKey, baseURL)
		if err != nil {
			return nil, err
		}
		profile, err := resolvePromptCacheProfileValue(PromptCacheProviderOpenAICompatible, override.PromptCache)
		if err != nil {
			return nil, err
		}
		return &OpenAIChatProvider{
			BaseURL:     baseURL,
			APIKey:      apiKey,
			Model:       effectiveModel,
			PromptCache: promptCacheProfilePtr(profile),
		}, nil
	}

	// An explicit OpenAI-compatible base URL with no recognized hosted model is a
	// custom endpoint.  Permit credentialless use only for local endpoints.
	if overrideBaseURL != "" {
		apiKey, err := requireOpenAICompatibleCredential("custom OpenAI-compatible provider", effectiveModel, overrideAPIKey, "", overrideBaseURL)
		if err != nil {
			return nil, err
		}
		profile, err := resolvePromptCacheProfileValue(PromptCacheProviderOpenAICompatible, override.PromptCache)
		if err != nil {
			return nil, err
		}
		return &OpenAIChatProvider{BaseURL: overrideBaseURL, APIKey: apiKey, Model: effectiveModel, PromptCache: promptCacheProfilePtr(profile)}, nil
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
	if apiKey == "" && !isLocalBaseURL(baseURL) {
		return nil, fmt.Errorf("provider api_key is required for non-local provider base_url %q", baseURL)
	}
	if _, err := ResolvePromptCacheProfile(PromptCacheProviderUnsupported, override.PromptCache); err != nil {
		return nil, err
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
