package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"metiq/internal/agent"
	mcppkg "metiq/internal/mcp"
	secretspkg "metiq/internal/secrets"
	"metiq/internal/store/state"
)

func newTestMCPAuthController(t *testing.T) *mcpAuthController {
	t.Helper()
	store := secretspkg.NewStore(nil)
	store.SetMCPAuthPath(filepath.Join(t.TempDir(), "mcp-auth.json"))
	return newMCPAuthController(nil, nil, store, func() state.ConfigDoc { return state.ConfigDoc{} })
}

func cloneValues(in url.Values) url.Values {
	out := make(url.Values, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func TestMCPAuthControllerStartOAuthFlowStoresCredential(t *testing.T) {
	tokenRequests := make(chan url.Values, 1)
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm error: %v", err)
		}
		tokenRequests <- cloneValues(r.Form)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-a","refresh_token":"refresh-a","token_type":"Bearer","expires_in":3600}`)
	}))
	defer oauthServer.Close()

	controller := newTestMCPAuthController(t)
	cfg := mcppkg.ServerConfig{
		Enabled: true,
		Type:    "http",
		URL:     "https://mcp.example.com/http",
		OAuth: &mcppkg.OAuthConfig{
			Enabled:      true,
			ClientID:     "client-1",
			AuthorizeURL: oauthServer.URL + "/authorize",
			TokenURL:     oauthServer.URL + "/token",
			Scopes:       []string{"profile", "offline_access"},
			UsePKCE:      true,
		},
	}

	started, err := controller.startOAuthFlow("remote", cfg, "", 0)
	if err != nil {
		t.Fatalf("startOAuthFlow error: %v", err)
	}
	if !started.PKCE {
		t.Fatalf("expected PKCE flow")
	}
	authURL, err := url.Parse(started.AuthorizeURL)
	if err != nil {
		t.Fatalf("Parse authorize URL error: %v", err)
	}
	stateToken := authURL.Query().Get("state")
	if strings.TrimSpace(stateToken) == "" {
		t.Fatalf("expected authorize URL to include state")
	}
	resp, err := http.Get(started.CallbackURL + "?code=test-code&state=" + url.QueryEscape(stateToken))
	if err != nil {
		t.Fatalf("callback GET error: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d", resp.StatusCode)
	}
	form := <-tokenRequests
	if got := form.Get("grant_type"); got != "authorization_code" {
		t.Fatalf("grant_type = %q", got)
	}
	if got := form.Get("code"); got != "test-code" {
		t.Fatalf("code = %q", got)
	}
	if got := form.Get("code_verifier"); strings.TrimSpace(got) == "" {
		t.Fatalf("expected code_verifier in token exchange")
	}
	cred, ok := controller.secrets.GetMCPCredential(mcppkg.CredentialKey(cfg))
	if !ok {
		t.Fatalf("expected stored credential")
	}
	if cred.AccessToken != "access-a" || cred.RefreshToken != "refresh-a" {
		t.Fatalf("unexpected stored credential: %#v", cred)
	}
}

func TestMCPAuthControllerAuthorizationHeadersRefreshExpiredCredential(t *testing.T) {
	tokenRequests := make(chan url.Values, 1)
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm error: %v", err)
		}
		tokenRequests <- cloneValues(r.Form)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-b","refresh_token":"refresh-b","token_type":"Bearer","expires_in":7200}`)
	}))
	defer oauthServer.Close()

	controller := newTestMCPAuthController(t)
	cfg := mcppkg.ServerConfig{
		Enabled: true,
		Type:    "http",
		URL:     "https://mcp.example.com/http",
		OAuth: &mcppkg.OAuthConfig{
			Enabled:      true,
			ClientID:     "client-1",
			AuthorizeURL: oauthServer.URL + "/authorize",
			TokenURL:     oauthServer.URL + "/token",
		},
	}
	if err := controller.secrets.PutMCPCredential(mcppkg.CredentialKey(cfg), secretspkg.MCPAuthCredential{
		AccessToken:  "expired-token",
		RefreshToken: "refresh-a",
		TokenType:    "Bearer",
		Expiry:       time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("PutMCPCredential error: %v", err)
	}
	headers, err := controller.authorizationHeaders(context.Background(), "remote", cfg)
	if err != nil {
		t.Fatalf("authorizationHeaders error: %v", err)
	}
	if got := headers["Authorization"]; got != "Bearer access-b" {
		t.Fatalf("Authorization header = %q", got)
	}
	form := <-tokenRequests
	if got := form.Get("grant_type"); got != "refresh_token" {
		t.Fatalf("grant_type = %q", got)
	}
	stored, ok := controller.secrets.GetMCPCredential(mcppkg.CredentialKey(cfg))
	if !ok {
		t.Fatalf("expected refreshed credential to be stored")
	}
	if stored.AccessToken != "access-b" || stored.RefreshToken != "refresh-b" {
		t.Fatalf("unexpected refreshed credential: %#v", stored)
	}
}

