package main

import (
	"testing"

	configpkg "metiq/internal/config"
	"metiq/internal/store/state"
)

func TestRuntimeConfigStoreReappliesManagedPrecedence(t *testing.T) {
	managed := configpkg.ManagedSettings{
		RequireToolApproval: true,
		Permissions: &state.PermissionsConfig{
			DefaultBehavior: "deny",
			Rules:           []state.PermissionRule{{ID: "managed-deny", Tool: "bash", Behavior: "deny"}},
		},
	}
	store := newRuntimeConfigStoreWithManaged(state.ConfigDoc{
		Permissions: state.PermissionsConfig{DefaultBehavior: "allow"},
	}, managed)
	if got := store.Get().Permissions.DefaultBehavior; got != "deny" {
		t.Fatalf("startup managed behavior=%q want deny", got)
	}

	store.Set(state.ConfigDoc{Permissions: state.PermissionsConfig{DefaultBehavior: "allow"}})
	got := store.Get()
	if got.Permissions.DefaultBehavior != "deny" || len(got.Permissions.Rules) != 1 || got.Permissions.Rules[0].ID != "managed-deny" {
		t.Fatalf("runtime override escaped managed precedence: %+v", got.Permissions)
	}
	if _, ok := got.Extra["managed_settings"]; !ok {
		t.Fatalf("managed layer missing after runtime Set: %+v", got.Extra)
	}
}
