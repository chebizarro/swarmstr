package runtime

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/events"
	"metiq/internal/testutil"
)

func TestCapabilitySoulFactoryContentRoundTrip(t *testing.T) {
	cap := CapabilityAnnouncement{
		PubKey:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Runtime: "metiq",
		Relays:  []string{"wss://relay.example"},
		SoulFactory: SoulFactoryCapability{
			Runtime:           "metiq",
			Methods:           []string{"soulfactory.resume", "soulfactory.provision", "soulfactory.provision"},
			ControllerPubKeys: []string{"BBBB", "bbbb"},
		},
	}
	content := BuildCapabilityContent(cap)
	if content == "" {
		t.Fatal("expected SoulFactory capability content")
	}
	pk, err := ParsePubKey(cap.PubKey)
	if err != nil {
		t.Fatalf("ParsePubKey: %v", err)
	}
	evt := nostr.Event{Kind: nostr.Kind(events.KindCapability), PubKey: pk, CreatedAt: nostr.Timestamp(100), Tags: BuildCapabilityTags(cap), Content: content}
	parsed, err := ParseCapabilityEvent(&evt)
	if err != nil {
		t.Fatalf("ParseCapabilityEvent: %v", err)
	}
	if parsed.SoulFactory.Schema != SoulFactoryRuntimeCapabilitySchema {
		t.Fatalf("schema = %q", parsed.SoulFactory.Schema)
	}
	if parsed.SoulFactory.ControlSchema != SoulFactoryRuntimeControlSchema {
		t.Fatalf("control schema = %q", parsed.SoulFactory.ControlSchema)
	}
	if !relaySliceEqual(parsed.SoulFactory.Methods, []string{"soulfactory.provision", "soulfactory.resume"}) {
		t.Fatalf("methods = %v", parsed.SoulFactory.Methods)
	}
	if !relaySliceEqual(parsed.SoulFactory.ControllerPubKeys, []string{"bbbb"}) {
		t.Fatalf("controller pubkeys = %v", parsed.SoulFactory.ControllerPubKeys)
	}
}

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
		PubKey:            pubkey.Hex(),
		Runtime:           "metiq",
		RuntimeVersion:    "1.2.3",
		DMSchemes:         []string{"giftwrap", "nip17", "nip44", "nip04", "nip17"},
		ACPVersion:        1,
		Tools:             []string{"web_search", "memory_search", "web_search"},
		ContextVMFeatures: []string{"tools_call", "discover", "discover"},
		Relays:            []string{"wss://b", "wss://a", "wss://a"},
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
	if !relaySliceEqual(got.ContextVMFeatures, []string{"discover", "tools_call"}) {
		t.Fatalf("ContextVMFeatures = %v", got.ContextVMFeatures)
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

	first := CapabilityAnnouncement{PubKey: "peer", Runtime: "metiq", RuntimeVersion: "1.0.0", Tools: []string{"a"}, ContextVMFeatures: []string{"discover"}, CreatedAt: 100, EventID: "a1"}
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

	sameStateNewer := CapabilityAnnouncement{PubKey: "peer", Runtime: "metiq", RuntimeVersion: "1.0.0", Tools: []string{"a"}, ContextVMFeatures: []string{"discover"}, CreatedAt: 110, EventID: "a2"}
	if !reg.Set(sameStateNewer) {
		t.Fatal("expected newer metadata update to be accepted")
	}
	if len(calls) != 1 {
		t.Fatalf("callback count = %d, want unchanged semantic count 1", len(calls))
	}

	newer := CapabilityAnnouncement{PubKey: "peer", Runtime: "metiq", RuntimeVersion: "1.1.0", Tools: []string{"a", "b"}, ContextVMFeatures: []string{"discover", "tools_list"}, CreatedAt: 120, EventID: "a3"}
	if !reg.Set(newer) {
		t.Fatal("expected newer event to be accepted")
	}
	if len(calls) != 2 {
		t.Fatalf("callback count = %d, want 2", len(calls))
	}
	stored, ok = reg.Get("peer")
	if !ok || stored.RuntimeVersion != "1.1.0" || !relaySliceEqual(stored.Tools, []string{"a", "b"}) || !relaySliceEqual(stored.ContextVMFeatures, []string{"discover", "tools_list"}) {
		t.Fatalf("stored = %+v", stored)
	}

	reordered := CapabilityAnnouncement{PubKey: "peer", Runtime: "metiq", RuntimeVersion: "1.1.0", Tools: []string{"b", "a"}, ContextVMFeatures: []string{"tools_list", "discover"}, CreatedAt: 121, EventID: "a4"}
	if !reg.Set(reordered) {
		t.Fatal("expected reordered metadata update to be accepted")
	}
	if len(calls) != 2 {
		t.Fatalf("callback count = %d, want semantic count to remain 2", len(calls))
	}
}

