package main

import (
	"context"
	"strings"
	"testing"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent"
	"metiq/internal/agent/toolbuiltin"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func withACPRoutingTestState(t *testing.T, fn func()) {
	t.Helper()
	prevFleet := nip51FleetEntries
	prevRegistry := capabilityRegistry
	prevPeers := controlACPPeers
	prevBus := controlDMBus
	prevNIP17 := controlNIP17Bus
	prevNIP04 := controlNIP04Bus
	prevToolRegistry := controlToolRegistry
	defer func() {
		nip51FleetEntries = prevFleet
		capabilityRegistry = prevRegistry
		controlACPPeers = prevPeers
		controlDMBus = prevBus
		controlNIP17Bus = prevNIP17
		controlNIP04Bus = prevNIP04
		controlToolRegistry = prevToolRegistry
	}()
	fn()
}

func agentRegistryForACPRoutingTest() *agent.ToolRegistry {
	tools := agent.NewToolRegistry()
	register := func(name string) {
		tools.RegisterWithDef(name, func(context.Context, map[string]any) (string, error) { return "", nil }, agent.ToolDefinition{Name: name})
	}
	register("memory_search")
	register("contextvm_resources_read")
	return tools
}

func TestResolveACPFleetTargetPrefersRegisteredCapablePeer(t *testing.T) {
	withACPRoutingTestState(t, func() {
		plainPubKey := "1111111111111111111111111111111111111111111111111111111111111111"
		richPubKey := "2222222222222222222222222222222222222222222222222222222222222222"
		nip51FleetEntries = map[string]toolbuiltin.FleetEntry{
			plainPubKey: {Pubkey: plainPubKey, Name: "Wizard", Relay: "wss://relay-a"},
			richPubKey:  {Pubkey: richPubKey, Name: "Wizard", Relay: "wss://relay-b"},
		}
		capabilityRegistry = nostruntime.NewCapabilityRegistry()
		capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{PubKey: richPubKey, Runtime: "metiq", ACPVersion: 1, DMSchemes: []string{"giftwrap", "nip17"}, Tools: []string{"web_search"}, CreatedAt: 10, EventID: "cap-rich"})
		controlACPPeers = acppkg.NewPeerRegistry()
		if err := controlACPPeers.Register(acppkg.PeerEntry{PubKey: richPubKey, Alias: "Wizard"}); err != nil {
			t.Fatalf("Register: %v", err)
		}
		controlNIP17Bus = &nostruntime.NIP17Bus{}
		controlDMBus = controlNIP17Bus

		pubkey, display, err := resolveACPFleetTarget("wizard")
		if err != nil {
			t.Fatalf("resolveACPFleetTarget: %v", err)
		}
		if pubkey != richPubKey {
			t.Fatalf("pubkey = %q, want %q", pubkey, richPubKey)
		}
		if display != "Wizard" {
			t.Fatalf("display = %q, want Wizard", display)
		}
	})
}

