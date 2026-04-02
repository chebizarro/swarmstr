package policy

import (
	"testing"

	"metiq/internal/store/state"
)

// ─── ConfigChangedNeedsRestart ────────────────────────────────────────────────

func baseDoc() state.ConfigDoc {
	return state.ConfigDoc{
		DM:    state.DMPolicy{Policy: "open"},
		Agent: state.AgentPolicy{DefaultModel: "echo"},
		Relays: state.RelayPolicy{
			Read:  []string{"wss://relay.example"},
			Write: []string{"wss://relay.example"},
		},
	}
}

func TestConfigChangedNeedsRestart_noChange(t *testing.T) {
	d := baseDoc()
	if ConfigChangedNeedsRestart(d, d) {
		t.Fatal("identical docs should not need restart")
	}
}

func TestConfigChangedNeedsRestart_dmPolicyChange(t *testing.T) {
	old := baseDoc()
	next := baseDoc()
	next.DM.Policy = "pairing"
	if ConfigChangedNeedsRestart(old, next) {
		t.Fatal("DM policy change should be hot-applied (no restart)")
	}
}

func TestConfigChangedNeedsRestart_relayChange(t *testing.T) {
	old := baseDoc()
	next := baseDoc()
	next.Relays.Read = []string{"wss://other.relay"}
	if ConfigChangedNeedsRestart(old, next) {
		t.Fatal("relay change should be hot-applied (no restart)")
	}
}

func TestConfigChangedNeedsRestart_modelChange(t *testing.T) {
	old := baseDoc()
	next := baseDoc()
	next.Agent.DefaultModel = "http"
	if !ConfigChangedNeedsRestart(old, next) {
		t.Fatal("model change should require restart")
	}
}

func TestConfigChangedNeedsRestart_providerAPIKeyChange(t *testing.T) {
	old := baseDoc()
	old.Providers = state.ProvidersConfig{"openai": {APIKey: "old-key"}}
	next := baseDoc()
	next.Providers = state.ProvidersConfig{"openai": {APIKey: "new-key"}}
	if !ConfigChangedNeedsRestart(old, next) {
		t.Fatal("provider API key change should require restart")
	}
}

func TestConfigChangedNeedsRestart_providerAdded(t *testing.T) {
	old := baseDoc()
	next := baseDoc()
	next.Providers = state.ProvidersConfig{"anthropic": {Enabled: true}}
	if !ConfigChangedNeedsRestart(old, next) {
		t.Fatal("adding a new provider should require restart")
	}
}

func TestConfigChangedNeedsRestart_extensionChanged(t *testing.T) {
	old := baseDoc()
	old.Extra = map[string]any{"extensions": []any{"plugin-a"}}
	next := baseDoc()
	next.Extra = map[string]any{"extensions": []any{"plugin-a", "plugin-b"}}
	if !ConfigChangedNeedsRestart(old, next) {
		t.Fatal("extension change should require restart")
	}
}

// ─── Agents config validation ─────────────────────────────────────────────────

func TestValidateAgents_valid(t *testing.T) {
	d := baseDoc()
	d.Agents = state.AgentsConfig{
		{ID: "main", Name: "Main Agent", Model: "echo", ToolProfile: "coding"},
		{ID: "secondary", ToolProfile: "minimal"},
	}
	if err := ValidateConfig(d); err != nil {
		t.Fatalf("expected valid agents config, got: %v", err)
	}
}

func TestValidateAgents_missingID(t *testing.T) {
	d := baseDoc()
	d.Agents = state.AgentsConfig{{Name: "No ID"}}
	if err := ValidateConfig(d); err == nil {
		t.Fatal("expected error for agent with missing id")
	}
}

func TestValidateAgents_duplicateID(t *testing.T) {
	d := baseDoc()
	d.Agents = state.AgentsConfig{
		{ID: "main"},
		{ID: "main"},
	}
	if err := ValidateConfig(d); err == nil {
		t.Fatal("expected error for duplicate agent id")
	}
}

