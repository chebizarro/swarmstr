package zap

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Mock keyer for Send tests
// ═══════════════════════════════════════════════════════════════════════════════

type mockKeyer struct {
	pk nostr.PubKey
}

func (m *mockKeyer) GetPublicKey(_ context.Context) (nostr.PubKey, error) {
	return m.pk, nil
}

func (m *mockKeyer) SignEvent(_ context.Context, evt *nostr.Event) error {
	evt.PubKey = m.pk
	return nil
}

func (m *mockKeyer) Encrypt(_ context.Context, plaintext string, _ nostr.PubKey) (string, error) {
	return plaintext, nil
}

func (m *mockKeyer) Decrypt(_ context.Context, ciphertext string, _ nostr.PubKey) (string, error) {
	return ciphertext, nil
}

type errMockKeyer struct {
	err error
}

func (e *errMockKeyer) GetPublicKey(_ context.Context) (nostr.PubKey, error) {
	return nostr.PubKey{}, e.err
}
func (e *errMockKeyer) SignEvent(_ context.Context, _ *nostr.Event) error {
	return e.err
}
func (e *errMockKeyer) Encrypt(_ context.Context, p string, _ nostr.PubKey) (string, error) {
	return p, nil
}
func (e *errMockKeyer) Decrypt(_ context.Context, c string, _ nostr.PubKey) (string, error) {
	return c, nil
}

// insecureTransport allows tests to connect to TLS test servers.
func withInsecureTransport(t *testing.T) {
	t.Helper()
	orig := http.DefaultTransport
	http.DefaultTransport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	t.Cleanup(func() { http.DefaultTransport = orig })
}

// ═══════════════════════════════════════════════════════════════════════════════
// ResolveLNURL edge cases
// ═══════════════════════════════════════════════════════════════════════════════

func TestResolveLNURL_AllowsNostrTrueFromServer(t *testing.T) {
	withInsecureTransport(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(lnurlPayMetadata{
			Callback:    "https://example.com/cb",
			MinSendable: 1000,
			MaxSendable: 100000000,
			AllowsNostr: true,
			NostrPubkey: "abcdef",
		})
	}))
	defer srv.Close()

	// TLS server URL is https://127.0.0.1:PORT → lud16 = user@127.0.0.1:PORT
	host := strings.TrimPrefix(srv.URL, "https://")
	meta, err := ResolveLNURL(context.Background(), "user@"+host)
	if err != nil {
		t.Fatal(err)
	}
	if meta.NostrPubkey != "abcdef" {
		t.Errorf("nostrPubkey: %q", meta.NostrPubkey)
	}
	if !meta.AllowsNostr {
		t.Error("expected AllowsNostr = true")
	}
}

func TestResolveLNURL_AllowsNostrFalseFromServer(t *testing.T) {
	withInsecureTransport(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(lnurlPayMetadata{
			Callback:    "https://example.com/cb",
			MinSendable: 1000,
			MaxSendable: 100000000,
			AllowsNostr: false,
		})
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	_, err := ResolveLNURL(context.Background(), "user@"+host)
	if err == nil || !strings.Contains(err.Error(), "does not support Nostr zaps") {
		t.Errorf("err: %v", err)
	}
}

func TestResolveLNURL_InvalidJSONBody(t *testing.T) {
	withInsecureTransport(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`not valid json`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	_, err := ResolveLNURL(context.Background(), "user@"+host)
	if err == nil || !strings.Contains(err.Error(), "parse LNURL metadata") {
		t.Errorf("err: %v", err)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Send tests with full HTTP mock flow (TLS)
// ═══════════════════════════════════════════════════════════════════════════════

// sendTestServer creates a TLS test HTTP server that handles both LNURL
// resolution and callback in one mux. Returns the server and a "lud16" to use.
func sendTestServer(t *testing.T, callbackHandler http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/.well-known/lnurlp/user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(lnurlPayMetadata{
			Callback:    srv.URL + "/callback",
			MinSendable: 1000,    // 1 sat
			MaxSendable: 1000000, // 1000 sats
			AllowsNostr: true,
			NostrPubkey: "0000000000000000000000000000000000000000000000000000000000000001",
		})
	})
	mux.HandleFunc("/callback", callbackHandler)

	srv = httptest.NewTLSServer(mux)
	host := strings.TrimPrefix(srv.URL, "https://")
	return srv, fmt.Sprintf("user@%s", host)
}

func TestSend_SuccessFullFlow(t *testing.T) {
	withInsecureTransport(t)
	srv, lud16 := sendTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("nostr") == "" {
			t.Error("missing nostr param")
		}
		if r.URL.Query().Get("amount") != "10000" {
			t.Errorf("amount: %s", r.URL.Query().Get("amount"))
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"pr": "lnbc10n1..."})
	})
	defer srv.Close()

	res, err := Send(context.Background(), SendOpts{
		Keyer:  &mockKeyer{},
		Relays: []string{"wss://relay.example"},
	}, lud16, "0000000000000000000000000000000000000000000000000000000000000001", "", 10, "test zap")
	if err != nil {
		t.Fatal(err)
	}
	if res.Invoice != "lnbc10n1..." {
		t.Errorf("invoice: %q", res.Invoice)
	}
	if res.ZapRequestID == "" {
		t.Error("expected non-empty zap request ID")
	}
}