func TestResolveACPFleetTargetFallsBackFromIncompatiblePeer(t *testing.T) {
	withACPRoutingTestState(t, func() {
		incompatiblePubKey := "3333333333333333333333333333333333333333333333333333333333333333"
		compatiblePubKey := "4444444444444444444444444444444444444444444444444444444444444444"
		nip51FleetEntries = map[string]toolbuiltin.FleetEntry{
			incompatiblePubKey: {Pubkey: incompatiblePubKey, Name: "Wizard", Relay: "wss://relay-a"},
			compatiblePubKey:   {Pubkey: compatiblePubKey, Name: "Wizard", Relay: "wss://relay-b"},
		}
		capabilityRegistry = nostruntime.NewCapabilityRegistry()
		capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{PubKey: incompatiblePubKey, Runtime: "metiq", ACPVersion: 1, DMSchemes: []string{"nip04"}, CreatedAt: 10, EventID: "cap-nip04"})
		capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{PubKey: compatiblePubKey, Runtime: "metiq", ACPVersion: 1, DMSchemes: []string{"nip17"}, CreatedAt: 11, EventID: "cap-nip17"})
		controlACPPeers = acppkg.NewPeerRegistry()
		for _, pubkey := range []string{incompatiblePubKey, compatiblePubKey} {
			if err := controlACPPeers.Register(acppkg.PeerEntry{PubKey: pubkey, Alias: "Wizard"}); err != nil {
				t.Fatalf("Register(%s): %v", pubkey, err)
			}
		}
		controlNIP17Bus = &nostruntime.NIP17Bus{}
		controlDMBus = controlNIP17Bus

		pubkey, display, err := resolveACPFleetTarget("wizard")
		if err != nil {
			t.Fatalf("resolveACPFleetTarget: %v", err)
		}
		if pubkey != compatiblePubKey {
			t.Fatalf("pubkey = %q, want %q", pubkey, compatiblePubKey)
		}
		if display != "Wizard" {
			t.Fatalf("display = %q, want Wizard", display)
		}
	})
}

func TestResolveACPFleetTargetRejectsIncompatibleDMScheme(t *testing.T) {
	withACPRoutingTestState(t, func() {
		peerPubKey := "3333333333333333333333333333333333333333333333333333333333333333"
		nip51FleetEntries = map[string]toolbuiltin.FleetEntry{
			peerPubKey: {Pubkey: peerPubKey, Name: "Wizard", Relay: "wss://relay-a"},
		}
		capabilityRegistry = nostruntime.NewCapabilityRegistry()
		capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{PubKey: peerPubKey, Runtime: "metiq", ACPVersion: 1, DMSchemes: []string{"nip04"}, CreatedAt: 10, EventID: "cap-nip04"})
		controlACPPeers = acppkg.NewPeerRegistry()
		if err := controlACPPeers.Register(acppkg.PeerEntry{PubKey: peerPubKey, Alias: "Wizard"}); err != nil {
			t.Fatalf("Register: %v", err)
		}
		controlNIP17Bus = &nostruntime.NIP17Bus{}
		controlDMBus = controlNIP17Bus

		_, _, err := resolveACPFleetTarget("wizard")
		if err == nil || !strings.Contains(err.Error(), "compatible DM scheme") {
			t.Fatalf("err = %v, want compatible DM scheme failure", err)
		}
	})
}

func TestResolveACPFleetTargetForConfigPrefersConfiguredTransport(t *testing.T) {
	withACPRoutingTestState(t, func() {
		nip04PubKey := "5555555555555555555555555555555555555555555555555555555555555555"
		nip17PubKey := "6666666666666666666666666666666666666666666666666666666666666666"
		nip51FleetEntries = map[string]toolbuiltin.FleetEntry{
			nip04PubKey: {Pubkey: nip04PubKey, Name: "OpenClaw", Relay: "wss://relay-a", DMSchemes: []string{"nip04"}, ACPVersion: 1},
			nip17PubKey: {Pubkey: nip17PubKey, Name: "OpenClaw", Relay: "wss://relay-b", DMSchemes: []string{"giftwrap", "nip17"}, ACPVersion: 1},
		}
		controlACPPeers = acppkg.NewPeerRegistry()
		for _, pubkey := range []string{nip04PubKey, nip17PubKey} {
			if err := controlACPPeers.Register(acppkg.PeerEntry{PubKey: pubkey, Alias: "OpenClaw"}); err != nil {
				t.Fatalf("Register(%s): %v", pubkey, err)
			}
		}
		controlNIP17Bus = &nostruntime.NIP17Bus{}
		controlNIP04Bus = &nostruntime.DMBus{}
		controlDMBus = controlNIP17Bus

		cfg := state.ConfigDoc{ACP: state.ACPConfig{Transport: "nip04"}}
		pubkey, _, err := resolveACPFleetTargetForConfig("openclaw", cfg)
		if err != nil {
			t.Fatalf("resolveACPFleetTargetForConfig: %v", err)
		}
		if pubkey != nip04PubKey {
			t.Fatalf("pubkey = %q, want %q", pubkey, nip04PubKey)
		}
	})
}

