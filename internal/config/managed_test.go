package config

import (
	"strings"
	"testing"

	"metiq/internal/store/state"
)

func TestApplyManagedSettingsPrecedence(t *testing.T) {
	operator := state.ConfigDoc{Permissions: state.PermissionsConfig{DefaultBehavior: "allow"}}
	managed := ManagedSettings{
		RequireToolApproval:             true,
		AllowManagedPermissionRulesOnly: true,
		Permissions: &state.PermissionsConfig{
			DefaultBehavior: "deny",
			Rules:           []state.PermissionRule{{ID: "deny-bash", Behavior: "deny", Tool: "bash"}},
		},
	}
	doc := ApplyManagedSettings(operator, managed)
	if doc.Permissions.DefaultBehavior != "deny" || len(doc.Permissions.Rules) != 1 {
		t.Fatalf("managed permissions did not win: %+v", doc.Permissions)
	}
	if _, ok := doc.Extra["managed_settings"]; !ok {
		t.Fatalf("managed_settings not recorded in Extra: %+v", doc.Extra)
	}
}

func TestEnforceManagedSettingsRejectsRuntimeOverride(t *testing.T) {
	base := ApplyManagedSettings(state.ConfigDoc{}, ManagedSettings{
		RequireToolApproval: true,
		LockedPaths:         []string{"agent.default_model"},
	})
	base.Agent.DefaultModel = "managed-model"
	candidate := base
	candidate.Permissions.DefaultBehavior = "allow"
	if err := EnforceManagedSettings(base, candidate); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("expected permissions lockdown error, got %v", err)
	}
	candidate = base
	candidate.Agent.DefaultModel = "operator-model"
	if err := EnforceManagedSettings(base, candidate); err == nil || !strings.Contains(err.Error(), "agent.default_model") {
		t.Fatalf("expected locked path error, got %v", err)
	}
}

func TestParseConfigPreservesManagedSettingsForAudit(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"managed_settings":{"require_tool_approval":true}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	managed, ok := ManagedSettingsFromConfig(doc)
	if !ok || !managed.RequireToolApproval {
		t.Fatalf("managed settings not parsed: ok=%v managed=%+v", ok, managed)
	}
}
