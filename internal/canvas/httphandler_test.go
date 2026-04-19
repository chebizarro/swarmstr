package canvas_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/canvas"

	"github.com/coder/websocket"
)

// ─── File resolution ────────────────────────────────────────────────────────

func setupRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "<h1>Home</h1>")
	writeFile(t, dir, "style.css", "body{}")
	writeFile(t, dir, "data.json", `{"ok":true}`)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	writeFile(t, dir, "sub/page.html", "<p>sub</p>")
	writeFile(t, dir, "sub/index.html", "<p>sub-index</p>")
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHandler_ServesIndexHTML(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<h1>Home</h1>") {
		t.Fatalf("body = %q", body)
	}
}

func TestHandler_ServesCSS(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/style.css", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/css" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestHandler_ServesJSON(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/data.json", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestHandler_SubdirectoryIndex(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/sub/", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "sub-index") {
		t.Fatalf("expected sub index.html content")
	}
}

func TestHandler_SubdirectoryFile(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/sub/page.html", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<p>sub</p>") {
		t.Fatalf("body = %q", rr.Body.String())
	}
}

func TestHandler_PathTraversalBlocked(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/../../../etc/passwd", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 404 {
		t.Fatalf("expected 404 for path traversal, got %d", rr.Code)
	}
}

func TestHandler_NotFoundFile(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/nonexistent.txt", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 404 {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", canvas.DefaultBasePath+"/index.html", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 405 {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestHandler_HeadMethod(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("HEAD", canvas.DefaultBasePath+"/style.css", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandler_OutsideBasePath(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/other/path", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 404 {
		t.Fatalf("expected 404 for outside base path, got %d", rr.Code)
	}
}

func TestHandler_CustomBasePath(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root, BasePath: "/my-canvas"})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/my-canvas/index.html", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<h1>Home</h1>") {
		t.Fatalf("body = %q", rr.Body.String())
	}
}

func TestHandler_NoRootDir(t *testing.T) {
	h := canvas.NewHandler(canvas.HandlerOpts{})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/anything", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 404 {
		t.Fatalf("expected 404 with no root dir, got %d", rr.Code)
	}
}

func TestHandler_NoCache(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/data.json", nil)
	h.ServeHTTP(rr, req)

	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
}

// ─── Live reload injection ──────────────────────────────────────────────────

func TestInjectLiveReloadScript_BeforeBody(t *testing.T) {
	html := "<html><body><p>hello</p></body></html>"
	result := canvas.InjectLiveReloadScript(html, canvas.WSPath)

	if !strings.Contains(result, "<script>") {
		t.Fatal("expected script injection")
	}
	if !strings.Contains(result, canvas.WSPath) {
		t.Fatal("expected WS path in script")
	}
	// Script should appear before </body>.
	idx := strings.Index(result, "<script>")
	bodyIdx := strings.LastIndex(result, "</body>")
	if idx >= bodyIdx {
		t.Fatal("script should be before </body>")
	}
}

func TestInjectLiveReloadScript_NoBody(t *testing.T) {
	html := "<p>hello</p>"
	result := canvas.InjectLiveReloadScript(html, canvas.WSPath)
	if !strings.Contains(result, "<script>") {
		t.Fatal("expected script appended")
	}
}

func TestHandler_HTMLInjectsLiveReload(t *testing.T) {
	root := setupRoot(t)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/", nil)
	h.ServeHTTP(rr, req)

	if !strings.Contains(rr.Body.String(), "<script>") {
		t.Fatal("expected live reload script in HTML response")
	}
}

func TestHandler_LiveReloadDisabled(t *testing.T) {
	root := setupRoot(t)
	lr := false
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root, LiveReload: &lr})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/", nil)
	h.ServeHTTP(rr, req)

	if strings.Contains(rr.Body.String(), "<script>") {
		t.Fatal("expected no live reload script when disabled")
	}
}

// ─── WebSocket live reload ──────────────────────────────────────────────────

func TestHandler_WebSocketReload(t *testing.T) {
	root := setupRoot(t)
	host := canvas.NewHost()
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root, Host: host})
	defer h.Close()

	srv := httptest.NewServer(h)
	defer srv.Close()

	// Connect WebSocket.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + canvas.WSPath
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer c.CloseNow()

	// Trigger a canvas update.
	if err := host.UpdateCanvas("test", "html", "<p>updated</p>"); err != nil {
		t.Fatal(err)
	}

	// Read the reload message (with timeout).
	readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readCancel()

	_, data, err := c.Read(readCtx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if string(data) != "reload" {
		t.Fatalf("expected 'reload', got %q", string(data))
	}
}

func TestHandler_WebSocketDisabledReturns404(t *testing.T) {
	root := setupRoot(t)
	lr := false
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root, LiveReload: &lr})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.WSPath, nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 404 {
		t.Fatalf("expected 404 when live reload disabled, got %d", rr.Code)
	}
}

// ─── DefaultIndexHTML ───────────────────────────────────────────────────────

func TestDefaultIndexHTML(t *testing.T) {
	html := canvas.DefaultIndexHTML()
	if !strings.Contains(html, "Swarmstr Canvas") {
		t.Fatal("expected 'Swarmstr Canvas' in default index")
	}
	if !strings.Contains(html, "<body>") {
		t.Fatal("expected <body> in default index")
	}
}

// ─── MissingRootIndex ───────────────────────────────────────────────────────

func TestHandler_MissingRootIndexShowsHint(t *testing.T) {
	dir := t.TempDir() // empty directory, no index.html
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: dir})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 404 {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "index.html") {
		t.Fatalf("expected hint about index.html, got %q", rr.Body.String())
	}
}

// ─── Symlink rejection ──────────────────────────────────────────────────────

func TestHandler_SymlinkRejected(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	writeFile(t, target, "secret.txt", "secret-data")

	// Create a symlink from root/link.txt → target/secret.txt.
	if err := os.Symlink(filepath.Join(target, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symlinks not supported:", err)
	}

	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/link.txt", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 404 {
		t.Fatalf("expected 404 for symlink, got %d", rr.Code)
	}
}

// ─── Benchmark ──────────────────────────────────────────────────────────────

func BenchmarkHandler_ServeFile(b *testing.B) {
	root := b.TempDir()
	os.WriteFile(filepath.Join(root, "bench.html"), []byte("<p>bench</p>"), 0o644)
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root})
	defer h.Close()

	req := httptest.NewRequest("GET", canvas.DefaultBasePath+"/bench.html", nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}
}

// ─── Integration: canvas host + HTTP handler ────────────────────────────────

func TestHandler_CanvasHostHTTPIntegration(t *testing.T) {
	root := setupRoot(t)
	host := canvas.NewHost()
	h := canvas.NewHandler(canvas.HandlerOpts{RootDir: root, Host: host})
	defer h.Close()

	srv := httptest.NewServer(h)
	defer srv.Close()

	// Serve static file.
	resp, err := http.Get(srv.URL + canvas.DefaultBasePath + "/style.css")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("static file status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "body{}" {
		t.Fatalf("unexpected body: %q", body)
	}
}
