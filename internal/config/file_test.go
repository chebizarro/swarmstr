package config

import (
	"strings"
	"testing"
)

func TestParseConfigBytesStorageEncrypt(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"storage":{"encrypt":false}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if doc.Storage.Encrypt == nil || *doc.Storage.Encrypt {
		t.Fatalf("expected storage.encrypt=false, got %#v", doc.Storage)
	}
}

func TestParseConfigBytesACPTransport(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"acp":{"transport":"nip04"}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if doc.ACP.Transport != "nip04" {
		t.Fatalf("expected acp.transport=nip04, got %#v", doc.ACP)
	}
}

func TestParseConfigBytesDMReplyScheme(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"dm":{"reply_scheme":"nip17"}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if doc.DM.ReplyScheme != "nip17" {
		t.Fatalf("expected dm.reply_scheme=nip17, got %#v", doc.DM)
	}
}

func TestParseConfigBytesAgentHeartbeatModel(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"agents":[{"id":"main","model":"gpt-4o","heartbeat":{"model":"gpt-4o-mini"}}]}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if len(doc.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(doc.Agents))
	}
	if doc.Agents[0].Heartbeat.Model != "gpt-4o-mini" {
		t.Fatalf("expected agents[0].heartbeat.model=gpt-4o-mini, got %#v", doc.Agents[0].Heartbeat)
	}
}

func TestParseConfigBytesAgentContextWindow(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"agents":[{"id":"local","model":"phi-3-mini.gguf","context_window":4096,"max_context_tokens":3000}]}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if len(doc.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(doc.Agents))
	}
	if doc.Agents[0].ContextWindow != 4096 {
		t.Fatalf("expected context_window=4096, got %d", doc.Agents[0].ContextWindow)
	}
	if doc.Agents[0].MaxContextTokens != 3000 {
		t.Fatalf("expected max_context_tokens=3000, got %d", doc.Agents[0].MaxContextTokens)
	}
}

func TestParseConfigBytesAgentContextWindowCamelCase(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"agents":[{"id":"local","model":"gemma.gguf","contextWindow":8192}]}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if len(doc.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(doc.Agents))
	}
	if doc.Agents[0].ContextWindow != 8192 {
		t.Fatalf("expected contextWindow=8192, got %d", doc.Agents[0].ContextWindow)
	}
}

func TestParseConfigBytesOpenClawDefaultHeartbeatEvery(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"agents":{"defaults":{"heartbeat":{"every":"2h"}}}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if !doc.Heartbeat.Enabled {
		t.Fatalf("expected heartbeat to be enabled, got %#v", doc.Heartbeat)
	}
	if want := 2 * 60 * 60 * 1000; doc.Heartbeat.IntervalMS != want {
		t.Fatalf("expected heartbeat interval %d, got %#v", want, doc.Heartbeat)
	}
}

func TestParseConfigBytesTopLevelHeartbeatOverridesDefaults(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"agents":{"defaults":{"heartbeat":{"every":"2h"}}},"heartbeat":{"enabled":true,"interval_ms":15000}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if !doc.Heartbeat.Enabled || doc.Heartbeat.IntervalMS != 15000 {
		t.Fatalf("expected explicit top-level heartbeat override, got %#v", doc.Heartbeat)
	}
}

// ── FIPS config parsing ───────────────────────────────────────────────────────

func TestParseConfigBytesFIPSEnabled(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"fips":{"enabled":true}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if !doc.FIPS.Enabled {
		t.Fatalf("expected fips.enabled=true, got %#v", doc.FIPS)
	}
}

func TestParseConfigBytesFIPSDisabledByDefault(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if doc.FIPS.Enabled {
		t.Fatalf("expected fips.enabled=false by default, got %#v", doc.FIPS)
	}
}

