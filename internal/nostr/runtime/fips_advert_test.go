package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

func testFIPSAdvert() FIPSOverlayAdvert {
	return FIPSOverlayAdvert{
		Identifier: FIPSOverlayAdvertIdentifier,
		Version:    FIPSOverlayAdvertVersion,
		Endpoints: []FIPSOverlayEndpointAdvert{
			{Transport: FIPSOverlayTransportUDP, Addr: "8.8.8.8:2121"},
		},
	}
}

func signedFIPSAdvertEvent(t *testing.T, advert FIPSOverlayAdvert, protocol string, createdAt, expiresAt int64) nostr.Event {
	t.Helper()
	sk, err := ParseSecretKey("2222222222222222222222222222222222222222222222222222222222222222")
	if err != nil {
		t.Fatalf("ParseSecretKey: %v", err)
	}
	keyer := newNIP04KeyerAdapter(sk)
	content, err := BuildFIPSAdvertContent(advert)
	if err != nil {
		t.Fatalf("BuildFIPSAdvertContent: %v", err)
	}
	tags, err := BuildFIPSAdvertTags(protocol, expiresAt)
	if err != nil {
		t.Fatalf("BuildFIPSAdvertTags: %v", err)
	}
	evt := nostr.Event{
		Kind:      nostr.Kind(FIPSOverlayAdvertKind),
		CreatedAt: nostr.Timestamp(createdAt),
		Tags:      tags,
		Content:   content,
	}
	if err := keyer.SignEvent(context.Background(), &evt); err != nil {
		t.Fatalf("SignEvent: %v", err)
	}
	return evt
}

func TestFIPSAdvertCodecMatchesV1Schema(t *testing.T) {
	now := time.Unix(1_000, 0)
	evt := signedFIPSAdvertEvent(t, testFIPSAdvert(), FIPSOverlayAdvertIdentifier, 900, 1_500)
	got, err := ParseFIPSAdvertEvent(&evt, FIPSOverlayAdvertIdentifier, now)
	if err != nil {
		t.Fatalf("ParseFIPSAdvertEvent: %v", err)
	}
	if got.Protocol != FIPSOverlayAdvertIdentifier || got.ExpiresAt != 1_500 {
		t.Fatalf("announcement metadata = %+v", got)
	}
	if got.Advert.Identifier != FIPSOverlayAdvertIdentifier || got.Advert.Version != 1 || len(got.Advert.Endpoints) != 1 {
		t.Fatalf("advert = %+v", got.Advert)
	}
	if reason := fipsAdvertValidationFailure(evt, map[string]struct{}{evt.PubKey.Hex(): {}}, now); reason != "" {
		t.Fatalf("validation failure = %q", reason)
	}
}

func TestValidateFIPSOverlayAdvertNATRequirements(t *testing.T) {
	advert := FIPSOverlayAdvert{
		Identifier: FIPSOverlayAdvertIdentifier,
		Version:    FIPSOverlayAdvertVersion,
		Endpoints:  []FIPSOverlayEndpointAdvert{{Transport: FIPSOverlayTransportUDP, Addr: "NAT"}},
	}
	if _, err := ValidateFIPSOverlayAdvert(advert); err == nil || !strings.Contains(err.Error(), "signalRelays") {
		t.Fatalf("missing relay error = %v", err)
	}
	advert.SignalRelays = []string{"wss://relay.example"}
	if _, err := ValidateFIPSOverlayAdvert(advert); err == nil || !strings.Contains(err.Error(), "stunServers") {
		t.Fatalf("missing STUN error = %v", err)
	}
	advert.STUNServers = []string{"stun:stun.example:3478"}
	got, err := ValidateFIPSOverlayAdvert(advert)
	if err != nil {
		t.Fatalf("ValidateFIPSOverlayAdvert: %v", err)
	}
	if got.Endpoints[0].Addr != "nat" {
		t.Fatalf("NAT endpoint not normalized: %+v", got.Endpoints)
	}
}

func TestValidateFIPSOverlayAdvertFiltersUnroutableEndpoints(t *testing.T) {
	advert := testFIPSAdvert()
	advert.Endpoints = append([]FIPSOverlayEndpointAdvert{
		{Transport: FIPSOverlayTransportTCP, Addr: "127.0.0.1:9000"},
		{Transport: FIPSOverlayTransportUDP, Addr: "10.0.0.1:2121"},
	}, advert.Endpoints...)
	advert.SignalRelays = []string{"wss://unused.example"}
	advert.STUNServers = []string{"stun:unused.example:3478"}
	got, err := ValidateFIPSOverlayAdvert(advert)
	if err != nil {
		t.Fatalf("ValidateFIPSOverlayAdvert: %v", err)
	}
	if len(got.Endpoints) != 1 || got.Endpoints[0].Addr != "8.8.8.8:2121" {
		t.Fatalf("filtered endpoints = %+v", got.Endpoints)
	}
	if got.SignalRelays != nil || got.STUNServers != nil {
		t.Fatalf("non-NAT metadata must be omitted: %+v", got)
	}
}

