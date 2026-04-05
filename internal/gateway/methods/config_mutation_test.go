package methods

import (
	"strings"
	"testing"

	mcppkg "metiq/internal/mcp"
	"metiq/internal/store/state"
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
	if installedAt, _ := installCodegen["installedAt"].(string); strings.TrimSpace(installedAt) == "" {
		t.Fatalf("expected plugins.installs.codegen.installedAt to be set: %#v", installCodegen)
	}

	next, err = ApplyConfigSet(next, "mcp.enabled", true)
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp.enabled error: %v", err)
	}
	next, err = ApplyConfigSet(next, "mcp.servers.files.enabled", true)
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp server enabled error: %v", err)
	}
	next, err = ApplyConfigSet(next, "mcp.servers.files.command", " npx ")
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp server command error: %v", err)
	}
	next, err = ApplyConfigSet(next, "mcp.servers.files.args", []string{" -y ", "server-filesystem", "/tmp", "/tmp"})
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp server args error: %v", err)
	}
	next, err = ApplyConfigSet(next, "mcp.servers.files.env", map[string]any{" NODE_ENV ": " production ", "EMPTY": " "})
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp server env error: %v", err)
	}
	next, err = ApplyConfigSet(next, "mcp.servers.remote", map[string]any{
		"enabled": true,
		"type":    " HTTP ",
		"url":     " https://mcp.example.com/http ",
		"headers": map[string]any{" Authorization ": " Bearer tok "},
		"oauth": map[string]any{
			"enabled":       true,
			"client_id":     " client-1 ",
			"authorize_url": " https://mcp.example.com/oauth/authorize ",
			"token_url":     " https://mcp.example.com/oauth/token ",
			"scopes":        []string{" profile ", "offline_access"},
			"use_pkce":      true,
		},
	})
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp server object error: %v", err)
	}
	rawMCP, _ := next.Extra["mcp"].(map[string]any)
	if enabled, _ := rawMCP["enabled"].(bool); !enabled {
		t.Fatalf("expected mcp.enabled=true, got %#v", rawMCP)
	}
	rawServers, _ := rawMCP["servers"].(map[string]any)
	files, _ := rawServers["files"].(map[string]any)
	if files["command"] != "npx" {
		t.Fatalf("unexpected mcp.servers.files.command: %#v", files)
	}
	args, _ := files["args"].([]string)
	if len(args) != 4 || args[0] != "-y" || args[1] != "server-filesystem" || args[2] != "/tmp" || args[3] != "/tmp" {
		t.Fatalf("expected ordered duplicate-preserving args, got %#v", files["args"])
	}
	envRaw, ok := files["env"]
	if !ok {
		t.Fatalf("expected mcp.servers.files.env: %#v", files)
	}
	envMap, err = anyToStringMap(envRaw)
	if err != nil {
		t.Fatalf("unexpected mcp.servers.files.env type: %v (%#v)", err, envRaw)
	}
	if envMap["NODE_ENV"] != "production" {
		t.Fatalf("unexpected mcp.servers.files.env: %#v", envMap)
	}
	if _, ok := envMap["EMPTY"]; ok {
		t.Fatalf("expected EMPTY key to be dropped from mcp.servers.files.env: %#v", envMap)
	}
	remote, _ := rawServers["remote"].(map[string]any)
	if remote["type"] != "http" || remote["url"] != "https://mcp.example.com/http" {
		t.Fatalf("unexpected mcp.servers.remote normalization: %#v", remote)
	}
	oauth, _ := remote["oauth"].(map[string]any)
	if oauth["client_id"] != "client-1" || oauth["authorize_url"] != "https://mcp.example.com/oauth/authorize" {
		t.Fatalf("unexpected mcp oauth normalization: %#v", oauth)
	}
	scopes, _ := oauth["scopes"].([]string)
	if len(scopes) != 2 || scopes[0] != "profile" || scopes[1] != "offline_access" {
		t.Fatalf("unexpected mcp oauth scopes: %#v", oauth)
	}
	next, err = ApplyConfigSet(next, "mcp.servers.remote.oauth.callback_port", 4317)
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp oauth callback_port error: %v", err)
	}
	next, err = ApplyConfigSet(next, "mcp.servers.remote.oauth.client_secret_ref", " env:MCP_SECRET ")
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp oauth client_secret_ref error: %v", err)
	}
	next, err = ApplyConfigSet(next, "mcp.policy.allowed", []any{
		map[string]any{"name": " remote "},
		map[string]any{"url": " https://mcp.example.com/* "},
	})
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp policy allowed error: %v", err)
	}
	next, err = ApplyConfigSet(next, "mcp.policy.denied", []any{
		map[string]any{"command": []any{" npx ", " -y ", "server-filesystem", "/tmp", "/tmp"}},
	})
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp policy denied error: %v", err)
	}
	next, err = ApplyConfigSet(next, "mcp.policy.require_remote_approval", true)
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp policy require_remote_approval error: %v", err)
	}
	next, err = ApplyConfigSet(next, "mcp.policy.approved_servers", []string{" remote "})
	if err != nil {
		t.Fatalf("ApplyConfigSet mcp policy approved_servers error: %v", err)
	}
	rawMCP, _ = next.Extra["mcp"].(map[string]any)
	rawServers, _ = rawMCP["servers"].(map[string]any)
	remote, _ = rawServers["remote"].(map[string]any)
	oauth, _ = remote["oauth"].(map[string]any)
	if oauth["callback_port"] != 4317 || oauth["client_secret_ref"] != "env:MCP_SECRET" {
		t.Fatalf("unexpected nested mcp oauth fields: %#v", oauth)
	}
	rawPolicy, _ := rawMCP["policy"].(map[string]any)
	if rawPolicy["require_remote_approval"] != true {
		t.Fatalf("expected mcp.policy.require_remote_approval=true, got %#v", rawPolicy)
	}
	approvedServers, _ := rawPolicy["approved_servers"].([]string)
	if len(approvedServers) != 1 || approvedServers[0] != "remote" {
		t.Fatalf("unexpected mcp.policy.approved_servers: %#v", rawPolicy)
	}
	allowedMatchers, _ := rawPolicy["allowed"].([]map[string]any)
	if len(allowedMatchers) != 2 || allowedMatchers[0]["name"] != "remote" || allowedMatchers[1]["url"] != "https://mcp.example.com/*" {
		t.Fatalf("unexpected mcp.policy.allowed normalization: %#v", rawPolicy["allowed"])
	}
	deniedMatchers, _ := rawPolicy["denied"].([]map[string]any)
	deniedCommand, _ := deniedMatchers[0]["command"].([]string)
	if len(deniedCommand) != 5 || deniedCommand[0] != "npx" || deniedCommand[1] != "-y" {
		t.Fatalf("unexpected mcp.policy.denied normalization: %#v", rawPolicy["denied"])
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
	next, err = ApplyConfigPatch(next, map[string]any{
		"mcp": map[string]any{
			"servers": map[string]any{
				"files": map[string]any{
					"env": map[string]any{
						"NODE_ENV": "",
						"OTHER":    "still",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyConfigPatch mcp nested error: %v", err)
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
	rawMCP, _ = next.Extra["mcp"].(map[string]any)
	rawServers, _ = rawMCP["servers"].(map[string]any)
	files, _ = rawServers["files"].(map[string]any)
	envRaw = files["env"]
	envMap, err = anyToStringMap(envRaw)
	if err != nil {
		t.Fatalf("unexpected MCP env type after patch: %v (%#v)", err, envRaw)
	}
	if _, ok := envMap["NODE_ENV"]; ok {
		t.Fatalf("expected NODE_ENV to be removed from MCP env patch merge: %#v", envMap)
	}
	if envMap["OTHER"] != "still" {
		t.Fatalf("expected OTHER to remain after MCP env patch merge: %#v", envMap)
	}
}

func TestApplyConfigSetMCPRejectsInvalidType(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1}
	if _, err := ApplyConfigSet(cfg, "mcp.servers.demo.type", "ws"); err == nil {
		t.Fatalf("expected invalid MCP transport type error")
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
		"mcp": map[string]any{
			"enabled": true,
			"servers": map[string]any{
				"filesystem": map[string]any{"enabled": true, "command": "npx", "args": []string{"-y", "server-filesystem", "/tmp"}},
				"duplicate":  map[string]any{"enabled": true, "command": "npx", "args": []string{"-y", "server-filesystem", "/tmp"}},
				"remote":     map[string]any{"enabled": true, "type": "http", "url": "https://remote.example.com/mcp"},
			},
			"policy": map[string]any{
				"require_remote_approval": true,
				"approved_servers":        []string{},
			},
		},
	}}
	s := ConfigSchema(cfg)
	fields, ok := s["fields"].([]string)
	if !ok {
		t.Fatalf("unexpected schema payload: %#v", s)
	}
	mustHave := map[string]struct{}{
		"dm.policy":                                {},
		"dm.reply_scheme":                          {},
		"relays.read":                              {},
		"relays.write":                             {},
		"storage.encrypt":                          {},
		"acp.transport":                            {},
		"agent.verbose":                            {},
		"control.require_auth":                     {},
		"plugins.deny":                             {},
		"plugins.load":                             {},
		"plugins.load.paths":                       {},
		"plugins.entries.<id>.enabled":             {},
		"plugins.entries.<id>.apiKey":              {},
		"plugins.entries.<id>.env":                 {},
		"plugins.entries.<id>.tools":               {},
		"plugins.entries.<id>.gatewayMethods":      {},
		"plugins.installs":                         {},
		"plugins.installs.<id>":                    {},
		"plugins.installs.<id>.source":             {},
		"plugins.installs.<id>.spec":               {},
		"plugins.installs.<id>.sourcePath":         {},
		"plugins.installs.<id>.installPath":        {},
		"plugins.installs.<id>.version":            {},
		"plugins.installs.<id>.resolvedName":       {},
		"plugins.installs.<id>.resolvedVersion":    {},
		"plugins.installs.<id>.resolvedSpec":       {},
		"plugins.installs.<id>.integrity":          {},
		"plugins.installs.<id>.shasum":             {},
		"plugins.installs.<id>.resolvedAt":         {},
		"plugins.installs.<id>.installedAt":        {},
		"plugins.installs.<id>.<field>":            {},
		"mcp.enabled":                              {},
		"mcp.policy":                               {},
		"mcp.policy.allowed":                       {},
		"mcp.policy.denied":                        {},
		"mcp.policy.require_remote_approval":       {},
		"mcp.policy.approved_servers":              {},
		"mcp.servers":                              {},
		"mcp.servers.<id>":                         {},
		"mcp.servers.<id>.enabled":                 {},
		"mcp.servers.<id>.command":                 {},
		"mcp.servers.<id>.args":                    {},
		"mcp.servers.<id>.env":                     {},
		"mcp.servers.<id>.type":                    {},
		"mcp.servers.<id>.url":                     {},
		"mcp.servers.<id>.headers":                 {},
		"mcp.servers.<id>.oauth":                   {},
		"mcp.servers.<id>.oauth.enabled":           {},
		"mcp.servers.<id>.oauth.client_id":         {},
		"mcp.servers.<id>.oauth.client_secret_ref": {},
		"mcp.servers.<id>.oauth.authorize_url":     {},
		"mcp.servers.<id>.oauth.token_url":         {},
		"mcp.servers.<id>.oauth.revoke_url":        {},
		"mcp.servers.<id>.oauth.scopes":            {},
		"mcp.servers.<id>.oauth.callback_host":     {},
		"mcp.servers.<id>.oauth.callback_port":     {},
		"mcp.servers.<id>.oauth.use_pkce":          {},
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
	mcpSummary, _ := s["mcp"].(map[string]any)
	if mcpSummary["enabled"] != true {
		t.Fatalf("expected mcp schema enabled summary: %#v", mcpSummary)
	}
	mcpServers, _ := mcpSummary["servers"].([]map[string]any)
	if len(mcpServers) != 1 || mcpServers[0]["name"] != "duplicate" {
		t.Fatalf("expected deduplicated mcp schema servers summary: %#v", mcpSummary)
	}
	mcpSuppressed, _ := mcpSummary["suppressed"].([]map[string]any)
	if len(mcpSuppressed) != 1 || mcpSuppressed[0]["reason"] != mcppkg.SuppressionReasonDuplicateSignature {
		t.Fatalf("expected suppressed mcp schema summary: %#v", mcpSummary)
	}
	mcpFiltered, _ := mcpSummary["filtered"].([]map[string]any)
	if len(mcpFiltered) != 1 || mcpFiltered[0]["name"] != "remote" || mcpFiltered[0]["policy_status"] != mcppkg.PolicyStatusApprovalRequired {
		t.Fatalf("expected filtered mcp schema summary: %#v", mcpSummary)
	}
	policySummary, _ := mcpSummary["policy"].(map[string]any)
	if policySummary["require_remote_approval"] != true {
		t.Fatalf("expected mcp policy schema summary: %#v", mcpSummary)
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

func TestApplyConfigSetStorageEncrypt(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1}
	next, err := ApplyConfigSet(cfg, "storage.encrypt", false)
	if err != nil {
		t.Fatalf("ApplyConfigSet storage.encrypt error: %v", err)
	}
	if next.Storage.Encrypt == nil || *next.Storage.Encrypt {
		t.Fatalf("expected storage.encrypt=false, got %#v", next.Storage)
	}
}

func TestApplyConfigSetACPTransport(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1}
	next, err := ApplyConfigSet(cfg, "acp.transport", "nip-04")
	if err != nil {
		t.Fatalf("ApplyConfigSet acp.transport error: %v", err)
	}
	if next.ACP.Transport != "nip04" {
		t.Fatalf("expected acp.transport=nip04, got %#v", next.ACP)
	}
	if _, err := ApplyConfigSet(cfg, "acp.transport", "smtp"); err == nil {
		t.Fatalf("expected acp.transport validation error")
	}
}

func TestApplyConfigSetDMReplyScheme(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1}
	next, err := ApplyConfigSet(cfg, "dm.reply_scheme", "nip-17")
	if err != nil {
		t.Fatalf("ApplyConfigSet dm.reply_scheme error: %v", err)
	}
	if next.DM.ReplyScheme != "nip17" {
		t.Fatalf("expected dm.reply_scheme=nip17, got %#v", next.DM)
	}
	if _, err := ApplyConfigSet(cfg, "dm.reply_scheme", "smtp"); err == nil {
		t.Fatalf("expected dm.reply_scheme validation error")
	}
}

func TestApplyConfigSetPluginsInstallsLifecycleParity(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1}
	next, err := ApplyConfigSet(cfg, "plugins.installs.codegen", map[string]any{
		"source":       " npm ",
		"spec":         " @acme/codegen@1.0.0 ",
		"install_path": " /tmp/codegen ",
	})
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins.installs.<id> error: %v", err)
	}
	rawExt, _ := next.Extra["extensions"].(map[string]any)
	installs, _ := rawExt["installs"].(map[string]any)
	codegen, _ := installs["codegen"].(map[string]any)
	if codegen["source"] != "npm" || codegen["spec"] != "@acme/codegen@1.0.0" {
		t.Fatalf("unexpected normalized install record fields: %#v", codegen)
	}
	if codegen["installPath"] != "/tmp/codegen" {
		t.Fatalf("expected installPath canonicalization, got: %#v", codegen)
	}
	installedAt1, _ := codegen["installedAt"].(string)
	if strings.TrimSpace(installedAt1) == "" {
		t.Fatalf("expected installedAt to be set: %#v", codegen)
	}

	next, err = ApplyConfigSet(next, "plugins.installs.codegen.version", " 1.0.1 ")
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins.installs.<id>.<field> error: %v", err)
	}
	rawExt, _ = next.Extra["extensions"].(map[string]any)
	installs, _ = rawExt["installs"].(map[string]any)
	codegen, _ = installs["codegen"].(map[string]any)
	if codegen["version"] != "1.0.1" {
		t.Fatalf("expected version trim normalization, got: %#v", codegen)
	}
	installedAt2, _ := codegen["installedAt"].(string)
	if strings.TrimSpace(installedAt2) == "" {
		t.Fatalf("expected installedAt to be present on update: %#v", codegen)
	}
	if installedAt2 == installedAt1 {
		t.Fatalf("expected installedAt to refresh on record update: before=%q after=%q", installedAt1, installedAt2)
	}

	next, err = ApplyConfigSet(next, "plugins.installs.codegen.spec", "")
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins.installs.codegen.spec clear error: %v", err)
	}
	rawExt, _ = next.Extra["extensions"].(map[string]any)
	installs, _ = rawExt["installs"].(map[string]any)
	codegen, _ = installs["codegen"].(map[string]any)
	if _, ok := codegen["spec"]; ok {
		t.Fatalf("expected empty spec to remove field: %#v", codegen)
	}

	next, err = ApplyConfigSet(next, "plugins.installs.codegen.source", "")
	if err != nil {
		t.Fatalf("ApplyConfigSet plugins.installs.codegen.source clear error: %v", err)
	}
	rawExt, _ = next.Extra["extensions"].(map[string]any)
	if _, ok := rawExt["installs"]; ok {
		t.Fatalf("expected install record removal when source is cleared: %#v", rawExt)
	}
}