func TestParseConfigBytesFIPSFullConfig(t *testing.T) {
	input := `{
		"fips": {
			"enabled": true,
			"control_socket": "/run/fips/control.sock",
			"agent_port": 1337,
			"control_port": 1338,
			"transport_pref": "fips-first",
			"peers": ["npub1abc123", "npub1def456"],
			"conn_timeout": "5s",
			"reach_cache_ttl": "30s"
		}
	}`
	doc, err := ParseConfigBytes([]byte(input), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	fips := doc.FIPS
	if !fips.Enabled {
		t.Errorf("expected enabled=true")
	}
	if fips.ControlSocket != "/run/fips/control.sock" {
		t.Errorf("expected control_socket=/run/fips/control.sock, got %q", fips.ControlSocket)
	}
	if fips.AgentPort != 1337 {
		t.Errorf("expected agent_port=1337, got %d", fips.AgentPort)
	}
	if fips.ControlPort != 1338 {
		t.Errorf("expected control_port=1338, got %d", fips.ControlPort)
	}
	if fips.TransportPref != "fips-first" {
		t.Errorf("expected transport_pref=fips-first, got %q", fips.TransportPref)
	}
	if len(fips.Peers) != 2 || fips.Peers[0] != "npub1abc123" || fips.Peers[1] != "npub1def456" {
		t.Errorf("expected 2 peers, got %v", fips.Peers)
	}
	if fips.ConnTimeout != "5s" {
		t.Errorf("expected conn_timeout=5s, got %q", fips.ConnTimeout)
	}
	if fips.ReachCacheTTL != "30s" {
		t.Errorf("expected reach_cache_ttl=30s, got %q", fips.ReachCacheTTL)
	}
}

func TestParseConfigBytesFIPSDefaultPorts(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"fips":{"enabled":true}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if doc.FIPS.EffectiveAgentPort() != 1337 {
		t.Errorf("expected default agent port 1337, got %d", doc.FIPS.EffectiveAgentPort())
	}
	if doc.FIPS.EffectiveControlPort() != 1338 {
		t.Errorf("expected default control port 1338, got %d", doc.FIPS.EffectiveControlPort())
	}
}

func TestParseConfigBytesFIPSTransportPref(t *testing.T) {
	for _, tc := range []struct {
		input    string
		expected string
	}{
		{`{"fips":{"enabled":true}}`, "fips-first"},
		{`{"fips":{"enabled":true,"transport_pref":"relay-first"}}`, "relay-first"},
		{`{"fips":{"enabled":true,"transport_pref":"fips-only"}}`, "fips-only"},
		{`{"fips":{"enabled":true,"transport_pref":"FIPS-FIRST"}}`, "fips-first"},
	} {
		doc, err := ParseConfigBytes([]byte(tc.input), ".json")
		if err != nil {
			t.Fatalf("ParseConfigBytes(%s): %v", tc.input, err)
		}
		if got := doc.FIPS.EffectiveTransportPref(); got != tc.expected {
			t.Errorf("input %s: expected transport_pref=%q, got %q", tc.input, tc.expected, got)
		}
	}
}

func TestParseConfigBytesLoadsPreviouslyDroppedFields(t *testing.T) {
	input := `{
		"version": 7,
		"control": {
			"require_auth": true,
			"allow_unauth_methods": ["status.get"],
			"legacy_token_fallback": true,
			"admins": [{"pubkey":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","methods":["config.get"]}]
		},
		"providers": {
			"openai": {
				"api_key": "${OPENAI_API_KEY}",
				"api_keys": ["${OPENAI_API_KEY_A}", "${OPENAI_API_KEY_B}"]
			}
		},
		"session": {
			"ttl_seconds": 10,
			"prune_after_days": 30,
			"prune_on_boot": true
		},
		"cron": {
			"enabled": true,
			"job_timeout_secs": 123
		},
		"hooks": {
			"enabled": true,
			"token": "secret",
			"allowed_agent_ids": ["main"],
			"default_session_key": "hook:main",
			"allow_request_session_key": true
		},
		"timeouts": {
			"git_ops_secs": 22,
			"memory_persist_secs": 33,
			"subagent_default_secs": 44
		},
		"nostr_channels": {
			"dev": {
				"kind": "chat",
				"allow_from": ["*"],
				"config": {
					"root_tag": "general"
				}
			}
		},
		"agents": [{
			"id": "main",
			"model": "gpt-4o",
			"max_agentic_iterations": 12
		}]
	}`
	doc, err := ParseConfigBytes([]byte(input), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if doc.Version != 7 {
		t.Fatalf("expected version=7, got %d", doc.Version)
	}
	if !doc.Control.RequireAuth || !doc.Control.LegacyTokenFallback || len(doc.Control.AllowUnauthMethods) != 1 || len(doc.Control.Admins) != 1 {
		t.Fatalf("unexpected control config: %#v", doc.Control)
	}
	if len(doc.Providers["openai"].APIKeys) != 2 {
		t.Fatalf("expected providers.openai.api_keys to load, got %#v", doc.Providers["openai"])
	}
	if doc.Session.PruneAfterDays != 30 || !doc.Session.PruneOnBoot {
		t.Fatalf("unexpected session config: %#v", doc.Session)
	}
	if !doc.CronCfg.Enabled || doc.CronCfg.JobTimeoutSecs != 123 {
		t.Fatalf("unexpected cron config: %#v", doc.CronCfg)
	}
	if !doc.Hooks.Enabled || doc.Hooks.Token != "secret" || doc.Hooks.DefaultSessionKey != "hook:main" {
		t.Fatalf("unexpected hooks config: %#v", doc.Hooks)
	}
	if doc.Timeouts.GitOpsSecs != 22 || doc.Timeouts.MemoryPersistSecs != 33 || doc.Timeouts.SubagentDefaultSecs != 44 {
		t.Fatalf("unexpected timeouts config: %#v", doc.Timeouts)
	}
	if doc.NostrChannels["dev"].Config["root_tag"] != "general" || len(doc.NostrChannels["dev"].AllowFrom) != 1 {
		t.Fatalf("unexpected nostr channel config: %#v", doc.NostrChannels["dev"])
	}
	if len(doc.Agents) != 1 || doc.Agents[0].MaxAgenticIterations != 12 {
		t.Fatalf("unexpected agent config: %#v", doc.Agents)
	}
}

func TestParseConfigBytesRejectsUnsupportedFieldsInsteadOfDroppingThem(t *testing.T) {
	_, err := ParseConfigBytes([]byte(`{
		"hooks": {"enabled": true, "bogus": true},
		"agents": [{"id":"main","model":"gpt-4o","bogus_agent_field":1}],
		"session": {"ttl_seconds": 1, "history_limit": 50}
	}`), ".json")
	if err == nil {
		t.Fatal("expected unsupported field error")
	}
	msg := err.Error()
	for _, want := range []string{"hooks.bogus", "agents[0].bogus_agent_field", "session.history_limit"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to mention %q, got: %v", want, err)
		}
	}
}

func TestParseConfigBytesOpenClawAgentsListWithoutDefaults(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{
		"agents": {
			"list": [{"id":"main","model":"gpt-4o"}]
		}
	}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if len(doc.Agents) != 1 || doc.Agents[0].ID != "main" {
		t.Fatalf("unexpected parsed agents: %#v", doc.Agents)
	}
}
