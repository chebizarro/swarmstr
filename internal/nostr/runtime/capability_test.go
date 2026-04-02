package runtime

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/events"
)

func TestBuildAndParseCapabilityEventRoundTrip(t *testing.T) {
	sk, err := ParseSecretKey("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("ParseSecretKey: %v", err)
	}
	keyer := newNIP04KeyerAdapter(sk)
	pubkey, err := keyer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	want := CapabilityAnnouncement{
		PubKey:         pubkey.Hex(),
		Runtime:        "metiq",
		RuntimeVersion: "1.2.3",
		DMSchemes:      []string{"giftwrap", "nip17", "nip44", "nip04", "nip17"},
		ACPVersion:     1,
		Tools:          []string{"web_search", "memory_search", "web_search"},
		Relays:         []string{"wss://b", "wss://a", "wss://a"},
	}
	evt := nostr.Event{
		Kind:      nostr.Kind(events.KindCapability),
		CreatedAt: nostr.Timestamp(1234),
		Tags:      BuildCapabilityTags(want),
		Content:   "",
	}
	if err := keyer.SignEvent(context.Background(), &evt); err != nil {
		t.Fatalf("SignEvent: %v", err)
	}
	got, err := ParseCapabilityEvent(&evt)
	if err != nil {
		t.Fatalf("ParseCapabilityEvent: %v", err)
	}
	if got.PubKey != pubkey.Hex() {
		t.Fatalf("PubKey = %q, want %q", got.PubKey, pubkey.Hex())
	}
	if got.DTag != pubkey.Hex() {
		t.Fatalf("DTag = %q, want %q", got.DTag, pubkey.Hex())
	}
	if got.Runtime != "metiq" || got.RuntimeVersion != "1.2.3" {
		t.Fatalf("runtime = %s %s", got.Runtime, got.RuntimeVersion)
	}
	if got.ACPVersion != 1 {
		t.Fatalf("ACPVersion = %d, want 1", got.ACPVersion)
	}
	if !relaySliceEqual(got.DMSchemes, []string{"giftwrap", "nip04", "nip17", "nip44"}) {
		t.Fatalf("DMSchemes = %v", got.DMSchemes)
	}
	if !relaySliceEqual(got.Tools, []string{"memory_search", "web_search"}) {
		t.Fatalf("Tools = %v", got.Tools)
	}
	if !relaySliceEqual(got.Relays, []string{"wss://b", "wss://a"}) {
		t.Fatalf("Relays = %v", got.Relays)
	}
}

func TestCapabilityRegistryPrefersNewestEvent(t *testing.T) {
	reg := NewCapabilityRegistry()
	var calls []CapabilityAnnouncement
	reg.OnChange(func(_ string, cap CapabilityAnnouncement) {
		calls = append(calls, cap)
	})

	first := CapabilityAnnouncement{PubKey: "peer", Runtime: "metiq", RuntimeVersion: "1.0.0", Tools: []string{"a"}, CreatedAt: 100, EventID: "a1"}
	if !reg.Set(first) {
		t.Fatal("expected first set to be accepted")
	}
	if len(calls) != 1 {
		t.Fatalf("callback count = %d, want 1", len(calls))
	}

	older := CapabilityAnnouncement{PubKey: "peer", Runtime: "metiq", RuntimeVersion: "0.9.0", Tools: []string{"old"}, CreatedAt: 90, EventID: "a0"}
	if reg.Set(older) {
		t.Fatal("expected older event to be ignored")
	}
	stored, ok := reg.Get("peer")
	if !ok || stored.RuntimeVersion != "1.0.0" {
		t.Fatalf("stored version = %+v", stored)
	}

	sameStateNewer := CapabilityAnnouncement{PubKey: "peer", Runtime: "metiq", RuntimeVersion: "1.0.0", Tools: []string{"a"}, CreatedAt: 110, EventID: "a2"}
	if !reg.Set(sameStateNewer) {
		t.Fatal("expected newer metadata update to be accepted")
	}
	if len(calls) != 1 {
		t.Fatalf("callback count = %d, want unchanged semantic count 1", len(calls))
	}

	newer := CapabilityAnnouncement{PubKey: "peer", Runtime: "metiq", RuntimeVersion: "1.1.0", Tools: []string{"a", "b"}, CreatedAt: 120, EventID: "a3"}
	if !reg.Set(newer) {
		t.Fatal("expected newer event to be accepted")
	}
	if len(calls) != 2 {
		t.Fatalf("callback count = %d, want 2", len(calls))
	}
	stored, ok = reg.Get("peer")
	if !ok || stored.RuntimeVersion != "1.1.0" || !relaySliceEqual(stored.Tools, []string{"a", "b"}) {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestCapabilityMonitorSnapshotUsesCanonicalDTags(t *testing.T) {
	sk1, err := ParseSecretKey("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("ParseSecretKey(1): %v", err)
	}
	sk2, err := ParseSecretKey("2222222222222222222222222222222222222222222222222222222222222222")
	if err != nil {
		t.Fatalf("ParseSecretKey(2): %v", err)
	}
	pk1, err := newNIP04KeyerAdapter(sk1).GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("GetPublicKey(1): %v", err)
	}
	pk2, err := newNIP04KeyerAdapter(sk2).GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("GetPublicKey(2): %v", err)
	}

	mon := NewCapabilityMonitor(CapabilityMonitorOptions{
		SubscribeRelays: []string{"wss://relay-b", "wss://relay-a", "wss://relay-a"},
		Peers:           []string{pk1.Hex(), pk2.Hex()},
	})
	relays, authors, dTags := mon.snapshotSubscriptionConfig()
	if !relaySliceEqual(relays, []string{"wss://relay-b", "wss://relay-a"}) {
		t.Fatalf("relays = %v", relays)
	}
	if len(authors) != 2 {
		t.Fatalf("authors len = %d, want 2", len(authors))
	}
	if len(dTags) != 2 {
		t.Fatalf("dTags len = %d, want 2", len(dTags))
	}
	for i, author := range authors {
		if dTags[i] != canonicalCapabilityDTag(author.Hex()) {
			t.Fatalf("dTags[%d] = %q, want canonical %q", i, dTags[i], canonicalCapabilityDTag(author.Hex()))
		}
	}
}
