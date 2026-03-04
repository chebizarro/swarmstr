package methods

import (
	"testing"

	"swarmstr/internal/store/state"
)

func TestApplyConfigSetAndPatch(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "pairing"}, Relays: state.RelayPolicy{Read: []string{"wss://r"}, Write: []string{"wss://r"}}}

	next, err := ApplyConfigSet(cfg, "dm.policy", "open")
	if err != nil {
		t.Fatalf("ApplyConfigSet error: %v", err)
	}
	if next.DM.Policy != "open" {
		t.Fatalf("expected dm.policy=open, got %q", next.DM.Policy)
	}

	next, err = ApplyConfigPatch(next, map[string]any{"relays": map[string]any{"read": []string{"wss://r2"}}})
	if err != nil {
		t.Fatalf("ApplyConfigPatch error: %v", err)
	}
	if len(next.Relays.Read) != 1 || next.Relays.Read[0] != "wss://r2" {
		t.Fatalf("unexpected relays.read: %+v", next.Relays.Read)
	}
}

func TestConfigSchemaContainsCoreFields(t *testing.T) {
	s := ConfigSchema()
	fields, ok := s["fields"].([]string)
	if !ok {
		t.Fatalf("unexpected schema payload: %#v", s)
	}
	mustHave := map[string]struct{}{
		"dm.policy":      {},
		"relays.read":    {},
		"relays.write":   {},
		"agent.verbose":  {},
		"control.require_auth": {},
	}
	for _, field := range fields {
		delete(mustHave, field)
	}
	if len(mustHave) != 0 {
		t.Fatalf("missing schema fields: %+v", mustHave)
	}
}
