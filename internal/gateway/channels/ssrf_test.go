package channels

import (
	"net/http"
	"testing"
	"time"
)

func TestValidateSafeURL_BlocksUnsafeSchemesAndPrivateHosts(t *testing.T) {
	if err := ValidateSafeURL("http://example.com/file", SafeHTTPOptions{}); err == nil {
		t.Fatal("expected plain HTTP to be blocked by default")
	}
	if err := ValidateSafeURL("https://127.0.0.1/file", SafeHTTPOptions{}); err == nil {
		t.Fatal("expected loopback host to be blocked")
	}
	if err := ValidateSafeURL("https://169.254.169.254/latest/meta-data", SafeHTTPOptions{}); err == nil {
		t.Fatal("expected metadata IP to be blocked")
	}
	if err := ValidateSafeURL("https://user:pass@example.com/file", SafeHTTPOptions{}); err == nil {
		t.Fatal("expected userinfo URL to be blocked")
	}
}

func TestValidateSafeURL_Allowlist(t *testing.T) {
	opts := SafeHTTPOptions{AllowedHosts: []string{"93.184.216.34"}}
	if err := ValidateSafeURL("https://93.184.216.34/image.png", opts); err != nil {
		t.Fatalf("expected allowed public IP host: %v", err)
	}
	if err := ValidateSafeURL("https://93.184.216.35/image.png", opts); err == nil {
		t.Fatal("expected host outside allowlist to be blocked")
	}
}

func TestSSRFClient_DisablesEnvironmentProxy(t *testing.T) {
	client := NewSSRFClient(SafeHTTPOptions{Timeout: time.Second})
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("expected proxy bypass: transport.Proxy must be nil")
	}
}

func TestSSRFClient_StripsSensitiveHeadersOnCrossOriginRedirect(t *testing.T) {
	client := NewSSRFClient(SafeHTTPOptions{AllowedHosts: []string{"93.184.216.34", "93.184.216.35"}})
	from, _ := http.NewRequest(http.MethodGet, "https://93.184.216.34/a", nil)
	from.Header.Set("Authorization", "Bearer secret")
	from.Header.Set("Cookie", "sid=secret")
	to, _ := http.NewRequest(http.MethodGet, "https://93.184.216.35/b", nil)
	to.Header = from.Header.Clone()
	if err := client.CheckRedirect(to, []*http.Request{from}); err != nil {
		t.Fatalf("redirect should be URL-safe: %v", err)
	}
	if to.Header.Get("Authorization") != "" || to.Header.Get("Cookie") != "" {
		t.Fatalf("expected auth headers stripped, got %v", to.Header)
	}
}

func TestHostAllowed_Wildcards(t *testing.T) {
	if !hostAllowed("media.example.com", []string{"*.example.com"}) {
		t.Fatal("expected wildcard allow")
	}
	if hostAllowed("example.net", []string{"*.example.com"}) {
		t.Fatal("expected different suffix denied")
	}
}