func TestMCPAuthControllerRefreshCredentialForcesRefreshGrant(t *testing.T) {
	tokenRequests := make(chan url.Values, 1)
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm error: %v", err)
		}
		tokenRequests <- cloneValues(r.Form)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-c","refresh_token":"refresh-c","token_type":"Bearer","expires_in":1800}`)
	}))
	defer oauthServer.Close()

	controller := newTestMCPAuthController(t)
	cfg := mcppkg.ServerConfig{
		Enabled: true,
		Type:    "http",
		URL:     "https://mcp.example.com/http",
		OAuth: &mcppkg.OAuthConfig{
			Enabled:      true,
			ClientID:     "client-1",
			AuthorizeURL: oauthServer.URL + "/authorize",
			TokenURL:     oauthServer.URL + "/token",
		},
	}
	current := secretspkg.MCPAuthCredential{
		AccessToken:  "still-valid",
		RefreshToken: "refresh-a",
		TokenType:    "Bearer",
		Expiry:       time.Now().UTC().Add(time.Hour),
	}
	updated, err := controller.refreshCredential(context.Background(), "remote", cfg, current, false)
	if err != nil {
		t.Fatalf("refreshCredential error: %v", err)
	}
	form := <-tokenRequests
	if got := form.Get("grant_type"); got != "refresh_token" {
		t.Fatalf("grant_type = %q", got)
	}
	if got := form.Get("refresh_token"); got != "refresh-a" {
		t.Fatalf("refresh_token = %q", got)
	}
	if updated.AccessToken != "access-c" || updated.RefreshToken != "refresh-c" {
		t.Fatalf("unexpected refreshed credential: %#v", updated)
	}
}

func TestMCPAuthControllerClearCredential(t *testing.T) {
	controller := newTestMCPAuthController(t)
	cfg := mcppkg.ServerConfig{
		Enabled: true,
		Type:    "http",
		URL:     "https://mcp.example.com/http",
		OAuth:   &mcppkg.OAuthConfig{Enabled: true, ClientID: "client-1", AuthorizeURL: "https://mcp.example.com/authorize", TokenURL: "https://mcp.example.com/token"},
	}
	if err := controller.secrets.PutMCPCredential(mcppkg.CredentialKey(cfg), secretspkg.MCPAuthCredential{AccessToken: "token-a"}); err != nil {
		t.Fatalf("PutMCPCredential error: %v", err)
	}
	deleted, err := controller.clearCredential("remote", cfg)
	if err != nil {
		t.Fatalf("clearCredential error: %v", err)
	}
	if !deleted {
		t.Fatalf("expected deleted=true")
	}
	if _, ok := controller.secrets.GetMCPCredential(mcppkg.CredentialKey(cfg)); ok {
		t.Fatalf("expected credential to be cleared")
	}
}

func TestMCPAuthControllerClearCredentialForcesReconnectOnLiveManager(t *testing.T) {
	controller := newTestMCPAuthController(t)
	var mgr *mcppkg.Manager
	tools := agent.NewToolRegistry()
	controller = newMCPAuthController(&mgr, tools, controller.secrets, func() state.ConfigDoc {
		return state.ConfigDoc{Extra: map[string]any{
			"mcp": map[string]any{
				"enabled": true,
				"servers": map[string]any{
					"remote": map[string]any{
						"enabled": true,
						"type":    "http",
						"url":     "https://mcp.example.com/http",
						"oauth": map[string]any{
							"enabled":       true,
							"client_id":     "client-1",
							"authorize_url": "https://mcp.example.com/authorize",
							"token_url":     "https://mcp.example.com/token",
						},
					},
				},
			},
		}}
	})
	mgr = mcppkg.NewManager()
	controller.InstallOnManager(mgr)
	var attempts atomic.Int32
	mgr.SetConnectFunc(func(_ context.Context, name string, _ mcppkg.ServerConfig) (*mcppkg.ServerConnection, error) {
		attempts.Add(1)
		return &mcppkg.ServerConnection{Name: name, Tools: []*mcp.Tool{{Name: "echo"}}, Capabilities: mcppkg.CapabilitySnapshot{Tools: true}}, nil
	})
	resolved := mcppkg.ResolveConfigDoc(controller.configDoc())
	if err := controller.secrets.PutMCPCredential(mcppkg.CredentialKey(resolved.Servers["remote"].ServerConfig), secretspkg.MCPAuthCredential{AccessToken: "token-a"}); err != nil {
		t.Fatalf("PutMCPCredential error: %v", err)
	}
	if err := mgr.ApplyConfig(context.Background(), resolved); err != nil {
		t.Fatalf("ApplyConfig error: %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected initial connect attempt, got %d", attempts.Load())
	}
	deleted, err := controller.clearCredential("remote", resolved.Servers["remote"].ServerConfig)
	if err != nil {
		t.Fatalf("clearCredential error: %v", err)
	}
	if !deleted {
		t.Fatalf("expected deleted=true")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if attempts.Load() >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected reconnect after clear, attempts=%d", attempts.Load())
}
