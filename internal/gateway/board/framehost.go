package board

import (
	"net/http"
	"net/url"
	"strings"
)

// FrameHandler serves the byte-frozen HTML of renderable board widgets at
// GET HTTPPathPrefix<sessionKey>/<name>/index.html?bt=<view ticket>,
// mirroring OpenClaw src/gateway/board-http.ts. The view ticket is the sole
// authorization boundary: it must verify, be fresh, and resolve to exactly
// the widget named in the path. Responses carry a deny-by-default sandbox
// Content-Security-Policy whose connect-src widens only to the granted
// declared network origins.

type frameHandler struct {
	store *Store
}

// NewFrameHandler returns the HTTP handler for the board widget frame host.
func NewFrameHandler(store *Store) http.Handler {
	return frameHandler{store: store}
}

// buildWidgetCSP mirrors OpenClaw buildBoardWidgetContentSecurityPolicy:
// defense in depth on top of the iframe sandbox, with network access only
// for granted declared origins.
func buildWidgetCSP(view AuthorizedView) string {
	connect := "'none'"
	if view.GrantState == GrantGranted && view.Declared != nil && len(view.Declared.NetOrigins) > 0 {
		connect = strings.Join(view.Declared.NetOrigins, " ")
	}
	return strings.Join([]string{
		"default-src 'none'",
		"script-src 'unsafe-inline'",
		"style-src 'unsafe-inline'",
		"img-src data:",
		"connect-src " + connect,
		"webrtc 'block'",
		"base-uri 'none'",
		"object-src 'none'",
		"form-action 'none'",
		"frame-src 'none'",
		"sandbox allow-scripts",
	}, "; ")
}

// parseFramePath extracts and validates the sessionKey and widget name from
// an escaped request path. It parses the escaped form so session keys with
// encoded separators cannot smuggle extra path segments.
func parseFramePath(escapedPath string) (sessionKey, name string, ok bool) {
	if !strings.HasPrefix(escapedPath, HTTPPathPrefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(escapedPath, HTTPPathPrefix), "/")
	if len(parts) != 3 || parts[2] != "index.html" {
		return "", "", false
	}
	sessionKey, err := url.PathUnescape(parts[0])
	if err != nil || sessionKey == "" {
		return "", "", false
	}
	name, err = url.PathUnescape(parts[1])
	if err != nil || !widgetNamePattern.MatchString(name) {
		return "", "", false
	}
	return sessionKey, name, true
}

func (h frameHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The ticket is the authorization boundary. CORS lets a Control UI hosted
	// away from the gateway fetch the bytes and observe authorization
	// failures.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionKey, name, ok := parseFramePath(r.URL.EscapedPath())
	if !ok {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	ticket := r.URL.Query().Get("bt")
	if ticket == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	view, err := h.store.ResolveViewTicket(ticket)
	if err != nil || view.SessionKey != sessionKey || view.Name != name {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	header := w.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Content-Security-Policy", buildWidgetCSP(view))
	header.Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(view.HTML))
}
