package main

import (
	"context"
	"encoding/json"
	"testing"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
)

func TestSessionLifecycleAliasesViaInjectedDeps(t *testing.T) {
	h, _, _ := newTestControlRPCHandler(t)
	ctx := context.Background()
	call := func(method, params string) (any, error) {
		result, err := h.Handle(ctx, nostruntime.ControlRPCInbound{Method: method, Params: json.RawMessage(params), FromPubKey: "test-pubkey"})
		return result.Result, err
	}

	createdRaw, err := call(methods.MethodSessionsCreate, `{"key":"session-a","agentId":"main","label":"Demo","model":"test-model"}`)
	if err != nil {
		t.Fatal(err)
	}
	created := createdRaw.(map[string]any)
	if created["key"] != "session-a" || created["created"] != true {
		t.Fatalf("create=%#v", created)
	}
	adoptedRaw, err := call(methods.MethodSessionsCreate, `{"key":"session-a"}`)
	if err != nil || adoptedRaw.(map[string]any)["created"] != false {
		t.Fatalf("adopt=%#v err=%v", adoptedRaw, err)
	}

	describedRaw, err := call(methods.MethodSessionsDescribe, `{"key":"session-a","includeLastMessage":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if describedRaw.(methods.SessionGetResponse).Session.SessionID != "session-a" {
		t.Fatalf("describe=%#v", describedRaw)
	}

	if _, err := call(methods.MethodSessionsSend, `{"key":"session-a","message":"hello"}`); err != nil {
		t.Fatalf("send: %v", err)
	}
	_, release := h.deps.chatCancels.Begin("session-a", context.Background())
	defer release()
	abortedRaw, err := call(methods.MethodSessionsAbort, `{"key":"session-a"}`)
	if err != nil {
		t.Fatal(err)
	}
	if abortedRaw.(map[string]any)["aborted"] != true {
		t.Fatalf("abort=%#v", abortedRaw)
	}
}
