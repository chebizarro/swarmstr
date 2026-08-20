package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/gateway/methods"
	"metiq/internal/nostr/events"
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

func TestSoulFactoryUnsupportedMethodsFailClosedWithoutSideEffects(t *testing.T) {
	h, docs := testSoulFactoryHandler(t)
	unsupported := []string{
		methods.MethodSoulFactoryUpdate,
		methods.MethodSoulFactorySuspend,
		methods.MethodSoulFactoryResume,
		methods.MethodSoulFactoryRedeploy,
		methods.MethodSoulFactoryRevoke,
		methods.MethodSoulFactoryAvatarGenerate,
		methods.MethodSoulFactoryAvatarSet,
		methods.MethodSoulFactoryVoiceConfigure,
		methods.MethodSoulFactoryVoiceSample,
		methods.MethodSoulFactoryMemoryConfigure,
		methods.MethodSoulFactoryMemoryReindex,
		methods.MethodSoulFactoryPersonaUpdate,
		methods.MethodSoulFactoryConfigReload,
		"soulfactory.unknown",
	}
	for i, method := range unsupported {
		t.Run(method, func(t *testing.T) {
			in := testSoulFactoryInboundWith(t, method, map[string]any{}, fmt.Sprintf("idem-%d", i), "sha256:spec", fmt.Sprintf("event-%d", i), "agent-alice")
			res, err := h.Handle(context.Background(), in)
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			env := decodeSoulFactoryTestResult(t, res)
			if env["status"] != "rejected" {
				t.Fatalf("status = %v, want rejected envelope=%#v", env["status"], env)
			}
			errShape, ok := env["error"].(map[string]any)
			if !ok || errShape["code"] != "unsupported_method" {
				t.Fatalf("expected unsupported_method, got %#v", env["error"])
			}
		})
	}
	if _, err := docs.GetAgent(context.Background(), "agent-alice"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unsupported methods created binding state: %v", err)
	}
}

func TestSoulFactoryIdempotencyExactReplaySurvivesRestartWithoutSideEffects(t *testing.T) {
	cfg := state.ConfigDoc{
		Agent:   state.AgentPolicy{DefaultModel: "echo"},
		Control: state.ControlPolicy{RequireAuth: true, Admins: []state.ControlAdmin{{PubKey: testSoulFactoryController, Methods: []string{methods.MethodSoulFactoryProvision}}}},
	}
	backing := newTestStore()
	docs := state.NewDocsRepository(backing, "test-author")
	h := newControlRPCHandler(controlRPCDeps{configState: newRuntimeConfigStore(cfg), docsRepo: docs, startedAt: time.Now()})
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

	// Recreate both repository and handler to model a daemon restart. The
	// checkpoint and agent binding must be recovered from durable state.
	docs = state.NewDocsRepository(backing, "test-author")
	h = newControlRPCHandler(controlRPCDeps{configState: newRuntimeConfigStore(cfg), docsRepo: docs, startedAt: time.Now()})

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

func TestSoulFactoryIdempotencyConflictSurvivesRestart(t *testing.T) {
	cfg := state.ConfigDoc{
		Agent:   state.AgentPolicy{DefaultModel: "echo"},
		Control: state.ControlPolicy{RequireAuth: true, Admins: []state.ControlAdmin{{PubKey: testSoulFactoryController, Methods: []string{methods.MethodSoulFactoryProvision}}}},
	}
	backing := newTestStore()
	docs := state.NewDocsRepository(backing, "test-author")
	h := newControlRPCHandler(controlRPCDeps{configState: newRuntimeConfigStore(cfg), docsRepo: docs, startedAt: time.Now()})
	ctx := context.Background()
	params := testSoulFactoryProvisionParams()
	firstIn := testSoulFactoryInboundWith(t, methods.MethodSoulFactoryProvision, params, "idem-conflict", "sha256:spec-1", "event-first", "agent-alice")
	first, err := h.Handle(ctx, firstIn)
	if err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	requireSoulFactoryStatus(t, decodeSoulFactoryTestResult(t, first), "success")

	docs = state.NewDocsRepository(backing, "test-author")
	h = newControlRPCHandler(controlRPCDeps{configState: newRuntimeConfigStore(cfg), docsRepo: docs, startedAt: time.Now()})

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
	params := testSoulFactoryProvisionParams()
	delete(params, "assets")
	in := testSoulFactoryInbound(t, methods.MethodSoulFactoryProvision, params)
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

func TestLocalCapabilityAnnouncementAdvertisesOnlyProvision(t *testing.T) {
	cfg := state.ConfigDoc{
		Relays: state.RelayPolicy{Read: []string{"wss://relay.example"}, Write: []string{"wss://relay.example"}},
		Control: state.ControlPolicy{Admins: []state.ControlAdmin{
			{PubKey: "controller-a", Methods: []string{"status.get"}},
			{PubKey: "controller-b", Methods: []string{methods.MethodSoulFactoryProvision}},
			{PubKey: "controller-c", Methods: []string{methods.MethodSoulFactoryUpdate}},
		}},
	}
	cap := buildLocalCapabilityAnnouncement(context.Background(), cfg, nil)
	if cap.SoulFactory.Schema != nostruntime.SoulFactoryRuntimeCapabilitySchema {
		t.Fatalf("schema = %q", cap.SoulFactory.Schema)
	}
	if cap.SoulFactory.ControlSchema != nostruntime.SoulFactoryRuntimeControlSchema {
		t.Fatalf("control schema = %q", cap.SoulFactory.ControlSchema)
	}
	if len(cap.SoulFactory.Methods) != 1 || cap.SoulFactory.Methods[0] != methods.MethodSoulFactoryProvision {
		t.Fatalf("advertised methods = %v, want [%s]", cap.SoulFactory.Methods, methods.MethodSoulFactoryProvision)
	}
	if len(cap.SoulFactory.ControllerPubKeys) != 1 || cap.SoulFactory.ControllerPubKeys[0] != "controller-b" {
		t.Fatalf("controllers = %v", cap.SoulFactory.ControllerPubKeys)
	}
	if len(cap.SoulFactory.Features) != 0 || cap.SoulFactory.FeatureParity.Runtime != "" {
		t.Fatalf("unimplemented features advertised: features=%#v parity=%#v", cap.SoulFactory.Features, cap.SoulFactory.FeatureParity)
	}
	content := nostruntime.BuildCapabilityContent(cap)
	parsed := testParseSoulFactoryCapabilityContent(t, content)
	if len(parsed.Methods) != 1 || parsed.Methods[0] != methods.MethodSoulFactoryProvision {
		t.Fatalf("serialized methods = %v content=%s", parsed.Methods, content)
	}
}

func testParseSoulFactoryCapabilityContent(t *testing.T, content string) nostruntime.SoulFactoryCapability {
	t.Helper()
	evt := nostr.Event{Kind: nostr.Kind(events.CAS_AGENT_CAPABILITY), Content: content}
	cap, err := nostruntime.ParseCapabilityEvent(&evt)
	if err != nil {
		t.Fatalf("ParseCapabilityEvent: %v", err)
	}
	return cap.SoulFactory
}
