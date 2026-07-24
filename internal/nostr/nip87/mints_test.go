package nip87

import (
	"context"
	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"testing"
)

func TestAnnouncementAndRecommendationWire(t *testing.T) {
	s := keyer.NewPlainKeySigner(nostr.Generate())
	mint := nostr.Generate().Public()
	a, err := BuildAnnouncement(context.Background(), s, KindCashuMint, "mint-key", []string{"https://mint.example"}, []string{"1", "2"}, "mainnet", map[string]any{"name": "Mint"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseAnnouncement(a)
	if err != nil || parsed.Identifier != "mint-key" || a.Tags[2][0] != "nuts" {
		t.Fatalf("announcement: %#v %v", parsed, err)
	}
	r, err := BuildRecommendation(context.Background(), s, KindCashuMint, "mint-key", mint, "wss://relay.example", []string{"https://mint.example"}, "trusted")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ParseRecommendation(r)
	if err != nil || rec.MintKind != KindCashuMint || rec.Address == "" {
		t.Fatalf("recommendation: %#v %v", rec, err)
	}
}
func TestLatestParameterizedReplacement(t *testing.T) {
	sk := nostr.Generate()
	s := keyer.NewPlainKeySigner(sk)
	mint := nostr.Generate().Public()
	old, _ := BuildRecommendation(context.Background(), s, KindFedimint, "fed", mint, "", nil, "old")
	old.CreatedAt = 10
	_ = old.Sign(sk)
	newer, _ := BuildRecommendation(context.Background(), s, KindFedimint, "fed", mint, "", nil, "new")
	newer.CreatedAt = 11
	_ = newer.Sign(sk)
	got := LatestRecommendations([]nostr.Event{newer, old})
	if len(got) != 1 {
		t.Fatal(got)
	}
	for _, r := range got {
		if r.Review != "new" {
			t.Fatal(r.Review)
		}
	}
}
