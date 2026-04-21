package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
