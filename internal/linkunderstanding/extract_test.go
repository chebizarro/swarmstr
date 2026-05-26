package linkunderstanding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractURLsDedupesAndTrimsPunctuation(t *testing.T) {
	links := ExtractURLs("Read https://example.com/a). Also https://example.com/a and http://example.org?q=1.")
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %+v", links)
	}
	if links[0].URL != "https://example.com/a" || links[1].URL != "http://example.org?q=1" {
		t.Fatalf("unexpected links: %+v", links)
	}
}

func TestParseMetadataOpenGraphAndSummary(t *testing.T) {
	doc := `<html><head><title>Fallback</title><meta property="og:title" content="OG Title"><meta name="description" content="Desc"></head><body><h1>Hello</h1><p>Long page text for summary.</p></body></html>`
	meta := ParseMetadata(doc)
	if meta.Title != "OG Title" || meta.Description != "Desc" {
		t.Fatalf("bad metadata: %+v", meta)
	}
	if !strings.Contains(meta.Summary, "Hello") {
		t.Fatalf("expected body summary, got %q", meta.Summary)
	}
}

func TestFetchMetadataAllowsPrivateWhenConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Local</title><meta property="og:description" content="Testing"></head><body>Fetched body.</body></html>`))
	}))
	defer srv.Close()
	meta, err := FetchMetadata(context.Background(), srv.URL, FetchOptions{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Local" || meta.Description != "Testing" || meta.FinalURL == "" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
}

func TestAssembleContext(t *testing.T) {
	ctx := AssembleContext([]Metadata{{URL: "https://example.com", Title: "Example", Description: "A site", Summary: "Summary"}})
	for _, want := range []string{"Linked resources", "Example", "URL: https://example.com"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("context missing %q: %s", want, ctx)
		}
	}
}
