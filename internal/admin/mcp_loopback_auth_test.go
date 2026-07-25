package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentpkg "metiq/internal/agent"
	attachpkg "metiq/internal/gateway/attach"
	mcppkg "metiq/internal/mcp"
)

// ── withMCPLoopbackAuth ─────────────────────────────────────────────────────

func loopbackAuthProbe(t *testing.T, adminToken string, resolver func(string) (string, bool), authHeader string) (int, *mcpAuthScope, string) {
	t.Helper()
	var scope *mcpAuthScope
	var boundSession string
	handler := withMCPLoopbackAuth(adminToken, resolver, func(w http.ResponseWriter, r *http.Request) {
		if s, ok := mcpScopeFromContext(r.Context()); ok {
			cp := s
			scope = &cp
		}
		boundSession = agentpkg.SessionIDFromContext(mcpLoopbackCallContext(r))
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec.Code, scope, boundSession
}

func TestWithMCPLoopbackAuth_AdminTokenUnscoped(t *testing.T) {
	code, scope, session := loopbackAuthProbe(t, "admin-tok", nil, "Bearer admin-tok")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if scope == nil || !scope.admin || scope.sessionKey != "" {
		t.Fatalf("unexpected scope: %+v", scope)
	}
	if session != "" {
		t.Fatalf("admin scope should not bind a session, got %q", session)
	}
}

func TestWithMCPLoopbackAuth_GrantTokenScoped(t *testing.T) {
	resolver := func(token string) (string, bool) {
		if token == "grant-tok" {
			return "sess-42", true
		}
		return "", false
	}
	code, scope, session := loopbackAuthProbe(t, "admin-tok", resolver, "Bearer grant-tok")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	// A grant must never escalate to admin scope.
	if scope == nil || scope.admin {
		t.Fatalf("grant escalated to admin scope: %+v", scope)
	}
	if scope.sessionKey != "sess-42" {
		t.Fatalf("sessionKey = %q, want sess-42", scope.sessionKey)
	}
	// The dispatch context must carry the grant's session key.
	if session != "sess-42" {
		t.Fatalf("bound session = %q, want sess-42", session)
	}
}

func TestWithMCPLoopbackAuth_RejectsInvalidBearer(t *testing.T) {
	deny := func(string) (string, bool) { return "", false }
	cases := []struct {
		name       string
		adminToken string
		resolver   func(string) (string, bool)
		header     string
	}{
		{"wrong admin token no resolver", "admin-tok", nil, "Bearer nope"},
		{"unknown grant token", "admin-tok", deny, "Bearer expired-or-revoked"},
		{"grant token but resolver nil", "admin-tok", nil, "Bearer grant-tok"},
		{"malformed auth header", "admin-tok", deny, "admin-tok"},
		{"bearer against tokenless bind", "", deny, "Bearer anything"},
		{"empty-session grant resolution", "admin-tok", func(string) (string, bool) { return "  ", true }, "Bearer grant-tok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, scope, session := loopbackAuthProbe(t, tc.adminToken, tc.resolver, tc.header)
			if code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", code)
			}
			if scope != nil || session != "" {
				t.Fatalf("rejected request leaked scope: %+v session=%q", scope, session)
			}
		})
	}
}

