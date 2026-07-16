package toolbuiltin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"metiq/internal/browser"
)

type stubL402Fetcher struct {
	response browser.Response
	err      error
	request  browser.Request
}

func (s *stubL402Fetcher) Fetch(_ context.Context, request browser.Request) (browser.Response, error) {
	s.request = request
	return s.response, s.err
}

func TestL402FetchToolReturnsReadableBoundedContent(t *testing.T) {
	fetcher := &stubL402Fetcher{response: browser.Response{Text: "paid visible content"}}
	tool := L402FetchTool(L402FetchOpts{Client: fetcher, MaxPaymentTimeout: 1500 * time.Millisecond})
	result, err := tool(context.Background(), map[string]any{
		"url": "https://paid.example/resource", "max_chars": 4, "timeout_seconds": 30,
		"headers": map[string]any{"Authorization": "must-not-pass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "paid\n[truncated]" {
		t.Fatalf("result = %q", result)
	}
	if fetcher.request.URL != "https://paid.example/resource" || fetcher.request.Method != "GET" {
		t.Fatalf("request = %#v", fetcher.request)
	}
	if fetcher.request.TimeoutMS != 1500 {
		t.Fatalf("timeout = %dms", fetcher.request.TimeoutMS)
	}
	if len(fetcher.request.Headers) != 0 {
		t.Fatalf("caller headers reached L402 client: %#v", fetcher.request.Headers)
	}
}

func TestL402FetchToolErrorsAreScopedAndDefinitionIsSpending(t *testing.T) {
	fetcher := &stubL402Fetcher{err: errors.New("authorization rejected: macaroon=secret-macaroon preimage=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")}
	tool := L402FetchTool(L402FetchOpts{Client: fetcher})
	if _, err := tool(context.Background(), map[string]any{"url": "https://paid.example"}); err == nil ||
		!strings.HasPrefix(err.Error(), "l402_fetch: authorization rejected: ") ||
		strings.Contains(err.Error(), "secret-macaroon") ||
		strings.Contains(err.Error(), "0123456789abcdef") {
		t.Fatalf("error was not safely scoped and redacted: %v", err)
	}
	registration := L402FetchRegistration(L402FetchOpts{Client: fetcher})
	if registration.Descriptor.Name != "l402_fetch" || !registration.Descriptor.Traits.Destructive || registration.Descriptor.Traits.ReadOnly {
		t.Fatalf("unsafe registration metadata: %#v", registration.Descriptor)
	}
	if _, ok := L402FetchDef.Parameters.Properties["allow_local"]; ok {
		t.Fatal("l402_fetch exposes allow_local")
	}
	if _, ok := L402FetchDef.Parameters.Properties["provider"]; ok {
		t.Fatal("l402_fetch exposes plugin providers")
	}
}
