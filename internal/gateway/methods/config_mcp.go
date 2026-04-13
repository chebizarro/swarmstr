package methods

import (
	"fmt"
	"metiq/internal/store/state"
	"strings"
)

func applyMCPConfigSet(cfg state.ConfigDoc, key string, value any) (state.ConfigDoc, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return cfg, false, nil
	}
	segments := strings.Split(key, ".")
	if len(segments) == 0 || segments[0] != "mcp" {
		return cfg, false, nil
	}
	if cfg.Extra == nil {
		cfg.Extra = map[string]any{}
	}
	rawMCP, _ := cfg.Extra["mcp"].(map[string]any)
	if rawMCP == nil {
		rawMCP = map[string]any{}
		cfg.Extra["mcp"] = rawMCP
	}
	if len(segments) == 2 && segments[1] == "enabled" {
		b, ok := value.(bool)
		if !ok {
			return cfg, true, fmt.Errorf("mcp.enabled must be bool")
		}
		rawMCP["enabled"] = b
		return cleanupMCPConfig(cfg), true, nil
	}
	if len(segments) >= 2 && segments[1] == "policy" {
		if len(segments) == 2 {
			if value == nil {
				delete(rawMCP, "policy")
				return cleanupMCPConfig(cfg), true, nil
			}
			entry, err := normalizeMCPPolicyEntry(value)
			if err != nil {
				return cfg, true, fmt.Errorf("mcp.policy must be object with valid fields: %w", err)
			}
			if len(entry) == 0 {
				delete(rawMCP, "policy")
			} else {
				rawMCP["policy"] = entry
			}
			return cleanupMCPConfig(cfg), true, nil
		}
		if len(segments) != 3 {
			return cfg, true, fmt.Errorf("unsupported config key %q", key)
		}
		rawPolicy, _ := rawMCP["policy"].(map[string]any)
		if rawPolicy == nil {
			rawPolicy = map[string]any{}
			rawMCP["policy"] = rawPolicy
		}
		if err := applyMCPPolicyField(rawPolicy, strings.TrimSpace(segments[2]), value); err != nil {
			return cfg, true, err
		}
		if len(rawPolicy) == 0 {
			delete(rawMCP, "policy")
		}
		return cleanupMCPConfig(cfg), true, nil
	}
	if len(segments) < 2 || segments[1] != "servers" {
		return cfg, false, nil
	}
	if len(segments) == 2 {
		if value == nil {
			delete(rawMCP, "servers")
			return cleanupMCPConfig(cfg), true, nil
		}
		rawServers, ok := value.(map[string]any)
		if !ok {
			return cfg, true, fmt.Errorf("mcp.servers must be object")
		}
		normalized := map[string]any{}
		for serverID, serverValue := range rawServers {
			serverID = strings.TrimSpace(serverID)
			if serverID == "" {
				continue
			}
			entry, err := normalizeMCPServerEntry(serverID, serverValue)
			if err != nil {
				return cfg, true, fmt.Errorf("mcp.servers.%s invalid: %w", serverID, err)
			}
			if len(entry) > 0 {
				normalized[serverID] = entry
			}
		}
		if len(normalized) == 0 {
			delete(rawMCP, "servers")
		} else {
			rawMCP["servers"] = normalized
		}
		return cleanupMCPConfig(cfg), true, nil
	}
	serverID := strings.TrimSpace(segments[2])
	if serverID == "" {
		return cfg, true, fmt.Errorf("mcp.servers.<id> requires non-empty id")
	}
	rawServers, _ := rawMCP["servers"].(map[string]any)
	if rawServers == nil {
		rawServers = map[string]any{}
		rawMCP["servers"] = rawServers
	}
	if len(segments) == 3 {
		if value == nil {
			delete(rawServers, serverID)
			return cleanupMCPConfig(cfg), true, nil
		}
		entry, err := normalizeMCPServerEntry(serverID, value)
		if err != nil {
			return cfg, true, fmt.Errorf("mcp.servers.%s must be object with valid fields: %w", serverID, err)
		}
		if len(entry) == 0 {
			delete(rawServers, serverID)
		} else {
			rawServers[serverID] = entry
		}
		return cleanupMCPConfig(cfg), true, nil
	}
	if len(segments) == 5 && strings.TrimSpace(segments[3]) == "oauth" {
		entry, _ := rawServers[serverID].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
			rawServers[serverID] = entry
		}
		if err := applyMCPOAuthField(entry, serverID, strings.TrimSpace(segments[4]), value); err != nil {
			return cfg, true, err
		}
		if len(entry) == 0 {
			delete(rawServers, serverID)
		}
		return cleanupMCPConfig(cfg), true, nil
	}
	if len(segments) != 4 {
		return cfg, true, fmt.Errorf("unsupported config key %q", key)
	}
	entry, _ := rawServers[serverID].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		rawServers[serverID] = entry
	}
	if err := applyMCPServerField(entry, serverID, strings.TrimSpace(segments[3]), value); err != nil {
		return cfg, true, err
	}
	if len(entry) == 0 {
		delete(rawServers, serverID)
	}
	return cleanupMCPConfig(cfg), true, nil
}