func TestResolveACPFleetTargetForConfigAndRequirementsPrefersMatchingContextVMSurface(t *testing.T) {
	withACPRoutingTestState(t, func() {
		plainPubKey := "abababababababababababababababababababababababababababababababab"
		ctxvmPubKey := "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
		nip51FleetEntries = map[string]toolbuiltin.FleetEntry{
			plainPubKey: {Pubkey: plainPubKey, Name: "Worker", Relay: "wss://relay-a", DMSchemes: []string{"nip17"}, ACPVersion: 1, Tools: []string{"memory_search"}},
			ctxvmPubKey: {Pubkey: ctxvmPubKey, Name: "Worker", Relay: "wss://relay-b", DMSchemes: []string{"nip17"}, ACPVersion: 1, Tools: []string{"memory_search", "contextvm_resources_read"}},
		}
		controlACPPeers = acppkg.NewPeerRegistry()
		for _, pubkey := range []string{plainPubKey, ctxvmPubKey} {
			if err := controlACPPeers.Register(acppkg.PeerEntry{PubKey: pubkey, Alias: "Worker"}); err != nil {
				t.Fatalf("Register(%s): %v", pubkey, err)
			}
		}
		controlNIP17Bus = &nostruntime.NIP17Bus{}
		controlDMBus = controlNIP17Bus
		controlToolRegistry = agentRegistryForACPRoutingTest()

		req := buildACPTargetRequirements(state.ConfigDoc{}, turnToolConstraints{EnabledTools: []string{"contextvm_resources_read"}})
		pubkey, _, err := resolveACPFleetTargetForConfigAndRequirements("worker", state.ConfigDoc{}, req)
		if err != nil {
			t.Fatalf("resolveACPFleetTargetForConfigAndRequirements: %v", err)
		}
		if pubkey != ctxvmPubKey {
			t.Fatalf("pubkey = %q, want %q", pubkey, ctxvmPubKey)
		}
	})
}

func TestResolveACPFleetTargetForConfigAndRequirementsRejectsExplicitlyIncompatibleSurface(t *testing.T) {
	withACPRoutingTestState(t, func() {
		peerPubKey := "efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef"
		nip51FleetEntries = map[string]toolbuiltin.FleetEntry{
			peerPubKey: {Pubkey: peerPubKey, Name: "Worker", Relay: "wss://relay-a", DMSchemes: []string{"nip17"}, ACPVersion: 1, Tools: []string{"memory_search"}},
		}
		controlACPPeers = acppkg.NewPeerRegistry()
		if err := controlACPPeers.Register(acppkg.PeerEntry{PubKey: peerPubKey, Alias: "Worker"}); err != nil {
			t.Fatalf("Register: %v", err)
		}
		controlNIP17Bus = &nostruntime.NIP17Bus{}
		controlDMBus = controlNIP17Bus
		controlToolRegistry = agentRegistryForACPRoutingTest()

		req := buildACPTargetRequirements(state.ConfigDoc{}, turnToolConstraints{EnabledTools: []string{"contextvm_resources_read"}})
		_, _, err := resolveACPFleetTargetForConfigAndRequirements("worker", state.ConfigDoc{}, req)
		if err == nil || !strings.Contains(err.Error(), "contextvm_features: resources_read") {
			t.Fatalf("err = %v, want contextvm feature mismatch", err)
		}
	})
}

