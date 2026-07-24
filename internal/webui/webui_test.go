package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	gatewaymethods "metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
)

func TestHandler_ServesHTMLAtRoot(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	cc := w.Header().Get("Cache-Control")
	if cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

func TestHandler_Returns404ForNonRoot(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodGet, "/other", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_Returns405ForPost(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandler_DefaultWSPath(t *testing.T) {
	h := Handler("", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/ws") {
		t.Errorf("response body should contain default /ws path")
	}
}

func TestHandler_IncludesMarkdownRenderingAssets(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"marked.min.js", "purify.min.js", "highlight.min.js", "copy-code-btn", "renderMarkdown"} {
		if !strings.Contains(body, want) {
			t.Errorf("response body should contain %q", want)
		}
	}
}

func TestHandler_IncludesParityRunControlsAndToolActivity(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"id=\"abort-run\"",
		"sessions.abort",
		"sessions.send",
		"sessions.reset",
		"sessions.compact",
		"tool.start",
		"renderToolActivity",
		"plugin.approval.requested",
		"exec.approval.list",
		"approval.resolve",
		"reconcilePendingApprovals",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("response body should contain %q", want)
		}
	}
}

func TestHandler_IncludesMobileResumeRunwayUI(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"id=\"mobile-menu-btn\"",
		"@media (max-width: 900px)",
		"sidebar-open",
		"safe-area-inset-bottom",
		"Resuming session history",
		"loadSessionHistory(sessionID)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("response body should contain %q", want)
		}
	}
}

func TestHandler_IncludesManagementViews(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"data-view=\"dashboard\"",
		"showDashboardView",
		"showAgentsView",
		"showChannelsView",
		"showConfigView",
		"showUsageView",
		"config.put",
		"usage.cost",
		"channels.status",
		"logs.tail",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("response body should contain %q", want)
		}
	}
}

func TestHandler_IncludesThemeLocalizationAndComposerPolish(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"theme-light",
		"locale-select",
		"I18N",
		"slash-menu",
		"slashCommands",
		"attach-input",
		"pendingAttachments",
		"exportChatMarkdown",
		"searchChat",
		"metiq_input_history",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("response body should contain %q", want)
		}
	}
}

func TestHandler_ServesCommandCatalog(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodGet, "/command-catalog.json", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	body := w.Body.String()
	for _, want := range []string{"commands", "/help", "gateway"} {
		if !strings.Contains(body, want) {
			t.Errorf("catalog should contain %q", want)
		}
	}
}

func TestHandler_IncludesExpandedWebUIViewsAndCatalog(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"data-view=\"cron\"", "showCronView", "cron.add", "cron.run", "cron.remove",
		"data-view=\"nodes\"", "showNodesView", "node.describe", "node.invoke", "node.rename",
		"data-view=\"mcp\"", "showMCPView", "mcp.test", "mcp.auth.start", "mcp.reconnect",
		"data-view=\"skills\"", "showSkillsView", "skills.install", "skills.update",
		"sessions.export", "sessions.prune", "/command-catalog.json", "loadCommandCatalog",
		"Relay URLs", "Nostr pubkey", "Agent roster", "Files", "Tools", "Skills",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("response body should contain %q", want)
		}
	}
	for _, unwanted := range []string{
		"nodes.list", "nodes.pending", "nodes.status", "nodes.invoke", "nodes.rename", "nodes.approval.resolve", "channels.reconnect",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("response body should not contain unregistered method %q", unwanted)
		}
	}
}

