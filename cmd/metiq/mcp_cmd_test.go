package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRunMCPListCallsGatewayMethod(t *testing.T) {
	var gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotMethod = req.Method
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"servers": []any{map[string]any{"name": "demo", "state": "connected", "enabled": true}},
			},
		})
	}))
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	out, err := captureStdout(t, func() error { return runMCP([]string{"list"}) })
	if err != nil {
		t.Fatalf("runMCP(list) error: %v", err)
	}
	if gotMethod != "mcp.list" {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if !strings.Contains(out, "demo") {
		t.Fatalf("expected server in output, got %q", out)
	}
}

func TestRunMCPPutBuildsConfigPayload(t *testing.T) {
	var gotMethod string
	var gotParams map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotMethod = req.Method
		gotParams = req.Params
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"ok": true, "server": map[string]any{"name": "demo", "state": "pending", "transport": "stdio"}},
		})
	}))
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	if _, err := captureStdout(t, func() error {
		return runMCP([]string{"put", "demo", "--command", "npx", "--arg", "-y", "--arg", "server-filesystem", "--env", "MODE=dev"})
	}); err != nil {
		t.Fatalf("runMCP(put) error: %v", err)
	}
	if gotMethod != "mcp.put" {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotParams["server"] != "demo" {
		t.Fatalf("unexpected params: %#v", gotParams)
	}
	cfg, _ := gotParams["config"].(map[string]any)
	if cfg["command"] != "npx" || cfg["type"] != "stdio" {
		t.Fatalf("unexpected config payload: %#v", cfg)
	}
}

func TestRunMCPTestReturnsFailureForProbeError(t *testing.T) {
	var gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotMethod = req.Method
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"ok": false, "error": "dial failed", "server": map[string]any{"name": "demo", "state": "failed"}},
		})
	}))
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	_, err := captureStdout(t, func() error { return runMCP([]string{"test", "demo"}) })
	if err == nil || !strings.Contains(err.Error(), "dial failed") {
		t.Fatalf("expected failure result error, got %v", err)
	}
	if gotMethod != "mcp.test" {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
}
