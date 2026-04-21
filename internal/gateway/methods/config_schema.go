package methods

import (
	"sort"
	"strings"

	mcppkg "metiq/internal/mcp"
	"metiq/internal/store/state"
)

func ConfigSchema(cfg ...state.ConfigDoc) map[string]any {
	schema := map[string]any{
		"fields": []string{
			"dm.policy",
			"dm.reply_scheme",
			"dm.allow_from",
			"relays.read",
			"relays.write",
			"agent.default_model",
			"agent.thinking",
			"agent.verbose",
			"control.require_auth",
			"control.allow_unauth_methods",
			"control.legacy_token_fallback",
			"plugins.enabled",
			"plugins.allow",
			"plugins.deny",
			"plugins.load",
			"plugins.load.paths",
			"plugins.loadPaths",
			"plugins.slots.memory",
			"plugins.entries.<id>.enabled",
			"plugins.entries.<id>.apiKey",
			"plugins.entries.<id>.env",
			"plugins.entries.<id>.tools",
			"plugins.entries.<id>.gatewayMethods",
			"plugins.entries.<id>.config",
			"plugins.installs",
			"plugins.installs.<id>",
			"plugins.installs.<id>.source",
			"plugins.installs.<id>.spec",
			"plugins.installs.<id>.sourcePath",
			"plugins.installs.<id>.installPath",
			"plugins.installs.<id>.version",
			"plugins.installs.<id>.resolvedName",
			"plugins.installs.<id>.resolvedVersion",
			"plugins.installs.<id>.resolvedSpec",
			"plugins.installs.<id>.integrity",
			"plugins.installs.<id>.shasum",
			"plugins.installs.<id>.resolvedAt",
			"plugins.installs.<id>.installedAt",
			"plugins.installs.<id>.<field>",
			"mcp.enabled",
			"mcp.servers",
			"mcp.servers.<id>",
			"mcp.servers.<id>.enabled",
			"mcp.servers.<id>.command",
			"mcp.servers.<id>.args",
			"mcp.servers.<id>.env",
			"mcp.servers.<id>.type",
			"mcp.servers.<id>.url",
			"mcp.servers.<id>.headers",
			"mcp.servers.<id>.oauth",
			"mcp.servers.<id>.oauth.enabled",
			"mcp.servers.<id>.oauth.client_id",
			"mcp.servers.<id>.oauth.client_secret_ref",
			"mcp.servers.<id>.oauth.authorize_url",
			"mcp.servers.<id>.oauth.token_url",
			"mcp.servers.<id>.oauth.revoke_url",
			"mcp.servers.<id>.oauth.scopes",
			"mcp.servers.<id>.oauth.callback_host",
			"mcp.servers.<id>.oauth.callback_port",
			"mcp.servers.<id>.oauth.use_pkce",
			"mcp.policy",
			"mcp.policy.allowed",
			"mcp.policy.denied",
			"mcp.policy.require_remote_approval",
			"mcp.policy.approved_servers",
			// Typed agent section (multi-agent support)
			"agents[].id",
			"agents[].name",
			"agents[].model",
			"agents[].workspace_dir",
			"agents[].tool_profile",
			"agents[].history_limit",
			// Nostr channel configuration
			"nostr_channels.<name>.kind",
			"nostr_channels.<name>.enabled",
			"nostr_channels.<name>.group_address",
			"nostr_channels.<name>.channel_id",
			"nostr_channels.<name>.relays",
			"nostr_channels.<name>.agent_id",
			"nostr_channels.<name>.tags",
			"nostr_channels.<name>.config.auto_review.enabled",
			"nostr_channels.<name>.config.auto_review.agent_id",
			"nostr_channels.<name>.config.auto_review.tool_profile",
			"nostr_channels.<name>.config.auto_review.enabled_tools",
			"nostr_channels.<name>.config.auto_review.trigger_types",
			"nostr_channels.<name>.config.auto_review.followed_only",
			"nostr_channels.<name>.config.auto_review.instructions",
			// Provider overrides
			"providers.<name>.api_key",
			"providers.<name>.base_url",
			"providers.<name>.model",
			// Session tunables
			"session.ttl_seconds",
			"session.history_limit",
			"session.max_tokens",
			"session.temperature",
			"storage.encrypt",
			"acp.transport",
			// Heartbeat
			"heartbeat.interval_ms",
			"heartbeat.enabled",
			// TTS
			"tts.provider",
			"tts.voice",
			"tts.enabled",
			// Secrets
			"secrets.<name>",
			// Cron
			"cron.enabled",
			"cron.jobs.<id>",
			"cron.jobs.<id>.schedule",
			"cron.jobs.<id>.command",
			"cron.jobs.<id>.enabled",
		},
	}
	if len(cfg) > 0 {
		schema["plugins"] = extensionSchemaEntries(cfg[0])
		schema["mcp"] = mcpSchemaEntries(cfg[0])
		schema["agents"] = agentSchemaEntries(cfg[0])
		schema["nostr_channels"] = nostrChannelSchemaEntries(cfg[0])
	}
	return schema
}

