package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

type stubDMTransport struct {
	pubkey string
}

func (s stubDMTransport) SendDM(_ context.Context, _, _ string) error { return nil }
func (s stubDMTransport) PublicKey() string                           { return s.pubkey }
func (s stubDMTransport) Relays() []string                            { return nil }
func (s stubDMTransport) SetRelays([]string) error                    { return nil }
func (s stubDMTransport) Close()                                      {}

type stubHooksFirer struct {
	mu    sync.Mutex
	calls []string
}

func (h *stubHooksFirer) Fire(event string, _ string, _ map[string]any) []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, event)
	return nil
}

func (h *stubHooksFirer) firedEvents() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.calls...)
}

func newTestControlRPCHandler(t *testing.T) (controlRPCHandler, *state.DocsRepository, *state.TranscriptRepository) {
	t.Helper()
	store := newTestStore()
	docsRepo := state.NewDocsRepository(store, "test-author")
	transcriptRepo := state.NewTranscriptRepository(store, "test-author")
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	cfgState := newRuntimeConfigStore(state.ConfigDoc{})
	tools := agent.NewToolRegistry()
	agentJobs := newAgentJobRegistry()
	sessionRouter := agent.NewAgentSessionRouter()
	agentRegistry := agent.NewAgentRuntimeRegistry(stubAgentRuntime{})

	deps := controlRPCDeps{
		dmBus:          stubDMTransport{pubkey: "test-pubkey"},
		chatCancels:    newChatAbortRegistry(),
		docsRepo:       docsRepo,
		transcriptRepo: transcriptRepo,
		configState:    cfgState,
		tools:          tools,
		startedAt:      time.Now(),

		sessionStore:  sessionStore,
		toolRegistry:  tools,
		agentJobs:     agentJobs,
		sessionRouter: sessionRouter,
		agentRegistry: agentRegistry,
		agentRuntime:  stubAgentRuntime{},
	}
	return newControlRPCHandler(deps), docsRepo, transcriptRepo
}

func TestSessionRPCHandlerSessionGetViaInjectedDeps(t *testing.T) {
	h, docsRepo, transcriptRepo := newTestControlRPCHandler(t)
	ctx := context.Background()

	if _, err := docsRepo.PutSession(ctx, "sess-1", state.SessionDoc{
		Version:   1,
		SessionID: "sess-1",
	}); err != nil {
		t.Fatalf("put session: %v", err)
	}
	if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
		Version:   1,
		SessionID: "sess-1",
		EntryID:   "entry-1",
		Role:      "user",
		Text:      "hello",
		Unix:      time.Now().Unix(),
	}); err != nil {
		t.Fatalf("put entry: %v", err)
	}

	params, _ := json.Marshal(map[string]any{"session_id": "sess-1"})
	in := nostruntime.ControlRPCInbound{
		Method:     methods.MethodSessionGet,
		Params:     params,
		FromPubKey: "test-pubkey",
	}

	result, err := h.Handle(ctx, in)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	resp, ok := result.Result.(methods.SessionGetResponse)
	if !ok {
		t.Fatalf("expected SessionGetResponse, got %T", result.Result)
	}
	if resp.Session.SessionID != "sess-1" {
		t.Fatalf("session_id = %q want sess-1", resp.Session.SessionID)
	}
	if len(resp.Transcript) != 1 || resp.Transcript[0].Text != "hello" {
		t.Fatalf("transcript mismatch: %+v", resp.Transcript)
	}
}

func TestSessionRPCHandlerSessionsListViaInjectedDeps(t *testing.T) {
	h, docsRepo, _ := newTestControlRPCHandler(t)
	ctx := context.Background()

	for _, id := range []string{"s-1", "s-2"} {
		if _, err := docsRepo.PutSession(ctx, id, state.SessionDoc{
			Version:   1,
			SessionID: id,
		}); err != nil {
			t.Fatalf("put session %s: %v", id, err)
		}
	}

	params, _ := json.Marshal(map[string]any{"limit": 10})
	in := nostruntime.ControlRPCInbound{
		Method:     methods.MethodSessionsList,
		Params:     params,
		FromPubKey: "test-pubkey",
	}

	result, err := h.Handle(ctx, in)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	m, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result.Result)
	}
	sessions, ok := m["sessions"]
	if !ok {
		t.Fatalf("missing sessions key in result")
	}
	switch v := sessions.(type) {
	case []state.SessionDoc:
		if len(v) < 2 {
			t.Fatalf("expected at least 2 sessions, got %d", len(v))
		}
	case []map[string]any:
		if len(v) < 2 {
			t.Fatalf("expected at least 2 sessions, got %d", len(v))
		}
	default:
		t.Fatalf("sessions is %T, want slice", sessions)
	}
}