func TestParseFIPSAdvertEventRejectsMalformedTagsAndTrailingJSON(t *testing.T) {
	evt := signedFIPSAdvertEvent(t, testFIPSAdvert(), FIPSOverlayAdvertIdentifier, 900, 1_500)
	evt.Tags = append(evt.Tags, nostr.Tag{"d", FIPSOverlayAdvertIdentifier})
	if _, err := ParseFIPSAdvertEvent(&evt, FIPSOverlayAdvertIdentifier, time.Unix(1_000, 0)); err == nil {
		t.Fatal("expected duplicate d tag rejection")
	}

	evt = signedFIPSAdvertEvent(t, testFIPSAdvert(), FIPSOverlayAdvertIdentifier, 900, 1_500)
	evt.Content += " {}"
	if _, err := ParseFIPSAdvertEvent(&evt, FIPSOverlayAdvertIdentifier, time.Unix(1_000, 0)); err == nil {
		t.Fatal("expected trailing JSON rejection")
	}

	evt = signedFIPSAdvertEvent(t, testFIPSAdvert(), "other-protocol", 900, 1_500)
	if _, err := ParseFIPSAdvertEvent(&evt, FIPSOverlayAdvertIdentifier, time.Unix(1_000, 0)); err == nil {
		t.Fatal("expected protocol mismatch rejection")
	}
}

func TestCapabilityRegistryMergesIndependentFIPSAdvertStream(t *testing.T) {
	reg := NewCapabilityRegistry()
	base := CapabilityAnnouncement{
		PubKey:        "peer",
		Runtime:       "metiq",
		DMSchemes:     []string{"nip17"},
		FIPSEnabled:   true,
		FIPSTransport: "udp:2121",
		CreatedAt:     10,
		EventID:       "base-1",
	}
	if !reg.Set(base) {
		t.Fatal("base capability rejected")
	}
	now := time.Now().Unix()
	announcement := FIPSAdvertAnnouncement{
		PubKey:    "peer",
		Protocol:  FIPSOverlayAdvertIdentifier,
		Advert:    testFIPSAdvert(),
		EventID:   "fips-1",
		CreatedAt: now,
		ExpiresAt: now + 60,
	}
	if !reg.SetFIPSAdvert(announcement) {
		t.Fatal("FIPS advert rejected")
	}
	if !reg.Set(CapabilityAnnouncement{PubKey: "peer", Runtime: "metiq", DMSchemes: []string{"nip17"}, CreatedAt: 20, EventID: "base-2"}) {
		t.Fatal("newer base capability rejected")
	}
	got, ok := reg.Get("peer")
	if !ok || !got.FIPSEnabled || got.FIPSAdvert == nil {
		t.Fatalf("merged capability = %+v, ok=%v", got, ok)
	}
	if got.FIPSTransport != "" || got.FIPSProtocol != FIPSOverlayAdvertIdentifier {
		t.Fatalf("legacy/structured projection = transport:%q protocol:%q", got.FIPSTransport, got.FIPSProtocol)
	}
	if !relaySliceEqual(got.DMSchemes, []string{"fips", "nip17"}) {
		t.Fatalf("DM schemes = %v", got.DMSchemes)
	}
	announcement.CreatedAt--
	announcement.EventID = "fips-old"
	if reg.SetFIPSAdvert(announcement) {
		t.Fatal("older FIPS advert must be rejected independently")
	}
}

func TestCapabilityRegistryFIPSFirstArrivalAndExpirationFallback(t *testing.T) {
	reg := NewCapabilityRegistry()
	var callbacks []CapabilityAnnouncement
	reg.OnChange(func(_ string, cap CapabilityAnnouncement) { callbacks = append(callbacks, cap) })
	now := time.Now().Unix()
	announcement := FIPSAdvertAnnouncement{
		PubKey:    "peer",
		Protocol:  FIPSOverlayAdvertIdentifier,
		Advert:    testFIPSAdvert(),
		EventID:   "fips-1",
		CreatedAt: now,
		ExpiresAt: now + 60,
	}
	if !reg.SetFIPSAdvert(announcement) {
		t.Fatal("FIPS-first advert rejected")
	}
	if _, ok := reg.Get("peer"); ok {
		t.Fatal("base-less advert must not create a capability entry")
	}
	if !reg.Set(CapabilityAnnouncement{PubKey: "peer", Runtime: "metiq", FIPSEnabled: true, FIPSTransport: "udp:2121", CreatedAt: 10, EventID: "base"}) {
		t.Fatal("base capability rejected")
	}
	if len(callbacks) != 1 || callbacks[0].FIPSAdvert == nil {
		t.Fatalf("callbacks after base arrival = %+v", callbacks)
	}
	if changed := reg.PruneExpiredFIPS(time.Unix(now+61, 0)); changed != 1 {
		t.Fatalf("expired changes = %d, want 1", changed)
	}
	got, ok := reg.Get("peer")
	if !ok || got.FIPSAdvert != nil || !got.FIPSEnabled || got.FIPSTransport != "udp:2121" {
		t.Fatalf("legacy fallback after expiry = %+v, ok=%v", got, ok)
	}
	if len(callbacks) != 2 || callbacks[1].FIPSAdvert != nil {
		t.Fatalf("expiration callbacks = %+v", callbacks)
	}
}

func TestCapabilityRegistryExpiredAdvertRetainsHighWaterMark(t *testing.T) {
	reg := NewCapabilityRegistry()
	reg.Set(CapabilityAnnouncement{PubKey: "peer", Runtime: "metiq", CreatedAt: 1, EventID: "base"})
	now := time.Now().Unix()
	newerExpired := FIPSAdvertAnnouncement{
		PubKey: "peer", Protocol: FIPSOverlayAdvertIdentifier, Advert: testFIPSAdvert(),
		EventID: "newer", CreatedAt: now, ExpiresAt: now - 1,
	}
	if !reg.SetFIPSAdvert(newerExpired) {
		t.Fatal("expired replacement should advance high-water mark")
	}
	olderActive := newerExpired
	olderActive.EventID = "older"
	olderActive.CreatedAt = now - 1
	olderActive.ExpiresAt = now + 60
	if reg.SetFIPSAdvert(olderActive) {
		t.Fatal("older active advert must not revive an expired replaceable slot")
	}
}
