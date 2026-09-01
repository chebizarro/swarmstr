package main

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"

	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func TestNostrPublicationProvenancePersistsOnTranscriptEntries(t *testing.T) {
	ctx := context.Background()
	backing := newTestStore()
	docs := state.NewDocsRepository(backing, "nostr-provenance")
	transcripts := state.NewTranscriptRepository(backing, "nostr-provenance")
	if _, err := docs.PutSession(ctx, "session", state.SessionDoc{Version: 1, SessionID: "session", PeerPubKey: "peer"}); err != nil {
		t.Fatal(err)
	}

	inbound := nostruntime.InboundDM{
		EventID:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		FromPubKey: "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
		Text:       "hello", RelayURL: "wss://relay.example", CreatedAt: 10,
		Scheme: "nip17", Kind: nostr.KindDirectMessage,
		Recipients: []string{"c6047f9441ed7d6d3045406e95c07cd85a67fb26f5b601198906a586efb9f9e"},
	}
	if err := persistInbound(ctx, docs, transcripts, "session", inbound); err != nil {
		t.Fatal(err)
	}
	entry, err := transcripts.GetEntry(ctx, "session", inbound.EventID)
	if err != nil {
		t.Fatal(err)
	}
	storedKind, kindErr := metaInt(entry.Meta["nostr_kind"])
	if kindErr != nil || entry.Meta["nostr_event_id"] != inbound.EventID || entry.Meta["nostr_transport"] != "nip17" || storedKind != int(nostr.KindDirectMessage) {
		t.Fatalf("inbound provenance missing: %+v", entry.Meta)
	}

	assistantEntryID, err := persistAssistant(ctx, docs, transcripts, "session", "reply", inbound.EventID)
	if err != nil {
		t.Fatal(err)
	}
	publication := nostrPublication{
		EventID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Relays:  []string{"wss://relay.example"}, Kind: int(nostr.KindDirectMessage),
		PubKey: inbound.FromPubKey, Transport: "nip17", Recipients: inbound.Recipients,
	}
	if err := attachNostrPublication(ctx, transcripts, "session", assistantEntryID, publication); err != nil {
		t.Fatal(err)
	}
	assistant, err := transcripts.GetEntry(ctx, "session", assistantEntryID)
	if err != nil {
		t.Fatal(err)
	}
	if assistant.Meta["nostr_event_id"] != publication.EventID || assistant.Meta["nostr_transport"] != "nip17" {
		t.Fatalf("assistant provenance missing: %+v", assistant.Meta)
	}
}
