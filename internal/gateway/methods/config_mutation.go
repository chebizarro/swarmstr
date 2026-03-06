package methods

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"swarmstr/internal/store/state"
)

var ErrPluginNotFound = errors.New("plugin not found")

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
		if strings.HasPrefix(key, "plugins.entries.") && strings.HasSuffix(key, ".env") {
			return applyPluginEnvPatch(cfg, key, child)
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

func applyPluginEnvPatch(cfg state.ConfigDoc, key string, patch map[string]any) (state.ConfigDoc, error) {
	segments := strings.Split(strings.TrimSpace(key), ".")
	if len(segments) != 4 || segments[0] != "plugins" || segments[1] != "entries" || segments[3] != "env" {
		return cfg, fmt.Errorf("invalid plugin env patch key %q", key)
	}
	entryID := strings.TrimSpace(segments[2])
	if entryID == "" {
		return cfg, fmt.Errorf("plugins.entries.<id>.env requires non-empty id")
	}
	merged := map[string]string{}
	if cfg.Extra != nil {
		if rawExt, ok := cfg.Extra["extensions"].(map[string]any); ok {
			if rawEntries, ok := rawExt["entries"].(map[string]any); ok {
				if entry, ok := rawEntries[entryID].(map[string]any); ok {
					switch existing := entry["env"].(type) {
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
			return cfg, fmt.Errorf("plugins.entries.%s.env must be object<string,string>", entryID)
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

func canonicalInstallField(field string) (string, bool) {
	switch strings.TrimSpace(field) {
	case "source":
		return "source", true
	case "spec":
		return "spec", true
	case "sourcePath", "source_path":
		return "sourcePath", true
	case "installPath", "install_path":
		return "installPath", true
	case "version":
		return "version", true
	case "resolvedName", "resolved_name":
		return "resolvedName", true
	case "resolvedVersion", "resolved_version":
		return "resolvedVersion", true
	case "resolvedSpec", "resolved_spec":
		return "resolvedSpec", true
	case "integrity":
		return "integrity", true
	case "shasum":
		return "shasum", true
	case "resolvedAt", "resolved_at":
		return "resolvedAt", true
	case "installedAt", "installed_at":
		return "installedAt", true
	default:
		return "", false
	}
}

func applyInstallRecordField(entry map[string]any, field string, value any) error {
	field = strings.TrimSpace(field)
	canonical, known := canonicalInstallField(field)
	if !known {
		if value == nil {
			delete(entry, field)
		} else {
			entry[field] = value
		}
		return nil
	}
	if field != canonical {
		delete(entry, field)
	}
	if value == nil {
		delete(entry, canonical)
		return nil
	}
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("plugins.installs field %q must be string", canonical)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		delete(entry, canonical)
		return nil
	}
	if canonical == "source" {
		s = strings.ToLower(s)
		switch s {
		case "npm", "archive", "path":
		default:
			return fmt.Errorf("plugins.installs field %q must be one of npm, archive, path", canonical)
		}
	}
	entry[canonical] = s
	return nil
}

func normalizeInstallRecord(value any) (map[string]any, error) {
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
		if err := applyInstallRecordField(entry, field, fieldValue); err != nil {
			return nil, err
		}
	}
	if _, ok := entry["source"]; !ok {
		return nil, fmt.Errorf("plugins.installs.<id>.source is required")
	}
	if _, ok := entry["installedAt"]; !ok {
		entry["installedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return entry, nil
}

func updateInstallRecordField(entry map[string]any, field string, value any) error {
	if err := applyInstallRecordField(entry, field, value); err != nil {
		return err
	}
	canonical, known := canonicalInstallField(field)
	if known && canonical == "installedAt" {
		return nil
	}
	// Guarantee the timestamp strictly advances even on sub-microsecond fast machines
	// (two consecutive time.Now() calls can return the same nanosecond value).
	prev, _ := entry["installedAt"].(string)
	next := time.Now().UTC().Format(time.RFC3339Nano)
	if next == prev {
		if t, err := time.Parse(time.RFC3339Nano, prev); err == nil {
			next = t.Add(time.Microsecond).UTC().Format(time.RFC3339Nano)
		}
	}
	entry["installedAt"] = next
	return nil
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
		case "deny":
			items, err := anyToStringSlice(value)
			if err != nil {
				return cfg, true, fmt.Errorf("plugins.deny must be string array")
			}
			rawExt["deny"] = items
			return cfg, true, nil
		case "load":
			b, ok := value.(bool)
			if !ok {
				return cfg, true, fmt.Errorf("plugins.load must be bool")
			}
			rawExt["load"] = b
			return cfg, true, nil
		case "loadPaths", "load_paths":
			items, err := anyToStringSlice(value)
			if err != nil {
				return cfg, true, fmt.Errorf("plugins.loadPaths must be string array")
			}
			rawExt["load_paths"] = items
			return cfg, true, nil
		case "installs":
			if value == nil {
				delete(rawExt, "installs")
				return cfg, true, nil
			}
			entryMap, ok := value.(map[string]any)
			if !ok {
				return cfg, true, fmt.Errorf("plugins.installs must be object")
			}
			normalized := map[string]any{}
			for installID, installValue := range entryMap {
				installID = strings.TrimSpace(installID)
				if installID == "" {
					continue
				}
				record, err := normalizeInstallRecord(installValue)
				if err != nil {
					return cfg, true, fmt.Errorf("plugins.installs.%s must be object with valid fields: %w", installID, err)
				}
				normalized[installID] = record
			}
			rawExt["installs"] = normalized
			if len(normalized) == 0 {
				delete(rawExt, "installs")
			}
			return cfg, true, nil
		}
	}
	if len(segments) == 3 && segments[1] == "load" && segments[2] == "paths" {
		items, err := anyToStringSlice(value)
		if err != nil {
			return cfg, true, fmt.Errorf("plugins.load.paths must be string array")
		}
		rawExt["load_paths"] = items
		return cfg, true, nil
	}
	if len(segments) >= 3 && segments[1] == "installs" {
		installID := strings.TrimSpace(segments[2])
		if installID == "" {
			return cfg, true, fmt.Errorf("plugins.installs.<id> requires non-empty id")
		}
		rawInstalls, _ := rawExt["installs"].(map[string]any)
		if rawInstalls == nil {
			rawInstalls = map[string]any{}
			rawExt["installs"] = rawInstalls
		}
		if len(segments) == 3 {
			if value == nil {
				delete(rawInstalls, installID)
				if len(rawInstalls) == 0 {
					delete(rawExt, "installs")
				}
				return cfg, true, nil
			}
			record, err := normalizeInstallRecord(value)
			if err != nil {
				return cfg, true, fmt.Errorf("plugins.installs.%s must be object with valid fields: %w", installID, err)
			}
			rawInstalls[installID] = record
			return cfg, true, nil
		}
		if len(segments) != 4 {
			return cfg, true, fmt.Errorf("unsupported config key %q", key)
		}
		field := strings.TrimSpace(segments[3])
		if field == "" {
			return cfg, true, fmt.Errorf("plugins.installs.%s.<field> requires non-empty field", installID)
		}
		entry, _ := rawInstalls[installID].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
			rawInstalls[installID] = entry
		}
		if err := updateInstallRecordField(entry, field, value); err != nil {
			return cfg, true, fmt.Errorf("plugins.installs.%s.%s invalid: %w", installID, field, err)
		}
		if _, hasSource := entry["source"]; !hasSource {
			delete(rawInstalls, installID)
		} else if len(entry) == 0 {
			delete(rawInstalls, installID)
		}
		if len(rawInstalls) == 0 {
			delete(rawExt, "installs")
		}
		return cfg, true, nil
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
	case "apiKey", "api_key":
		s, ok := value.(string)
		if !ok {
			return cfg, true, fmt.Errorf("plugins.entries.%s.apiKey must be string", entryID)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			delete(entry, "api_key")
		} else {
			entry["api_key"] = s
		}
	case "env":
		items, err := anyToStringMap(value)
		if err != nil {
			return cfg, true, fmt.Errorf("plugins.entries.%s.env must be object<string,string>", entryID)
		}
		if len(items) == 0 {
			delete(entry, "env")
		} else {
			entry["env"] = items
		}
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

type PluginUninstallActions struct {
	Entry      bool `json:"entry"`
	Install    bool `json:"install"`
	Allowlist  bool `json:"allowlist"`
	LoadPath   bool `json:"loadPath"`
	MemorySlot bool `json:"memorySlot"`
}

type PluginUpdateStatus string

const (
	PluginUpdateStatusUpdated   PluginUpdateStatus = "updated"
	PluginUpdateStatusUnchanged PluginUpdateStatus = "unchanged"
	PluginUpdateStatusSkipped   PluginUpdateStatus = "skipped"
	PluginUpdateStatusError     PluginUpdateStatus = "error"
)

type PluginUpdateOutcome struct {
	PluginID       string             `json:"pluginId"`
	Status         PluginUpdateStatus `json:"status"`
	Message        string             `json:"message"`
	CurrentVersion string             `json:"currentVersion,omitempty"`
	NextVersion    string             `json:"nextVersion,omitempty"`
}

type PluginUpdateResult struct {
	Status      PluginUpdateStatus
	Message     string
	NextVersion string
	InstallPath string
	RecordPatch map[string]any
}

type PluginUpdateRunner func(pluginID string, record map[string]any, dryRun bool) PluginUpdateResult

func ApplyPluginUpdateOperation(cfg state.ConfigDoc, pluginIDs []string, dryRun bool, runner PluginUpdateRunner) (state.ConfigDoc, bool, []PluginUpdateOutcome) {
	outcomes := []PluginUpdateOutcome{}
	if runner == nil {
		return cfg, false, append(outcomes, PluginUpdateOutcome{PluginID: "", Status: PluginUpdateStatusError, Message: "plugin update runner is required"})
	}
	rawExt, _ := cfg.Extra["extensions"].(map[string]any)
	rawInstalls, _ := rawExt["installs"].(map[string]any)
	targets := sanitizeStrings(pluginIDs)
	if len(targets) == 0 {
		targets = make([]string, 0, len(rawInstalls))
		for pluginID := range rawInstalls {
			targets = append(targets, strings.TrimSpace(pluginID))
		}
		sort.Strings(targets)
	}
	changed := false
	nextCfg := cfg
	for _, pluginID := range targets {
		pluginID = strings.TrimSpace(pluginID)
		if pluginID == "" {
			continue
		}
		record, _ := rawInstalls[pluginID].(map[string]any)
		if record == nil {
			outcomes = append(outcomes, PluginUpdateOutcome{PluginID: pluginID, Status: PluginUpdateStatusSkipped, Message: fmt.Sprintf("No install record for %q.", pluginID)})
			continue
		}
		source, _ := record["source"].(string)
		if source != "npm" {
			outcomes = append(outcomes, PluginUpdateOutcome{PluginID: pluginID, Status: PluginUpdateStatusSkipped, Message: fmt.Sprintf("Skipping %q (source: %s).", pluginID, source)})
			continue
		}
		spec, _ := record["spec"].(string)
		spec = strings.TrimSpace(spec)
		if spec == "" {
			outcomes = append(outcomes, PluginUpdateOutcome{PluginID: pluginID, Status: PluginUpdateStatusSkipped, Message: fmt.Sprintf("Skipping %q (missing npm spec).", pluginID)})
			continue
		}
		currentVersion, _ := record["version"].(string)
		res := runner(pluginID, record, dryRun)
		status := res.Status
		if status == "" {
			status = PluginUpdateStatusError
		}
		outcome := PluginUpdateOutcome{
			PluginID:       pluginID,
			Status:         status,
			Message:        strings.TrimSpace(res.Message),
			CurrentVersion: strings.TrimSpace(currentVersion),
			NextVersion:    strings.TrimSpace(res.NextVersion),
		}
		if outcome.Message == "" {
			switch outcome.Status {
			case PluginUpdateStatusUpdated:
				outcome.Message = fmt.Sprintf("Updated %s.", pluginID)
			case PluginUpdateStatusUnchanged:
				outcome.Message = fmt.Sprintf("%s is up to date.", pluginID)
			case PluginUpdateStatusSkipped:
				outcome.Message = fmt.Sprintf("Skipping %s.", pluginID)
			default:
				outcome.Message = fmt.Sprintf("Failed to update %s.", pluginID)
			}
		}
		if outcome.Status == PluginUpdateStatusUpdated && !dryRun {
			nextRecord := map[string]any{}
			for key, value := range record {
				nextRecord[key] = value
			}
			nextRecord["installedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
			if strings.TrimSpace(res.NextVersion) != "" {
				nextRecord["version"] = strings.TrimSpace(res.NextVersion)
			}
			if strings.TrimSpace(res.InstallPath) != "" {
				nextRecord["installPath"] = strings.TrimSpace(res.InstallPath)
			}
			for key, value := range res.RecordPatch {
				nextRecord[key] = value
			}
			updatedCfg, err := ApplyConfigSet(nextCfg, "plugins.installs."+pluginID, nextRecord)
			if err != nil {
				outcome.Status = PluginUpdateStatusError
				outcome.Message = fmt.Sprintf("Failed to persist update for %s: %v", pluginID, err)
			} else {
				nextCfg = updatedCfg
				rawExt, _ = nextCfg.Extra["extensions"].(map[string]any)
				rawInstalls, _ = rawExt["installs"].(map[string]any)
				changed = true
			}
		}
		outcomes = append(outcomes, outcome)
	}
	return nextCfg, changed, outcomes
}

func ApplyPluginInstallOperation(cfg state.ConfigDoc, pluginID string, installRecord map[string]any, enableEntry bool, includeLoadPath bool) (state.ConfigDoc, error) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return cfg, fmt.Errorf("plugin id is required")
	}
	if len(installRecord) == 0 {
		return cfg, fmt.Errorf("install record is required")
	}
	next, err := ApplyConfigSet(cfg, "plugins.installs."+pluginID, installRecord)
	if err != nil {
		return cfg, err
	}
	if enableEntry {
		next, err = ApplyConfigSet(next, "plugins.entries."+pluginID+".enabled", true)
		if err != nil {
			return cfg, err
		}
	}
	if includeLoadPath {
		rawExt, _ := next.Extra["extensions"].(map[string]any)
		rawInstalls, _ := rawExt["installs"].(map[string]any)
		record, _ := rawInstalls[pluginID].(map[string]any)
		source, _ := record["source"].(string)
		sourcePath, _ := record["sourcePath"].(string)
		if source == "path" && strings.TrimSpace(sourcePath) != "" {
			loadPaths := getStringSlice(rawExt, "load_paths")
			loadPaths = append(loadPaths, sourcePath)
			next, err = ApplyConfigSet(next, "plugins.load.paths", sanitizeStrings(loadPaths))
			if err != nil {
				return cfg, err
			}
		}
	}
	return next, nil
}

func ApplyPluginUninstallOperation(cfg state.ConfigDoc, pluginID string) (state.ConfigDoc, PluginUninstallActions, error) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return cfg, PluginUninstallActions{}, fmt.Errorf("plugin id is required")
	}
	if cfg.Extra == nil {
		return cfg, PluginUninstallActions{}, fmt.Errorf("%w: %s", ErrPluginNotFound, pluginID)
	}
	rawExt, ok := cfg.Extra["extensions"].(map[string]any)
	if !ok || rawExt == nil {
		return cfg, PluginUninstallActions{}, fmt.Errorf("%w: %s", ErrPluginNotFound, pluginID)
	}
	actions := PluginUninstallActions{}

	rawEntries, _ := rawExt["entries"].(map[string]any)
	_, hasEntry := rawEntries[pluginID]
	if hasEntry {
		delete(rawEntries, pluginID)
		actions.Entry = true
		if len(rawEntries) == 0 {
			delete(rawExt, "entries")
		}
	}

	rawInstalls, _ := rawExt["installs"].(map[string]any)
	installRecord, _ := rawInstalls[pluginID].(map[string]any)
	_, hasInstall := rawInstalls[pluginID]
	if hasInstall {
		delete(rawInstalls, pluginID)
		actions.Install = true
		if len(rawInstalls) == 0 {
			delete(rawExt, "installs")
		}
	}
	if !hasEntry && !hasInstall {
		return cfg, PluginUninstallActions{}, fmt.Errorf("%w: %s", ErrPluginNotFound, pluginID)
	}

	allow := getStringSlice(rawExt, "allow")
	if len(allow) > 0 {
		nextAllow := make([]string, 0, len(allow))
		for _, candidate := range allow {
			if candidate == pluginID {
				actions.Allowlist = true
				continue
			}
			nextAllow = append(nextAllow, candidate)
		}
		if len(nextAllow) == 0 {
			delete(rawExt, "allow")
		} else {
			rawExt["allow"] = nextAllow
		}
	}

	source, _ := installRecord["source"].(string)
	sourcePath, _ := installRecord["sourcePath"].(string)
	if source == "path" && strings.TrimSpace(sourcePath) != "" {
		loadPaths := getStringSlice(rawExt, "load_paths")
		if len(loadPaths) > 0 {
			nextLoad := make([]string, 0, len(loadPaths))
			for _, candidate := range loadPaths {
				if candidate == sourcePath {
					actions.LoadPath = true
					continue
				}
				nextLoad = append(nextLoad, candidate)
			}
			if len(nextLoad) == 0 {
				delete(rawExt, "load_paths")
			} else {
				rawExt["load_paths"] = nextLoad
			}
		}
	}

	rawSlots, _ := rawExt["slots"].(map[string]any)
	if rawSlots != nil {
		if memory, _ := rawSlots["memory"].(string); memory == pluginID {
			rawSlots["memory"] = "memory-core"
			actions.MemorySlot = true
		}
		if len(rawSlots) == 0 {
			delete(rawExt, "slots")
		}
	}

	if len(rawExt) == 0 {
		delete(cfg.Extra, "extensions")
		if len(cfg.Extra) == 0 {
			cfg.Extra = nil
		}
	}
	return cfg, actions, nil
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
