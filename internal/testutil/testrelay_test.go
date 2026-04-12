package testutil

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

func TestNewTestRelay_ConnectAndPublish(t *testing.T) {
	url := NewTestRelay(t)
	if url == "" {
		t.Fatal("expected non-empty URL")
	}

	// Connect a client
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	rl, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer rl.Close()

	// Publish an event
	kp := NewTestKeyPair(t)
	evt := nostr.Event{
		Kind:      nostr.KindTextNote,
		Content:   "test note from testrelay_test",
		CreatedAt: nostr.Now(),
	}
	kp.SignEvent(t, &evt)

	err = rl.Publish(ctx, evt)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Query it back
	sub, err := rl.Subscribe(ctx, nostr.Filter{
		IDs: []nostr.ID{evt.ID},
	}, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsub()

	select {
	case got := <-sub.Events:
		if got.ID != evt.ID {
			t.Errorf("got event %v, want %v", got.ID, evt.ID)
		}
		if got.Content != "test note from testrelay_test" {
			t.Errorf("content: %q", got.Content)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for event")
	}
}

func TestNewTestRelay_TwoClients(t *testing.T) {
	url := NewTestRelay(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	client1 := MustRelayConnect(t, url)
	client2 := MustRelayConnect(t, url)

	// Client 2 subscribes
	kp := NewTestKeyPair(t)
	sub, err := client2.Subscribe(ctx, nostr.Filter{
		Authors: []nostr.PubKey{kp.PublicKey},
		Kinds:   []nostr.Kind{nostr.KindTextNote},
	}, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsub()

	// Client 1 publishes
	evt := nostr.Event{
		Kind:      nostr.KindTextNote,
		Content:   "hello from client1",
		CreatedAt: nostr.Now(),
	}
	kp.SignEvent(t, &evt)

	err = client1.Publish(ctx, evt)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Client 2 should receive it
	select {
	case got := <-sub.Events:
		if got.ID != evt.ID {
			t.Errorf("got %v, want %v", got.ID, evt.ID)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for cross-client event")
	}
}

func TestNewTestKeyPair(t *testing.T) {
	kp := NewTestKeyPair(t)

	if kp.SecretKeyHex() == "" {
		t.Error("empty secret key hex")
	}
	if kp.PubKeyHex() == "" {
		t.Error("empty public key hex")
	}
	// keyer.KeySigner is a struct, not a pointer — just verify PubKeyHex works
	_ = kp.Keyer

	// Two key pairs should differ
	kp2 := NewTestKeyPair(t)
	if kp.SecretKeyHex() == kp2.SecretKeyHex() {
		t.Error("two key pairs should be different")
	}
}

func TestMustRelayConnect(t *testing.T) {
	url := NewTestRelay(t)
	rl := MustRelayConnect(t, url)
	if rl == nil {
		t.Fatal("expected non-nil relay")
	}
}
