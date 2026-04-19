package canvas

import (
	"context"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ─── Constants ──────────────────────────────────────────────────────────────

const (
	// DefaultBasePath is the URL prefix under which the canvas handler mounts.
	DefaultBasePath = "/__swarmstr__/canvas"
	// WSPath is the WebSocket endpoint for live reload notifications.
	WSPath = "/__swarmstr__/ws"
	// DefaultReloadDebounce is the delay before broadcasting a reload after
	// a file-system or canvas change.
	DefaultReloadDebounce = 75 * time.Millisecond
)

// ─── Handler ────────────────────────────────────────────────────────────────

// HandlerOpts configures a canvas HTTP handler.
type HandlerOpts struct {
	// Host is the canvas CRUD store.  When non-nil the handler subscribes to
	// updates and broadcasts live-reload events.
	Host *Host
	// RootDir is the filesystem directory from which static files are served.
	// If empty the handler only serves in-memory canvases via the Host.
	RootDir string
	// BasePath overrides the URL prefix.  Defaults to DefaultBasePath.
	BasePath string
	// LiveReload enables WebSocket-based live reload.  Defaults to true.
	LiveReload *bool
}

// Handler serves canvas content over HTTP and optionally provides live
// reload via a WebSocket endpoint.  It implements http.Handler so it can
// be mounted directly on an http.ServeMux.
type Handler struct {
	host       *Host
	rootDir    string
	basePath   string
	wsPath     string
	liveReload bool

	mu      sync.Mutex
	conns   map[*websocket.Conn]struct{}
	timer   *time.Timer
	closed  bool
}

// NewHandler creates a new canvas HTTP handler.
func NewHandler(opts HandlerOpts) *Handler {
	basePath := strings.TrimRight(strings.TrimSpace(opts.BasePath), "/")
	if basePath == "" {
		basePath = DefaultBasePath
	}
	lr := true
	if opts.LiveReload != nil {
		lr = *opts.LiveReload
	}
	h := &Handler{
		host:       opts.Host,
		rootDir:    filepath.Clean(opts.RootDir),
		basePath:   basePath,
		wsPath:     WSPath,
		liveReload: lr,
		conns:      make(map[*websocket.Conn]struct{}),
	}

	// Subscribe to canvas updates if a host is provided.
	if h.host != nil && lr {
		h.host.Subscribe(func(_ UpdateEvent) {
			h.scheduleReload()
		})
	}
	return h
}

// Close shuts down the handler and disconnects all WebSocket clients.
func (h *Handler) Close() {
	h.mu.Lock()
	h.closed = true
	if h.timer != nil {
		h.timer.Stop()
	}
	conns := make([]*websocket.Conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()

	for _, c := range conns {
		c.CloseNow()
	}
}

// ServeHTTP dispatches incoming requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path

	// WebSocket upgrade.
	if urlPath == h.wsPath {
		h.handleWS(w, r)
		return
	}

	// Check base path prefix.
	if urlPath != h.basePath && !strings.HasPrefix(urlPath, h.basePath+"/") {
		http.NotFound(w, r)
		return
	}

	// Strip base path.
	rel := "/"
	if urlPath != h.basePath {
		rel = urlPath[len(h.basePath):]
	}
	if rel == "" {
		rel = "/"
	}

	// Only GET and HEAD.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		fmt.Fprint(w, "Method Not Allowed")
		return
	}

	h.serveFile(w, r, rel)
}

