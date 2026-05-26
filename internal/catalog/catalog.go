package catalog

import (
	"os"
	"sort"
	"strings"
)

// Model describes one model available through a provider.
type Model struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Provider      string             `json:"provider"`
	ContextWindow int                `json:"context_window,omitempty"`
	Reasoning     bool               `json:"reasoning,omitempty"`
	Configured    bool               `json:"configured,omitempty"`
	Capabilities  []string           `json:"capabilities,omitempty"`
	Modalities    []string           `json:"modalities,omitempty"`
	Cost          map[string]float64 `json:"cost,omitempty"`
	Source        string             `json:"source,omitempty"`
}

// ProviderConfig is the minimal provider config needed to add configured
// provider models without depending on the daemon's state package.
type ProviderConfig struct {
	Enabled bool
	Model   string
}

// Registry stores models and provider indexes for discovery/filtering.
type Registry struct {
	models    map[string]Model
	providers map[string][]string
}

func NewRegistry() *Registry {
	return &Registry{models: map[string]Model{}, providers: map[string][]string{}}
}

func (r *Registry) Register(models ...Model) {
	if r.models == nil {
		r.models = map[string]Model{}
	}
	if r.providers == nil {
		r.providers = map[string][]string{}
	}
	for _, m := range models {
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" {
			continue
		}
		if m.Provider == "" {
			m.Provider = providerFromID(m.ID)
		}
		if m.Source == "" {
			m.Source = "core"
		}
		m.Capabilities = uniqueStrings(m.Capabilities)
		m.Modalities = uniqueStrings(m.Modalities)
		r.models[m.ID] = m
	}
	r.reindex()
}

func (r *Registry) reindex() {
	r.providers = map[string][]string{}
	for id, m := range r.models {
		p := strings.TrimSpace(m.Provider)
		if p == "" {
			p = providerFromID(id)
		}
		r.providers[p] = append(r.providers[p], id)
	}
	for p := range r.providers {
		sort.Strings(r.providers[p])
	}
}

func (r *Registry) Get(id string) (Model, bool) { m, ok := r.models[id]; return m, ok }

