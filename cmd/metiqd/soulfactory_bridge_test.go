package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

const testSoulFactoryController = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testSoulFactoryHandler() controlRPCHandler {
	cfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true, Admins: []state.ControlAdmin{{PubKey: testSoulFactoryController, Methods: []string{"soulfactory.*"}}}}}
	return newControlRPCHandler(controlRPCDeps{configState: newRuntimeConfigStore(cfg), startedAt: time.Now()})
}

func testSoulFactoryInbound(t *testing.T, method string, params map[string]any) nostruntime.ControlRPCInbound {
	t.Helper()
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	targetPubKey := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	envelopeRaw, err := json.Marshal(map[string]any{
		"schema":          nostruntime.SoulFactoryRuntimeControlSchema,
		"method":          method,
		"idempotency_key": "idem-1",
		"requested_at":    int64(1715700000),
		"operator":        map[string]any{"pubkey": "operator", "request_event": "operator-event"},
		"controller":      map[string]any{"pubkey": testSoulFactoryController},
		"target":          map[string]any{"runtime": "metiq", "runtime_pubkey": targetPubKey, "agent_id": "agent-alice"},
		"soul":            map[string]any{"id": "alice", "spec_hash": "sha256:spec"},
		"params":          json.RawMessage(paramsRaw),
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return nostruntime.ControlRPCInbound{
		EventID:       "38384-event",
		RequestID:     "idem-1",
		FromPubKey:    testSoulFactoryController,
		Method:        method,
		Params:        json.RawMessage(paramsRaw),
		RawContent:    json.RawMessage(envelopeRaw),
		CreatedAt:     time.Now().Unix(),
		Authenticated: true,
		Tags: nostr.Tags{
			{"p", targetPubKey},
			{"method", method},
			{"e", "operator-event"},
			{"soul", "alice"},
			{"agent-id", "agent-alice"},
			{"controller", testSoulFactoryController},
			{"idempotency-key", "idem-1"},
			{"spec-hash", "sha256:spec"},
			{"schema", nostruntime.SoulFactoryRuntimeControlSchema},
		},
	}
}

func TestSoulFactoryProvisionHandlerValidatesAndReturnsContractEnvelope(t *testing.T) {
	h := testSoulFactoryHandler()
	in := testSoulFactoryInbound(t, methods.MethodSoulFactoryProvision, map[string]any{
		"identity":     map[string]any{"name": "Alice", "purpose": "test", "tier": "standard"},
		"runtime":      map[string]any{"target": "metiq", "capability_ref": "cap"},
		"permissions":  map[string]any{"allowed_kinds": []int{1}, "tool_grants": []string{}, "approval_policy": "ask"},
		"relay_policy": map[string]any{"read": []string{}, "write": []string{}, "control": []string{}},
		"workspace":    map[string]any{},
		"assets":       map[string]any{},
	})
	res, err := h.Handle(context.Background(), in)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(res.RawPayload), &env); err != nil {
		t.Fatalf("unmarshal raw payload: %v payload=%q", err, res.RawPayload)
	}
	if env["schema"] != nostruntime.SoulFactoryRuntimeControlSchema || env["method"] != methods.MethodSoulFactoryProvision || env["status"] != "failed" {
		t.Fatalf("unexpected envelope: %#v", env)
	}
	result, ok := env["result"].(map[string]any)
	if !ok || result["runtime"] != "metiq" || result["agent_id"] != "agent-alice" {
		t.Fatalf("unexpected runtime result: %#v", env["result"])
	}
	errShape, ok := env["error"].(map[string]any)
	if !ok || errShape["code"] != "execution_failed" {
		t.Fatalf("expected execution_failed scaffold error, got %#v", env["error"])
	}
}

func TestSoulFactoryHandlerRejectsMissingRequiredParam(t *testing.T) {
	h := testSoulFactoryHandler()
	in := testSoulFactoryInbound(t, methods.MethodSoulFactoryRedeploy, map[string]any{"reason": "operator request"})
	res, err := h.Handle(context.Background(), in)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(res.RawPayload), &env); err != nil {
		t.Fatalf("unmarshal raw payload: %v payload=%q", err, res.RawPayload)
	}
	if env["status"] != "rejected" {
		t.Fatalf("status = %v, want rejected", env["status"])
	}
	errShape, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("error shape = %T %#v", env["error"], env["error"])
	}
	if errShape["code"] != "missing_required_param" {
		t.Fatalf("code = %q", errShape["code"])
	}
}

func TestLocalCapabilityAnnouncementAdvertisesSoulFactoryControllers(t *testing.T) {
	cfg := state.ConfigDoc{
		Relays: state.RelayPolicy{Read: []string{"wss://relay.example"}, Write: []string{"wss://relay.example"}},
		Control: state.ControlPolicy{Admins: []state.ControlAdmin{
			{PubKey: "controller-a", Methods: []string{"status.get"}},
			{PubKey: "controller-b", Methods: []string{"soulfactory.*"}},
		}},
	}
	cap := buildLocalCapabilityAnnouncement(context.Background(), cfg, nil)
	if cap.SoulFactory.Schema != nostruntime.SoulFactoryRuntimeCapabilitySchema {
		t.Fatalf("schema = %q", cap.SoulFactory.Schema)
	}
	if cap.SoulFactory.ControlSchema != nostruntime.SoulFactoryRuntimeControlSchema {
		t.Fatalf("control schema = %q", cap.SoulFactory.ControlSchema)
	}
	if len(cap.SoulFactory.Methods) != len(methods.SoulFactoryMethods()) {
		t.Fatalf("methods = %v", cap.SoulFactory.Methods)
	}
	if len(cap.SoulFactory.ControllerPubKeys) != 1 || cap.SoulFactory.ControllerPubKeys[0] != "controller-b" {
		t.Fatalf("controllers = %v", cap.SoulFactory.ControllerPubKeys)
	}
}
