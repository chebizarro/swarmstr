// parity_test.go – OpenClaw API response-shape parity tests.
//
// These tests exercise handleControlRPCRequest with a minimal in-process
// daemon state and assert that the response shapes match the documented
// OpenClaw gateway API contract.  They serve as a regression guard to ensure
// that refactoring or bead work doesn't silently break the API surface.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// parityCall is a helper that dispatches a gateway method with zero or
// minimal optional parameters and returns the result map.  Pass docsRepo=nil
// for read-only methods; use parityCallWithDocs for mutations that persist state.
func parityCall(t *testing.T, method string, params map[string]any, cfgState *runtimeConfigStore) map[string]any {
	t.Helper()
	return parityCallWithDocs(t, method, params, cfgState, nil)
}

func parityCallWithDocs(t *testing.T, method string, params map[string]any, cfgState *runtimeConfigStore, docsRepo *state.DocsRepository) map[string]any {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params for %s: %v", method, err)
	}
	res, err := handleControlRPCRequest(
		context.Background(),
		nostruntime.ControlRPCInbound{
			FromPubKey: "parity-caller",
			Method:     method,
			Params:     raw,
		},
		nil, nil, nil, nil, nil, nil, docsRepo, nil, nil,
		cfgState, nil, nil,
		time.Now().Add(-time.Minute),
	)
	if err != nil {
		t.Fatalf("%s returned error: %v", method, err)
	}
	b, err := json.Marshal(res.Result)
	if err != nil {
		t.Fatalf("%s result marshal failed: %v", method, err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s result should decode to map[string]any, got %T err=%v", method, res.Result, err)
	}
	return out
}

// newParityDocs creates a minimal in-memory DocsRepository backed by an in-memory store.
func newParityDocs() *state.DocsRepository {
	return state.NewDocsRepository(newTestStore(), "parity-author")
}

// minimalOpenCfg returns a ConfigDoc with enough content to pass validation.
func minimalOpenCfg() state.ConfigDoc {
	return state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		DM:      state.DMPolicy{Policy: "open"},
		Relays: state.RelayPolicy{
			Read:  []string{"wss://relay.example.com"},
			Write: []string{"wss://relay.example.com"},
		},
	}
}

// ── health ────────────────────────────────────────────────────────────────────

func TestParity_Health(t *testing.T) {
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	out := parityCall(t, methods.MethodHealth, nil, cfgState)
	// OpenClaw: {ok, version, uptime_ms}
	if _, ok := out["ok"]; !ok {
		t.Errorf("health: missing 'ok' field: %v", out)
	}
}

// ── status.get ───────────────────────────────────────────────────────────────

func TestParity_StatusGet(t *testing.T) {
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	out := parityCall(t, methods.MethodStatus, nil, cfgState)
	// OpenClaw: {version, relay_connected, dm_connected, uptime_ms, ...}
	for _, key := range []string{"version", "uptime_ms"} {
		if _, ok := out[key]; !ok {
			t.Errorf("status.get: missing %q field: %v", key, out)
		}
	}
}

// ── config.get ───────────────────────────────────────────────────────────────

func TestParity_ConfigGet(t *testing.T) {
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	out := parityCall(t, methods.MethodConfigGet, nil, cfgState)
	// OpenClaw parity: {config: {...}, base_hash: "<sha256>"}
	if _, ok := out["config"]; !ok {
		t.Errorf("config.get: missing 'config' key: %v", out)
	}
	hash, ok := out["base_hash"].(string)
	if !ok || hash == "" {
		t.Errorf("config.get: missing or empty 'base_hash': %v", out)
	}
	if len(hash) != 64 {
		t.Errorf("config.get: base_hash should be 64-char hex, got len=%d val=%q", len(hash), hash)
	}
	if hashAlias, _ := out["hash"].(string); hashAlias == "" {
		t.Errorf("config.get: missing or empty 'hash': %v", out)
	} else if hashAlias != hash {
		t.Errorf("config.get: hash alias mismatch hash=%q base_hash=%q", hashAlias, hash)
	}
}

// ── config.set ───────────────────────────────────────────────────────────────