func TestApplyConfigSetPluginsInstallsSourceValidation(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1}
	_, err := ApplyConfigSet(cfg, "plugins.installs.codegen.source", "git")
	if err == nil {
		t.Fatal("expected source enum validation error")
	}
	if !strings.Contains(err.Error(), "one of npm, archive, path") {
		t.Fatalf("unexpected source validation error: %v", err)
	}

	next, err := ApplyConfigSet(cfg, "plugins.installs.codegen", map[string]any{"spec": "@acme/codegen@1.0.0"})
	if err == nil {
		t.Fatal("expected missing source validation error")
	}
	if !strings.Contains(err.Error(), "source is required") {
		t.Fatalf("unexpected missing source validation error: %v", err)
	}
	if next.Extra != nil {
		t.Fatalf("expected config to remain unchanged on missing source validation error: %#v", next)
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

func TestApplyPluginInstallOperation(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1}
	next, err := ApplyPluginInstallOperation(cfg, "codegen", map[string]any{
		"source":      "path",
		"sourcePath":  " ./extensions/codegen ",
		"installPath": " ./extensions/codegen ",
		"version":     " 1.0.0 ",
	}, true, true)
	if err != nil {
		t.Fatalf("ApplyPluginInstallOperation error: %v", err)
	}
	rawExt, _ := next.Extra["extensions"].(map[string]any)
	rawEntries, _ := rawExt["entries"].(map[string]any)
	entry, _ := rawEntries["codegen"].(map[string]any)
	if enabled, _ := entry["enabled"].(bool); !enabled {
		t.Fatalf("expected codegen entry enabled after install operation: %#v", entry)
	}
	rawInstalls, _ := rawExt["installs"].(map[string]any)
	record, _ := rawInstalls["codegen"].(map[string]any)
	if record["source"] != "path" || record["version"] != "1.0.0" {
		t.Fatalf("unexpected normalized install record: %#v", record)
	}
	if installedAt, _ := record["installedAt"].(string); strings.TrimSpace(installedAt) == "" {
		t.Fatalf("expected install operation to stamp installedAt: %#v", record)
	}
	loadPaths, _ := rawExt["load_paths"].([]string)
	if len(loadPaths) != 1 || loadPaths[0] != "./extensions/codegen" {
		t.Fatalf("expected sourcePath to be added to plugins.load_paths: %#v", rawExt)
	}

	next, err = ApplyPluginInstallOperation(next, "codegen", map[string]any{
		"source":      "path",
		"sourcePath":  "./extensions/codegen",
		"installPath": "./extensions/codegen",
		"version":     "1.0.1",
	}, true, true)
	if err != nil {
		t.Fatalf("ApplyPluginInstallOperation idempotent path add error: %v", err)
	}
	rawExt, _ = next.Extra["extensions"].(map[string]any)
	loadPaths, _ = rawExt["load_paths"].([]string)
	if len(loadPaths) != 1 || loadPaths[0] != "./extensions/codegen" {
		t.Fatalf("expected deduplicated load paths after repeated install operation: %#v", rawExt)
	}
}