func TestAgentRPCHandlerAgentRunViaInjectedDeps(t *testing.T) {
	h, _, _ := newTestControlRPCHandler(t)
	ctx := context.Background()

	params, _ := json.Marshal(map[string]any{"message": "hello agent", "session_id": "test-sess"})
	in := nostruntime.ControlRPCInbound{
		Method:     methods.MethodAgent,
		Params:     params,
		FromPubKey: "test-pubkey",
	}

	result, err := h.Handle(ctx, in)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	m, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result.Result)
	}
	runID, _ := m["run_id"].(string)
	if runID == "" {
		t.Fatalf("missing run_id in agent response")
	}
	status, _ := m["status"].(string)
	if status != "accepted" {
		t.Fatalf("status = %q want accepted", status)
	}
}

func TestAgentRPCHandlerAgentsCreateListViaInjectedDeps(t *testing.T) {
	h, _, _ := newTestControlRPCHandler(t)
	ctx := context.Background()

	createParams, _ := json.Marshal(map[string]any{
		"agent_id": "test-agent",
		"name":     "Test Agent",
	})
	in := nostruntime.ControlRPCInbound{
		Method:     methods.MethodAgentsCreate,
		Params:     createParams,
		FromPubKey: "test-pubkey",
	}
	_, err := h.Handle(ctx, in)
	if err != nil {
		t.Fatalf("Handle agents.create: %v", err)
	}

	listParams, _ := json.Marshal(map[string]any{"limit": 10})
	in2 := nostruntime.ControlRPCInbound{
		Method:     methods.MethodAgentsList,
		Params:     listParams,
		FromPubKey: "test-pubkey",
	}
	result, err := h.Handle(ctx, in2)
	if err != nil {
		t.Fatalf("Handle agents.list: %v", err)
	}
	m, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result.Result)
	}
	agents, _ := m["agents"].([]state.AgentDoc)
	found := false
	for _, ag := range agents {
		if ag.AgentID == "test-agent" {
			found = true
			if ag.Name != "Test Agent" {
				t.Fatalf("agent name = %q want Test Agent", ag.Name)
			}
		}
	}
	if !found {
		t.Fatalf("created agent not found in agents.list result")
	}
}

func TestAgentRPCHandlerAgentIdentityViaInjectedDeps(t *testing.T) {
	h, docsRepo, _ := newTestControlRPCHandler(t)
	ctx := context.Background()

	if _, err := docsRepo.PutAgent(ctx, "main", state.AgentDoc{
		Version: 1,
		AgentID: "main",
		Name:    "Injected Agent",
	}); err != nil {
		t.Fatalf("put agent: %v", err)
	}

	params, _ := json.Marshal(map[string]any{})
	in := nostruntime.ControlRPCInbound{
		Method:     methods.MethodAgentIdentityGet,
		Params:     params,
		FromPubKey: "test-pubkey",
	}

	result, err := h.Handle(ctx, in)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	m, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result.Result)
	}
	displayName, _ := m["display_name"].(string)
	if displayName != "Injected Agent" {
		t.Fatalf("display_name = %q want Injected Agent", displayName)
	}
}

func TestSessionRPCHandlerNilHooksMgrSafe(t *testing.T) {
	h, docsRepo, _ := newTestControlRPCHandler(t)
	ctx := context.Background()

	if _, err := docsRepo.PutSession(ctx, "sess-reset", state.SessionDoc{
		Version:   1,
		SessionID: "sess-reset",
	}); err != nil {
		t.Fatalf("put session: %v", err)
	}

	params, _ := json.Marshal(map[string]any{"session_id": "sess-reset"})
	in := nostruntime.ControlRPCInbound{
		Method:     methods.MethodSessionsReset,
		Params:     params,
		FromPubKey: "test-pubkey",
	}

	result, err := h.Handle(ctx, in)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	m, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result.Result)
	}
	if okVal, _ := m["ok"].(bool); !okVal {
		t.Fatalf("expected ok=true, got %v", m["ok"])
	}
}