func TestParity_ConfigSet(t *testing.T) {
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	docs := newParityDocs()
	out := parityCallWithDocs(t, methods.MethodConfigSet, map[string]any{
		"key":   "dm.policy",
		"value": "open",
	}, cfgState, docs)
	// OpenClaw: {ok: true, hash: "<hex>", restart_pending: bool}
	if ok, _ := out["ok"].(bool); !ok {
		t.Errorf("config.set: expected ok=true, got: %v", out)
	}
	if hash, _ := out["hash"].(string); hash == "" {
		t.Errorf("config.set: missing or empty 'hash': %v", out)
	}
	if _, hasRP := out["restart_pending"]; !hasRP {
		t.Errorf("config.set: missing 'restart_pending' field: %v", out)
	}
}

// ── supported.methods ────────────────────────────────────────────────────────

func TestParity_SupportedMethods(t *testing.T) {
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	res, err := handleControlRPCRequest(
		context.Background(),
		nostruntime.ControlRPCInbound{
			FromPubKey: "parity-caller",
			Method:     methods.MethodSupportedMethods,
			Params:     json.RawMessage(`{}`),
		},
		nil, nil, nil, nil, nil, nil, nil, nil, nil,
		cfgState, nil, nil,
		time.Now().Add(-time.Minute),
	)
	if err != nil {
		t.Fatalf("supported.methods error: %v", err)
	}
	list, ok := res.Result.([]string)
	if !ok || len(list) == 0 {
		t.Fatalf("supported.methods: expected []string, got %T", res.Result)
	}
	// All critical OpenClaw methods must be present.
	required := []string{
		methods.MethodHealth,
		methods.MethodStatus,
		methods.MethodConfigGet,
		methods.MethodConfigSet,
		methods.MethodConfigPatch,
		methods.MethodConfigApply,
		methods.MethodAgent,
		methods.MethodAgentWait,
		methods.MethodToolsCatalog,
		methods.MethodToolsProfileGet,
		methods.MethodToolsProfileSet,
		methods.MethodChatSend,
		methods.MethodChatHistory,
		methods.MethodSessionGet,
		methods.MethodSessionsList,
		methods.MethodSkillsStatus,
	}
	supported := make(map[string]bool, len(list))
	for _, m := range list {
		supported[m] = true
	}
	for _, want := range required {
		if !supported[want] {
			t.Errorf("supported.methods: missing required method %q", want)
		}
	}
}

// ── sessions.list ────────────────────────────────────────────────────────────

func TestParity_SessionsList(t *testing.T) {
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	out := parityCallWithDocs(t, methods.MethodSessionsList, map[string]any{"limit": 10}, cfgState, newParityDocs())
	// OpenClaw: {sessions: [...], total: N}
	if _, ok := out["sessions"]; !ok {
		t.Errorf("sessions.list: missing 'sessions' key: %v", out)
	}
	if _, ok := out["total"]; !ok {
		t.Errorf("sessions.list: missing 'total' key: %v", out)
	}
	if _, ok := out["path"]; !ok {
		t.Errorf("sessions.list: missing 'path' key: %v", out)
	}
}

// ── agent.identity.get ───────────────────────────────────────────────────────

func TestParity_AgentIdentityGet(t *testing.T) {
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	out := parityCallWithDocs(t, methods.MethodAgentIdentityGet, map[string]any{"session_id": "test-session"}, cfgState, newParityDocs())
	// OpenClaw-compatible: preserve snake_case while exposing camelCase aliases.
	for _, key := range []string{"agent_id", "display_name", "session_id", "agentId", "displayName", "sessionId"} {
		if _, ok := out[key]; !ok {
			t.Errorf("agent.identity.get: missing %q field: %v", key, out)
		}
	}
	if agentID, _ := out["agent_id"].(string); strings.TrimSpace(agentID) == "" {
		t.Errorf("agent.identity.get: agent_id should be non-empty: %v", out)
	}
}

// ── tools.catalog ────────────────────────────────────────────────────────────

func TestParity_ToolsCatalog(t *testing.T) {
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	out := parityCall(t, methods.MethodToolsCatalog, map[string]any{}, cfgState)
	// OpenClaw: {agentId, profiles: [...], groups: [...]}
	for _, key := range []string{"agentId", "profiles", "groups"} {
		if _, ok := out[key]; !ok {
			t.Errorf("tools.catalog: missing %q field: %v", key, out)
		}
	}
	profiles, ok := out["profiles"].([]any)
	if !ok || len(profiles) == 0 {
		t.Errorf("tools.catalog: profiles should be non-empty []any, got %T", out["profiles"])
	}
	groups, ok := out["groups"].([]any)
	if !ok || len(groups) == 0 {
		t.Errorf("tools.catalog: groups should be non-empty []any, got %T", out["groups"])
	}
}