func TestResolveACPDMTransportAutoFallsBackToPeerCompatibleScheme(t *testing.T) {
	withACPRoutingTestState(t, func() {
		peerPubKey := "7777777777777777777777777777777777777777777777777777777777777777"
		capabilityRegistry = nostruntime.NewCapabilityRegistry()
		capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{PubKey: peerPubKey, Runtime: "openclaw", ACPVersion: 1, DMSchemes: []string{"nip04"}, CreatedAt: 10, EventID: "cap-nip04"})
		controlNIP17Bus = &nostruntime.NIP17Bus{}
		controlNIP04Bus = &nostruntime.DMBus{}
		controlDMBus = controlNIP17Bus

		bus, scheme, err := resolveACPDMTransport(state.ConfigDoc{}, peerPubKey)
		if err != nil {
			t.Fatalf("resolveACPDMTransport: %v", err)
		}
		if scheme != "nip04" {
			t.Fatalf("scheme = %q, want nip04", scheme)
		}
		if _, ok := bus.(*nostruntime.DMBus); !ok {
			t.Fatalf("bus type = %T, want *nostruntime.DMBus", bus)
		}
	})
}

func TestResolveACPDMTransportAutoPrefersNIP17WhenPeerSupportsBoth(t *testing.T) {
	withACPRoutingTestState(t, func() {
		peerPubKey := "8888888888888888888888888888888888888888888888888888888888888888"
		capabilityRegistry = nostruntime.NewCapabilityRegistry()
		capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{PubKey: peerPubKey, Runtime: "metiq", ACPVersion: 1, DMSchemes: []string{"giftwrap", "nip04"}, CreatedAt: 10, EventID: "cap-both"})
		controlNIP17Bus = &nostruntime.NIP17Bus{}
		controlNIP04Bus = &nostruntime.DMBus{}
		controlDMBus = controlNIP17Bus

		bus, scheme, err := resolveACPDMTransport(state.ConfigDoc{}, peerPubKey)
		if err != nil {
			t.Fatalf("resolveACPDMTransport: %v", err)
		}
		if scheme != "nip17" {
			t.Fatalf("scheme = %q, want nip17", scheme)
		}
		if _, ok := bus.(*nostruntime.NIP17Bus); !ok {
			t.Fatalf("bus type = %T, want *nostruntime.NIP17Bus", bus)
		}
	})
}

func TestResolveACPDMTransportHonorsConfiguredNIP04Mode(t *testing.T) {
	withACPRoutingTestState(t, func() {
		peerPubKey := "9999999999999999999999999999999999999999999999999999999999999999"
		capabilityRegistry = nostruntime.NewCapabilityRegistry()
		capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{PubKey: peerPubKey, Runtime: "openclaw", ACPVersion: 1, DMSchemes: []string{"nip04"}, CreatedAt: 10, EventID: "cap-nip04"})
		controlNIP04Bus = &nostruntime.DMBus{}
		controlDMBus = controlNIP04Bus

		bus, scheme, err := resolveACPDMTransport(state.ConfigDoc{ACP: state.ACPConfig{Transport: "nip04"}}, peerPubKey)
		if err != nil {
			t.Fatalf("resolveACPDMTransport: %v", err)
		}
		if scheme != "nip04" {
			t.Fatalf("scheme = %q, want nip04", scheme)
		}
		if _, ok := bus.(*nostruntime.DMBus); !ok {
			t.Fatalf("bus type = %T, want *nostruntime.DMBus", bus)
		}
	})
}

func TestResolveACPDMTransportAutoPrefersNIP04ForUnknownPeers(t *testing.T) {
	withACPRoutingTestState(t, func() {
		peerPubKey := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		controlNIP17Bus = &nostruntime.NIP17Bus{}
		controlNIP04Bus = &nostruntime.DMBus{}
		controlDMBus = controlNIP17Bus

		bus, scheme, err := resolveACPDMTransport(state.ConfigDoc{}, peerPubKey)
		if err != nil {
			t.Fatalf("resolveACPDMTransport: %v", err)
		}
		if scheme != "nip04" {
			t.Fatalf("scheme = %q, want nip04", scheme)
		}
		if _, ok := bus.(*nostruntime.DMBus); !ok {
			t.Fatalf("bus type = %T, want *nostruntime.DMBus", bus)
		}
	})
}