func normalizeMCPServerEntry(serverID string, value any) (map[string]any, error) {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value must be object")
	}
	entry := map[string]any{}
	for field, fieldValue := range raw {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if err := applyMCPServerField(entry, serverID, field, fieldValue); err != nil {
			return nil, err
		}
	}
	return entry, nil
}

func normalizeMCPPolicyEntry(value any) (map[string]any, error) {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value must be object")
	}
	entry := map[string]any{}
	for field, fieldValue := range raw {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if err := applyMCPPolicyField(entry, field, fieldValue); err != nil {
			return nil, err
		}
	}
	return entry, nil
}

func applyMCPPolicyField(entry map[string]any, field string, value any) error {
	switch field {
	case "allowed", "denied":
		if value == nil {
			delete(entry, field)
			return nil
		}
		matchers, err := normalizeMCPPolicyMatchers(field, value)
		if err != nil {
			return err
		}
		entry[field] = matchers
	case "require_remote_approval":
		b, ok := value.(bool)
		if !ok {
			return fmt.Errorf("mcp.policy.require_remote_approval must be bool")
		}
		entry["require_remote_approval"] = b
	case "approved_servers":
		items, err := anyToTrimmedStringList(value)
		if err != nil {
			return fmt.Errorf("mcp.policy.approved_servers must be string array")
		}
		entry["approved_servers"] = items
	default:
		return fmt.Errorf("unsupported config key %q", "mcp.policy."+field)
	}
	return nil
}

func normalizeMCPPolicyMatchers(field string, value any) ([]map[string]any, error) {
	raw, ok := value.([]any)
	if !ok {
		if direct, ok := value.([]map[string]any); ok {
			raw = make([]any, 0, len(direct))
			for _, item := range direct {
				raw = append(raw, item)
			}
		} else {
			return nil, fmt.Errorf("mcp.policy.%s must be array<object>", field)
		}
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		matcher, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mcp.policy.%s must be array<object>", field)
		}
		entry := map[string]any{}
		if name, ok := matcher["name"].(string); ok {
			name = strings.TrimSpace(name)
			if name != "" {
				entry["name"] = name
			}
		}
		if command, exists := matcher["command"]; exists {
			items, err := anyToTrimmedStringList(command)
			if err != nil {
				return nil, fmt.Errorf("mcp.policy.%s.command must be string array", field)
			}
			if len(items) > 0 {
				entry["command"] = items
			}
		}
		if url, ok := matcher["url"].(string); ok {
			url = strings.TrimSpace(url)
			if url != "" {
				entry["url"] = url
			}
		}
		if len(entry) == 0 {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func applyMCPServerField(entry map[string]any, serverID, field string, value any) error {
	switch field {
	case "enabled":
		b, ok := value.(bool)
		if !ok {
			return fmt.Errorf("mcp.servers.%s.enabled must be bool", serverID)
		}
		entry["enabled"] = b
	case "command", "url":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("mcp.servers.%s.%s must be string", serverID, field)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			delete(entry, field)
		} else {
			entry[field] = s
		}
	case "type":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("mcp.servers.%s.type must be string", serverID)
		}
		s = strings.ToLower(strings.TrimSpace(s))
		switch s {
		case "":
			delete(entry, "type")
		case "stdio", "sse", "http":
			entry["type"] = s
		default:
			return fmt.Errorf("mcp.servers.%s.type must be one of stdio, sse, http", serverID)
		}
	case "args":
		items, err := anyToTrimmedStringList(value)
		if err != nil {
			return fmt.Errorf("mcp.servers.%s.args must be string array", serverID)
		}
		if len(items) == 0 {
			delete(entry, "args")
		} else {
			entry["args"] = items
		}
	case "env", "headers":
		items, err := anyToStringMap(value)
		if err != nil {
			return fmt.Errorf("mcp.servers.%s.%s must be object<string,string>", serverID, field)
		}
		if len(items) == 0 {
			delete(entry, field)
		} else {
			entry[field] = items
		}
	case "oauth":
		if value == nil {
			delete(entry, "oauth")
			return nil
		}
		oauthEntry, err := normalizeMCPOAuthEntry(serverID, value)
		if err != nil {
			return err
		}
		if len(oauthEntry) == 0 {
			delete(entry, "oauth")
		} else {
			entry["oauth"] = oauthEntry
		}
	default:
		return fmt.Errorf("unsupported config key %q", "mcp.servers."+serverID+"."+field)
	}
	return nil
}

