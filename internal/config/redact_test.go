package config

import (
	"testing"

	"metiq/internal/store/state"
)

func TestRedact_secretsSection(t *testing.T) {
	doc := state.ConfigDoc{
		Version: 1,
		Extra: map[string]any{
			"secrets": map[string]any{"openai_key": "sk-real"},
			"memory":  map[string]any{"backend": "sqlite"},
		},
	}
	out := Redact(doc)
	if out.Extra["secrets"] != RedactedValue {
		t.Errorf("secrets not fully redacted: %v", out.Extra["secrets"])
	}
	// Non-secret section preserved.
	mem, ok := out.Extra["memory"].(map[string]any)
	if !ok || mem["backend"] != "sqlite" {
		t.Errorf("memory section mangled: %v", out.Extra["memory"])
	}
}

func TestRedact_pairingSection(t *testing.T) {
	doc := state.ConfigDoc{
		Version: 1,
		Extra: map[string]any{
			"pairing": map[string]any{"token": "secret-pair-token"},
		},
	}
	out := Redact(doc)
	if out.Extra["pairing"] != RedactedValue {
		t.Errorf("pairing not redacted: %v", out.Extra["pairing"])
	}
}

func TestRedact_apiKeyInProviders(t *testing.T) {
	doc := state.ConfigDoc{
		Version: 1,
		Extra: map[string]any{
			"providers": map[string]any{
				"openai": map[string]any{
					"api_key": "sk-openai-real",
					"model":   "gpt-4o",
				},
				"anthropic": map[string]any{
					"apiKey": "sk-ant-real",
					"model":  "claude-3-5-sonnet",
				},
			},
		},
	}
	out := Redact(doc)
	providers, ok := out.Extra["providers"].(map[string]any)
	if !ok {
		t.Fatal("providers not a map")
	}
	openai, ok := providers["openai"].(map[string]any)
	if !ok {
		t.Fatal("openai not a map")
	}
	if openai["api_key"] != RedactedValue {
		t.Errorf("openai api_key not redacted: %v", openai["api_key"])
	}
	if openai["model"] != "gpt-4o" {
		t.Errorf("model should be preserved: %v", openai["model"])
	}
	anthropic, ok := providers["anthropic"].(map[string]any)
	if !ok {
		t.Fatal("anthropic not a map")
	}
	if anthropic["apiKey"] != RedactedValue {
		t.Errorf("anthropic apiKey not redacted: %v", anthropic["apiKey"])
	}
}

func TestRedact_passwordAndToken(t *testing.T) {
	m := RedactMap(map[string]any{
		"host":         "db.example.com",
		"password":     "hunter2",
		"access_token": "tok-abc",
		"port":         5432,
	})
	if m["password"] != RedactedValue {
		t.Errorf("password not redacted: %v", m["password"])
	}
	if m["access_token"] != RedactedValue {
		t.Errorf("access_token not redacted: %v", m["access_token"])
	}
	if m["host"] != "db.example.com" {
		t.Errorf("host mangled: %v", m["host"])
	}
	if m["port"] != 5432 {
		t.Errorf("port mangled: %v", m["port"])
	}
}

func TestRedact_preservesNonSensitive(t *testing.T) {
	doc := state.ConfigDoc{
		Version: 1,
		DM:      state.DMPolicy{Policy: "open"},
		Relays:  state.RelayPolicy{Read: []string{"wss://r.example"}},
		Agent:   state.AgentPolicy{DefaultModel: "claude-opus-4"},
		Extra: map[string]any{
			"skills": map[string]any{"workspace": "/home/agent"},
		},
	}
	out := Redact(doc)
	if out.DM.Policy != "open" {
		t.Errorf("DM.Policy mutated: %q", out.DM.Policy)
	}
	if out.Agent.DefaultModel != "claude-opus-4" {
		t.Errorf("Agent.DefaultModel mutated: %q", out.Agent.DefaultModel)
	}
	if len(out.Relays.Read) != 1 {
		t.Errorf("Relays.Read mutated: %v", out.Relays.Read)
	}
	skills, ok := out.Extra["skills"].(map[string]any)
	if !ok || skills["workspace"] != "/home/agent" {
		t.Errorf("skills mangled: %v", out.Extra["skills"])
	}
}

