package main

import (
	"context"
	"encoding/json"
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