// agentSchemaEntries returns a live summary of configured agents for the schema response.
func agentSchemaEntries(cfg state.ConfigDoc) []map[string]any {
	out := make([]map[string]any, 0, len(cfg.Agents))
	for _, ag := range cfg.Agents {
		out = append(out, map[string]any{
			"id":           ag.ID,
			"name":         ag.Name,
			"model":        ag.Model,
			"tool_profile": ag.ToolProfile,
		})
	}
	return out
}

// nostrChannelSchemaEntries returns a live summary of configured Nostr channels for the schema response.
func nostrChannelSchemaEntries(cfg state.ConfigDoc) []map[string]any {
	out := make([]map[string]any, 0, len(cfg.NostrChannels))
	for name, ch := range cfg.NostrChannels {
		out = append(out, map[string]any{
			"name":    name,
			"kind":    ch.Kind,
			"enabled": ch.Enabled,
		})
	}
	return out
}

func extensionSchemaEntries(cfg state.ConfigDoc) map[string]any {
	entries := extensionEntries(cfg)
	pluginIDs := make([]string, 0, len(entries))
	for pluginID := range entries {
		pluginIDs = append(pluginIDs, pluginID)
	}
	sort.Strings(pluginIDs)
	out := make([]map[string]any, 0, len(pluginIDs))
	for _, pluginID := range pluginIDs {
		entry := entries[pluginID]
		envMap, _ := getStringMap(entry, "env")
		envKeys := make([]string, 0, len(envMap))
		for key := range envMap {
			envKeys = append(envKeys, key)
		}
		sort.Strings(envKeys)
		apiKey, _ := entry["api_key"].(string)
		out = append(out, map[string]any{
			"id":             pluginID,
			"enabled":        getBool(entry, "enabled"),
			"hasApiKey":      strings.TrimSpace(apiKey) != "",
			"env":            envKeys,
			"tools":          getStringSlice(entry, "tools"),
			"gatewayMethods": extensionEntryGatewayMethods(entry),
		})
	}
	rawExt := map[string]any{}
	if cfg.Extra != nil {
		if ext, ok := cfg.Extra["extensions"].(map[string]any); ok {
			rawExt = ext
		}
	}
	enabled := true
	if v, ok := rawExt["enabled"].(bool); ok {
		enabled = v
	}
	load := true
	if v, ok := rawExt["load"].(bool); ok {
		load = v
	}
	loadPaths := getStringSlice(rawExt, "load_paths")
	if len(loadPaths) == 0 {
		loadPaths = getStringSlice(rawExt, "loadPaths")
	}
	installIDs := []string{}
	if rawInstalls, ok := rawExt["installs"].(map[string]any); ok {
		for installID, installData := range rawInstalls {
			installID = strings.TrimSpace(installID)
			if installID == "" {
				continue
			}
			if _, isMap := installData.(map[string]any); !isMap {
				continue
			}
			installIDs = append(installIDs, installID)
		}
		sort.Strings(installIDs)
	}
	return map[string]any{
		"enabled":   enabled,
		"load":      load,
		"allow":     getStringSlice(rawExt, "allow"),
		"deny":      getStringSlice(rawExt, "deny"),
		"loadPaths": loadPaths,
		"installs":  installIDs,
		"entries":   out,
	}
}

