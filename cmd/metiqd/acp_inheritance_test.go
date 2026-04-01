package main

import (
	"context"
	"path/filepath"
	"testing"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent"
	"metiq/internal/agent/toolbuiltin"
	ctxengine "metiq/internal/context"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

type capturingProvider struct {
	lastTurn agent.Turn
}

func (p *capturingProvider) Generate(_ context.Context, turn agent.Turn) (agent.ProviderResult, error) {
	p.lastTurn = turn
	return agent.ProviderResult{Text: "ok"}, nil
}

type stubContextEngine struct {
	assembled ctxengine.AssembleResult
}

type filterableRuntime struct {
	allowed map[string]bool
}

func (r *filterableRuntime) ProcessTurn(context.Context, agent.Turn) (agent.TurnResult, error) {
	return agent.TurnResult{Text: "ok"}, nil
}

func (r *filterableRuntime) Filtered(allowed map[string]bool) agent.Runtime {
	return &filterableRuntime{allowed: allowed}
}

func (s *stubContextEngine) Ingest(context.Context, string, ctxengine.Message) (ctxengine.IngestResult, error) {
	return ctxengine.IngestResult{}, nil
}

func (s *stubContextEngine) Assemble(context.Context, string, int) (ctxengine.AssembleResult, error) {
	return s.assembled, nil
}

func (s *stubContextEngine) Compact(context.Context, string) (ctxengine.CompactResult, error) {
	return ctxengine.CompactResult{}, nil
}

func (s *stubContextEngine) Bootstrap(context.Context, string, []ctxengine.Message) (ctxengine.BootstrapResult, error) {
	return ctxengine.BootstrapResult{}, nil
}

func (s *stubContextEngine) Close() error { return nil }

func TestBuildInheritedACPTaskPayloadUsesRuntimeHintsAndContext(t *testing.T) {
	prevEngine := controlContextEngine
	controlContextEngine = &stubContextEngine{assembled: ctxengine.AssembleResult{
		Messages: []ctxengine.Message{{Role: "assistant", Content: "prior context"}},
	}}
	defer func() { controlContextEngine = prevEngine }()

	ctx := context.Background()
	ctx = agent.ContextWithSessionID(ctx, "session-acp")
	ctx = agent.ContextWithMemoryScope(ctx, agent.MemoryScopeContext{
		Scope:     string(state.AgentMemoryScopeProject),
		AgentID:   "worker",
		SessionID: "session-acp",
	})
	cfg := state.ConfigDoc{Agents: []state.AgentConfig{{
		ID:           "worker",
		ToolProfile:  "coding",
		EnabledTools: []string{"memory_search", "memory_store", "memory_search"},
	}}}

	ctx = contextWithACPTaskPayload(ctx, acppkg.TaskPayload{
		ContextMessages: encodeACPConversationMessages([]agent.ConversationMessage{{Role: "user", Content: "inherited parent history"}}),
	})
	payload := buildInheritedACPTaskPayload(ctx, cfg, nil, nil, acppkg.TaskPayload{Instructions: "delegate this"})
	if payload.MemoryScope != state.AgentMemoryScopeProject {
		t.Fatalf("memory scope = %q, want %q", payload.MemoryScope, state.AgentMemoryScopeProject)
	}
	if payload.ToolProfile != "coding" {
		t.Fatalf("tool profile = %q, want coding", payload.ToolProfile)
	}
	if got, want := len(payload.EnabledTools), 2; got != want {
		t.Fatalf("enabled tools len = %d, want %d (%v)", got, want, payload.EnabledTools)
	}
	if payload.EnabledTools[0] != "memory_search" || payload.EnabledTools[1] != "memory_store" {
		t.Fatalf("enabled tools = %v, want ordered deduped config list", payload.EnabledTools)
	}
	if payload.ParentContext == nil {
		t.Fatal("parent context missing")
	}
	if payload.ParentContext.SessionID != "session-acp" || payload.ParentContext.AgentID != "worker" {
		t.Fatalf("parent context = %+v, want session-acp/worker", payload.ParentContext)
	}
	if got := decodeACPConversationMessages(payload.ContextMessages); len(got) != 1 || got[0].Role != "user" || got[0].Content != "inherited parent history" {
		t.Fatalf("context messages decoded = %+v", got)
	}
}

func TestHandleACPMessageReturnsACPErrorForMalformedTask(t *testing.T) {
	var replied string
	dm := nostruntime.InboundDM{Reply: func(_ context.Context, text string) error {
		replied = text
		return nil
	}}
	msg := acppkg.Message{
		ACPType: "task",
		TaskID:  "task-bad",
		Payload: map[string]any{"instructions": []any{"bad"}},
	}
	if err := handleACPMessage(context.Background(), msg, "peer-pubkey", dm, agent.NewAgentRuntimeRegistry(nil), agent.NewAgentSessionRouter(), nil, nil); err != nil {
		t.Fatalf("handleACPMessage malformed task: %v", err)
	}
	parsed, err := acppkg.Parse([]byte(replied))
	if err != nil {
		t.Fatalf("parse ACP result: %v", err)
	}
	if parsed.ACPType != "result" {
		t.Fatalf("acp_type = %q, want result", parsed.ACPType)
	}
	if got, _ := parsed.Payload["error"].(string); got == "" {
		t.Fatalf("expected result error payload, got %#v", parsed.Payload)
	}
}

func TestApplyACPTaskRuntimeConstraintsUsesRuntimeFilteredCapability(t *testing.T) {
	prevToolRegistry := controlToolRegistry
	controlToolRegistry = agent.NewToolRegistry()
	defer func() { controlToolRegistry = prevToolRegistry }()
	cfg := state.ConfigDoc{Agents: []state.AgentConfig{{
		ID:           "worker",
		EnabledTools: []string{"memory_search"},
	}}}
	rt := applyACPTaskRuntimeConstraints(context.Background(), &filterableRuntime{}, "worker", acppkg.TaskPayload{}, cfg, nil)
	filtered, ok := rt.(*filterableRuntime)
	if !ok {
		t.Fatalf("runtime type = %T, want *filterableRuntime", rt)
	}
	if filtered.allowed == nil || !filtered.allowed["memory_search"] {
		t.Fatalf("allowed tools = %v, want memory_search allowlist", filtered.allowed)
	}
}

func TestHandleACPMessageAppliesInheritedRuntimeHints(t *testing.T) {
	provider := &capturingProvider{}
	tools := agent.NewToolRegistry()
	tools.RegisterWithDef("memory_search", func(context.Context, map[string]any) (string, error) { return "", nil }, toolbuiltin.MemorySearchDef)
	tools.RegisterWithDef("memory_store", func(context.Context, map[string]any) (string, error) { return "", nil }, toolbuiltin.MemoryStoreDef)
	runtime, err := agent.NewProviderRuntime(provider, tools)
	if err != nil {
		t.Fatalf("new provider runtime: %v", err)
	}
	agentReg := agent.NewAgentRuntimeRegistry(runtime)
	sessionRouter := agent.NewAgentSessionRouter()
	sessionRouter.Assign("peer-pubkey", "worker")

	ss, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	prevSessionStore := controlSessionStore
	prevToolRegistry := controlToolRegistry
	prevRuntimeConfig := controlRuntimeConfig
	controlSessionStore = ss
	controlToolRegistry = tools
	controlRuntimeConfig = newRuntimeConfigStore(state.ConfigDoc{Agents: []state.AgentConfig{{ID: "worker"}}})
	defer func() {
		controlSessionStore = prevSessionStore
		controlToolRegistry = prevToolRegistry
		controlRuntimeConfig = prevRuntimeConfig
	}()

	msg := acppkg.NewTask("task-1", "sender", acppkg.TaskPayload{
		Instructions: "handle this",
		EnabledTools: []string{"memory_search"},
		MemoryScope:  state.AgentMemoryScopeProject,
		ContextMessages: encodeACPConversationMessages([]agent.ConversationMessage{{
			Role:    "assistant",
			Content: "existing parent transcript",
		}}),
	})
	dm := nostruntime.InboundDM{Reply: func(context.Context, string) error { return nil }}
	if err := handleACPMessage(context.Background(), msg, "peer-pubkey", dm, agentReg, sessionRouter, tools, nil); err != nil {
		t.Fatalf("handleACPMessage: %v", err)
	}

	if got, want := len(provider.lastTurn.Tools), 1; got != want {
		t.Fatalf("provider turn tools len = %d, want %d (%+v)", got, want, provider.lastTurn.Tools)
	}
	if provider.lastTurn.Tools[0].Name != "memory_search" {
		t.Fatalf("provider turn tools = %+v, want only memory_search", provider.lastTurn.Tools)
	}
	if got, want := len(provider.lastTurn.History), 1; got != want {
		t.Fatalf("turn history len = %d, want %d", got, want)
	}
	if provider.lastTurn.History[0].Content != "existing parent transcript" {
		t.Fatalf("turn history = %+v", provider.lastTurn.History)
	}
	entry, ok := ss.Get("acp:peer-pubkey")
	if !ok {
		t.Fatal("expected ACP worker session entry")
	}
	if entry.MemoryScope != state.AgentMemoryScopeProject {
		t.Fatalf("persisted memory scope = %q, want %q", entry.MemoryScope, state.AgentMemoryScopeProject)
	}
	if entry.AgentID != "worker" {
		t.Fatalf("persisted agent id = %q, want worker", entry.AgentID)
	}
}
