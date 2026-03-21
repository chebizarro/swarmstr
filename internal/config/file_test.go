package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/store/state"
)

// ─── JSON5 / HuJSON ─────────────────────────────────────────────────────────

func TestLoadConfigFile_JSON5_basic(t *testing.T) {
	content := `{
		// Metiq config
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

func TestParseConfigBytes_TypedSectionsMapping(t *testing.T) {
	raw := []byte(`{
		"providers": {
			"openai": {
				"enabled": true,
				"api_key": "sk-live",
				"base_url": "https://api.openai.com/v1",
				"model": "gpt-4o-mini",
				"temperature": 0.2,
				"stream": true
			}
		},
		"session": {
			"ttl_seconds": 3600,
			"max_sessions": 12,
			"history_limit": 80
		},
		"heartbeat": {
			"enabled": true,
			"interval_ms": 15000
		},
		"tts": {
			"enabled": true,
			"provider": "elevenlabs",
			"voice": "alloy"
		},
		"secrets": {
			"openai_api_key": "env:OPENAI_API_KEY"
		},
		"cron": {
			"enabled": true
		},
		"memory": {
			"backend": "sqlite"
		}
	}`)

	doc, err := ParseConfigBytes(raw, ".json")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	provider, ok := doc.Providers["openai"]
	if !ok {
		t.Fatalf("expected typed provider entry for openai, got: %#v", doc.Providers)
	}
	if provider.APIKey != "sk-live" {
		t.Fatalf("provider api_key mismatch: %q", provider.APIKey)
	}
	if provider.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("provider base_url mismatch: %q", provider.BaseURL)
	}
	if provider.Model != "gpt-4o-mini" {
		t.Fatalf("provider model mismatch: %q", provider.Model)
	}
	if provider.Extra == nil {
		t.Fatalf("expected provider extra fields to be preserved")
	}
	if v, ok := provider.Extra["temperature"].(float64); !ok || v != 0.2 {
		t.Fatalf("provider extra temperature missing/mismatched: %#v", provider.Extra["temperature"])
	}
	if v, ok := provider.Extra["stream"].(bool); !ok || !v {
		t.Fatalf("provider extra stream missing/mismatched: %#v", provider.Extra["stream"])
	}

	if doc.Session.TTLSeconds != 3600 || doc.Session.MaxSessions != 12 || doc.Session.HistoryLimit != 80 {
		t.Fatalf("session mapping mismatch: %#v", doc.Session)
	}
	if !doc.Heartbeat.Enabled || doc.Heartbeat.IntervalMS != 15000 {
		t.Fatalf("heartbeat mapping mismatch: %#v", doc.Heartbeat)
	}
	if !doc.TTS.Enabled || doc.TTS.Provider != "elevenlabs" || doc.TTS.Voice != "alloy" {
		t.Fatalf("tts mapping mismatch: %#v", doc.TTS)
	}
	if doc.Secrets["openai_api_key"] != "env:OPENAI_API_KEY" {
		t.Fatalf("secrets mapping mismatch: %#v", doc.Secrets)
	}
	if !doc.CronCfg.Enabled {
		t.Fatalf("cron mapping mismatch: %#v", doc.CronCfg)
	}

	if doc.Extra == nil {
		t.Fatalf("expected extra map to keep passthrough sections")
	}
	if _, ok := doc.Extra["memory"]; !ok {
		t.Fatalf("expected memory passthrough in extra")
	}
	for _, typedKey := range []string{"providers", "session", "heartbeat", "tts", "secrets", "cron"} {
		if _, ok := doc.Extra[typedKey]; ok {
			t.Fatalf("typed key %q should not be duplicated in extra", typedKey)
		}
	}
}

func TestParseConfigBytes_AgentsListPreservesMissingIDForValidation(t *testing.T) {
	raw := []byte(`{
		"agents": [
			{"name":"No ID","tool_profile":"coding"},
			{"id":"main","model":"echo"}
		]
	}`)

	doc, err := ParseConfigBytes(raw, ".json")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(doc.Agents) != 2 {
		t.Fatalf("expected both agent entries preserved, got %#v", doc.Agents)
	}
	if doc.Agents[0].ID != "" {
		t.Fatalf("expected first agent ID to remain empty for validator, got %#v", doc.Agents[0])
	}
}

func TestLoadConfigFile_YAMLAgentsNumericFields(t *testing.T) {
	content := `
relays:
  read:
    - wss://relay.yaml.example
  write:
    - wss://relay.yaml.example
dm:
  policy: open
agents:
  - id: main
    heartbeat_ms: 1500
    history_limit: 25
`
	path := writeTmp(t, "agents.yaml", content)
	doc, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("unexpected YAML load error: %v", err)
	}
	if len(doc.Agents) != 1 {
		t.Fatalf("expected one parsed agent, got %#v", doc.Agents)
	}
	if doc.Agents[0].HeartbeatMS != 1500 || doc.Agents[0].HistoryLimit != 25 {
		t.Fatalf("unexpected YAML numeric mapping: %#v", doc.Agents[0])
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

func TestWriteLoadConfigFile_roundtripProviderExtra(t *testing.T) {
	doc := state.ConfigDoc{
		Version: 1,
		DM:      state.DMPolicy{Policy: "pairing"},
		Relays:  state.RelayPolicy{Read: []string{"wss://a.example"}, Write: []string{"wss://b.example"}},
		Providers: state.ProvidersConfig{
			"openai": {
				Enabled: true,
				APIKey:  "sk-live",
				BaseURL: "https://api.openai.com/v1",
				Model:   "gpt-4o-mini",
				Extra: map[string]any{
					"temperature": 0.3,
					"stream":      true,
				},
			},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.json")
	if err := WriteConfigFile(path, doc); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	loaded, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	provider, ok := loaded.Providers["openai"]
	if !ok {
		t.Fatalf("provider missing after round-trip: %#v", loaded.Providers)
	}
	if provider.APIKey != "sk-live" {
		t.Fatalf("api_key mismatch after round-trip: %q", provider.APIKey)
	}
	if provider.Extra == nil {
		t.Fatalf("provider extra missing after round-trip")
	}
	if v, ok := provider.Extra["temperature"].(float64); !ok || v != 0.3 {
		t.Fatalf("temperature extra mismatch after round-trip: %#v", provider.Extra["temperature"])
	}
	if v, ok := provider.Extra["stream"].(bool); !ok || !v {
		t.Fatalf("stream extra mismatch after round-trip: %#v", provider.Extra["stream"])
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

func TestParseConfigBytes_AgentDmPeersAndProvider(t *testing.T) {
	raw := []byte(`{
		"agents": [
			{
				"id": "support-bot",
				"model": "echo",
				"provider": "anthropic",
				"dm_peers": ["abc123def456", "deadbeef0011"]
			}
		]
	}`)
	doc, err := ParseConfigBytes(raw, ".json")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(doc.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(doc.Agents))
	}
	ag := doc.Agents[0]
	if ag.ID != "support-bot" {
		t.Errorf("id mismatch: %q", ag.ID)
	}
	if ag.Provider != "anthropic" {
		t.Errorf("provider mismatch: %q", ag.Provider)
	}
	if len(ag.DmPeers) != 2 {
		t.Fatalf("expected 2 dm_peers, got %d: %v", len(ag.DmPeers), ag.DmPeers)
	}
	if ag.DmPeers[0] != "abc123def456" || ag.DmPeers[1] != "deadbeef0011" {
		t.Errorf("dm_peers mismatch: %v", ag.DmPeers)
	}
}

func TestParseConfigBytes_AgentDmPeersCamelCase(t *testing.T) {
	// Verify camelCase alias dmPeers also parses correctly.
	raw := []byte(`{
		"agents": [{"id": "bot", "model": "echo", "dmPeers": ["peer1abc"]}]
	}`)
	doc, err := ParseConfigBytes(raw, ".json")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(doc.Agents) != 1 || len(doc.Agents[0].DmPeers) != 1 || doc.Agents[0].DmPeers[0] != "peer1abc" {
		t.Errorf("camelCase dmPeers parse failed: %#v", doc.Agents)
	}
}

func writeTmp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
