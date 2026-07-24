package nip05

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	nostr "fiatjaf.com/nostr"
)

func TestResolveVerifyAndNoRedirect(t *testing.T) {
	pk := nostr.Generate().Public()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/nostr.json" || r.URL.Query().Get("name") != "bob" {
			t.Fatalf("wrong request: %s", r.URL)
		}
		fmt.Fprintf(w, `{"names":{"bob":"%s"},"relays":{"%s":["wss://relay.example"]}}`, pk.Hex(), pk.Hex())
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL + "/.well-known/nostr.json?name=bob")
	resolver := Resolver{Client: server.Client(), Endpoint: func(_, _ string) (*url.URL, error) { return endpoint, nil }}
	result, err := resolver.Verify(context.Background(), "bob@example.com", pk)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "bob" || len(result.Relays) != 1 {
		t.Fatalf("bad result: %#v", result)
	}

	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, server.URL, http.StatusFound) }))
	defer redirect.Close()
	redirectURL, _ := url.Parse(redirect.URL)
	resolver = Resolver{Client: redirect.Client(), Endpoint: func(_, _ string) (*url.URL, error) { return redirectURL, nil }}
	if _, err := resolver.Resolve(context.Background(), "bob@example.com"); err == nil {
		t.Fatal("followed forbidden redirect")
	}
}

func TestRejectsInvalidIdentifiersAndDocuments(t *testing.T) {
	for _, id := range []string{"Bob@example.com", "a+b@example.com", "bob@example", "bob@@example.com"} {
		if _, _, err := ParseIdentifier(id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	pk := nostr.Generate().Public()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"names":{"bob":"%s"}}`, "ABC"+pk.Hex()[3:])
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	if _, err := (Resolver{Client: server.Client(), Endpoint: func(_, _ string) (*url.URL, error) { return u, nil }}).Resolve(context.Background(), "bob@example.com"); err == nil {
		t.Fatal("accepted uppercase key")
	}
}
