package subagent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"metiq/internal/agent"
	pluginhooks "metiq/internal/plugins/hooks"
	pluginregistry "metiq/internal/plugins/registry"
	"metiq/internal/skills"
)

type blockingRuntime struct {
	started chan agent.Turn
}

func (r *blockingRuntime) ProcessTurn(ctx context.Context, turn agent.Turn) (agent.TurnResult, error) {
	r.started <- turn
	<-ctx.Done()
	return agent.TurnResult{}, context.Cause(ctx)
}

type usageRuntime struct{}

type streamingBudgetRuntime struct {
	seen   chan agent.Turn
	chunks []string
}

func (r *streamingBudgetRuntime) ProcessTurn(ctx context.Context, turn agent.Turn) (agent.TurnResult, error) {
	return r.ProcessTurnStreaming(ctx, turn, nil)
}

func (r *streamingBudgetRuntime) ProcessTurnStreaming(ctx context.Context, turn agent.Turn, onChunk func(string)) (agent.TurnResult, error) {
	r.seen <- turn
	for _, chunk := range r.chunks {
		if onChunk != nil {
			onChunk(chunk)
		}
		if turn.RuntimeEventSink != nil {
			turn.RuntimeEventSink(agent.RuntimeEvent{Type: agent.RuntimeEventAssistantDelta, Delta: chunk})
		}
		select {
		case <-ctx.Done():
			return agent.TurnResult{}, context.Cause(ctx)
		default:
		}
	}
	return agent.TurnResult{Text: "completed"}, nil
}

func (usageRuntime) ProcessTurn(ctx context.Context, turn agent.Turn) (agent.TurnResult, error) {
	return usageRuntime{}.ProcessTurnStreaming(ctx, turn, nil)
}

func (usageRuntime) ProcessTurnStreaming(ctx context.Context, turn agent.Turn, _ func(string)) (agent.TurnResult, error) {
	turn.RuntimeEventSink(agent.RuntimeEvent{Type: agent.RuntimeEventUsage, Usage: agent.TurnUsage{InputTokens: 4, OutputTokens: 3}})
	<-ctx.Done()
	return agent.TurnResult{}, context.Cause(ctx)
}

