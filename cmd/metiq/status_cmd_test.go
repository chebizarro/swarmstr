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

func TestRunDoctorReadsNestedMemoryStats(t *testing.T) {
	bootstrapPath := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(bootstrapPath, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("write bootstrap: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "status": "ok"})
		case "/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"relays": []string{"wss://relay.example"}})
		case "/call":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			method, _ := req["method"].(string)
			switch method {
			case "doctor.memory.status":
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"ok": true, "index": map[string]any{"entry_count": 12, "session_count": 3}}})
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	out, err := captureStdout(t, func() error {
		return runDoctor([]string{"--bootstrap", bootstrapPath})
	})
	if err != nil {
		t.Fatalf("runDoctor: %v", err)
	}
	if !strings.Contains(out, "12 docs / 3 sessions") {
		t.Fatalf("unexpected doctor output: %s", out)
	}
}

func TestRunChannelsListDerivesKindAndStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		method, _ := req["method"].(string)
		if method != "channels.status" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"channels": []map[string]any{{
					"id":         "nostr",
					"connected":  true,
					"logged_out": false,
				}},
			},
		})
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	out, err := captureStdout(t, func() error {
		return runChannelsList(nil)
	})
	if err != nil {
		t.Fatalf("runChannelsList: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("unexpected output: %q", out)
	}
	fields := strings.Fields(lines[1])
	if len(fields) != 3 {
		t.Fatalf("unexpected row fields: %q", lines[1])
	}
	if fields[0] != "nostr" || fields[1] != "nostr" || fields[2] != "connected" {
		t.Fatalf("unexpected derived fields: %v", fields)
	}
}
