package main

import (
	"context"
	"encoding/json"
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