func TestValidateAgents_invalidToolProfile(t *testing.T) {
	d := baseDoc()
	d.Agents = state.AgentsConfig{{ID: "x", ToolProfile: "ultra"}}
	if err := ValidateConfig(d); err == nil {
		t.Fatal("expected error for unknown tool_profile")
	}
}

func TestValidateACPTransportRejectsInvalidMode(t *testing.T) {
	d := baseDoc()
	d.ACP.Transport = "nip004"
	if err := ValidateConfig(d); err == nil {
		t.Fatal("expected invalid acp.transport error")
	}
}

func TestValidateNostrChannels_relayFilterNIP34RequiresRepoAddr(t *testing.T) {
	d := baseDoc()
	d.NostrChannels = state.NostrChannelsConfig{
		"repo-events": {
			Kind:   string(state.NostrChannelKindRelayFilter),
			Config: map[string]any{"mode": "nip34"},
			Relays: []string{"wss://relay.example"},
		},
	}
	if err := ValidateConfig(d); err == nil {
		t.Fatal("expected error when relay-filter mode=nip34 omits tags.a")
	}
}

func TestValidateNostrChannels_NIP34AutoReviewRejectsInvalidToolProfile(t *testing.T) {
	d := baseDoc()
	d.NostrChannels = state.NostrChannelsConfig{
		"repo-events": {
			Kind:   string(state.NostrChannelKindNIP34Inbox),
			Relays: []string{"wss://relay.example"},
			Tags:   map[string][]string{"a": {"30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"}},
			Config: map[string]any{
				"auto_review": map[string]any{
					"enabled":      true,
					"tool_profile": "ultra",
				},
			},
		},
	}
	if err := ValidateConfig(d); err == nil {
		t.Fatal("expected invalid tool_profile error for auto_review config")
	}
}

func TestValidateNostrChannels_NIP34AutoReviewAcceptsValidConfig(t *testing.T) {
	d := baseDoc()
	d.NostrChannels = state.NostrChannelsConfig{
		"repo-events": {
			Kind:   string(state.NostrChannelKindNIP34Inbox),
			Relays: []string{"wss://relay.example"},
			Tags:   map[string][]string{"a": {"30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"}},
			Config: map[string]any{
				"auto_review": map[string]any{
					"enabled":       true,
					"tool_profile":  "coding",
					"enabled_tools": []any{"memory_search"},
					"trigger_types": []any{"pull_request", "pull_request_update"},
				},
			},
		},
	}
	if err := ValidateConfig(d); err != nil {
		t.Fatalf("expected valid auto_review config, got %v", err)
	}
}

func TestNormalizeConfigRelaySets(t *testing.T) {
	cfg := NormalizeConfig(state.ConfigDoc{
		DM: state.DMPolicy{Policy: ""},
		Relays: state.RelayPolicy{
			Read:  []string{" wss://relay.example ", "wss://relay.example"},
			Write: []string{"wss://relay.example", "ws://relay-2.example"},
		},
	})
	if cfg.DM.Policy != DMPolicyPairing {
		t.Fatalf("unexpected default dm policy: %q", cfg.DM.Policy)
	}
	if len(cfg.Relays.Read) != 1 {
		t.Fatalf("expected deduped read relays, got %v", cfg.Relays.Read)
	}
	if len(cfg.Relays.Write) != 2 {
		t.Fatalf("expected normalized write relays, got %v", cfg.Relays.Write)
	}
	if !cfg.StorageEncryptEnabled() {
		t.Fatalf("expected storage encryption default to be enabled, got %#v", cfg.Storage)
	}
}

func TestNormalizeConfigACPTransport(t *testing.T) {
	cfg := NormalizeConfig(state.ConfigDoc{ACP: state.ACPConfig{Transport: "nip-04"}})
	if cfg.ACP.Transport != "nip04" {
		t.Fatalf("expected acp.transport to normalize to nip04, got %#v", cfg.ACP)
	}
}

