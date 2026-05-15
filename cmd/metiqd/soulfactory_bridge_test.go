package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

const testSoulFactoryController = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testSoulFactoryHandler(t *testing.T) (controlRPCHandler, *state.DocsRepository) {
	t.Helper()
	cfg := state.ConfigDoc{
		Agent:   state.AgentPolicy{DefaultModel: "echo"},
		Control: state.ControlPolicy{RequireAuth: true, Admins: []state.ControlAdmin{{PubKey: testSoulFactoryController, Methods: []string{"soulfactory.*"}}}},
	}
	docs := state.NewDocsRepository(newTestStore(), "test-author")
	h := newControlRPCHandler(controlRPCDeps{configState: newRuntimeConfigStore(cfg), docsRepo: docs, startedAt: time.Now()})
	return h, docs
}

func testSoulFactoryInbound(t *testing.T, method string, params map[string]any) nostruntime.ControlRPCInbound {
	t.Helper()
	return testSoulFactoryInboundWith(t, method, params, "idem-1", "sha256:spec", "38384-event", "agent-alice")
}

func testSoulFactoryInboundWith(t *testing.T, method string, params map[string]any, idempotencyKey string, specHash string, eventID string, agentID string) nostruntime.ControlRPCInbound {
	t.Helper()
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	targetPubKey := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	envelopeRaw, err := json.Marshal(map[string]any{
		"schema":          nostruntime.SoulFactoryRuntimeControlSchema,
		"method":          method,
		"idempotency_key": idempotencyKey,
		"requested_at":    int64(1715700000),
		"operator":        map[string]any{"pubkey": "operator", "request_event": "operator-event"},
		"controller":      map[string]any{"pubkey": testSoulFactoryController},
		"target":          map[string]any{"runtime": "metiq", "runtime_pubkey": targetPubKey, "agent_id": agentID},
		"soul":            map[string]any{"id": "alice", "spec_hash": specHash},
		"params":          json.RawMessage(paramsRaw),
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return nostruntime.ControlRPCInbound{
		EventID:       eventID,
		RequestID:     idempotencyKey,
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
			{"agent-id", agentID},
			{"controller", testSoulFactoryController},
			{"idempotency-key", idempotencyKey},
			{"spec-hash", specHash},
			{"schema", nostruntime.SoulFactoryRuntimeControlSchema},
		},
	}
}

func testSoulFactoryProvisionParams() map[string]any {
	return map[string]any{
		"identity":     map[string]any{"name": "Alice", "purpose": "test", "tier": "standard"},
		"runtime":      map[string]any{"target": "metiq", "capability_ref": "cap"},
		"permissions":  map[string]any{"allowed_kinds": []int{1}, "tool_grants": []string{}, "approval_policy": "ask"},
		"relay_policy": map[string]any{"read": []string{}, "write": []string{}, "control": []string{}},
		"workspace":    map[string]any{"repo": "repo-a"},
		"assets":       map[string]any{},
	}
}

func decodeSoulFactoryTestResult(t *testing.T, res nostruntime.ControlRPCResult) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(res.RawPayload), &env); err != nil {
		t.Fatalf("unmarshal raw payload: %v payload=%q", err, res.RawPayload)
	}
	return env
}

func requireSoulFactoryStatus(t *testing.T, env map[string]any, want string) map[string]any {
	t.Helper()
	if env["schema"] != nostruntime.SoulFactoryRuntimeControlSchema || env["status"] != want {
		t.Fatalf("unexpected envelope: %#v", env)
	}
	if want == "success" && env["error"] != nil {
		t.Fatalf("success error = %#v", env["error"])
	}
	result, _ := env["result"].(map[string]any)
	return result
}

func TestSoulFactoryProvisionHandlerExecutesAndReturnsContractEnvelope(t *testing.T) {
	h, docs := testSoulFactoryHandler(t)
	in := testSoulFactoryInbound(t, methods.MethodSoulFactoryProvision, testSoulFactoryProvisionParams())
	res, err := h.Handle(context.Background(), in)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	env := decodeSoulFactoryTestResult(t, res)
	result := requireSoulFactoryStatus(t, env, "success")
	if env["method"] != methods.MethodSoulFactoryProvision || result["runtime"] != "metiq" || result["agent_id"] != "agent-alice" || result["state"] != "running" {
		t.Fatalf("unexpected runtime result: %#v", env)
	}
	doc, err := docs.GetAgent(context.Background(), "agent-alice")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if doc.Name != "Alice" || doc.Workspace != "repo-a" || doc.Deleted {
		t.Fatalf("unexpected agent doc: %#v", doc)
	}
}

