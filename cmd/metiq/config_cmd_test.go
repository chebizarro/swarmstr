package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTmpConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return p
}

const sampleConfigJSON = `{
  "relays": {
    "read":  ["wss://relay.example.com"],
    "write": ["wss://relay.example.com"]
  },
  "secrets": {
    "api_key": "supersecret"
  }
}`

func TestRunConfigExport_toStdout(t *testing.T) {
	path := writeTmpConfig(t, sampleConfigJSON)
	// Redirect stdout to a pipe so we can capture output.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runConfigExport([]string{"--path", path})

	w.Close()
	os.Stdout = old

	var buf strings.Builder
	io := make([]byte, 4096)
	n, _ := r.Read(io)
	buf.Write(io[:n])
	r.Close()

	if err != nil {
		t.Fatalf("runConfigExport error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "relay.example.com") {
		t.Errorf("output missing relay URL; got: %s", got)
	}
}

func TestRunConfigExport_toFile(t *testing.T) {
	path := writeTmpConfig(t, sampleConfigJSON)
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "out.json")

	if err := runConfigExport([]string{"--path", path, "--out", outPath}); err != nil {
		t.Fatalf("runConfigExport error: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !strings.Contains(string(data), "relay.example.com") {
		t.Errorf("output file missing relay URL")
	}
}

func TestRunConfigExport_redact(t *testing.T) {
	path := writeTmpConfig(t, sampleConfigJSON)
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "out.json")

	if err := runConfigExport([]string{"--path", path, "--out", outPath, "--redact"}); err != nil {
		t.Fatalf("runConfigExport error: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	// secrets section should be redacted.
	if strings.Contains(string(data), "supersecret") {
		t.Error("redacted export still contains secret value")
	}
}

func TestRunConfigExport_missingFile(t *testing.T) {
	err := runConfigExport([]string{"--path", "/nonexistent/path/config.json"})
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestRunConfigImport_fromFile(t *testing.T) {
	srcPath := writeTmpConfig(t, sampleConfigJSON)
	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "imported.json")

	if err := runConfigImport([]string{"--file", srcPath, "--path", destPath}); err != nil {
		t.Fatalf("runConfigImport error: %v", err)
	}
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read imported file: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("imported file is not valid JSON: %v", err)
	}
}

func TestRunConfigImport_dryRun(t *testing.T) {
	srcPath := writeTmpConfig(t, sampleConfigJSON)
	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "dry.json")

	if err := runConfigImport([]string{"--file", srcPath, "--path", destPath, "--dry-run"}); err != nil {
		t.Fatalf("runConfigImport --dry-run error: %v", err)
	}
	// File should NOT have been written.
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Error("dry-run should not write the config file")
	}
}

func TestRunConfigImport_invalidJSON(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(srcPath, []byte("{not valid json{{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runConfigImport([]string{"--file", srcPath, "--path", filepath.Join(dir, "out.json")})
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestRunConfigImport_createsParentDirs(t *testing.T) {
	srcPath := writeTmpConfig(t, sampleConfigJSON)
	destPath := filepath.Join(t.TempDir(), "a", "b", "c", "config.json")

	if err := runConfigImport([]string{"--file", srcPath, "--path", destPath}); err != nil {
		t.Fatalf("runConfigImport error: %v", err)
	}
	if _, err := os.Stat(destPath); err != nil {
		t.Errorf("config file was not written: %v", err)
	}
}

func TestRunConfigImport_rejectsInvalidTargetPath(t *testing.T) {
	srcPath := writeTmpConfig(t, sampleConfigJSON)
	err := runConfigImport([]string{"--file", srcPath, "--path", filepath.Join(t.TempDir(), "config.txt")})
	if err == nil {
		t.Fatal("expected error for unsupported target config extension")
	}
	if !strings.Contains(err.Error(), ".json, .json5, .yaml, or .yml") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunConfigImport_rejectsInvalidSourcePath(t *testing.T) {
	dir := t.TempDir()
	err := runConfigImport([]string{"--file", dir, "--path", filepath.Join(dir, "out.json")})
	if err == nil {
		t.Fatal("expected error for directory source path")
	}
	if !strings.Contains(err.Error(), "config file path must be a file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunConfigSetSupportsArrayPathAndJSON5Value(t *testing.T) {
	path := writeTmpConfig(t, sampleConfigJSON)
	if err := runConfigSet([]string{"--path", path, "relays.read[0]", "\"wss://new.example.com\""}); err != nil {
		t.Fatalf("runConfigSet error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "wss://new.example.com") {
		t.Fatalf("config was not updated: %s", data)
	}
}

func TestRunConfigSetDryRunDoesNotWrite(t *testing.T) {
	path := writeTmpConfig(t, sampleConfigJSON)
	if err := runConfigSet([]string{"--path", path, "--dry-run", "relays.read[0]", "\"wss://dry.example.com\""}); err != nil {
		t.Fatalf("runConfigSet dry-run error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "wss://dry.example.com") {
		t.Fatal("dry-run wrote config")
	}
}

func TestRunConfigPatchMergesJSON5Object(t *testing.T) {
	path := writeTmpConfig(t, sampleConfigJSON)
	patchPath := filepath.Join(t.TempDir(), "patch.json5")
	if err := os.WriteFile(patchPath, []byte(`{ "relays": { "write": ["wss://patched.example.com"] } }`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runConfigPatch([]string{"--path", path, "--file", patchPath}); err != nil {
		t.Fatalf("runConfigPatch error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "wss://patched.example.com") {
		t.Fatalf("config was not patched: %s", data)
	}
}
