package methods

import (
	"fmt"
	"sort"
	"strings"

	"swarmstr/internal/store/state"
)

func ConfigSchema(cfg ...state.ConfigDoc) map[string]any {
	schema := map[string]any{
		"fields": []string{
			"dm.policy",
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
			"plugins.loadPaths",
			"plugins.slots.memory",
			"plugins.entries.<id>.enabled",
			"plugins.entries.<id>.tools",
			"plugins.entries.<id>.gatewayMethods",
			"plugins.entries.<id>.config",
		},
	}
	if len(cfg) > 0 {
		schema["plugins"] = extensionSchemaEntries(cfg[0])
	}
	return schema
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
		out = append(out, map[string]any{
			"id":             pluginID,
			"enabled":        getBool(entry, "enabled"),
			"tools":          getStringSlice(entry, "tools"),
			"gatewayMethods": extensionEntryGatewayMethods(entry),
		})
	}
	return map[string]any{"entries": out}
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

func ApplyConfigSet(cfg state.ConfigDoc, key string, value any) (state.ConfigDoc, error) {
	key = strings.TrimSpace(key)
	switch key {
	case "dm.policy":
		s, ok := value.(string)
		if !ok {
			return cfg, fmt.Errorf("dm.policy must be string")
		}
		cfg.DM.Policy = strings.TrimSpace(s)
	case "dm.allow_from":
		items, err := anyToStringSlice(value)
		if err != nil {
			return cfg, fmt.Errorf("dm.allow_from must be string array")
		}
		cfg.DM.AllowFrom = items
	case "relays.read":
		items, err := anyToStringSlice(value)
		if err != nil {
			return cfg, fmt.Errorf("relays.read must be string array")
		}
		cfg.Relays.Read = items
	case "relays.write":
		items, err := anyToStringSlice(value)
		if err != nil {
			return cfg, fmt.Errorf("relays.write must be string array")
		}
		cfg.Relays.Write = items
	case "agent.default_model":
		s, ok := value.(string)
		if !ok {
			return cfg, fmt.Errorf("agent.default_model must be string")
		}
		cfg.Agent.DefaultModel = strings.TrimSpace(s)
	case "agent.thinking":
		s, ok := value.(string)
		if !ok {
			return cfg, fmt.Errorf("agent.thinking must be string")
		}
		cfg.Agent.Thinking = strings.TrimSpace(s)
	case "agent.verbose":
		s, ok := value.(string)
		if !ok {
			return cfg, fmt.Errorf("agent.verbose must be string")
		}
		cfg.Agent.Verbose = strings.TrimSpace(s)
	case "control.require_auth":
		b, ok := value.(bool)
		if !ok {
			return cfg, fmt.Errorf("control.require_auth must be bool")
		}
		cfg.Control.RequireAuth = b
	case "control.allow_unauth_methods":
		items, err := anyToStringSlice(value)
		if err != nil {
			return cfg, fmt.Errorf("control.allow_unauth_methods must be string array")
		}
		cfg.Control.AllowUnauthMethods = items
	case "control.legacy_token_fallback":
		b, ok := value.(bool)
		if !ok {
			return cfg, fmt.Errorf("control.legacy_token_fallback must be bool")
		}
		cfg.Control.LegacyTokenFallback = b
	default:
		next, applied, err := applyPluginConfigSet(cfg, key, value)
		if err != nil {
			return cfg, err
		}
		if !applied {
			return cfg, fmt.Errorf("unsupported config key %q", key)
		}
		cfg = next
	}
	if cfg.Version <= 0 {
		cfg.Version = 1
	}
	return cfg, nil
}

func ApplyConfigPatch(cfg state.ConfigDoc, patch map[string]any) (state.ConfigDoc, error) {
	for key, value := range patch {
		next, err := applyConfigPatchValue(cfg, strings.TrimSpace(key), value)
		if err != nil {
			return cfg, err
		}
		cfg = next
	}
	return cfg, nil
}

func applyConfigPatchValue(cfg state.ConfigDoc, key string, value any) (state.ConfigDoc, error) {
	if key == "" {
		return cfg, fmt.Errorf("invalid patch key")
	}
	if child, ok := value.(map[string]any); ok {
		for nestedKey, nestedValue := range child {
			nextKey := strings.TrimSpace(nestedKey)
			if nextKey == "" {
				return cfg, fmt.Errorf("invalid patch key: empty nested key under %q", key)
			}
			next, err := applyConfigPatchValue(cfg, key+"."+nextKey, nestedValue)
			if err != nil {
				return cfg, err
			}
			cfg = next
		}
		return cfg, nil
	}
	return ApplyConfigSet(cfg, key, value)
}

