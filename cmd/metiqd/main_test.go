package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip44"

	"metiq/internal/agent"
	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/autoreply"
	"metiq/internal/config"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	mcppkg "metiq/internal/mcp"
	"metiq/internal/memory"
	"metiq/internal/nostr/events"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/nostr/secure"
	"metiq/internal/store/state"
)

type mainTestKeyer struct {
	keyer.KeySigner
	sk nostr.SecretKey
}

func newMainTestKeyer(t *testing.T) nostr.Keyer {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("SecretKeyFromHex: %v", err)
	}
	return mainTestKeyer{KeySigner: keyer.NewPlainKeySigner([32]byte(sk)), sk: sk}
}

func (k mainTestKeyer) Encrypt(_ context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	ck, err := nip44.GenerateConversationKey(recipient, k.sk)
	if err != nil {
		return "", err
	}
	return nip44.Encrypt(plaintext, ck)
}

func (k mainTestKeyer) Decrypt(_ context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	ck, err := nip44.GenerateConversationKey(sender, k.sk)
	if err != nil {
		return "", err
	}
	return nip44.Decrypt(ciphertext, ck)
}

func TestEnsureRuntimeConfigDefaultsStorageEncryption(t *testing.T) {
	docs := state.NewDocsRepository(newTestStore(), "author")
	cfg, err := ensureRuntimeConfig(context.Background(), docs, []string{"wss://relay.example"}, "admin")
	if err != nil {
		t.Fatalf("ensureRuntimeConfig: %v", err)
	}
	if !cfg.StorageEncryptEnabled() {
		t.Fatalf("expected storage encryption enabled by default, got %#v", cfg.Storage)
	}
}

func TestApplyRuntimeConfigSideEffectsUpdatesStorageCodec(t *testing.T) {
	prevCodec := controlStateEnvelopeCodec
	codec, err := secure.NewMutableSelfEnvelopeCodec(newMainTestKeyer(t), true)
	if err != nil {
		t.Fatalf("NewMutableSelfEnvelopeCodec: %v", err)
	}
	controlStateEnvelopeCodec = codec
	defer func() { controlStateEnvelopeCodec = prevCodec }()

	applyRuntimeConfigSideEffects(state.ConfigDoc{Storage: state.StorageConfig{Encrypt: state.BoolPtr(false)}})
	if codec.EncryptEnabled() {
		t.Fatal("expected storage codec to switch to plaintext mode")
	}

	applyRuntimeConfigSideEffects(state.ConfigDoc{Storage: state.StorageConfig{Encrypt: state.BoolPtr(true)}})
	if !codec.EncryptEnabled() {
		t.Fatal("expected storage codec to switch back to encrypted mode")
	}
}

func TestHandleControlRPCRequest_SystemAndVoiceMethods(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}, Extra: map[string]any{
		"extensions": map[string]any{
			"enabled": true,
			"load":    true,
			"allow":   []string{"codegen"},
			"deny":    []string{"blocked"},
			"entries": map[string]any{
				"codegen":  map[string]any{"enabled": true, "gateway_methods": []any{"ext.codegen.run", "ext.codegen.status"}},
				"blocked":  map[string]any{"enabled": true, "gateway_methods": []any{"ext.blocked"}},
				"extra":    map[string]any{"enabled": true, "gateway_methods": []any{"ext.extra"}},
				"disabled": map[string]any{"enabled": false, "gateway_methods": []any{"ext.disabled"}},
			},
		},
	}})
	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSupportedMethods,
		Params:     json.RawMessage(`[]`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("handleControlRPCRequest error: %v", err)
	}
	list, ok := res.Result.([]string)
	if !ok || len(list) == 0 {
		t.Fatalf("unexpected result: %#v", res.Result)
	}
	contains := func(target string) bool {
		for _, method := range list {
			if method == target {
				return true
			}
		}
		return false
	}
	if !contains("ext.codegen.run") || !contains("ext.codegen.status") {
		t.Fatalf("expected allowed extension methods in projection: %#v", list)
	}
	if contains("ext.disabled") || contains("ext.blocked") || contains("ext.extra") {
		t.Fatalf("unexpected filtered extension methods present in projection: %#v", list)
	}

	cfgState.Set(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}, Extra: map[string]any{
		"extensions": map[string]any{
			"enabled": false,
			"entries": map[string]any{"codegen": map[string]any{"enabled": true, "gateway_methods": []any{"ext.codegen.run"}}},
		},
	}})
	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSupportedMethods,
		Params:     json.RawMessage(`[]`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("handleControlRPCRequest disabled-extensions error: %v", err)
	}
	list, _ = res.Result.([]string)
	if contains := func(target string) bool {
		for _, method := range list {
			if method == target {
				return true
			}
		}
		return false
	}("ext.codegen.run"); contains {
		t.Fatalf("expected no extension methods when plugins.enabled=false: %#v", list)
	}

	cfgState.Set(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}, Extra: map[string]any{
		"extensions": map[string]any{
			"enabled": true,
			"allow":   "invalid-type",
			"entries": map[string]any{"codegen": map[string]any{"enabled": true, "gateway_methods": []any{"ext.codegen.run"}}},
		},
	}})
	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSupportedMethods,
		Params:     json.RawMessage(`[]`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("handleControlRPCRequest invalid-allowlist error: %v", err)
	}
	list, _ = res.Result.([]string)
	if contains := func(target string) bool {
		for _, method := range list {
			if method == target {
				return true
			}
		}
		return false
	}("ext.codegen.run"); contains {
		t.Fatalf("expected invalid allowlist type to fail-closed extension projection: %#v", list)
	}
}

func TestHandleControlRPCRequest_AuthzDenied(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{
		RequireAuth: true,
		Admins:      []state.ControlAdmin{{PubKey: "admin-a", Methods: []string{"status.get"}}},
	}})
	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller-b",
		Method:     methods.MethodConfigPut,
		Params:     json.RawMessage(`[{"dm":{"policy":"open"}}]`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err == nil {
		t.Fatal("expected authorization error")
	}
}

func TestHandleControlRPCRequest_ListGetAndPut(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})

	putRaw := json.RawMessage(`["allowlist",["npub1","npub2","npub1"]]`)
	putRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodListPut,
		Params:     putRaw,
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("list.put error: %v", err)
	}
	if putRes.Error != "" {
		t.Fatalf("unexpected list.put error result: %s", putRes.Error)
	}

	getRaw := json.RawMessage(`["allowlist"]`)
	getRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodListGet,
		Params:     getRaw,
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("list.get error: %v", err)
	}
	list, ok := getRes.Result.(state.ListDoc)
	if !ok {
		t.Fatalf("unexpected list.get type: %T", getRes.Result)
	}
	if list.Name != "allowlist" || len(list.Items) != 2 {
		t.Fatalf("unexpected list.get payload: %+v", list)
	}
}

func TestHandleControlRPCRequest_ListPutPreconditionConflict(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	if _, err := docs.PutList(context.Background(), "allowlist", state.ListDoc{Version: 2, Name: "allowlist", Items: []string{"a"}}); err != nil {
		t.Fatalf("seed list: %v", err)
	}
	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodListPut,
		Params:     json.RawMessage(`{"name":"allowlist","items":["b"],"expected_version":1}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err == nil {
		t.Fatal("expected precondition conflict")
	}
	if !strings.Contains(err.Error(), "current_version=2") {
		t.Fatalf("expected current version metadata in error, got: %v", err)
	}
}

func TestHandleControlRPCRequest_ListPutExpectedVersionZeroSemantics(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})

	if _, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodListPut,
		Params:     json.RawMessage(`{"name":"allowlist","items":["a"],"expected_version":0}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("expected create-if-missing for expected_version=0, got err=%v", err)
	}

	if _, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodListPut,
		Params:     json.RawMessage(`{"name":"allowlist","items":["b"],"expected_version":0}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute)); err == nil {
		t.Fatal("expected conflict when expected_version=0 and list exists")
	} else {
		var conflict *methods.PreconditionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("expected precondition conflict error, got: %v", err)
		}
		if conflict.CurrentVersion <= 0 {
			t.Fatalf("expected current version in conflict, got: %#v", conflict)
		}
	}

	list, err := docs.GetList(context.Background(), "allowlist")
	if err != nil {
		t.Fatalf("failed to read list after create: %v", err)
	}
	if list.Version != 1 {
		t.Fatalf("expected version=1 after create, got: %d", list.Version)
	}

	if _, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodListPut,
		Params:     json.RawMessage(`{"name":"allowlist","items":["c","d"],"expected_version":1}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("expected successful update with expected_version=1, got err=%v", err)
	}

	list, err = docs.GetList(context.Background(), "allowlist")
	if err != nil {
		t.Fatalf("failed to read list after update: %v", err)
	}
	if list.Version != 2 {
		t.Fatalf("expected version=2 after update, got: %d", list.Version)
	}
	if len(list.Items) != 2 || list.Items[0] != "c" || list.Items[1] != "d" {
		t.Fatalf("expected items=[c,d] after update, got: %v", list.Items)
	}
}

func TestMapGatewayWSError(t *testing.T) {
	shape := mapGatewayWSError(fmt.Errorf("unknown method \"x\""))
	if shape == nil || shape.Code != "INVALID_REQUEST" {
		t.Fatalf("unexpected shape for unknown method: %#v", shape)
	}
	shape = mapGatewayWSError(fmt.Errorf("unknown agent id \"ghost\""))
	if shape == nil || shape.Code != "INVALID_REQUEST" {
		t.Fatalf("unexpected shape for unknown agent: %#v", shape)
	}
	shape = mapGatewayWSError(fmt.Errorf("run not found"))
	if shape == nil || shape.Code != "INVALID_REQUEST" {
		t.Fatalf("unexpected shape for run not found: %#v", shape)
	}
	shape = mapGatewayWSError(fmt.Errorf("forbidden: requires pairing"))
	if shape == nil || shape.Code != "NOT_LINKED" {
		t.Fatalf("unexpected shape for forbidden: %#v", shape)
	}
	conflict := &methods.PreconditionConflictError{Resource: "config", ExpectedVersion: 2, CurrentVersion: 3}
	shape = mapGatewayWSError(conflict)
	if shape == nil || shape.Code != "INVALID_REQUEST" {
		t.Fatalf("unexpected shape for precondition conflict: %#v", shape)
	}
}

func TestDefaultAgentIDCanonicalMain(t *testing.T) {
	if got := defaultAgentID(""); got != "main" {
		t.Fatalf("expected empty agent id to canonicalize to main, got: %q", got)
	}
	if got := defaultAgentID(" MAIN "); got != "main" {
		t.Fatalf("expected MAIN agent id to canonicalize to main, got: %q", got)
	}
}

func TestChatAbortRegistryCancelsInFlight(t *testing.T) {
	registry := newChatAbortRegistry()
	ctx, release := registry.Begin("s1", context.Background())
	defer release()
	if !registry.Abort("s1") {
		t.Fatal("expected abort to cancel in-flight session")
	}
	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected in-flight context cancellation")
	}
}

func TestHandleControlRPCRequest_ChatAbortUsesRegistry(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	registry := newChatAbortRegistry()
	_, release := registry.Begin("s1", context.Background())
	defer release()
	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodChatAbort,
		Params:     json.RawMessage(`{"session_id":"s1"}`),
	}, nil, nil, registry, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("chat.abort error: %v", err)
	}
	payload, ok := res.Result.(map[string]any)
	if !ok || payload["aborted"] != true || payload["aborted_count"] != 1 || payload["abortedCount"] != 1 || payload["sessionId"] != "s1" {
		t.Fatalf("unexpected chat.abort result: %#v", res.Result)
	}
}

func TestHandleControlRPCRequest_DoctorMemoryStatusIncludesStoreHealth(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	idx, err := memory.OpenIndex(filepath.Join(t.TempDir(), "memory.json"))
	if err != nil {
		t.Fatalf("OpenIndex failed: %v", err)
	}
	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodDoctorMemoryStatus,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, idx, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("doctor.memory_status error: %v", err)
	}
	out, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected doctor.memory_status result: %#v", res.Result)
	}
	store, ok := out["store"].(map[string]any)
	if !ok {
		t.Fatalf("expected store status payload, got %#v", out["store"])
	}
	primary, ok := store["primary"].(map[string]any)
	if !ok {
		t.Fatalf("expected primary store status payload, got %#v", store)
	}
	if store["kind"] != "index" || primary["name"] != "json-fts" || primary["available"] != true {
		t.Fatalf("unexpected store status: %#v", store)
	}
}

func TestHandleControlRPCRequest_DoctorMemoryStatusIncludesSessionMemoryHealth(t *testing.T) {
	prevSessionStore := controlSessionStore
	prevRuntime := controlSessionMemoryRuntime
	defer func() {
		controlSessionStore = prevSessionStore
		controlSessionMemoryRuntime = prevRuntime
	}()

	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		Extra: map[string]any{
			"memory": map[string]any{
				"session_memory": map[string]any{
					"enabled":                    true,
					"init_chars":                 9000,
					"update_chars":               4500,
					"tool_calls_between_updates": 4,
				},
			},
		},
	})
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	workspaceDir := t.TempDir()
	path, err := memory.WriteSessionMemoryFile(workspaceDir, "sess-a", testSessionMemoryDocument("Session continuity is being promoted into active recall and diagnostics."))
	if err != nil {
		t.Fatalf("write session memory: %v", err)
	}
	if err := sessionStore.Put("sess-a", state.SessionEntry{
		SessionID:                "sess-a",
		SpawnedWorkspace:         workspaceDir,
		SessionMemoryFile:        path,
		SessionMemoryInitialized: true,
		SessionMemoryUpdatedAt:   1712345678,
		FileMemorySurfaced: map[string]string{
			"docs/plan.md":   "topic:planning",
			"src/runtime.go": "query:runtime",
		},
		RecentMemoryRecall: []state.MemoryRecallSample{{
			RecordedAtMS: 1712345678123,
			TurnID:       "turn-a",
			Strategy:     "deterministic",
			InjectedAny:  true,
		}},
		CompactionCount: 2,
		MemoryFlushAt:   1712345600,
	}); err != nil {
		t.Fatalf("seed initialized session: %v", err)
	}
	if err := sessionStore.Put("sess-b", state.SessionEntry{
		SessionID:                     "sess-b",
		SessionMemoryObservedChars:    1200,
		SessionMemoryPendingChars:     300,
		SessionMemoryPendingToolCalls: 2,
	}); err != nil {
		t.Fatalf("seed pending session: %v", err)
	}
	staleWorkspace := t.TempDir()
	if err := sessionStore.Put("sess-c", state.SessionEntry{
		SessionID:                "sess-c",
		SpawnedWorkspace:         staleWorkspace,
		SessionMemoryFile:        filepath.Join(staleWorkspace, "session-memory", "wrong-session.md"),
		SessionMemoryInitialized: true,
		SessionMemoryUpdatedAt:   1712346789,
		FileMemorySurfaced: map[string]string{
			"notes/ops.md": "topic:ops",
		},
		RecentMemoryRecall: []state.MemoryRecallSample{{
			RecordedAtMS: 1712346789123,
			TurnID:       "turn-c",
			Strategy:     "deterministic",
			InjectedAny:  true,
		}},
		CompactionCount: 3,
		MemoryFlushAt:   1712346800,
	}); err != nil {
		t.Fatalf("seed stale session: %v", err)
	}
	runtime := newSessionMemoryRuntime(sessionStore, nil)
	runtime.inFlight["sess-b"] = time.Now()
	controlSessionStore = sessionStore
	controlSessionMemoryRuntime = runtime

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodDoctorMemoryStatus,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, &memoryStoreStub{}, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("doctor.memory_status error: %v", err)
	}
	out, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected doctor.memory_status result: %#v", res.Result)
	}
	sessionMemory, ok := out["session_memory"].(map[string]any)
	if !ok {
		t.Fatalf("expected session_memory payload, got %#v", out["session_memory"])
	}
	if sessionMemory["enabled"] != true || sessionMemory["runtime_available"] != true || sessionMemory["session_store_available"] != true {
		t.Fatalf("unexpected session memory availability payload: %#v", sessionMemory)
	}
	if sessionMemory["tracked_sessions"] != 3 || sessionMemory["initialized_sessions"] != 2 || sessionMemory["artifact_sessions"] != 1 {
		t.Fatalf("unexpected session memory counts: %#v", sessionMemory)
	}
	if sessionMemory["stale_artifact_sessions"] != 1 {
		t.Fatalf("expected one stale artifact session, got %#v", sessionMemory)
	}
	if sessionMemory["pending_sessions"] != 1 || sessionMemory["in_flight_sessions"] != 1 {
		t.Fatalf("unexpected pending/in-flight session memory state: %#v", sessionMemory)
	}
	if sessionMemory["latest_update_unix"] != int64(1712346789) {
		t.Fatalf("expected latest session memory timestamp, got %#v", sessionMemory)
	}
	fileMemory, ok := out["file_memory"].(map[string]any)
	if !ok {
		t.Fatalf("expected file_memory payload, got %#v", out["file_memory"])
	}
	if fileMemory["session_store_available"] != true || fileMemory["sessions_with_surface_state"] != 2 || fileMemory["surfaced_paths"] != 3 {
		t.Fatalf("unexpected file memory surface payload: %#v", fileMemory)
	}
	if fileMemory["sessions_with_recent_recall"] != 2 || fileMemory["recent_recall_samples"] != 2 {
		t.Fatalf("unexpected file memory recall payload: %#v", fileMemory)
	}
	if fileMemory["latest_recall_recorded_at_ms"] != int64(1712346789123) {
		t.Fatalf("expected latest recall timestamp, got %#v", fileMemory)
	}
	maintenance, ok := out["maintenance"].(map[string]any)
	if !ok {
		t.Fatalf("expected maintenance payload, got %#v", out["maintenance"])
	}
	if maintenance["session_store_available"] != true || maintenance["sessions_with_compaction"] != 2 || maintenance["total_compactions"] != int64(5) {
		t.Fatalf("unexpected maintenance compaction payload: %#v", maintenance)
	}
	if maintenance["sessions_with_memory_flush"] != 2 || maintenance["latest_memory_flush_unix"] != int64(1712346800) {
		t.Fatalf("unexpected maintenance flush payload: %#v", maintenance)
	}
}