// ── tools.profile.get ────────────────────────────────────────────────────────

func TestParity_ToolsProfileGet(t *testing.T) {
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	out := parityCallWithDocs(t, methods.MethodToolsProfileGet, map[string]any{}, cfgState, newParityDocs())
	// OpenClaw: {agentId, profile}
	for _, key := range []string{"agentId", "profile"} {
		if _, ok := out[key]; !ok {
			t.Errorf("tools.profile.get: missing %q field: %v", key, out)
		}
	}
}

// ── tools.profile.set ────────────────────────────────────────────────────────

func TestParity_ToolsProfileSet(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	res, err := handleControlRPCRequest(
		context.Background(),
		nostruntime.ControlRPCInbound{
			FromPubKey: "parity-caller",
			Method:     methods.MethodToolsProfileSet,
			Params:     json.RawMessage(`{"profile":"coding"}`),
		},
		nil, nil, nil, nil, nil, nil, docs, nil, nil,
		cfgState, nil, nil,
		time.Now().Add(-time.Minute),
	)
	if err != nil {
		t.Fatalf("tools.profile.set error: %v", err)
	}
	out, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("tools.profile.set: expected map[string]any, got %T", res.Result)
	}
	if p, _ := out["profile"].(string); p != "coding" {
		t.Errorf("tools.profile.set: expected profile=coding, got %q", p)
	}
}

// ── agent (run) ──────────────────────────────────────────────────────────────

func TestParity_AgentRun(t *testing.T) {
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	controlAgentRuntime = stubAgentRuntime{}
	controlAgentJobs = newAgentJobRegistry()

	res, err := handleControlRPCRequest(
		context.Background(),
		nostruntime.ControlRPCInbound{
			FromPubKey: "parity-caller",
			Method:     methods.MethodAgent,
			Params:     json.RawMessage(`{"message":"hello","session_id":"parity-s1"}`),
		},
		nil, nil, nil, nil, nil, nil, nil, nil, nil,
		cfgState, nil, nil,
		time.Now().Add(-time.Minute),
	)
	if err != nil {
		t.Fatalf("agent error: %v", err)
	}
	out, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("agent: expected map[string]any, got %T", res.Result)
	}
	// OpenClaw-compatible: preserve snake_case while exposing camelCase aliases.
	for _, key := range []string{"run_id", "status", "accepted_at", "runId", "acceptedAt"} {
		if _, ok := out[key]; !ok {
			t.Errorf("agent: missing %q field: %v", key, out)
		}
	}
	if out["status"] != "accepted" {
		t.Errorf("agent: status should be 'accepted', got %q", out["status"])
	}
}

// ── config.get base_hash round-trip ──────────────────────────────────────────

func TestParity_ConfigBaseHashRoundTrip(t *testing.T) {
	cfgState := newRuntimeConfigStore(minimalOpenCfg())
	docs := newParityDocs()

	// Get the current hash.
	getOut := parityCall(t, methods.MethodConfigGet, nil, cfgState)
	hash, _ := getOut["base_hash"].(string)
	if hash == "" {
		t.Fatal("config.get: empty base_hash")
	}

	// Use the hash in a config.set; it should succeed.
	res, err := handleControlRPCRequest(
		context.Background(),
		nostruntime.ControlRPCInbound{
			FromPubKey: "parity-caller",
			Method:     methods.MethodConfigSet,
			Params:     json.RawMessage(`{"key":"dm.policy","value":"open","baseHash":"` + hash + `"}`),
		},
		nil, nil, nil, nil, nil, nil, docs, nil, nil,
		cfgState, nil, nil,
		time.Now().Add(-time.Minute),
	)
	if err != nil {
		t.Fatalf("config.set with valid base_hash failed: %v", err)
	}
	setOut, ok := res.Result.(map[string]any)
	if !ok || setOut["ok"] != true {
		t.Errorf("config.set with valid base_hash: expected ok=true, got: %v", setOut)
	}

	// A stale hash should be rejected.
	_, err = handleControlRPCRequest(
		context.Background(),
		nostruntime.ControlRPCInbound{
			FromPubKey: "parity-caller",
			Method:     methods.MethodConfigSet,
			Params:     json.RawMessage(`{"key":"dm.policy","value":"pairing","baseHash":"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}`),
		},
		nil, nil, nil, nil, nil, nil, docs, nil, nil,
		cfgState, nil, nil,
		time.Now().Add(-time.Minute),
	)
	if err == nil {
		t.Error("config.set with stale base_hash should have failed")
	}
}