func TestResolveSkillAgentDefinitions(t *testing.T) {
	runtime, err := agent.NewProviderRuntime(agent.EchoProvider{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	o := NewOrchestrator(nil, DefaultConfig())
	if err := o.RegisterDefinition(AgentDefinition{ID: "researcher", Runtime: runtime, SkillKeys: []string{" Web-Research "}}); err != nil {
		t.Fatal(err)
	}
	if err := o.RegisterDefinition(AgentDefinition{ID: "reviewer", Runtime: runtime, Metadata: map[string]any{"skills": []any{"web-research"}}}); err != nil {
		t.Fatal(err)
	}
	catalog := &skills.SkillCatalog{Skills: []*skills.ResolvedSkill{{Skill: &skills.Skill{SkillKey: "web-research"}, Eligible: true}}}
	defs := o.ResolveSkillAgentDefinitions(catalog, "WEB-RESEARCH")
	if len(defs) != 2 || defs[0].ID != "researcher" || defs[1].ID != "reviewer" {
		t.Fatalf("definitions = %#v", defs)
	}
	catalog.Skills[0].Eligible = false
	if got := o.ResolveSkillAgentDefinitions(catalog, "web-research"); len(got) != 0 {
		t.Fatalf("ineligible definitions = %#v", got)
	}
}

func TestOrchestratorStreamsResultAndLifecycleHooks(t *testing.T) {
	providerRuntime, err := agent.NewProviderRuntime(agent.EchoProvider{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	orchestrator := NewOrchestrator(registry, DefaultConfig())
	if err := orchestrator.RegisterDefinition(AgentDefinition{ID: "researcher", Runtime: providerRuntime}); err != nil {
		t.Fatal(err)
	}

	invoker := pluginhooks.NewHookInvoker(nil, nil)
	var mu sync.Mutex
	var hookOrder []pluginregistry.HookEvent
	var spawned pluginhooks.SubagentSpawnedEvent
	var ended pluginhooks.SubagentEndedEvent
	register := func(event pluginregistry.HookEvent, id string, handler pluginhooks.NativeHandler) {
		invoker.RegisterNative(event, id, 1, func(ctx context.Context, payload any) (any, error) {
			mu.Lock()
			hookOrder = append(hookOrder, event)
			mu.Unlock()
			return handler(ctx, payload)
		})
	}
	register(pluginregistry.HookSubagentSpawning, "spawning", func(context.Context, any) (any, error) { return nil, nil })
	register(pluginregistry.HookSubagentSpawned, "spawned", func(_ context.Context, payload any) (any, error) {
		spawned = payload.(pluginhooks.SubagentSpawnedEvent)
		return nil, nil
	})
	register(pluginregistry.HookSubagentEnded, "ended", func(_ context.Context, payload any) (any, error) {
		ended = payload.(pluginhooks.SubagentEndedEvent)
		return nil, nil
	})

	handle, err := orchestrator.Spawn(context.Background(), SpawnRequest{AgentID: "researcher", ParentAgentID: "parent", ParentSessionID: "session-a", Task: "inspect", HookInvoker: invoker})
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for event := range handle.Events {
		events = append(events, event)
	}
	result, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Turn.Text != "ack: inspect" || result.RunID != handle.RunID {
		t.Fatalf("result=%#v", result)
	}
	if len(events) < 3 || events[0].Type != EventStarted || events[len(events)-1].Type != EventCompleted {
		t.Fatalf("events=%#v", events)
	}
	var sawStreamStart, sawStreamEnd bool
	for _, event := range events {
		if event.RuntimeEvent == nil {
			continue
		}
		sawStreamStart = sawStreamStart || event.RuntimeEvent.Type == agent.RuntimeEventStreamStart
		sawStreamEnd = sawStreamEnd || event.RuntimeEvent.Type == agent.RuntimeEventStreamEnd
	}
	if !sawStreamStart || !sawStreamEnd {
		t.Fatalf("missing runtime lifecycle: %#v", events)
	}
	mu.Lock()
	gotOrder := append([]pluginregistry.HookEvent(nil), hookOrder...)
	mu.Unlock()
	wantOrder := []pluginregistry.HookEvent{pluginregistry.HookSubagentSpawning, pluginregistry.HookSubagentSpawned, pluginregistry.HookSubagentEnded}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("hook order=%#v", gotOrder)
	}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("hook order[%d]=%q want %q", i, gotOrder[i], wantOrder[i])
		}
	}
	if spawned.RunID != handle.RunID || spawned.ParentAgentID != "parent" || ended.Outcome != "ok" || ended.SessionID != result.SessionID {
		t.Fatalf("spawned=%#v ended=%#v", spawned, ended)
	}
	record := registry.Get(handle.RunID)
	if record == nil || record.EndedAt == 0 || record.Outcome == nil || record.Outcome.Status != "ok" || record.Depth != 1 {
		t.Fatalf("record=%#v", record)
	}
}

func TestOrchestratorEnforcesConcurrencyAndCancellation(t *testing.T) {
	runtime := &blockingRuntime{started: make(chan agent.Turn, 1)}
	orchestrator := NewOrchestrator(nil, Config{MaxDepth: 2, MaxConcurrent: 1, MaxChildrenPerParent: 1, EventBuffer: 8})
	if err := orchestrator.RegisterDefinition(AgentDefinition{ID: "worker", Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	handle, err := orchestrator.Spawn(context.Background(), SpawnRequest{AgentID: "worker", ParentSessionID: "s", Task: "first"})
	if err != nil {
		t.Fatal(err)
	}
	<-runtime.started
	if _, err := orchestrator.Spawn(context.Background(), SpawnRequest{AgentID: "worker", ParentSessionID: "s", Task: "second"}); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("second spawn err=%v", err)
	}
	handle.Cancel()
	if _, err := handle.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
}

func TestOrchestratorEnforcesDepthBeforeSpawn(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(SubagentRunRecord{RunID: "parent", AgentID: "parent-agent", Depth: 2, ChildSessionKey: "parent-session"}); err != nil {
		t.Fatal(err)
	}
	orchestrator := NewOrchestrator(registry, Config{MaxDepth: 2, MaxConcurrent: 2, MaxChildrenPerParent: 1, EventBuffer: 8})
	if err := orchestrator.RegisterDefinition(AgentDefinition{ID: "worker", Runtime: &blockingRuntime{started: make(chan agent.Turn, 1)}}); err != nil {
		t.Fatal(err)
	}
	_, err := orchestrator.Spawn(context.Background(), SpawnRequest{AgentID: "worker", ParentRunID: "parent", Task: "too deep"})
	if !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("err=%v", err)
	}
}

func TestOrchestratorCancelsWhenTokenBudgetExceeded(t *testing.T) {
	orchestrator := NewOrchestrator(nil, Config{MaxDepth: 2, MaxConcurrent: 2, MaxChildrenPerParent: 1, EventBuffer: 8})
	if err := orchestrator.RegisterDefinition(AgentDefinition{ID: "worker", Runtime: usageRuntime{}}); err != nil {
		t.Fatal(err)
	}
	handle, err := orchestrator.Spawn(context.Background(), SpawnRequest{AgentID: "worker", ParentSessionID: "s", Task: "bounded", Budget: Budget{MaxTotalTokens: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Wait(context.Background()); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("budget err=%v", err)
	}
	record := orchestrator.registry.Get(handle.RunID)
	if record == nil || record.Outcome == nil || record.Outcome.Error != ErrBudgetExceeded.Error() {
		t.Fatalf("record=%#v", record)
	}
}

func TestOrchestratorPreflightsInputBudgetBeforeGeneration(t *testing.T) {
	runtime := &streamingBudgetRuntime{seen: make(chan agent.Turn, 1), chunks: []string{"unused"}}
	orchestrator := NewOrchestrator(nil, DefaultConfig())
	if err := orchestrator.RegisterDefinition(AgentDefinition{ID: "worker", Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	handle, err := orchestrator.Spawn(context.Background(), SpawnRequest{
		AgentID: "worker", ParentSessionID: "s", Task: "a request that cannot fit", Budget: Budget{MaxInputTokens: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Wait(context.Background()); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("preflight err=%v", err)
	}
	select {
	case turn := <-runtime.seen:
		t.Fatalf("runtime started despite failed preflight: %#v", turn)
	default:
	}
}

func TestOrchestratorPropagatesOutputHintAndCancelsMidStream(t *testing.T) {
	runtime := &streamingBudgetRuntime{seen: make(chan agent.Turn, 1), chunks: []string{"123456789012", "should-not-run"}}
	orchestrator := NewOrchestrator(nil, DefaultConfig())
	if err := orchestrator.RegisterDefinition(AgentDefinition{ID: "worker", Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	handle, err := orchestrator.Spawn(context.Background(), SpawnRequest{
		AgentID: "worker", ParentSessionID: "s", Task: "bounded", Budget: Budget{MaxOutputTokens: 2, MaxTotalTokens: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	turn := <-runtime.seen
	if turn.MaxOutputTokens != 2 {
		t.Fatalf("provider output hint=%d want=2", turn.MaxOutputTokens)
	}
	if _, err := handle.Wait(context.Background()); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("mid-stream budget err=%v", err)
	}
}

func TestOrchestratorSpawnHookCanReject(t *testing.T) {
	runtime := &blockingRuntime{started: make(chan agent.Turn, 1)}
	orchestrator := NewOrchestrator(nil, DefaultConfig())
	if err := orchestrator.RegisterDefinition(AgentDefinition{ID: "worker", Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	invoker := pluginhooks.NewHookInvoker(nil, nil)
	invoker.RegisterNative(pluginregistry.HookSubagentSpawning, "reject", 1, func(context.Context, any) (any, error) {
		return map[string]any{"reject": true, "reason": "not allowed"}, nil
	})
	_, err := orchestrator.Spawn(context.Background(), SpawnRequest{AgentID: "worker", ParentSessionID: "s", Task: "blocked", HookInvoker: invoker})
	if err == nil || orchestrator.registry.Len() != 0 {
		t.Fatalf("err=%v registry=%d", err, orchestrator.registry.Len())
	}
	select {
	case turn := <-runtime.started:
		t.Fatalf("runtime started unexpectedly: %#v", turn)
	default:
	}
}