func TestSend_WithNoteID(t *testing.T) {
	withInsecureTransport(t)
	srv, lud16 := sendTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"pr": "lnbc20n1..."})
	})
	defer srv.Close()

	res, err := Send(context.Background(), SendOpts{
		Keyer:  &mockKeyer{},
		Relays: []string{"wss://r1"},
	}, lud16, "0000000000000000000000000000000000000000000000000000000000000001",
		"abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		5, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Invoice != "lnbc20n1..." {
		t.Errorf("invoice: %q", res.Invoice)
	}
}

func TestSend_WithComment(t *testing.T) {
	withInsecureTransport(t)
	srv, lud16 := sendTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("comment") != "nice post" {
			t.Errorf("comment: %s", r.URL.Query().Get("comment"))
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"pr": "lnbc1..."})
	})
	defer srv.Close()

	_, err := Send(context.Background(), SendOpts{
		Keyer:  &mockKeyer{},
		Relays: []string{"wss://r"},
	}, lud16, "pk", "", 5, "nice post")
	if err != nil {
		t.Fatal(err)
	}
}

func TestSend_AmountBelowMinimum(t *testing.T) {
	withInsecureTransport(t)
	srv, lud16 := sendTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("callback should not be reached")
	})
	defer srv.Close()

	_, err := Send(context.Background(), SendOpts{
		Keyer:  &mockKeyer{},
		Relays: []string{"wss://r"},
	}, lud16, "pk", "", 0, "")
	if err == nil || !strings.Contains(err.Error(), "below minimum") {
		t.Errorf("err: %v", err)
	}
}

func TestSend_AmountAboveMaximum(t *testing.T) {
	withInsecureTransport(t)
	srv, lud16 := sendTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("callback should not be reached")
	})
	defer srv.Close()

	_, err := Send(context.Background(), SendOpts{
		Keyer:  &mockKeyer{},
		Relays: []string{"wss://r"},
	}, lud16, "pk", "", 2000, "")
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("err: %v", err)
	}
}

func TestSend_WalletReturnsError(t *testing.T) {
	withInsecureTransport(t)
	srv, lud16 := sendTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ERROR",
			"reason": "insufficient balance",
		})
	})
	defer srv.Close()

	_, err := Send(context.Background(), SendOpts{
		Keyer:  &mockKeyer{},
		Relays: []string{"wss://r"},
	}, lud16, "pk", "", 5, "")
	if err == nil || !strings.Contains(err.Error(), "insufficient balance") {
		t.Errorf("err: %v", err)
	}
}

func TestSend_WalletReturnsNoInvoice(t *testing.T) {
	withInsecureTransport(t)
	srv, lud16 := sendTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{})
	})
	defer srv.Close()

	_, err := Send(context.Background(), SendOpts{
		Keyer:  &mockKeyer{},
		Relays: []string{"wss://r"},
	}, lud16, "pk", "", 5, "")
	if err == nil || !strings.Contains(err.Error(), "no invoice") {
		t.Errorf("err: %v", err)
	}
}

func TestSend_CallbackInvalidJSON(t *testing.T) {
	withInsecureTransport(t)
	srv, lud16 := sendTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	defer srv.Close()

	_, err := Send(context.Background(), SendOpts{
		Keyer:  &mockKeyer{},
		Relays: []string{"wss://r"},
	}, lud16, "pk", "", 5, "")
	if err == nil || !strings.Contains(err.Error(), "parse callback response") {
		t.Errorf("err: %v", err)
	}
}

func TestSend_GetPublicKeyError(t *testing.T) {
	errKeyer := &errMockKeyer{err: fmt.Errorf("key unavailable")}
	_, err := Send(context.Background(), SendOpts{
		Keyer: errKeyer,
	}, "user@example.com", "pk", "", 5, "")
	if err == nil || !strings.Contains(err.Error(), "key unavailable") {
		t.Errorf("err: %v", err)
	}
}