// ── profile filtering enforcement ────────────────────────────────────────────

func TestParity_ProfileFilteredRuntime(t *testing.T) {
	prevRegistry := controlToolRegistry
	defer func() { controlToolRegistry = prevRegistry }()

	controlAgentRuntime = stubAgentRuntime{}
	controlAgentJobs = newAgentJobRegistry()
	controlToolRegistry = agent.NewToolRegistry()

	// With no profile set, the full runtime is used (no filtering).
	rt := applyAgentProfileFilter(
		context.Background(),
		stubAgentRuntime{},
		"session-x",
		minimalOpenCfg(),
		nil,
	)
	// stubAgentRuntime is not a ProviderRuntime, so it should be returned unchanged.
	if _, ok := rt.(stubAgentRuntime); !ok {
		t.Errorf("non-ProviderRuntime should be returned unchanged, got %T", rt)
	}

	// A ProviderRuntime with no profile should also be returned unchanged.
	base, _ := agent.BuildRuntimeForModel("echo", controlToolRegistry)
	rt2 := applyAgentProfileFilter(
		context.Background(),
		base,
		"session-y",
		minimalOpenCfg(),
		nil,
	)
	// No profile configured → same runtime returned.
	if rt2 != base {
		t.Error("ProviderRuntime with no profile should be returned unchanged")
	}

	// Unknown configured profile must fail closed (deny all tool calls).
	toolReg := agent.NewToolRegistry()
	toolReg.Register("session_status", func(context.Context, map[string]any) (string, error) {
		return "ok", nil
	})
	providerRT, err := agent.NewProviderRuntime(profileTestProvider{}, toolReg)
	if err != nil {
		t.Fatalf("new provider runtime: %v", err)
	}
	filteredUnknown := applyAgentProfileFilter(
		context.Background(),
		providerRT,
		"session-z",
		state.ConfigDoc{Agents: state.AgentsConfig{{ID: "main", ToolProfile: "unknown-profile"}}},
		nil,
	)
	unknownRes, err := filteredUnknown.ProcessTurn(context.Background(), agent.Turn{SessionID: "s", UserText: "hi"})
	if err != nil {
		t.Fatalf("filtered runtime process turn (unknown profile): %v", err)
	}
	if len(unknownRes.ToolTraces) == 0 || unknownRes.ToolTraces[0].Error == "" {
		t.Fatalf("expected blocked tool trace for unknown profile, got: %+v", unknownRes.ToolTraces)
	}

	// Missing catalog source (nil registry) must also fail closed for non-full profiles.
	controlToolRegistry = nil
	filteredNoCatalog := applyAgentProfileFilter(
		context.Background(),
		providerRT,
		"session-z",
		state.ConfigDoc{Agents: state.AgentsConfig{{ID: "main", ToolProfile: "minimal"}}},
		nil,
	)
	noCatalogRes, err := filteredNoCatalog.ProcessTurn(context.Background(), agent.Turn{SessionID: "s", UserText: "hi"})
	if err != nil {
		t.Fatalf("filtered runtime process turn (missing catalog): %v", err)
	}
	if len(noCatalogRes.ToolTraces) == 0 || noCatalogRes.ToolTraces[0].Error == "" {
		t.Fatalf("expected blocked tool trace when catalog unavailable, got: %+v", noCatalogRes.ToolTraces)
	}
}

type profileTestProvider struct{}

func (profileTestProvider) Generate(context.Context, agent.Turn) (agent.ProviderResult, error) {
	return agent.ProviderResult{ToolCalls: []agent.ToolCall{{Name: "session_status"}}}, nil
}