func mcpSchemaEntries(cfg state.ConfigDoc) map[string]any {
	resolved := mcppkg.ResolveConfigDoc(cfg)
	servers := make([]map[string]any, 0, len(resolved.Servers)+len(resolved.DisabledServers))
	filtered := make([]map[string]any, 0, len(resolved.FilteredServers))
	names := make([]string, 0, len(resolved.Servers)+len(resolved.DisabledServers))
	for name := range resolved.Servers {
		names = append(names, name)
	}
	for name := range resolved.DisabledServers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		server, ok := resolved.Servers[name]
		if !ok {
			server = resolved.DisabledServers[name]
		}
		servers = append(servers, map[string]any{
			"name":       server.Name,
			"enabled":    server.Enabled,
			"source":     server.Source,
			"precedence": server.Precedence,
			"signature":  server.Signature,
			"type":       server.Type,
			"command":    server.Command,
			"url":        server.URL,
			"oauth":      server.OAuth != nil,
		})
	}
	filteredNames := make([]string, 0, len(resolved.FilteredServers))
	for name := range resolved.FilteredServers {
		filteredNames = append(filteredNames, name)
	}
	sort.Strings(filteredNames)
	for _, name := range filteredNames {
		server := resolved.FilteredServers[name]
		filtered = append(filtered, map[string]any{
			"name":          server.Name,
			"enabled":       server.Enabled,
			"source":        server.Source,
			"precedence":    server.Precedence,
			"signature":     server.Signature,
			"type":          server.Type,
			"command":       server.Command,
			"url":           server.URL,
			"oauth":         server.OAuth != nil,
			"policy_status": server.PolicyStatus,
			"policy_reason": server.PolicyReason,
		})
	}
	suppressed := make([]map[string]any, 0, len(resolved.Suppressed))
	for _, server := range resolved.Suppressed {
		suppressed = append(suppressed, map[string]any{
			"name":        server.Name,
			"source":      server.Source,
			"precedence":  server.Precedence,
			"signature":   server.Signature,
			"duplicateOf": server.DuplicateOf,
			"reason":      server.Reason,
		})
	}
	return map[string]any{
		"enabled":    resolved.Enabled,
		"servers":    servers,
		"filtered":   filtered,
		"suppressed": suppressed,
		"policy": map[string]any{
			"allowed":                 policyMatcherMaps(resolved.Policy.Allowed),
			"allowed_defined":         resolved.Policy.AllowedDefined,
			"denied":                  policyMatcherMaps(resolved.Policy.Denied),
			"require_remote_approval": resolved.Policy.RequireRemoteApproval,
			"approved_servers":        append([]string(nil), resolved.Policy.ApprovedServers...),
		},
	}
}

func policyMatcherMaps(matchers []mcppkg.PolicyMatcher) []map[string]any {
	if len(matchers) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(matchers))
	for _, matcher := range matchers {
		entry := map[string]any{}
		if matcher.Name != "" {
			entry["name"] = matcher.Name
		}
		if len(matcher.Command) > 0 {
			entry["command"] = append([]string(nil), matcher.Command...)
		}
		if matcher.URL != "" {
			entry["url"] = matcher.URL
		}
		if len(entry) > 0 {
			out = append(out, entry)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func extensionEntries(cfg state.ConfigDoc) map[string]map[string]any {
	out := map[string]map[string]any{}
	if cfg.Extra == nil {
		return out
	}
	rawExt, ok := cfg.Extra["extensions"].(map[string]any)
	if !ok {
		return out
	}
	rawEntries, ok := rawExt["entries"].(map[string]any)
	if !ok {
		return out
	}
	for key, value := range rawEntries {
		entryMap, ok := value.(map[string]any)
		if !ok {
			continue
		}
		copyEntry := map[string]any{}
		for ek, ev := range entryMap {
			copyEntry[ek] = ev
		}
		out[key] = copyEntry
	}
	return out
}

func extensionEntryGatewayMethods(entry map[string]any) []string {
	methods := getStringSlice(entry, "gatewayMethods")
	if len(methods) == 0 {
		methods = getStringSlice(entry, "gateway_methods")
	}
	return methods
}

func getBool(in map[string]any, key string) bool {
	v, ok := in[key].(bool)
	return ok && v
}

func getStringSlice(in map[string]any, key string) []string {
	raw, ok := in[key]
	if !ok {
		return []string{}
	}
	items, err := anyToStringSlice(raw)
	if err != nil {
		return []string{}
	}
	return items
}

func getStringMap(in map[string]any, key string) (map[string]string, bool) {
	raw, ok := in[key]
	if !ok {
		return nil, false
	}
	items, err := anyToStringMap(raw)
	if err != nil {
		return nil, false
	}
	return items, true
}
