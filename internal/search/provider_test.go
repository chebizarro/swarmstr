package search

import (
	"context"
	"testing"
)

type stubInvoker struct{ result any }

func (s stubInvoker) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
	return s.result, nil
}

func TestRegistry_RegisterAndLookupProviders(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterWebSearchProvider(NewPluginWebSearchProvider("plugin-search", stubInvoker{})); err != nil {
		t.Fatalf("register search: %v", err)
	}
	if err := r.RegisterWebFetchProvider(NewPluginWebFetchProvider("plugin-fetch", stubInvoker{})); err != nil {
		t.Fatalf("register fetch: %v", err)
	}
	if _, ok := r.WebSearchProvider("plugin-search"); !ok {
		t.Fatalf("expected search provider lookup to succeed")
	}
	if _, ok := r.WebFetchProvider("plugin-fetch"); !ok {
		t.Fatalf("expected fetch provider lookup to succeed")
	}
}

func TestPluginWebSearchProvider_ParseResults(t *testing.T) {
	p := NewPluginWebSearchProvider("search", stubInvoker{result: map[string]any{
		"results": []map[string]any{{"title": "A", "url": "https://a", "snippet": "sa"}},
	}})
	results, err := p.Search(context.Background(), "q", SearchOptions{MaxResults: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://a" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestPluginWebFetchProvider_ParseResults(t *testing.T) {
	p := NewPluginWebFetchProvider("fetch", stubInvoker{result: map[string]any{"text": "hello"}})
	result, err := p.Fetch(context.Background(), "https://example.com", FetchOptions{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if result.Content != "hello" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
}