func normalizeMCPOAuthEntry(serverID string, value any) (map[string]any, error) {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcp.servers.%s.oauth must be object", serverID)
	}
	entry := map[string]any{}
	for field, fieldValue := range raw {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if err := applyMCPOAuthField(map[string]any{"oauth": entry}, serverID, field, fieldValue); err != nil {
			return nil, err
		}
	}
	return entry, nil
}

func applyMCPOAuthField(entry map[string]any, serverID, field string, value any) error {
	oauthEntry, _ := entry["oauth"].(map[string]any)
	if oauthEntry == nil {
		oauthEntry = map[string]any{}
	}
	switch field {
	case "enabled", "use_pkce":
		b, ok := value.(bool)
		if !ok {
			return fmt.Errorf("mcp.servers.%s.oauth.%s must be bool", serverID, field)
		}
		oauthEntry[field] = b
	case "client_id", "client_secret_ref", "authorize_url", "token_url", "revoke_url", "callback_host":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("mcp.servers.%s.oauth.%s must be string", serverID, field)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			delete(oauthEntry, field)
		} else {
			oauthEntry[field] = s
		}
	case "callback_port":
		n, ok := anyToInt(value)
		if !ok || n < 0 {
			return fmt.Errorf("mcp.servers.%s.oauth.callback_port must be integer >= 0", serverID)
		}
		if n == 0 {
			delete(oauthEntry, "callback_port")
		} else {
			oauthEntry["callback_port"] = n
		}
	case "scopes":
		items, err := anyToTrimmedStringList(value)
		if err != nil {
			return fmt.Errorf("mcp.servers.%s.oauth.scopes must be string array", serverID)
		}
		if len(items) == 0 {
			delete(oauthEntry, "scopes")
		} else {
			oauthEntry["scopes"] = items
		}
	default:
		return fmt.Errorf("unsupported config key %q", "mcp.servers."+serverID+".oauth."+field)
	}
	if len(oauthEntry) == 0 {
		delete(entry, "oauth")
		return nil
	}
	entry["oauth"] = oauthEntry
	return nil
}

func applyMCPStringMapPatch(cfg state.ConfigDoc, key string, patch map[string]any) (state.ConfigDoc, error) {
	segments := strings.Split(strings.TrimSpace(key), ".")
	if len(segments) != 4 || segments[0] != "mcp" || segments[1] != "servers" || (segments[3] != "env" && segments[3] != "headers") {
		return cfg, fmt.Errorf("invalid MCP map patch key %q", key)
	}
	serverID := strings.TrimSpace(segments[2])
	if serverID == "" {
		return cfg, fmt.Errorf("mcp.servers.<id>.%s requires non-empty id", segments[3])
	}
	merged := map[string]string{}
	if cfg.Extra != nil {
		if rawMCP, ok := cfg.Extra["mcp"].(map[string]any); ok {
			if rawServers, ok := rawMCP["servers"].(map[string]any); ok {
				if entry, ok := rawServers[serverID].(map[string]any); ok {
					switch existing := entry[segments[3]].(type) {
					case map[string]string:
						for existingKey, existingVal := range existing {
							existingKey = strings.TrimSpace(existingKey)
							existingVal = strings.TrimSpace(existingVal)
							if existingKey == "" || existingVal == "" {
								continue
							}
							merged[existingKey] = existingVal
						}
					case map[string]any:
						for existingKey, raw := range existing {
							existingVal, ok := raw.(string)
							if !ok {
								continue
							}
							existingKey = strings.TrimSpace(existingKey)
							existingVal = strings.TrimSpace(existingVal)
							if existingKey == "" || existingVal == "" {
								continue
							}
							merged[existingKey] = existingVal
						}
					}
				}
			}
		}
	}
	for patchKey, raw := range patch {
		patchVal, ok := raw.(string)
		if !ok {
			return cfg, fmt.Errorf("mcp.servers.%s.%s must be object<string,string>", serverID, segments[3])
		}
		patchKey = strings.TrimSpace(patchKey)
		if patchKey == "" {
			continue
		}
		patchVal = strings.TrimSpace(patchVal)
		if patchVal == "" {
			delete(merged, patchKey)
			continue
		}
		merged[patchKey] = patchVal
	}
	return ApplyConfigSet(cfg, key, merged)
}

func cleanupMCPConfig(cfg state.ConfigDoc) state.ConfigDoc {
	if cfg.Extra == nil {
		return cfg
	}
	rawMCP, _ := cfg.Extra["mcp"].(map[string]any)
	if rawMCP == nil {
		return cfg
	}
	if rawServers, ok := rawMCP["servers"].(map[string]any); ok && len(rawServers) == 0 {
		delete(rawMCP, "servers")
	}
	if rawPolicy, ok := rawMCP["policy"].(map[string]any); ok && len(rawPolicy) == 0 {
		delete(rawMCP, "policy")
	}
	if len(rawMCP) == 0 {
		delete(cfg.Extra, "mcp")
	}
	if len(cfg.Extra) == 0 {
		cfg.Extra = nil
	}
	return cfg
}
