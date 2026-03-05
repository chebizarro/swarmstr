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

	next, err = ApplyConfigSet(next, "plugins.entries.codegen.enabled", true)
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins enabled error: %v", err)
	}
	next, err = ApplyConfigSet(next, "plugins.entries.codegen.gatewayMethods", []string{"ext.codegen.run"})
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins gatewayMethods error: %v", err)
	}
	rawExt, _ := next.Extra["extensions"].(map[string]any)
	rawEntries, _ := rawExt["entries"].(map[string]any)
	codegen, _ := rawEntries["codegen"].(map[string]any)
	if enabled, _ := codegen["enabled"].(bool); !enabled {
		t.Fatalf("expected plugins.entries.codegen.enabled to be true, got: %#v", codegen)
	}
	methods, _ := codegen["gateway_methods"].([]string)
	if len(methods) != 1 || methods[0] != "ext.codegen.run" {
		t.Fatalf("unexpected plugins.entries.codegen.gateway_methods: %#v", codegen)
	}

	next, err = ApplyConfigPatch(next, map[string]any{
		"plugins": map[string]any{
			"entries": map[string]any{
				"codegen": map[string]any{"enabled": false, "tools": []string{"codegen.apply"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyConfigPatch plugins nested error: %v", err)
	}
	rawExt, _ = next.Extra["extensions"].(map[string]any)
	rawEntries, _ = rawExt["entries"].(map[string]any)
	codegen, _ = rawEntries["codegen"].(map[string]any)
	if enabled, _ := codegen["enabled"].(bool); enabled {
		t.Fatalf("expected plugins.entries.codegen.enabled to be false after patch, got: %#v", codegen)
	}
	tools, _ := codegen["tools"].([]string)
	if len(tools) != 1 || tools[0] != "codegen.apply" {
		t.Fatalf("unexpected plugins.entries.codegen.tools after patch: %#v", codegen)
	}
}

func TestConfigSchemaContainsCoreFields(t *testing.T) {
	cfg := state.ConfigDoc{Extra: map[string]any{
		"extensions": map[string]any{"entries": map[string]any{
			"codegen": map[string]any{"enabled": true, "tools": []string{"codegen.apply"}, "gateway_methods": []string{"ext.codegen.run"}},
		}},
	}}
	s := ConfigSchema(cfg)
	fields, ok := s["fields"].([]string)
	if !ok {
		t.Fatalf("unexpected schema payload: %#v", s)
	}
	mustHave := map[string]struct{}{
		"dm.policy":                     {},
		"relays.read":                   {},
		"relays.write":                  {},
		"agent.verbose":                 {},
		"control.require_auth":          {},
		"plugins.entries.<id>.enabled":  {},
		"plugins.entries.<id>.tools":    {},
		"plugins.entries.<id>.gatewayMethods": {},
	}
	for _, field := range fields {
		delete(mustHave, field)
	}
	if len(mustHave) != 0 {
		t.Fatalf("missing schema fields: %+v", mustHave)
	}
	plugins, _ := s["plugins"].(map[string]any)
	entries, _ := plugins["entries"].([]map[string]any)
	if len(entries) != 1 || entries[0]["id"] != "codegen" {
		t.Fatalf("unexpected plugin schema entries: %#v", s["plugins"])
	}
}