func TestApplyPluginUninstallOperation(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1, Extra: map[string]any{"extensions": map[string]any{
		"allow":      []string{"codegen", "other"},
		"load_paths": []string{"./extensions/codegen", "./extensions/other"},
		"entries": map[string]any{
			"codegen": map[string]any{"enabled": true},
			"other":   map[string]any{"enabled": true},
		},
		"installs": map[string]any{
			"codegen": map[string]any{"source": "path", "sourcePath": "./extensions/codegen", "installPath": "./extensions/codegen"},
			"other":   map[string]any{"source": "npm", "spec": "other@1.0.0"},
		},
		"slots": map[string]any{"memory": "codegen"},
	}}}

	next, actions, err := ApplyPluginUninstallOperation(cfg, "codegen")
	if err != nil {
		t.Fatalf("ApplyPluginUninstallOperation error: %v", err)
	}
	if !actions.Entry || !actions.Install || !actions.Allowlist || !actions.LoadPath || !actions.MemorySlot {
		t.Fatalf("unexpected uninstall actions: %#v", actions)
	}
	rawExt, _ := next.Extra["extensions"].(map[string]any)
	rawEntries, _ := rawExt["entries"].(map[string]any)
	if _, ok := rawEntries["codegen"]; ok {
		t.Fatalf("expected codegen removed from entries: %#v", rawEntries)
	}
	rawInstalls, _ := rawExt["installs"].(map[string]any)
	if _, ok := rawInstalls["codegen"]; ok {
		t.Fatalf("expected codegen removed from installs: %#v", rawInstalls)
	}
	allow, _ := rawExt["allow"].([]string)
	if len(allow) != 1 || allow[0] != "other" {
		t.Fatalf("expected codegen removed from allow list: %#v", rawExt)
	}
	loadPaths, _ := rawExt["load_paths"].([]string)
	if len(loadPaths) != 1 || loadPaths[0] != "./extensions/other" {
		t.Fatalf("expected codegen sourcePath removed from load paths: %#v", rawExt)
	}
	rawSlots, _ := rawExt["slots"].(map[string]any)
	if rawSlots["memory"] != "memory-core" {
		t.Fatalf("expected memory slot reset on uninstall: %#v", rawSlots)
	}
}

