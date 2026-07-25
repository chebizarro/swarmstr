package main

import (
	"context"
	"encoding/json"
	"testing"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func chatSurfaceFixture(t *testing.T) controlRPCHandler {
	t.Helper()
	ctx := context.Background()
	backing := newTestStore()
	docs := state.NewDocsRepository(backing, "chat-test")
	transcripts := state.NewTranscriptRepository(backing, "chat-test")
	if _, err := docs.PutSession(ctx, "sess-1", state.SessionDoc{
		Version: 1, SessionID: "sess-1", PeerPubKey: "peer-1", LastInboundAt: 100, LastReplyAt: 200,
	}); err != nil {
		t.Fatal(err)
	}
	entries := []state.TranscriptEntryDoc{
		{Version: 1, SessionID: "sess-1", EntryID: "e1", Role: "user", Text: "hello there", Unix: 10},
		{Version: 1, SessionID: "sess-1", EntryID: "e2", Role: "assistant", Text: "hi back", Unix: 20},
	}
	for _, e := range entries {
		if _, err := transcripts.PutEntry(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	return newControlRPCHandler(controlRPCDeps{docsRepo: docs, transcriptRepo: transcripts})
}

func chatCall(t *testing.T, h controlRPCHandler, method, params string) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	result, handled, err := h.handleChatSurfaceRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, state.ConfigDoc{})
	if !handled {
		t.Fatalf("method %s was not handled by chat surface dispatch", method)
	}
	return result, err
}

func TestChatStartup(t *testing.T) {
	h := chatSurfaceFixture(t)
	res, err := chatCall(t, h, methods.MethodChatStartup, `{"sessionKey":"sess-1"}`)
	if err != nil {
		t.Fatalf("chat.startup: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["ok"] != true || payload["startup"] != true || payload["session_id"] != "sess-1" {
		t.Fatalf("unexpected startup payload: %+v", payload)
	}
	if got := len(payload["entries"].([]state.TranscriptEntryDoc)); got != 2 {
		t.Fatalf("expected 2 entries, got %d", got)
	}
	// Missing sessionKey is rejected.
	if _, err := chatCall(t, h, methods.MethodChatStartup, `{}`); err == nil {
		t.Fatal("expected error for missing sessionKey")
	}
}

func TestChatMetadata(t *testing.T) {
	h := chatSurfaceFixture(t)
	res, err := chatCall(t, h, methods.MethodChatMetadata, `{"sessionKey":"sess-1"}`)
	if err != nil {
		t.Fatalf("chat.metadata: %v", err)
	}
	meta := res.Result.(map[string]any)["metadata"].(map[string]any)
	if meta["session_id"] != "sess-1" || meta["peer_pubkey"] != "peer-1" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
	if meta["message_count"].(int) != 2 || meta["user_count"].(int) != 1 || meta["assistant_count"].(int) != 1 {
		t.Fatalf("unexpected counts: %+v", meta)
	}
	if meta["last_message_at"].(int64) != 20 {
		t.Fatalf("unexpected last_message_at: %+v", meta["last_message_at"])
	}
}

func TestChatMessageGet(t *testing.T) {
	h := chatSurfaceFixture(t)
	res, err := chatCall(t, h, methods.MethodChatMessageGet, `{"sessionKey":"sess-1","messageId":"e2"}`)
	if err != nil {
		t.Fatalf("chat.message.get: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["ok"] != true {
		t.Fatalf("expected ok, got %+v", payload)
	}
	msg := payload["message"].(map[string]any)
	if msg["entry_id"] != "e2" || msg["role"] != "assistant" || msg["text"] != "hi back" {
		t.Fatalf("unexpected message: %+v", msg)
	}

	// maxChars truncates.
	res, err = chatCall(t, h, methods.MethodChatMessageGet, `{"sessionKey":"sess-1","messageId":"e1","maxChars":3}`)
	if err != nil {
		t.Fatalf("chat.message.get truncated: %v", err)
	}
	msg = res.Result.(map[string]any)["message"].(map[string]any)
	if msg["text"] != "hel…" {
		t.Fatalf("expected truncated text 'hel…', got %q", msg["text"])
	}

	// Unknown id -> ok:false not_found.
	res, err = chatCall(t, h, methods.MethodChatMessageGet, `{"sessionKey":"sess-1","messageId":"missing"}`)
	if err != nil {
		t.Fatalf("chat.message.get missing: %v", err)
	}
	payload = res.Result.(map[string]any)
	if payload["ok"] != false || payload["unavailableReason"] != "not_found" {
		t.Fatalf("expected not_found, got %+v", payload)
	}
}

func TestChatToolTitles(t *testing.T) {
	h := chatSurfaceFixture(t)
	res, err := chatCall(t, h, methods.MethodChatToolTitles,
		`{"items":[{"toolCallId":"tc1","toolName":"sessions.history"},{"toolCallId":"tc2","toolName":"read_file"}]}`)
	if err != nil {
		t.Fatalf("chat.toolTitles: %v", err)
	}
	titles := res.Result.(map[string]any)["titles"].(map[string]string)
	if titles["tc1"] != "Sessions History" || titles["tc2"] != "Read File" {
		t.Fatalf("unexpected titles: %+v", titles)
	}
	// Empty batch yields an empty (non-nil) map.
	res, err = chatCall(t, h, methods.MethodChatToolTitles, `{"items":[]}`)
	if err != nil {
		t.Fatalf("chat.toolTitles empty: %v", err)
	}
	if len(res.Result.(map[string]any)["titles"].(map[string]string)) != 0 {
		t.Fatalf("expected empty titles map")
	}
}
