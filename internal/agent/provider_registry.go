package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// AuthMethod describes how a provider obtains credentials.
type AuthMethod string

const (
	AuthMethodNone   AuthMethod = "none"
	AuthMethodAPIKey AuthMethod = "api_key"
	AuthMethodOAuth  AuthMethod = "oauth"
	AuthMethodAWS    AuthMethod = "aws_credentials"
)

// ProviderCapabilities advertises provider features used by runtime selection
// and future plugin-backed provider discovery.
type ProviderCapabilities struct {
	SupportsTools         bool    `json:"supports_tools,omitempty"`
	SupportsStreaming     bool    `json:"supports_streaming,omitempty"`
	SupportsVision        bool    `json:"supports_vision,omitempty"`
	SupportsPromptCaching bool    `json:"supports_prompt_caching,omitempty"`
	SupportsThinking      bool    `json:"supports_thinking,omitempty"`
	ContextWindowTokens   int     `json:"context_window_tokens,omitempty"`
	CostPer1KInput        float64 `json:"cost_per_1k_input,omitempty"`
	CostPer1KOutput       float64 `json:"cost_per_1k_output,omitempty"`
	InputModalities       string  `json:"input_modalities,omitempty"`
	OutputModalities      string  `json:"output_modalities,omitempty"`
	Transport             string  `json:"transport,omitempty"`
	TokenAccounting       string  `json:"token_accounting,omitempty"`
}

// ModelInfo describes a model exposed by a provider catalog.
type ModelInfo struct {
	ID                  string               `json:"id"`
	Name                string               `json:"name,omitempty"`
	ProviderID          string               `json:"provider_id,omitempty"`
	ContextWindowTokens int                  `json:"context_window_tokens,omitempty"`
	Capabilities        ProviderCapabilities `json:"capabilities,omitempty"`
	Metadata            map[string]any       `json:"metadata,omitempty"`
}

// ToolSchemaNormalizer rewrites provider-facing tool schemas for provider
// compatibility without mutating the caller's definitions.
type ToolSchemaNormalizer func([]ToolDefinition) []ToolDefinition

// ModelCatalogFunc fetches a provider's current model catalog.
type ModelCatalogFunc func(ctx context.Context) ([]ModelInfo, error)

// ProviderPrepareRequestFunc mutates an outbound provider HTTP request before
// it is sent. It is intended for provider-specific headers, beta flags, service
// tiers, and other transport policy that should not be hard-coded in callers.
type ProviderPrepareRequestFunc func(req *http.Request) error

// ProviderTransportWrapper wraps the RoundTripper used for provider HTTP calls.
// Providers can use it for tracing, custom auth transports, proxy policy, or
// provider-specific retry/accounting layers.
type ProviderTransportWrapper func(base http.RoundTripper) http.RoundTripper

// ProviderFactory constructs a Provider for a matched model and optional
// explicit config override.
type ProviderFactory func(model string, override ProviderOverride) (Provider, error)

// ProviderDescriptor describes an inference provider plugin/adapter.
type ProviderDescriptor struct {
	ID                      string
	Name                    string
	Aliases                 []string
	Prefixes                []string
	BaseURL                 string
	BaseURLEnv              string
	APIKeyEnv               string
	AuthMethods             []AuthMethod
	Capabilities            ProviderCapabilities
	NormalizeToolSchema     ToolSchemaNormalizer
	PrepareRequest          ProviderPrepareRequestFunc
	WrapTransport           ProviderTransportWrapper
	ListModels              ModelCatalogFunc
	TokenAccountantForModel func(model string) TokenAccountant
	Factory                 ProviderFactory
}

func (d ProviderDescriptor) normalizedID() string { return strings.ToLower(strings.TrimSpace(d.ID)) }

func (d ProviderDescriptor) matches(normModel string) bool {
	for _, alias := range d.Aliases {
		if normModel == strings.ToLower(strings.TrimSpace(alias)) {
			return true
		}
	}
	for _, prefix := range d.Prefixes {
		if p := strings.ToLower(strings.TrimSpace(prefix)); p != "" && strings.HasPrefix(normModel, p) {
			return true
		}
	}
	return false
}

