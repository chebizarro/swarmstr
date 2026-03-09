package toolbuiltin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetchTool_HTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><script>ignored</script><p>Hello world</p></body></html>`))
	}))
	defer srv.Close()

	tool := WebFetchTool(WebFetchOpts{AllowLocal: true})
	result, err := tool(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Hello world") {
		t.Errorf("expected 'Hello world' in result, got: %q", result)
	}
	if strings.Contains(result, "ignored") {
		t.Errorf("script tag content should be stripped, got: %q", result)
	}
}

func TestWebFetchTool_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("plain content here"))
	}))
	defer srv.Close()

	tool := WebFetchTool(WebFetchOpts{AllowLocal: true})
	result, err := tool(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "plain content here") {
		t.Errorf("expected plain content in result, got: %q", result)
	}
}

func TestWebFetchTool_SSRFRejection(t *testing.T) {
	tool := WebFetchTool(WebFetchOpts{AllowLocal: false})
	_, err := tool(context.Background(), map[string]any{"url": "http://127.0.0.1/"})
	if err == nil {
		t.Error("expected SSRF rejection for 127.0.0.1")
	}
}

func TestWebFetchTool_AllowLocalOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("local"))
	}))
	defer srv.Close()

	// AllowLocal=false globally, but allow_local=true per-call.
	tool := WebFetchTool(WebFetchOpts{AllowLocal: false})
	result, err := tool(context.Background(), map[string]any{
		"url":         srv.URL,
		"allow_local": true,
	})
	if err != nil {
		t.Fatalf("unexpected error with per-call allow_local: %v", err)
	}
	if !strings.Contains(result, "local") {
		t.Errorf("expected 'local' in result, got: %q", result)
	}
}

func TestWebFetchTool_MaxChars(t *testing.T) {
	body := strings.Repeat("x", 200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	tool := WebFetchTool(WebFetchOpts{AllowLocal: true})
	result, err := tool(context.Background(), map[string]any{
		"url":       srv.URL,
		"max_chars": 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(result, "[truncated]") {
		t.Errorf("expected truncation marker, got: %q", result)
	}
}

func TestWebFetchTool_MissingURL(t *testing.T) {
	tool := WebFetchTool(WebFetchOpts{})
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing URL")
	}
}

func TestWebFetchTool_InvalidScheme(t *testing.T) {
	tool := WebFetchTool(WebFetchOpts{})
	_, err := tool(context.Background(), map[string]any{"url": "ftp://example.com/"})
	if err == nil {
		t.Error("expected error for ftp scheme")
	}
}
