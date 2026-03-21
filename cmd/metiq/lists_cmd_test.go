package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = old
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	_ = r.Close()
	return string(buf[:n]), runErr
}

func TestRunListsGet(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"list": map[string]any{"name": "allow", "items": []string{"npub1a", "npub1b"}},
			},
		})
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	out, err := captureStdout(t, func() error {
		return runLists([]string{"get", "--name", "allow"})
	})
	if err != nil {
		t.Fatalf("runLists get: %v", err)
	}
	if !strings.Contains(out, "list=allow items=2") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRunListsPut(t *testing.T) {
	var gotMethod string
	var gotParams map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotMethod, _ = req["method"].(string)
		gotParams, _ = req["params"].(map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"event_id": "evt-123"},
		})
	}))
	defer ts.Close()

	itemsPath := filepath.Join(t.TempDir(), "items.txt")
	if err := os.WriteFile(itemsPath, []byte("npub1x\nnpub1y\n"), 0o600); err != nil {
		t.Fatalf("write items file: %v", err)
	}

	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	out, err := captureStdout(t, func() error {
		return runLists([]string{"put", "--name", "allow", "--item", "npub1x,npub1z", "--file", itemsPath, "--expected-version", "0"})
	})
	if err != nil {
		t.Fatalf("runLists put: %v", err)
	}
	if gotMethod != "list.put" {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotParams["name"] != "allow" {
		t.Fatalf("unexpected name param: %#v", gotParams["name"])
	}
	items, _ := gotParams["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("expected deduped merged items, got %#v", gotParams["items"])
	}
	if gotParams["expected_version"].(float64) != 0 {
		t.Fatalf("expected expected_version=0, got %#v", gotParams["expected_version"])
	}
	if !strings.Contains(out, "event_id=evt-123") {
		t.Fatalf("unexpected output: %s", out)
	}
}