func (d ProviderDescriptor) resolvedBaseURL() string {
	base := strings.TrimRight(strings.TrimSpace(d.BaseURL), "/")
	if d.BaseURLEnv != "" {
		if override := strings.TrimRight(strings.TrimSpace(os.Getenv(d.BaseURLEnv)), "/"); override != "" {
			base = override
		}
	}
	return base
}

// HTTPClient returns base decorated with descriptor transport hooks. A nil
// result means the caller can use its SDK/default client unchanged.
func (d ProviderDescriptor) HTTPClient(base *http.Client) *http.Client {
	if d.PrepareRequest == nil && d.WrapTransport == nil {
		return base
	}
	client := &http.Client{}
	if base != nil {
		*client = *base
	}
	rt := client.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	if d.WrapTransport != nil {
		rt = d.WrapTransport(rt)
		if rt == nil {
			rt = http.DefaultTransport
		}
	}
	if d.PrepareRequest != nil {
		rt = providerPrepareRoundTripper{base: rt, prepare: d.PrepareRequest}
	}
	client.Transport = rt
	return client
}

type providerPrepareRoundTripper struct {
	base    http.RoundTripper
	prepare ProviderPrepareRequestFunc
}

func (t providerPrepareRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.prepare != nil {
		if err := t.prepare(req); err != nil {
			return nil, err
		}
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// ProviderRegistry is a goroutine-safe registry of inference provider descriptors.
type ProviderRegistry struct {
	mu          sync.RWMutex
	descriptors map[string]ProviderDescriptor
	order       []string
	modelCache  map[string]modelCatalogCacheEntry
}

const modelCatalogTTL = 5 * time.Minute

type modelCatalogCacheEntry struct {
	models    []ModelInfo
	expiresAt time.Time
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{descriptors: make(map[string]ProviderDescriptor), modelCache: make(map[string]modelCatalogCacheEntry)}
}

func (r *ProviderRegistry) Register(desc ProviderDescriptor) error {
	id := desc.normalizedID()
	if id == "" {
		return fmt.Errorf("provider descriptor ID is required")
	}
	if desc.Factory == nil {
		return fmt.Errorf("provider descriptor %q requires a factory", id)
	}
	desc.ID = id
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.descriptors[id]; !exists {
		r.order = append(r.order, id)
	}
	r.descriptors[id] = desc
	return nil
}

func (r *ProviderRegistry) Descriptor(id string) (ProviderDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	desc, ok := r.descriptors[strings.ToLower(strings.TrimSpace(id))]
	return desc, ok
}

func (r *ProviderRegistry) Descriptors() []ProviderDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProviderDescriptor, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.descriptors[id])
	}
	return out
}

func (r *ProviderRegistry) Match(model string) (ProviderDescriptor, bool) {
	norm := strings.ToLower(strings.TrimSpace(model))
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, id := range r.order {
		desc := r.descriptors[id]
		if desc.matches(norm) {
			return desc, true
		}
	}
	return ProviderDescriptor{}, false
}

func (r *ProviderRegistry) ModelInfo(model string) (ModelInfo, bool) {
	if row, ok := resolveCatalogModelRef(model); ok {
		caps := row.Capabilities
		if caps.ContextWindowTokens == 0 {
			caps.ContextWindowTokens = row.ContextWindowTokens
		}
		return ModelInfo{ID: row.ID, Name: row.Name, ProviderID: row.ProviderID, ContextWindowTokens: row.ContextWindowTokens, Capabilities: caps, Metadata: map[string]any{"aliases": append([]string(nil), row.Aliases...), "compatibility": row.Compatibility}}, true
	}
	desc, ok := r.Match(model)
	if !ok {
		return ModelInfo{}, false
	}
	_, modelID := normalizeModelRef(model)
	return ModelInfo{ID: modelID, ProviderID: desc.ID, ContextWindowTokens: desc.Capabilities.ContextWindowTokens, Capabilities: desc.Capabilities}, true
}

func (r *ProviderRegistry) CapabilitiesForModel(model string) (ProviderCapabilities, bool) {
	info, ok := r.ModelInfo(model)
	return info.Capabilities, ok
}

func (r *ProviderRegistry) TokenAccountant(model string) (TokenAccountant, bool) {
	desc, ok := r.Match(model)
	if !ok || desc.TokenAccountantForModel == nil {
		return nil, false
	}
	accountant := desc.TokenAccountantForModel(model)
	return accountant, accountant != nil
}