func TestRedact_noExtraIsNil(t *testing.T) {
	doc := state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "pairing"}}
	out := Redact(doc)
	if out.Extra != nil {
		t.Errorf("Extra should be nil when empty: %v", out.Extra)
	}
	if out.DM.Policy != "pairing" {
		t.Errorf("DM.Policy mutated: %q", out.DM.Policy)
	}
}

func TestRedact_doesNotMutateOriginal(t *testing.T) {
	doc := state.ConfigDoc{
		Version: 1,
		Extra: map[string]any{
			"providers": map[string]any{"openai": map[string]any{"api_key": "original-key"}},
		},
	}
	_ = Redact(doc)
	providers := doc.Extra["providers"].(map[string]any)
	openai := providers["openai"].(map[string]any)
	if openai["api_key"] != "original-key" {
		t.Error("Redact mutated the original doc's api_key")
	}
}

func TestRedact_typedSections(t *testing.T) {
	doc := state.ConfigDoc{
		Version: 1,
		Providers: state.ProvidersConfig{
			"openai": {
				Enabled: true,
				APIKey:  "sk-real",
				APIKeys: []string{"sk-1", "sk-2"},
				BaseURL: "https://api.openai.com/v1",
				Model:   "gpt-4o-mini",
				Extra:   map[string]any{"temperature": 0.1},
			},
		},
		Session:   state.SessionConfig{TTLSeconds: 60},
		Heartbeat: state.HeartbeatConfig{Enabled: true, IntervalMS: 15000},
		TTS:       state.TTSConfig{Enabled: true, Provider: "elevenlabs", Voice: "alloy"},
		Secrets:   state.SecretsConfig{"openai_api_key": "env:OPENAI_API_KEY"},
		Hooks:     state.HooksConfig{Enabled: true, Token: "hook-secret"},
		CronCfg:   state.CronConfig{Enabled: true},
	}

	out := Redact(doc)

	if out.Providers["openai"].APIKey != RedactedValue {
		t.Fatalf("typed provider api_key not redacted: %#v", out.Providers["openai"].APIKey)
	}
	if got := out.Providers["openai"].APIKeys; len(got) != 2 || got[0] != RedactedValue || got[1] != RedactedValue {
		t.Fatalf("typed provider api_keys not redacted: %#v", got)
	}
	if out.Providers["openai"].BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("typed provider base_url should be preserved: %#v", out.Providers["openai"].BaseURL)
	}
	if v, ok := out.Providers["openai"].Extra["temperature"].(float64); !ok || v != 0.1 {
		t.Fatalf("typed provider extra should be preserved: %#v", out.Providers["openai"].Extra)
	}

	if out.Secrets["openai_api_key"] != RedactedValue {
		t.Fatalf("typed secrets value not redacted: %#v", out.Secrets)
	}
	if out.Hooks.Token != RedactedValue {
		t.Fatalf("hook token not redacted: %#v", out.Hooks.Token)
	}

	if out.Session != doc.Session {
		t.Fatalf("session should be preserved: got %#v want %#v", out.Session, doc.Session)
	}
	if out.Heartbeat != doc.Heartbeat {
		t.Fatalf("heartbeat should be preserved: got %#v want %#v", out.Heartbeat, doc.Heartbeat)
	}
	if out.TTS != doc.TTS {
		t.Fatalf("tts should be preserved: got %#v want %#v", out.TTS, doc.TTS)
	}
	if out.CronCfg != doc.CronCfg {
		t.Fatalf("cron should be preserved: got %#v want %#v", out.CronCfg, doc.CronCfg)
	}
}
