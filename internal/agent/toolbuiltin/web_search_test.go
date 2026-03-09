package toolbuiltin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebSearchTool_MissingQuery(t *testing.T) {
	tool := WebSearchTool(WebSearchConfig{})
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing query")
	}
}

func TestWebSearchTool_NoProvider(t *testing.T) {
	// Clear any keys that might be set in the environment during CI.
	cfg := WebSearchConfig{BraveAPIKey: "", SerperAPIKey: ""}
	tool := WebSearchTool(cfg)
	_, err := tool(context.Background(), map[string]any{"query": "test"})
	if err == nil {
		t.Error("expected error when no API keys configured")
	}
}

func TestWebSearchTool_UnknownProvider(t *testing.T) {
	cfg := WebSearchConfig{DefaultProvider: "bing", BraveAPIKey: "key"}
	tool := WebSearchTool(cfg)
	_, err := tool(context.Background(), map[string]any{"query": "test", "provider": "bing"})
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestWebSearchTool_BraveMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") == "" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"web": map[string]any{
				"results": []map[string]any{
					{"title": "Go lang", "url": "https://go.dev", "description": "The Go programming language"},
					{"title": "Golang Tour", "url": "https://tour.golang.org", "description": "Interactive tour"},
				},
			},
		})
	}))
	defer srv.Close()

	cfg := WebSearchConfig{
		BraveAPIKey:   "testkey",
		BraveEndpoint: srv.URL,
	}
	tool := WebSearchTool(cfg)
	result, err := tool(context.Background(), map[string]any{
		"query":    "golang",
		"provider": "brave",
		"count":    2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var results []SearchResult
	if err := json.Unmarshal([]byte(result), &results); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Go lang" {
		t.Errorf("unexpected title: %q", results[0].Title)
	}
	if results[0].URL != "https://go.dev" {
		t.Errorf("unexpected URL: %q", results[0].URL)
	}
}

func TestWebSearchTool_SerperMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") == "" {
			http.Error(w, "missing key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"organic": []map[string]any{
				{"title": "Rust lang", "link": "https://rust-lang.org", "snippet": "Systems language"},
			},
		})
	}))
	defer srv.Close()

	cfg := WebSearchConfig{
		SerperAPIKey:   "testkey",
		SerperEndpoint: srv.URL,
	}
	tool := WebSearchTool(cfg)
	result, err := tool(context.Background(), map[string]any{
		"query":    "rust",
		"provider": "serper",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var results []SearchResult
	if err := json.Unmarshal([]byte(result), &results); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].URL != "https://rust-lang.org" {
		t.Errorf("unexpected URL: %q", results[0].URL)
	}
}

func TestWebSearchTool_CountClamped(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		q := r.URL.Query()
		if got := q.Get("count"); got != "10" {
			t.Errorf("expected count=10 (max), got %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()

	cfg := WebSearchConfig{BraveAPIKey: "key", BraveEndpoint: srv.URL}
	tool := WebSearchTool(cfg)
	// Request 99 — should be clamped to 10.
	_, err := tool(context.Background(), map[string]any{"query": "test", "count": 99, "provider": "brave"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 HTTP call, got %d", callCount)
	}
}
