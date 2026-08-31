package nip37

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func TestDraftRoundTripAndDeletion(t *testing.T) {
	ctx := context.Background()
	signer := keyer.NewPlainKeySigner(nostr.Generate())
	draft := nostr.Event{Kind: 1, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"t", "draft"}}, Content: "hello"}
	wrap, err := BuildDraft(ctx, signer, "post-1", draft, time.Now().Add(90*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptDraft(ctx, signer, wrap)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != draft.Kind || got.Content != draft.Content || got.VerifySignature() {
		t.Fatalf("unexpected unsigned draft: %#v", got)
	}
	deleted, err := BuildDraftDeletion(ctx, signer, "post-1", draft.Kind)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptDraft(ctx, signer, deleted); err == nil {
		t.Fatal("expected deleted draft error")
	}
}

func TestPrivateRelayListRoundTrip(t *testing.T) {
	ctx := context.Background()
	signer := keyer.NewPlainKeySigner(nostr.Generate())
	event, err := BuildPrivateRelayList(ctx, signer, []string{"wss://relay.example", "wss://relay.example"})
	if err != nil {
		t.Fatal(err)
	}
	relays, err := DecryptPrivateRelayList(ctx, signer, event)
	if err != nil {
		t.Fatal(err)
	}
	if len(relays) != 1 || relays[0] != "wss://relay.example" {
		t.Fatalf("unexpected relays: %#v", relays)
	}
}
