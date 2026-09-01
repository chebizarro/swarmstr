package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"metiq/internal/autoreply"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func callSessionGoalRPCForTest(t *testing.T, h controlRPCHandler, method string, params map[string]any) (map[string]any, error) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	result, handled, err := h.handleSessionGoalRPC(context.Background(), nostruntime.ControlRPCInbound{Method: method, Params: raw, Internal: true, FromPubKey: "operator"}, method, state.ConfigDoc{})
	if !handled {
		t.Fatalf("method %s was not handled", method)
	}
	if err != nil {
		return nil, err
	}
	payload, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", result.Result)
	}
	return payload, nil
}

func TestSessionGoalLifecycleIsDurableFencedAndIdempotent(t *testing.T) {
	docs, _, _ := newHistoryFixture(t)
	mailboxes := autoreply.NewSteeringMailboxRegistry(8, autoreply.QueueDropNewest)
	h := newControlRPCHandler(controlRPCDeps{docsRepo: docs, steeringMailboxes: mailboxes})
	now := time.Now().UnixMilli()
	base := map[string]any{
		"sessionKey": "s1", "sessionId": "s1", "goalId": "goal-1",
		"operationId": "op-edit", "issuedAtMs": now,
		"action": "edit", "objective": "Ship the gateway backlog",
	}
	created, err := callSessionGoalRPCForTest(t, h, methods.MethodSessionsGoalUpdate, base)
	if err != nil {
		t.Fatal(err)
	}
	goal := created["goal"].(map[string]any)
	if goal["id"] != "goal-1" || goal["status"] != "active" || goal["objective"] != "Ship the gateway backlog" {
		t.Fatalf("created goal = %#v", goal)
	}

	replay, err := callSessionGoalRPCForTest(t, h, methods.MethodSessionsGoalUpdate, base)
	if err != nil || replay["replayed"] != true {
		t.Fatalf("replay = %#v err=%v", replay, err)
	}
	conflict := mapsClone(base)
	conflict["objective"] = "Different request"
	if _, err := callSessionGoalRPCForTest(t, h, methods.MethodSessionsGoalUpdate, conflict); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("conflicting operation id error = %v", err)
	}
	fenced := mapsClone(base)
	fenced["operationId"] = "op-fenced"
	fenced["sessionId"] = "old-session"
	if _, err := callSessionGoalRPCForTest(t, h, methods.MethodSessionsGoalUpdate, fenced); err == nil || !strings.Contains(err.Error(), "session changed") {
		t.Fatalf("session fence error = %v", err)
	}

	mailbox := mailboxes.Get("s1")
	resume := map[string]any{
		"sessionKey": "s1", "sessionId": "s1", "goalId": "goal-1",
		"operationId": "op-resume", "issuedAtMs": now, "action": "resume", "note": "Use the focused tests",
	}
	if _, err := callSessionGoalRPCForTest(t, h, methods.MethodSessionsGoalUpdate, resume); err != nil {
		t.Fatal(err)
	}
	if mailbox.Len() != 1 {
		t.Fatalf("resume mailbox len = %d", mailbox.Len())
	}
	if _, err := callSessionGoalRPCForTest(t, h, methods.MethodSessionsGoalUpdate, resume); err != nil {
		t.Fatal(err)
	}
	if mailbox.Len() != 1 {
		t.Fatalf("replayed resume dispatched twice: len=%d", mailbox.Len())
	}

	clear := map[string]any{
		"sessionKey": "s1", "sessionId": "s1", "goalId": "goal-1",
		"operationId": "op-clear", "issuedAtMs": now,
	}
	cleared, err := callSessionGoalRPCForTest(t, h, methods.MethodSessionsGoalClear, clear)
	if err != nil || cleared["status"] != "cleared" {
		t.Fatalf("clear = %#v err=%v", cleared, err)
	}
	doc, err := docs.GetSession(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Meta[sessionGoalMetaKey]; ok {
		t.Fatalf("goal remained after clear: %#v", doc.Meta)
	}
}

func mapsClone(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