func TestBuildAndParseCapabilityFIPSTags(t *testing.T) {
	cap := CapabilityAnnouncement{
		PubKey:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Runtime:       "metiq",
		FIPSEnabled:   true,
		FIPSTransport: "udp:2121",
		CreatedAt:     100,
	}
	tags := BuildCapabilityTags(cap)

	// Verify FIPS tags are present.
	foundFIPS := false
	foundFIPSTransport := false
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == "fips" && tag[1] == "true" {
			foundFIPS = true
		}
		if len(tag) >= 2 && tag[0] == "fips_transport" && tag[1] == "udp:2121" {
			foundFIPSTransport = true
		}
	}
	if !foundFIPS {
		t.Fatal("expected fips=true tag")
	}
	if !foundFIPSTransport {
		t.Fatal("expected fips_transport tag")
	}

	// Parse them back.
	pk, err := ParsePubKey(cap.PubKey)
	if err != nil {
		t.Fatalf("ParsePubKey: %v", err)
	}
	evt := nostr.Event{
		Kind:      nostr.Kind(events.KindCapability),
		PubKey:    pk,
		CreatedAt: nostr.Timestamp(100),
		Tags:      tags,
	}
	parsed, err := ParseCapabilityEvent(&evt)
	if err != nil {
		t.Fatalf("ParseCapabilityEvent: %v", err)
	}
	if !parsed.FIPSEnabled {
		t.Fatal("expected FIPSEnabled=true after parse")
	}
	if parsed.FIPSTransport != "udp:2121" {
		t.Fatalf("expected FIPSTransport=udp:2121, got %q", parsed.FIPSTransport)
	}
}

func TestCapabilityFIPSNotSetByDefault(t *testing.T) {
	cap := CapabilityAnnouncement{
		PubKey:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Runtime: "metiq",
	}
	tags := BuildCapabilityTags(cap)
	for _, tag := range tags {
		if len(tag) >= 1 && (tag[0] == "fips" || tag[0] == "fips_transport") {
			t.Fatalf("FIPS tags should not be present when disabled: %v", tag)
		}
	}
}

func TestCapabilitySemanticEqual_FIPS(t *testing.T) {
	a := CapabilityAnnouncement{PubKey: "aaa", Runtime: "metiq", FIPSEnabled: true, FIPSTransport: "udp:2121"}
	b := CapabilityAnnouncement{PubKey: "aaa", Runtime: "metiq", FIPSEnabled: true, FIPSTransport: "udp:2121"}
	if !capabilitySemanticEqual(a, b) {
		t.Fatal("expected equal")
	}
	b.FIPSEnabled = false
	if capabilitySemanticEqual(a, b) {
		t.Fatal("expected not equal when FIPSEnabled differs")
	}
	b.FIPSEnabled = true
	b.FIPSTransport = "tcp:4000"
	if capabilitySemanticEqual(a, b) {
		t.Fatal("expected not equal when FIPSTransport differs")
	}
}

func TestCapabilityValidationFailureRejectsWrongAuthor(t *testing.T) {
	valid := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		nostr.Kind(events.KindCapability),
		nostr.Timestamp(10),
		nostr.Tags{{"d", canonicalCapabilityDTag(mustControlPubKey(t, testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")).Hex())}},
	)
	allowed := map[string]struct{}{valid.PubKey.Hex(): {}}
	wrongAuthor := mustSignedMetadataEvent(t,
		"2222222222222222222222222222222222222222222222222222222222222222",
		nostr.Kind(events.KindCapability),
		nostr.Timestamp(20),
		nostr.Tags{{"d", canonicalCapabilityDTag("2222222222222222222222222222222222222222222222222222222222222222")}},
	)
	if reason := capabilityValidationFailure(wrongAuthor, allowed); reason != "unexpected_author" {
		t.Fatalf("reason = %q, want unexpected_author", reason)
	}
}

func TestCapabilityValidationFailureRejectsInvalidSignature(t *testing.T) {
	valid := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		nostr.Kind(events.KindCapability),
		nostr.Timestamp(10),
		nostr.Tags{{"d", "cap"}},
	)
	allowed := map[string]struct{}{valid.PubKey.Hex(): {}}
	invalidSig := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		nostr.Kind(events.KindCapability),
		nostr.Timestamp(20),
		nostr.Tags{{"d", "cap"}},
	)
	invalidSig.Sig[0] ^= 0x01
	if reason := capabilityValidationFailure(invalidSig, allowed); reason != "invalid_signature" {
		t.Fatalf("reason = %q, want invalid_signature", reason)
	}
}

func TestCapabilityMonitorStartIsIdempotent(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)
	published := make(chan string, 2)

	mon := NewCapabilityMonitor(CapabilityMonitorOptions{
		Pool:          pool,
		Keyer:         kp.Keyer,
		PublishRelays: []string{url},
		Local: CapabilityAnnouncement{
			Runtime: "metiq",
		},
		OnPublished: func(eventID string) {
			published <- eventID
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	mon.Start(ctx)
	mon.Start(ctx)

	select {
	case eventID := <-published:
		if eventID == "" {
			t.Fatal("expected non-empty published event id")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for initial capability publish")
	}

	select {
	case eventID := <-published:
		t.Fatalf("unexpected extra publish after double start: %s", eventID)
	case <-time.After(200 * time.Millisecond):
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