func TestHandleControlRPCRequest_OperationalSemantics(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		Relays:  state.RelayPolicy{Read: []string{"wss://read"}, Write: []string{"wss://write"}},
	})
	usage := newUsageTracker(time.Now().Add(-time.Minute))
	logs := newRuntimeLogBuffer(32)
	logs.Append("info", "first")
	channels := newChannelRuntimeState()
	prevObserve := toolbuiltin.RuntimeObserveProvider{}
	toolbuiltin.SetRuntimeObserveProvider(toolbuiltin.RuntimeObserveProvider{Observe: func(_ context.Context, req toolbuiltin.RuntimeObserveRequest) (map[string]any, error) {
		return map[string]any{"events": map[string]any{"cursor": req.EventCursor, "events": []map[string]any{{"event": "tool.start", "agent_id": req.Filters.AgentID}}, "truncated": false, "reset": false}, "timed_out": false, "waited_ms": int64(0)}, nil
	}})
	defer toolbuiltin.SetRuntimeObserveProvider(prevObserve)

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodLogsTail,
		Params:     json.RawMessage(`{"limit":10}`),
	}, nil, nil, nil, usage, logs, channels, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("logs.tail error: %v", err)
	}
	out, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected logs.tail result: %#v", res.Result)
	}
	lines, _ := out["lines"].([]string)
	if len(lines) == 0 {
		t.Fatalf("expected log lines, got: %#v", out)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodRuntimeObserve,
		Params:     json.RawMessage(`{"include_events":true,"include_logs":false,"event_cursor":12,"event_limit":5,"agent_id":"agent-1"}`),
	}, nil, nil, nil, usage, logs, channels, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("runtime.observe error: %v", err)
	}
	obsOut, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected runtime.observe result: %#v", res.Result)
	}
	eventsSection, _ := obsOut["events"].(map[string]any)
	events, _ := eventsSection["events"].([]map[string]any)
	if len(events) != 1 || events[0]["event"] != "tool.start" || events[0]["agent_id"] != "agent-1" {
		t.Fatalf("unexpected runtime.observe payload: %#v", obsOut)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodChannelsLogout,
		Params:     json.RawMessage(`{"channel":"nostr"}`),
	}, nil, nil, nil, usage, logs, channels, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("channels.logout error: %v", err)
	}
	out, _ = res.Result.(map[string]any)
	if out["loggedOut"] != true {
		t.Fatalf("unexpected channels.logout payload: %#v", out)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodUsageStatus,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, usage, logs, channels, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("usage.status error: %v", err)
	}
	out, _ = res.Result.(map[string]any)
	totals, _ := out["totals"].(map[string]any)
	if totals["control_calls"].(int64) < 1 {
		t.Fatalf("expected control call accounting, got: %#v", totals)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodUsageCost,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, usage, logs, channels, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("usage.cost error: %v", err)
	}
	out, _ = res.Result.(map[string]any)
	if _, ok := out["estimate"]; !ok {
		t.Fatalf("expected usage estimate in payload: %#v", out)
	}
}

func TestHandleControlRPCRequest_AgentMethods(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	controlAgentRuntime = stubAgentRuntime{}
	controlAgentJobs = newAgentJobRegistry()

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgent,
		Params:     json.RawMessage(`{"message":"hello","session_id":"s1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agent error: %v", err)
	}
	out, _ := res.Result.(map[string]any)
	runID, _ := out["run_id"].(string)
	if strings.TrimSpace(runID) == "" || out["runId"] != runID {
		t.Fatalf("expected compatibility run id fields, got: %#v", res.Result)
	}
	if _, ok := out["acceptedAt"]; !ok {
		t.Fatalf("expected acceptedAt alias, got: %#v", res.Result)
	}

	waitRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentWait,
		Params:     json.RawMessage(fmt.Sprintf(`{"run_id":%q,"timeout_ms":1000}`, runID)),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agent.wait error: %v", err)
	}
	out, _ = waitRes.Result.(map[string]any)
	if out["status"] != "ok" || out["runId"] != runID {
		t.Fatalf("unexpected wait result: %#v", waitRes.Result)
	}
	if _, ok := out["startedAt"]; !ok {
		t.Fatalf("expected startedAt alias, got: %#v", waitRes.Result)
	}
	if _, ok := out["endedAt"]; !ok {
		t.Fatalf("expected endedAt alias, got: %#v", waitRes.Result)
	}

	identityRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentIdentityGet,
		Params:     json.RawMessage(`{"session_id":"s1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agent.identity.get error: %v", err)
	}
	out, _ = identityRes.Result.(map[string]any)
	if out["agent_id"] != "main" || out["agentId"] != "main" || out["sessionId"] != "s1" {
		t.Fatalf("unexpected identity result: %#v", identityRes.Result)
	}
	if _, ok := out["displayName"]; !ok {
		t.Fatalf("expected displayName alias, got: %#v", identityRes.Result)
	}
}

func TestHandleControlRPCRequest_RelayPolicyGet(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		Relays:  state.RelayPolicy{Read: []string{"wss://read"}, Write: []string{"wss://write"}},
	})
	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodRelayPolicyGet,
		Params:     json.RawMessage(`[]`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("relay.policy.get error: %v", err)
	}
	view, ok := res.Result.(methods.RelayPolicyResponse)
	if !ok {
		t.Fatalf("unexpected result type: %T", res.Result)
	}
	if len(view.ReadRelays) != 1 || view.ReadRelays[0] != "wss://read" {
		t.Fatalf("unexpected read relays: %+v", view.ReadRelays)
	}
}

