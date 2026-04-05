package toolbuiltin

import (
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
)

func TestSetIdentityInfoDerivesNPubForMyIdentity(t *testing.T) {
	prev := identityInfo
	defer SetIdentityInfo(prev)

	var sk [32]byte
	sk[31] = 1
	pubkey := nostr.GetPublicKey(sk).Hex()
	SetIdentityInfo(IdentityInfo{
		Name:   "wizard",
		Pubkey: pubkey,
		Model:  "gpt-test",
	})

	out, err := MyIdentityTool(nil, nil)
	if err != nil {
		t.Fatalf("MyIdentityTool returned error: %v", err)
	}
	if !strings.Contains(out, "name: wizard") {
		t.Fatalf("expected identity output to include name, got %q", out)
	}
	if !strings.Contains(out, "nostr_pubkey: "+pubkey) {
		t.Fatalf("expected identity output to include hex pubkey, got %q", out)
	}
	wantNPub := NostrNPubFromHex(pubkey)
	if wantNPub == "" {
		t.Fatal("expected test pubkey to encode to npub")
	}
	if !strings.Contains(out, "nostr_npub: "+wantNPub) {
		t.Fatalf("expected identity output to include npub, got %q", out)
	}
	if !strings.Contains(out, "model: gpt-test") {
		t.Fatalf("expected identity output to include model, got %q", out)
	}
}

func TestNostrNPubFromHexRejectsInvalidHex(t *testing.T) {
	if got := NostrNPubFromHex("not-a-pubkey"); got != "" {
		t.Fatalf("expected invalid hex pubkey to return empty npub, got %q", got)
	}
}
