package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/gateway/sessioncoord"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func newCollabTestHandler(t *testing.T) (context.Context, controlRPCHandler, *sessioncoord.Service) {
	t.Helper()
	ctx := context.Background()
	backend := newTestStore()
	docs := state.NewDocsRepository(backend, "author")
	store, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docs.PutSession(ctx, "session-a", state.SessionDoc{Version: 1, SessionID: "session-a"}); err != nil {
		t.Fatal(err)
	}
	coordinator := sessioncoord.New(docs, store)
	handler := newControlRPCHandler(controlRPCDeps{docsRepo: docs, sessionStore: store, sessionCoordinator: coordinator})
	return ctx, handler, coordinator
}

func principalCtx(ctx context.Context, subject string, scopes ...string) context.Context {
	return gatewayws.ContextWithControlPrincipal(ctx, gatewayws.ControlPrincipal{
		Authenticated:  true,
		Subject:        subject,
		Scopes:         scopes,
		ScopesEnforced: true,
	})
}

func TestSessionCollabRPCVisibilityAndMembers(t *testing.T) {
	ctx, handler, _ := newCollabTestHandler(t)
	ownerCtx := principalCtx(ctx, "alice", "operator.write", "operator.read")
	viewerCtx := principalCtx(ctx, "bob", "operator.write", "operator.read")

	set := func(callCtx context.Context, params string) (nostruntime.ControlRPCResult, error) {
		result, handled, err := handler.handleSessionCollabRPC(callCtx, nostruntime.ControlRPCInbound{Method: "session.visibility.set", Params: json.RawMessage(params)}, "session.visibility.set", state.ConfigDoc{})
		if !handled {
			t.Fatal("session.visibility.set must be handled")
		}
		return result, err
	}
	if _, err := set(ownerCtx, `{"sessionKey":"session-a","visibility":"read-only"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := set(viewerCtx, `{"sessionKey":"session-a","visibility":"shared"}`); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("viewer visibility set must be forbidden: %v", err)
	}

	// Central choke point: read-only rejects viewer sends, admits owner and members.
	send := nostruntime.ControlRPCInbound{Method: "sessions.send", Params: json.RawMessage(`{"session_id":"session-a","text":"hi"}`)}
	if err := handler.authorizeSessionMutationVisibility(viewerCtx, send, "sessions.send"); err == nil {
		t.Fatal("read-only session must reject viewer mutation")
	}
	if err := handler.authorizeSessionMutationVisibility(ownerCtx, send, "sessions.send"); err != nil {
		t.Fatalf("owner mutation must pass: %v", err)
	}
	if err := handler.authorizeSessionMutationVisibility(ctx, send, "sessions.send"); err != nil {
		t.Fatalf("principal-less operator path must pass: %v", err)
	}
	if _, handled, err := handler.handleSessionCollabRPC(ownerCtx, nostruntime.ControlRPCInbound{Method: "session.members.add", Params: json.RawMessage(`{"sessionKey":"session-a","identityId":"bob"}`)}, "session.members.add", state.ConfigDoc{}); !handled || err != nil {
		t.Fatalf("members.add: handled=%v err=%v", handled, err)
	}
	if err := handler.authorizeSessionMutationVisibility(viewerCtx, send, "sessions.send"); err != nil {
		t.Fatalf("member mutation must pass after grant: %v", err)
	}
	listResult, handled, err := handler.handleSessionCollabRPC(ownerCtx, nostruntime.ControlRPCInbound{Method: "session.members.list", Params: json.RawMessage(`{"sessionKey":"session-a"}`)}, "session.members.list", state.ConfigDoc{})
	if !handled || err != nil {
		t.Fatalf("members.list: handled=%v err=%v", handled, err)
	}
	aggregate, ok := listResult.Result.(sessioncoord.MembersListResult)
	if !ok || aggregate.Role != "owner" || len(aggregate.Members) != 1 || aggregate.Members[0].IdentityID != "bob" {
		t.Fatalf("unexpected members aggregate: %+v", listResult.Result)
	}
}

func TestSessionsObserverVisibilityRequiresConnection(t *testing.T) {
	ctx, handler, coordinator := newCollabTestHandler(t)
	in := nostruntime.ControlRPCInbound{Method: "sessions.observer.visibility", Params: json.RawMessage(`{"visible":true}`)}
	if _, handled, err := handler.handleSessionCollabRPC(ctx, in, "sessions.observer.visibility", state.ConfigDoc{}); !handled || err == nil {
		t.Fatalf("observer visibility without a connection must fail: handled=%v err=%v", handled, err)
	}
	connCtx := gatewayws.ContextWithConnectionID(ctx, "conn-1")
	if _, handled, err := handler.handleSessionCollabRPC(connCtx, in, "sessions.observer.visibility", state.ConfigDoc{}); !handled || err != nil {
		t.Fatalf("observer visibility: handled=%v err=%v", handled, err)
	}
	if !coordinator.ObserverVisible("conn-1") {
		t.Fatal("observer visibility must be recorded for the connection")
	}
}

func TestSessionSuggestionsAndTypingRPC(t *testing.T) {
	ctx, handler, coordinator := newCollabTestHandler(t)
	ownerCtx := principalCtx(ctx, "alice", "operator.write", "operator.read")
	suggesterCtx := principalCtx(ctx, "bob", "operator.write", "operator.read")

	call := func(callCtx context.Context, method, params string) (nostruntime.ControlRPCResult, error) {
		result, handled, err := handler.handleSessionCollabRPC(callCtx, nostruntime.ControlRPCInbound{Method: method, Params: json.RawMessage(params)}, method, state.ConfigDoc{})
		if !handled {
			t.Fatalf("%s must be handled", method)
		}
		return result, err
	}
	if _, err := call(ownerCtx, "session.visibility.set", `{"sessionKey":"session-a","visibility":"suggest"}`); err != nil {
		t.Fatal(err)
	}
	added, err := call(suggesterCtx, "session.suggestions.add", `{"sessionKey":"session-a","text":"try a smaller diff"}`)
	if err != nil {
		t.Fatal(err)
	}
	suggestion := added.Result.(map[string]any)["suggestion"].(sessioncoord.Suggestion)
	if suggestion.State != "pending" || suggestion.Author.ID != "bob" {
		t.Fatalf("unexpected suggestion: %+v", suggestion)
	}
	listed, err := call(ownerCtx, "session.suggestions.list", `{"sessionKey":"session-a"}`)
	if err != nil {
		t.Fatal(err)
	}
	if rows := listed.Result.(map[string]any)["suggestions"].([]sessioncoord.Suggestion); len(rows) != 1 {
		t.Fatalf("owner must list pending suggestions: %+v", rows)
	}
	if _, err := call(suggesterCtx, "session.suggestions.resolve", `{"sessionKey":"session-a","id":"`+suggestion.ID+`","resolution":"dismiss"}`); err == nil {
		t.Fatal("viewer resolve must be forbidden")
	}
	resolved, err := call(ownerCtx, "session.suggestions.resolve", `{"sessionKey":"session-a","id":"`+suggestion.ID+`","resolution":"dismiss"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Result.(map[string]any)["suggestion"].(sessioncoord.Suggestion); got.State != "dismissed" {
		t.Fatalf("unexpected resolution: %+v", got)
	}

	// Typing requires a WS connection id to broadcast; identity accounting is
	// exercised in the sessioncoord tests.
	typingParams := `{"sessionKey":"session-a","sessionId":"session-a","typing":true}`
	quiet, err := call(ownerCtx, "session.typing", typingParams)
	if err != nil {
		t.Fatal(err)
	}
	if quiet.Result.(map[string]any)["broadcast"] != false {
		t.Fatal("typing without a connection must not broadcast")
	}
	connCtx := gatewayws.ContextWithConnectionID(ownerCtx, "conn-typing")
	loud, err := call(connCtx, "session.typing", typingParams)
	if err != nil {
		t.Fatal(err)
	}
	if loud.Result.(map[string]any)["broadcast"] != true {
		t.Fatal("connection-scoped identified typing must broadcast")
	}
	_ = coordinator
}

func TestSessionDiscussionAndObserverAskRPC(t *testing.T) {
	ctx, handler, coordinator := newCollabTestHandler(t)
	ownerCtx := principalCtx(ctx, "alice", "operator.write", "operator.read")
	call := func(callCtx context.Context, method, params string) (nostruntime.ControlRPCResult, error) {
		result, handled, err := handler.handleSessionCollabRPC(callCtx, nostruntime.ControlRPCInbound{Method: method, Params: json.RawMessage(params)}, method, state.ConfigDoc{})
		if !handled {
			t.Fatalf("%s must be handled", method)
		}
		return result, err
	}
	info, err := call(ownerCtx, "session.discussion.info", `{"sessionKey":"session-a"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Result.(sessioncoord.DiscussionState); got.State != "none" {
		t.Fatalf("absent provider must report none: %+v", got)
	}
	opened, err := call(ownerCtx, "session.discussion.open", `{"sessionKey":"session-a"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := opened.Result.(sessioncoord.DiscussionState); got.State != "none" {
		t.Fatalf("absent provider open must report none: %+v", got)
	}
	coordinator.SetObserverAskProvider(func(_ context.Context, key, question string) (string, error) {
		return "observed " + key + " re " + question, nil
	})
	if _, err := call(ownerCtx, "sessions.observer.ask", `{"sessionKey":"session-a","question":"state?"}`); err == nil {
		t.Fatal("observer ask without a connection must fail")
	}
	connCtx := gatewayws.ContextWithConnectionID(ownerCtx, "conn-ask")
	answered, err := call(connCtx, "sessions.observer.ask", `{"sessionKey":"session-a","question":"state?"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := answered.Result.(map[string]any)["answer"].(string); got != "observed session-a re state?" {
		t.Fatalf("unexpected answer: %q", got)
	}
}

func TestBuildObserverDigestBounds(t *testing.T) {
	ctx := context.Background()
	backend := newTestStore()
	transcripts := state.NewTranscriptRepository(backend, "author")
	for i := 0; i < 40; i++ {
		if _, err := transcripts.PutEntry(ctx, state.TranscriptEntryDoc{
			Version: 1, SessionID: "s-digest", EntryID: fmt.Sprintf("e%03d", i),
			Role: "assistant", Text: strings.Repeat("x", 500), Unix: int64(i + 1),
		}); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := buildObserverDigest(ctx, transcripts, "s-digest")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(digest), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || len(rows) > observerDigestMaxEntries {
		t.Fatalf("digest rows out of bounds: %d", len(rows))
	}
	for _, row := range rows {
		if len([]rune(row["text"])) > observerDigestMaxEntryLen+1 {
			t.Fatalf("digest entry exceeds per-entry bound: %d", len(row["text"]))
		}
	}
}