func TestSessionRPCHandlerHooksMgrFires(t *testing.T) {
	store := newTestStore()
	docsRepo := state.NewDocsRepository(store, "test-author")
	transcriptRepo := state.NewTranscriptRepository(store, "test-author")
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	cfgState := newRuntimeConfigStore(state.ConfigDoc{})
	tools := agent.NewToolRegistry()
	hooks := &stubHooksFirer{}

	deps := controlRPCDeps{
		dmBus:          stubDMTransport{pubkey: "test-pubkey"},
		chatCancels:    newChatAbortRegistry(),
		docsRepo:       docsRepo,
		transcriptRepo: transcriptRepo,
		configState:    cfgState,
		tools:          tools,
		startedAt:      time.Now(),
		sessionStore:   sessionStore,
		hooksMgr:       hooks,
		toolRegistry:   tools,
		agentJobs:      newAgentJobRegistry(),
		sessionRouter:  agent.NewAgentSessionRouter(),
		agentRegistry:  agent.NewAgentRuntimeRegistry(stubAgentRuntime{}),
		agentRuntime:   stubAgentRuntime{},
	}
	h := newControlRPCHandler(deps)
	ctx := context.Background()

	if _, err := docsRepo.PutSession(ctx, "sess-hooks", state.SessionDoc{
		Version:   1,
		SessionID: "sess-hooks",
	}); err != nil {
		t.Fatalf("put session: %v", err)
	}

	params, _ := json.Marshal(map[string]any{"session_id": "sess-hooks"})
	in := nostruntime.ControlRPCInbound{
		Method:     methods.MethodSessionsReset,
		Params:     params,
		FromPubKey: "test-pubkey",
	}
	if _, err := h.Handle(ctx, in); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	events := hooks.firedEvents()
	if len(events) != 1 || events[0] != "command:reset" {
		t.Fatalf("expected [command:reset], got %v", events)
	}
}

func TestAgentRPCHandlerSessionRouterViaInjectedDeps(t *testing.T) {
	store := newTestStore()
	docsRepo := state.NewDocsRepository(store, "test-author")
	transcriptRepo := state.NewTranscriptRepository(store, "test-author")
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	cfgState := newRuntimeConfigStore(state.ConfigDoc{})
	tools := agent.NewToolRegistry()
	sessionRouter := agent.NewAgentSessionRouter()
	agentRegistry := agent.NewAgentRuntimeRegistry(stubAgentRuntime{})

	customRT := namedStubRuntime{name: "custom"}
	agentRegistry.Set("custom-agent", customRT)
	sessionRouter.Assign("sess-routed", "custom-agent")

	deps := controlRPCDeps{
		dmBus:          stubDMTransport{pubkey: "test-pubkey"},
		chatCancels:    newChatAbortRegistry(),
		docsRepo:       docsRepo,
		transcriptRepo: transcriptRepo,
		configState:    cfgState,
		tools:          tools,
		startedAt:      time.Now(),
		sessionStore:   sessionStore,
		toolRegistry:   tools,
		agentJobs:      newAgentJobRegistry(),
		sessionRouter:  sessionRouter,
		agentRegistry:  agentRegistry,
		agentRuntime:   stubAgentRuntime{},
	}
	h := newControlRPCHandler(deps)
	ctx := context.Background()

	params, _ := json.Marshal(map[string]any{})
	in := nostruntime.ControlRPCInbound{
		Method:     methods.MethodAgentIdentityGet,
		Params:     params,
		FromPubKey: "sess-routed",
	}

	result, err := h.Handle(ctx, in)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	m, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result.Result)
	}
	agentID, _ := m["agent_id"].(string)
	if agentID != "custom-agent" {
		t.Fatalf("agent_id = %q want custom-agent (routed via injected sessionRouter)", agentID)
	}
}
