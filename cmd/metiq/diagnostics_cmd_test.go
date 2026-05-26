package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateDiagnosticsZipWritesBundleAndMethodFiles(t *testing.T) {
	out := filepath.Join(t.TempDir(), "diag.zip")
	files, err := createDiagnosticsZip(out, map[string]any{
		"created_at": "2026-01-01T00:00:00Z",
		"version":    "test",
		"methods": map[string]any{
			"status.get": map[string]any{"ok": true},
			"logs.tail":  map[string]any{"ok": false, "error": "offline"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("files = %#v", files)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("diagnostics zip permissions = %o, want 0600", info.Mode().Perm())
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	seen := map[string]bool{}
	for _, f := range zr.File {
		seen[f.Name] = true
	}
	for _, want := range []string{
		"metiq-diagnostics/manifest.json",
		"metiq-diagnostics/bundle.json",
		"metiq-diagnostics/methods/status.get.json",
		"metiq-diagnostics/methods/logs.tail.json",
	} {
		if !seen[want] {
			t.Fatalf("missing %s in %#v", want, seen)
		}
	}
}

func TestRedactDiagnosticsValueRedactsNestedSecrets(t *testing.T) {
	redacted := redactDiagnosticsValue(map[string]any{
		"config": map[string]any{
			"api_key": "sk-live",
			"nested":  []any{map[string]any{"token": "secret-token", "safe": "ok"}},
		},
	}).(map[string]any)
	cfg := redacted["config"].(map[string]any)
	if cfg["api_key"] != "[REDACTED]" {
		t.Fatalf("api key not redacted: %#v", redacted)
	}
	nested := cfg["nested"].([]any)[0].(map[string]any)
	if nested["token"] != "[REDACTED]" || nested["safe"] != "ok" {
		t.Fatalf("nested redaction mismatch: %#v", nested)
	}
}
