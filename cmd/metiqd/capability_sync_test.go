package main

import (
	"context"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/nostr/nip51"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func TestFleetDirectoryIncludesCapabilityOverlay(t *testing.T) {
	prevFleet := nip51FleetEntries
	prevRegistry := capabilityRegistry
	defer func() {
		nip51FleetEntries = prevFleet
		capabilityRegistry = prevRegistry
	}()

	nip51FleetEntries = map[string]toolbuiltin.FleetEntry{
		"peer": {Pubkey: "peer", Name: "Wizard", Relay: "wss://relay-a"},
	}
	capabilityRegistry = nostruntime.NewCapabilityRegistry()
	capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{
		PubKey:            "peer",
		Runtime:           "metiq",
		RuntimeVersion:    "1.2.3",
		DMSchemes:         []string{"nip17", "giftwrap"},
		ACPVersion:        1,
		Tools:             []string{"memory_search", "web_search"},
		ContextVMFeatures: []string{"discover", "resources_read"},
		Relays:            []string{"wss://relay-a", "wss://relay-b"},
		CreatedAt:         10,
		EventID:           "cap1",
	})

	entries := fleetDirectory()
	if len(entries) != 1 {
		t.Fatalf("fleetDirectory len = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Name != "Wizard" || entry.RuntimeVersion != "1.2.3" || entry.ACPVersion != 1 {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if len(entry.Tools) != 2 || entry.Tools[0] != "memory_search" || entry.Tools[1] != "web_search" {
		t.Fatalf("unexpected tools: %+v", entry.Tools)
	}
	if len(entry.ContextVMFeatures) != 2 || entry.ContextVMFeatures[0] != "discover" || entry.ContextVMFeatures[1] != "resources_read" {
		t.Fatalf("unexpected contextvm features: %+v", entry.ContextVMFeatures)
	}
	if len(entry.Relays) != 2 || entry.Relays[1] != "wss://relay-b" {
		t.Fatalf("unexpected relays: %+v", entry.Relays)
	}
}

func TestCurrentCapabilityToolSurfaceIncludesContextVMFeatures(t *testing.T) {
	prevRegistry := controlToolRegistry
	defer func() { controlToolRegistry = prevRegistry }()

	tools := agent.NewToolRegistry()
	register := func(name string) {
		tools.RegisterWithDef(name, func(context.Context, map[string]any) (string, error) { return "", nil }, agent.ToolDefinition{Name: name})
	}
	register("memory_search")
	register("contextvm_discover")
	register("contextvm_resources_read")
	register("contextvm_prompts_get")
	controlToolRegistry = tools

	surface := currentCapabilityToolSurface(context.Background(), state.ConfigDoc{}, nil)
	wantTools := []string{"contextvm_discover", "contextvm_prompts_get", "contextvm_resources_read", "memory_search"}
	if len(surface.ToolNames) != len(wantTools) {
		t.Fatalf("tool names = %v, want %v", surface.ToolNames, wantTools)
	}
	for i := range wantTools {
		if surface.ToolNames[i] != wantTools[i] {
			t.Fatalf("tool names = %v, want %v", surface.ToolNames, wantTools)
		}
	}
	wantFeatures := []string{"discover", "prompts_get", "resources_read"}
	if len(surface.ContextVMFeatures) != len(wantFeatures) {
		t.Fatalf("contextvm features = %v, want %v", surface.ContextVMFeatures, wantFeatures)
	}
	for i := range wantFeatures {
		if surface.ContextVMFeatures[i] != wantFeatures[i] {
			t.Fatalf("contextvm features = %v, want %v", surface.ContextVMFeatures, wantFeatures)
		}
	}
}

