package methods

import (
	"errors"
	"fmt"
	"metiq/internal/store/state"
	"sort"
	"strings"
	"time"
)

var ErrPluginNotFound = errors.New("plugin not found")

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
