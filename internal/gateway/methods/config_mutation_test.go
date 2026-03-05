package methods

import (
	"strings"
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
	next, err = ApplyConfigSet(next, "plugins.entries.codegen.apiKey", "  abc123  ")
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins apiKey error: %v", err)
	}
	next, err = ApplyConfigSet(next, "plugins.entries.codegen.env", map[string]any{" OPENAI_API_KEY ": " sk-1 ", "EMPTY": "  "})
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins env error: %v", err)
	}
	next, err = ApplyConfigSet(next, "plugins.entries.codegen.gatewayMethods", []string{"ext.codegen.run"})
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins gatewayMethods error: %v", err)
	}
	next, err = ApplyConfigSet(next, "plugins.deny", []string{"rogue-plugin"})
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins deny error: %v", err)
	}
	next, err = ApplyConfigSet(next, "plugins.load.paths", []string{"./extensions", " ./more "})
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins load.paths error: %v", err)
	}
	next, err = ApplyConfigSet(next, "plugins.installs.codegen", map[string]any{"source": "npm", "spec": "@acme/codegen@1.0.0"})
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins installs entry error: %v", err)
	}
	next, err = ApplyConfigSet(next, "plugins.installs.codegen.version", "1.0.1")
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins installs field error: %v", err)
	}
	rawExt, _ := next.Extra["extensions"].(map[string]any)
	rawEntries, _ := rawExt["entries"].(map[string]any)
	codegen, _ := rawEntries["codegen"].(map[string]any)
	if enabled, _ := codegen["enabled"].(bool); !enabled {
		t.Fatalf("expected plugins.entries.codegen.enabled to be true, got: %#v", codegen)
	}
	if apiKey, _ := codegen["api_key"].(string); apiKey != "abc123" {
		t.Fatalf("unexpected plugins.entries.codegen.api_key: %#v", codegen)
	}
	envMap, err := anyToStringMap(codegen["env"])
	if err != nil {
		t.Fatalf("unexpected plugins.entries.codegen.env type: %v (%#v)", err, codegen["env"])
	}
	if envMap["OPENAI_API_KEY"] != "sk-1" {
		t.Fatalf("unexpected plugins.entries.codegen.env: %#v", codegen)
	}
	if _, ok := envMap["EMPTY"]; ok {
		t.Fatalf("expected EMPTY key to be dropped from plugins.entries.codegen.env: %#v", codegen)
	}
	methods, _ := codegen["gateway_methods"].([]string)
	if len(methods) != 1 || methods[0] != "ext.codegen.run" {
		t.Fatalf("unexpected plugins.entries.codegen.gateway_methods: %#v", codegen)
	}
	deny, _ := rawExt["deny"].([]string)
	if len(deny) != 1 || deny[0] != "rogue-plugin" {
		t.Fatalf("unexpected plugins.deny: %#v", rawExt)
	}
	loadPaths, _ := rawExt["load_paths"].([]string)
	if len(loadPaths) != 2 || loadPaths[0] != "./extensions" || loadPaths[1] != "./more" {
		t.Fatalf("unexpected plugins.load_paths: %#v", rawExt)
	}
	installs, _ := rawExt["installs"].(map[string]any)
	installCodegen, _ := installs["codegen"].(map[string]any)
	if installCodegen["source"] != "npm" || installCodegen["version"] != "1.0.1" {
		t.Fatalf("unexpected plugins.installs.codegen: %#v", installs)
	}

	next, err = ApplyConfigPatch(next, map[string]any{
		"plugins": map[string]any{
			"entries": map[string]any{
				"codegen": map[string]any{"enabled": false, "tools": []string{"codegen.apply"}, "apiKey": "", "env": map[string]any{"OPENAI_API_KEY": "", "OTHER": "still"}},
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
	if _, ok := codegen["api_key"]; ok {
		t.Fatalf("expected plugins.entries.codegen.api_key removed by empty patch: %#v", codegen)
	}
	if envRaw, ok := codegen["env"]; !ok {
		t.Fatalf("expected plugins.entries.codegen.env to preserve unrelated keys after patch: %#v", codegen)
	} else {
		envAfter, err := anyToStringMap(envRaw)
		if err != nil {
			t.Fatalf("unexpected env type after patch: %v (%#v)", err, envRaw)
		}
		if _, ok := envAfter["OPENAI_API_KEY"]; ok {
			t.Fatalf("expected OPENAI_API_KEY to be removed from env patch merge: %#v", envAfter)
		}
		if envAfter["OTHER"] != "still" {
			t.Fatalf("expected OTHER to remain after env patch merge: %#v", envAfter)
		}
	}
}

func TestConfigSchemaContainsCoreFields(t *testing.T) {
	cfg := state.ConfigDoc{Extra: map[string]any{
		"extensions": map[string]any{
			"enabled":    true,
			"load":       true,
			"allow":      []string{"codegen"},
			"deny":       []string{"blocked"},
			"load_paths": []string{"./extensions"},
			"installs":   map[string]any{"codegen": map[string]any{"source": "npm"}},
			"entries": map[string]any{
				"codegen": map[string]any{"enabled": true, "tools": []string{"codegen.apply"}, "gateway_methods": []string{"ext.codegen.run"}},
			},
		},
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
		"plugins.deny":                  {},
		"plugins.load":                  {},
		"plugins.load.paths":            {},
		"plugins.entries.<id>.enabled":  {},
		"plugins.entries.<id>.apiKey":   {},
		"plugins.entries.<id>.env":      {},
		"plugins.entries.<id>.tools":    {},
		"plugins.entries.<id>.gatewayMethods": {},
		"plugins.installs":              {},
		"plugins.installs.<id>":         {},
		"plugins.installs.<id>.<field>": {},
	}
	for _, field := range fields {
		delete(mustHave, field)
	}
	if len(mustHave) != 0 {
		t.Fatalf("missing schema fields: %+v", mustHave)
	}
	plugins, _ := s["plugins"].(map[string]any)
	if plugins["enabled"] != true || plugins["load"] != true {
		t.Fatalf("unexpected plugin schema enabled/load summary: %#v", plugins)
	}
	allow, _ := plugins["allow"].([]string)
	if len(allow) != 1 || allow[0] != "codegen" {
		t.Fatalf("unexpected plugin schema allow summary: %#v", plugins)
	}
	deny, _ := plugins["deny"].([]string)
	if len(deny) != 1 || deny[0] != "blocked" {
		t.Fatalf("unexpected plugin schema deny summary: %#v", plugins)
	}
	loadPaths, _ := plugins["loadPaths"].([]string)
	if len(loadPaths) != 1 || loadPaths[0] != "./extensions" {
		t.Fatalf("unexpected plugin schema loadPaths summary: %#v", plugins)
	}
	installs, _ := plugins["installs"].([]string)
	if len(installs) != 1 || installs[0] != "codegen" {
		t.Fatalf("unexpected plugin schema installs summary: %#v", plugins)
	}
	entries, _ := plugins["entries"].([]map[string]any)
	if len(entries) != 1 || entries[0]["id"] != "codegen" {
		t.Fatalf("unexpected plugin schema entries: %#v", s["plugins"])
	}

	cfg = state.ConfigDoc{Extra: map[string]any{"extensions": map[string]any{"entries": map[string]any{"codegen": map[string]any{"enabled": true, "api_key": "secret", "env": map[string]string{"OPENAI_API_KEY": "present"}}}}}}
	s = ConfigSchema(cfg)
	plugins, _ = s["plugins"].(map[string]any)
	entries, _ = plugins["entries"].([]map[string]any)
	if len(entries) != 1 || entries[0]["hasApiKey"] != true {
		t.Fatalf("expected hasApiKey=true in plugin schema entries: %#v", entries)
	}
	envKeys, _ := entries[0]["env"].([]string)
	if len(envKeys) != 1 || envKeys[0] != "OPENAI_API_KEY" {
		t.Fatalf("expected env key projection in plugin schema entries: %#v", entries)
	}
}

func TestApplyConfigSetPluginsInstallsFieldValidation(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1}
	_, err := ApplyConfigSet(cfg, "plugins.installs.codegen.", "bad")
	if err == nil {
		t.Fatal("expected plugins.installs.<id>.<field> empty field validation error")
	}
	if !strings.Contains(err.Error(), "non-empty field") {
		t.Fatalf("unexpected installs field validation error: %v", err)
	}
}

