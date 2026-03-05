package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"swarmstr/internal/agent"
	"swarmstr/internal/gateway/methods"
	"swarmstr/internal/nostr/events"
	nostruntime "swarmstr/internal/nostr/runtime"
	"swarmstr/internal/store/state"
)

func TestHandleControlRPCRequest_SupportedMethods(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSupportedMethods,
		Params:     json.RawMessage(`[]`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("handleControlRPCRequest error: %v", err)
	}
	list, ok := res.Result.([]string)
	if !ok || len(list) == 0 {
		t.Fatalf("unexpected result: %#v", res.Result)
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
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now().Add(-time.Minute))
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now().Add(-time.Minute))
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now().Add(-time.Minute))
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("expected create-if-missing for expected_version=0, got err=%v", err)
	}

	if _, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodListPut,
		Params:     json.RawMessage(`{"name":"allowlist","items":["b"],"expected_version":0}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now().Add(-time.Minute)); err == nil {
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now().Add(-time.Minute)); err != nil {
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
	shape = mapGatewayWSError(fmt.Errorf("forbidden"))
	if shape == nil || shape.Code != "NOT_LINKED" {
		t.Fatalf("unexpected shape for forbidden: %#v", shape)
	}
	conflict := &methods.PreconditionConflictError{Resource: "config", ExpectedVersion: 2, CurrentVersion: 3}
	shape = mapGatewayWSError(conflict)
	if shape == nil || shape.Code != "INVALID_REQUEST" {
		t.Fatalf("unexpected shape for precondition conflict: %#v", shape)
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
	}, nil, nil, registry, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("chat.abort error: %v", err)
	}
	payload, ok := res.Result.(map[string]any)
	if !ok || payload["aborted"] != true || payload["aborted_count"] != 1 {
		t.Fatalf("unexpected chat.abort result: %#v", res.Result)
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

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodLogsTail,
		Params:     json.RawMessage(`{"limit":10}`),
	}, nil, nil, nil, usage, logs, channels, nil, nil, nil, cfgState, nil, time.Now())
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
		Method:     methods.MethodChannelsLogout,
		Params:     json.RawMessage(`{"channel":"nostr"}`),
	}, nil, nil, nil, usage, logs, channels, nil, nil, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, usage, logs, channels, nil, nil, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, usage, logs, channels, nil, nil, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("agent error: %v", err)
	}
	out, _ := res.Result.(map[string]any)
	runID, _ := out["run_id"].(string)
	if strings.TrimSpace(runID) == "" {
		t.Fatalf("expected run_id, got: %#v", res.Result)
	}

	waitRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentWait,
		Params:     json.RawMessage(fmt.Sprintf(`{"run_id":%q,"timeout_ms":1000}`, runID)),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("agent.wait error: %v", err)
	}
	out, _ = waitRes.Result.(map[string]any)
	if out["status"] != "ok" {
		t.Fatalf("unexpected wait result: %#v", waitRes.Result)
	}

	identityRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentIdentityGet,
		Params:     json.RawMessage(`{"session_id":"s1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("agent.identity.get error: %v", err)
	}
	out, _ = identityRes.Result.(map[string]any)
	if out["agent_id"] != "main" {
		t.Fatalf("unexpected identity result: %#v", identityRes.Result)
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
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now().Add(-time.Minute))
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("config.patch error: %v", err)
	}
	if got := cfgState.Get().DM.Policy; got != "open" {
		t.Fatalf("dm.policy=%q want open after patch", got)
	}

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodConfigSchema,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
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
}

