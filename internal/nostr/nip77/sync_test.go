package nip77

import (
	"context"
	"iter"
	"net/http/httptest"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/slicestore"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/khatru"
)

type testEventStore struct{ events map[nostr.ID]nostr.Event }

func (s *testEventStore) QueryEvents(filter nostr.Filter) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		for _, event := range s.events {
			if filter.Matches(event) && !yield(event) {
				return
			}
		}
	}
}

func (s *testEventStore) Publish(_ context.Context, event nostr.Event) error {
	if !event.CheckID() || !event.VerifySignature() {
		return context.Canceled
	}
	s.events[event.ID] = event
	return nil
}

func TestSyncReconcilesAndTransfersEvents(t *testing.T) {
	relay := khatru.NewRelay()
	relay.Negentropy = true
	remoteStore := &slicestore.SliceStore{}
	if err := remoteStore.Init(); err != nil {
		t.Fatal(err)
	}
	relay.UseEventstore(remoteStore, 400)
	server := httptest.NewServer(relay)
	defer server.Close()
	relayURL := "ws" + server.URL[4:]

	signer := keyer.NewPlainKeySigner(nostr.Generate())
	makeEvent := func(content string) nostr.Event {
		event := nostr.Event{Kind: 30078, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"d", content}}, Content: content}
		if err := signer.SignEvent(context.Background(), &event); err != nil {
			t.Fatal(err)
		}
		return event
	}
	localEvent := makeEvent("local")
	remoteEvent := makeEvent("remote")
	if err := remoteStore.SaveEvent(remoteEvent); err != nil {
		t.Fatal(err)
	}
	local := &testEventStore{events: map[nostr.ID]nostr.Event{localEvent.ID: localEvent}}
	result, err := Sync(context.Background(), relayURL, nostr.Filter{Kinds: []nostr.Kind{30078}}, local, local, SyncOptions{SubscriptionID: "test-sync"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Uploaded != 1 || result.Downloaded != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, ok := local.events[remoteEvent.ID]; !ok {
		t.Fatal("remote event was not downloaded")
	}
	foundUploaded := false
	for event := range remoteStore.QueryEvents(nostr.Filter{IDs: []nostr.ID{localEvent.ID}}, 1) {
		foundUploaded = event.ID == localEvent.ID
	}
	if !foundUploaded {
		t.Fatal("local event was not uploaded")
	}
}

func TestSyncRejectsUnscopedFilter(t *testing.T) {
	store := &testEventStore{events: map[nostr.ID]nostr.Event{}}
	if _, err := Sync(context.Background(), "ws://unused", nostr.Filter{}, store, store, SyncOptions{}); err == nil {
		t.Fatal("unscoped filter accepted")
	}
}