// ─── File serving ───────────────────────────────────────────────────────────

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, urlPath string) {
	if h.rootDir == "" || h.rootDir == "." {
		http.NotFound(w, r)
		return
	}

	resolved, ok := resolveFileWithinRoot(h.rootDir, urlPath)
	if !ok {
		if urlPath == "/" || strings.HasSuffix(urlPath, "/") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Swarmstr Canvas</title><pre>Missing file.
Create %s/index.html</pre>`, h.rootDir)
			return
		}
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	ct := detectContentType(resolved)
	w.Header().Set("Cache-Control", "no-store")

	if ct == "text/html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if h.liveReload {
			data = []byte(InjectLiveReloadScript(string(data), h.wsPath))
		}
		w.Write(data)
		return
	}

	w.Header().Set("Content-Type", ct)
	w.Write(data)
}

// ─── WebSocket live reload ──────────────────────────────────────────────────

func (h *Handler) handleWS(w http.ResponseWriter, r *http.Request) {
	if !h.liveReload {
		http.NotFound(w, r)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // canvas is local/trusted
	})
	if err != nil {
		return
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		c.CloseNow()
		return
	}
	h.conns[c] = struct{}{}
	h.mu.Unlock()

	// Block until the client disconnects.
	ctx := r.Context()
	for {
		_, _, err := c.Read(ctx)
		if err != nil {
			break
		}
	}

	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
	c.CloseNow()
}

func (h *Handler) scheduleReload() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	if h.timer != nil {
		h.timer.Stop()
	}
	h.timer = time.AfterFunc(DefaultReloadDebounce, func() {
		h.broadcastReload()
	})
}

func (h *Handler) broadcastReload() {
	h.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()

	for _, c := range conns {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = c.Write(ctx, websocket.MessageText, []byte("reload"))
		cancel()
	}
}

// ─── File resolution ────────────────────────────────────────────────────────

// resolveFileWithinRoot safely resolves a URL path to a file within rootDir.
// It prevents path traversal, rejects symlinks, and handles index.html
// fallback for directories.  Returns the resolved real path and true on
// success, or empty string and false otherwise.
func resolveFileWithinRoot(rootDir, urlPath string) (string, bool) {
	// Normalize: decode and clean the path.
	cleaned := path.Clean("/" + urlPath)
	rel := strings.TrimPrefix(cleaned, "/")
	if rel == "" {
		rel = "."
	}

	// Reject path traversal.
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", false
		}
	}

	candidate := filepath.Join(rootDir, filepath.FromSlash(rel))

	// Verify the resolved path is still within root.
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return "", false
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", false
	}
	if !strings.HasPrefix(absCandidate, absRoot+string(filepath.Separator)) && absCandidate != absRoot {
		return "", false
	}

	info, err := os.Lstat(candidate)
	if err != nil {
		return "", false
	}

	// Reject symlinks.
	if info.Mode()&fs.ModeSymlink != 0 {
		return "", false
	}

	// Directory → try index.html.
	if info.IsDir() {
		indexPath := filepath.Join(candidate, "index.html")
		if idxInfo, err := os.Lstat(indexPath); err == nil && !idxInfo.IsDir() && idxInfo.Mode()&fs.ModeSymlink == 0 {
			return indexPath, true
		}
		return "", false
	}

	return candidate, true
}

// ─── MIME detection ─────────────────────────────────────────────────────────

func detectContentType(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js", ".mjs":
		return "application/javascript"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".md":
		return "text/markdown"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// ─── Live reload injection ──────────────────────────────────────────────────

// InjectLiveReloadScript injects a WebSocket-based live reload <script>
// snippet into an HTML string, just before the closing </body> tag.
func InjectLiveReloadScript(html, wsPath string) string {
	snippet := fmt.Sprintf(`
<script>
(() => {
  try {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(proto + "://" + location.host + %q);
    ws.onmessage = (ev) => {
      if (String(ev.data || "") === "reload") location.reload();
    };
    ws.onclose = () => setTimeout(() => location.reload(), 2000);
  } catch {}
})();
</script>`, wsPath)

	idx := strings.LastIndex(strings.ToLower(html), "</body>")
	if idx >= 0 {
		return html[:idx] + "\n" + strings.TrimSpace(snippet) + "\n" + html[idx:]
	}
	return html + "\n" + strings.TrimSpace(snippet) + "\n"
}

// DefaultIndexHTML returns a minimal default canvas landing page.
func DefaultIndexHTML() string {
	return `<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Swarmstr Canvas</title>
<style>
  html, body { height: 100%; margin: 0; background: #0a0a0a; color: #e0e0e0; font: 16px/1.5 system-ui, -apple-system, sans-serif; }
  .wrap { min-height: 100%; display: grid; place-items: center; padding: 24px; }
  .card { width: min(640px, 100%); background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.08); border-radius: 12px; padding: 24px; }
  h1 { margin: 0 0 8px; font-size: 20px; }
  .sub { opacity: 0.6; font-size: 14px; }
</style>
<body>
<div class="wrap">
  <div class="card">
    <h1>⚡ Swarmstr Canvas</h1>
    <div class="sub">Live-reload enabled. Waiting for canvas updates…</div>
  </div>
</div>
</body>
`
}
