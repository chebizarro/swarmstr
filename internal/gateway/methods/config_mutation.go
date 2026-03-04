package methods

import (
	"fmt"
	"strings"

	"swarmstr/internal/store/state"
)

func ConfigSchema() map[string]any {
	return map[string]any{
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
		},
	}
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
		return cfg, fmt.Errorf("unsupported config key %q", key)
	}
	if cfg.Version <= 0 {
		cfg.Version = 1
	}
	return cfg, nil
}

func ApplyConfigPatch(cfg state.ConfigDoc, patch map[string]any) (state.ConfigDoc, error) {
	for key, value := range patch {
		if child, ok := value.(map[string]any); ok {
			for nestedKey, nestedValue := range child {
				var err error
				cfg, err = ApplyConfigSet(cfg, key+"."+nestedKey, nestedValue)
				if err != nil {
					return cfg, err
				}
			}
			continue
		}
		var err error
		cfg, err = ApplyConfigSet(cfg, key, value)
		if err != nil {
			return cfg, err
		}
	}
	return cfg, nil
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
