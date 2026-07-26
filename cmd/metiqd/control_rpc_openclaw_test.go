package main

import (
	"context"
	"encoding/json"
	"testing"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func openclawCall(t *testing.T, h controlRPCHandler, method, params string) (nostruntime.ControlRPCResult, bool, error) {
	t.Helper()
	return h.handleOpenclawRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, state.ConfigDoc{})
}

// TestOpenclawChatHistoryReDispatch proves openclaw.chat.history is a real alias
// that reaches the native chat.history surface and returns its transcript shape.
func TestOpenclawChatHistoryReDispatch(t *testing.T) {
	ctx := context.Background()
	backing := newTestStore()
	docs := state.NewDocsRepository(backing, "openclaw-test")
	transcripts := state.NewTranscriptRepository(backing, "openclaw-test")
	if _, err := docs.PutSession(ctx, "sess-1", state.SessionDoc{Version: 1, SessionID: "sess-1", PeerPubKey: "peer-1"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []state.TranscriptEntryDoc{
		{Version: 1, SessionID: "sess-1", EntryID: "e1", Role: "user", Text: "hello", Unix: 10},
		{Version: 1, SessionID: "sess-1", EntryID: "e2", Role: "assistant", Text: "hi", Unix: 20},
	} {
		if _, err := transcripts.PutEntry(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	h := newControlRPCHandler(controlRPCDeps{
		configState:    newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}}),
		docsRepo:       docs,
		transcriptRepo: transcripts,
	})

	res, handled, err := openclawCall(t, h, methods.MethodOpenclawChatHistory, `{"session_id":"sess-1","limit":10}`)
	if !handled {
		t.Fatal("openclaw.chat.history not handled")
	}
	if err != nil {
		t.Fatalf("openclaw.chat.history: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["session_id"] != "sess-1" {
		t.Fatalf("unexpected session_id: %+v", p)
	}
	entries, ok := p["entries"].([]state.TranscriptEntryDoc)
	if !ok || len(entries) != 2 {
		t.Fatalf("expected 2 transcript entries, got %+v", p["entries"])
	}

	// Unknown session propagates the native error (no fabricated empty result).
	if _, _, err := openclawCall(t, h, methods.MethodOpenclawChatHistory, `{"session_id":"nope"}`); err == nil {
		t.Fatal("expected error for unknown session")
	}
}

// TestOpenclawApprovalListReDispatch proves openclaw.approval.list reaches the
// native approval ledger and returns its pending records.
func TestOpenclawApprovalListReDispatch(t *testing.T) {
	reg := newExecApprovalsRegistry()
	reg.Request(methods.ExecApprovalRequestRequest{Command: "git status", TimeoutMS: 60_000})
	h := newControlRPCHandler(controlRPCDeps{
		configState:   newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}}),
		execApprovals: reg,
	})

	res, handled, err := openclawCall(t, h, methods.MethodOpenclawApprovalList, `{}`)
	if !handled {
		t.Fatal("openclaw.approval.list not handled")
	}
	if err != nil {
		t.Fatalf("openclaw.approval.list: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["count"].(int) != 1 {
		t.Fatalf("expected 1 pending approval, got %+v", p)
	}
}

// TestOpenclawChatValidatesNativeContract proves openclaw.chat inherits the
// native chat.send validation (a peer target is required) rather than silently
// succeeding.
func TestOpenclawChatValidatesNativeContract(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{
		configState: newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}}),
	})
	if _, _, err := openclawCall(t, h, methods.MethodOpenclawChat, `{"text":"hi"}`); err == nil {
		t.Fatal("expected error for openclaw.chat with no target")
	}
}

// TestOpenclawSetupMethodsAreUnavailable confirms the five openclaw.setup.*
// onboarding methods are a genuine accepted deviation: the openclaw dispatch
// does not claim them (handled=false), so the gateway reports them unavailable
// rather than returning a fabricated stub.
func TestOpenclawSetupMethodsAreUnavailable(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{
		configState: newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}}),
	})
	for _, method := range []string{
		"openclaw.setup.detect",
		"openclaw.setup.activate",
		"openclaw.setup.auth.start",
		"openclaw.setup.prepare.start",
		"openclaw.setup.verify",
	} {
		if _, handled, _ := openclawCall(t, h, method, `{}`); handled {
			t.Fatalf("openclaw.setup method %s must not be handled (honest UNAVAILABLE)", method)
		}
	}
}
