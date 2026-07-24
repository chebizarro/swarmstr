package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRunExecPolicyDoctorExposesDeterministicJSON(t *testing.T) {
	var gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		gotMethod, _ = request["method"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"approvals": map[string]any{}},
		})
	}))
	defer server.Close()
	t.Setenv("METIQ_ADMIN_ADDR", strings.TrimPrefix(server.URL, "http://"))

	out, err := captureStdout(t, func() error {
		return runExecPolicy([]string{"doctor", "--json"})
	})
	if err != nil {
		t.Fatalf("exec-policy doctor: %v", err)
	}
	if gotMethod != "exec.approvals.get" {
		t.Fatalf("method = %q", gotMethod)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode doctor JSON: %v\n%s", err, out)
	}
	if result["valid"] != true {
		t.Fatalf("unexpected doctor result: %#v", result)
	}
}

func TestRunExecPolicyDoctorReturnsFailureForInvalidPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"approvals": map[string]any{"mode": "sometimes"}},
		})
	}))
	defer server.Close()
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", strings.TrimPrefix(server.URL, "http://"))

	out, err := captureStdout(t, func() error {
		return runExecPolicy([]string{"doctor", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "invalid policy") {
		t.Fatalf("expected invalid policy failure, got %v", err)
	}
	if !strings.Contains(out, `"code": "invalid-mode"`) {
		t.Fatalf("missing invalid-mode diagnostic: %s", out)
	}
}