func TestSoulFactoryRuntimeExecutionCoversAllMethods(t *testing.T) {
	h, docs := testSoulFactoryHandler(t)
	ctx := context.Background()
	if _, err := docs.PutSession(ctx, "session-1", state.SessionDoc{Version: 1, SessionID: "session-1", Meta: map[string]any{"agent_id": "agent-alice"}}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	cases := []struct {
		method string
		idem   string
		spec   string
		params map[string]any
		state  string
	}{
		{method: methods.MethodSoulFactoryProvision, idem: "idem-provision", spec: "sha256:spec-1", params: testSoulFactoryProvisionParams(), state: "running"},
		{method: methods.MethodSoulFactoryUpdate, idem: "idem-update", spec: "sha256:spec-2", params: map[string]any{"resolved_spec": map[string]any{"identity": map[string]any{"name": "Alice Updated"}, "workspace": map[string]any{"repo": "repo-b"}}, "previous_spec_hash": "sha256:spec-1", "new_spec_hash": "sha256:spec-2", "update_mode": "replace"}, state: "running"},
		{method: methods.MethodSoulFactorySuspend, idem: "idem-suspend", spec: "sha256:spec-2", params: map[string]any{"reason": "maintenance"}, state: "suspended"},
		{method: methods.MethodSoulFactoryResume, idem: "idem-resume", spec: "sha256:spec-2", params: map[string]any{"reason": "maintenance complete"}, state: "running"},
		{method: methods.MethodSoulFactoryRedeploy, idem: "idem-redeploy", spec: "sha256:spec-2", params: map[string]any{"reason": "refresh", "strategy": "restart"}, state: "running"},
		{method: methods.MethodSoulFactoryRevoke, idem: "idem-revoke", spec: "sha256:spec-2", params: map[string]any{"reason": "operator revoke", "revoke_runtime_credentials": true}, state: "revoked"},
	}
	for i, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			in := testSoulFactoryInboundWith(t, tc.method, tc.params, tc.idem, tc.spec, fmt.Sprintf("event-%d", i), "agent-alice")
			res, err := h.Handle(ctx, in)
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			env := decodeSoulFactoryTestResult(t, res)
			result := requireSoulFactoryStatus(t, env, "success")
			if result["state"] != tc.state {
				t.Fatalf("state = %v, want %s envelope=%#v", result["state"], tc.state, env)
			}
		})
	}
	doc, err := docs.GetAgent(ctx, "agent-alice")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if !doc.Deleted {
		t.Fatalf("revoke should mark agent deleted: %#v", doc)
	}
	sf, _ := doc.Meta["soulfactory"].(map[string]any)
	if sf["state"] != "revoked" || sf["reason"] != "operator revoke" {
		t.Fatalf("unexpected soulfactory meta: %#v", sf)
	}
	session, err := docs.GetSession(ctx, "session-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if session.Meta != nil && session.Meta["agent_id"] != nil {
		t.Fatalf("revoke should clear persisted session assignment: %#v", session.Meta)
	}
}

func TestSoulFactoryUpdateRejectsSpecHashMismatch(t *testing.T) {
	h, _ := testSoulFactoryHandler(t)
	ctx := context.Background()
	provision := testSoulFactoryInboundWith(t, methods.MethodSoulFactoryProvision, testSoulFactoryProvisionParams(), "idem-provision", "sha256:spec-1", "event-provision", "agent-alice")
	res, err := h.Handle(ctx, provision)
	if err != nil {
		t.Fatalf("provision Handle: %v", err)
	}
	requireSoulFactoryStatus(t, decodeSoulFactoryTestResult(t, res), "success")
	updateParams := map[string]any{"patch": map[string]any{"identity": map[string]any{"name": "Stale"}}, "previous_spec_hash": "sha256:older", "new_spec_hash": "sha256:spec-2", "update_mode": "merge"}
	update := testSoulFactoryInboundWith(t, methods.MethodSoulFactoryUpdate, updateParams, "idem-stale-update", "sha256:spec-2", "event-update", "agent-alice")
	res, err = h.Handle(ctx, update)
	if err != nil {
		t.Fatalf("update Handle: %v", err)
	}
	env := decodeSoulFactoryTestResult(t, res)
	if env["status"] != "failed" {
		t.Fatalf("status = %v, want failed envelope=%#v", env["status"], env)
	}
	errShape, ok := env["error"].(map[string]any)
	if !ok || errShape["code"] != "spec_hash_mismatch" {
		t.Fatalf("expected spec_hash_mismatch, got %#v", env["error"])
	}
}

