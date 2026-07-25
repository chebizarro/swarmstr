package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"metiq/internal/gateway/methods"
	"metiq/internal/gateway/nodepending"
	nostruntime "metiq/internal/nostr/runtime"
)

func nodeSurfaceCall(t *testing.T, h controlRPCHandler, method, params string) (nostruntime.ControlRPCResult, bool, error) {
	t.Helper()
	return h.handleNodeSurfaceRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method)
}

func newNodeSurfaceHandler() (controlRPCHandler, *nodepending.Store) {
	pending := nodepending.New()
	return newControlRPCHandler(controlRPCDeps{
		nodeInvocations: newNodeInvocationRegistry(),
		nodePending:     pending,
		dmBus:           stubDMTransport{pubkey: "gw"},
	}), pending
}

func TestNodePluginSurfaceRefreshEnqueues(t *testing.T) {
	h, pending := newNodeSurfaceHandler()
	res, handled, err := nodeSurfaceCall(t, h, methods.MethodNodePluginSurfaceRefresh, `{"node_id":"n1"}`)
	if !handled || err != nil {
		t.Fatalf("pluginSurface.refresh handled=%v err=%v", handled, err)
	}
	payload := res.Result.(map[string]any)
	if payload["ok"] != true || payload["command"] != "pluginSurface.refresh" || payload["delivery"] != "node_pending_queue" {
		t.Fatalf("unexpected refresh payload: %+v", payload)
	}
	// The command is durably queued for the node to pull.
	pulled, err := pending.Pull("n1")
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	actions, _ := pulled["actions"].([]nodepending.Action)
	if len(actions) != 1 || actions[0].Command != "pluginSurface.refresh" {
		t.Fatalf("expected one queued refresh command, got %+v", pulled)
	}
}

func TestNodePluginToolsUpdateCarriesPayloadAndDedupes(t *testing.T) {
	h, _ := newNodeSurfaceHandler()
	params := `{"node_id":"n2","tools":[{"id":"t1"}],"idempotency_key":"k1"}`
	res, _, err := nodeSurfaceCall(t, h, methods.MethodNodePluginToolsUpdate, params)
	if err != nil {
		t.Fatalf("pluginTools.update: %v", err)
	}
	if res.Result.(map[string]any)["command"] != "pluginTools.update" {
		t.Fatalf("unexpected command: %+v", res.Result)
	}
	// Same idempotency key dedupes rather than double-queueing.
	res2, _, err := nodeSurfaceCall(t, h, methods.MethodNodePluginToolsUpdate, params)
	if err != nil {
		t.Fatalf("pluginTools.update (2nd): %v", err)
	}
	pending2, _ := res2.Result.(map[string]any)["pending"].(map[string]any)
	if pending2["deduped"] != true {
		t.Fatalf("expected idempotent dedupe: %+v", pending2)
	}
}

func TestNodeSkillsUpdateEnqueues(t *testing.T) {
	h, pending := newNodeSurfaceHandler()
	res, _, err := nodeSurfaceCall(t, h, methods.MethodNodeSkillsUpdate, `{"node_id":"n3","skills":[{"id":"s1"}]}`)
	if err != nil {
		t.Fatalf("skills.update: %v", err)
	}
	if res.Result.(map[string]any)["command"] != "skills.update" {
		t.Fatalf("unexpected command: %+v", res.Result)
	}
	pulled, err := pending.Pull("n3")
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	actions, _ := pulled["actions"].([]nodepending.Action)
	if len(actions) != 1 || actions[0].Command != "skills.update" {
		t.Fatalf("expected one queued skills.update, got %+v", pulled)
	}
}

func TestNodeSurfaceRequiresNodeID(t *testing.T) {
	h, _ := newNodeSurfaceHandler()
	_, handled, err := nodeSurfaceCall(t, h, methods.MethodNodePluginSurfaceRefresh, `{}`)
	if !handled {
		t.Fatal("expected method handled")
	}
	if err == nil || !strings.Contains(err.Error(), "node_id is required") {
		t.Fatalf("expected node_id-required error, got %v", err)
	}
}

func TestNodeSurfaceRuntimeUnavailable(t *testing.T) {
	// No node invoke registry configured → honest error, not a silent success.
	h := newControlRPCHandler(controlRPCDeps{})
	_, handled, err := nodeSurfaceCall(t, h, methods.MethodNodeSkillsUpdate, `{"node_id":"n1"}`)
	if !handled {
		t.Fatal("expected method handled")
	}
	if err == nil {
		t.Fatal("expected runtime-unavailable error")
	}
}

func TestNodeSurfaceUnownedMethodNotHandled(t *testing.T) {
	h, _ := newNodeSurfaceHandler()
	_, handled, _ := nodeSurfaceCall(t, h, methods.MethodNodeList, `{}`)
	if handled {
		t.Fatal("node.list must not be claimed by the node-surface handler")
	}
}
