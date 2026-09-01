package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// messageActionFixture stages a session with a user turn (e1) and an assistant
// reply (e2, parent e1) in a fresh transcript store.
func messageActionFixture(t *testing.T) (controlRPCHandler, *state.TranscriptRepository) {
	t.Helper()
	ctx := context.Background()
	backing := newTestStore()
	docs := state.NewDocsRepository(backing, "msg-test")
	transcripts := state.NewTranscriptRepository(backing, "msg-test")
	if _, err := docs.PutSession(ctx, "sess-1", state.SessionDoc{
		Version: 1, SessionID: "sess-1", PeerPubKey: "peer-1",
	}); err != nil {
		t.Fatal(err)
	}
	entries := []state.TranscriptEntryDoc{
		{Version: 1, SessionID: "sess-1", EntryID: "e1", Role: "user", Text: "hello there", Unix: 10},
		{Version: 1, SessionID: "sess-1", EntryID: "e2", ParentEntryID: "e1", Role: "assistant", Text: "hi back", Unix: 20},
	}
	for _, e := range entries {
		if _, err := transcripts.PutEntry(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	h := newControlRPCHandler(controlRPCDeps{docsRepo: docs, transcriptRepo: transcripts})
	return h, transcripts
}

func messageActionCall(t *testing.T, h controlRPCHandler, params string) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	result, handled, err := h.handleMessageActionRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: methods.MethodMessageAction,
		Params: json.RawMessage(params),
	}, methods.MethodMessageAction, state.ConfigDoc{})
	if !handled {
		t.Fatalf("message.action was not handled by dispatch")
	}
	return result, err
}

type fakeMessageNostrPropagator struct {
	deletes   []messageNostrRef
	reactions []messageNostrRef
	reaction  []string
	err       error
}

func (f *fakeMessageNostrPropagator) Delete(_ context.Context, ref messageNostrRef) error {
	f.deletes = append(f.deletes, ref)
	return f.err
}

func (f *fakeMessageNostrPropagator) React(_ context.Context, ref messageNostrRef, reaction string) error {
	f.reactions = append(f.reactions, ref)
	f.reaction = append(f.reaction, reaction)
	return f.err
}

func setPublishedMessageMeta(t *testing.T, transcripts *state.TranscriptRepository, entryID string) {
	t.Helper()
	entry, err := transcripts.GetEntry(context.Background(), "sess-1", entryID)
	if err != nil {
		t.Fatal(err)
	}
	entry.Meta = map[string]any{
		"nostr_event_id":  strings.Repeat("a", 64),
		"nostr_pubkey":    "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
		"nostr_kind":      1,
		"nostr_transport": "nostr",
		"nostr_relays":    []string{"wss://relay.example"},
	}
	if _, err := transcripts.ReplaceEntry(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
}

func TestMessageAction_Delete(t *testing.T) {
	h, transcripts := messageActionFixture(t)
	res, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e1","verb":"delete"}`)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["ok"] != true || payload["deleted"] != true || payload["entry_id"] != "e1" {
		t.Fatalf("unexpected delete payload: %+v", payload)
	}
	// The entry is gone from the durable store.
	if _, err := transcripts.GetEntry(context.Background(), "sess-1", "e1"); err == nil {
		t.Fatalf("expected e1 to be tombstoned")
	}
	remaining, err := transcripts.ListSessionAll(context.Background(), "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range remaining {
		if e.EntryID == "e1" {
			t.Fatalf("deleted entry still listed: %+v", e)
		}
	}
}

