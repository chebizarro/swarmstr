package secrets

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestVaultBackendKV2RoundTrip(t *testing.T) {
	var mu sync.Mutex
	value := ""
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "test-token" || r.Header.Get("X-Vault-Namespace") != "team-a" {
			http.Error(w, "unauthorized", http.StatusForbidden)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/secret/data/swarmstr/mcp-auth":
			var body struct {
				Data map[string]string `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
			}
			mu.Lock()
			value = body.Data["value"]
			deleted = false
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/secret/data/swarmstr/mcp-auth":
			mu.Lock()
			current, gone := value, deleted
			mu.Unlock()
			if gone || current == "" {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": map[string]string{"value": current}}})
		case r.Method == "LIST" && r.URL.Path == "/v1/secret/metadata/swarmstr/plugins":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"keys": []string{"zeta", "nested/", "alpha", "alpha"}}})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/secret/metadata/swarmstr/mcp-auth":
			mu.Lock()
			deleted = true
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	backend, err := NewVaultBackend(VaultConfig{
		Address:           server.URL,
		Token:             "test-token",
		Namespace:         "team-a",
		Mount:             "secret",
		Prefix:            "swarmstr",
		KVVersion:         2,
		AllowInsecureHTTP: true,
	})
	if err != nil {
		t.Fatalf("NewVaultBackend: %v", err)
	}
	if err := backend.Set("mcp-auth", "opaque-secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, found, err := backend.Get("mcp-auth")
	if err != nil || !found || got != "opaque-secret" {
		t.Fatalf("Get=(%q,%v,%v)", got, found, err)
	}
	keys, err := backend.List("plugins")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := strings.Join(keys, ","); got != "alpha,nested/,zeta" {
		t.Fatalf("List keys=%q", got)
	}
	if err := backend.Delete("mcp-auth"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := backend.Get("mcp-auth"); err != nil || found {
		t.Fatalf("deleted Get found=%v err=%v", found, err)
	}
}

func TestVaultBackendListKV1AndErrorResponses(t *testing.T) {
	t.Run("kv1 path and deterministic redacted keys", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "LIST" || r.URL.Path != "/v1/custom/team/plugins" {
				http.Error(w, "unexpected request", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"keys": []string{"nested/", "beta", "alpha"}}})
		}))
		defer server.Close()
		backend, err := NewVaultBackend(VaultConfig{Address: server.URL, Token: "token", Mount: "custom", Prefix: "team", KVVersion: 1, AllowInsecureHTTP: true})
		if err != nil {
			t.Fatal(err)
		}
		keys, err := backend.List("plugins")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(keys, ","); got != "alpha,beta,nested/" {
			t.Fatalf("List keys=%q", got)
		}
	})

	t.Run("authorization and malformed responses fail closed", func(t *testing.T) {
		var status atomic.Int32
		status.Store(http.StatusForbidden)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			current := int(status.Load())
			w.WriteHeader(current)
			if current == http.StatusForbidden {
				_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"keys":`))
		}))
		defer server.Close()
		backend, err := NewVaultBackend(VaultConfig{Address: server.URL, Token: "token", KVVersion: 1, AllowInsecureHTTP: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := backend.List(""); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("expected authorization error, got %v", err)
		}
		status.Store(http.StatusOK)
		if _, err := backend.List(""); err == nil || !strings.Contains(err.Error(), "decode vault response") {
			t.Fatalf("expected malformed response error, got %v", err)
		}
	})
}

func TestVaultBackendFailsClosed(t *testing.T) {
	tests := []VaultConfig{
		{Address: "http://vault.example", Token: "token"},
		{Address: "https://token@vault.example", Token: "token"},
		{Address: "https://vault.example?x=1", Token: "token"},
		{Address: "https://vault.example", Token: ""},
		{Address: "https://vault.example", Token: "token", KVVersion: 3},
		{Address: "https://vault.example", Token: "token", Prefix: "../escape"},
	}
	for _, cfg := range tests {
		if _, err := NewVaultBackend(cfg); err == nil {
			t.Fatalf("unsafe config accepted: %+v", cfg)
		}
	}
	backend, err := NewVaultBackend(VaultConfig{Address: "https://vault.example", Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Set("../escape", "value"); err == nil {
		t.Fatal("path traversal key accepted")
	}
}

func TestVaultBackendRejectsRedirectWithoutLeakingToken(t *testing.T) {
	var leaked string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Vault-Token")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	backend, err := NewVaultBackend(VaultConfig{Address: source.URL, Token: "super-secret", AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	err = backend.Set("key", "value")
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("expected redirect rejection, got %v", err)
	}
	if leaked != "" {
		t.Fatalf("vault token leaked across redirect: %q", leaked)
	}
}
