package extensions_test

import (
	"testing"

	"metiq/internal/extensions"
	"metiq/internal/store/state"

	// Blank-import at least one extension so its init() registers a
	// constructor and AvailableKinds / RegisterConfigured have something
	// to work with.
	_ "metiq/internal/extensions/imessage"
	_ "metiq/internal/extensions/nextcloud"
	_ "metiq/internal/extensions/qqbot"
	_ "metiq/internal/extensions/telegram"
)

func TestAvailableKinds_NonEmpty(t *testing.T) {
	kinds := extensions.AvailableKinds()
	if len(kinds) == 0 {
		t.Fatal("AvailableKinds returned empty slice; expected compiled-in extension kinds")
	}
}

func TestAvailableKinds_ContainsCompiledAndAliasKinds(t *testing.T) {
	kinds := extensions.AvailableKinds()
	want := map[string]bool{"telegram": false, "qqbot": false, "imessage": false, "nextcloud": false, "nextcloud-talk": false}
	for _, k := range kinds {
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for kind, found := range want {
		if !found {
			t.Errorf("expected %q in available kinds, got %v", kind, kinds)
		}
	}
}

func TestRegisterConfigured_NoChannels(t *testing.T) {
	// Empty config → nothing to register.
	cfg := state.ConfigDoc{}
	count := extensions.RegisterConfigured(cfg)
	if count != 0 {
		t.Errorf("expected 0 registrations for empty config, got %d", count)
	}
}

func TestRegisterConfigured_UnknownKind(t *testing.T) {
	cfg := state.ConfigDoc{
		NostrChannels: map[string]state.NostrChannelConfig{
			"ch1": {Kind: "nonexistent-kind-xyz"},
		},
	}
	count := extensions.RegisterConfigured(cfg)
	if count != 0 {
		t.Errorf("expected 0 for unknown kind, got %d", count)
	}
}