func TestMessageAction_PropagatesPublishedNostrActions(t *testing.T) {
	t.Run("reaction add is NIP-25 and idempotent", func(t *testing.T) {
		h, transcripts := messageActionFixture(t)
		setPublishedMessageMeta(t, transcripts, "e2")
		publisher := &fakeMessageNostrPropagator{}
		h.deps.messageNostr = publisher

		params := `{"sessionKey":"sess-1","messageId":"e2","verb":"react","reaction":"+","actor":"alice"}`
		res, err := messageActionCall(t, h, params)
		if err != nil {
			t.Fatalf("react: %v", err)
		}
		if len(publisher.reactions) != 1 || publisher.reaction[0] != "+" {
			t.Fatalf("unexpected reaction publications: refs=%+v reactions=%+v", publisher.reactions, publisher.reaction)
		}
		if publisher.reactions[0].EventID != strings.Repeat("a", 64) || publisher.reactions[0].Relays[0] != "wss://relay.example" {
			t.Fatalf("wrong published-event mapping: %+v", publisher.reactions[0])
		}
		if res.Result.(map[string]any)["nostr_propagated"] != true {
			t.Fatalf("expected propagated result: %+v", res.Result)
		}
		if _, err := messageActionCall(t, h, params); err != nil {
			t.Fatalf("duplicate react: %v", err)
		}
		if len(publisher.reactions) != 1 {
			t.Fatalf("duplicate reaction republished: %d calls", len(publisher.reactions))
		}
		if _, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e2","verb":"react","reaction":"+","actor":"alice","remove":true}`); err != nil {
			t.Fatalf("remove react: %v", err)
		}
		if len(publisher.reactions) != 1 {
			t.Fatalf("local removal must not publish another NIP-25 event: %d calls", len(publisher.reactions))
		}
	})

	t.Run("delete is NIP-09 before tombstone", func(t *testing.T) {
		h, transcripts := messageActionFixture(t)
		setPublishedMessageMeta(t, transcripts, "e2")
		publisher := &fakeMessageNostrPropagator{}
		h.deps.messageNostr = publisher
		res, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e2","verb":"delete"}`)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if len(publisher.deletes) != 1 || publisher.deletes[0].Kind != 1 {
			t.Fatalf("unexpected deletion publications: %+v", publisher.deletes)
		}
		if res.Result.(map[string]any)["nostr_propagated"] != true {
			t.Fatalf("expected propagated delete result: %+v", res.Result)
		}
	})
}

func TestMessageAction_NostrPublishFailureDoesNotMutateLocalEntry(t *testing.T) {
	h, transcripts := messageActionFixture(t)
	setPublishedMessageMeta(t, transcripts, "e2")
	h.deps.messageNostr = &fakeMessageNostrPropagator{err: errors.New("relay rejected")}
	if _, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e2","verb":"delete"}`); err == nil {
		t.Fatal("expected propagation failure")
	}
	if _, err := transcripts.GetEntry(context.Background(), "sess-1", "e2"); err != nil {
		t.Fatalf("entry was tombstoned after failed publish: %v", err)
	}
	if _, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e2","verb":"react","reaction":"+","actor":"alice"}`); err == nil {
		t.Fatal("expected reaction propagation failure")
	}
	entry, err := transcripts.GetEntry(context.Background(), "sess-1", "e2")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := entry.Meta["reactions"]; exists {
		t.Fatalf("reaction mutated locally after failed publish: %+v", entry.Meta)
	}
}

func TestMessageAction_EditPreservesPriorText(t *testing.T) {
	h, transcripts := messageActionFixture(t)
	res, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e1","verb":"edit","text":"hello, edited"}`)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	msg := res.Result.(map[string]any)["message"].(map[string]any)
	if msg["text"] != "hello, edited" {
		t.Fatalf("edit did not apply new text: %+v", msg)
	}
	meta := msg["meta"].(map[string]any)
	if meta["edited_from"] != "hello there" {
		t.Fatalf("edited_from not preserved: %+v", meta)
	}
	revisions, ok := meta["revisions"].([]any)
	if !ok || len(revisions) != 1 {
		t.Fatalf("expected one revision record, got %+v", meta["revisions"])
	}
	prior := revisions[0].(map[string]any)
	if prior["text"] != "hello there" {
		t.Fatalf("revision did not preserve prior text: %+v", prior)
	}
	// The durable entry reflects the edit.
	got, err := transcripts.GetEntry(context.Background(), "sess-1", "e1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "hello, edited" {
		t.Fatalf("durable text not updated: %q", got.Text)
	}

	// A second edit appends a second revision (history accumulates).
	if _, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e1","verb":"edit","text":"hello, twice"}`); err != nil {
		t.Fatalf("second edit: %v", err)
	}
	got, _ = transcripts.GetEntry(context.Background(), "sess-1", "e1")
	if revs, _ := got.Meta["revisions"].([]any); len(revs) != 2 {
		t.Fatalf("expected two revision records after second edit, got %+v", got.Meta["revisions"])
	}
}

func TestMessageAction_EditRejectsNonUserRole(t *testing.T) {
	h, _ := messageActionFixture(t)
	if _, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e2","verb":"edit","text":"nope"}`); err == nil {
		t.Fatalf("expected error editing an assistant message")
	}
}

