package nip58

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func testKeyer(t *testing.T) nostr.Keyer {
	t.Helper()
	sk := nostr.Generate()
	kr := keyer.NewPlainKeySigner(sk)
	return &kr
}

func testPubKey(t *testing.T, kr nostr.Keyer) string {
	t.Helper()
	pk, err := kr.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	return pk.Hex()
}

func signEvent(t *testing.T, kr nostr.Keyer, ev *nostr.Event) {
	t.Helper()
	if err := kr.SignEvent(context.Background(), ev); err != nil {
		t.Fatalf("SignEvent: %v", err)
	}
}

func TestKindConstants(t *testing.T) {
	if KindBadgeDefinition != 30009 {
		t.Fatalf("KindBadgeDefinition = %d, want 30009", KindBadgeDefinition)
	}
	if KindBadgeAward != 8 {
		t.Fatalf("KindBadgeAward = %d, want 8", KindBadgeAward)
	}
}

func TestCascadiaBadgeConstants(t *testing.T) {
	want := []string{
		"cascadia:can-deploy",
		"cascadia:can-deploy:production",
		"cascadia:can-approve",
		"cascadia:can-merge",
		"cascadia:can-sign",
		"cascadia:can-operate",
		"cascadia:can-admin",
		"cascadia:trust:verified",
		"cascadia:trust:audited",
		"cascadia:trust:compliant",
		"cascadia:cert:security-review",
		"cascadia:cert:sbom-attested",
		"cascadia:cert:provenance",
	}
	if len(CascadiaBadges) != len(want) {
		t.Fatalf("CascadiaBadges length = %d, want %d", len(CascadiaBadges), len(want))
	}
	for _, badge := range want {
		if !IsCascadiaBadge(badge) {
			t.Fatalf("IsCascadiaBadge(%q) = false", badge)
		}
	}
	if IsCascadiaBadge("cascadia:unknown") {
		t.Fatal("unexpected unknown Cascadia badge")
	}
}

func TestNewBadgeDefinitionEventAndVerify(t *testing.T) {
	kr := testKeyer(t)
	ev, err := NewBadgeDefinitionEvent(BadgeCanDeploy, "Can deploy", "May deploy services", "https://example.com/badge.png", "https://example.com/thumb.png")
	if err != nil {
		t.Fatalf("NewBadgeDefinitionEvent: %v", err)
	}
	if ev.Kind != nostr.Kind(KindBadgeDefinition) {
		t.Fatalf("kind = %d, want %d", ev.Kind, KindBadgeDefinition)
	}
	signEvent(t, kr, &ev)

	def, err := VerifyBadgeDefinition(&ev)
	if err != nil {
		t.Fatalf("VerifyBadgeDefinition: %v", err)
	}
	if def.DTag != BadgeCanDeploy || def.Name != "Can deploy" || def.Description != "May deploy services" {
		t.Fatalf("unexpected definition: %+v", def)
	}
	if def.Image != "https://example.com/badge.png" || def.Thumb != "https://example.com/thumb.png" {
		t.Fatalf("unexpected image fields: %+v", def)
	}
	if def.PubKey != testPubKey(t, kr) {
		t.Fatalf("definition pubkey = %q", def.PubKey)
	}
}

func TestNewBadgeAwardEventVerifyAndHasBadge(t *testing.T) {
	issuer := testKeyer(t)
	recipient := testPubKey(t, testKeyer(t))
	issuerPubKey := testPubKey(t, issuer)

	ev, err := NewBadgeAwardEvent(issuerPubKey, BadgeCanDeployProduction, recipient, "wss://relay.example")
	if err != nil {
		t.Fatalf("NewBadgeAwardEvent: %v", err)
	}
	signEvent(t, issuer, &ev)

	award, err := VerifyBadgeAward(&ev)
	if err != nil {
		t.Fatalf("VerifyBadgeAward: %v", err)
	}
	wantAddress := "30009:" + issuerPubKey + ":" + BadgeCanDeployProduction
	if award.BadgeAddress != wantAddress {
		t.Fatalf("BadgeAddress = %q, want %q", award.BadgeAddress, wantAddress)
	}
	if award.Recipient != recipient {
		t.Fatalf("Recipient = %q, want %q", award.Recipient, recipient)
	}
	if award.Relay != "wss://relay.example" {
		t.Fatalf("Relay = %q", award.Relay)
	}
	if !HasBadge(&ev, issuerPubKey, BadgeCanDeployProduction, recipient) {
		t.Fatal("HasBadge returned false for matching award")
	}
	if HasBadge(&ev, issuerPubKey, BadgeCanAdmin, recipient) {
		t.Fatal("HasBadge returned true for wrong badge")
	}
}

func TestBuilderValidation(t *testing.T) {
	if _, err := NewBadgeDefinitionEvent("", "", "", "", ""); err == nil {
		t.Fatal("expected error for empty d-tag")
	}
	if _, err := NewBadgeAwardEvent("", BadgeCanDeploy, "recipient", ""); err == nil {
		t.Fatal("expected error for empty definition author")
	}
	if _, err := NewBadgeAwardEvent("author", "", "recipient", ""); err == nil {
		t.Fatal("expected error for empty badge d-tag")
	}
	if _, err := NewBadgeAwardEvent("author", BadgeCanDeploy, "", ""); err == nil {
		t.Fatal("expected error for empty recipient")
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	kr := testKeyer(t)
	ev, err := NewBadgeDefinitionEvent(BadgeCanApprove, "Can approve", "", "", "")
	if err != nil {
		t.Fatalf("NewBadgeDefinitionEvent: %v", err)
	}
	signEvent(t, kr, &ev)
	ev.Tags = append(ev.Tags, nostr.Tag{"description", "tampered"})
	if _, err := VerifyBadgeDefinition(&ev); err == nil {
		t.Fatal("expected tampered event to fail verification")
	}
}
