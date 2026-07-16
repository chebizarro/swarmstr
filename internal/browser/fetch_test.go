package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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

func TestClientRetainsHeadersWithoutJSONExposure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("WWW-Authenticate", `L402 macaroon="abc", invoice="lnbc1"`)
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer srv.Close()

	resp, err := (&Client{}).Fetch(context.Background(), Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := resp.Headers.Values("WWW-Authenticate"); len(got) != 1 || !strings.Contains(got[0], "macaroon") {
		t.Fatalf("headers not retained: %#v", got)
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "WWW-Authenticate") || strings.Contains(string(encoded), "macaroon") {
		t.Fatalf("internal headers leaked through JSON: %s", encoded)
	}
}

func TestClientRejectsRedirectDestination(t *testing.T) {
	var reached bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer target.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer start.Close()

	client := &Client{ValidateURL: func(raw string) error {
		if strings.HasPrefix(raw, target.URL) {
			return fmt.Errorf("blocked redirect")
		}
		return nil
	}}
	_, err := client.Fetch(context.Background(), Request{URL: start.URL})
	if err == nil || !strings.Contains(err.Error(), "blocked redirect") {
		t.Fatalf("expected redirect validation error, got %v", err)
	}
	if reached {
		t.Fatal("rejected redirect destination was requested")
	}
}

func TestClientStripsAuthorizationOnCrossOriginRedirect(t *testing.T) {
	var gotAuthorization string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer start.Close()

	_, err := (&Client{}).Fetch(context.Background(), Request{URL: start.URL, Headers: map[string]string{"Authorization": "L402 secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuthorization != "" {
		t.Fatalf("authorization leaked cross-origin: %q", gotAuthorization)
	}
}

func TestClientPreservesAuthorizationOnSameOriginRedirect(t *testing.T) {
	var gotAuthorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		gotAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	_, err := (&Client{}).Fetch(context.Background(), Request{URL: srv.URL + "/start", Headers: map[string]string{"Authorization": "L402 same-origin"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuthorization != "L402 same-origin" {
		t.Fatalf("same-origin authorization = %q", gotAuthorization)
	}
}

func TestClientValidatedDialRejectsPrivateResolution(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer srv.Close()
	address := strings.TrimPrefix(srv.URL, "http://127.0.0.1")
	lookups := 0
	client := &Client{
		ValidateIP: func(ip net.IP) error {
			if ip.IsLoopback() {
				return fmt.Errorf("private dial blocked")
			}
			return nil
		},
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			lookups++
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		},
	}
	_, err := client.Fetch(context.Background(), Request{URL: "http://public.example" + address})
	if err == nil || !strings.Contains(err.Error(), "private dial blocked") {
		t.Fatalf("dial validation error = %v", err)
	}
	if reached || lookups != 1 {
		t.Fatalf("dial reached=%v lookups=%d", reached, lookups)
	}
}

func TestClientFinalRedirectStrippingCannotBeOverridden(t *testing.T) {
	var authorization, cookie string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		cookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer start.Close()
	base := &http.Client{CheckRedirect: func(next *http.Request, _ []*http.Request) error {
		next.Header.Set("Authorization", "reinserted")
		next.Header.Set("Cookie", "session=secret")
		return nil
	}}
	_, err := (&Client{HTTPClient: base}).Fetch(context.Background(), Request{
		URL: start.URL, Headers: map[string]string{"Authorization": "original", "Cookie": "original=secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" || cookie != "" {
		t.Fatalf("redirect credentials leaked: authorization=%q cookie=%q", authorization, cookie)
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