func TestHandleControlRPCRequest_TaskLifecycleMethods(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})

	createRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodTasksCreate,
		Params:     json.RawMessage(`{"task":{"goal_id":"goal-1","instructions":"  Review deployment output  ","status":"blocked","assigned_agent":" Worker "}}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("tasks.create error: %v", err)
	}
	created, ok := createRes.Result.(methods.TasksGetResponse)
	if !ok {
		t.Fatalf("unexpected tasks.create result: %#v", createRes.Result)
	}
	if created.Task.TaskID == "" {
		t.Fatal("expected generated task id")
	}
	if created.Task.Title != "Review deployment output" {
		t.Fatalf("unexpected title: %#v", created.Task)
	}
	if created.Task.Status != state.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %q", created.Task.Status)
	}
	if created.Task.AssignedAgent != "worker" {
		t.Fatalf("expected normalized assigned agent, got %q", created.Task.AssignedAgent)
	}
	if len(created.Runs) != 0 {
		t.Fatalf("expected no runs on create, got %#v", created.Runs)
	}

	listRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodTasksList,
		Params:     json.RawMessage(fmt.Sprintf(`{"status":"blocked","assignedAgent":"worker","goalId":"goal-1","limit":10}`)),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("tasks.list error: %v", err)
	}
	listed, ok := listRes.Result.(methods.TasksListResponse)
	if !ok {
		t.Fatalf("unexpected tasks.list result: %#v", listRes.Result)
	}
	if listed.Count != 1 || len(listed.Tasks) != 1 || listed.Tasks[0].TaskID != created.Task.TaskID {
		t.Fatalf("unexpected tasks.list payload: %#v", listed)
	}

	resumeRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodTasksResume,
		Params:     json.RawMessage(fmt.Sprintf(`{"taskId":%q,"reason":"retry after unblock"}`, created.Task.TaskID)),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("tasks.resume error: %v", err)
	}
	resumed, ok := resumeRes.Result.(methods.TasksGetResponse)
	if !ok {
		t.Fatalf("unexpected tasks.resume result: %#v", resumeRes.Result)
	}
	if resumed.Task.Status != state.TaskStatusReady {
		t.Fatalf("expected ready task after resume, got %q", resumed.Task.Status)
	}
	if resumed.Task.CurrentRunID == "" || resumed.Task.LastRunID == "" {
		t.Fatalf("expected run ids after resume, got %#v", resumed.Task)
	}
	if len(resumed.Runs) != 1 || resumed.Runs[0].Status != state.TaskRunStatusQueued {
		t.Fatalf("expected queued run after resume, got %#v", resumed.Runs)
	}

	getRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodTasksGet,
		Params:     json.RawMessage(fmt.Sprintf(`{"taskId":%q,"runsLimit":5}`, created.Task.TaskID)),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("tasks.get error: %v", err)
	}
	fetched, ok := getRes.Result.(methods.TasksGetResponse)
	if !ok {
		t.Fatalf("unexpected tasks.get result: %#v", getRes.Result)
	}
	if fetched.Task.CurrentRunID != resumed.Task.CurrentRunID || len(fetched.Runs) != 1 {
		t.Fatalf("unexpected tasks.get payload: %#v", fetched)
	}

	cancelRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodTasksCancel,
		Params:     json.RawMessage(fmt.Sprintf(`{"task_id":%q,"reason":"operator cancelled"}`, created.Task.TaskID)),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("tasks.cancel error: %v", err)
	}
	cancelled, ok := cancelRes.Result.(methods.TasksGetResponse)
	if !ok {
		t.Fatalf("unexpected tasks.cancel result: %#v", cancelRes.Result)
	}
	if cancelled.Task.Status != state.TaskStatusCancelled {
		t.Fatalf("expected cancelled task, got %q", cancelled.Task.Status)
	}
	if cancelled.Task.CurrentRunID != "" || cancelled.Task.LastRunID == "" {
		t.Fatalf("expected current run cleared after cancel, got %#v", cancelled.Task)
	}
	if len(cancelled.Runs) != 1 || cancelled.Runs[0].Status != state.TaskRunStatusCancelled {
		t.Fatalf("expected cancelled run, got %#v", cancelled.Runs)
	}
}

func TestHandleControlRPCRequest_MCPMethods(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}, Relays: state.RelayPolicy{Read: []string{"wss://relay.example"}, Write: []string{"wss://relay.example"}}})
	var mgr *mcppkg.Manager
	oldMCPOps := controlMCPOps
	controlMCPOps = newMCPOpsController(&mgr, agent.NewToolRegistry(), nil, cfgState, docs)
	defer func() { controlMCPOps = oldMCPOps }()

	putRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodMCPPut,
		Params:     json.RawMessage(`{"server":"demo","config":{"type":"stdio","command":"npx"}}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, agent.NewToolRegistry(), nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("mcp.put error: %v", err)
	}
	putOut, ok := putRes.Result.(map[string]any)
	if !ok || putOut["ok"] != true {
		t.Fatalf("unexpected mcp.put result: %#v", putRes.Result)
	}

	getRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodMCPGet,
		Params:     json.RawMessage(`{"server":"demo"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, agent.NewToolRegistry(), nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("mcp.get error: %v", err)
	}
	getOut, ok := getRes.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected mcp.get result: %#v", getRes.Result)
	}
	server, _ := getOut["server"].(map[string]any)
	if fmt.Sprint(server["name"]) != "demo" {
		t.Fatalf("unexpected mcp.get server payload: %#v", getOut)
	}

	listRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodMCPList,
		Params:     nil,
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, agent.NewToolRegistry(), nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("mcp.list error: %v", err)
	}
	listOut, ok := listRes.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected mcp.list result: %#v", listRes.Result)
	}
	servers, _ := listOut["servers"].([]map[string]any)
	if len(servers) != 1 {
		t.Fatalf("unexpected mcp.list result: %#v", listRes.Result)
	}
}

func TestHandleControlRPCRequest_StatusIncludesMCPTelemetry(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		DM:      state.DMPolicy{Policy: "open"},
		Relays:  state.RelayPolicy{Read: []string{"wss://relay.example"}, Write: []string{"wss://relay.example"}},
		Extra: map[string]any{
			"mcp": map[string]any{
				"enabled": true,
				"servers": map[string]any{
					"remote": map[string]any{
						"enabled": true,
						"type":    "http",
						"url":     "https://mcp.example.com/http",
					},
				},
			},
		},
	})
	resolved := mcppkg.ResolveConfigDoc(cfgState.Get())
	mgr := mcppkg.NewManager()
	mgr.SetConnectFunc(func(_ context.Context, _ string, _ mcppkg.ServerConfig) (*mcppkg.ServerConnection, error) {
		return nil, errors.New("401 unauthorized")
	})
	if err := mgr.ApplyConfig(context.Background(), resolved); err == nil {
		t.Fatal("expected auth-required apply error for status telemetry test")
	}

	oldMCPOps := controlMCPOps
	controlMCPOps = newMCPOpsController(&mgr, agent.NewToolRegistry(), nil, cfgState, state.NewDocsRepository(newTestStore(), "author"))
	defer func() {
		controlMCPOps = oldMCPOps
		_ = mgr.Close()
	}()

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodStatus,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("status.get error: %v", err)
	}
	status, ok := res.Result.(methods.StatusResponse)
	if !ok {
		t.Fatalf("unexpected status result type: %T", res.Result)
	}
	if status.MCP == nil {
		t.Fatalf("expected mcp telemetry in status response, got %+v", status)
	}
	if status.MCP.Summary.NeedsAuthServers != 1 || status.MCP.Summary.TotalServers != 1 {
		t.Fatalf("unexpected mcp summary: %#v", status.MCP.Summary)
	}
	if len(status.MCP.Servers) != 1 || status.MCP.Servers[0].State != string(mcppkg.ConnectionStateNeedsAuth) {
		t.Fatalf("unexpected mcp server telemetry: %#v", status.MCP.Servers)
	}
}

func TestFilteredMCPLifecycleTrackerEmitsSnapshotsAndRemovals(t *testing.T) {
	capture := &capturingEmitter{}
	tracker := newFilteredMCPLifecycleTracker()
	resolved := mcppkg.Config{
		Enabled: true,
		FilteredServers: map[string]mcppkg.FilteredServer{
			"pending-remote": {
				ResolvedServerConfig: mcppkg.ResolvedServerConfig{
					Name:         "pending-remote",
					ServerConfig: mcppkg.ServerConfig{Enabled: true, Type: "http", URL: "https://pending.example.com/http"},
					Source:       mcppkg.ConfigSourceExtraMCP,
					Precedence:   100,
					Signature:    "pending-sig",
				},
				PolicyStatus: mcppkg.PolicyStatusApprovalRequired,
				PolicyReason: mcppkg.PolicyReasonRemoteApproval,
			},
		},
	}

	tracker.Emit(capture, resolved, "config.snapshot", 123)
	events := capture.eventsByName(gatewayws.EventMCPLifecycle)
	if len(events) != 1 {
		t.Fatalf("expected one filtered lifecycle event, got %d", len(events))
	}
	payload, ok := events[0].(gatewayws.MCPLifecyclePayload)
	if !ok {
		t.Fatalf("unexpected filtered lifecycle payload type: %T", events[0])
	}
	if payload.Name != "pending-remote" || payload.State != string(mcppkg.PolicyStatusApprovalRequired) {
		t.Fatalf("unexpected filtered lifecycle payload: %+v", payload)
	}
	if payload.PolicyStatus != string(mcppkg.PolicyStatusApprovalRequired) || payload.PolicyReason != string(mcppkg.PolicyReasonRemoteApproval) {
		t.Fatalf("expected policy metadata on lifecycle payload, got %+v", payload)
	}
	if payload.RuntimePresent || payload.Healthy {
		t.Fatalf("expected filtered lifecycle payload to remain non-runtime/unhealthy, got %+v", payload)
	}

	tracker.Emit(capture, mcppkg.Config{Enabled: true}, "config.snapshot", 124)
	events = capture.eventsByName(gatewayws.EventMCPLifecycle)
	if len(events) != 2 {
		t.Fatalf("expected tombstone lifecycle event after removal, got %d", len(events))
	}
	removed, ok := events[1].(gatewayws.MCPLifecyclePayload)
	if !ok {
		t.Fatalf("unexpected tombstone lifecycle payload type: %T", events[1])
	}
	if !removed.Removed || removed.Name != "pending-remote" || removed.Reason != "config.snapshot.removed" {
		t.Fatalf("unexpected filtered removal payload: %+v", removed)
	}
}

func TestFilteredMCPLifecycleTrackerSuppressesTombstoneWhenServerBecomesActive(t *testing.T) {
	capture := &capturingEmitter{}
	tracker := newFilteredMCPLifecycleTracker()
	filtered := mcppkg.Config{
		Enabled: true,
		FilteredServers: map[string]mcppkg.FilteredServer{
			"demo": {
				ResolvedServerConfig: mcppkg.ResolvedServerConfig{
					Name:         "demo",
					ServerConfig: mcppkg.ServerConfig{Enabled: true, Type: "http", URL: "https://pending.example.com/http"},
					Source:       mcppkg.ConfigSourceExtraMCP,
					Precedence:   100,
					Signature:    "demo-sig",
				},
				PolicyStatus: mcppkg.PolicyStatusApprovalRequired,
				PolicyReason: mcppkg.PolicyReasonRemoteApproval,
			},
		},
	}

	tracker.Emit(capture, filtered, "config.snapshot", 123)
	tracker.Emit(capture, mcppkg.Config{
		Enabled: true,
		Servers: map[string]mcppkg.ResolvedServerConfig{
			"demo": {
				Name:         "demo",
				ServerConfig: mcppkg.ServerConfig{Enabled: true, Type: "http", URL: "https://allowed.example.com/http"},
				Source:       mcppkg.ConfigSourceExtraMCP,
				Precedence:   100,
				Signature:    "demo-sig",
			},
		},
	}, "config.snapshot", 124)

	events := capture.eventsByName(gatewayws.EventMCPLifecycle)
	if len(events) != 1 {
		t.Fatalf("expected no filtered tombstone when server remains in resolved inventory, got %d events", len(events))
	}
}

func TestFilteredMCPLifecycleTrackerEmitsDisabledReplacement(t *testing.T) {
	capture := &capturingEmitter{}
	tracker := newFilteredMCPLifecycleTracker()
	tracker.Emit(capture, mcppkg.Config{
		Enabled: true,
		FilteredServers: map[string]mcppkg.FilteredServer{
			"demo": {
				ResolvedServerConfig: mcppkg.ResolvedServerConfig{
					Name:         "demo",
					ServerConfig: mcppkg.ServerConfig{Enabled: true, Type: "http", URL: "https://pending.example.com/http"},
					Source:       mcppkg.ConfigSourceExtraMCP,
					Precedence:   100,
					Signature:    "demo-sig",
				},
				PolicyStatus: mcppkg.PolicyStatusApprovalRequired,
				PolicyReason: mcppkg.PolicyReasonRemoteApproval,
			},
		},
	}, "config.snapshot", 123)
	tracker.Emit(capture, mcppkg.Config{
		Enabled: true,
		DisabledServers: map[string]mcppkg.ResolvedServerConfig{
			"demo": {
				Name:         "demo",
				ServerConfig: mcppkg.ServerConfig{Enabled: false, Type: "http", URL: "https://pending.example.com/http"},
				Source:       mcppkg.ConfigSourceExtraMCP,
				Precedence:   100,
				Signature:    "demo-sig",
			},
		},
	}, "config.snapshot", 124)

	events := capture.eventsByName(gatewayws.EventMCPLifecycle)
	if len(events) != 2 {
		t.Fatalf("expected disabled replacement event, got %d", len(events))
	}
	payload, ok := events[1].(gatewayws.MCPLifecyclePayload)
	if !ok {
		t.Fatalf("unexpected disabled replacement payload type: %T", events[1])
	}
	if payload.State != string(mcppkg.ConnectionStateDisabled) || payload.PreviousState != string(mcppkg.PolicyStatusApprovalRequired) {
		t.Fatalf("unexpected disabled replacement lifecycle payload: %+v", payload)
	}
}

func TestBuildMCPLifecyclePayloadRemovalClearsRuntimePresence(t *testing.T) {
	payload := buildMCPLifecyclePayload(mcppkg.StateChange{
		Server: mcppkg.ServerStateSnapshot{
			Name:    "demo",
			State:   mcppkg.ConnectionStateConnected,
			Enabled: true,
		},
		PreviousState: mcppkg.ConnectionStateConnected,
		Reason:        "config.removed",
		Removed:       true,
	}, 123)
	if !payload.Removed {
		t.Fatalf("expected removed payload, got %+v", payload)
	}
	if payload.RuntimePresent || payload.Healthy {
		t.Fatalf("expected removed payload to clear runtime/healthy flags, got %+v", payload)
	}
}

func TestHandleControlRPCRequest_ConfigSetApplyPatchSchema(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		DM:      state.DMPolicy{Policy: "pairing"},
		Relays:  state.RelayPolicy{Read: []string{"wss://relay"}, Write: []string{"wss://relay"}},
	})

	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigSet,
		Params:     json.RawMessage(`{"key":"dm.policy","value":"open"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("config.set error: %v", err)
	}
	if got := cfgState.Get().DM.Policy; got != "open" {
		t.Fatalf("dm.policy=%q want open", got)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigApply,
		Params:     json.RawMessage(`{"config":{"version":2,"dm":{"policy":"pairing"},"relays":{"read":["wss://r1"],"write":["wss://r1"]}}}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("config.apply error: %v", err)
	}
	if got := cfgState.Get().DM.Policy; got != "pairing" {
		t.Fatalf("dm.policy=%q want pairing", got)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigPatch,
		Params:     json.RawMessage(`{"patch":{"dm":{"policy":"open"}}}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("config.patch error: %v", err)
	}
	if got := cfgState.Get().DM.Policy; got != "open" {
		t.Fatalf("dm.policy=%q want open after patch", got)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigPatch,
		Params:     json.RawMessage(`{"patch":{"plugins":{"entries":{"codegen":{"enabled":true,"gatewayMethods":["ext.codegen.run"],"env":{"OPENAI_API_KEY":"sk-1","KEEP":"y"}}}}}}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("config.patch plugins error: %v", err)
	}
	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigSet,
		Params:     json.RawMessage(`{"key":"plugins.deny","value":["rogue-plugin"]}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("config.set plugins.deny error: %v", err)
	}
	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigSet,
		Params:     json.RawMessage(`{"key":"plugins.load.paths","value":["./extensions"]}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("config.set plugins.load.paths error: %v", err)
	}
	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigPatch,
		Params:     json.RawMessage(`{"patch":{"plugins":{"entries":{"codegen":{"env":{"OPENAI_API_KEY":""}}}}}}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("config.patch plugins env merge error: %v", err)
	}
	rawExt, _ := cfgState.Get().Extra["extensions"].(map[string]any)
	rawEntries, _ := rawExt["entries"].(map[string]any)
	codegen, _ := rawEntries["codegen"].(map[string]any)
	envMap, err := methodsAnyToStringMapForTest(codegen["env"])
	if err != nil {
		t.Fatalf("unexpected plugin env map type after patch merge: %v", err)
	}
	if _, ok := envMap["OPENAI_API_KEY"]; ok {
		t.Fatalf("expected OPENAI_API_KEY removed after env patch merge, got: %#v", envMap)
	}
	if envMap["KEEP"] != "y" {
		t.Fatalf("expected KEEP env var to be preserved after env patch merge, got: %#v", envMap)
	}
	deny, _ := rawExt["deny"].([]string)
	if len(deny) != 1 || deny[0] != "rogue-plugin" {
		t.Fatalf("expected plugins.deny to be persisted, got: %#v", rawExt)
	}
	loadPaths, _ := rawExt["load_paths"].([]string)
	if len(loadPaths) != 1 || loadPaths[0] != "./extensions" {
		t.Fatalf("expected plugins.load.paths alias persistence, got: %#v", rawExt)
	}

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigSchema,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("config.schema error: %v", err)
	}
	schema, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected config.schema result type: %T", res.Result)
	}
	fields, ok := schema["fields"].([]string)
	if !ok || len(fields) == 0 {
		t.Fatalf("unexpected config.schema payload: %#v", res.Result)
	}
	plugins, _ := schema["plugins"].(map[string]any)
	entries, _ := plugins["entries"].([]map[string]any)
	if len(entries) == 0 {
		t.Fatalf("expected plugin schema entries in config.schema payload: %#v", schema)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSupportedMethods,
		Params:     json.RawMessage(`[]`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("supported.methods error: %v", err)
	}
	list, _ := res.Result.([]string)
	hasExtMethod := false
	for _, method := range list {
		if method == "ext.codegen.run" {
			hasExtMethod = true
			break
		}
	}
	if !hasExtMethod {
		t.Fatalf("expected ext.codegen.run in supported methods after plugin patch: %#v", list)
	}
}

func TestHandleControlRPCRequest_ConfigGetResponseShape(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		DM:      state.DMPolicy{Policy: "open"},
	})

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigGet,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("config.get error: %v", err)
	}
	out, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("config.get result should be map[string]any, got %T (%#v)", res.Result, res.Result)
	}
	if _, hasConfig := out["config"]; !hasConfig {
		t.Fatalf("config.get result missing 'config' key: %#v", out)
	}
	if hash, _ := out["base_hash"].(string); hash == "" {
		t.Fatalf("config.get result missing 'base_hash': %#v", out)
	}
	if hash, _ := out["hash"].(string); hash == "" {
		t.Fatalf("config.get result missing 'hash': %#v", out)
	}
}

func TestHandleControlRPCRequest_ConfigPutBaseHashConflict(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		DM:      state.DMPolicy{Policy: "open"},
		Relays:  state.RelayPolicy{Read: []string{"wss://relay"}, Write: []string{"wss://relay"}},
	})

	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigPut,
		Params:     json.RawMessage(`{"config":{"dm":{"policy":"pairing"}},"base_hash":"deadbeef"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !errors.Is(err, methods.ErrConfigConflict) {
		t.Fatalf("expected ErrConfigConflict, got: %v", err)
	}
}

func TestHandleControlRPCRequest_ConfigPutIncludesRestartPending(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		DM:      state.DMPolicy{Policy: "open"},
		Relays:  state.RelayPolicy{Read: []string{"wss://relay"}, Write: []string{"wss://relay"}},
	})

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigPut,
		Params:     json.RawMessage(`{"config":{"dm":{"policy":"pairing"},"relays":{"read":["wss://relay"],"write":["wss://relay"]}}}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("config.put error: %v", err)
	}
	out, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("config.put result should be map[string]any, got %T (%#v)", res.Result, res.Result)
	}
	if pending, ok := out["restart_pending"].(bool); !ok || pending {
		t.Fatalf("expected restart_pending=false for dm policy change, got %#v", out)
	}
}