func (r *Registry) List() []Model {
	out := make([]Model, 0, len(r.models))
	for _, m := range r.models {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Providers() []string {
	out := make([]string, 0, len(r.providers))
	for p := range r.providers {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) ByProvider(provider string) []Model {
	ids := r.providers[provider]
	out := make([]Model, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.models[id])
	}
	return out
}

// Filter constrains model search/list operations.
type Filter struct {
	Provider   string
	Capability string
	Modality   string
	Configured *bool
	MinContext int
	Query      string
	Reasoning  *bool
}

func (r *Registry) Search(f Filter) []Model {
	query := strings.ToLower(strings.TrimSpace(f.Query))
	out := []Model{}
	for _, m := range r.List() {
		if f.Provider != "" && m.Provider != f.Provider {
			continue
		}
		if f.Capability != "" && !containsString(m.Capabilities, f.Capability) {
			continue
		}
		if f.Modality != "" && !containsString(m.Modalities, f.Modality) {
			continue
		}
		if f.Configured != nil && m.Configured != *f.Configured {
			continue
		}
		if f.Reasoning != nil && m.Reasoning != *f.Reasoning {
			continue
		}
		if f.MinContext > 0 && m.ContextWindow < f.MinContext {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(m.ID+" "+m.Name+" "+m.Provider), query) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (r *Registry) WithCapability(cap string) []Model { return r.Search(Filter{Capability: cap}) }

func BuiltinModels() []Model {
	return []Model{
		{ID: "echo", Name: "Echo (built-in)", Provider: "echo", ContextWindow: 8192, Capabilities: []string{"chat"}, Modalities: []string{"text"}, Source: "core"},
		{ID: "claude-sonnet-4-20250514", Name: "Anthropic Claude", Provider: "anthropic", ContextWindow: 200000, Reasoning: true, Capabilities: []string{"chat", "tools", "vision", "reasoning"}, Modalities: []string{"text", "image"}, Source: "core"},
		{ID: "gpt-4o", Name: "OpenAI GPT-4o", Provider: "openai", ContextWindow: 128000, Reasoning: true, Capabilities: []string{"chat", "tools", "vision", "reasoning"}, Modalities: []string{"text", "image"}, Source: "core"},
		{ID: "gemini-2.5-pro", Name: "Google Gemini", Provider: "gemini", ContextWindow: 1000000, Reasoning: true, Capabilities: []string{"chat", "tools", "vision", "reasoning"}, Modalities: []string{"text", "image"}, Source: "core"},
		{ID: "grok-3", Name: "xAI Grok", Provider: "xai", ContextWindow: 131072, Reasoning: true, Capabilities: []string{"chat", "tools", "reasoning"}, Modalities: []string{"text"}, Source: "core"},
		{ID: "command-r-plus", Name: "Cohere Command", Provider: "cohere", ContextWindow: 128000, Capabilities: []string{"chat", "tools"}, Modalities: []string{"text"}, Source: "core"},
		{ID: "groq/llama-4-scout-17b-16e-instruct", Name: "Groq", Provider: "groq", ContextWindow: 131072, Capabilities: []string{"chat", "tools"}, Modalities: []string{"text"}, Source: "core"},
		{ID: "mistral-large-latest", Name: "Mistral AI", Provider: "mistral", ContextWindow: 128000, Reasoning: true, Capabilities: []string{"chat", "tools", "reasoning"}, Modalities: []string{"text"}, Source: "core"},
		{ID: "together/meta-llama/Llama-4-Scout-17B-16E-Instruct", Name: "Together AI", Provider: "together", ContextWindow: 131072, Capabilities: []string{"chat", "tools"}, Modalities: []string{"text"}, Source: "core"},
		{ID: "openrouter/anthropic/claude-sonnet-4", Name: "OpenRouter", Provider: "openrouter", ContextWindow: 200000, Reasoning: true, Capabilities: []string{"chat", "tools", "reasoning"}, Modalities: []string{"text"}, Source: "core"},
	}
}

func DefaultRegistry(configProviders map[string]ProviderConfig) *Registry {
	r := NewRegistry()
	models := BuiltinModels()
	envByID := map[string]string{
		"claude-sonnet-4-20250514":            "ANTHROPIC_API_KEY",
		"gpt-4o":                              "OPENAI_API_KEY",
		"gemini-2.5-pro":                      "GEMINI_API_KEY",
		"grok-3":                              "XAI_API_KEY",
		"command-r-plus":                      "COHERE_API_KEY",
		"groq/llama-4-scout-17b-16e-instruct": "GROQ_API_KEY",
		"mistral-large-latest":                "MISTRAL_API_KEY",
		"together/meta-llama/Llama-4-Scout-17B-16E-Instruct": "TOGETHER_API_KEY",
		"openrouter/anthropic/claude-sonnet-4":               "OPENROUTER_API_KEY",
	}
	for i := range models {
		if env := envByID[models[i].ID]; env != "" {
			models[i].Configured = strings.TrimSpace(os.Getenv(env)) != ""
		}
	}
	if strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_URL")) != "" {
		models = append(models, Model{ID: "http-default", Name: "HTTP Provider", Provider: "http", ContextWindow: 16384, Reasoning: true, Configured: true, Capabilities: []string{"chat", "tools"}, Modalities: []string{"text"}, Source: "env"})
	}
	for providerID, entry := range configProviders {
		id := strings.TrimSpace(entry.Model)
		if id == "" {
			id = providerID
		}
		models = append(models, Model{ID: id, Name: id + " (config)", Provider: providerID, ContextWindow: 128000, Reasoning: true, Configured: true, Capabilities: []string{"chat", "tools", "reasoning"}, Modalities: []string{"text"}, Source: "config"})
	}
	r.Register(models...)
	return r
}

func ToMaps(models []Model) []map[string]any {
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		entry := map[string]any{"id": m.ID, "name": m.Name, "provider": m.Provider}
		if m.ContextWindow > 0 {
			entry["context_window"] = m.ContextWindow
		}
		if m.Reasoning {
			entry["reasoning"] = true
		}
		if m.Configured {
			entry["configured"] = true
		}
		if len(m.Capabilities) > 0 {
			entry["capabilities"] = append([]string(nil), m.Capabilities...)
		}
		if len(m.Modalities) > 0 {
			entry["modalities"] = append([]string(nil), m.Modalities...)
		}
		if m.Source != "" {
			entry["source"] = m.Source
		}
		out = append(out, entry)
	}
	return out
}

func providerFromID(id string) string {
	if strings.Contains(id, "/") {
		return strings.SplitN(id, "/", 2)[0]
	}
	if strings.HasPrefix(id, "gpt-") {
		return "openai"
	}
	if strings.HasPrefix(id, "claude-") {
		return "anthropic"
	}
	return id
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(strings.ToLower(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func containsString(values []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, v := range values {
		if strings.ToLower(v) == want {
			return true
		}
	}
	return false
}