func TestApplyPluginUninstallOperationMissingPlugin(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1}
	_, _, err := ApplyPluginUninstallOperation(cfg, "missing")
	if err == nil {
		t.Fatal("expected missing plugin uninstall error")
	}
	if !strings.Contains(err.Error(), "plugin not found") {
		t.Fatalf("unexpected missing plugin uninstall error: %v", err)
	}
}

func TestApplyPluginUpdateOperation(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1, Extra: map[string]any{"extensions": map[string]any{
		"installs": map[string]any{
			"codegen": map[string]any{"source": "npm", "spec": "@acme/codegen@latest", "version": "1.0.0", "installPath": "./extensions/codegen", "installedAt": "2026-03-01T00:00:00Z"},
			"local":   map[string]any{"source": "path", "sourcePath": "./extensions/local", "installPath": "./extensions/local"},
			"bad":     map[string]any{"source": "npm", "version": "0.0.1"},
		},
	}}}

	runner := func(pluginID string, record map[string]any, dryRun bool) PluginUpdateResult {
		switch pluginID {
		case "codegen":
			if dryRun {
				return PluginUpdateResult{Status: PluginUpdateStatusUpdated, Message: "Would update codegen", NextVersion: "1.1.0"}
			}
			return PluginUpdateResult{Status: PluginUpdateStatusUpdated, Message: "Updated codegen", NextVersion: "1.1.0", InstallPath: "./extensions/codegen"}
		default:
			return PluginUpdateResult{Status: PluginUpdateStatusError, Message: "unexpected target"}
		}
	}

	dryCfg, changed, outcomes := ApplyPluginUpdateOperation(cfg, nil, true, runner)
	if changed {
		t.Fatalf("expected no config changes in dry run")
	}
	if dryCfg.Extra == nil {
		t.Fatalf("expected dry-run config to stay intact")
	}
	if len(outcomes) != 3 {
		t.Fatalf("expected 3 outcomes, got %d: %#v", len(outcomes), outcomes)
	}
	if outcomes[0].PluginID != "bad" || outcomes[0].Status != PluginUpdateStatusSkipped {
		t.Fatalf("expected bad plugin skip (missing spec): %#v", outcomes)
	}
	if outcomes[1].PluginID != "codegen" || outcomes[1].Status != PluginUpdateStatusUpdated {
		t.Fatalf("expected codegen dry-run update outcome: %#v", outcomes)
	}
	if outcomes[2].PluginID != "local" || outcomes[2].Status != PluginUpdateStatusSkipped {
		t.Fatalf("expected local plugin skip (path source): %#v", outcomes)
	}

	next, changed, outcomes := ApplyPluginUpdateOperation(cfg, []string{"codegen"}, false, runner)
	if !changed {
		t.Fatalf("expected config changes for non-dry-run updated plugin")
	}
	if len(outcomes) != 1 || outcomes[0].Status != PluginUpdateStatusUpdated {
		t.Fatalf("expected one updated outcome: %#v", outcomes)
	}
	rawExt, _ := next.Extra["extensions"].(map[string]any)
	rawInstalls, _ := rawExt["installs"].(map[string]any)
	codegen, _ := rawInstalls["codegen"].(map[string]any)
	if codegen["version"] != "1.1.0" {
		t.Fatalf("expected updated install record version: %#v", codegen)
	}
	installedAt, _ := codegen["installedAt"].(string)
	if strings.TrimSpace(installedAt) == "" {
		t.Fatalf("expected installedAt refresh after update persistence: %#v", codegen)
	}
	if installedAt == "2026-03-01T00:00:00Z" {
		t.Fatalf("expected installedAt to change on update persistence: %#v", codegen)
	}
}
