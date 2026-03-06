package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"swarmstr/internal/store/state"
)

// ─── JSON5 / HuJSON ─────────────────────────────────────────────────────────

func TestLoadConfigFile_JSON5_basic(t *testing.T) {
	content := `{
		// Swarmstr config
		"relays": {
			"read":  ["wss://relay.example.com"],
			"write": ["wss://relay.example.com", "wss://other.relay"]
		},
		"channels": {
			"web": {
				"dm": {
					"policy": "allowlist",
					"allow_from": ["pubkey1", "pubkey2"]
				}
			}
		},
		"agents": {
			"defaults": { "model": "claude-3-5-sonnet-20241022" }
		},
		"plugins": [{"name": "my-plugin"}],
		"memory": {"backend": "sqlite"}
	}`
	path := writeTmp(t, "config.json", content)

	doc, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Relays
	if len(doc.Relays.Read) != 1 || doc.Relays.Read[0] != "wss://relay.example.com" {
		t.Errorf("read relays mismatch: %v", doc.Relays.Read)
	}
	if len(doc.Relays.Write) != 2 {
		t.Errorf("write relays mismatch: %v", doc.Relays.Write)
	}

	// DM policy
	if doc.DM.Policy != "allowlist" {
		t.Errorf("DM policy: got %q, want %q", doc.DM.Policy, "allowlist")
	}
	if len(doc.DM.AllowFrom) != 2 {
		t.Errorf("DM allow_from: got %v", doc.DM.AllowFrom)
	}

	// Agent model
	if doc.Agent.DefaultModel != "claude-3-5-sonnet-20241022" {
		t.Errorf("default model: got %q", doc.Agent.DefaultModel)
	}

	// Extra: extensions (mapped from plugins)
	if doc.Extra == nil {
		t.Fatal("extra is nil")
	}
	if _, ok := doc.Extra["extensions"]; !ok {
		t.Error("expected extra[extensions] to be present")
	}
	if _, ok := doc.Extra["memory"]; !ok {
		t.Error("expected extra[memory] pass-through")
	}
}

func TestLoadConfigFile_JSON5_comments(t *testing.T) {
	content := `{
		/* block comment */
		"relays": {
			"read": ["wss://a.com"], // inline comment
			"write": []
		}
	}`
	path := writeTmp(t, "config.json5", content)
	doc, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("JSON5 with comments failed: %v", err)
	}
	if len(doc.Relays.Read) != 1 {
		t.Errorf("relays.read: %v", doc.Relays.Read)
	}
}

// ─── YAML ────────────────────────────────────────────────────────────────────

func TestLoadConfigFile_YAML(t *testing.T) {
	content := `
relays:
  read:
    - wss://relay.yaml.example
  write:
    - wss://relay.yaml.example
channels:
  web:
    dm:
      policy: open
agents:
  defaults:
    model: gpt-4o
skills:
  workspace: /home/agent
`
	path := writeTmp(t, "config.yaml", content)
	doc, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("YAML load failed: %v", err)
	}
	if doc.DM.Policy != "open" {
		t.Errorf("DM policy: %q", doc.DM.Policy)
	}
	if doc.Agent.DefaultModel != "gpt-4o" {
		t.Errorf("model: %q", doc.Agent.DefaultModel)
	}
	if _, ok := doc.Extra["skills"]; !ok {
		t.Error("skills pass-through missing")
	}
}

func TestLoadConfigFile_YML_extension(t *testing.T) {
	content := "relays:\n  read:\n    - wss://r.example\n  write: []\n"
	path := writeTmp(t, "config.yml", content)
	_, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("unexpected error for .yml: %v", err)
	}
}

// ─── Write / round-trip ──────────────────────────────────────────────────────

func TestWriteConfigFile_roundtrip(t *testing.T) {
	doc := state.ConfigDoc{
		Version: 1,
		DM:      state.DMPolicy{Policy: "pairing"},
		Relays:  state.RelayPolicy{Read: []string{"wss://a.example"}, Write: []string{"wss://b.example"}},
		Agent:   state.AgentPolicy{DefaultModel: "claude-opus-4"},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	if err := WriteConfigFile(path, doc); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back state.ConfigDoc
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if back.DM.Policy != "pairing" {
		t.Errorf("DM policy after round-trip: %q", back.DM.Policy)
	}
	if back.Agent.DefaultModel != "claude-opus-4" {
		t.Errorf("model after round-trip: %q", back.Agent.DefaultModel)
	}
}

func TestWriteConfigFile_createsParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "config.json")
	if err := WriteConfigFile(path, state.ConfigDoc{Version: 1}); err != nil {
		t.Fatalf("write with missing parents: %v", err)
	}
	if !ConfigFileExists(path) {
		t.Error("file not created")
	}
}

// ─── Error cases ─────────────────────────────────────────────────────────────

func TestLoadConfigFile_missingFile(t *testing.T) {
	_, err := LoadConfigFile("/nonexistent/config.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadConfigFile_emptyPath(t *testing.T) {
	_, err := LoadConfigFile("")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestLoadConfigFile_malformedJSON(t *testing.T) {
	path := writeTmp(t, "bad.json", `{ "relays": INVALID }`)
	_, err := LoadConfigFile(path)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestLoadConfigFile_nullFile(t *testing.T) {
	path := writeTmp(t, "null.json", "null")
	_, err := LoadConfigFile(path)
	if err == nil {
		t.Error("expected error for null config")
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func writeTmp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
