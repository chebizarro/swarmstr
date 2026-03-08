// Package webui embeds a minimal browser-based chat interface for the local
// swarmstrd gateway.  It is served via an HTTP handler that can be mounted
// alongside the WebSocket gateway runtime.
//
// Usage:
//
//	mux.Handle("/", webui.Handler("/ws", ""))
package webui

import (
	_ "embed"
	"html/template"
	"net/http"
	"strings"
)

//go:embed ui.html
var rawHTML string

// tmplOnce is the parsed template (lazy-initialised on first request).
var uiTemplate *template.Template

func init() {
	uiTemplate = template.Must(template.New("ui").Parse(rawHTML))
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
// The token is embedded in the page and should only be served to localhost
// clients; the ws.Runtime already enforces loopback-only exposure without
// a token, so this matches the existing security posture.
func Handler(wsPath, token string) http.Handler {
	if strings.TrimSpace(wsPath) == "" {
		wsPath = "/ws"
	}
	data := templateData{WSPath: wsPath, Token: token}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only serve from the root path; let unknown paths 404.
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
