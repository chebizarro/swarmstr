package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"metiq/internal/autoreply"
	"metiq/internal/gateway/methods"
	"metiq/internal/gateway/sessioncoord"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func TestPhase2SessionRuntimeHandlers(t *testing.T) {
	h, docsRepo, _ := newTestControlRPCHandler(t)
	h.deps.sessionCoordinator = sessioncoord.New(docsRepo, h.deps.sessionStore)
	h.deps.steeringMailboxes = autoreply.NewSteeringMailboxRegistry(8, autoreply.QueueDropNewest)
	ctx := context.Background()
	if _, err := docsRepo.PutSession(ctx, "sess-1", state.SessionDoc{Version: 1, SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}

	mailbox := h.deps.steeringMailboxes.Get("sess-1")
	steer, _ := json.Marshal(map[string]any{"key": "sess-1", "message": "change course", "idempotencyKey": "steer-1"})
	if _, handled, err := h.handleSessionRPC(ctx, nostruntime.ControlRPCInbound{Method: methods.MethodSessionsSteer, Params: steer}, methods.MethodSessionsSteer, state.ConfigDoc{}); err != nil || !handled || mailbox.Len() != 1 {
		t.Fatalf("sessions.steer: handled=%v len=%d err=%v", handled, mailbox.Len(), err)
	}

	viewerCtx := gatewayws.ContextWithConnectionID(ctx, "conn-1")
	viewers, _ := json.Marshal(map[string]any{"sessionKeys": []string{"sess-1"}})
	if _, handled, err := h.handleSessionRPC(viewerCtx, nostruntime.ControlRPCInbound{Method: methods.MethodSessionsViewersSet, Params: viewers}, methods.MethodSessionsViewersSet, state.ConfigDoc{}); err != nil || !handled {
		t.Fatalf("sessions.viewers.set: handled=%v err=%v", handled, err)
	}
	if got := h.deps.sessionCoordinator.ViewerSessions("conn-1"); len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("viewer sessions: %v", got)
	}

	ownerCtx := gatewayws.ContextWithControlPrincipal(ctx, gatewayws.ControlPrincipal{Subject: "alice", Role: "operator"})
	assign, _ := json.Marshal(map[string]any{"key": "sess-1", "owner": map[string]any{"type": "agent", "id": "planner"}})
	if _, handled, err := h.handleSessionRPC(ownerCtx, nostruntime.ControlRPCInbound{Method: methods.MethodSessionsAssignOwner, Params: assign}, methods.MethodSessionsAssignOwner, state.ConfigDoc{}); err != nil || !handled {
		t.Fatalf("sessions.assignOwner: handled=%v err=%v", handled, err)
	}
	doc, err := docsRepo.GetSessionSharing(ctx, "sess-1")
	if err != nil || doc.OwnerType != "agent" || doc.OwnerSubject != "planner" {
		t.Fatalf("assigned owner doc: %+v err=%v", doc, err)
	}

	if _, err := h.deps.sessionCoordinator.PutGroups(ctx, []string{"Work"}); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "work")
	update, _ := json.Marshal(map[string]any{"name": "Work", "cwd": cwd, "worktree": true})
	if _, handled, err := h.handleSessionRPC(ctx, nostruntime.ControlRPCInbound{Method: methods.MethodSessionsGroupsUpdate, Params: update}, methods.MethodSessionsGroupsUpdate, state.ConfigDoc{}); err != nil || !handled {
		t.Fatalf("sessions.groups.update: handled=%v err=%v", handled, err)
	}
	defaultsResult, handled, err := h.handleSessionRPC(ctx, nostruntime.ControlRPCInbound{Method: methods.MethodSessionsGroupsDefaults, Params: json.RawMessage(`{}`)}, methods.MethodSessionsGroupsDefaults, state.ConfigDoc{})
	if err != nil || !handled {
		t.Fatalf("sessions.groups.defaults: handled=%v err=%v", handled, err)
	}
	defaults := defaultsResult.Result.(map[string]any)["defaults"].([]sessioncoord.GroupDefault)
	if len(defaults) != 1 || defaults[0].CWD == nil || *defaults[0].CWD != cwd || !defaults[0].Worktree {
		t.Fatalf("group defaults: %+v", defaults)
	}
}

func TestNodeRunnerInventoryInternalHandler(t *testing.T) {
	h, _, _ := newTestControlRPCHandler(t)
	h.deps.nodeInvocations = newNodeInvocationRegistry()
	ctx := gatewayws.ContextWithControlPrincipal(context.Background(), gatewayws.ControlPrincipal{Subject: "node-1", Role: "node"})
	params := json.RawMessage(`{"protocolFeatures":["node-worker-supervisor-v6"],"workerHost":{"enabled":true,"capacity":{"total":2,"available":1}}}`)
	result, handled, err := h.handleNodeRPC(ctx, nostruntime.ControlRPCInbound{Method: methods.MethodNodeRunnerInventoryUpdate, Params: params}, methods.MethodNodeRunnerInventoryUpdate)
	if err != nil || !handled {
		t.Fatalf("node.runnerInventory.update: handled=%v err=%v", handled, err)
	}
	if result.Result.(map[string]any)["nodeId"] != "node-1" {
		t.Fatalf("unexpected inventory result: %#v", result.Result)
	}
	for _, advertised := range methods.SupportedMethods() {
		if advertised == methods.MethodNodeRunnerInventoryUpdate {
			t.Fatal("internal runner inventory method must not be advertised")
		}
	}
}

func TestSubagentLifecycleEmitter(t *testing.T) {
	registry := newSubagentRegistry()
	var events []SubagentRecord
	registry.SetLifecycleEmitter(func(record SubagentRecord) { events = append(events, record) })
	if _, ok := registry.Spawn("run-events", "child-events", "parent", 1, "work", 10); !ok {
		t.Fatal("spawn failed")
	}
	registry.Finish("run-events", "done", "")
	if len(events) != 2 || events[0].Status != "running" || events[1].Status != "done" {
		t.Fatalf("lifecycle events: %+v", events)
	}
}

func TestTaskRecoveryHandlersUseDurableSubagentRegistry(t *testing.T) {
	durable, err := openDurableSubagentRegistry(filepath.Join(t.TempDir(), "subagents.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer durable.Close()
	if _, ok := durable.Spawn("run-1", "child-1", "parent-1", 1, "do work", 10); !ok {
		t.Fatal("spawn failed")
	}
	durable.Finish("run-1", "done", "")
	h := newControlRPCHandler(controlRPCDeps{services: &daemonServices{session: sessionServices{subagents: durable}}})
	params := json.RawMessage(`{"taskIds":["run-1"]}`)
	for _, method := range []string{methods.MethodTasksRetry, methods.MethodTasksDismiss} {
		result, handled, err := h.handleTaskRPC(context.Background(), nostruntime.ControlRPCInbound{Method: method, Params: params}, method)
		if err != nil || !handled {
			t.Fatalf("%s: handled=%v err=%v", method, handled, err)
		}
		rows := result.Result.(map[string]any)["results"].([]map[string]any)
		if len(rows) != 1 || rows[0]["ok"] != true {
			t.Fatalf("%s result: %#v", method, rows)
		}
	}
}
