package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── HTMLToText ────────────────────────────────────────────────────────────────

func TestHTMLToText_stripsBasicTags(t *testing.T) {
	html := `<html><body><p>Hello <b>world</b>!</p></body></html>`
	got := HTMLToText(html)
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "world") {
		t.Errorf("expected text content in output, got: %q", got)
	}
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Errorf("output should not contain angle brackets, got: %q", got)
	}
}

func TestHTMLToText_removesScriptStyle(t *testing.T) {
	html := `<p>content</p><script>var x = 1;</script><style>.a{color:red}</style>`
	got := HTMLToText(html)
	if strings.Contains(got, "var x") || strings.Contains(got, "color:red") {
		t.Errorf("script/style content should be stripped, got: %q", got)
	}
	if !strings.Contains(got, "content") {
		t.Errorf("visible content should remain, got: %q", got)
	}
}

func TestHTMLToText_decodesEntities(t *testing.T) {
	html := `<p>AT&amp;T &lt;rocks&gt; &quot;says&quot; &#39;so&#39;</p>`
	got := HTMLToText(html)
	if !strings.Contains(got, "AT&T") {
		t.Errorf("expected &amp; decoded, got: %q", got)
	}
	if !strings.Contains(got, "<rocks>") {
		t.Errorf("expected &lt;&gt; decoded, got: %q", got)
	}
}

func TestHTMLToText_collapseWhitespace(t *testing.T) {
	html := `<p>  lots   of   spaces  </p>`
	got := HTMLToText(html)
	if strings.Contains(got, "  ") {
		t.Errorf("expected collapsed whitespace, got: %q", got)
	}
}

func TestHTMLToText_blockTagsAddNewlines(t *testing.T) {
	html := `<h1>Title</h1><p>Para one</p><p>Para two</p>`
	got := HTMLToText(html)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	// Should have at least 2 non-empty lines.
	nonEmpty := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty < 2 {
		t.Errorf("expected multiple lines from block tags, got: %q", got)
	}
}

// ─── Fetch ─────────────────────────────────────────────────────────────────────

func TestFetch_plainHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><body><p>Hello browser</p></body></html>`))
	}))
	defer srv.Close()

	resp, err := Fetch(context.Background(), Request{Method: "GET", URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Text, "Hello browser") {
		t.Errorf("expected plain text in response, got: %q", resp.Text)
	}
	if resp.Body != "" {
		t.Errorf("HTML response should use Text field, not Body")
	}
}

func TestFetch_jsonResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"key":"value"}`))
	}))
	defer srv.Close()

	resp, err := Fetch(context.Background(), Request{Method: "GET", URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(resp.Body, "key") {
		t.Errorf("expected JSON in Body field, got: %q", resp.Body)
	}
	if resp.Text != "" {
		t.Errorf("non-HTML response should use Body field, not Text")
	}
}

func TestFetch_queryParams(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), Request{
		Method: "GET",
		URL:    srv.URL,
		Query:  map[string]any{"foo": "bar", "n": 42},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(gotQuery, "foo=bar") {
		t.Errorf("expected foo=bar in query, got: %q", gotQuery)
	}
}

func TestFetch_missingURLErrors(t *testing.T) {
	_, err := Fetch(context.Background(), Request{Method: "GET", URL: ""})
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestFetch_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	resp, err := Fetch(context.Background(), Request{Method: "GET", URL: srv.URL})
	if err != nil {
		t.Fatalf("unexpected network error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}
