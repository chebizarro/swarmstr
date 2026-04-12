package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"metiq/internal/store/state"
)

// ─── parseLimit ───────────────────────────────────────────────────────────────

func TestParseLimit(t *testing.T) {
	cases := []struct {
		raw      string
		def, max int
		want     int
	}{
		{"", 10, 100, 10},
		{"5", 10, 100, 5},
		{"0", 10, 100, 10},
		{"-1", 10, 100, 10},
		{"200", 10, 100, 100},
		{"  50  ", 10, 100, 50},
		{"abc", 10, 100, 10},
	}
	for _, c := range cases {
		got := parseLimit(c.raw, c.def, c.max)
		if got != c.want {
			t.Errorf("parseLimit(%q, %d, %d) = %d, want %d", c.raw, c.def, c.max, got, c.want)
		}
	}
}

// ─── handleStateError ────────────────────────────────────────────────────────

func TestHandleStateError_NotFound(t *testing.T) {
	w := httptest.NewRecorder()
	handleStateError(w, state.ErrNotFound)
	if w.Code != http.StatusNotFound {
		t.Errorf("code: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not found") {
		t.Errorf("body: %s", w.Body.String())
	}
}

func TestHandleStateError_Internal(t *testing.T) {
	w := httptest.NewRecorder()
	handleStateError(w, errors.New("some error"))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("code: %d", w.Code)
	}
}

// ─── validateExposure ─────────────────────────────────────────────────────────

func TestValidateExposure_Loopback(t *testing.T) {
	if err := validateExposure("127.0.0.1:8080", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateExposure("localhost:8080", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateExposure("[::1]:8080", ""); err != nil {
		t.Fatal(err)
	}
}

func TestValidateExposure_NonLoopbackNoToken(t *testing.T) {
	err := validateExposure("0.0.0.0:8080", "")
	if err == nil {
		t.Error("expected error for non-loopback without token")
	}
}

func TestValidateExposure_NonLoopbackWithToken(t *testing.T) {
	if err := validateExposure("0.0.0.0:8080", "my-token"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateExposure_InvalidAddr(t *testing.T) {
	err := validateExposure("badaddr", "")
	if err == nil {
		t.Error("expected error for bad address")
	}
}

// ─── withAuth ─────────────────────────────────────────────────────────────────

func TestWithAuth_NoToken(t *testing.T) {
	called := false
	handler := withAuth("", func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	if !called {
		t.Error("empty token should pass through")
	}
}

func TestWithAuth_ValidToken(t *testing.T) {
	called := false
	handler := withAuth("secret", func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler(w, req)
	if !called {
		t.Error("valid token should call handler")
	}
}

func TestWithAuth_InvalidToken(t *testing.T) {
	handler := withAuth("secret", func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not be called")
	})
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("code: %d", w.Code)
	}
}

func TestWithAuth_MissingAuthHeader(t *testing.T) {
	handler := withAuth("secret", func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not be called")
	})
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("code: %d", w.Code)
	}
}

// ─── truncateRunes ────────────────────────────────────────────────────────────

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		input    string
		maxRunes int
		want     string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel"},
		{"hello", 0, ""},
		{"日本語", 2, "日本"},
		{"", 5, ""},
	}
	for _, c := range cases {
		got := truncateRunes(c.input, c.maxRunes)
		if got != c.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", c.input, c.maxRunes, got, c.want)
		}
	}
}

// ─── Webhook helpers ──────────────────────────────────────────────────────────

func TestHooksExtractToken(t *testing.T) {
	// Bearer token
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer my-token")
	if got := hooksExtractToken(req); got != "my-token" {
		t.Errorf("bearer: %q", got)
	}

	// X-Metiq-Token header
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Metiq-Token", "alt-token")
	if got := hooksExtractToken(req2); got != "alt-token" {
		t.Errorf("x-metiq: %q", got)
	}

	// No token
	req3 := httptest.NewRequest("GET", "/", nil)
	if got := hooksExtractToken(req3); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestInterpolateTemplate(t *testing.T) {
	body := map[string]any{
		"action": "opened",
		"repository": map[string]any{
			"full_name": "org/repo",
		},
	}
	result := interpolateTemplate("event {{action}} on {{repository.full_name}}", body)
	if result != "event opened on org/repo" {
		t.Errorf("got: %q", result)
	}
}

func TestInterpolateTemplate_MissingKey(t *testing.T) {
	result := interpolateTemplate("hello {{missing}}", map[string]any{})
	if result != "hello " {
		t.Errorf("got: %q", result)
	}
}

func TestLookupDotPath(t *testing.T) {
	m := map[string]any{
		"a": map[string]any{
			"b": "deep",
		},
		"top": "val",
	}
	if got := lookupDotPath(m, "top"); got != "val" {
		t.Errorf("top: %v", got)
	}
	if got := lookupDotPath(m, "a.b"); got != "deep" {
		t.Errorf("a.b: %v", got)
	}
	if got := lookupDotPath(m, "missing"); got != "" {
		t.Errorf("missing: %v", got)
	}
	if got := lookupDotPath(m, "top.sub"); got != "" {
		t.Errorf("top.sub: %v", got)
	}
}

func TestHooksExtractReplyText(t *testing.T) {
	if got := hooksExtractReplyText(map[string]any{"text": "hi"}); got != "hi" {
		t.Errorf("text: %q", got)
	}
	if got := hooksExtractReplyText(map[string]any{"reply": "r"}); got != "r" {
		t.Errorf("reply: %q", got)
	}
	if got := hooksExtractReplyText(map[string]any{}); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestHooksCheckRateLimit(t *testing.T) {
	ip := "192.0.2.99"
	// Fresh IP should pass
	if !hooksCheckRateLimit(ip) {
		t.Error("new IP should pass rate limit")
	}
	// Record some failures
	for i := 0; i < hooksAuthFailMax+5; i++ {
		hooksRecordAuthFailure(ip)
	}
	if hooksCheckRateLimit(ip) {
		t.Error("should be rate limited after many failures")
	}
}

func TestSessionRowMatchesSearch(t *testing.T) {
	row := map[string]any{
		"displayName": "Alice Bot",
		"sessionId":   "sess-123",
	}
	if !sessionRowMatchesSearch(row, "alice") {
		t.Error("should match displayName")
	}
	if !sessionRowMatchesSearch(row, "sess-123") {
		t.Error("should match sessionId")
	}
	if sessionRowMatchesSearch(row, "zzz") {
		t.Error("should not match")
	}
}

func TestSessionMetaString(t *testing.T) {
	if got := sessionMetaString(nil, "key"); got != "" {
		t.Errorf("nil: %q", got)
	}
	if got := sessionMetaString(map[string]any{"key": "val"}, "key"); got != "val" {
		t.Errorf("got: %q", got)
	}
	if got := sessionMetaString(map[string]any{}, "key"); got != "" {
		t.Errorf("missing: %q", got)
	}
}
