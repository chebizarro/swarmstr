package methods

import (
	"fmt"
	"metiq/internal/store/state"
	"strings"
)

func ApplyConfigSet(cfg state.ConfigDoc, key string, value any) (state.ConfigDoc, error) {
	key = strings.TrimSpace(key)
	switch key {
	case "dm.policy":
		s, ok := value.(string)
		if !ok {
			return cfg, fmt.Errorf("dm.policy must be string")
		}
		cfg.DM.Policy = strings.TrimSpace(s)
	case "dm.reply_scheme":
		s, ok := value.(string)
		if !ok {
			return cfg, fmt.Errorf("dm.reply_scheme must be string")
		}
		mode, valid := state.ParseDMReplyScheme(s)
		if !valid {
			return cfg, fmt.Errorf("dm.reply_scheme must be one of auto, nip17, nip04")
		}
		cfg.DM.ReplyScheme = mode
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
	case "storage.encrypt":
		b, ok := value.(bool)
		if !ok {
			return cfg, fmt.Errorf("storage.encrypt must be bool")
		}
		cfg.Storage.Encrypt = state.BoolPtr(b)
	case "acp.transport":
		s, ok := value.(string)
		if !ok {
			return cfg, fmt.Errorf("acp.transport must be string")
		}
		mode, valid := state.ParseACPTransportMode(s)
		if !valid {
			return cfg, fmt.Errorf("acp.transport must be one of auto, nip17, nip04, fips")
		}
		cfg.ACP.Transport = mode
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
		next, applied, err := applyMCPConfigSet(cfg, key, value)
		if err != nil {
			return cfg, err
		}
		if applied {
			cfg = next
			break
		}
		next, applied, err = applyPluginConfigSet(cfg, key, value)
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
		if strings.HasPrefix(key, "plugins.entries.") && strings.HasSuffix(key, ".env") {
			return applyPluginEnvPatch(cfg, key, child)
		}
		if strings.HasPrefix(key, "mcp.servers.") && (strings.HasSuffix(key, ".env") || strings.HasSuffix(key, ".headers")) {
			return applyMCPStringMapPatch(cfg, key, child)
		}
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

func anyToTrimmedStringList(value any) ([]string, error) {
	raw, ok := value.([]any)
	if !ok {
		if direct, ok := value.([]string); ok {
			out := make([]string, 0, len(direct))
			for _, item := range direct {
				item = strings.TrimSpace(item)
				if item == "" {
					continue
				}
				out = append(out, item)
			}
			return out, nil
		}
		return nil, fmt.Errorf("value must be array")
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("value must be string array")
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func anyToStringMap(value any) (map[string]string, error) {
	raw, ok := value.(map[string]any)
	if !ok {
		if direct, ok := value.(map[string]string); ok {
			out := map[string]string{}
			for key, item := range direct {
				k := strings.TrimSpace(key)
				v := strings.TrimSpace(item)
				if k == "" || v == "" {
					continue
				}
				out[k] = v
			}
			return out, nil
		}
		return nil, fmt.Errorf("value must be object")
	}
	out := map[string]string{}
	for key, item := range raw {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("value must be object<string,string>")
		}
		v := strings.TrimSpace(s)
		if v == "" {
			continue
		}
		out[k] = v
	}
	return out, nil
}

func anyToInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int8:
		return int(v), true
	case int16:
		return int(v), true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		if float64(int(v)) == v {
			return int(v), true
		}
	}
	return 0, false
}

const (
	PluginUpdateStatusUpdated   PluginUpdateStatus = "updated"
	PluginUpdateStatusUnchanged PluginUpdateStatus = "unchanged"
	PluginUpdateStatusSkipped   PluginUpdateStatus = "skipped"
	PluginUpdateStatusError     PluginUpdateStatus = "error"
)
