package main

import (
	"strings"
	"testing"

	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/nostr/nip51"
	nostruntime "metiq/internal/nostr/runtime"
)

// withFleetTestState saves and restores the fleet global state around a test.
func withFleetTestState(t *testing.T, fn func()) {
	t.Helper()
	prevFleet := nip51FleetEntries
	prevPerList := nip51PerListEntries
	prevPerPubkeys := nip51PerListPubkeys
	prevRegistry := capabilityRegistry
	defer func() {
		nip51FleetEntries = prevFleet
		nip51PerListEntries = prevPerList
		nip51PerListPubkeys = prevPerPubkeys
		capabilityRegistry = prevRegistry
	}()
	nip51FleetEntries = make(map[string]toolbuiltin.FleetEntry)
	nip51PerListEntries = make(map[string]map[string]toolbuiltin.FleetEntry)
	nip51PerListPubkeys = make(map[string]map[string]struct{})
	fn()
}

// ── NIP-51 FIPS tag parsing ───────────────────────────────────────────────────

func TestSetNIP51ListEntries_FIPS_tags(t *testing.T) {
	withFleetTestState(t, func() {
		pk := "384169899efb2f577db9f2995239c6ed07298d7e2fdd91ff6c86d8c384a42b3a"
		entries := []nip51.ListEntry{
			{Tag: "fips", Value: "true"},
			{Tag: "fips_transport", Value: "udp:2121"},
			{Tag: "p", Value: pk, Relay: "wss://relay.example.com", Petname: "Agent1"},
		}

		setNIP51ListEntries("owner1", "fleet", entries)

		fleet := fleetDirectory()
		if len(fleet) != 1 {
			t.Fatalf("expected 1 fleet entry, got %d", len(fleet))
		}
		entry := fleet[0]
		if !entry.FIPSEnabled {
			t.Fatal("expected FIPSEnabled=true")
		}
		if entry.FIPSTransport != "udp:2121" {
			t.Fatalf("expected FIPSTransport=udp:2121, got %q", entry.FIPSTransport)
		}
		if entry.Name != "Agent1" {
			t.Fatalf("expected Name=Agent1, got %q", entry.Name)
		}
	})
}

func TestSetNIP51ListEntries_no_FIPS_tags(t *testing.T) {
	withFleetTestState(t, func() {
		pk := "384169899efb2f577db9f2995239c6ed07298d7e2fdd91ff6c86d8c384a42b3a"
		entries := []nip51.ListEntry{
			{Tag: "p", Value: pk, Petname: "NoFIPS"},
		}

		setNIP51ListEntries("owner1", "fleet", entries)

		fleet := fleetDirectory()
		if len(fleet) != 1 {
			t.Fatalf("expected 1 fleet entry, got %d", len(fleet))
		}
		if fleet[0].FIPSEnabled {
			t.Fatal("expected FIPSEnabled=false when no FIPS tags")
		}
		if fleet[0].FIPSIPv6Addr != "" {
			t.Fatalf("expected no FIPSIPv6Addr, got %q", fleet[0].FIPSIPv6Addr)
		}
	})
}

// ── IPv6 address derivation ───────────────────────────────────────────────────

func TestFleetDirectory_derives_FIPS_IPv6(t *testing.T) {
	withFleetTestState(t, func() {
		pk := "384169899efb2f577db9f2995239c6ed07298d7e2fdd91ff6c86d8c384a42b3a"
		entries := []nip51.ListEntry{
			{Tag: "fips", Value: "true"},
			{Tag: "p", Value: pk, Petname: "Mesh1"},
		}

		setNIP51ListEntries("owner1", "fleet", entries)

		fleet := fleetDirectory()
		if len(fleet) != 1 {
			t.Fatalf("expected 1 fleet entry, got %d", len(fleet))
		}

		// Verify the derived address matches the identity module.
		expectedIP, err := nostruntime.FIPSIPv6FromPubkey(pk)
		if err != nil {
			t.Fatalf("FIPSIPv6FromPubkey: %v", err)
		}
		if fleet[0].FIPSIPv6Addr != expectedIP.String() {
			t.Fatalf("expected FIPSIPv6Addr=%s, got %q", expectedIP.String(), fleet[0].FIPSIPv6Addr)
		}
		if !strings.HasPrefix(fleet[0].FIPSIPv6Addr, "fd") {
			t.Fatalf("expected fd00::/8 prefix, got %s", fleet[0].FIPSIPv6Addr)
		}
	})
}

// ── DMSchemes includes "fips" ─────────────────────────────────────────────────

func TestFleetDirectory_adds_fips_to_dm_schemes(t *testing.T) {
	withFleetTestState(t, func() {
		pk := "384169899efb2f577db9f2995239c6ed07298d7e2fdd91ff6c86d8c384a42b3a"

		// Set up capability registry with existing DM schemes.
		capabilityRegistry = nostruntime.NewCapabilityRegistry()
		capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{
			PubKey:    pk,
			Runtime:   "metiq",
			DMSchemes: []string{"nip17"},
			CreatedAt: 10,
			EventID:   "e1",
		})

		entries := []nip51.ListEntry{
			{Tag: "fips", Value: "true"},
			{Tag: "p", Value: pk, Petname: "Agent1"},
		}
		setNIP51ListEntries("owner1", "fleet", entries)

		fleet := fleetDirectory()
		if len(fleet) != 1 {
			t.Fatalf("expected 1 fleet entry, got %d", len(fleet))
		}

		hasFIPS := false
		hasNIP17 := false
		for _, s := range fleet[0].DMSchemes {
			if s == "fips" {
				hasFIPS = true
			}
			if s == "nip17" {
				hasNIP17 = true
			}
		}
		if !hasFIPS {
			t.Fatal("expected 'fips' in DMSchemes")
		}
		if !hasNIP17 {
			t.Fatal("expected 'nip17' preserved in DMSchemes")
		}
	})
}