func TestHandleControlRPCRequest_ChatHistoryAndSessionViews(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	transcript := state.NewTranscriptRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}, Agent: state.AgentPolicy{DefaultModel: "gpt-test"}})

	if _, err := docs.PutSession(context.Background(), "s1", state.SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer", LastInboundAt: time.Now().Unix()}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for i, text := range []string{"Need briefing", "Here is the latest update"} {
		role := "user"
		if i == 1 {
			role = "assistant"
		}
		_, _ = transcript.PutEntry(context.Background(), state.TranscriptEntryDoc{Version: 1, SessionID: "s1", EntryID: fmt.Sprintf("e%d", i), Role: role, Text: text, Unix: time.Now().Unix() + int64(i)})
	}
	oldSessionStore := controlSessionStore
	t.Cleanup(func() { controlSessionStore = oldSessionStore })
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	fresh := true
	if err := sessionStore.Put("s1", state.SessionEntry{SessionID: "s1", AgentID: "main", Label: "Briefing", LastChannel: "nostr", LastTo: "peer", InputTokens: 12, OutputTokens: 7, TotalTokens: 19, TotalTokensFresh: &fresh, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("seed session store: %v", err)
	}
	controlSessionStore = sessionStore

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSessionsList,
		Params:     json.RawMessage(`{"limit":10,"label":"Briefing","agentId":"main","includeDerivedTitles":true,"includeLastMessage":true}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("sessions.list error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	sessions, ok := payload["sessions"].([]map[string]any)
	if !ok || len(sessions) != 1 {
		t.Fatalf("unexpected sessions.list payload: %#v", res.Result)
	}
	if payload["path"] != sessionStore.Path() || payload["count"].(int) != 1 || payload["total"].(int) != 1 {
		t.Fatalf("unexpected sessions.list envelope: %#v", payload)
	}
	if sessions[0]["key"] != "s1" || sessions[0]["label"] != "Briefing" || sessions[0]["lastMessagePreview"] != "Here is the latest update" || sessions[0]["model"] != "gpt-test" {
		t.Fatalf("unexpected sessions.list row: %#v", sessions[0])
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSessionsPreview,
		Params:     json.RawMessage(`{"session_id":"s1","limit":5}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("sessions.preview error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if len(payload["preview"].([]state.TranscriptEntryDoc)) != 2 {
		t.Fatalf("unexpected sessions.preview payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodChatHistory,
		Params:     json.RawMessage(`{"session_id":"s1","limit":5}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("chat.history error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if len(payload["entries"].([]state.TranscriptEntryDoc)) != 2 {
		t.Fatalf("unexpected chat.history payload: %#v", res.Result)
	}
}

func TestHandleControlRPCRequest_ConfigSetAndSessionMutations(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	transcript := state.NewTranscriptRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		DM:      state.DMPolicy{Policy: "pairing"},
		Relays:  state.RelayPolicy{Read: []string{"wss://relay"}, Write: []string{"wss://relay"}},
	})
	if _, err := docs.PutSession(context.Background(), "s1", state.SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer", LastInboundAt: time.Now().Unix()}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for i := 0; i < 3; i++ {
		_, _ = transcript.PutEntry(context.Background(), state.TranscriptEntryDoc{Version: 1, SessionID: "s1", EntryID: fmt.Sprintf("e%d", i), Role: "user", Text: "hi", Unix: time.Now().Unix() + int64(i)})
	}

	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigSet,
		Params:     json.RawMessage(`{"key":"dm.policy","value":"open"}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("config.set error: %v", err)
	}
	if got := cfgState.Get().DM.Policy; got != "open" {
		t.Fatalf("dm.policy=%q want open", got)
	}

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSessionsPatch,
		Params:     json.RawMessage(`{"session_id":"s1","meta":{"k":"v"}}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("sessions.patch error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	if payload["ok"] != true {
		t.Fatalf("unexpected sessions.patch result: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSessionsCompact,
		Params:     json.RawMessage(`{"session_id":"s1","keep":1}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("sessions.compact error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["dropped"].(int) < 1 || payload["sessionId"] != "s1" {
		t.Fatalf("unexpected sessions.compact result: %#v", res.Result)
	}
	if _, ok := payload["fromEntries"]; !ok {
		t.Fatalf("expected fromEntries alias, got: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSessionsDelete,
		Params:     json.RawMessage(`{"session_id":"s1"}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("sessions.delete error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["deleted"] != true || payload["sessionId"] != "s1" {
		t.Fatalf("unexpected sessions.delete result: %#v", res.Result)
	}
}

func TestHandleControlRPCRequest_SessionsCompactHandlesLargeTranscripts(t *testing.T) {
	oldRuntime := controlAgentRuntime
	controlAgentRuntime = nil
	t.Cleanup(func() { controlAgentRuntime = oldRuntime })

	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	transcript := state.NewTranscriptRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	if _, err := docs.PutSession(context.Background(), "s-large", state.SessionDoc{Version: 1, SessionID: "s-large", PeerPubKey: "peer", LastInboundAt: time.Now().Unix()}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for i := 0; i < 2505; i++ {
		_, err := transcript.PutEntry(context.Background(), state.TranscriptEntryDoc{
			Version:   1,
			SessionID: "s-large",
			EntryID:   fmt.Sprintf("e%04d", i),
			Role:      "user",
			Text:      fmt.Sprintf("msg %d", i),
			Unix:      int64(i + 1),
		})
		if err != nil {
			t.Fatalf("put transcript entry %d: %v", i, err)
		}
	}

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSessionsCompact,
		Params:     json.RawMessage(`{"session_id":"s-large","keep":5}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("sessions.compact error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	if got := payload["fromEntries"]; got != 2505 {
		t.Fatalf("expected fromEntries=2505, got %#v", got)
	}
	if got := payload["dropped"]; got != 2500 {
		t.Fatalf("expected dropped=2500, got %#v", got)
	}
	entries, err := transcript.ListSessionAll(context.Background(), "s-large")
	if err != nil {
		t.Fatalf("list session: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 remaining entries, got %d", len(entries))
	}
	if entries[0].EntryID != "e2500" || entries[4].EntryID != "e2504" {
		t.Fatalf("unexpected remaining entries: first=%s last=%s", entries[0].EntryID, entries[4].EntryID)
	}
}

func TestHandleControlRPCRequest_SessionsCompactPrefersLightModelSummary(t *testing.T) {
	prevRuntime := controlAgentRuntime
	prevRegistry := controlAgentRegistry
	prevRouter := controlSessionRouter
	prevTools := controlToolRegistry
	defer func() {
		controlAgentRuntime = prevRuntime
		controlAgentRegistry = prevRegistry
		controlSessionRouter = prevRouter
		controlToolRegistry = prevTools
	}()

	controlAgentRuntime = namedStubRuntime{name: "primary"}
	controlAgentRegistry = agent.NewAgentRuntimeRegistry(controlAgentRuntime)
	controlSessionRouter = agent.NewAgentSessionRouter()
	controlToolRegistry = agent.NewToolRegistry()

	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	transcript := state.NewTranscriptRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		Agents:  state.AgentsConfig{{ID: "main", Model: "gpt-4o", LightModel: "echo"}},
	})
	if _, err := docs.PutSession(context.Background(), "s-light", state.SessionDoc{Version: 1, SessionID: "s-light", PeerPubKey: "peer", LastInboundAt: time.Now().Unix()}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for i := 0; i < 3; i++ {
		_, err := transcript.PutEntry(context.Background(), state.TranscriptEntryDoc{Version: 1, SessionID: "s-light", EntryID: fmt.Sprintf("e%d", i), Role: "user", Text: fmt.Sprintf("message %d", i), Unix: time.Now().Unix() + int64(i)})
		if err != nil {
			t.Fatalf("seed transcript: %v", err)
		}
	}

	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSessionsCompact,
		Params:     json.RawMessage(`{"session_id":"s-light","keep":1}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("sessions.compact error: %v", err)
	}
	entries, err := transcript.ListSessionAll(context.Background(), "s-light")
	if err != nil {
		t.Fatalf("list session: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected summary + kept entry, got %d entries", len(entries))
	}
	if !strings.Contains(entries[0].Text, "ack: You are a session-memory assistant") {
		t.Fatalf("expected compact summary to come from light model, got %q", entries[0].Text)
	}
}

func TestHandleControlRPCRequest_AgentCrudAndFiles(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})

	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsCreate,
		Params:     json.RawMessage(`{"agent_id":"main","name":"Main Agent","workspace":"/tmp/main","model":"gpt-5"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.create error: %v", err)
	}

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsList,
		Params:     json.RawMessage(`{"limit":10}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.list error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	agents, _ := payload["agents"].([]state.AgentDoc)
	if len(agents) != 1 || agents[0].AgentID != "main" {
		t.Fatalf("unexpected agents.list result: %#v", res.Result)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsUpdate,
		Params:     json.RawMessage(`{"agent_id":"main","name":"Updated Agent"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.update error: %v", err)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsFilesSet,
		Params:     json.RawMessage(`{"agent_id":"main","name":"instructions.md","content":"hello"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.files.set error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsFilesGet,
		Params:     json.RawMessage(`{"agent_id":"main","name":"instructions.md"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.files.get error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	file, _ := payload["file"].(map[string]any)
	if file["missing"] != false || file["content"] != "hello" {
		t.Fatalf("unexpected agents.files.get result: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsFilesList,
		Params:     json.RawMessage(`{"agent_id":"main"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.files.list error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	files, _ := payload["files"].([]map[string]any)
	if len(files) != 1 || files[0]["name"] != "instructions.md" {
		t.Fatalf("unexpected agents.files.list result: %#v", res.Result)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsDelete,
		Params:     json.RawMessage(`{"agent_id":"main"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.delete error: %v", err)
	}
	deleted, err := docs.GetAgent(context.Background(), "main")
	if err != nil {
		t.Fatalf("load deleted agent: %v", err)
	}
	if !deleted.Deleted {
		t.Fatalf("expected deleted flag set, got: %+v", deleted)
	}
}

func TestHandleControlRPCRequest_AgentRoutingLifecycle(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})

	prevRegistry := controlAgentRegistry
	prevRouter := controlSessionRouter
	controlAgentRegistry = agent.NewAgentRuntimeRegistry(stubAgentRuntime{})
	controlSessionRouter = agent.NewAgentSessionRouter()
	defer func() {
		controlAgentRegistry = prevRegistry
		controlSessionRouter = prevRouter
	}()

	// Seed a legacy-shaped session where SessionID != PeerPubKey.
	_, err := docs.PutSession(context.Background(), "ws-legacy-1", state.SessionDoc{
		Version:    1,
		SessionID:  "ws-legacy-1",
		PeerPubKey: "npub-legacy-peer",
		Meta:       map[string]any{"agent_id": "alpha"},
	})
	if err != nil {
		t.Fatalf("seed legacy session: %v", err)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsCreate,
		Params:     json.RawMessage(`{"agent_id":"alpha","name":"Alpha","model":"echo"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.create alpha error: %v", err)
	}

	assignRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsAssign,
		Params:     json.RawMessage(`{"agent_id":"alpha","session_id":"ws-1"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.assign ws-1 error: %v", err)
	}
	assignPayload, _ := assignRes.Result.(map[string]any)
	if assignPayload["durability"] != "best_effort" {
		t.Fatalf("expected best_effort durability on assign, got: %#v", assignPayload)
	}
	if persisted, _ := assignPayload["persisted"].(bool); !persisted {
		t.Fatalf("expected persisted=true on assign in test store, got: %#v", assignPayload)
	}
	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsAssign,
		Params:     json.RawMessage(`{"agent_id":"alpha","session_id":"ws-2"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.assign ws-2 error: %v", err)
	}

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsActive,
		Params:     json.RawMessage(`{"limit":1}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.active error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	assignmentCount := 0
	switch items := payload["assignments"].(type) {
	case []map[string]any:
		assignmentCount = len(items)
	case []any:
		assignmentCount = len(items)
	}
	if assignmentCount != 1 {
		t.Fatalf("expected agents.active to honor limit=1, got: %#v", payload)
	}

	unassignRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsUnassign,
		Params:     json.RawMessage(`{"session_id":"ws-1"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.unassign ws-1 error: %v", err)
	}
	unassignPayload, _ := unassignRes.Result.(map[string]any)
	if unassignPayload["durability"] != "best_effort" {
		t.Fatalf("expected best_effort durability on unassign, got: %#v", unassignPayload)
	}
	if persisted, _ := unassignPayload["persisted"].(bool); !persisted {
		t.Fatalf("expected persisted=true on unassign in test store, got: %#v", unassignPayload)
	}
	s1, err := docs.GetSession(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("load ws-1 after unassign: %v", err)
	}
	if _, exists := s1.Meta["agent_id"]; exists {
		t.Fatalf("expected ws-1 agent_id removed on unassign, got: %+v", s1.Meta)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsDelete,
		Params:     json.RawMessage(`{"agent_id":"alpha"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.delete alpha error: %v", err)
	}

	for _, sessionID := range []string{"ws-2", "ws-legacy-1"} {
		sess, sessErr := docs.GetSession(context.Background(), sessionID)
		if sessErr != nil {
			t.Fatalf("load %s after delete: %v", sessionID, sessErr)
		}
		if _, exists := sess.Meta["agent_id"]; exists {
			t.Fatalf("expected session %s agent_id removed on delete cleanup, got: %+v", sessionID, sess.Meta)
		}
	}
}

func TestHandleControlRPCRequest_ChannelsJoinRejectsUnsupportedType(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodChannelsJoin,
		Params:     json.RawMessage(`{"type":"slack","group_address":"relay.example.com'group-1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err == nil {
		t.Fatalf("expected channels.join to reject unsupported type")
	}
	if !strings.Contains(err.Error(), "unsupported channel type") {
		t.Fatalf("expected unsupported channel type error, got: %v", err)
	}
}

func TestHandleControlRPCRequest_ModelsToolsSkillsMethods(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}, Extra: map[string]any{
		"extensions": map[string]any{"entries": map[string]any{
			"codegen": map[string]any{"enabled": true, "tools": []any{map[string]any{"id": "codegen.apply", "label": "Codegen Apply", "optional": true}, map[string]any{"id": "apply_patch", "label": "Plugin Patch"}}},
			"simple":  map[string]any{"enabled": true, "tools": []string{"simple.tool"}},
		}},
	}})
	tools := agent.NewToolRegistry()
	tools.Register("memory.search", func(context.Context, map[string]any) (string, error) {
		return "[]", nil
	})

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodModelsList,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("models.list error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	if len(payload["models"].([]map[string]any)) == 0 {
		t.Fatalf("unexpected models.list payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodToolsCatalog,
		Params:     json.RawMessage(`{"agent_id":"main"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("tools.catalog error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["agentId"] != "main" {
		t.Fatalf("unexpected tools.catalog payload: %#v", res.Result)
	}
	groups, ok := payload["groups"].([]map[string]any)
	if !ok {
		t.Fatalf("unexpected groups shape: %#v", payload["groups"])
	}
	hasPlugin := false
	hasCodegenTool := false
	hasSimpleTool := false
	hasConflictingPluginTool := false
	for _, group := range groups {
		if group["source"] != "core" && group["source"] != "plugin" {
			t.Fatalf("unexpected non-parity group source in tools.catalog payload: %#v", group)
		}
		if group["source"] == "plugin" {
			hasPlugin = true
			if _, ok := group["pluginId"]; !ok {
				t.Fatalf("expected pluginId field on plugin group, got: %#v", group)
			}
			if toolsList, ok := group["tools"].([]map[string]any); ok {
				for _, toolEntry := range toolsList {
					if toolEntry["id"] == "codegen.apply" {
						hasCodegenTool = true
						if toolEntry["pluginId"] != "codegen" {
							t.Fatalf("expected plugin id on tool entry, got: %#v", toolEntry)
						}
						if _, ok := toolEntry["defaultProfiles"]; !ok {
							t.Fatalf("expected defaultProfiles on tool entry, got: %#v", toolEntry)
						}
					}
					if toolEntry["id"] == "simple.tool" {
						hasSimpleTool = true
					}
					if toolEntry["id"] == "apply_patch" {
						hasConflictingPluginTool = true
					}
				}
			}
		}
	}
	if !hasPlugin || !hasCodegenTool || !hasSimpleTool {
		t.Fatalf("expected plugin tool groups with codegen.apply and simple.tool in tools.catalog payload: %#v", payload)
	}
	if hasConflictingPluginTool {
		t.Fatalf("expected plugin tool ids conflicting with core catalog to be suppressed: %#v", payload)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodToolsCatalog,
		Params:     json.RawMessage(`{"agent_id":"main","include_plugins":false}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("tools.catalog include_plugins=false error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	groups, ok = payload["groups"].([]map[string]any)
	if !ok {
		t.Fatalf("unexpected groups shape (include_plugins=false): %#v", payload["groups"])
	}
	for _, group := range groups {
		if group["source"] == "plugin" {
			t.Fatalf("expected plugin groups excluded when include_plugins=false: %#v", groups)
		}
	}
	hasApplyPatch := false
	hasTTS := false
	for _, group := range groups {
		if group["source"] != "core" {
			continue
		}
		if toolsList, ok := group["tools"].([]map[string]any); ok {
			for _, toolEntry := range toolsList {
				if toolEntry["id"] == "apply_patch" {
					hasApplyPatch = true
				}
				if toolEntry["id"] == "tts" {
					hasTTS = true
				}
			}
		}
	}
	if !hasApplyPatch || !hasTTS {
		t.Fatalf("expected core tools apply_patch and tts in tools.catalog payload: %#v", groups)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSkillsInstall,
		Params:     json.RawMessage(`{"name":"nostr-core","install_id":"builtin"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("skills.install error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["ok"] != false || payload["code"] != 1 {
		t.Fatalf("unexpected skills.install payload: %#v", payload)
	}
	if _, ok := payload["installId"]; ok {
		t.Fatalf("unexpected non-parity skills.install field installId: %#v", payload)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSkillsStatus,
		Params:     json.RawMessage(`{"agent_id":"main"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("skills.status error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["workspaceDir"] == nil || payload["managedSkillsDir"] == nil {
		t.Fatalf("unexpected skills.status payload: %#v", res.Result)
	}
	if _, ok := payload["count"]; ok {
		t.Fatalf("unexpected non-parity count field in skills.status payload: %#v", payload)
	}
	skills, _ := payload["skills"].([]map[string]any)
	if len(skills) == 0 || skills[0]["skillKey"] == nil {
		t.Fatalf("expected OpenClaw-style skill status entries, got: %#v", payload)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSkillsBins,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("skills.bins error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	bins, ok := payload["bins"].([]string)
	if !ok {
		t.Fatalf("unexpected skills.bins payload shape: %#v", res.Result)
	}
	for _, b := range bins {
		if b == "nostr-core" {
			t.Fatalf("did not expect config-only install failure to add nostr-core bin: %#v", res.Result)
		}
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSkillsUpdate,
		Params:     json.RawMessage(`{"skill_key":"nostr-core","enabled":true}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("skills.update rpc error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["skillKey"] != "nostr-core" {
		t.Fatalf("expected skillKey in skills.update response, got: %#v", payload)
	}
	if _, ok := payload["skills"]; ok {
		t.Fatalf("unexpected non-parity skills list in skills.update response: %#v", payload)
	}

	enabled := true
	apiKey := "token"
	_, updatedEntry, err := applySkillUpdate(context.Background(), docs, cfgState, methods.SkillsUpdateRequest{SkillKey: "nostr-core", Enabled: &enabled, APIKey: &apiKey, Env: map[string]string{" A ": " B "}})
	if err != nil {
		t.Fatalf("applySkillUpdate helper failed: %v", err)
	}
	envAfter, _ := updatedEntry["env"].(map[string]any)
	if envAfter["A"] != "B" {
		t.Fatalf("expected trimmed env key/value, got: %#v", envAfter)
	}
	blank := "  "
	_, updatedEntry, err = applySkillUpdate(context.Background(), docs, cfgState, methods.SkillsUpdateRequest{SkillKey: "nostr-core", APIKey: &blank, Env: map[string]string{"A": ""}})
	if err != nil {
		t.Fatalf("applySkillUpdate cleanup failed: %v", err)
	}
	if _, hasKey := updatedEntry["api_key"]; hasKey {
		t.Fatalf("expected api_key to be removed on blank update, got: %#v", updatedEntry)
	}
	if _, hasEnv := updatedEntry["env"]; hasEnv {
		t.Fatalf("expected env to be removed when empty after cleanup, got: %#v", updatedEntry)
	}
	_, updatedEntry, err = applySkillUpdate(context.Background(), docs, cfgState, methods.SkillsUpdateRequest{SkillKey: "nostr-core", Env: map[string]string{" ": "X"}})
	if err != nil {
		t.Fatalf("expected blank env key to be ignored, got: %v", err)
	}
	if envRaw, hasEnv := updatedEntry["env"]; hasEnv {
		envMap, _ := envRaw.(map[string]any)
		if _, exists := envMap[" "]; exists {
			t.Fatalf("expected blank env key to be dropped, got: %#v", envMap)
		}
	}

	tmpExtDir := t.TempDir()
	sourceDir := filepath.Join(tmpExtDir, "source-codegen")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodPluginsInstall,
		Params:     json.RawMessage(fmt.Sprintf(`{"plugin_id":"codegen","install":{"source":"path","sourcePath":%q,"installPath":"./extensions/codegen"}}`, sourceDir)),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("plugins.install error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["pluginId"] != "codegen" {
		t.Fatalf("unexpected plugins.install payload: %#v", payload)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodPluginsUpdate,
		Params:     json.RawMessage(`{"plugin_ids":["codegen"],"dry_run":true}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("plugins.update dry-run error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["changed"] != false {
		t.Fatalf("expected plugins.update dry-run changed=false, got: %#v", payload)
	}

	cfgInstall := cfgState.Get()
	rawExtInstall, _ := cfgInstall.Extra["extensions"].(map[string]any)
	rawInstallsInstall, _ := rawExtInstall["installs"].(map[string]any)
	installCodegen, _ := rawInstallsInstall["codegen"].(map[string]any)
	installCodegen["source"] = "npm"
	installCodegen["spec"] = "@acme/codegen@1.0.0"
	installCodegen["version"] = "1.0.0"
	rawInstallsInstall["codegen"] = installCodegen
	rawExtInstall["installs"] = rawInstallsInstall
	cfgInstall.Extra["extensions"] = rawExtInstall
	if _, err := docs.PutConfig(context.Background(), cfgInstall); err != nil {
		t.Fatalf("persist npm install record for update test: %v", err)
	}
	cfgState.Set(cfgInstall)

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodPluginsUpdate,
		Params:     json.RawMessage(`{"plugin_ids":["codegen"],"dry_run":false}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("plugins.update execute error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["changed"] != false {
		t.Fatalf("expected pinned npm plugins.update to be unchanged, got: %#v", payload)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodPluginsUninstall,
		Params:     json.RawMessage(`{"plugin_id":"codegen"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("plugins.uninstall error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["ok"] != true {
		t.Fatalf("unexpected plugins.uninstall payload: %#v", payload)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodPluginsInstall,
		Params:     json.RawMessage(`{"plugin_id":"bad","install":{"source":"path","sourcePath":"/definitely/missing/path"}}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err == nil {
		t.Fatalf("expected plugins.install validation error for missing sourcePath")
	}

	archivePath := filepath.Join(tmpExtDir, "plugin.zip")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create archive fixture: %v", err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("package/index.js")
	if err != nil {
		t.Fatalf("create archive entry: %v", err)
	}
	if _, err := w.Write([]byte("module.exports={}")); err != nil {
		t.Fatalf("write archive entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close archive writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close archive file: %v", err)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodPluginsInstall,
		Params:     json.RawMessage(fmt.Sprintf(`{"plugin_id":"archivebad","install":{"source":"archive","sourcePath":%q,"installPath":%q}}`, archivePath, tmpExtDir)),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err == nil {
		t.Fatalf("expected plugins.install validation error for unmanaged archive installPath")
	}
	cfgAfterArchiveFail := cfgState.Get()
	rawExtAfterArchiveFail, _ := cfgAfterArchiveFail.Extra["extensions"].(map[string]any)
	rawInstallsAfterArchiveFail, _ := rawExtAfterArchiveFail["installs"].(map[string]any)
	if _, exists := rawInstallsAfterArchiveFail["archivebad"]; exists {
		t.Fatalf("expected transactional install semantics to avoid persisted record on backend validation failure")
	}

	cfgWithLegacyCase := cfgState.Get()
	entries := extractSkillEntries(cfgWithLegacyCase)
	delete(entries, "nostr-core")
	entries["NoStr-Core"] = map[string]any{"enabled": true}
	cfgWithLegacyCase = configWithSkillEntries(cfgWithLegacyCase, entries)
	if _, err := docs.PutConfig(context.Background(), cfgWithLegacyCase); err != nil {
		t.Fatalf("seed mixed-case skill key: %v", err)
	}
	cfgState.Set(cfgWithLegacyCase)
	_, _, err = applySkillUpdate(context.Background(), docs, cfgState, methods.SkillsUpdateRequest{SkillKey: "nostr-core", Enabled: &enabled})
	if err != nil {
		t.Fatalf("applySkillUpdate mixed-case key migration failed: %v", err)
	}
	after := extractSkillEntries(cfgState.Get())
	if _, ok := after["NoStr-Core"]; ok {
		t.Fatalf("expected mixed-case legacy skill key to be removed: %#v", after)
	}
	if _, ok := after["nostr-core"]; !ok {
		t.Fatalf("expected normalized skill key to exist: %#v", after)
	}
}

func TestHandleControlRPCRequest_SkillsStatusUnknownAgent(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSkillsStatus,
		Params:     json.RawMessage(`{"agent_id":"ghost"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err == nil || !strings.Contains(err.Error(), "unknown agent id") {
		t.Fatalf("expected unknown agent id error, got: %v", err)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSkillsBins,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("expected skills.bins to ignore agent scope and succeed, got: %v", err)
	}
}

func TestHandleControlRPCRequest_ToolsCatalogUnknownAgent(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodToolsCatalog,
		Params:     json.RawMessage(`{"agent_id":"ghost"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err == nil || !strings.Contains(err.Error(), "unknown agent id") {
		t.Fatalf("expected unknown agent id error for tools.catalog, got: %v", err)
	}

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodToolsCatalog,
		Params:     json.RawMessage(`{"agent_id":"MAIN"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("expected case-insensitive main agent id to be accepted, got: %v", err)
	}
	payload, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected tools.catalog result for MAIN agent: %#v", res.Result)
	}
	if payload["agentId"] != "main" {
		t.Fatalf("expected canonical main agent id in response, got: %#v", payload)
	}
}

func TestHandleControlRPCRequest_ToolsCatalogRespectsPluginPolicy(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}, Extra: map[string]any{
		"extensions": map[string]any{
			"enabled": true,
			"load":    true,
			"allow":   []string{"codegen"},
			"deny":    []string{"blocked"},
			"entries": map[string]any{
				"codegen": map[string]any{"enabled": true, "tools": []string{"codegen.apply"}},
				"blocked": map[string]any{"enabled": true, "tools": []string{"blocked.tool"}},
				"extra":   map[string]any{"enabled": true, "tools": []string{"extra.tool"}},
			},
		},
	}})
	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodToolsCatalog,
		Params:     json.RawMessage(`{"agent_id":"main"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("tools.catalog policy error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	groups, _ := payload["groups"].([]map[string]any)
	hasCodegen := false
	hasBlocked := false
	hasExtra := false
	for _, group := range groups {
		if group["source"] != "plugin" {
			continue
		}
		if toolsList, ok := group["tools"].([]map[string]any); ok {
			for _, toolEntry := range toolsList {
				switch toolEntry["id"] {
				case "codegen.apply":
					hasCodegen = true
				case "blocked.tool":
					hasBlocked = true
				case "extra.tool":
					hasExtra = true
				}
			}
		}
	}
	if !hasCodegen || hasBlocked || hasExtra {
		t.Fatalf("unexpected plugin policy filtering in tools.catalog: %#v", payload)
	}

	cfgState.Set(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}, Extra: map[string]any{
		"extensions": map[string]any{
			"enabled": true,
			"load":    false,
			"entries": map[string]any{"codegen": map[string]any{"enabled": true, "tools": []string{"codegen.apply"}}},
		},
	}})
	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodToolsCatalog,
		Params:     json.RawMessage(`{"agent_id":"main"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("tools.catalog load=false error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	groups, _ = payload["groups"].([]map[string]any)
	for _, group := range groups {
		if group["source"] == "plugin" {
			t.Fatalf("expected no plugin groups when plugins.load=false: %#v", payload)
		}
	}
}

func TestHandleControlRPCRequest_NodeDevicePairingMethods(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	tools := agent.NewToolRegistry()

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodePairRequest,
		Params:     json.RawMessage(`{"node_id":"n1","display_name":"Node One"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("node.pair.request error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	if payload["status"] != "pending" || payload["created"] != true {
		t.Fatalf("unexpected node.pair.request payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodePairRequest,
		Params:     json.RawMessage(`{"node_id":"n1","display_name":"Node One v2"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("node.pair.request update error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["created"] != false {
		t.Fatalf("expected update path created=false, got: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodePairList,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("node.pair.list error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	pending, _ := payload["pending"].([]map[string]any)
	if len(pending) != 1 {
		t.Fatalf("expected one pending node request, got: %#v", res.Result)
	}
	requestID := fmt.Sprintf("%v", pending[0]["request_id"])

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodePairApprove,
		Params:     json.RawMessage(fmt.Sprintf(`{"request_id":%q}`, requestID)),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("node.pair.approve error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	node, _ := payload["node"].(map[string]any)
	if fmt.Sprintf("%v", node["token"]) == "" {
		t.Fatalf("expected approved node token, got: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodePairVerify,
		Params:     json.RawMessage(`{"node_id":"n1","token":"wrong"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("node.pair.verify error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["ok"] != false {
		t.Fatalf("expected failed verify payload, got: %#v", res.Result)
	}

	_, err = applyPairingConfigUpdate(context.Background(), docs, cfgState, func(p map[string]any) (map[string]any, map[string]any, error) {
		p["device_pending"] = []map[string]any{{"request_id": "dreq-1", "device_id": "d1", "public_key": "pub", "role": "node", "scopes": []string{"operator.read"}, "ts": time.Now().UnixMilli()}}
		return p, map[string]any{}, nil
	})
	if err != nil {
		t.Fatalf("seed device pending failed: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodDevicePairApprove,
		Params:     json.RawMessage(`{"request_id":"dreq-1"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("device.pair.approve error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodDeviceTokenRotate,
		Params:     json.RawMessage(`{"device_id":"d1","role":"node","scopes":["operator.read"]}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("device.token.rotate error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["token"] == "" {
		t.Fatalf("expected token in rotate payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodDevicePairList,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("device.pair.list error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	paired, _ := payload["paired"].([]map[string]any)
	if len(paired) != 1 {
		t.Fatalf("expected one paired device, got: %#v", res.Result)
	}
	tokens, _ := paired[0]["tokens"].([]map[string]any)
	if len(tokens) == 0 || tokens[0]["token"] != nil {
		t.Fatalf("expected redacted token summaries in list payload: %#v", res.Result)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodDeviceTokenRevoke,
		Params:     json.RawMessage(`{"device_id":"d1","role":"node"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, nil, time.Now())
	if err != nil {
		t.Fatalf("device.token.revoke error: %v", err)
	}
}

func TestHandleControlRPCRequest_NodeInvokeAndCronMethods(t *testing.T) {
	docs := state.NewDocsRepository(newTestStore(), "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		Extra:   map[string]any{"pairing": map[string]any{"node_paired": []any{map[string]any{"node_id": "n1", "display_name": "Node One", "token": "secret-node-token", "caps": []any{"canvas"}, "approved_at_ms": int64(1)}}}},
	})
	prevNode := controlNodeInvocations
	prevCron := controlCronJobs
	controlNodeInvocations = newNodeInvocationRegistry()
	controlCronJobs = newCronRegistry()
	defer func() {
		controlNodeInvocations = prevNode
		controlCronJobs = prevCron
	}()

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeList,
		Params:     json.RawMessage(`{"limit":10}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("node.list error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	nodes, _ := payload["nodes"].([]map[string]any)
	if len(nodes) != 1 {
		t.Fatalf("unexpected node.list payload: %#v", res.Result)
	}
	if nodes[0]["token"] != config.RedactedValue {
		t.Fatalf("expected node.list token to be redacted, got: %#v", nodes[0])
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeDescribe,
		Params:     json.RawMessage(`{"node_id":"n1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("node.describe error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["status"] != "paired" {
		t.Fatalf("unexpected node.describe payload: %#v", res.Result)
	}
	describedNode, _ := payload["node"].(map[string]any)
	if describedNode["token"] != config.RedactedValue {
		t.Fatalf("expected node.describe token to be redacted, got: %#v", payload)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeRename,
		Params:     json.RawMessage(`{"node_id":"n1","name":"Kitchen"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("node.rename error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeCanvasCapabilityRefresh,
		Params:     json.RawMessage(`{"node_id":"n1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("node.canvas.capability.refresh error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeInvoke,
		Params:     json.RawMessage(`{"node_id":"n1","command":"ping"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("node.invoke error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	runID, _ := payload["run_id"].(string)
	if runID == "" {
		t.Fatalf("expected run_id in node.invoke payload: %#v", res.Result)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeEvent,
		Params:     json.RawMessage(fmt.Sprintf(`{"run_id":%q,"type":"progress","status":"running"}`, runID)),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("node.event error: %v", err)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeInvokeResult,
		Params:     json.RawMessage(fmt.Sprintf(`{"run_id":%q,"status":"ok","result":{"pong":true}}`, runID)),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("node.invoke.result error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodCronAdd,
		Params:     json.RawMessage(`{"id":"c1","schedule":"* * * * *","method":"status.get"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("cron.add error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	job, ok := payload["job"].(cronJobRecord)
	if !ok || job.ID != "c1" {
		t.Fatalf("unexpected cron.add payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodCronList,
		Params:     json.RawMessage(`{"limit":10}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("cron.list error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if _, ok := payload["jobs"]; !ok {
		t.Fatalf("unexpected cron.list payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodCronStatus,
		Params:     json.RawMessage(`{"id":"c1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("cron.status error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if jobAny, ok := payload["job"]; !ok || jobAny == nil {
		t.Fatalf("unexpected cron.status payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodCronUpdate,
		Params:     json.RawMessage(`{"id":"c1","enabled":false}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("cron.update error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["ok"] != true {
		t.Fatalf("unexpected cron.update payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodCronRun,
		Params:     json.RawMessage(`{"id":"c1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("cron.run error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["ok"] != true {
		t.Fatalf("unexpected cron.run payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodCronRuns,
		Params:     json.RawMessage(`{"limit":10}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("cron.runs error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if _, ok := payload["runs"]; !ok {
		t.Fatalf("unexpected cron.runs payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodCronRemove,
		Params:     json.RawMessage(`{"id":"c1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("cron.remove error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["ok"] != true {
		t.Fatalf("unexpected cron.remove payload: %#v", res.Result)
	}
}

func TestApplyNodeDescribe_RedactsPendingNodeToken(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Extra: map[string]any{"pairing": map[string]any{"node_pending": []any{map[string]any{"node_id": "pending-1", "token": "pending-secret", "display_name": "Pending Node"}}}},
	})
	result, err := applyNodeDescribe(cfgState, methods.NodeDescribeRequest{NodeID: "pending-1"})
	if err != nil {
		t.Fatalf("applyNodeDescribe error: %v", err)
	}
	if result["status"] != "pending" {
		t.Fatalf("unexpected status: %#v", result)
	}
	node, _ := result["node"].(map[string]any)
	if node["token"] != config.RedactedValue {
		t.Fatalf("expected pending node token to be redacted, got: %#v", result)
	}
}

func TestHandleControlRPCRequest_OpenClawHighRiskParityFixtures(t *testing.T) {
	type fixtureCase struct {
		Name                string         `json:"name"`
		Method              string         `json:"method"`
		Params              map[string]any `json:"params"`
		ExpectErrorContains string         `json:"expect_error_contains"`
		ResultKind          string         `json:"result_kind"`
	}
	type fixtureFile struct {
		Cases []fixtureCase `json:"cases"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "parity", "high_risk_methods.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx fixtureFile
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	prevOps := controlOps
	controlOps = newOperationsRegistry()
	defer func() { controlOps = prevOps }()

	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			paramsRaw, err := json.Marshal(tc.Params)
			if err != nil {
				t.Fatalf("marshal params: %v", err)
			}
			res, callErr := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
				FromPubKey: "caller",
				Method:     tc.Method,
				Params:     paramsRaw,
			}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
			if tc.ExpectErrorContains != "" {
				if callErr == nil || !strings.Contains(callErr.Error(), tc.ExpectErrorContains) {
					t.Fatalf("err=%v, want contains %q", callErr, tc.ExpectErrorContains)
				}
				return
			}
			if callErr != nil {
				t.Fatalf("unexpected error: %v", callErr)
			}
			switch tc.ResultKind {
			case "array":
				if _, ok := res.Result.([]map[string]any); !ok {
					t.Fatalf("result kind mismatch: %#v", res.Result)
				}
			}
		})
	}
}

func TestHandleControlRPCRequest_RuntimeUnavailableParityFixtures(t *testing.T) {
	type fixtureCase struct {
		Name          string         `json:"name"`
		Method        string         `json:"method"`
		Params        map[string]any `json:"params"`
		ErrorContains string         `json:"error_contains"`
	}
	type fixtureFile struct {
		Cases []fixtureCase `json:"cases"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "parity", "control_runtime_unavailable_cases.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx fixtureFile
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	prevOps := controlOps
	controlOps = newOperationsRegistry()
	defer func() { controlOps = prevOps }()

	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			paramsRaw, err := json.Marshal(tc.Params)
			if err != nil {
				t.Fatalf("marshal params: %v", err)
			}
			_, callErr := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
				FromPubKey: "caller",
				Method:     tc.Method,
				Params:     paramsRaw,
			}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
			if callErr == nil {
				t.Fatalf("expected runtime-unavailable error")
			}
			if !strings.Contains(callErr.Error(), tc.ErrorContains) {
				t.Fatalf("error=%v want contains %q", callErr, tc.ErrorContains)
			}
		})
	}
}

func TestHandleControlRPCRequest_OperationalBundles(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	prevExec := controlExecApprovals
	prevWizard := controlWizards
	prevOps := controlOps
	controlExecApprovals = newExecApprovalsRegistry()
	controlWizards = newWizardRegistry()
	controlOps = newOperationsRegistry()
	defer func() {
		controlExecApprovals = prevExec
		controlWizards = prevWizard
		controlOps = prevOps
	}()

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalsSet, Params: json.RawMessage(`{"approvals":{"allow":true}}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("exec.approvals.set error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	if payload["ok"] != true {
		t.Fatalf("unexpected exec.approvals.set payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalRequest, Params: json.RawMessage(`{"command":"ls"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("exec.approval.request error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["status"] != "accepted" {
		t.Fatalf("unexpected exec.approval.request payload: %#v", res.Result)
	}
	approvalID, _ := payload["id"].(string)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res, err = handleControlRPCRequest(ctx, nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalWaitDecision, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"timeout_ms":100}`, approvalID))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("exec.approval.waitDecision error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["resolved"] != false {
		t.Fatalf("unexpected exec.approval.waitDecision timeout payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalResolve, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"decision":"approve"}`, approvalID))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("exec.approval.resolve error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalWaitDecision, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"timeout_ms":5000}`, approvalID))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("exec.approval.waitDecision resolved error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["resolved"] != true {
		t.Fatalf("unexpected resolved wait payload: %#v", res.Result)
	}

	ctxCancel, cancelFunc := context.WithCancel(context.Background())
	done := make(chan bool)
	go func() {
		res2, _ := handleControlRPCRequest(ctxCancel, nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalRequest, Params: json.RawMessage(`{"command":"test-cancel"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
		payload2, _ := res2.Result.(map[string]any)
		approvalID2, _ := payload2["id"].(string)
		res3, _ := handleControlRPCRequest(ctxCancel, nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalWaitDecision, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"timeout_ms":5000}`, approvalID2))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
		payload3, _ := res3.Result.(map[string]any)
		if payload3["cancelled"] != true {
			t.Errorf("expected cancelled=true, got: %#v", payload3)
		}
		done <- true
	}()
	time.Sleep(20 * time.Millisecond)
	cancelFunc()
	<-done

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalRequest, Params: json.RawMessage(`{"command":"test-concurrent","timeout_ms":5000}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("exec.approval.request concurrent error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	approvalID3, _ := payload["id"].(string)

	done2 := make(chan map[string]any, 2)
	go func() {
		res4, _ := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalWaitDecision, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"timeout_ms":2000}`, approvalID3))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
		payload4, _ := res4.Result.(map[string]any)
		done2 <- payload4
	}()
	go func() {
		res5, _ := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalWaitDecision, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"timeout_ms":2000}`, approvalID3))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
		payload5, _ := res5.Result.(map[string]any)
		done2 <- payload5
	}()
	time.Sleep(50 * time.Millisecond)
	_, _ = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalResolve, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"decision":"approve"}`, approvalID3))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())

	result1 := <-done2
	result2 := <-done2
	if result1["resolved"] != true || result2["resolved"] != true {
		t.Fatalf("concurrent waiters should both receive resolution: r1=%#v r2=%#v", result1, result2)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodWizardStart, Params: json.RawMessage(`{"mode":"local"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("wizard.start error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	sessionID, _ := payload["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("unexpected wizard.start payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodSystemEvent, Params: json.RawMessage(`{"text":"Node: online","deviceId":"mac-1"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("system-event error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["ok"] != true {
		t.Fatalf("unexpected system-event payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodSystemPresence, Params: json.RawMessage(`{}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("system-presence error: %v", err)
	}
	if _, ok := res.Result.([]map[string]any); !ok {
		t.Fatalf("unexpected system-presence payload: %#v", res.Result)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodSetHeartbeats, Params: json.RawMessage(`{"interval_ms":30000}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err == nil {
		t.Fatalf("expected set-heartbeats to require enabled")
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodSetHeartbeats, Params: json.RawMessage(`{"enabled":true,"interval_ms":30000}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("set-heartbeats error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["enabled"] != true {
		t.Fatalf("unexpected set-heartbeats payload: %#v", res.Result)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodSend, Params: json.RawMessage(`{"to":"npub1abc","message":"hello","idempotencyKey":"idem-1"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err == nil {
		t.Fatalf("expected send to fail when dm runtime is unavailable")
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodBrowserRequest, Params: json.RawMessage(`{"method":"GET","path":"/status"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err == nil {
		t.Fatalf("expected browser.request to fail when browser control is disabled")
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodVoicewakeSet, Params: json.RawMessage(`{"triggers":["openclaw","metiq"]}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("voicewake.set error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if _, ok := payload["triggers"]; !ok {
		t.Fatalf("unexpected voicewake.set payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodTTSSetProvider, Params: json.RawMessage(`{"provider":"edge"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("tts.setProvider error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["provider"] != "edge" {
		t.Fatalf("expected provider=edge, got: %#v", payload)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodTTSSetProvider, Params: json.RawMessage(`{"provider":"invalid-provider"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("tts.setProvider invalid error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["provider"] != "openai" {
		t.Fatalf("expected invalid provider to default to openai, got: %#v", payload)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodTTSConvert, Params: json.RawMessage(`{"text":"hello","provider":"openai"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, nil, time.Now())
	if err == nil {
		t.Fatalf("tts.convert should return error when TTS is disabled, got nil")
	}
	if !strings.Contains(err.Error(), "tts") {
		t.Fatalf("tts.convert error should mention tts, got: %v", err)
	}
}

func TestResolveModelProviderOverride_PrefersAgentProviderForUnqualifiedModel(t *testing.T) {
	cfg := state.ConfigDoc{
		Providers: state.ProvidersConfig{
			"edge": {BaseURL: "https://edge.example/v1", APIKey: "edge-key"},
		},
	}
	agCfg := state.AgentConfig{ID: "main", Provider: "edge", SystemPrompt: "system prompt"}
	override := resolveModelProviderOverride(cfg, agCfg, "gpt-4o-mini")
	if override.BaseURL != "https://edge.example/v1" {
		t.Fatalf("expected agent provider base URL, got %#v", override)
	}
	if override.APIKey != "edge-key" {
		t.Fatalf("expected agent provider API key, got %#v", override)
	}
	if override.SystemPrompt != "system prompt" {
		t.Fatalf("expected system prompt to propagate, got %#v", override)
	}
}

func TestResolveModelProviderOverride_ProviderQualifiedModelWins(t *testing.T) {
	cfg := state.ConfigDoc{
		Providers: state.ProvidersConfig{
			"edge":       {BaseURL: "https://edge.example/v1", APIKey: "edge-key"},
			"openrouter": {BaseURL: "https://openrouter.example/api/v1", APIKey: "or-key"},
		},
	}
	agCfg := state.AgentConfig{ID: "main", Provider: "edge"}
	override := resolveModelProviderOverride(cfg, agCfg, "openrouter/meta-llama/llama-3.1-8b-instruct")
	if override.BaseURL != "https://openrouter.example/api/v1" {
		t.Fatalf("expected provider-qualified model to pick openrouter override, got %#v", override)
	}
	if override.APIKey != "or-key" {
		t.Fatalf("expected provider-qualified model API key, got %#v", override)
	}
}

func TestResolveInboundChannelRuntime_PrefersConfiguredAgentThenSessionThenMain(t *testing.T) {
	prevRegistry := controlAgentRegistry
	prevRouter := controlSessionRouter
	prevRuntime := controlAgentRuntime
	defer func() {
		controlAgentRegistry = prevRegistry
		controlSessionRouter = prevRouter
		controlAgentRuntime = prevRuntime
	}()

	mainRT := namedStubRuntime{name: "main"}
	alphaRT := namedStubRuntime{name: "alpha"}
	betaRT := namedStubRuntime{name: "beta"}

	controlAgentRuntime = mainRT
	controlAgentRegistry = agent.NewAgentRuntimeRegistry(mainRT)
	controlAgentRegistry.Set("alpha", alphaRT)
	controlAgentRegistry.Set("beta", betaRT)
	controlSessionRouter = agent.NewAgentSessionRouter()
	controlSessionRouter.Assign("session-1", "beta")

	resolvedID, rt := resolveInboundChannelRuntime("alpha", "session-1")
	if resolvedID != "alpha" {
		t.Fatalf("expected configured agent to win, got id=%q", resolvedID)
	}
	if result, err := rt.ProcessTurn(context.Background(), agent.Turn{UserText: "hello"}); err != nil || !strings.HasPrefix(result.Text, "alpha:") {
		t.Fatalf("expected alpha runtime, got result=%q err=%v", result.Text, err)
	}

	resolvedID, rt = resolveInboundChannelRuntime("", "session-1")
	if resolvedID != "beta" {
		t.Fatalf("expected session-assigned agent, got id=%q", resolvedID)
	}
	if result, err := rt.ProcessTurn(context.Background(), agent.Turn{UserText: "hello"}); err != nil || !strings.HasPrefix(result.Text, "beta:") {
		t.Fatalf("expected beta runtime, got result=%q err=%v", result.Text, err)
	}

	resolvedID, rt = resolveInboundChannelRuntime("", "session-unknown")
	if resolvedID != "main" {
		t.Fatalf("expected main fallback, got id=%q", resolvedID)
	}
	if result, err := rt.ProcessTurn(context.Background(), agent.Turn{UserText: "hello"}); err != nil || !strings.HasPrefix(result.Text, "main:") {
		t.Fatalf("expected main runtime fallback, got result=%q err=%v", result.Text, err)
	}
}

func TestExecuteAgentRun_EmitsAgentStatusWithSession(t *testing.T) {
	prevRouter := controlSessionRouter
	prevEmitter := controlWsEmitter
	defer func() {
		controlSessionRouter = prevRouter
		setControlWSEmitter(prevEmitter)
	}()

	controlSessionRouter = agent.NewAgentSessionRouter()
	controlSessionRouter.Assign("session-42", "alpha")

	capture := &capturingEmitter{}
	setControlWSEmitter(capture)

	jobs := newAgentJobRegistry()
	runID := "run-session-event"
	jobs.Begin(runID, "session-42")
	executeAgentRun(runID, methods.AgentRequest{SessionID: "session-42", Message: "hello", TimeoutMS: 500}, stubAgentRuntime{}, nil, jobs)

	events := capture.eventsByName(gatewayws.EventAgentStatus)
	if len(events) != 2 {
		t.Fatalf("expected 2 agent.status events, got %d", len(events))
	}

	thinking, ok := events[0].(gatewayws.AgentStatusPayload)
	if !ok {
		t.Fatalf("unexpected first agent.status payload type: %T", events[0])
	}
	idle, ok := events[1].(gatewayws.AgentStatusPayload)
	if !ok {
		t.Fatalf("unexpected second agent.status payload type: %T", events[1])
	}

	if thinking.Status != "thinking" || idle.Status != "idle" {
		t.Fatalf("unexpected status transitions: thinking=%q idle=%q", thinking.Status, idle.Status)
	}
	if thinking.Session != "session-42" || idle.Session != "session-42" {
		t.Fatalf("expected session in both status events, got thinking=%q idle=%q", thinking.Session, idle.Session)
	}
	if thinking.AgentID != "alpha" || idle.AgentID != "alpha" {
		t.Fatalf("expected routed agent id alpha in status events, got thinking=%q idle=%q", thinking.AgentID, idle.AgentID)
	}
}

func TestExecuteAgentRun_EmitsAndPersistsTurnTelemetry(t *testing.T) {
	prevRouter := controlSessionRouter
	prevEmitter := controlWsEmitter
	prevSessionStore := controlSessionStore
	defer func() {
		controlSessionRouter = prevRouter
		setControlWSEmitter(prevEmitter)
		controlSessionStore = prevSessionStore
	}()

	controlSessionRouter = agent.NewAgentSessionRouter()
	controlSessionRouter.Assign("session-telemetry", "alpha")

	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	controlSessionStore = sessionStore

	capture := &capturingEmitter{}
	setControlWSEmitter(capture)

	jobs := newAgentJobRegistry()
	runID := "run-turn-telemetry"
	jobs.Begin(runID, "session-telemetry")
	executeAgentRun(runID, methods.AgentRequest{SessionID: "session-telemetry", Message: "hello", TimeoutMS: 500}, runtimeFunc(func(_ context.Context, turn agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{
			Text:  "ack: " + turn.UserText,
			Usage: agent.TurnUsage{InputTokens: 7, OutputTokens: 3},
		}, nil
	}), nil, jobs)

	events := capture.eventsByName(gatewayws.EventTurnResult)
	if len(events) != 1 {
		t.Fatalf("expected 1 turn.result event, got %d", len(events))
	}
	payload, ok := events[0].(gatewayws.TurnResultPayload)
	if !ok {
		t.Fatalf("unexpected turn.result payload type: %T", events[0])
	}
	if payload.SessionID != "session-telemetry" || payload.AgentID != "alpha" {
		t.Fatalf("unexpected turn.result payload: %+v", payload)
	}
	if payload.Outcome != string(agent.TurnOutcomeCompleted) || payload.StopReason != string(agent.TurnStopReasonModelText) {
		t.Fatalf("unexpected turn classification payload: %+v", payload)
	}
	if payload.InputTokens != 7 || payload.OutputTokens != 3 {
		t.Fatalf("unexpected turn usage payload: %+v", payload)
	}

	se, ok := sessionStore.Get("session-telemetry")
	if !ok {
		t.Fatal("expected session entry")
	}
	if se.LastTurn == nil {
		t.Fatal("expected persisted last_turn telemetry")
	}
	if se.LastTurn.Outcome != string(agent.TurnOutcomeCompleted) || se.LastTurn.StopReason != string(agent.TurnStopReasonModelText) {
		t.Fatalf("unexpected persisted turn telemetry: %+v", se.LastTurn)
	}
	if se.LastTurn.InputTokens != 7 || se.LastTurn.OutputTokens != 3 {
		t.Fatalf("unexpected persisted turn usage: %+v", se.LastTurn)
	}
}

func TestApplySessionsSpawn_InheritsParentTaskLinkage(t *testing.T) {
	prevRuntime := controlAgentRuntime
	prevJobs := controlAgentJobs
	prevSubagents := controlSubagents
	prevSessionStore := controlSessionStore
	defer func() {
		controlAgentRuntime = prevRuntime
		controlAgentJobs = prevJobs
		controlSubagents = prevSubagents
		controlSessionStore = prevSessionStore
	}()

	controlAgentRuntime = runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{Text: "ok"}, nil
	})
	controlAgentJobs = newAgentJobRegistry()
	controlSubagents = newSubagentRegistry()

	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	controlSessionStore = sessionStore
	if err := sessionStore.Put("parent-session", state.SessionEntry{
		SessionID:    "parent-session",
		AgentID:      "planner",
		ActiveTaskID: "task-parent",
		ActiveRunID:  "run-parent",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed parent session: %v", err)
	}

	resp, err := applySessionsSpawn(context.Background(), methods.SessionsSpawnRequest{
		ParentSessionID: "parent-session",
		AgentID:         "worker",
		Message:         "do the child task",
		TimeoutMS:       500,
	}, state.ConfigDoc{}, nil, nil)
	if err != nil {
		t.Fatalf("applySessionsSpawn: %v", err)
	}
	sessionID, _ := resp["session_id"].(string)
	if strings.TrimSpace(sessionID) == "" {
		t.Fatalf("expected spawned session id, got %#v", resp)
	}
	entry, ok := sessionStore.Get(sessionID)
	if !ok {
		t.Fatalf("expected spawned session entry for %s", sessionID)
	}
	if entry.ParentTaskID != "task-parent" || entry.ParentRunID != "run-parent" {
		t.Fatalf("spawned session linkage = %+v", entry)
	}
	if entry.AgentID != "worker" {
		t.Fatalf("spawned session agent_id = %q, want worker", entry.AgentID)
	}
	if entry.SpawnedBy != "sessions.spawn" {
		t.Fatalf("spawned session spawned_by = %q, want sessions.spawn", entry.SpawnedBy)
	}
}

func TestToolLifecycleEmitter_MapsAgentEventsToWSPayloads(t *testing.T) {
	capture := &capturingEmitter{}
	sink := toolLifecycleEmitter(capture, "alpha")
	sink(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventProgress,
		TS:         122,
		SessionID:  "session-42",
		TurnID:     "turn-7",
		ToolCallID: "call-1",
		ToolName:   "fetch",
		Data: agent.ToolSchedulerDecision{
			Kind:             agent.ToolDecisionKindScheduler,
			Mode:             "parallel",
			BatchIndex:       0,
			BatchCount:       1,
			BatchSize:        1,
			BatchPosition:    0,
			ConcurrencySafe:  true,
			ConcurrencyLimit: 10,
		},
	})
	sink(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventStart,
		TS:         123,
		SessionID:  "session-42",
		TurnID:     "turn-7",
		ToolCallID: "call-1",
		ToolName:   "fetch",
	})
	sink(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventResult,
		TS:         124,
		SessionID:  "session-42",
		TurnID:     "turn-7",
		ToolCallID: "call-1",
		ToolName:   "fetch",
		Result:     "ok",
	})
	sink(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventError,
		TS:         125,
		SessionID:  "session-42",
		TurnID:     "turn-7",
		ToolCallID: "call-2",
		ToolName:   "write",
		Error:      "permission denied",
	})

	progress := capture.eventsByName(gatewayws.EventToolProgress)
	starts := capture.eventsByName(gatewayws.EventToolStart)
	results := capture.eventsByName(gatewayws.EventToolResult)
	errors := capture.eventsByName(gatewayws.EventToolError)
	if len(progress) != 1 || len(starts) != 1 || len(results) != 1 || len(errors) != 1 {
		t.Fatalf("unexpected lifecycle event counts progress=%d start=%d result=%d error=%d", len(progress), len(starts), len(results), len(errors))
	}
	progressPayload, ok := progress[0].(gatewayws.ToolLifecyclePayload)
	if !ok {
		t.Fatalf("unexpected progress payload type: %T", progress[0])
	}
	if progressDecision, ok := progressPayload.Data.(gatewayws.ToolSchedulerDecisionPayload); !ok || progressDecision.Mode != "parallel" || progressDecision.Kind != gatewayws.ToolDecisionKindScheduler {
		t.Fatalf("unexpected progress payload data: %+v", progressPayload)
	}
	startPayload, ok := starts[0].(gatewayws.ToolLifecyclePayload)
	if !ok {
		t.Fatalf("unexpected start payload type: %T", starts[0])
	}
	if startPayload.AgentID != "alpha" || startPayload.SessionID != "session-42" || startPayload.TurnID != "turn-7" {
		t.Fatalf("unexpected start payload: %+v", startPayload)
	}
	errorPayload, ok := errors[0].(gatewayws.ToolLifecyclePayload)
	if !ok {
		t.Fatalf("unexpected error payload type: %T", errors[0])
	}
	if errorPayload.ToolCallID != "call-2" || errorPayload.ToolName != "write" || errorPayload.Error != "permission denied" {
		t.Fatalf("unexpected error payload: %+v", errorPayload)
	}
}

func TestToolLifecycleEmitter_ProjectsLoopDecisionPayloads(t *testing.T) {
	capture := &capturingEmitter{}
	sink := toolLifecycleEmitter(capture, "alpha")
	sink(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventError,
		TS:         130,
		SessionID:  "session-42",
		TurnID:     "turn-8",
		ToolCallID: "call-9",
		ToolName:   "poll",
		Error:      "CRITICAL: tool loop blocked",
		Data: agent.ToolLoopDecision{
			Kind:           agent.ToolDecisionKindLoopDetection,
			Blocked:        true,
			Level:          "critical",
			Detector:       "generic_repeat",
			Count:          4,
			WarningKey:     "poll-repeat",
			PairedToolName: "fetch",
			Message:        "tool loop blocked",
		},
	})

	errors := capture.eventsByName(gatewayws.EventToolError)
	if len(errors) != 1 {
		t.Fatalf("unexpected tool.error count: %d", len(errors))
	}
	errorPayload, ok := errors[0].(gatewayws.ToolLifecyclePayload)
	if !ok {
		t.Fatalf("unexpected error payload type: %T", errors[0])
	}
	decision, ok := errorPayload.Data.(gatewayws.ToolLoopDecisionPayload)
	if !ok {
		t.Fatalf("unexpected error payload data type: %T", errorPayload.Data)
	}
	if decision.Kind != gatewayws.ToolDecisionKindLoopDetection || !decision.Blocked || decision.Detector != "generic_repeat" || decision.PairedToolName != "fetch" {
		t.Fatalf("unexpected loop decision payload: %+v", decision)
	}
}

type stubAgentRuntime struct{}

func (stubAgentRuntime) ProcessTurn(_ context.Context, turn agent.Turn) (agent.TurnResult, error) {
	return agent.TurnResult{Text: "ack: " + strings.TrimSpace(turn.UserText)}, nil
}

type namedStubRuntime struct {
	name string
}

func (r namedStubRuntime) ProcessTurn(_ context.Context, turn agent.Turn) (agent.TurnResult, error) {
	return agent.TurnResult{Text: strings.TrimSpace(r.name) + ": " + strings.TrimSpace(turn.UserText)}, nil
}

type capturedEvent struct {
	name    string
	payload any
}

type capturingEmitter struct {
	mu     sync.Mutex
	events []capturedEvent
}

func (e *capturingEmitter) Emit(event string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, capturedEvent{name: event, payload: payload})
}

func (e *capturingEmitter) eventsByName(name string) []any {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]any, 0, len(e.events))
	for _, evt := range e.events {
		if evt.name == name {
			out = append(out, evt.payload)
		}
	}
	return out
}

type testStore struct {
	mu          sync.Mutex
	replaceable map[string]state.Event
}

func TestControlTrackerPersistsHandledResponsesAcrossRestart(t *testing.T) {
	ctx := context.Background()
	docs := state.NewDocsRepository(newTestStore(), "author")
	tracker := newControlTracker(state.CheckpointDoc{})
	handled := nostruntime.ControlRPCHandled{
		EventID:      "evt-2",
		EventUnix:    time.Now().Unix(),
		CallerPubKey: "caller-a",
		RequestID:    "req-1",
		Response: nostruntime.ControlRPCCachedResponse{
			Payload: `{"result":{"ok":true}}`,
			Tags:    nostr.Tags{{"req", "req-1"}, {"status", "ok"}},
		},
	}
	if err := tracker.MarkHandled(ctx, docs, handled); err != nil {
		t.Fatalf("MarkHandled: %v", err)
	}
	cached, ok := tracker.LookupResponse("caller-a", "req-1")
	if !ok {
		t.Fatal("expected tracker cache hit")
	}
	if cached.Payload != handled.Response.Payload {
		t.Fatalf("unexpected cached payload: %q", cached.Payload)
	}
	checkpoint, err := docs.GetCheckpoint(ctx, "control_ingest")
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if checkpoint.LastEvent != handled.EventID {
		t.Fatalf("unexpected checkpoint event id: %q", checkpoint.LastEvent)
	}
	if len(checkpoint.ControlResponses) != 1 {
		t.Fatalf("expected one persisted control response, got %d", len(checkpoint.ControlResponses))
	}
	restarted := newControlTracker(checkpoint)
	cached, ok = restarted.LookupResponse("caller-a", "req-1")
	if !ok {
		t.Fatal("expected restart cache hit")
	}
	if cached.Payload != handled.Response.Payload {
		t.Fatalf("unexpected restarted payload: %q", cached.Payload)
	}
}

func TestControlTrackerDropsExpiredResponsesOnLoad(t *testing.T) {
	tracker := newControlTracker(state.CheckpointDoc{ControlResponses: []state.ControlResponseCacheDoc{{
		CallerPubKey: "caller-a",
		RequestID:    "req-1",
		Payload:      `{"result":{"ok":true}}`,
		Tags:         [][]string{{"req", "req-1"}},
		EventUnix:    time.Now().Add(-controlResponseCheckpointTTL - time.Minute).Unix(),
	}}})
	if _, ok := tracker.LookupResponse("caller-a", "req-1"); ok {
		t.Fatal("expected expired control response to be pruned")
	}
}

func TestControlTrackerDoesNotPersistSecretsResolveResponses(t *testing.T) {
	ctx := context.Background()
	docs := state.NewDocsRepository(newTestStore(), "author")
	tracker := newControlTracker(state.CheckpointDoc{})
	handled := nostruntime.ControlRPCHandled{
		EventID:      "evt-secret",
		EventUnix:    time.Now().Unix(),
		CallerPubKey: "caller-a",
		RequestID:    "req-secret",
		Method:       methods.MethodSecretsResolve,
		Response: nostruntime.ControlRPCCachedResponse{
			Payload: `{"result":{"assignments":[{"value":"super-secret"}]}}`,
			Tags:    nostr.Tags{{"req", "req-secret"}, {"status", "ok"}},
		},
	}
	if err := tracker.MarkHandled(ctx, docs, handled); err != nil {
		t.Fatalf("MarkHandled: %v", err)
	}
	if _, ok := tracker.LookupResponse("caller-a", "req-secret"); ok {
		t.Fatal("expected secrets.resolve response to skip cache persistence")
	}
	checkpoint, err := docs.GetCheckpoint(ctx, "control_ingest")
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if len(checkpoint.ControlResponses) != 0 {
		t.Fatalf("expected no persisted control responses, got %+v", checkpoint.ControlResponses)
	}
}

func TestIngestTrackerSameSecondEventDedupUsesExplicitIDs(t *testing.T) {
	tracker := newIngestTracker(state.CheckpointDoc{LastUnix: 100, RecentEventIDs: []string{"evt-b"}})
	if !tracker.AlreadyProcessed("evt-b", 100) {
		t.Fatal("expected known same-second event to be treated as processed")
	}
	if tracker.AlreadyProcessed("evt-a", 100) {
		t.Fatal("unexpected same-second event dedupe based on lexical ordering")
	}
}

func newTestStore() *testStore {
	return &testStore{replaceable: map[string]state.Event{}}
}

func (s *testStore) GetLatestReplaceable(_ context.Context, addr state.Address) (state.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evt, ok := s.replaceable[s.key(addr)]
	if !ok {
		return state.Event{}, state.ErrNotFound
	}
	return evt, nil
}

func (s *testStore) PutReplaceable(_ context.Context, addr state.Address, content string, extraTags [][]string) (state.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evt := state.Event{
		ID:        fmt.Sprintf("evt:%s", s.key(addr)),
		PubKey:    addr.PubKey,
		Kind:      addr.Kind,
		CreatedAt: time.Now().Unix(),
		Tags:      append(extraTags, []string{"d", addr.DTag}),
		Content:   content,
	}
	s.replaceable[s.key(addr)] = evt
	return evt, nil
}

func (s *testStore) PutAppend(_ context.Context, _ state.Address, _ string, _ [][]string) (state.Event, error) {
	return state.Event{}, nil
}

func (s *testStore) ListByTag(_ context.Context, kind events.Kind, tagName, tagValue string, limit int) ([]state.Event, error) {
	return s.listByTag(kind, "", tagName, tagValue, limit), nil
}

func (s *testStore) ListByTagForAuthor(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]state.Event, error) {
	return s.listByTag(kind, authorPubKey, tagName, tagValue, limit), nil
}

func (s *testStore) ListByTagPage(_ context.Context, kind events.Kind, tagName, tagValue string, limit int, cursor *state.EventPageCursor) (state.EventPage, error) {
	return s.listByTagPage(kind, "", tagName, tagValue, limit, cursor), nil
}

func (s *testStore) ListByTagForAuthorPage(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *state.EventPageCursor) (state.EventPage, error) {
	return s.listByTagPage(kind, authorPubKey, tagName, tagValue, limit, cursor), nil
}

func (s *testStore) listByTag(kind events.Kind, authorPubKey, tagName, tagValue string, limit int) []state.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]state.Event, 0, limit)
	for _, evt := range s.replaceable {
		if evt.Kind != kind {
			continue
		}
		if authorPubKey != "" && evt.PubKey != authorPubKey {
			continue
		}
		for _, tag := range evt.Tags {
			if len(tag) >= 2 && tag[0] == tagName && tag[1] == tagValue {
				out = append(out, evt)
				break
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *testStore) listByTagPage(kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *state.EventPageCursor) state.EventPage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]state.Event, 0, len(s.replaceable))
	for _, evt := range s.replaceable {
		if evt.Kind != kind {
			continue
		}
		if authorPubKey != "" && evt.PubKey != authorPubKey {
			continue
		}
		for _, tag := range evt.Tags {
			if len(tag) >= 2 && tag[0] == tagName && tag[1] == tagValue {
				out = append(out, evt)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	skip := make(map[string]struct{})
	if cursor != nil {
		for _, id := range cursor.SkipIDs {
			if id == "" {
				continue
			}
			skip[id] = struct{}{}
		}
	}
	filtered := make([]state.Event, 0, len(out))
	for _, evt := range out {
		if cursor != nil && cursor.Until > 0 {
			if evt.CreatedAt > cursor.Until {
				continue
			}
			if evt.CreatedAt == cursor.Until {
				if _, ok := skip[evt.ID]; ok {
					continue
				}
			}
		}
		filtered = append(filtered, evt)
	}
	page := state.EventPage{Events: filtered}
	if len(filtered) > limit {
		page.Events = filtered[:limit]
		boundaryUnix := page.Events[len(page.Events)-1].CreatedAt
		nextSkip := make(map[string]struct{})
		var skipIDs []string
		if cursor != nil && cursor.Until == boundaryUnix {
			for _, id := range cursor.SkipIDs {
				if id == "" {
					continue
				}
				if _, ok := nextSkip[id]; ok {
					continue
				}
				nextSkip[id] = struct{}{}
				skipIDs = append(skipIDs, id)
			}
		}
		for _, evt := range page.Events {
			if evt.CreatedAt != boundaryUnix || evt.ID == "" {
				continue
			}
			if _, ok := nextSkip[evt.ID]; ok {
				continue
			}
			nextSkip[evt.ID] = struct{}{}
			skipIDs = append(skipIDs, evt.ID)
		}
		sort.Strings(skipIDs)
		page.NextCursor = &state.EventPageCursor{Until: boundaryUnix, SkipIDs: skipIDs}
	}
	return page
}

func (s *testStore) key(addr state.Address) string {
	return fmt.Sprintf("%d|%s|%s", addr.Kind, addr.PubKey, addr.DTag)
}

func TestUpdateSessionDocSerializesConcurrentMutations(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		err := updateSessionDoc(context.Background(), docs, "session-race", "peer-a", func(doc *state.SessionDoc) error {
			doc.LastInboundAt = 111
			if doc.Meta == nil {
				doc.Meta = map[string]any{}
			}
			doc.Meta["source"] = "inbound"
			time.Sleep(25 * time.Millisecond)
			return nil
		})
		if err != nil {
			t.Errorf("update inbound: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		err := updateSessionDoc(context.Background(), docs, "session-race", "peer-b", func(doc *state.SessionDoc) error {
			doc.LastReplyAt = 222
			doc.Meta = mergeSessionMeta(doc.Meta, map[string]any{"active_turn": true})
			return nil
		})
		if err != nil {
			t.Errorf("update reply: %v", err)
		}
	}()

	close(start)
	wg.Wait()

	session, err := docs.GetSession(context.Background(), "session-race")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.LastInboundAt != 111 {
		t.Fatalf("expected LastInboundAt=111, got %d", session.LastInboundAt)
	}
	if session.LastReplyAt != 222 {
		t.Fatalf("expected LastReplyAt=222, got %d", session.LastReplyAt)
	}
	if got, _ := session.Meta["active_turn"].(bool); !got {
		t.Fatalf("expected active_turn=true in meta, got %#v", session.Meta)
	}
	if got, _ := session.Meta["source"].(string); got != "inbound" {
		t.Fatalf("expected source=inbound in meta, got %#v", session.Meta)
	}
	if session.PeerPubKey == "" {
		t.Fatalf("expected peer pubkey to be populated: %#v", session)
	}
}

func TestHandleControlRPCRequest_SessionsResetWaitsForTurnRelease(t *testing.T) {
	prevTurns := controlSessionTurns
	controlSessionTurns = autoreply.NewSessionTurns()
	defer func() { controlSessionTurns = prevTurns }()

	release, ok := controlSessionTurns.TryAcquire("s1")
	if !ok {
		t.Fatal("expected to acquire session turn slot")
	}

	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	transcript := state.NewTranscriptRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	if _, err := docs.PutSession(context.Background(), "s1", state.SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer", LastInboundAt: 10, LastReplyAt: 20, Meta: map[string]any{"active_turn": true}}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := handleControlRPCRequest(ctx, nostruntime.ControlRPCInbound{
			FromPubKey: "caller",
			Method:     methods.MethodSessionsReset,
			Params:     json.RawMessage(`{"session_id":"s1"}`),
		}, nil, nil, newChatAbortRegistry(), nil, nil, nil, docs, transcript, nil, cfgState, nil, nil, time.Now())
		done <- err
	}()

	time.Sleep(75 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("reset returned before turn release: %v", err)
	default:
	}

	release()
	if err := <-done; err != nil {
		t.Fatalf("reset after release: %v", err)
	}

	session, err := docs.GetSession(context.Background(), "s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.LastInboundAt != 0 || session.LastReplyAt != 0 {
		t.Fatalf("expected reset timestamps cleared, got %#v", session)
	}
	if len(session.Meta) != 0 {
		t.Fatalf("expected reset meta cleared, got %#v", session.Meta)
	}
}

func TestRunSessionsPruneSkipsBusySession(t *testing.T) {
	prevTurns := controlSessionTurns
	controlSessionTurns = autoreply.NewSessionTurns()
	defer func() { controlSessionTurns = prevTurns }()

	release, ok := controlSessionTurns.TryAcquire("s1")
	if !ok {
		t.Fatal("expected to acquire session turn slot")
	}
	defer release()

	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	transcript := state.NewTranscriptRepository(store, "author")
	oldUnix := time.Now().Add(-48 * time.Hour).Unix()
	if _, err := docs.PutSession(context.Background(), "s1", state.SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer", LastInboundAt: oldUnix}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := transcript.PutEntry(context.Background(), state.TranscriptEntryDoc{Version: 1, SessionID: "s1", EntryID: "e1", Role: "user", Text: "hello", Unix: oldUnix}); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err := runSessionsPrune(ctx, docs, transcript, methods.SessionsPruneRequest{OlderThanDays: 1}, "manual")
	if err != nil {
		t.Fatalf("runSessionsPrune: %v", err)
	}
	if got := result["deleted_count"].(int); got != 0 {
		t.Fatalf("expected deleted_count=0, got %d", got)
	}
	if got := result["skipped_count"].(int); got != 1 {
		t.Fatalf("expected skipped_count=1, got %d", got)
	}
	session, err := docs.GetSession(context.Background(), "s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if deleted, _ := session.Meta["deleted"].(bool); deleted {
		t.Fatalf("expected busy session to remain undeleted: %#v", session)
	}
	entries, err := transcript.ListSession(context.Background(), "s1", 10)
	if err != nil {
		t.Fatalf("list transcript: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected transcript entry to remain, got %d", len(entries))
	}
}

func TestRunSessionsPruneRechecksEligibilityAfterRelease(t *testing.T) {
	prevTurns := controlSessionTurns
	controlSessionTurns = autoreply.NewSessionTurns()
	defer func() { controlSessionTurns = prevTurns }()

	release, ok := controlSessionTurns.TryAcquire("s1")
	if !ok {
		t.Fatal("expected to acquire session turn slot")
	}

	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	transcript := state.NewTranscriptRepository(store, "author")
	oldUnix := time.Now().Add(-48 * time.Hour).Unix()
	if _, err := docs.PutSession(context.Background(), "s1", state.SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer", LastInboundAt: oldUnix}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := transcript.PutEntry(context.Background(), state.TranscriptEntryDoc{Version: 1, SessionID: "s1", EntryID: "e1", Role: "user", Text: "hello", Unix: oldUnix}); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	done := make(chan struct {
		result map[string]any
		err    error
	}, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result, err := runSessionsPrune(ctx, docs, transcript, methods.SessionsPruneRequest{OlderThanDays: 1}, "manual")
		done <- struct {
			result map[string]any
			err    error
		}{result: result, err: err}
	}()

	time.Sleep(75 * time.Millisecond)
	if err := updateSessionDoc(context.Background(), docs, "s1", "peer", func(session *state.SessionDoc) error {
		session.LastInboundAt = time.Now().Unix()
		return nil
	}); err != nil {
		t.Fatalf("refresh session activity: %v", err)
	}
	release()

	out := <-done
	if out.err != nil {
		t.Fatalf("runSessionsPrune: %v", out.err)
	}
	if got := out.result["deleted_count"].(int); got != 0 {
		t.Fatalf("expected deleted_count=0 after activity refresh, got %d", got)
	}
	if got := out.result["skipped_count"].(int); got != 1 {
		t.Fatalf("expected skipped_count=1 after activity refresh, got %d", got)
	}
	session, err := docs.GetSession(context.Background(), "s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if deleted, _ := session.Meta["deleted"].(bool); deleted {
		t.Fatalf("expected reactivated session to remain undeleted: %#v", session)
	}
	entries, err := transcript.ListSession(context.Background(), "s1", 10)
	if err != nil {
		t.Fatalf("list transcript: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected transcript entry to remain, got %d", len(entries))
	}
}

func TestRotateSessionCoordinatedWaitsBeforeMutatingRouterState(t *testing.T) {
	prevTurns := controlSessionTurns
	controlSessionTurns = autoreply.NewSessionTurns()
	defer func() { controlSessionTurns = prevTurns }()

	release, ok := controlSessionTurns.TryAcquire("s1")
	if !ok {
		t.Fatal("expected to acquire session turn slot")
	}

	store := newTestStore()
	transcript := state.NewTranscriptRepository(store, "author")
	if _, err := transcript.PutEntry(context.Background(), state.TranscriptEntryDoc{Version: 1, SessionID: "s1", EntryID: "e1", Role: "user", Text: "hello", Unix: time.Now().Unix()}); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	sessionRouter := agent.NewAgentSessionRouter()
	sessionRouter.Assign("s1", "agent-x")
	var seen sync.Map
	seen.Store("s1", struct{}{})

	dir := t.TempDir()
	sessionStore, err := state.NewSessionStore(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := sessionStore.Put("s1", state.SessionEntry{SessionID: "s1", SessionFile: "x"}); err != nil {
		t.Fatalf("seed session store: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- rotateSessionCoordinated(ctx, "s1", "test", false, newChatAbortRegistry(), sessionRouter, &seen, nil, transcript, sessionStore, state.ConfigDoc{})
	}()

	time.Sleep(75 * time.Millisecond)
	if got := sessionRouter.Get("s1"); got != "agent-x" {
		t.Fatalf("router mutated before turn release: %q", got)
	}
	if _, ok := seen.Load("s1"); !ok {
		t.Fatal("seenChannelSessions mutated before turn release")
	}

	release()
	if err := <-done; err != nil {
		t.Fatalf("rotateSessionCoordinated: %v", err)
	}
	if got := sessionRouter.Get("s1"); got != "" {
		t.Fatalf("expected router cleared after release, got %q", got)
	}
	if _, ok := seen.Load("s1"); ok {
		t.Fatal("expected seenChannelSessions cleared after release")
	}
}

func TestPersistAndIngestTurnHistory_PersistsTurnResultMetadata(t *testing.T) {
	store := newTestStore()
	transcript := state.NewTranscriptRepository(store, "author")
	delta := []agent.ConversationMessage{
		{Role: "assistant", Content: "Calling fetch", ToolCalls: []agent.ToolCallRef{{ID: "call-1", Name: "fetch", ArgsJSON: `{"q":"nostr"}`}}},
		{Role: "tool", ToolCallID: "call-1", Content: "ok"},
		{Role: "assistant", Content: "done"},
	}
	turnMeta, ok := agent.BuildTurnResultMetadata(agent.TurnResult{
		Text:         "done",
		HistoryDelta: delta,
		Outcome:      agent.TurnOutcomeCompletedWithTools,
		StopReason:   agent.TurnStopReasonModelText,
		Usage:        agent.TurnUsage{InputTokens: 11, OutputTokens: 7},
	}, nil)
	if !ok {
		t.Fatal("expected turn metadata")
	}

	persistAndIngestTurnHistory(context.Background(), transcript, nil, "s1", "evt-1", delta, &turnMeta)

	entries, err := transcript.ListSession(context.Background(), "s1", 10)
	if err != nil {
		t.Fatalf("list transcript: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 transcript entries, got %d", len(entries))
	}
	turnResultCount := 0
	for _, entry := range entries {
		if got := entry.Meta["request_event_id"]; got != "evt-1" {
			t.Fatalf("entry %s missing request_event_id, got %#v", entry.EntryID, got)
		}
		turnResult, ok := entry.Meta["turn_result"].(map[string]any)
		if !ok {
			continue
		}
		turnResultCount++
		if entry.EntryID != "turn:evt-1:assistant:2" {
			t.Fatalf("unexpected terminal metadata entry: %s", entry.EntryID)
		}
		if got := turnResult["outcome"]; got != string(agent.TurnOutcomeCompletedWithTools) {
			t.Fatalf("entry %s outcome = %#v", entry.EntryID, got)
		}
		if got := turnResult["stop_reason"]; got != string(agent.TurnStopReasonModelText) {
			t.Fatalf("entry %s stop_reason = %#v", entry.EntryID, got)
		}
		usage, ok := turnResult["usage"].(map[string]any)
		if !ok {
			t.Fatalf("entry %s missing usage metadata: %#v", entry.EntryID, turnResult)
		}
		if got, ok := usage["input_tokens"].(float64); !ok || int64(got) != 11 {
			t.Fatalf("entry %s input_tokens = %#v", entry.EntryID, usage["input_tokens"])
		}
		if got, ok := usage["output_tokens"].(float64); !ok || int64(got) != 7 {
			t.Fatalf("entry %s output_tokens = %#v", entry.EntryID, usage["output_tokens"])
		}
	}
	if turnResultCount != 1 {
		t.Fatalf("expected 1 terminal turn_result metadata entry, got %d", turnResultCount)
	}
}

func TestPersistAndIngestTurnHistory_PersistsPartialTurnResultMetadata(t *testing.T) {
	store := newTestStore()
	transcript := state.NewTranscriptRepository(store, "author")
	delta := []agent.ConversationMessage{
		{Role: "assistant", Content: "Calling fetch", ToolCalls: []agent.ToolCallRef{{ID: "call-1", Name: "fetch"}}},
		{Role: "tool", ToolCallID: "call-1", Content: "blocked"},
	}
	turnErr := &agent.TurnExecutionError{
		Cause: fmt.Errorf("tool loop blocked"),
		Partial: agent.TurnResult{
			HistoryDelta: delta,
			Outcome:      agent.TurnOutcomeBlocked,
			StopReason:   agent.TurnStopReasonLoopBlocked,
			Usage:        agent.TurnUsage{InputTokens: 9},
		},
	}
	turnMeta := turnResultMetadataPtr(agent.TurnResult{}, turnErr)
	if turnMeta == nil {
		t.Fatal("expected partial turn metadata")
	}

	persistAndIngestTurnHistory(context.Background(), transcript, nil, "s1", "evt-2", delta, turnMeta)

	entries, err := transcript.ListSession(context.Background(), "s1", 10)
	if err != nil {
		t.Fatalf("list transcript: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 transcript entries, got %d", len(entries))
	}
	turnResultCount := 0
	for _, entry := range entries {
		turnResult, ok := entry.Meta["turn_result"].(map[string]any)
		if !ok {
			continue
		}
		turnResultCount++
		if entry.EntryID != "turn:evt-2:tool:call-1" {
			t.Fatalf("unexpected terminal metadata entry: %s", entry.EntryID)
		}
		if got := turnResult["outcome"]; got != string(agent.TurnOutcomeBlocked) {
			t.Fatalf("entry %s outcome = %#v", entry.EntryID, got)
		}
		if got := turnResult["stop_reason"]; got != string(agent.TurnStopReasonLoopBlocked) {
			t.Fatalf("entry %s stop_reason = %#v", entry.EntryID, got)
		}
	}
	if turnResultCount != 1 {
		t.Fatalf("expected 1 terminal turn_result metadata entry, got %d", turnResultCount)
	}
}

func TestDeleteSessionCoordinatedWaitsAndDoesNotCreatePhantomSessionDoc(t *testing.T) {
	prevTurns := controlSessionTurns
	controlSessionTurns = autoreply.NewSessionTurns()
	defer func() { controlSessionTurns = prevTurns }()

	release, ok := controlSessionTurns.TryAcquire("missing")
	if !ok {
		t.Fatal("expected to acquire session turn slot")
	}

	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	transcript := state.NewTranscriptRepository(store, "author")
	sessionRouter := agent.NewAgentSessionRouter()
	sessionRouter.Assign("missing", "agent-x")
	var seen sync.Map
	seen.Store("missing", struct{}{})

	dir := t.TempDir()
	sessionStore, err := state.NewSessionStore(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := sessionStore.Put("missing", state.SessionEntry{SessionID: "missing", SessionFile: "x"}); err != nil {
		t.Fatalf("seed session store: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- deleteSessionCoordinated(ctx, "missing", newChatAbortRegistry(), sessionRouter, &seen, docs, transcript, sessionStore)
	}()

	time.Sleep(75 * time.Millisecond)
	if got := sessionRouter.Get("missing"); got != "agent-x" {
		t.Fatalf("router mutated before turn release: %q", got)
	}
	if _, ok := seen.Load("missing"); !ok {
		t.Fatal("seenChannelSessions mutated before turn release")
	}
	if _, err := docs.GetSession(context.Background(), "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected no session doc before release, got err=%v", err)
	}

	release()
	if err := <-done; err != nil {
		t.Fatalf("deleteSessionCoordinated: %v", err)
	}
	if got := sessionRouter.Get("missing"); got != "" {
		t.Fatalf("expected router cleared after release, got %q", got)
	}
	if _, ok := seen.Load("missing"); ok {
		t.Fatal("expected seenChannelSessions cleared after release")
	}
	if _, err := docs.GetSession(context.Background(), "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected no phantom session doc after delete, got err=%v", err)
	}
	if _, ok := sessionStore.Get("missing"); ok {
		t.Fatal("expected session store entry deleted")
	}
}

func methodsAnyToStringMapForTest(value any) (map[string]string, error) {
	out := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		for key, item := range typed {
			out[strings.TrimSpace(key)] = strings.TrimSpace(item)
		}
		return out, nil
	case map[string]any:
		for key, item := range typed {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("non-string env value for %q", key)
			}
			out[strings.TrimSpace(key)] = strings.TrimSpace(s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported map type %T", value)
	}
}

// ─── RPCCorrelator inbox tests ──────────────────────────────────────────────

func TestRPCCorrelatorInboxStoreAndDrain(t *testing.T) {
	c := newRPCCorrelator()
	pk := "cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400"

	c.StoreInbox(pk, "hello from agent")
	c.StoreInbox(pk, "second message")

	if c.InboxCount() != 2 {
		t.Fatalf("expected 2 inbox entries, got %d", c.InboxCount())
	}

	// Peek should return entries without removing them.
	peeked := c.PeekInbox(pk)
	if len(peeked) != 2 {
		t.Fatalf("peek expected 2, got %d", len(peeked))
	}
	if c.InboxCount() != 2 {
		t.Fatal("peek should not remove entries")
	}

	// Drain should return and remove entries.
	drained := c.DrainInbox(pk)
	if len(drained) != 2 {
		t.Fatalf("drain expected 2, got %d", len(drained))
	}
	if drained[0].Text != "hello from agent" {
		t.Errorf("unexpected text: %s", drained[0].Text)
	}
	if c.InboxCount() != 0 {
		t.Fatal("inbox should be empty after drain")
	}

	// Second drain should be empty.
	if len(c.DrainInbox(pk)) != 0 {
		t.Fatal("expected empty after second drain")
	}
}

func TestRPCCorrelatorInboxCapacity(t *testing.T) {
	c := newRPCCorrelator()
	pk := "cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400"

	// Store more than maxInboxPerAgent (50).
	for i := 0; i < 60; i++ {
		c.StoreInbox(pk, fmt.Sprintf("msg-%d", i))
	}

	entries := c.DrainInbox(pk)
	if len(entries) != 50 {
		t.Fatalf("expected 50 (capped), got %d", len(entries))
	}
	// Should have the newest 50 (msg-10 through msg-59).
	if entries[0].Text != "msg-10" {
		t.Errorf("expected oldest kept to be msg-10, got %s", entries[0].Text)
	}
}

func TestRPCCorrelatorDeliverDoesNotStoreInInbox(t *testing.T) {
	c := newRPCCorrelator()
	pk := "cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400"

	// Register a synchronous waiter.
	replyCh, cancel := c.Register(pk)
	defer cancel()

	// Deliver should go to the waiter, not the inbox.
	if !c.Deliver(pk, "sync reply") {
		t.Fatal("expected deliver to succeed")
	}

	select {
	case reply := <-replyCh:
		if reply != "sync reply" {
			t.Errorf("unexpected reply: %s", reply)
		}
	default:
		t.Fatal("expected reply on channel")
	}

	// Inbox should be empty since it went to the synchronous waiter.
	if len(c.DrainInbox(pk)) != 0 {
		t.Fatal("inbox should be empty when sync waiter consumed the message")
	}
}