func TestSoulFactoryIdempotencyExactReplayReturnsPriorResultWithoutSideEffects(t *testing.T) {
	h, docs := testSoulFactoryHandler(t)
	ctx := context.Background()
	params := testSoulFactoryProvisionParams()
	firstIn := testSoulFactoryInboundWith(t, methods.MethodSoulFactoryProvision, params, "idem-replay", "sha256:spec-1", "event-first", "agent-alice")
	first, err := h.Handle(ctx, firstIn)
	if err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	requireSoulFactoryStatus(t, decodeSoulFactoryTestResult(t, first), "success")
	doc, err := docs.GetAgent(ctx, "agent-alice")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	doc.Name = "Manual Edit"
	if _, err := docs.PutAgent(ctx, "agent-alice", doc); err != nil {
		t.Fatalf("PutAgent: %v", err)
	}

	replayIn := testSoulFactoryInboundWith(t, methods.MethodSoulFactoryProvision, params, "idem-replay", "sha256:spec-1", "event-replay", "agent-alice")
	replay, err := h.Handle(ctx, replayIn)
	if err != nil {
		t.Fatalf("replay Handle: %v", err)
	}
	if replay.RawPayload != first.RawPayload {
		t.Fatalf("replay payload changed\nfirst=%s\nreplay=%s", first.RawPayload, replay.RawPayload)
	}
	doc, err = docs.GetAgent(ctx, "agent-alice")
	if err != nil {
		t.Fatalf("GetAgent after replay: %v", err)
	}
	if doc.Name != "Manual Edit" {
		t.Fatalf("exact replay repeated side effects, doc=%#v", doc)
	}
}

func TestSoulFactoryIdempotencyConflictReturnsDuplicateConflict(t *testing.T) {
	h, docs := testSoulFactoryHandler(t)
	ctx := context.Background()
	params := testSoulFactoryProvisionParams()
	firstIn := testSoulFactoryInboundWith(t, methods.MethodSoulFactoryProvision, params, "idem-conflict", "sha256:spec-1", "event-first", "agent-alice")
	first, err := h.Handle(ctx, firstIn)
	if err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	requireSoulFactoryStatus(t, decodeSoulFactoryTestResult(t, first), "success")

	conflictIn := testSoulFactoryInboundWith(t, methods.MethodSoulFactoryProvision, params, "idem-conflict", "sha256:spec-2", "event-conflict", "agent-alice")
	conflict, err := h.Handle(ctx, conflictIn)
	if err != nil {
		t.Fatalf("conflict Handle: %v", err)
	}
	env := decodeSoulFactoryTestResult(t, conflict)
	if env["status"] != "rejected" {
		t.Fatalf("status = %v, want rejected envelope=%#v", env["status"], env)
	}
	errShape, ok := env["error"].(map[string]any)
	if !ok || errShape["code"] != "duplicate_conflict" {
		t.Fatalf("expected duplicate_conflict, got %#v", env["error"])
	}
	doc, err := docs.GetAgent(ctx, "agent-alice")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	sf, _ := doc.Meta["soulfactory"].(map[string]any)
	if sf["spec_hash"] != "sha256:spec-1" {
		t.Fatalf("conflict mutated agent state: %#v", sf)
	}
}

func TestSoulFactoryHandlerRejectsMissingRequiredParam(t *testing.T) {
	h, _ := testSoulFactoryHandler(t)
	in := testSoulFactoryInbound(t, methods.MethodSoulFactoryRedeploy, map[string]any{"reason": "operator request"})
	res, err := h.Handle(context.Background(), in)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	env := decodeSoulFactoryTestResult(t, res)
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