func TestFleetDirectory_no_duplicate_fips_scheme(t *testing.T) {
	withFleetTestState(t, func() {
		pk := "384169899efb2f577db9f2995239c6ed07298d7e2fdd91ff6c86d8c384a42b3a"

		// Capability already includes "fips" in DMSchemes.
		capabilityRegistry = nostruntime.NewCapabilityRegistry()
		capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{
			PubKey:    pk,
			Runtime:   "metiq",
			DMSchemes: []string{"nip17", "fips"},
			CreatedAt: 10,
			EventID:   "e1",
		})

		entries := []nip51.ListEntry{
			{Tag: "fips", Value: "true"},
			{Tag: "p", Value: pk},
		}
		setNIP51ListEntries("owner1", "fleet", entries)

		fleet := fleetDirectory()
		fipsCount := 0
		for _, s := range fleet[0].DMSchemes {
			if s == "fips" {
				fipsCount++
			}
		}
		if fipsCount != 1 {
			t.Fatalf("expected exactly 1 'fips' in DMSchemes, got %d", fipsCount)
		}
	})
}

// ── Capability registry FIPS enrichment ───────────────────────────────────────

func TestFleetDirectory_capability_fips_enrichment(t *testing.T) {
	withFleetTestState(t, func() {
		pk := "384169899efb2f577db9f2995239c6ed07298d7e2fdd91ff6c86d8c384a42b3a"

		// NIP-51 list without FIPS tags, but capability event has FIPS.
		capabilityRegistry = nostruntime.NewCapabilityRegistry()
		capabilityRegistry.Set(nostruntime.CapabilityAnnouncement{
			PubKey:        pk,
			Runtime:       "metiq",
			FIPSEnabled:   true,
			FIPSTransport: "udp:2121",
			CreatedAt:     10,
			EventID:       "e1",
		})

		entries := []nip51.ListEntry{
			{Tag: "p", Value: pk, Petname: "CapFIPS"},
		}
		setNIP51ListEntries("owner1", "fleet", entries)

		fleet := fleetDirectory()
		if !fleet[0].FIPSEnabled {
			t.Fatal("expected FIPSEnabled from capability enrichment")
		}
		if fleet[0].FIPSTransport != "udp:2121" {
			t.Fatalf("expected FIPSTransport=udp:2121, got %q", fleet[0].FIPSTransport)
		}
		if fleet[0].FIPSIPv6Addr == "" {
			t.Fatal("expected FIPSIPv6Addr derived from pubkey")
		}
	})
}

// ── Merge across lists ────────────────────────────────────────────────────────

func TestRebuildFleetEntries_merges_FIPS(t *testing.T) {
	withFleetTestState(t, func() {
		pk := "384169899efb2f577db9f2995239c6ed07298d7e2fdd91ff6c86d8c384a42b3a"

		// List 1: no FIPS.
		entries1 := []nip51.ListEntry{
			{Tag: "p", Value: pk, Petname: "Agent1"},
		}
		setNIP51ListEntries("owner1", "fleet-a", entries1)

		fleet := fleetDirectory()
		if fleet[0].FIPSEnabled {
			t.Fatal("should not be FIPS-enabled from list-a")
		}

		// List 2: FIPS-enabled.
		entries2 := []nip51.ListEntry{
			{Tag: "fips", Value: "true"},
			{Tag: "fips_transport", Value: "udp:2121"},
			{Tag: "p", Value: pk},
		}
		setNIP51ListEntries("owner2", "fleet-b", entries2)

		fleet = fleetDirectory()
		if len(fleet) != 1 {
			t.Fatalf("expected 1 merged entry, got %d", len(fleet))
		}
		if !fleet[0].FIPSEnabled {
			t.Fatal("expected FIPSEnabled=true after merge from fleet-b")
		}
		if fleet[0].FIPSTransport != "udp:2121" {
			t.Fatalf("expected FIPSTransport=udp:2121, got %q", fleet[0].FIPSTransport)
		}
		// Name should be preserved from list-a (first seen).
		if fleet[0].Name != "Agent1" {
			t.Fatalf("expected Name=Agent1 from first list, got %q", fleet[0].Name)
		}
	})
}

// ── FleetEntry JSON includes FIPS fields ──────────────────────────────────────

func TestFleetEntry_FIPS_fields_json(t *testing.T) {
	entry := toolbuiltin.FleetEntry{
		Pubkey:       "abc",
		Name:         "test",
		FIPSEnabled:  true,
		FIPSIPv6Addr: "fd12:3456::",
	}
	if !entry.FIPSEnabled {
		t.Fatal("FIPSEnabled not set")
	}
	if entry.FIPSIPv6Addr != "fd12:3456::" {
		t.Fatalf("wrong FIPSIPv6Addr: %q", entry.FIPSIPv6Addr)
	}
}