func (r *ProviderRegistry) Build(model string, override ProviderOverride) (Provider, bool, error) {
	desc, ok := r.Match(model)
	if !ok {
		return nil, false, nil
	}
	provider, err := desc.Factory(model, override)
	return provider, true, err
}

// ListModels returns a provider's model catalog, using a short in-memory cache.
func (r *ProviderRegistry) ListModels(ctx context.Context, providerID string) ([]ModelInfo, error) {
	id := strings.ToLower(strings.TrimSpace(providerID))
	if id == "" {
		return nil, fmt.Errorf("provider id is required")
	}
	r.mu.RLock()
	cached, cachedOK := r.modelCache[id]
	desc, ok := r.descriptors[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", providerID)
	}
	if cachedOK && time.Now().Before(cached.expiresAt) {
		return cloneModelInfoSlice(cached.models), nil
	}
	if desc.ListModels == nil {
		return nil, fmt.Errorf("provider %q does not support dynamic model listing", providerID)
	}
	models, err := desc.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	for i := range models {
		if models[i].ProviderID == "" {
			models[i].ProviderID = desc.ID
		}
		if models[i].Capabilities == (ProviderCapabilities{}) {
			models[i].Capabilities = desc.Capabilities
		}
	}
	r.mu.Lock()
	r.modelCache[id] = modelCatalogCacheEntry{models: cloneModelInfoSlice(models), expiresAt: time.Now().Add(modelCatalogTTL)}
	r.mu.Unlock()
	return cloneModelInfoSlice(models), nil
}

var defaultProviderRegistry = newDefaultProviderRegistry()

func DefaultProviderRegistry() *ProviderRegistry { return defaultProviderRegistry }

func newDefaultProviderRegistry() *ProviderRegistry {
	reg := NewProviderRegistry()
	for _, desc := range builtinProviderDescriptors() {
		if err := reg.Register(desc); err != nil {
			panic(err)
		}
	}
	return reg
}