func TestMessageAction_ReactAddAndRemove(t *testing.T) {
	h, transcripts := messageActionFixture(t)
	// Add a reaction to the assistant message.
	res, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e2","verb":"react","reaction":"👍","actor":"alice"}`)
	if err != nil {
		t.Fatalf("react add: %v", err)
	}
	meta := res.Result.(map[string]any)["message"].(map[string]any)["meta"].(map[string]any)
	reactions := meta["reactions"].(map[string]any)
	if got := metaStringSlice(reactions["alice"]); len(got) != 1 || got[0] != "👍" {
		t.Fatalf("expected alice 👍, got %+v", reactions["alice"])
	}
	// Adding the same reaction again is idempotent (no duplicate).
	if _, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e2","verb":"react","reaction":"👍","actor":"alice"}`); err != nil {
		t.Fatalf("react add dup: %v", err)
	}
	got, _ := transcripts.GetEntry(context.Background(), "sess-1", "e2")
	if r := got.Meta["reactions"].(map[string]any); len(metaStringSlice(r["alice"])) != 1 {
		t.Fatalf("duplicate reaction stored: %+v", r["alice"])
	}
	// Remove it — the actor (and reactions map) prunes empty.
	res, err = messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e2","verb":"react","reaction":"👍","actor":"alice","remove":true}`)
	if err != nil {
		t.Fatalf("react remove: %v", err)
	}
	got, _ = transcripts.GetEntry(context.Background(), "sess-1", "e2")
	if _, ok := got.Meta["reactions"]; ok {
		t.Fatalf("expected reactions pruned after removal, got %+v", got.Meta["reactions"])
	}
}

func TestMessageAction_MissingMessageAndUnknownVerb(t *testing.T) {
	h, _ := messageActionFixture(t)
	// Missing message -> honest not_found (no error).
	res, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"nope","verb":"delete"}`)
	if err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["ok"] != false || payload["unavailableReason"] != "not_found" {
		t.Fatalf("expected not_found, got %+v", payload)
	}
	// Unknown verb -> rejected.
	if _, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e1","verb":"frobnicate"}`); err == nil {
		t.Fatalf("expected error for unknown verb")
	}
	// Missing verb -> rejected.
	if _, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e1"}`); err == nil {
		t.Fatalf("expected error for missing verb")
	}
}

func TestMessageAction_RetryLaunchesManagedRunFromUserTurn(t *testing.T) {
	h, _ := messageActionFixture(t)
	var calls int32
	var seenText atomic.Value
	rt := runtimeFunc(func(_ context.Context, turn agent.Turn) (agent.TurnResult, error) {
		atomic.AddInt32(&calls, 1)
		seenText.Store(turn.UserText)
		return agent.TurnResult{Text: "retried"}, nil
	})
	jobs := wireControllerGlobals(t, rt)

	// Retry on the assistant message resolves to the parent user turn's text.
	res, err := messageActionCall(t, h, `{"sessionKey":"sess-1","messageId":"e2","verb":"retry"}`)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	out := res.Result.(map[string]any)
	runID, _ := out["runId"].(string)
	if runID == "" {
		t.Fatalf("expected a runId, got %+v", out)
	}
	if _, ok := jobs.Get(runID); !ok {
		t.Fatalf("run %q not tracked", runID)
	}
	snap, ok := jobs.Wait(context.Background(), runID, 2*time.Second)
	if !ok || snap.Status != "ok" {
		t.Fatalf("run did not complete ok: ok=%v snap=%+v", ok, snap)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("runtime invoked %d times, want 1", calls)
	}
	if got, _ := seenText.Load().(string); got != "hello there" {
		t.Fatalf("retry prompt = %q, want the user turn text 'hello there'", got)
	}
}

func TestMessageAction_RetryIdempotencyReplay(t *testing.T) {
	h, _ := messageActionFixture(t)
	var calls int32
	rt := runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		atomic.AddInt32(&calls, 1)
		return agent.TurnResult{Text: "retried"}, nil
	})
	jobs := wireControllerGlobals(t, rt)

	params := `{"sessionKey":"sess-1","messageId":"e1","verb":"retry","idempotencyKey":"idem-ko2f-1"}`
	first, err := messageActionCall(t, h, params)
	if err != nil {
		t.Fatalf("first retry: %v", err)
	}
	runID1, _ := first.Result.(map[string]any)["runId"].(string)
	if runID1 == "" {
		t.Fatalf("expected runId on first call: %+v", first.Result)
	}
	jobs.Wait(context.Background(), runID1, 2*time.Second)

	second, err := messageActionCall(t, h, params)
	if err != nil {
		t.Fatalf("replay retry: %v", err)
	}
	out := second.Result.(map[string]any)
	if out["runId"] != runID1 {
		t.Fatalf("idempotency replay returned different run: %v vs %q", out["runId"], runID1)
	}
	if idem, _ := out["idempotent"].(bool); !idem {
		t.Fatalf("expected idempotent=true on replay, got %+v", out)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("runtime invoked %d times across replay, want 1 (no double-launch)", calls)
	}
}