func TestWithMCPLoopbackAuth_TokenlessBindAllowsCredentialless(t *testing.T) {
	// Parity with withAuth: a tokenless (loopback-only) bind accepts requests
	// that present no credential at all.
	code, scope, _ := loopbackAuthProbe(t, "", nil, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if scope == nil || !scope.admin {
		t.Fatalf("unexpected scope: %+v", scope)
	}
}

// ── Mounted /mcp with a real attach grant store ─────────────────────────────

func TestMountMCPLoopback_GrantLifecycle(t *testing.T) {
	store := attachpkg.NewStore()
	mgr := mcppkg.NewManager()
	defer mgr.Close()

	mux := http.NewServeMux()
	mountMCPLoopback(mux, ServerOptions{
		Token:      "admin-tok",
		MCPManager: mgr,
		AttachGrantResolver: func(token string) (string, bool) {
			grant, ok := store.Resolve(token)
			if !ok {
				return "", false
			}
			return grant.SessionKey, true
		},
	})

	initialize := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	grant, err := store.Mint("sess-1", time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	if code := initialize(grant.Token); code != http.StatusOK {
		t.Fatalf("live grant rejected: %d", code)
	}
	if code := initialize("admin-tok"); code != http.StatusOK {
		t.Fatalf("admin token rejected: %d", code)
	}
	if code := initialize(""); code != http.StatusUnauthorized {
		t.Fatalf("credential-less request accepted: %d", code)
	}
	if code := initialize("bogus"); code != http.StatusUnauthorized {
		t.Fatalf("bogus token accepted: %d", code)
	}

	// Revocation takes effect on the very next call (per-request resolution).
	if !store.Revoke(grant.Token) {
		t.Fatal("revoke failed")
	}
	if code := initialize(grant.Token); code != http.StatusUnauthorized {
		t.Fatalf("revoked grant accepted: %d", code)
	}

	// Expired grants are rejected too.
	now := time.Now()
	expStore := attachpkg.NewStore(attachpkg.Options{Now: func() time.Time { return now }})
	expGrant, err := expStore.Mint("sess-2", time.Second)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	now = now.Add(2 * time.Second)
	if _, ok := expStore.Resolve(expGrant.Token); ok {
		t.Fatal("expired grant resolved")
	}
}

// ── MCPAttachGrantServerConfig ──────────────────────────────────────────────

func TestMCPAttachGrantServerConfig_TokenBackedHeadersOnly(t *testing.T) {
	cfg := MCPAttachGrantServerConfig(23119)
	entry := cfg["mcpServers"].(map[string]any)["metiq"].(map[string]any)
	if entry["url"] != "http://127.0.0.1:23119/mcp" {
		t.Fatalf("unexpected url: %v", entry["url"])
	}
	headers := entry["headers"].(map[string]string)
	if len(headers) != 1 || headers["Authorization"] != "Bearer ${METIQ_MCP_TOKEN}" {
		t.Fatalf("attach grant config must carry only the token-backed Authorization header, got %v", headers)
	}
}

// ── Start publishes and clears the loopback runtime ─────────────────────────

func TestStart_SetsAndClearsMCPLoopbackRuntime(t *testing.T) {
	// Ensure clean global state.
	activeMCPLoopbackMu.Lock()
	activeMCPLoopbackRuntime = nil
	activeMCPLoopbackMu.Unlock()

	store := attachpkg.NewStore()
	mgr := mcppkg.NewManager()
	defer mgr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Start(ctx, ServerOptions{
			Addr:       "127.0.0.1:0",
			Token:      "admin-tok",
			MCPManager: mgr,
			AttachGrantResolver: func(token string) (string, bool) {
				grant, ok := store.Resolve(token)
				if !ok {
					return "", false
				}
				return grant.SessionKey, true
			},
		})
	}()

	var rt *MCPLoopbackRuntime
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if rt = GetActiveMCPLoopbackRuntime(); rt != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rt == nil {
		t.Fatal("active MCP loopback runtime was never published")
	}
	if rt.Port <= 0 {
		t.Fatalf("runtime port = %d, want the bound listener port", rt.Port)
	}

	// The published runtime serves /mcp: grant tokens work, garbage does not.
	grant, err := store.Mint("sess-live", time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	post := func(token string) int {
		req, err := http.NewRequest(http.MethodPost,
			fmt.Sprintf("http://127.0.0.1:%d/mcp", rt.Port),
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(grant.Token); code != http.StatusOK {
		t.Fatalf("grant token against live runtime: %d, want 200", code)
	}
	if code := post("garbage"); code != http.StatusUnauthorized {
		t.Fatalf("garbage token against live runtime: %d, want 401", code)
	}

	// Shutdown clears the runtime so attach.grant fails closed again.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not exit after cancel")
	}
	if got := GetActiveMCPLoopbackRuntime(); got != nil {
		t.Fatalf("runtime still active after shutdown: %+v", got)
	}
}