func TestSetNIP51ListEntriesRemovesStalePeers(t *testing.T) {
	prevPerListPubkeys := nip51PerListPubkeys
	prevPerListEntries := nip51PerListEntries
	prevFleet := nip51FleetEntries
	prevMonitor := capabilityMonitor
	defer func() {
		nip51PerListPubkeys = prevPerListPubkeys
		nip51PerListEntries = prevPerListEntries
		nip51FleetEntries = prevFleet
		capabilityMonitor = prevMonitor
	}()

	nip51PerListPubkeys = map[string]map[string]struct{}{}
	nip51PerListEntries = map[string]map[string]toolbuiltin.FleetEntry{}
	nip51FleetEntries = map[string]toolbuiltin.FleetEntry{}
	capabilityMonitor = nil

	setNIP51ListEntries("owner", "fleet", []nip51.ListEntry{
		{Tag: "p", Value: "peer-a", Petname: "A", Relay: "wss://a"},
		{Tag: "p", Value: "peer-b", Petname: "B", Relay: "wss://b"},
	})
	if got := fleetPeerPubkeys(); len(got) != 2 {
		t.Fatalf("fleetPeerPubkeys len = %d, want 2 (%v)", len(got), got)
	}

	setNIP51ListEntries("owner", "fleet", []nip51.ListEntry{
		{Tag: "p", Value: "peer-a", Petname: "A", Relay: "wss://a"},
	})
	got := fleetPeerPubkeys()
	if len(got) != 1 || got[0] != "peer-a" {
		t.Fatalf("fleetPeerPubkeys = %v, want [peer-a]", got)
	}
	entries := fleetDirectory()
	if len(entries) != 1 || entries[0].Pubkey != "peer-a" {
		t.Fatalf("fleetDirectory = %+v, want peer-a only", entries)
	}
}

func TestCurrentCapabilitySubscriptionRelaysIncludesFleetHints(t *testing.T) {
	prevFleet := nip51FleetEntries
	prevRegistry := capabilityRegistry
	defer func() {
		nip51FleetEntries = prevFleet
		capabilityRegistry = prevRegistry
	}()

	nip51FleetEntries = map[string]toolbuiltin.FleetEntry{
		"peer-a": {Pubkey: "peer-a", Relay: "wss://hint-a"},
		"peer-b": {Pubkey: "peer-b", Relay: "wss://hint-b"},
	}
	capabilityRegistry = nostruntime.NewCapabilityRegistry()
	capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{
		PubKey:    "peer-a",
		Relays:    []string{"wss://cap-a", "wss://hint-a"},
		CreatedAt: 10,
		EventID:   "cap-a",
	})

	relays := currentCapabilitySubscriptionRelays(state.ConfigDoc{Relays: state.RelayPolicy{Read: []string{"wss://cfg-read"}, Write: []string{"wss://cfg-write"}}})
	want := []string{"wss://cap-a", "wss://cfg-read", "wss://cfg-write", "wss://hint-a", "wss://hint-b"}
	if len(relays) != len(want) {
		t.Fatalf("relays = %v, want %v", relays, want)
	}
	for i := range want {
		if relays[i] != want[i] {
			t.Fatalf("relays = %v, want %v", relays, want)
		}
	}
}

func TestCurrentCapabilityDMSchemesIncludesActiveTransports(t *testing.T) {
	prevBus := controlDMBus
	prevNIP17 := controlNIP17Bus
	prevNIP04 := controlNIP04Bus
	defer func() {
		controlDMBus = prevBus
		controlNIP17Bus = prevNIP17
		controlNIP04Bus = prevNIP04
	}()

	controlNIP17Bus = &nostruntime.NIP17Bus{}
	controlNIP04Bus = &nostruntime.DMBus{}
	schemes := currentCapabilityDMSchemes()
	want := []string{"giftwrap", "nip04", "nip17", "nip44"}
	if len(schemes) != len(want) {
		t.Fatalf("schemes = %v, want %v", schemes, want)
	}
	for i := range want {
		if schemes[i] != want[i] {
			t.Fatalf("schemes = %v, want %v", schemes, want)
		}
	}
}

func TestFleetWorkspaceDirAccessors(t *testing.T) {
	prev := getFleetWorkspaceDir()
	defer setFleetWorkspaceDir(prev)

	setFleetWorkspaceDir("/tmp/fleet-a")
	if got := getFleetWorkspaceDir(); got != "/tmp/fleet-a" {
		t.Fatalf("getFleetWorkspaceDir = %q, want /tmp/fleet-a", got)
	}
	setFleetWorkspaceDir("/tmp/fleet-b")
	if got := getFleetWorkspaceDir(); got != "/tmp/fleet-b" {
		t.Fatalf("getFleetWorkspaceDir = %q, want /tmp/fleet-b", got)
	}
}