func anyToStringSlice(value any) ([]string, error) {
	raw, ok := value.([]any)
	if !ok {
		if direct, ok := value.([]string); ok {
			return sanitizeStrings(direct), nil
		}
		return nil, fmt.Errorf("value must be array")
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("value must be string array")
		}
		out = append(out, s)
	}
	return sanitizeStrings(out), nil
}

func applyPluginConfigSet(cfg state.ConfigDoc, key string, value any) (state.ConfigDoc, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return cfg, false, nil
	}
	segments := strings.Split(key, ".")
	if len(segments) == 0 || segments[0] != "plugins" {
		return cfg, false, nil
	}
	if cfg.Extra == nil {
		cfg.Extra = map[string]any{}
	}
	rawExt, _ := cfg.Extra["extensions"].(map[string]any)
	if rawExt == nil {
		rawExt = map[string]any{}
	}
	cfg.Extra["extensions"] = rawExt
	if len(segments) == 2 {
		switch segments[1] {
		case "enabled":
			b, ok := value.(bool)
			if !ok {
				return cfg, true, fmt.Errorf("plugins.enabled must be bool")
			}
			rawExt["enabled"] = b
			return cfg, true, nil
		case "allow":
			items, err := anyToStringSlice(value)
			if err != nil {
				return cfg, true, fmt.Errorf("plugins.allow must be string array")
			}
			rawExt["allow"] = items
			return cfg, true, nil
		case "loadPaths", "load_paths":
			items, err := anyToStringSlice(value)
			if err != nil {
				return cfg, true, fmt.Errorf("plugins.loadPaths must be string array")
			}
			rawExt["load_paths"] = items
			return cfg, true, nil
		}
	}
	if len(segments) == 3 && segments[1] == "slots" && segments[2] == "memory" {
		s, ok := value.(string)
		if !ok {
			return cfg, true, fmt.Errorf("plugins.slots.memory must be string")
		}
		rawSlots, _ := rawExt["slots"].(map[string]any)
		if rawSlots == nil {
			rawSlots = map[string]any{}
			rawExt["slots"] = rawSlots
		}
		rawSlots["memory"] = strings.TrimSpace(s)
		return cfg, true, nil
	}
	if len(segments) < 3 || segments[1] != "entries" {
		return cfg, false, nil
	}
	entryID := strings.TrimSpace(segments[2])
	if entryID == "" {
		return cfg, true, fmt.Errorf("plugins.entries.<id> requires non-empty id")
	}
	rawEntries, _ := rawExt["entries"].(map[string]any)
	if rawEntries == nil {
		rawEntries = map[string]any{}
		rawExt["entries"] = rawEntries
	}
	entry, _ := rawEntries[entryID].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		rawEntries[entryID] = entry
	}
	if len(segments) == 3 {
		entryMap, ok := value.(map[string]any)
		if !ok {
			return cfg, true, fmt.Errorf("plugins.entries.%s must be object", entryID)
		}
		for k, v := range entryMap {
			entry[k] = v
		}
		return cfg, true, nil
	}
	suffix := segments[3]
	switch suffix {
	case "enabled":
		b, ok := value.(bool)
		if !ok {
			return cfg, true, fmt.Errorf("plugins.entries.%s.enabled must be bool", entryID)
		}
		entry["enabled"] = b
	case "tools":
		items, err := anyToStringSlice(value)
		if err != nil {
			return cfg, true, fmt.Errorf("plugins.entries.%s.tools must be string array", entryID)
		}
		entry["tools"] = items
	case "gatewayMethods", "gateway_methods":
		items, err := anyToStringSlice(value)
		if err != nil {
			return cfg, true, fmt.Errorf("plugins.entries.%s.gatewayMethods must be string array", entryID)
		}
		entry["gateway_methods"] = items
	case "config":
		entryMap, ok := value.(map[string]any)
		if !ok {
			return cfg, true, fmt.Errorf("plugins.entries.%s.config must be object", entryID)
		}
		entry["config"] = entryMap
	default:
		return cfg, false, nil
	}
	return cfg, true, nil
}

func sanitizeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
