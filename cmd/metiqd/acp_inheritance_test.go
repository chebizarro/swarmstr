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
	result   agent.ProviderResult
}

func (p *capturingProvider) Generate(_ context.Context, turn agent.Turn) (agent.ProviderResult, error) {
	p.lastTurn = turn
	if p.result.Text == "" && p.result.Usage.InputTokens == 0 && p.result.Usage.OutputTokens == 0 && len(p.result.HistoryDelta) == 0 && p.result.Outcome == "" && p.result.StopReason == "" {
		return agent.ProviderResult{Text: "ok"}, nil
	}
	return p.result, nil
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
	if err := handleACPMessage(context.Background(), msg, "peer-pubkey", dm, agent.NewAgentRuntimeRegistry(nil), agent.NewAgentSessionRouter(), nil, nil, nil); err != nil {
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
	provider := &capturingProvider{result: agent.ProviderResult{
		Text:  "ok",
		Usage: agent.ProviderUsage{InputTokens: 3, OutputTokens: 2},
	}}
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
	transcriptRepo := state.NewTranscriptRepository(newTestStore(), "author")
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
	var replied string
	dm := nostruntime.InboundDM{Reply: func(_ context.Context, text string) error {
		replied = text
		return nil
	}}
	if err := handleACPMessage(context.Background(), msg, "peer-pubkey", dm, agentReg, sessionRouter, tools, nil, transcriptRepo); err != nil {
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
	if entry.LastTurn == nil {
		t.Fatal("expected persisted ACP worker last_turn telemetry")
	}
	if entry.LastTurn.Outcome != string(agent.TurnOutcomeCompleted) || entry.LastTurn.StopReason != string(agent.TurnStopReasonModelText) {
		t.Fatalf("last_turn = %+v", entry.LastTurn)
	}

	parsed, err := acppkg.Parse([]byte(replied))
	if err != nil {
		t.Fatalf("parse ACP result: %v", err)
	}
	resultPayload, err := acppkg.DecodeResultPayload(parsed.Payload)
	if err != nil {
		t.Fatalf("decode ACP result payload: %v", err)
	}
	if resultPayload.Worker == nil {
		t.Fatal("expected worker metadata in ACP result")
	}
	if resultPayload.Worker.SessionID != "acp:peer-pubkey" || resultPayload.Worker.AgentID != "worker" {
		t.Fatalf("worker metadata = %+v", resultPayload.Worker)
	}
	if resultPayload.Worker.TurnResult == nil {
		t.Fatal("expected worker turn_result metadata")
	}
	if resultPayload.Worker.TurnResult.Outcome != agent.TurnOutcomeCompleted || resultPayload.Worker.TurnResult.StopReason != agent.TurnStopReasonModelText {
		t.Fatalf("worker turn_result = %+v", resultPayload.Worker.TurnResult)
	}
	if resultPayload.Worker.TurnResult.Usage.InputTokens != 3 || resultPayload.Worker.TurnResult.Usage.OutputTokens != 2 {
		t.Fatalf("worker usage = %+v", resultPayload.Worker.TurnResult.Usage)
	}
	if got := len(resultPayload.Worker.HistoryEntryIDs); got != 2 {
		t.Fatalf("history entry ids len = %d, want 2 (%v)", got, resultPayload.Worker.HistoryEntryIDs)
	}

	entries, err := transcriptRepo.ListSession(context.Background(), "acp:peer-pubkey", 10)
	if err != nil {
		t.Fatalf("list transcript: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 transcript entries, got %d", len(entries))
	}
	if got := entries[0].Meta["acp_task_id"]; got != "task-1" {
		t.Fatalf("seed entry acp_task_id = %#v", got)
	}
	if got := entries[0].Meta["message_kind"]; got != "context_seed" {
		t.Fatalf("seed entry message_kind = %#v", got)
	}
	turnResult, ok := entries[1].Meta["turn_result"].(map[string]any)
	if !ok {
		t.Fatalf("terminal ACP transcript entry missing turn_result: %#v", entries[1].Meta)
	}
	if got := turnResult["outcome"]; got != string(agent.TurnOutcomeCompleted) {
		t.Fatalf("terminal outcome = %#v", got)
	}
}
