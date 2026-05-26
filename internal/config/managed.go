package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"metiq/internal/store/state"
)

// ManagedSettings are enterprise/MDM-style controls supplied from bootstrap or
// another trusted local channel. They take precedence over operator-editable
// runtime config.
type ManagedSettings struct {
	DisableBypassPermissionsMode    bool                     `json:"disable_bypass_permissions_mode,omitempty"`
	RequireToolApproval             bool                     `json:"require_tool_approval,omitempty"`
	ManagedHooksOnly                bool                     `json:"managed_hooks_only,omitempty"`
	AllowManagedPermissionRulesOnly bool                     `json:"allow_managed_permission_rules_only,omitempty"`
	Permissions                     *state.PermissionsConfig `json:"permissions,omitempty"`
	LockedPaths                     []string                 `json:"locked_paths,omitempty"`
}

func (m ManagedSettings) active() bool {
	return m.DisableBypassPermissionsMode || m.RequireToolApproval || m.ManagedHooksOnly || m.AllowManagedPermissionRulesOnly || m.Permissions != nil || len(m.LockedPaths) > 0
}

// ManagedSettingsFromBootstrap returns trusted managed settings from bootstrap.
func ManagedSettingsFromBootstrap(bs BootstrapConfig) ManagedSettings {
	if bs.ManagedSettings == nil {
		return ManagedSettings{}
	}
	return *bs.ManagedSettings
}

// ManagedSettingsFromConfig parses ConfigDoc.Extra["managed_settings"].
func ManagedSettingsFromConfig(doc state.ConfigDoc) (ManagedSettings, bool) {
	if doc.Extra == nil {
		return ManagedSettings{}, false
	}
	raw, ok := doc.Extra["managed_settings"]
	if !ok {
		return ManagedSettings{}, false
	}
	var managed ManagedSettings
	data, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(data, &managed) != nil {
		return ManagedSettings{}, false
	}
	return managed, managed.active()
}

// ApplyManagedSettings overlays trusted managed settings onto doc. Managed
// values win over operator config.
func ApplyManagedSettings(doc state.ConfigDoc, managed ManagedSettings) state.ConfigDoc {
	if !managed.active() {
		return doc
	}
	if doc.Extra == nil {
		doc.Extra = map[string]any{}
	}
	managedMap := managedSettingsMap(managed)
	doc.Extra["managed_settings"] = managedMap

	if managed.Permissions != nil || managed.AllowManagedPermissionRulesOnly {
		if managed.Permissions != nil {
			doc.Permissions = *managed.Permissions
		} else {
			doc.Permissions.Rules = nil
			doc.Permissions.Agents = nil
		}
	}
	if managed.RequireToolApproval {
		if doc.Permissions.DefaultBehavior == "" || strings.EqualFold(doc.Permissions.DefaultBehavior, "allow") {
			doc.Permissions.DefaultBehavior = "ask"
		}
	}
	if managed.ManagedHooksOnly {
		doc.Extra["managed_hooks_only"] = true
	}
	return doc
}

// EnforceManagedSettings rejects runtime overrides that would change a managed
// field after ApplyManagedSettings has established the trusted baseline.
func EnforceManagedSettings(current, candidate state.ConfigDoc) error {
	managed, ok := ManagedSettingsFromConfig(current)
	if !ok || !managed.active() {
		return nil
	}
	if managed.RequireToolApproval || managed.AllowManagedPermissionRulesOnly || managed.Permissions != nil {
		if !reflect.DeepEqual(current.Permissions, candidate.Permissions) {
			return fmt.Errorf("managed settings lockdown prevents overriding permissions")
		}
	}
	for _, path := range managed.LockedPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		cur, _ := configPathValue(current, path)
		next, _ := configPathValue(candidate, path)
		if !reflect.DeepEqual(cur, next) {
			return fmt.Errorf("managed settings lockdown prevents overriding %s", path)
		}
	}
	return nil
}

func managedSettingsMap(managed ManagedSettings) map[string]any {
	data, _ := json.Marshal(managed)
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out
}

func configPathValue(doc state.ConfigDoc, path string) (any, bool) {
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, false
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var cur any = root
	for _, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