func TestAuthLevelOf(t *testing.T) {
	allow := []string{"owner-key", "trusted:trusted-key", "public-key"}

	if got := authLevelOf("owner-key", allow); got != AuthOwner {
		t.Fatalf("got %v want AuthOwner", got)
	}
	if got := authLevelOf("trusted-key", allow); got != AuthTrusted {
		t.Fatalf("got %v want AuthTrusted", got)
	}
	if got := authLevelOf("public-key", allow); got != AuthTrusted {
		t.Fatalf("got %v want AuthTrusted", got)
	}
	if got := authLevelOf("unknown", allow); got != AuthDenied {
		t.Fatalf("got %v want AuthDenied", got)
	}
	if got := authLevelOf("", allow); got != AuthDenied {
		t.Fatalf("got %v want AuthDenied for empty sender", got)
	}
}

func TestEvaluateIncomingDM_AuthLevel(t *testing.T) {
	cfg := baseDoc()
	cfg.DM.Policy = DMPolicyAllowlist
	cfg.DM.AllowFrom = []string{"ownerkey", "trusted:trustedkey"}

	dec := EvaluateIncomingDM("ownerkey", cfg)
	if !dec.Allowed || dec.Level != AuthOwner {
		t.Fatalf("expected owner allowed: %+v", dec)
	}
	dec = EvaluateIncomingDM("trustedkey", cfg)
	if !dec.Allowed || dec.Level != AuthTrusted {
		t.Fatalf("expected trusted allowed: %+v", dec)
	}
	dec = EvaluateIncomingDM("unknown", cfg)
	if dec.Allowed || dec.Level != AuthDenied {
		t.Fatalf("expected denied: %+v", dec)
	}

	// Open policy gives at least AuthPublic to all.
	openCfg := baseDoc()
	openCfg.DM.Policy = DMPolicyOpen
	dec = EvaluateIncomingDM("anykey", openCfg)
	if !dec.Allowed || dec.Level < AuthPublic {
		t.Fatalf("expected public+ on open policy: %+v", dec)
	}
}

func TestEvaluateGroupMessage(t *testing.T) {
	cfg := baseDoc()
	cfg.DM.Policy = DMPolicyAllowlist
	cfg.DM.AllowFrom = []string{"aabbcc"}

	// Channel-specific allowlist takes precedence.
	dec := EvaluateGroupMessage("aabbcc", []string{"aabbcc"}, cfg)
	if !dec.Allowed {
		t.Fatalf("expected allowed with channel allowlist: %s", dec.Reason)
	}
	dec = EvaluateGroupMessage("unknown", []string{"aabbcc"}, cfg)
	if dec.Allowed {
		t.Fatal("expected rejected: not in channel allowlist")
	}

	// Wildcard allows all.
	dec = EvaluateGroupMessage("anyone", []string{"*"}, cfg)
	if !dec.Allowed {
		t.Fatal("expected allowed: wildcard")
	}

	// Empty channel allowlist falls back to DM policy.
	dec = EvaluateGroupMessage("aabbcc", nil, cfg)
	if !dec.Allowed {
		t.Fatalf("expected allowed via DM allowlist fallback: %s", dec.Reason)
	}
	dec = EvaluateGroupMessage("unknown", nil, cfg)
	if dec.Allowed {
		t.Fatal("expected rejected via DM allowlist fallback")
	}

	// Open DM policy allows all.
	openCfg := baseDoc()
	openCfg.DM.Policy = DMPolicyOpen
	dec = EvaluateGroupMessage("anyone", nil, openCfg)
	if !dec.Allowed {
		t.Fatal("expected allowed: open policy")
	}
}

func TestValidateConfigRejectsInvalidRelayPolicy(t *testing.T) {
	cfg := state.ConfigDoc{
		DM:     state.DMPolicy{Policy: DMPolicyOpen},
		Relays: state.RelayPolicy{Read: []string{"https://example.com"}, Write: []string{"wss://relay.example"}},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected invalid relay scheme error")
	}
}
