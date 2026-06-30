// Package webui embeds a minimal browser-based chat interface for the local
// metiqd gateway.  It is served via an HTTP handler that can be mounted
// alongside the WebSocket gateway runtime.
//
// Usage:
//
//	mux.Handle("/", webui.Handler("/ws", ""))
package webui

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"strings"
)

//go:embed ui.html
var rawHTML string

//go:embed components/session_sidebar.html
var sessionSidebarHTML string

//go:embed components/chat_message.html
var chatMessageHTML string

//go:embed components/tool_card.html
var toolCardHTML string

var componentTemplates = map[string]string{
	"session_sidebar": sessionSidebarHTML,
	"chat_message":    chatMessageHTML,
	"tool_card":       toolCardHTML,
}

// tmplOnce is the parsed template (lazy-initialised on first request).
var uiTemplate *template.Template

func init() {
	uiTemplate = template.Must(template.New("ui").Funcs(template.FuncMap{
		"component": func(name string) template.HTML {
			if html, ok := componentTemplates[name]; ok {
				return template.HTML(html)
			}
			return template.HTML("<!-- missing component: " + template.HTMLEscapeString(name) + " -->")
		},
	}).Parse(rawHTML))
}

// Command describes a slash command advertised to the embedded UI.
type Command struct {
	Command string `json:"command"`
	Text    string `json:"text"`
	Source  string `json:"source"`
	Desc    string `json:"desc"`
}

// commandCatalog is intentionally data-driven metadata rather than hardcoded in
// ui.html. Gateway/plugin/skill providers can supersede this through the WS
// command.catalog method; this endpoint is the embedded Web UI fallback.
var commandCatalog = []Command{
	{Command: "/help", Text: "/help", Source: "gateway", Desc: "Show available commands"},
	{Command: "/status", Text: "/status", Source: "gateway", Desc: "Show gateway status"},
	{Command: "/agents", Text: "/agents", Source: "gateway", Desc: "List agents"},
	{Command: "/channels", Text: "/channels", Source: "gateway", Desc: "List channels"},
	{Command: "/compact", Text: "/compact", Source: "gateway", Desc: "Compact current session"},
}

// templateData holds the values injected into the HTML template.
type templateData struct {
	WSPath string // e.g. "/ws"
	Token  string // gateway token (empty → no auth)
}

// Handler returns an http.Handler that serves the embedded chat UI.
//
// wsPath is the WebSocket endpoint path (e.g. "/ws").
// token is the gateway bearer token; pass "" for unauthenticated gateways.
// When a non-empty token is provided, the handler independently verifies
// that the request originates from a loopback address before rendering
// the page.  This prevents the token from leaking to non-local clients
// even if the handler is mounted on a publicly reachable server.
func Handler(wsPath, token string) http.Handler {
	if strings.TrimSpace(wsPath) == "" {
		wsPath = "/ws"
	}
	data := templateData{WSPath: wsPath, Token: token}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/command-catalog.json" {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"commands": commandCatalog})
			return
		}

		// Only serve from the root path; let unknown paths 404.
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// When a token is configured, only serve the page (which embeds
		// the token) to loopback clients.  This is a defense-in-depth
		// measure; the gateway WS runtime also enforces loopback-only
		// exposure, but the webui handler should not rely on external
		// enforcement to protect credentials.
		if data.Token != "" && !isLoopback(r.RemoteAddr) {
			http.Error(w, "forbidden: webui with token is only available from localhost", http.StatusForbidden)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		// Render the template, injecting wsPath and token as JS variables.
		if err := uiTemplate.Execute(w, data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	})
}

// isLoopback returns true if addr is a loopback address (127.x.x.x or ::1).
// addr is expected to be in host:port or host format.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// addr might not have a port; treat entire string as host.
		host = addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
