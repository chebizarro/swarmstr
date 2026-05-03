package search

import (
	"context"
	"strings"
	"testing"
)

type stubInvoker struct {
	result any
	err    error
	params any
}

func (s *stubInvoker) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
	s.params = params
	return s.result, s.err
}

func TestRegistry_RegisterAndLookupProviders(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterWebSearchProvider(NewPluginWebSearchProvider("plugin-search", &stubInvoker{})); err != nil {
		t.Fatalf("register search: %v", err)
	}
	if err := r.RegisterWebFetchProvider(NewPluginWebFetchProvider("plugin-fetch", &stubInvoker{})); err != nil {
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
	p := NewPluginWebSearchProvider("search", &stubInvoker{result: map[string]any{
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
	p := NewPluginWebFetchProvider("fetch", &stubInvoker{result: map[string]any{"text": "hello"}})
	result, err := p.Fetch(context.Background(), "https://example.com", FetchOptions{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if result.Content != "hello" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
}

func TestRegistryOrderDefaultsAndGlobalRegistration(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.FirstWebSearchProvider(); ok {
		t.Fatal("expected no first search provider")
	}
	if _, ok := r.FirstWebFetchProvider(); ok {
		t.Fatal("expected no first fetch provider")
	}
	if err := r.RegisterWebSearchProvider(nil); err == nil {
		t.Fatal("expected nil search provider error")
	}
	if err := r.RegisterWebFetchProvider(nil); err == nil {
		t.Fatal("expected nil fetch provider error")
	}
	if err := r.RegisterWebSearchProvider(NewPluginWebSearchProvider("z-search", &stubInvoker{})); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterWebSearchProvider(NewPluginWebSearchProvider("a-search", &stubInvoker{})); err != nil {
		t.Fatal(err)
	}
	first, ok := r.FirstWebSearchProvider()
	if !ok || first.ID() != "a-search" {
		t.Fatalf("first search = %v ok=%v", first, ok)
	}
	if err := r.RegisterWebFetchProvider(NewPluginWebFetchProvider("b-fetch", &stubInvoker{})); err != nil {
		t.Fatal(err)
	}
	firstFetch, ok := r.FirstWebFetchProvider()
	if !ok || firstFetch.ID() != "b-fetch" {
		t.Fatalf("first fetch = %v ok=%v", firstFetch, ok)
	}
	old := defaultRegistry
	defaultRegistry = NewRegistry()
	t.Cleanup(func() { defaultRegistry = old })
	if DefaultRegistry() != defaultRegistry {
		t.Fatal("default registry mismatch")
	}
	if err := RegisterWebSearchProvider(NewPluginWebSearchProvider("global-search", &stubInvoker{})); err != nil {
		t.Fatal(err)
	}
	if err := RegisterWebFetchProvider(NewPluginWebFetchProvider("global-fetch", &stubInvoker{})); err != nil {
		t.Fatal(err)
	}
}

func TestPluginProvidersPassParametersAndErrors(t *testing.T) {
	searchHost := &stubInvoker{result: []SearchResult{{Title: "direct", URL: "https://direct"}}}
	searchProvider := NewPluginWebSearchProvider("search", searchHost)
	results, err := searchProvider.Search(context.Background(), "nostr", SearchOptions{MaxResults: 3, Locale: "en-US"})
	if err != nil || len(results) != 1 {
		t.Fatalf("direct search results=%+v err=%v", results, err)
	}
	params := searchHost.params.(map[string]any)
	if params["query"] != "nostr" || params["max_results"] != 3 || params["locale"] != "en-US" {
		t.Fatalf("unexpected search params: %+v", params)
	}
	fetchHost := &stubInvoker{result: "plain body"}
	fetchProvider := NewPluginWebFetchProvider("fetch", fetchHost)
	fetchResult, err := fetchProvider.Fetch(context.Background(), "https://example.com", FetchOptions{MaxChars: 42, TimeoutSeconds: 2})
	if err != nil || fetchResult.Content != "plain body" {
		t.Fatalf("fetch=%+v err=%v", fetchResult, err)
	}
	fetchParams := fetchHost.params.(map[string]any)
	if fetchParams["url"] != "https://example.com" || fetchParams["max_chars"] != 42 || fetchParams["timeout_seconds"] != 2 {
		t.Fatalf("unexpected fetch params: %+v", fetchParams)
	}
	if _, err := NewPluginWebSearchProvider("missing", nil).Search(context.Background(), "q", SearchOptions{}); err == nil {
		t.Fatal("expected missing host search error")
	}
	if _, err := NewPluginWebFetchProvider("missing", nil).Fetch(context.Background(), "u", FetchOptions{}); err == nil {
		t.Fatal("expected missing host fetch error")
	}
	boom := searchErr("boom")
	if _, err := NewPluginWebSearchProvider("search", &stubInvoker{err: boom}).Search(context.Background(), "q", SearchOptions{}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected search error, got %v", err)
	}
}

func TestParseSearchAndFetchVariants(t *testing.T) {
	for _, input := range []any{
		nil,
		[]SearchResult{{Title: "direct"}},
		map[string]any{"items": []map[string]any{{"title": "item", "url": "u"}}},
		map[string]any{"data": []map[string]any{{"title": "data", "url": "u"}}},
	} {
		if _, err := parseSearchResults(input); err != nil {
			t.Fatalf("parseSearchResults(%#v): %v", input, err)
		}
	}
	for _, input := range []any{
		nil,
		"content",
		FetchResult{URL: "https://x", Content: "body"},
		map[string]any{"body": "body", "markdown": "# body", "title": "T", "url": "https://x"},
	} {
		if _, err := parseFetchResult(input); err != nil {
			t.Fatalf("parseFetchResult(%#v): %v", input, err)
		}
	}
	if firstNonEmpty(" ", " value ") != " value " || normalizeID(" X ") != "x" {
		t.Fatal("helper behavior mismatch")
	}
}

type searchErr string

func (e searchErr) Error() string { return string(e) }
