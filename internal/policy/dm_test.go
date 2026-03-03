package policy

import (
	"testing"

	"swarmstr/internal/store/state"
)

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
