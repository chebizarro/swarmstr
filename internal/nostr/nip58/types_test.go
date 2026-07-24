package nip58

import (
	"context"
	"fmt"
	"strings"
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

func TestBadgeDefinitionCurrentSpecOnlyRequiresDTag(t *testing.T) {
	evt, err := NewBadgeDefinitionEvent("minimal", "", "", "", "")
	if err != nil {
		t.Fatalf("minimal current-spec badge definition: %v", err)
	}
	if len(evt.Tags) != 1 || len(evt.Tags[0]) != 2 || evt.Tags[0][0] != "d" || evt.Tags[0][1] != "minimal" {
		t.Fatalf("minimal definition tags = %v", evt.Tags)
	}
	if _, err := ParseBadgeDefinition(&evt); err != nil {
		t.Fatalf("parse minimal definition: %v", err)
	}
}

func TestNewBadgeAwardEventMultiWireFormatAndRoundTrip(t *testing.T) {
	issuer := testKeyer(t)
	issuerPubKey := testPubKey(t, issuer)
	first := testPubKey(t, testKeyer(t))
	second := testPubKey(t, testKeyer(t))
	evt, err := NewBadgeAwardEventMulti(issuerPubKey, BadgeCanDeploy, "wss://definitions.example", []BadgeRecipient{
		{PubKey: first, Relay: "wss://first.example"}, {PubKey: second},
	})
	if err != nil {
		t.Fatalf("NewBadgeAwardEventMulti: %v", err)
	}
	wantAddress := fmt.Sprintf("30009:%s:%s", issuerPubKey, BadgeCanDeploy)
	want := nostr.Tags{{"a", wantAddress, "wss://definitions.example"}, {"p", first, "wss://first.example"}, {"p", second}}
	if len(evt.Tags) != len(want) {
		t.Fatalf("award tags = %v, want %v", evt.Tags, want)
	}
	for i := range want {
		if strings.Join(evt.Tags[i], "\x00") != strings.Join(want[i], "\x00") {
			t.Fatalf("tag[%d] = %v, want %v", i, evt.Tags[i], want[i])
		}
	}
	signEvent(t, issuer, &evt)
	award, err := VerifyBadgeAward(&evt)
	if err != nil {
		t.Fatalf("VerifyBadgeAward: %v", err)
	}
	if len(award.Recipients) != 2 || award.Recipients[0].Relay != "wss://first.example" {
		t.Fatalf("recipients = %+v", award.Recipients)
	}
	if !HasBadge(&evt, issuerPubKey, BadgeCanDeploy, first) || !HasBadge(&evt, issuerPubKey, BadgeCanDeploy, second) {
		t.Fatal("multi-recipient award did not grant both recipients")
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

func TestProfileBadgesAndBadgeSetEvents(t *testing.T) {
	kr := testKeyer(t)
	badge := BadgeReference{BadgeAddress: "30009:issuer:bravery", AwardEventID: "award-id", Relay: "wss://relay.example"}

	profile := NewProfileBadgesEvent([]BadgeReference{badge}, []string{"30008:user:favorites"})
	if profile.Kind != nostr.Kind(KindProfileBadges) {
		t.Fatalf("profile kind = %d, want %d", profile.Kind, KindProfileBadges)
	}
	signEvent(t, kr, &profile)
	parsedProfile, err := VerifyProfileBadges(&profile)
	if err != nil {
		t.Fatalf("VerifyProfileBadges: %v", err)
	}
	if len(parsedProfile.Badges) != 1 || parsedProfile.Badges[0] != badge {
		t.Fatalf("unexpected profile badges: %+v", parsedProfile.Badges)
	}
	if len(parsedProfile.Sets) != 1 || parsedProfile.Sets[0] != "30008:user:favorites" {
		t.Fatalf("unexpected profile sets: %+v", parsedProfile.Sets)
	}

	set, err := NewBadgeSetEvent("favorites", "Favorite Badges", []BadgeReference{badge})
	if err != nil {
		t.Fatalf("NewBadgeSetEvent: %v", err)
	}
	if set.Kind != nostr.Kind(KindBadgeSet) {
		t.Fatalf("badge set kind = %d, want %d", set.Kind, KindBadgeSet)
	}
	signEvent(t, kr, &set)
	parsedSet, err := VerifyBadgeSet(&set)
	if err != nil {
		t.Fatalf("VerifyBadgeSet: %v", err)
	}
	if parsedSet.DTag != "favorites" || parsedSet.Title != "Favorite Badges" {
		t.Fatalf("unexpected set metadata: %+v", parsedSet)
	}
	if len(parsedSet.Badges) != 1 || parsedSet.Badges[0] != badge {
		t.Fatalf("unexpected set badges: %+v", parsedSet.Badges)
	}
}