func builtinProviderDescriptors() []ProviderDescriptor {
	// Prices are USD per 1K tokens. They intentionally live in capabilities so
	// routing and budgeting code can reason about cost without a separate catalog.
	openAIGPT4oCaps := ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsPromptCaching: true, SupportsThinking: true, ContextWindowTokens: 128000, CostPer1KInput: 0.0025, CostPer1KOutput: 0.0100}
	anthropicClaudeCaps := ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsPromptCaching: true, SupportsThinking: true, ContextWindowTokens: 200000, CostPer1KInput: 0.0030, CostPer1KOutput: 0.0150}
	geminiFlashCaps := ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsPromptCaching: true, SupportsThinking: true, ContextWindowTokens: 1000000, CostPer1KInput: 0.0003, CostPer1KOutput: 0.0025}
	openAICompatCaps := ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsPromptCaching: true, SupportsThinking: true, InputModalities: "text,image", OutputModalities: "text", Transport: "openai-chat-sse", TokenAccounting: "provider_usage"}
	mistralCaps := ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, ContextWindowTokens: 128000}
	responsesCaps := ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsThinking: true, ContextWindowTokens: 1047576, InputModalities: "text,image", OutputModalities: "text", Transport: "responses-sse", TokenAccounting: "provider_usage"}
	vertexCaps := ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsThinking: true, ContextWindowTokens: 1000000}
	bedrockCaps := ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsVision: false, SupportsPromptCaching: false, SupportsThinking: true, ContextWindowTokens: 200000}
	anthropicVertexCaps := ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsVision: false, SupportsPromptCaching: false, SupportsThinking: true, ContextWindowTokens: 200000}

	mkOpenAICompat := func(id, name string, aliases, prefixes []string, baseURL, apiKeyEnv, baseURLEnv string) ProviderDescriptor {
		desc := ProviderDescriptor{
			ID: id, Name: name, Aliases: aliases, Prefixes: prefixes, BaseURL: baseURL, APIKeyEnv: apiKeyEnv, BaseURLEnv: baseURLEnv,
			AuthMethods: []AuthMethod{AuthMethodAPIKey}, Capabilities: openAICompatCaps,
		}
		desc.Factory = func(model string, override ProviderOverride) (Provider, error) {
			return buildOpenAICompatibleProvider(model, override, desc)
		}
		switch id {
		case "ollama":
			desc.ListModels = func(ctx context.Context) ([]ModelInfo, error) {
				return listOllamaModels(ctx, desc.resolvedBaseURL(), desc.Capabilities, desc.HTTPClient(nil))
			}
		case "lmstudio":
			desc.ListModels = func(ctx context.Context) ([]ModelInfo, error) {
				return listOpenAICompatibleModels(ctx, desc.resolvedBaseURL(), desc.ID, desc.Capabilities, desc.HTTPClient(nil))
			}
		case "xai":
			desc.NormalizeToolSchema = NormalizeStrictOpenAIToolSchema
		case "openrouter":
			desc.PrepareRequest = openRouterPrepareRequest
		case "deepseek", "zai", "qwen", "cerebras", "cohere", "vercel-ai-gateway":
			desc.ListModels = func(context.Context) ([]ModelInfo, error) {
				return catalogRowsForProvider(desc.ID, desc.Capabilities), nil
			}
		}
		if desc.ListModels == nil {
			desc.ListModels = func(ctx context.Context) ([]ModelInfo, error) {
				return listAuthenticatedOpenAICompatibleModels(ctx, desc)
			}
		}
		return desc
	}

	openaiDesc := ProviderDescriptor{
		ID: "openai", Name: "OpenAI", Aliases: []string{"openai"}, Prefixes: []string{"gpt-", "o1-", "o3-", "o4-"}, BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY",
		AuthMethods: []AuthMethod{AuthMethodAPIKey}, Capabilities: openAIGPT4oCaps,
	}
	openaiDesc.Factory = func(model string, override ProviderOverride) (Provider, error) {
		return buildOpenAICompatibleProvider(model, override, openaiDesc)
	}

	anthropicDesc := ProviderDescriptor{
		ID: "anthropic", Name: "Anthropic", Aliases: []string{"anthropic"}, Prefixes: []string{"claude-"}, APIKeyEnv: "ANTHROPIC_API_KEY",
		AuthMethods: []AuthMethod{AuthMethodAPIKey, AuthMethodOAuth}, Capabilities: anthropicClaudeCaps,
	}
	anthropicDesc.Factory = func(model string, override ProviderOverride) (Provider, error) {
		credential, err := requireProviderCredential("Anthropic", model, strings.TrimSpace(override.APIKey), "ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN")
		if err != nil {
			return nil, err
		}
		profile, err := resolvePromptCacheProfileValue(PromptCacheProviderAnthropic, override.PromptCache)
		if err != nil {
			return nil, err
		}
		return &AnthropicProvider{Model: strings.TrimSpace(model), APIKey: credential, PromptCache: promptCacheProfilePtr(profile), Client: anthropicDesc.HTTPClient(nil)}, nil
	}

	geminiDesc := ProviderDescriptor{
		ID: "gemini", Name: "Google Gemini", Aliases: []string{"gemini"}, Prefixes: []string{"gemini-"}, APIKeyEnv: "GEMINI_API_KEY",
		AuthMethods: []AuthMethod{AuthMethodAPIKey}, Capabilities: geminiFlashCaps, NormalizeToolSchema: NormalizeGeminiToolSchema,
	}
	geminiDesc.Factory = func(model string, override ProviderOverride) (Provider, error) {
		credential, err := requireProviderCredential("Gemini", model, strings.TrimSpace(override.APIKey), "GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY")
		if err != nil {
			return nil, err
		}
		profile, err := resolvePromptCacheProfileValue(PromptCacheProviderGemini, override.PromptCache)
		if err != nil {
			return nil, err
		}
		return &GoogleGeminiProvider{Model: strings.TrimSpace(model), APIKey: credential, PromptCache: promptCacheProfilePtr(profile), Client: geminiDesc.HTTPClient(nil)}, nil
	}

	mistralDesc := ProviderDescriptor{ID: "mistral", Name: "Mistral", Aliases: []string{"mistral"}, Prefixes: []string{"mistral-"}, BaseURL: "https://api.mistral.ai/v1", APIKeyEnv: "MISTRAL_API_KEY", AuthMethods: []AuthMethod{AuthMethodAPIKey}, Capabilities: mistralCaps, ListModels: func(context.Context) ([]ModelInfo, error) { return catalogRowsForProvider("mistral", mistralCaps), nil }}
	mistralDesc.Factory = func(model string, override ProviderOverride) (Provider, error) {
		baseURL := strings.TrimSpace(override.BaseURL)
		if baseURL == "" {
			baseURL = mistralDesc.resolvedBaseURL()
		}
		credential, err := requireProviderCredential("Mistral", model, strings.TrimSpace(override.APIKey), "MISTRAL_API_KEY")
		if err != nil {
			return nil, err
		}
		return &MistralChatProvider{BaseURL: baseURL, APIKey: credential, Model: strings.TrimSpace(model), Client: mistralDesc.HTTPClient(nil)}, nil
	}

	moonshotDesc := mkOpenAICompat("moonshot", "Moonshot/Kimi", []string{"moonshot", "kimi", "kimicode"}, []string{"moonshot/", "kimi/", "kimicode/", "kimi-"}, "https://api.moonshot.ai/v1", "MOONSHOT_API_KEY", "")
	moonshotDesc.APIKeyEnv = "MOONSHOT_API_KEY"
	moonshotDesc.PrepareRequest = moonshotPrepareRequest
	moonshotDesc.ListModels = func(context.Context) ([]ModelInfo, error) {
		return catalogRowsForProvider("moonshot", moonshotDesc.Capabilities), nil
	}
	moonshotDesc.Factory = func(model string, override ProviderOverride) (Provider, error) {
		return buildOpenAICompatibleProvider(normalizeKimiModelID(model), override, moonshotDesc)
	}

	openAIResponsesDesc := ProviderDescriptor{ID: "openai-responses", Name: "OpenAI Responses", Aliases: []string{"openai-responses", "responses"}, Prefixes: []string{"responses/"}, BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY", AuthMethods: []AuthMethod{AuthMethodAPIKey}, Capabilities: responsesCaps, ListModels: func(context.Context) ([]ModelInfo, error) {
		return catalogRowsForProvider("openai-responses", responsesCaps), nil
	}}
	openAIResponsesDesc.Factory = func(model string, override ProviderOverride) (Provider, error) {
		baseURL := strings.TrimSpace(override.BaseURL)
		if baseURL == "" {
			baseURL = openAIResponsesDesc.resolvedBaseURL()
		}
		credential, err := requireProviderCredential("OpenAI Responses", model, strings.TrimSpace(override.APIKey), "OPENAI_API_KEY")
		if err != nil {
			return nil, err
		}
		return &OpenAIResponsesProvider{BaseURL: baseURL, APIKey: credential, Model: strings.TrimPrefix(strings.TrimSpace(model), "responses/"), Client: openAIResponsesDesc.HTTPClient(nil)}, nil
	}

	azureResponsesDesc := ProviderDescriptor{ID: "azure-responses", Name: "Azure OpenAI Responses", Aliases: []string{"azure-responses", "azure"}, Prefixes: []string{"azure/"}, BaseURLEnv: "AZURE_OPENAI_ENDPOINT", APIKeyEnv: "AZURE_OPENAI_API_KEY", AuthMethods: []AuthMethod{AuthMethodAPIKey}, Capabilities: responsesCaps, ListModels: func(context.Context) ([]ModelInfo, error) {
		return catalogRowsForProvider("azure-responses", responsesCaps), nil
	}}
	azureResponsesDesc.Factory = func(model string, override ProviderOverride) (Provider, error) {
		baseURL := strings.TrimSpace(override.BaseURL)
		if baseURL == "" {
			baseURL = azureResponsesDesc.resolvedBaseURL()
		}
		credential, err := requireProviderCredential("Azure OpenAI Responses", model, strings.TrimSpace(override.APIKey), "AZURE_OPENAI_API_KEY")
		if err != nil {
			return nil, err
		}
		return &AzureResponsesProvider{BaseURL: baseURL, APIKey: credential, Model: strings.TrimPrefix(strings.TrimSpace(model), "azure/"), Client: azureResponsesDesc.HTTPClient(nil)}, nil
	}

	vertexDesc := ProviderDescriptor{ID: "vertex", Name: "Google Vertex AI", Aliases: []string{"vertex", "google-vertex"}, Prefixes: []string{"vertex/"}, BaseURL: "https://aiplatform.googleapis.com/v1", BaseURLEnv: "VERTEX_BASE_URL", APIKeyEnv: "GOOGLE_APPLICATION_CREDENTIALS", AuthMethods: []AuthMethod{AuthMethodAPIKey, AuthMethodOAuth}, Capabilities: vertexCaps, NormalizeToolSchema: NormalizeGeminiToolSchema, ListModels: func(context.Context) ([]ModelInfo, error) { return catalogRowsForProvider("vertex", vertexCaps), nil }}
	vertexDesc.Factory = func(model string, override ProviderOverride) (Provider, error) {
		baseURL := strings.TrimSpace(override.BaseURL)
		if baseURL == "" {
			baseURL = vertexDesc.resolvedBaseURL()
		}
		credential, err := requireProviderCredential("Vertex", model, strings.TrimSpace(override.APIKey), "VERTEX_ACCESS_TOKEN", "GOOGLE_ACCESS_TOKEN")
		if err != nil {
			return nil, err
		}
		return &VertexChatProvider{BaseURL: baseURL, APIKey: credential, Model: normalizeVertexModel(model), Client: vertexDesc.HTTPClient(nil)}, nil
	}

	bedrockDesc := ProviderDescriptor{ID: "bedrock", Name: "Amazon Bedrock", Aliases: []string{"bedrock", "amazon-bedrock"}, Prefixes: []string{"bedrock/"}, BaseURLEnv: "AWS_BEDROCK_ENDPOINT", AuthMethods: []AuthMethod{AuthMethodAWS}, Capabilities: bedrockCaps}
	bedrockDesc.Factory = func(model string, override ProviderOverride) (Provider, error) {
		baseURL := strings.TrimSpace(override.BaseURL)
		if baseURL == "" {
			baseURL = bedrockDesc.resolvedBaseURL()
		}
		return &BedrockProvider{Model: model, Region: strings.TrimSpace(os.Getenv("AWS_REGION")), Profile: strings.TrimSpace(os.Getenv("AWS_PROFILE")), BaseURL: baseURL}, nil
	}

	anthropicVertexDesc := ProviderDescriptor{ID: "anthropic-vertex", Name: "Anthropic on Vertex AI", Aliases: []string{"anthropic-vertex", "vertex-claude"}, Prefixes: []string{"anthropic-vertex/", "vertex-claude/"}, BaseURLEnv: "ANTHROPIC_VERTEX_BASE_URL", APIKeyEnv: "VERTEX_ACCESS_TOKEN", AuthMethods: []AuthMethod{AuthMethodOAuth}, Capabilities: anthropicVertexCaps}
	anthropicVertexDesc.Factory = func(model string, override ProviderOverride) (Provider, error) {
		credential := strings.TrimSpace(override.APIKey)
		if credential == "" {
			credential, _ = firstEnv("VERTEX_ACCESS_TOKEN", "GOOGLE_ACCESS_TOKEN")
		}
		baseURL := strings.TrimSpace(override.BaseURL)
		if baseURL == "" {
			baseURL = anthropicVertexDesc.resolvedBaseURL()
		}
		project := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT"))
		return &AnthropicVertexProvider{Model: model, ProjectID: project, Region: strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_LOCATION")), AccessToken: credential, BaseURL: baseURL}, nil
	}

	return []ProviderDescriptor{
		openaiDesc,
		anthropicDesc,
		geminiDesc,
		mistralDesc,
		moonshotDesc,
		openAIResponsesDesc,
		azureResponsesDesc,
		vertexDesc,
		bedrockDesc,
		anthropicVertexDesc,
		mkOpenAICompat("xai", "xAI", []string{"xai"}, []string{"grok-"}, "https://api.x.ai/v1", "XAI_API_KEY", ""),
		mkOpenAICompat("groq", "Groq", []string{"groq"}, []string{"groq/"}, "https://api.groq.com/openai/v1", "GROQ_API_KEY", ""),
		mkOpenAICompat("minimax", "Minimax", []string{"minimax"}, []string{"minimax/"}, "https://api.minimax.io/v1", "MINIMAX_API_KEY", ""),
		mkOpenAICompat("minimax-cn", "Minimax CN", []string{"minimax-cn", "minimax_cn"}, []string{"minimax-cn/"}, "https://api.minimaxi.com/v1", "MINIMAX_CN_API_KEY", ""),
		mkOpenAICompat("together", "Together AI", []string{"together"}, []string{"together/"}, "https://api.together.xyz/v1", "TOGETHER_API_KEY", ""),
		mkOpenAICompat("openrouter", "OpenRouter", []string{"openrouter"}, []string{"openrouter/"}, "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", ""),
		mkOpenAICompat("ollama", "Ollama", []string{"ollama"}, []string{"ollama/"}, "http://localhost:11434/v1", "OLLAMA_API_KEY", "OLLAMA_BASE_URL"),
		mkOpenAICompat("lmstudio", "LM Studio", []string{"lmstudio"}, []string{"lmstudio/"}, "http://localhost:1234/v1", "", "LMSTUDIO_BASE_URL"),
		mkOpenAICompat("fireworks", "Fireworks AI", []string{"fireworks"}, []string{"fireworks/"}, "https://api.fireworks.ai/inference/v1", "FIREWORKS_API_KEY", ""),
		mkOpenAICompat("deepinfra", "DeepInfra", []string{"deepinfra"}, []string{"deepinfra/"}, "https://api.deepinfra.com/v1/openai", "DEEPINFRA_API_KEY", ""),
		mkOpenAICompat("perplexity", "Perplexity", []string{"perplexity"}, []string{"pplx-"}, "https://api.perplexity.ai", "PERPLEXITY_API_KEY", ""),
		mkOpenAICompat("deepseek", "DeepSeek", []string{"deepseek"}, []string{"deepseek/", "deepseek-"}, "https://api.deepseek.com", "DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL"),
		mkOpenAICompat("zai", "Z.AI", []string{"zai", "z.ai", "glm"}, []string{"zai/", "z.ai/", "glm/", "glm-"}, "https://api.z.ai/api/paas/v4", "ZAI_API_KEY", "ZAI_BASE_URL"),
		mkOpenAICompat("qwen", "Qwen", []string{"qwen"}, []string{"qwen/", "qwen-"}, "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", "QWEN_API_KEY", "QWEN_BASE_URL"),
		mkOpenAICompat("cerebras", "Cerebras", []string{"cerebras"}, []string{"cerebras/"}, "https://api.cerebras.ai/v1", "CEREBRAS_API_KEY", "CEREBRAS_BASE_URL"),
		mkOpenAICompat("cohere", "Cohere", []string{"cohere"}, []string{"cohere/", "command-"}, "https://api.cohere.ai/compatibility/v1", "COHERE_API_KEY", "COHERE_BASE_URL"),
		mkOpenAICompat("vercel-ai-gateway", "Vercel AI Gateway", []string{"vercel-ai-gateway", "vercel"}, []string{"vercel-ai-gateway/", "vercel/", "ai-gateway/"}, "https://ai-gateway.vercel.sh/v1", "AI_GATEWAY_API_KEY", "AI_GATEWAY_BASE_URL"),
	}
}

