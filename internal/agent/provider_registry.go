package agent

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// AuthMethod describes how a provider obtains credentials.
type AuthMethod string

const (
	AuthMethodNone   AuthMethod = "none"
	AuthMethodAPIKey AuthMethod = "api_key"
	AuthMethodOAuth  AuthMethod = "oauth"
)

// ProviderCapabilities advertises provider features used by runtime selection
// and future plugin-backed provider discovery.
type ProviderCapabilities struct {
	SupportsTools         bool
	SupportsStreaming     bool
	SupportsVision        bool
	SupportsPromptCaching bool
	SupportsThinking      bool
}

// ProviderFactory constructs a Provider for a matched model and optional
// explicit config override.
type ProviderFactory func(model string, override ProviderOverride) (Provider, error)

// ProviderDescriptor describes an inference provider plugin/adapter.
type ProviderDescriptor struct {
	ID           string
	Name         string
	Aliases      []string
	Prefixes     []string
	BaseURL      string
	BaseURLEnv   string
	APIKeyEnv    string
	AuthMethods  []AuthMethod
	Capabilities ProviderCapabilities
	Factory      ProviderFactory
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

// ProviderRegistry is a goroutine-safe registry of inference provider descriptors.
type ProviderRegistry struct {
	mu          sync.RWMutex
	descriptors map[string]ProviderDescriptor
	order       []string
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{descriptors: make(map[string]ProviderDescriptor)}
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

func (r *ProviderRegistry) Build(model string, override ProviderOverride) (Provider, bool, error) {
	desc, ok := r.Match(model)
	if !ok {
		return nil, false, nil
	}
	provider, err := desc.Factory(model, override)
	return provider, true, err
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
	openAICompatCaps := ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsPromptCaching: true, SupportsThinking: true}
	mkOpenAICompat := func(id, name string, aliases, prefixes []string, baseURL, apiKeyEnv, baseURLEnv string) ProviderDescriptor {
		return ProviderDescriptor{
			ID: id, Name: name, Aliases: aliases, Prefixes: prefixes, BaseURL: baseURL, APIKeyEnv: apiKeyEnv, BaseURLEnv: baseURLEnv,
			AuthMethods: []AuthMethod{AuthMethodAPIKey}, Capabilities: openAICompatCaps,
			Factory: func(model string, override ProviderOverride) (Provider, error) {
				return buildOpenAICompatibleProvider(model, override, ProviderDescriptor{
					ID: id, Name: name, BaseURL: baseURL, APIKeyEnv: apiKeyEnv, BaseURLEnv: baseURLEnv,
				})
			},
		}
	}
	return []ProviderDescriptor{
		mkOpenAICompat("xai", "xAI", []string{"xai"}, []string{"grok-"}, "https://api.x.ai/v1", "XAI_API_KEY", ""),
		mkOpenAICompat("groq", "Groq", []string{"groq"}, []string{"groq/"}, "https://api.groq.com/openai/v1", "GROQ_API_KEY", ""),
		mkOpenAICompat("mistral", "Mistral", []string{"mistral"}, []string{"mistral-"}, "https://api.mistral.ai/v1", "MISTRAL_API_KEY", ""),
		mkOpenAICompat("together", "Together AI", []string{"together"}, []string{"together/"}, "https://api.together.xyz/v1", "TOGETHER_API_KEY", ""),
		mkOpenAICompat("openrouter", "OpenRouter", []string{"openrouter"}, []string{"openrouter/"}, "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", ""),
		mkOpenAICompat("ollama", "Ollama", []string{"ollama"}, []string{"ollama/"}, "http://localhost:11434/v1", "OLLAMA_API_KEY", "OLLAMA_BASE_URL"),
		mkOpenAICompat("lmstudio", "LM Studio", []string{"lmstudio"}, []string{"lmstudio/"}, "http://localhost:1234/v1", "", "LMSTUDIO_BASE_URL"),
		mkOpenAICompat("fireworks", "Fireworks AI", []string{"fireworks"}, []string{"fireworks/"}, "https://api.fireworks.ai/inference/v1", "FIREWORKS_API_KEY", ""),
		mkOpenAICompat("deepinfra", "DeepInfra", []string{"deepinfra"}, []string{"deepinfra/"}, "https://api.deepinfra.com/v1/openai", "DEEPINFRA_API_KEY", ""),
		mkOpenAICompat("perplexity", "Perplexity", []string{"perplexity"}, []string{"pplx-"}, "https://api.perplexity.ai", "PERPLEXITY_API_KEY", ""),
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
	apiKey, err := requireOpenAICompatibleCredential(desc.Name, effectiveModel, strings.TrimSpace(override.APIKey), desc.APIKeyEnv, baseURL)
	if err != nil {
		return nil, err
	}
	profile, err := resolvePromptCacheProfileValue(PromptCacheProviderOpenAICompatible, override.PromptCache)
	if err != nil {
		return nil, err
	}
	return &OpenAIChatProvider{BaseURL: baseURL, APIKey: apiKey, Model: effectiveModel, PromptCache: promptCacheProfilePtr(profile)}, nil
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
