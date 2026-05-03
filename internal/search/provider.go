package search

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// SearchResult is a normalized web search result.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// SearchOptions controls web search behavior.
type SearchOptions struct {
	MaxResults int
	Locale     string
}

// FetchOptions controls web fetch behavior.
type FetchOptions struct {
	MaxChars       int
	TimeoutSeconds int
}

// FetchResult is normalized fetched page content.
type FetchResult struct {
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
	Content  string `json:"content"`
	Markdown string `json:"markdown,omitempty"`
}

// WebSearchProvider is a pluggable web search backend.
type WebSearchProvider interface {
	ID() string
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
}

// WebFetchProvider is a pluggable web fetch backend.
type WebFetchProvider interface {
	ID() string
	Fetch(ctx context.Context, rawURL string, opts FetchOptions) (FetchResult, error)
}

// ProviderInvoker is the minimal host surface needed by OpenClaw-backed providers.
type ProviderInvoker interface {
	InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error)
}

// Registry stores registered search/fetch providers.
type Registry struct {
	mu            sync.RWMutex
	searchByID    map[string]WebSearchProvider
	fetchByID     map[string]WebFetchProvider
	searchOrder   []string
	fetchOrder    []string
}

func NewRegistry() *Registry {
	return &Registry{
		searchByID: map[string]WebSearchProvider{},
		fetchByID:  map[string]WebFetchProvider{},
	}
}

var defaultRegistry = NewRegistry()

func DefaultRegistry() *Registry { return defaultRegistry }

func normalizeID(id string) string { return strings.ToLower(strings.TrimSpace(id)) }

func (r *Registry) RegisterWebSearchProvider(p WebSearchProvider) error {
	if p == nil {
		return fmt.Errorf("web search provider is nil")
	}
	id := normalizeID(p.ID())
	if id == "" {
		return fmt.Errorf("web search provider id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.searchByID[id]; !exists {
		r.searchOrder = append(r.searchOrder, id)
		sort.Strings(r.searchOrder)
	}
	r.searchByID[id] = p
	return nil
}

func (r *Registry) RegisterWebFetchProvider(p WebFetchProvider) error {
	if p == nil {
		return fmt.Errorf("web fetch provider is nil")
	}
	id := normalizeID(p.ID())
	if id == "" {
		return fmt.Errorf("web fetch provider id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.fetchByID[id]; !exists {
		r.fetchOrder = append(r.fetchOrder, id)
		sort.Strings(r.fetchOrder)
	}
	r.fetchByID[id] = p
	return nil
}

func (r *Registry) WebSearchProvider(id string) (WebSearchProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.searchByID[normalizeID(id)]
	return p, ok
}

func (r *Registry) WebFetchProvider(id string) (WebFetchProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.fetchByID[normalizeID(id)]
	return p, ok
}

func (r *Registry) FirstWebSearchProvider() (WebSearchProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.searchOrder) == 0 {
		return nil, false
	}
	id := r.searchOrder[0]
	p, ok := r.searchByID[id]
	return p, ok
}

func (r *Registry) FirstWebFetchProvider() (WebFetchProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.fetchOrder) == 0 {
		return nil, false
	}
	id := r.fetchOrder[0]
	p, ok := r.fetchByID[id]
	return p, ok
}

// registerWebSearchProvider supports plugin-side registration naming.
func registerWebSearchProvider(p WebSearchProvider) error { return DefaultRegistry().RegisterWebSearchProvider(p) }

// registerWebFetchProvider supports plugin-side registration naming.
func registerWebFetchProvider(p WebFetchProvider) error { return DefaultRegistry().RegisterWebFetchProvider(p) }

// RegisterWebSearchProvider registers a global web search provider.
func RegisterWebSearchProvider(p WebSearchProvider) error { return registerWebSearchProvider(p) }

// RegisterWebFetchProvider registers a global web fetch provider.
func RegisterWebFetchProvider(p WebFetchProvider) error { return registerWebFetchProvider(p) }

type pluginWebSearchProvider struct {
	providerID string
	host       ProviderInvoker
}

func NewPluginWebSearchProvider(providerID string, host ProviderInvoker) WebSearchProvider {
	return &pluginWebSearchProvider{providerID: providerID, host: host}
}

func (p *pluginWebSearchProvider) ID() string { return p.providerID }

func (p *pluginWebSearchProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if p.host == nil {
		return nil, fmt.Errorf("web search provider %q has no host", p.providerID)
	}
	result, err := p.host.InvokeProvider(ctx, p.providerID, "search", map[string]any{
		"query":       query,
		"max_results": opts.MaxResults,
		"locale":      opts.Locale,
	})
	if err != nil {
		return nil, err
	}
	return parseSearchResults(result)
}

type pluginWebFetchProvider struct {
	providerID string
	host       ProviderInvoker
}

func NewPluginWebFetchProvider(providerID string, host ProviderInvoker) WebFetchProvider {
	return &pluginWebFetchProvider{providerID: providerID, host: host}
}

func (p *pluginWebFetchProvider) ID() string { return p.providerID }

func (p *pluginWebFetchProvider) Fetch(ctx context.Context, rawURL string, opts FetchOptions) (FetchResult, error) {
	if p.host == nil {
		return FetchResult{}, fmt.Errorf("web fetch provider %q has no host", p.providerID)
	}
	result, err := p.host.InvokeProvider(ctx, p.providerID, "fetch", map[string]any{
		"url":             rawURL,
		"max_chars":       opts.MaxChars,
		"timeout_seconds": opts.TimeoutSeconds,
	})
	if err != nil {
		return FetchResult{}, err
	}
	return parseFetchResult(result)
}

func parseSearchResults(v any) ([]SearchResult, error) {
	if v == nil {
		return nil, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var direct []SearchResult
	if err := json.Unmarshal(data, &direct); err == nil {
		return direct, nil
	}

	var wrapped struct {
		Results []SearchResult `json:"results"`
		Items   []SearchResult `json:"items"`
		Data    []SearchResult `json:"data"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return nil, err
	}
	if len(wrapped.Results) > 0 {
		return wrapped.Results, nil
	}
	if len(wrapped.Items) > 0 {
		return wrapped.Items, nil
	}
	return wrapped.Data, nil
}

func parseFetchResult(v any) (FetchResult, error) {
	if v == nil {
		return FetchResult{}, nil
	}
	if s, ok := v.(string); ok {
		return FetchResult{Content: s}, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return FetchResult{}, err
	}
	var out FetchResult
	if err := json.Unmarshal(data, &out); err == nil {
		if out.Content != "" || out.Markdown != "" {
			return out, nil
		}
	}
	var alt struct {
		Text     string `json:"text"`
		Body     string `json:"body"`
		Markdown string `json:"markdown"`
		Title    string `json:"title"`
		URL      string `json:"url"`
	}
	if err := json.Unmarshal(data, &alt); err != nil {
		return FetchResult{}, err
	}
	return FetchResult{URL: alt.URL, Title: alt.Title, Content: firstNonEmpty(alt.Text, alt.Body), Markdown: alt.Markdown}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