func TestGatewayMethodCallsitesAreRegistered(t *testing.T) {
	raw, err := os.ReadFile("ui.html")
	if err != nil {
		t.Fatalf("read ui.html: %v", err)
	}
	html := string(raw)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\b(?:callMethod|callSafe)\(\s*['"]([A-Za-z0-9_.-]+)['"]`),
		regexp.MustCompile(`\baddActionButton\([^,\n]+,\s*['"][^'"]+['"]\s*,\s*['"]([A-Za-z0-9_.-]+)['"]`),
	}
	called := map[string]struct{}{}
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(html, -1) {
			called[match[1]] = struct{}{}
		}
	}
	if len(called) == 0 {
		t.Fatal("gateway callsite parser found no methods in ui.html")
	}
	// The only identifier-based occurrences are the callMethod definition and
	// the two helpers above; concrete callsites must remain string literals so
	// this test can prove their registration.
	if got := len(regexp.MustCompile(`\bcallMethod\(\s*[A-Za-z_$]`).FindAllStringIndex(html, -1)); got != 3 {
		t.Fatalf("ui.html introduced a dynamic callMethod callsite (identifier occurrences=%d, want 3)", got)
	}

	supported := map[string]struct{}{}
	for _, method := range gatewaymethods.SupportedMethods() {
		supported[method] = struct{}{}
	}
	// Event subscriptions are handled directly by the WebSocket runtime rather
	// than the application method registry.
	supported[gatewayws.MethodEventsSubscribe] = struct{}{}
	var unregistered []string
	for method := range called {
		if _, ok := supported[method]; !ok {
			unregistered = append(unregistered, method)
		}
	}
	sort.Strings(unregistered)
	if len(unregistered) > 0 {
		t.Fatalf("ui.html calls unregistered gateway methods: %v", unregistered)
	}
}

func TestProtocolV4ChatStreamingContract(t *testing.T) {
	raw, err := os.ReadFile("ui.html")
	if err != nil {
		t.Fatalf("read ui.html: %v", err)
	}
	html := string(raw)
	for _, required := range []string{
		"minProtocol: 4, maxProtocol: 4",
		"scopes: ['operator.admin', 'operator.read', 'operator.write', 'operator.approvals', 'operator.pairing']",
		"hello.features.methodDescriptors",
		"gatewayMethodDescriptors.get(method)",
		"callMethod('events.subscribe'",
		"case 'chat':",
		"payload.runId",
		"payload.sessionKey",
		"payload.seq",
		"payload.state === 'status'",
		"payload.state === 'delta'",
		"payload.state === 'final'",
		"payload.state === 'aborted'",
		"payload.state === 'error'",
		"payload.replace === true",
		"replaceStreaming(payload.deltaText",
	} {
		if !strings.Contains(html, required) {
			t.Errorf("protocol-v4 Web UI missing %q", required)
		}
	}
	for _, legacy := range []string{"case 'chat.chunk':", "case 'turn.result':", "case 'turn.finish':", "case 'turn.error':"} {
		if strings.Contains(html, legacy) {
			t.Errorf("protocol-v4 Web UI still dispatches legacy event %q", legacy)
		}
	}
}

func TestArchitectureRunwayDocumented(t *testing.T) {
	body, err := os.ReadFile("ARCHITECTURE.md")
	if err != nil {
		t.Fatalf("read ARCHITECTURE.md: %v", err)
	}
	for _, want := range []string{"component", "Gateway client", "ToolCard", "embedded handler contract"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("architecture doc should contain %q", want)
		}
	}
}

func TestHandler_TokenIncludedForLoopback(t *testing.T) {
	h := Handler("/ws", "secret-token-123")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "secret-token-123") {
		t.Errorf("response body should contain the token for loopback clients")
	}
}

func TestHandler_TokenBlockedForRemoteClients(t *testing.T) {
	h := Handler("/ws", "secret-token-456")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.1:5678"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "secret-token-456") {
		t.Errorf("response body must NOT contain the token for remote clients")
	}
}

func TestHandler_NoTokenAllowsRemoteClients(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.1:5678"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when no token, got %d", w.Code)
	}
}

func TestHandler_IPv6LoopbackAllowed(t *testing.T) {
	h := Handler("/ws", "token-v6")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::1]:9999"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for ::1, got %d", w.Code)
	}
}

func TestHandler_HeadMethodAllowed(t *testing.T) {
	h := Handler("/ws", "")
	req := httptest.NewRequest(http.MethodHead, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for HEAD, got %d", w.Code)
	}
}

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:1234", true},
		{"127.0.0.1", true},
		{"[::1]:1234", true},
		{"::1", true},
		{"192.168.1.1:1234", false},
		{"10.0.0.1:5678", false},
		{"203.0.113.1:80", false},
		{"not-an-ip:80", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isLoopback(tt.addr)
		if got != tt.want {
			t.Errorf("isLoopback(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}
