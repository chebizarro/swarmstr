package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDescriptorArraySupportsTupleRows(t *testing.T) {
	raw := []byte(`
const CORE_GATEWAY_METHOD_SPECS = [
  ["health", "health", "operator.read", "<=2026.7"],
  [
    "models.authLogout",
    "models-auth-status",
    "operator.admin",
    "<=2026.7",
    { controlPlaneWrite: true },
  ],
  ["connect", "connect", "operator.admin", "<=2026.7", { advertise: false }],
] as const satisfies readonly CoreGatewayMethodSpecRow[];
`)

	got, err := parseDescriptorArray(raw, "const CORE_GATEWAY_METHOD_SPECS", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []descriptor{
		{Name: "health", Advertised: true},
		{Name: "models.authLogout", Advertised: true},
		{Name: "connect", Advertised: false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d descriptors, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("descriptor %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestParseDescriptorArrayStillSupportsObjectRows(t *testing.T) {
	raw := []byte(`
const coreCliCommandCatalog = defineCommandDescriptorCatalog([
  { name: "gateway", description: "Gateway" },
  { name: "status", description: "Status" },
] as const);
`)

	got, err := parseDescriptorArray(raw, "const coreCliCommandCatalog", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "gateway" || got[1].Name != "status" {
		t.Fatalf("unexpected descriptors: %#v", got)
	}
}

func TestPreserveGatewayNotesUsesExplicitTriageOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-method-parity.json")
	previous := gatewaySnapshot{Entries: []gatewayEntry{
		{Method: "health", Notes: "curated snapshot note"},
		{Method: "status", Notes: "old note"},
		{Method: "logs.tail"},
	}}
	raw, err := marshalJSON(previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	triage := triageConfig{MethodNotes: map[string]string{"status": "explicit triage note"}}
	if err := preserveGatewayNotes(path, &triage); err != nil {
		t.Fatal(err)
	}
	if got := triage.MethodNotes["health"]; got != "curated snapshot note" {
		t.Fatalf("preserved note = %q", got)
	}
	if got := triage.MethodNotes["status"]; got != "explicit triage note" {
		t.Fatalf("explicit note overwritten: %q", got)
	}
	if _, ok := triage.MethodNotes["logs.tail"]; ok {
		t.Fatal("empty snapshot note should not create a triage note")
	}
}