func buildOpenAICompatibleProvider(model string, override ProviderOverride, desc ProviderDescriptor) (Provider, error) {
	baseURL := strings.TrimSpace(override.BaseURL)
	if baseURL == "" {
		baseURL = desc.resolvedBaseURL()
	}
	effectiveModel := strings.TrimSpace(model)
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(override.Model)
	}
	effectiveModel = normalizeProviderModelID(effectiveModel, desc)
	apiKey, err := requireOpenAICompatibleCredential(desc.Name, effectiveModel, strings.TrimSpace(override.APIKey), desc.APIKeyEnv, baseURL)
	if err != nil {
		return nil, err
	}
	profile, err := resolvePromptCacheProfileValue(PromptCacheProviderOpenAICompatible, override.PromptCache)
	if err != nil {
		return nil, err
	}
	return &OpenAIChatProvider{BaseURL: baseURL, APIKey: apiKey, Model: effectiveModel, Client: desc.HTTPClient(nil), PromptCache: promptCacheProfilePtr(profile), ToolSchemaNormalizer: desc.NormalizeToolSchema}, nil
}

func normalizeProviderModelID(model string, desc ProviderDescriptor) string {
	trimmed := strings.TrimSpace(model)
	lower := strings.ToLower(trimmed)
	for _, prefix := range desc.Prefixes {
		prefix = strings.TrimSpace(prefix)
		if strings.HasSuffix(prefix, "/") && strings.HasPrefix(lower, strings.ToLower(prefix)) {
			return strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	return trimmed
}

func openRouterPrepareRequest(req *http.Request) error {
	referrer := strings.TrimSpace(os.Getenv("OPENROUTER_HTTP_REFERER"))
	if referrer == "" {
		referrer = "https://github.com/chebizarro/swarmstr"
	}
	title := strings.TrimSpace(os.Getenv("OPENROUTER_APP_TITLE"))
	if title == "" {
		title = "swarmstr"
	}
	req.Header.Set("HTTP-Referer", referrer)
	req.Header.Set("X-Title", title)
	return nil
}

func listAuthenticatedOpenAICompatibleModels(ctx context.Context, desc ProviderDescriptor) ([]ModelInfo, error) {
	apiKey := strings.TrimSpace(os.Getenv(desc.APIKeyEnv))
	if apiKey == "" && !isLocalBaseURL(desc.resolvedBaseURL()) {
		return nil, fmt.Errorf("%s is required for %s model discovery", desc.APIKeyEnv, desc.Name)
	}
	prepare := desc.PrepareRequest
	client := &http.Client{}
	client.Transport = providerPrepareRoundTripper{base: http.DefaultTransport, prepare: func(req *http.Request) error {
		if prepare != nil {
			if err := prepare(req); err != nil {
				return err
			}
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		return nil
	}}
	return listOpenAICompatibleModels(ctx, desc.resolvedBaseURL(), desc.ID, desc.Capabilities, client)
}

func resolveOpenAICompat(norm string) (baseURL, envKey string) {
	desc, ok := DefaultProviderRegistry().Match(norm)
	if !ok {
		return "", ""
	}
	return desc.resolvedBaseURL(), desc.APIKeyEnv
}

func registeredProviderHint() string {
	descs := DefaultProviderRegistry().Descriptors()
	parts := make([]string, 0, len(descs))
	for _, desc := range descs {
		if len(desc.Prefixes) > 0 {
			parts = append(parts, desc.Prefixes[0]+"*")
		} else if len(desc.Aliases) > 0 {
			parts = append(parts, desc.Aliases[0])
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func cloneModelInfoSlice(src []ModelInfo) []ModelInfo {
	if len(src) == 0 {
		return nil
	}
	out := make([]ModelInfo, len(src))
	copy(out, src)
	for i := range out {
		out[i].Metadata = cloneJSONMap(out[i].Metadata)
	}
	return out
}

func listOllamaModels(ctx context.Context, baseURL string, caps ProviderCapabilities, client *http.Client) ([]ModelInfo, error) {
	root := strings.TrimRight(strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1"), "/")
	if root == "" {
		root = "http://localhost:11434"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama model catalog returned %s", resp.Status)
	}
	var body struct {
		Models []struct {
			Name       string         `json:"name"`
			Model      string         `json:"model"`
			ModifiedAt string         `json:"modified_at"`
			Size       int64          `json:"size"`
			Details    map[string]any `json:"details"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(body.Models))
	for _, m := range body.Models {
		id := strings.TrimSpace(m.Model)
		if id == "" {
			id = strings.TrimSpace(m.Name)
		}
		if id == "" {
			continue
		}
		models = append(models, ModelInfo{ID: id, Name: m.Name, ProviderID: "ollama", Capabilities: caps, Metadata: map[string]any{"modified_at": m.ModifiedAt, "size": m.Size, "details": m.Details}})
	}
	return models, nil
}

func listOpenAICompatibleModels(ctx context.Context, baseURL, providerID string, caps ProviderCapabilities, client *http.Client) ([]ModelInfo, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("%s base URL is not configured", providerID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s model catalog returned %s", providerID, resp.Status)
	}
	var body struct {
		Data []struct {
			ID      string         `json:"id"`
			Object  string         `json:"object"`
			OwnedBy string         `json:"owned_by"`
			Meta    map[string]any `json:"metadata"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(body.Data))
	for _, m := range body.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		meta := cloneJSONMap(m.Meta)
		if meta == nil {
			meta = map[string]any{}
		}
		if m.Object != "" {
			meta["object"] = m.Object
		}
		if m.OwnedBy != "" {
			meta["owned_by"] = m.OwnedBy
		}
		models = append(models, ModelInfo{ID: id, ProviderID: providerID, Capabilities: caps, Metadata: meta})
	}
	return models, nil
}