func TestHandleControlRPCRequest_ChatHistoryAndSessionViews(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	transcript := state.NewTranscriptRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})

	if _, err := docs.PutSession(context.Background(), "s1", state.SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer"}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for i := 0; i < 2; i++ {
		_, _ = transcript.PutEntry(context.Background(), state.TranscriptEntryDoc{Version: 1, SessionID: "s1", EntryID: fmt.Sprintf("e%d", i), Role: "user", Text: "hi", Unix: time.Now().Unix() + int64(i)})
	}

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSessionsList,
		Params:     json.RawMessage(`{"limit":10}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("sessions.list error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	if len(payload["sessions"].([]state.SessionDoc)) != 1 {
		t.Fatalf("unexpected sessions.list payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSessionsPreview,
		Params:     json.RawMessage(`{"session_id":"s1","limit":5}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("sessions.compact error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["dropped"].(int) < 1 {
		t.Fatalf("unexpected sessions.compact result: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSessionsDelete,
		Params:     json.RawMessage(`{"session_id":"s1"}`),
	}, nil, nil, nil, nil, nil, nil, docs, transcript, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("sessions.delete error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["deleted"] != true {
		t.Fatalf("unexpected sessions.delete result: %#v", res.Result)
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.create error: %v", err)
	}

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsList,
		Params:     json.RawMessage(`{"limit":10}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.update error: %v", err)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsFilesSet,
		Params:     json.RawMessage(`{"agent_id":"main","name":"instructions.md","content":"hello"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("agents.files.set error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodAgentsFilesGet,
		Params:     json.RawMessage(`{"agent_id":"main","name":"instructions.md"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
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

func TestHandleControlRPCRequest_ModelsToolsSkillsMethods(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	tools := agent.NewToolRegistry()
	tools.Register("memory.search", func(context.Context, map[string]any) (string, error) {
		return "[]", nil
	})

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodModelsList,
		Params:     json.RawMessage(`{}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
	if err != nil {
		t.Fatalf("tools.catalog error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["agent_id"] != "main" {
		t.Fatalf("unexpected tools.catalog payload: %#v", res.Result)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSkillsInstall,
		Params:     json.RawMessage(`{"name":"nostr-core","install_id":"builtin"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
	if err != nil {
		t.Fatalf("skills.install error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSkillsStatus,
		Params:     json.RawMessage(`{"agent_id":"main"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
	if err != nil {
		t.Fatalf("skills.status error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["count"].(int) < 1 {
		t.Fatalf("unexpected skills.status payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSkillsBins,
		Params:     json.RawMessage(`{"agent_id":"main"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
	if err != nil {
		t.Fatalf("skills.bins error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if _, ok := payload["bins"]; !ok {
		t.Fatalf("unexpected skills.bins payload: %#v", res.Result)
	}

	enabled := true
	apiKey := "token"
	_, _, err = applySkillUpdate(context.Background(), docs, cfgState, methods.SkillsUpdateRequest{SkillKey: "nostr-core", Enabled: &enabled, APIKey: &apiKey, Env: map[string]string{"A": "B"}})
	if err != nil {
		t.Fatalf("applySkillUpdate helper failed: %v", err)
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
	if err != nil {
		t.Fatalf("device.pair.approve error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodDeviceTokenRotate,
		Params:     json.RawMessage(`{"device_id":"d1","role":"node","scopes":["operator.read"]}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, tools, time.Now())
	if err != nil {
		t.Fatalf("device.token.revoke error: %v", err)
	}
}

func TestHandleControlRPCRequest_NodeInvokeAndCronMethods(t *testing.T) {
	docs := state.NewDocsRepository(newTestStore(), "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		Extra: map[string]any{"pairing": map[string]any{"node_paired": []any{map[string]any{"node_id": "n1", "display_name": "Node One", "caps": []any{"canvas"}, "approved_at_ms": int64(1)}}}},
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
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("node.list error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	nodes, _ := payload["nodes"].([]map[string]any)
	if len(nodes) != 1 {
		t.Fatalf("unexpected node.list payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeDescribe,
		Params:     json.RawMessage(`{"node_id":"n1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("node.describe error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["status"] != "paired" {
		t.Fatalf("unexpected node.describe payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeRename,
		Params:     json.RawMessage(`{"node_id":"n1","name":"Kitchen"}`),
	}, nil, nil, nil, nil, nil, nil, docs, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("node.rename error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeCanvasCapabilityRefresh,
		Params:     json.RawMessage(`{"node_id":"n1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("node.canvas.capability.refresh error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeInvoke,
		Params:     json.RawMessage(`{"node_id":"n1","command":"ping"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
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
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("node.event error: %v", err)
	}

	_, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodNodeInvokeResult,
		Params:     json.RawMessage(fmt.Sprintf(`{"run_id":%q,"status":"ok","result":{"pong":true}}`, runID)),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("node.invoke.result error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodCronAdd,
		Params:     json.RawMessage(`{"id":"c1","schedule":"* * * * *","method":"status.get"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
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
		Method:     methods.MethodCronRun,
		Params:     json.RawMessage(`{"id":"c1"}`),
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("cron.run error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["ok"] != true {
		t.Fatalf("unexpected cron.run payload: %#v", res.Result)
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

	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalsSet, Params: json.RawMessage(`{"approvals":{"allow":true}}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("exec.approvals.set error: %v", err)
	}
	payload, _ := res.Result.(map[string]any)
	if payload["ok"] != true {
		t.Fatalf("unexpected exec.approvals.set payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalRequest, Params: json.RawMessage(`{"command":"ls"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
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
	res, err = handleControlRPCRequest(ctx, nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalWaitDecision, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"timeout_ms":100}`, approvalID))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("exec.approval.waitDecision error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["resolved"] != false {
		t.Fatalf("unexpected exec.approval.waitDecision timeout payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalResolve, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"decision":"approve"}`, approvalID))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("exec.approval.resolve error: %v", err)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalWaitDecision, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"timeout_ms":5000}`, approvalID))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
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
		res2, _ := handleControlRPCRequest(ctxCancel, nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalRequest, Params: json.RawMessage(`{"command":"test-cancel"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
		payload2, _ := res2.Result.(map[string]any)
		approvalID2, _ := payload2["id"].(string)
		res3, _ := handleControlRPCRequest(ctxCancel, nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalWaitDecision, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"timeout_ms":5000}`, approvalID2))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
		payload3, _ := res3.Result.(map[string]any)
		if payload3["cancelled"] != true {
			t.Errorf("expected cancelled=true, got: %#v", payload3)
		}
		done <- true
	}()
	time.Sleep(20 * time.Millisecond)
	cancelFunc()
	<-done

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalRequest, Params: json.RawMessage(`{"command":"test-concurrent","timeout_ms":5000}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("exec.approval.request concurrent error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	approvalID3, _ := payload["id"].(string)

	done2 := make(chan map[string]any, 2)
	go func() {
		res4, _ := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalWaitDecision, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"timeout_ms":2000}`, approvalID3))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
		payload4, _ := res4.Result.(map[string]any)
		done2 <- payload4
	}()
	go func() {
		res5, _ := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalWaitDecision, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"timeout_ms":2000}`, approvalID3))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
		payload5, _ := res5.Result.(map[string]any)
		done2 <- payload5
	}()
	time.Sleep(50 * time.Millisecond)
	_, _ = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodExecApprovalResolve, Params: json.RawMessage(fmt.Sprintf(`{"id":%q,"decision":"approve"}`, approvalID3))}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())

	result1 := <-done2
	result2 := <-done2
	if result1["resolved"] != true || result2["resolved"] != true {
		t.Fatalf("concurrent waiters should both receive resolution: r1=%#v r2=%#v", result1, result2)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodWizardStart, Params: json.RawMessage(`{"mode":"local"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("wizard.start error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	sessionID, _ := payload["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("unexpected wizard.start payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodVoicewakeSet, Params: json.RawMessage(`{"triggers":["openclaw","swarmstr"]}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("voicewake.set error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if _, ok := payload["triggers"]; !ok {
		t.Fatalf("unexpected voicewake.set payload: %#v", res.Result)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodTTSSetProvider, Params: json.RawMessage(`{"provider":"edge"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("tts.setProvider error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["provider"] != "edge" {
		t.Fatalf("expected provider=edge, got: %#v", payload)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodTTSSetProvider, Params: json.RawMessage(`{"provider":"invalid-provider"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("tts.setProvider invalid error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["provider"] != "openai" {
		t.Fatalf("expected invalid provider to default to openai, got: %#v", payload)
	}

	res, err = handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{FromPubKey: "caller", Method: methods.MethodTTSConvert, Params: json.RawMessage(`{"text":"hello"}`)}, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfgState, nil, time.Now())
	if err != nil {
		t.Fatalf("tts.convert error: %v", err)
	}
	payload, _ = res.Result.(map[string]any)
	if payload["provider"] == "" {
		t.Fatalf("unexpected tts.convert payload: %#v", res.Result)
	}
}

type stubAgentRuntime struct{}

func (stubAgentRuntime) ProcessTurn(_ context.Context, turn agent.Turn) (agent.TurnResult, error) {
	return agent.TurnResult{Text: "ack: " + strings.TrimSpace(turn.UserText)}, nil
}

type testStore struct {
	replaceable map[string]state.Event
}

func newTestStore() *testStore {
	return &testStore{replaceable: map[string]state.Event{}}
}

func (s *testStore) GetLatestReplaceable(_ context.Context, addr state.Address) (state.Event, error) {
	evt, ok := s.replaceable[s.key(addr)]
	if !ok {
		return state.Event{}, state.ErrNotFound
	}
	return evt, nil
}

func (s *testStore) PutReplaceable(_ context.Context, addr state.Address, content string, extraTags [][]string) (state.Event, error) {
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

func (s *testStore) listByTag(kind events.Kind, authorPubKey, tagName, tagValue string, limit int) []state.Event {
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

func (s *testStore) key(addr state.Address) string {
	return fmt.Sprintf("%d|%s|%s", addr.Kind, addr.PubKey, addr.DTag)
}
