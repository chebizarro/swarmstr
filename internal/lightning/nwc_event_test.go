package lightning

import (
	"context"
	"os"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
)

func TestPinnedNostrVersionKeepsCheckptrWorkaroundUnderReview(t *testing.T) {
	const unsafePinnedVersion = "v0.0.0-20260902034142-316ef6591fa2"
	goMod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	pinnedLine := "fiatjaf.com/nostr " + unsafePinnedVersion
	if !strings.Contains(string(goMod), pinnedLine) {
		t.Fatalf("fiatjaf.com/nostr changed from audited unsafe version %s; re-audit event.go under Go race/checkptr, then remove or update the local NWC serializer workaround", unsafePinnedVersion)
	}
}

func TestNWCEventSigningMatchesUpstreamSerialization(t *testing.T) {
	keyer, err := NewNWCKeyer("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("NewNWCKeyer: %v", err)
	}
	event := nostr.Event{
		CreatedAt: 1234567890,
		Kind:      nostr.Kind(KindNWCRequest),
		Tags: nostr.Tags{
			{"p", "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"},
			{"escaped", "quote\"slash\\line\ncarriage\rtab\t"},
		},
		Content: "ciphertext <>& payload",
	}
	if err := keyer.SignEvent(context.Background(), &event); err != nil {
		t.Fatalf("SignEvent: %v", err)
	}

	const expectedPubKey = "4f355bdcb7cc0af728ef3cceb9615d90684bb5b2ca5f859ab0f0b704075871aa"
	const expectedID = "40fd9c39718b1e0f4154330e94c0b853580879051d8bb4ac7af75d399c55de7a"
	if event.PubKey.Hex() != expectedPubKey {
		t.Fatalf("public key = %s, want %s", event.PubKey.Hex(), expectedPubKey)
	}
	if event.ID.Hex() != expectedID {
		t.Fatalf("event ID = %s, want upstream-compatible %s", event.ID.Hex(), expectedID)
	}
	if !validSignedNWCEvent(event) {
		t.Fatal("checkptr-safe event signature did not verify")
	}

	event.Content += "-tampered"
	if validSignedNWCEvent(event) {
		t.Fatal("tampered event passed signature verification")
	}
}
