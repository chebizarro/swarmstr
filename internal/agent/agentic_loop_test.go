package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"metiq/internal/agent/toolloop"
)

// mockChatProvider is a ChatProvider that returns preconfigured responses.
type mockChatProvider struct {
	responses []*LLMResponse
	callCount int
}

func (m *mockChatProvider) Chat(_ context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	if m.callCount >= len(m.responses) {
		return &LLMResponse{Content: "final response"}, nil
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return resp, nil
}

type capturingChatProvider struct {
	responses []*LLMResponse
	calls     [][]LLMMessage
	callCount int
}

func (p *capturingChatProvider) Chat(_ context.Context, messages []LLMMessage, _ []ToolDefinition, _ ChatOptions) (*LLMResponse, error) {
	captured := make([]LLMMessage, len(messages))
	copy(captured, messages)
	p.calls = append(p.calls, captured)
	if p.callCount >= len(p.responses) {
		return &LLMResponse{Content: "final response"}, nil
	}
	resp := p.responses[p.callCount]
	p.callCount++
	return resp, nil
}

type promptCacheCapturingProvider struct {
	profile PromptCacheProfile
	calls   [][]LLMMessage
	opts    []ChatOptions
}

func (p *promptCacheCapturingProvider) PromptCacheProfile() PromptCacheProfile {
	return p.profile
}

func (p *promptCacheCapturingProvider) Chat(_ context.Context, messages []LLMMessage, _ []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	captured := make([]LLMMessage, len(messages))
	copy(captured, messages)
	p.calls = append(p.calls, captured)
	p.opts = append(p.opts, opts)
	return &LLMResponse{Content: "ok"}, nil
}

func TestGenerateWithAgenticLoop_UsesPromptCacheProfileForPromptAssembly(t *testing.T) {
	provider := &promptCacheCapturingProvider{profile: PromptCacheProfile{
		Enabled:                 true,
		Backend:                 PromptCacheBackendVLLM,
		DynamicContextPlacement: DynamicContextPlacementLateUser,
	}}

	result, err := generateWithAgenticLoop(context.Background(), provider, Turn{
		UserText:           "current user",
		StaticSystemPrompt: "turn static",
		Context:            "dynamic runtime context",
		History: []ConversationMessage{
			{Role: "user", Content: "previous user"},
			{Role: "assistant", Content: "previous assistant"},
		},
	}, "provider static", "test-prefix-cache")
	if err != nil {
		t.Fatalf("generateWithAgenticLoop returned error: %v", err)
	}
	if result.Text != "ok" {
		t.Fatalf("unexpected result text: %q", result.Text)
	}
	if len(provider.calls) != 1 {
		t.Fatalf("expected one provider call, got %d", len(provider.calls))
	}
	msgs := provider.calls[0]
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" || msgs[0].Lane != PromptLaneSystemStatic || strings.Contains(msgs[0].Content, "dynamic runtime context") {
		t.Fatalf("expected stable static system prefix, got %+v", msgs[0])
	}
	if msgs[3].Role != "user" || msgs[3].Lane != PromptLaneDynamicContext || !strings.Contains(msgs[3].Content, "dynamic runtime context") {
		t.Fatalf("expected late dynamic-context user message, got %+v", msgs[3])
	}
	if msgs[4].Role != "user" || msgs[4].Lane != PromptLaneCurrentUser || msgs[4].Content != "current user" {
		t.Fatalf("expected real current user last, got %+v", msgs[4])
	}
	if len(provider.opts) != 1 || provider.opts[0].CacheSystem || provider.opts[0].CacheTools {
		t.Fatalf("expected non-Anthropic prefix profile to disable Anthropic cache flags, got %+v", provider.opts)
	}
}

// mockToolExecutor counts executions and returns a fixed result.
type mockToolExecutor struct {
	execCount    atomic.Int32
	results      map[string]string
	traits       map[string]ToolTraits
	delays       map[string]time.Duration
	inFlight     atomic.Int32
	maxInFlight  atomic.Int32
	executeOrder []string
	mu           sync.Mutex
}

func (m *mockToolExecutor) Execute(_ context.Context, call ToolCall) (string, error) {
	m.execCount.Add(1)
	current := m.inFlight.Add(1)
	for {
		maxCurrent := m.maxInFlight.Load()
		if current <= maxCurrent || m.maxInFlight.CompareAndSwap(maxCurrent, current) {
			break
		}
	}
	defer m.inFlight.Add(-1)
	if delay, ok := m.delays[call.Name]; ok && delay > 0 {
		time.Sleep(delay)
	}
	m.mu.Lock()
	m.executeOrder = append(m.executeOrder, call.Name)
	m.mu.Unlock()
	if r, ok := m.results[call.Name]; ok {
		return r, nil
	}
	return "ok", nil
}

func (m *mockToolExecutor) Definitions() []ToolDefinition { return nil }

func (m *mockToolExecutor) EffectiveTraits(call ToolCall) (ToolTraits, bool) {
	if m.traits == nil {
		return ToolTraits{}, false
	}
	traits, ok := m.traits[call.Name]
	return traits, ok
}

type capturedToolLifecycle struct {
	mu     sync.Mutex
	events []ToolLifecycleEvent
}

func (c *capturedToolLifecycle) sink(evt ToolLifecycleEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, evt)
}

func (c *capturedToolLifecycle) snapshot() []ToolLifecycleEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ToolLifecycleEvent, len(c.events))
	copy(out, c.events)
	return out
}

type toolExecutorFunc func(context.Context, ToolCall) (string, error)

func (f toolExecutorFunc) Execute(ctx context.Context, call ToolCall) (string, error) {
	return f(ctx, call)
}

func TestRunAgenticLoop_PrunesContextBeforeInitialCall(t *testing.T) {
	provider := &capturingChatProvider{
		responses: []*LLMResponse{{Content: "direct answer", NeedsToolResults: false}},
	}
	large := strings.Repeat("x", 30_000)
	messages := []LLMMessage{
		{Role: "user", Content: "read several files"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "tc1", Content: large},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "grep"}}},
		{Role: "tool", ToolCallID: "tc2", Content: large},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc3", Name: "file_search"}}},
		{Role: "tool", ToolCallID: "tc3", Content: large},
		{Role: "assistant", Content: "recent response 1"},
		{Role: "assistant", Content: "recent response 2"},
		{Role: "assistant", Content: "recent response 3"},
	}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:            provider,
		InitialMessages:     messages,
		Executor:            &mockToolExecutor{},
		MaxIterations:       1,
		LogPrefix:           "test",
		ContextWindowTokens: 1_000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "direct answer" {
		t.Fatalf("got %q, want direct answer", resp.Content)
	}
	if len(provider.calls) != 1 {
		t.Fatalf("expected one provider call, got %d", len(provider.calls))
	}
	if provider.calls[0][2].Content != DefaultContextPruningConfig().HardClear.Placeholder {
		t.Fatalf("expected provider to receive hard-cleared tool result, got %.80q", provider.calls[0][2].Content)
	}
	if messages[2].Content != large {
		t.Fatal("initial messages should not be mutated by pruning")
	}
}

func TestRunAgenticLoop_PrunesIterativeContextBeforeCompressionWithDefaults(t *testing.T) {
	provider := &capturingChatProvider{
		responses: []*LLMResponse{
			{NeedsToolResults: true, ToolCalls: []ToolCall{{ID: "tc1", Name: "read_file"}}},
			{NeedsToolResults: true, ToolCalls: []ToolCall{{ID: "tc2", Name: "read_file"}}},
			{Content: "done", NeedsToolResults: false},
		},
	}
	executor := &mockToolExecutor{results: map[string]string{"read_file": strings.Repeat("x", 60_000)}}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:            provider,
		InitialMessages:     []LLMMessage{{Role: "user", Content: "read files"}},
		Executor:            executor,
		MaxIterations:       3,
		LogPrefix:           "test",
		ContextWindowTokens: 1_000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "done" {
		t.Fatalf("got %q, want done", resp.Content)
	}
	if len(provider.calls) != 3 {
		t.Fatalf("expected three provider calls, got %d", len(provider.calls))
	}

	finalMessages := provider.calls[2]
	var tc1Content, tc2Content string
	for _, msg := range finalMessages {
		if msg.ToolCallID == "tc1" {
			tc1Content = msg.Content
		}
		if msg.ToolCallID == "tc2" {
			tc2Content = msg.Content
		}
	}
	if tc1Content != DefaultContextPruningConfig().HardClear.Placeholder {
		t.Fatalf("expected older iterative tool result hard-cleared, got %.80q", tc1Content)
	}
	if tc2Content == DefaultContextPruningConfig().HardClear.Placeholder {
		t.Fatal("latest iterative tool result should remain protected from hard clear")
	}
}

func TestRunAgenticLoop_NoToolCalls(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			{Content: "direct answer", NeedsToolResults: false},
		},
	}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "hello"}},
		Executor:        &mockToolExecutor{},
		MaxIterations:   10,
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "direct answer" {
		t.Errorf("got %q, want %q", resp.Content, "direct answer")
	}
	if resp.Outcome != TurnOutcomeCompleted || resp.StopReason != TurnStopReasonModelText {
		t.Fatalf("unexpected classification: outcome=%q stop_reason=%q", resp.Outcome, resp.StopReason)
	}
	if provider.callCount != 1 {
		t.Errorf("expected 1 LLM call, got %d", provider.callCount)
	}
}

func TestRunAgenticLoop_SingleToolCall(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			// First call: model requests a tool
			{
				Content:          "",
				ToolCalls:        []ToolCall{{ID: "tc1", Name: "test_tool", Args: map[string]any{"q": "hi"}}},
				NeedsToolResults: true,
			},
			// Second call: model produces text
			{Content: "tool result processed", NeedsToolResults: false},
		},
	}

	executor := &mockToolExecutor{results: map[string]string{"test_tool": "tool output"}}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "use a tool"}},
		Executor:        executor,
		MaxIterations:   10,
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "tool result processed" {
		t.Errorf("got %q, want %q", resp.Content, "tool result processed")
	}
	if resp.Outcome != TurnOutcomeCompletedWithTools || resp.StopReason != TurnStopReasonModelText {
		t.Fatalf("unexpected classification: outcome=%q stop_reason=%q", resp.Outcome, resp.StopReason)
	}
	if provider.callCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", provider.callCount)
	}
	if executor.execCount.Load() != 1 {
		t.Errorf("expected 1 tool execution, got %d", executor.execCount.Load())
	}
}

func TestRunAgenticLoop_EmitsToolLifecycleEvents(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			{
				ToolCalls:        []ToolCall{{ID: "tc1", Name: "test_tool"}},
				NeedsToolResults: true,
			},
			{Content: "done", NeedsToolResults: false},
		},
	}
	executor := &mockToolExecutor{results: map[string]string{"test_tool": "tool output"}}
	capture := &capturedToolLifecycle{}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "use tool"}},
		Executor:        executor,
		MaxIterations:   10,
		LogPrefix:       "test",
		SessionID:       "sess-1",
		TurnID:          "turn-1",
		ToolEventSink:   capture.sink,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "done" {
		t.Fatalf("unexpected response content: %q", resp.Content)
	}
	events := capture.snapshot()
	if len(events) != 3 {
		t.Fatalf("expected 3 lifecycle events, got %d", len(events))
	}
	if events[0].Type != ToolLifecycleEventProgress || events[1].Type != ToolLifecycleEventStart || events[2].Type != ToolLifecycleEventResult {
		t.Fatalf("unexpected lifecycle order: %+v", events)
	}
	scheduler, ok := events[0].Data.(ToolSchedulerDecision)
	if !ok {
		t.Fatalf("expected ToolSchedulerDecision, got %T", events[0].Data)
	}
	if scheduler.Kind != ToolDecisionKindScheduler || scheduler.Mode != "serial" || scheduler.BatchSize != 1 || scheduler.BatchPosition != 0 {
		t.Fatalf("unexpected scheduler decision: %+v", scheduler)
	}
	if events[1].SessionID != "sess-1" || events[1].TurnID != "turn-1" {
		t.Fatalf("missing correlation fields on start event: %+v", events[1])
	}
	interruptPolicy, ok := events[1].Data.(ToolInterruptPolicyDecision)
	if !ok {
		t.Fatalf("expected interrupt policy decision on start event, got %T", events[1].Data)
	}
	if interruptPolicy.Kind != ToolDecisionKindInterruptPolicy || interruptPolicy.InterruptBehavior != ToolInterruptBehaviorBlock {
		t.Fatalf("unexpected interrupt policy decision: %+v", interruptPolicy)
	}
	if events[2].ToolCallID != "tc1" || events[2].ToolName != "test_tool" || events[2].Result != "tool output" {
		t.Fatalf("unexpected result event: %+v", events[2])
	}
}

func TestRunAgenticLoop_EmitsCancelableToolInterruptPolicy(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			{
				ToolCalls:        []ToolCall{{ID: "tc-cancel", Name: "cancelable_tool"}},
				NeedsToolResults: true,
			},
			{Content: "done", NeedsToolResults: false},
		},
	}
	executor := &mockToolExecutor{
		results: map[string]string{"cancelable_tool": "ok"},
		traits: map[string]ToolTraits{
			"cancelable_tool": {InterruptBehavior: ToolInterruptBehaviorCancel},
		},
	}
	capture := &capturedToolLifecycle{}

	if _, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "use tool"}},
		Executor:        executor,
		MaxIterations:   10,
		LogPrefix:       "test",
		SessionID:       "sess-cancel",
		ToolEventSink:   capture.sink,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	events := capture.snapshot()
	var start ToolLifecycleEvent
	for _, evt := range events {
		if evt.Type == ToolLifecycleEventStart {
			start = evt
			break
		}
	}
	decision, ok := start.Data.(ToolInterruptPolicyDecision)
	if !ok {
		t.Fatalf("expected interrupt policy decision, got %T", start.Data)
	}
	if decision.InterruptBehavior != ToolInterruptBehaviorCancel {
		t.Fatalf("expected cancel interrupt behavior, got %+v", decision)
	}
}

func TestRunAgenticLoop_BlocksDuplicateMutatingToolCall(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			{
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "write", Args: map[string]any{"path": "/tmp/out.txt", "content": "hello"}},
					{ID: "tc2", Name: "write", Args: map[string]any{"path": "/tmp/out.txt", "content": "hello"}},
				},
				NeedsToolResults: true,
			},
			{Content: "done", NeedsToolResults: false},
		},
	}
	executor := &mockToolExecutor{results: map[string]string{"write": "wrote file"}}
	capture := &capturedToolLifecycle{}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "write twice"}},
		Executor:        executor,
		MaxIterations:   10,
		LogPrefix:       "test",
		SessionID:       "sess-mutate",
		TurnID:          "turn-mutate",
		ToolEventSink:   capture.sink,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "done" {
		t.Fatalf("unexpected response content: %q", resp.Content)
	}
	if got := executor.execCount.Load(); got != 1 {
		t.Fatalf("expected duplicate write to be blocked before execution, got %d executions", got)
	}
	if len(resp.HistoryDelta) < 3 || !strings.Contains(resp.HistoryDelta[2].Content, "duplicate mutating tool call blocked") {
		t.Fatalf("expected duplicate error in second tool result history, got %+v", resp.HistoryDelta)
	}

	var mutationDecision ToolMutationDecision
	var errorEvent *ToolLifecycleEvent
	for _, evt := range capture.snapshot() {
		if evt.Type == ToolLifecycleEventProgress {
			if decision, ok := evt.Data.(ToolMutationDecision); ok {
				mutationDecision = decision
			}
		}
		if evt.Type == ToolLifecycleEventError && evt.ToolCallID == "tc2" {
			copy := evt
			errorEvent = &copy
		}
	}
	if mutationDecision.Kind != ToolDecisionKindMutationDuplicate || !mutationDecision.Blocked || mutationDecision.Count != 1 || mutationDecision.Fingerprint == "" {
		t.Fatalf("unexpected mutation decision: %+v", mutationDecision)
	}
	if errorEvent == nil || !strings.Contains(errorEvent.Error, "duplicate mutating tool call blocked") {
		t.Fatalf("expected duplicate error lifecycle event, got %+v", errorEvent)
	}
	if decision, ok := errorEvent.Data.(ToolMutationDecision); !ok || decision.Kind != ToolDecisionKindMutationDuplicate || !decision.Blocked {
		t.Fatalf("expected mutation decision on duplicate error event, got %+v", errorEvent.Data)
	}
}

func TestRunAgenticLoop_AllowsSameTargetDifferentMutatingPayload(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			{
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "write", Args: map[string]any{"path": "/tmp/out.txt", "content": "hello"}},
					{ID: "tc2", Name: "write", Args: map[string]any{"path": "/tmp/out.txt", "content": "goodbye"}},
				},
				NeedsToolResults: true,
			},
			{Content: "done", NeedsToolResults: false},
		},
	}
	executor := &mockToolExecutor{results: map[string]string{"write": "wrote file"}}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "write two versions"}},
		Executor:        executor,
		MaxIterations:   10,
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "done" {
		t.Fatalf("unexpected response content: %q", resp.Content)
	}
	if got := executor.execCount.Load(); got != 2 {
		t.Fatalf("expected distinct payloads for the same target to execute, got %d executions", got)
	}
}

func TestExecuteSingleToolCall_EmitsToolError(t *testing.T) {
	capture := &capturedToolLifecycle{}
	call := ToolCall{ID: "tc-err", Name: "bad_tool"}
	failing := toolExecutorFunc(func(_ context.Context, _ ToolCall) (string, error) {
		return "", fmt.Errorf("boom")
	})

	result := executeSingleToolCall(context.Background(), failing, call, "sess-err", "turn-err", capture.sink, TraceContext{})
	if result.Content != "error: boom" {
		t.Fatalf("unexpected result content: %q", result.Content)
	}
	events := capture.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 lifecycle events, got %d", len(events))
	}
	if events[0].Type != ToolLifecycleEventStart || events[1].Type != ToolLifecycleEventError {
		t.Fatalf("unexpected lifecycle order: %+v", events)
	}
	if events[1].Error != "boom" || events[1].ToolCallID != "tc-err" || events[1].ToolName != "bad_tool" {
		t.Fatalf("unexpected error event: %+v", events[1])
	}
}

func TestExecuteToolBatches_EmitsSchedulerDecisions(t *testing.T) {
	executor := &mockToolExecutor{
		results: map[string]string{
			"safe_a":   "A",
			"safe_b":   "B",
			"unsafe_c": "C",
		},
		traits: map[string]ToolTraits{
			"safe_a":   {ConcurrencySafe: true},
			"safe_b":   {ConcurrencySafe: true},
			"unsafe_c": {},
		},
	}
	capture := &capturedToolLifecycle{}
	_ = executeToolBatches(context.Background(), executor, []ToolCall{
		{ID: "1", Name: "safe_a"},
		{ID: "2", Name: "safe_b"},
		{ID: "3", Name: "unsafe_c"},
	}, "sess-1", "turn-1", capture.sink, TraceContext{})

	var schedulerEvents []ToolLifecycleEvent
	for _, evt := range capture.snapshot() {
		if evt.Type == ToolLifecycleEventProgress {
			if _, ok := evt.Data.(ToolSchedulerDecision); ok {
				schedulerEvents = append(schedulerEvents, evt)
			}
		}
	}
	if len(schedulerEvents) != 3 {
		t.Fatalf("expected 3 scheduler events, got %d", len(schedulerEvents))
	}
	first := schedulerEvents[0].Data.(ToolSchedulerDecision)
	second := schedulerEvents[1].Data.(ToolSchedulerDecision)
	third := schedulerEvents[2].Data.(ToolSchedulerDecision)
	if first.Mode != "parallel" || first.BatchSize != 2 || first.BatchIndex != 0 || first.BatchPosition != 0 {
		t.Fatalf("unexpected first scheduler decision: %+v", first)
	}
	if second.Mode != "parallel" || second.BatchSize != 2 || second.BatchPosition != 1 || second.ConcurrencyLimit != 10 {
		t.Fatalf("unexpected second scheduler decision: %+v", second)
	}
	if third.Mode != "serial" || third.BatchIndex != 1 || third.BatchSize != 1 || third.ConcurrencySafe {
		t.Fatalf("unexpected third scheduler decision: %+v", third)
	}
}

func TestRunAgenticLoop_ParallelExecution(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			// Model requests 3 tools simultaneously
			{
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "tool_a"},
					{ID: "tc2", Name: "tool_b"},
					{ID: "tc3", Name: "tool_c"},
				},
				NeedsToolResults: true,
			},
			{Content: "all done", NeedsToolResults: false},
		},
	}

	executor := &mockToolExecutor{
		results: map[string]string{
			"tool_a": "a_result",
			"tool_b": "b_result",
			"tool_c": "c_result",
		},
		traits: map[string]ToolTraits{
			"tool_a": {ConcurrencySafe: true},
			"tool_b": {ConcurrencySafe: true},
			"tool_c": {ConcurrencySafe: true},
		},
		delays: map[string]time.Duration{
			"tool_a": 20 * time.Millisecond,
			"tool_b": 20 * time.Millisecond,
			"tool_c": 20 * time.Millisecond,
		},
	}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "do three things"}},
		Executor:        executor,
		MaxIterations:   10,
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "all done" {
		t.Errorf("got %q, want %q", resp.Content, "all done")
	}
	if executor.execCount.Load() != 3 {
		t.Errorf("expected 3 tool executions, got %d", executor.execCount.Load())
	}
	if executor.maxInFlight.Load() < 2 {
		t.Fatalf("expected concurrency-safe batch to execute concurrently, max in flight = %d", executor.maxInFlight.Load())
	}
}

func TestPartitionToolCalls_ConsecutiveConcurrencySafeBatches(t *testing.T) {
	executor := &mockToolExecutor{traits: map[string]ToolTraits{
		"safe_a":   {ConcurrencySafe: true},
		"safe_b":   {ConcurrencySafe: true},
		"unsafe_c": {},
		"safe_d":   {ConcurrencySafe: true},
	}}
	batches := partitionToolCalls(executor, []ToolCall{
		{ID: "1", Name: "safe_a"},
		{ID: "2", Name: "safe_b"},
		{ID: "3", Name: "unsafe_c"},
		{ID: "4", Name: "safe_d"},
	})
	if len(batches) != 3 {
		t.Fatalf("expected 3 batches, got %d", len(batches))
	}
	if !batches[0].isConcurrencySafe || len(batches[0].calls) != 2 {
		t.Fatalf("unexpected first batch: %+v", batches[0])
	}
	if batches[1].isConcurrencySafe || len(batches[1].calls) != 1 || batches[1].calls[0].Name != "unsafe_c" {
		t.Fatalf("unexpected second batch: %+v", batches[1])
	}
	if !batches[2].isConcurrencySafe || len(batches[2].calls) != 1 || batches[2].calls[0].Name != "safe_d" {
		t.Fatalf("unexpected third batch: %+v", batches[2])
	}
}

func TestExecuteToolBatches_PreservesResultOrderAcrossBatches(t *testing.T) {
	executor := &mockToolExecutor{
		results: map[string]string{
			"safe_a":   "A",
			"safe_b":   "B",
			"unsafe_c": "C",
			"safe_d":   "D",
		},
		traits: map[string]ToolTraits{
			"safe_a":   {ConcurrencySafe: true},
			"safe_b":   {ConcurrencySafe: true},
			"unsafe_c": {},
			"safe_d":   {ConcurrencySafe: true},
		},
		delays: map[string]time.Duration{
			"safe_a": 25 * time.Millisecond,
			"safe_b": 5 * time.Millisecond,
			"safe_d": 5 * time.Millisecond,
		},
	}
	results := executeToolBatches(context.Background(), executor, []ToolCall{
		{ID: "1", Name: "safe_a"},
		{ID: "2", Name: "safe_b"},
		{ID: "3", Name: "unsafe_c"},
		{ID: "4", Name: "safe_d"},
	}, "", "", nil, TraceContext{})
	if got, want := len(results), 4; got != want {
		t.Fatalf("expected %d results, got %d", want, got)
	}
	for i, wantID := range []string{"1", "2", "3", "4"} {
		if results[i].ToolCallID != wantID {
			t.Fatalf("result[%d].ToolCallID = %q, want %q", i, results[i].ToolCallID, wantID)
		}
	}
	for i, wantContent := range []string{"A", "B", "C", "D"} {
		if results[i].Content != wantContent {
			t.Fatalf("result[%d].Content = %q, want %q", i, results[i].Content, wantContent)
		}
	}
	if executor.maxInFlight.Load() < 2 {
		t.Fatalf("expected first safe batch to overlap, max in flight = %d", executor.maxInFlight.Load())
	}
}

func TestExecuteToolBatches_RespectsConcurrencyLimit(t *testing.T) {
	t.Setenv("CLAUDE_CODE_MAX_TOOL_USE_CONCURRENCY", "2")
	executor := &mockToolExecutor{
		results: map[string]string{
			"safe_a": "A",
			"safe_b": "B",
			"safe_c": "C",
			"safe_d": "D",
		},
		traits: map[string]ToolTraits{
			"safe_a": {ConcurrencySafe: true},
			"safe_b": {ConcurrencySafe: true},
			"safe_c": {ConcurrencySafe: true},
			"safe_d": {ConcurrencySafe: true},
		},
		delays: map[string]time.Duration{
			"safe_a": 20 * time.Millisecond,
			"safe_b": 20 * time.Millisecond,
			"safe_c": 20 * time.Millisecond,
			"safe_d": 20 * time.Millisecond,
		},
	}
	_ = executeToolBatches(context.Background(), executor, []ToolCall{
		{ID: "1", Name: "safe_a"},
		{ID: "2", Name: "safe_b"},
		{ID: "3", Name: "safe_c"},
		{ID: "4", Name: "safe_d"},
	}, "", "", nil, TraceContext{})
	if got := executor.maxInFlight.Load(); got > 2 {
		t.Fatalf("expected concurrency limit 2, got max in flight %d", got)
	}
}

func TestRunAgenticLoop_LoopBlocked(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			{
				ToolCalls:        []ToolCall{{ID: "tc1", Name: "stuck_tool"}},
				NeedsToolResults: true,
			},
		},
	}

	// Executor that returns CRITICAL error
	criticalExec := NewToolRegistry()
	criticalExec.Register("stuck_tool", func(_ context.Context, _ map[string]any) (string, error) {
		return "", fmt.Errorf("CRITICAL: tool loop detected")
	})

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "loop"}},
		Executor:        criticalExec,
		MaxIterations:   10,
		ForceText:       false,
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return the failure message since ForceText is false
	if !strings.Contains(resp.Content, "looping") {
		t.Errorf("expected loop failure message, got %q", resp.Content)
	}
	if resp.Outcome != TurnOutcomeBlocked || resp.StopReason != TurnStopReasonLoopBlocked {
		t.Fatalf("unexpected classification: outcome=%q stop_reason=%q", resp.Outcome, resp.StopReason)
	}
}

func TestRunAgenticLoop_LoopWarning_VisibleInHistory(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			{ToolCalls: []ToolCall{{ID: "tc1", Name: "poll", Args: map[string]any{"job": "123"}}}, NeedsToolResults: true},
			{Content: "done", NeedsToolResults: false},
		},
	}
	reg := NewToolRegistry()
	loopReg := toolloop.NewRegistry()
	cfg := toolloop.DefaultConfig()
	cfg.WarningThreshold = 2
	cfg.CriticalThreshold = 4
	cfg.GlobalCircuitBreakerThreshold = 6
	reg.SetLoopDetection(loopReg, cfg)
	reg.Register("poll", func(_ context.Context, _ map[string]any) (string, error) {
		return "tool output", nil
	})
	ctx := ContextWithSessionID(context.Background(), "sess-warning")
	capture := &capturedToolLifecycle{}
	state := loopReg.Get("sess-warning")
	for i := 0; i < 2; i++ {
		toolloop.RecordCall(state, "poll", map[string]any{"job": "123"}, fmt.Sprintf("prior-%d", i), &cfg)
	}

	resp, err := RunAgenticLoop(ctx, AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "poll"}},
		Executor:        reg,
		MaxIterations:   10,
		LogPrefix:       "test",
		ToolEventSink:   capture.sink,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.HistoryDelta) < 2 {
		t.Fatalf("expected tool result in history delta, got %+v", resp.HistoryDelta)
	}
	if !strings.Contains(resp.HistoryDelta[1].Content, "[LOOP DETECTION]") {
		t.Fatalf("expected loop warning in tool result history, got %q", resp.HistoryDelta[1].Content)
	}
	var loopDecision ToolLoopDecision
	found := false
	for _, evt := range capture.snapshot() {
		if evt.Type != ToolLifecycleEventProgress {
			continue
		}
		decision, ok := evt.Data.(ToolLoopDecision)
		if !ok {
			continue
		}
		loopDecision = decision
		found = true
		break
	}
	if !found {
		t.Fatal("expected loop decision event")
	}
	if loopDecision.Kind != ToolDecisionKindLoopDetection || loopDecision.Blocked || loopDecision.Level != string(toolloop.Warning) || loopDecision.Detector == "" {
		t.Fatalf("unexpected loop decision: %+v", loopDecision)
	}
	for _, evt := range capture.snapshot() {
		if evt.ToolCallID == "tc1" && evt.SessionID != "sess-warning" {
			t.Fatalf("expected consistent session id, got event %+v", evt)
		}
	}
}

func TestRunAgenticLoop_LoopCritical_BlocksExecution(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{{ToolCalls: []ToolCall{{ID: "tc1", Name: "command_status", Args: map[string]any{"job": "123"}}}, NeedsToolResults: true}},
	}
	reg := NewToolRegistry()
	loopReg := toolloop.NewRegistry()
	cfg := toolloop.DefaultConfig()
	cfg.WarningThreshold = 2
	cfg.CriticalThreshold = 3
	cfg.GlobalCircuitBreakerThreshold = 6
	var executed atomic.Int32
	reg.SetLoopDetection(loopReg, cfg)
	reg.Register("command_status", func(_ context.Context, _ map[string]any) (string, error) {
		executed.Add(1)
		return "tool output", nil
	})
	ctx := ContextWithSessionID(context.Background(), "sess-critical")
	capture := &capturedToolLifecycle{}
	state := loopReg.Get("sess-critical")
	for i := 0; i < 3; i++ {
		toolloop.RecordCall(state, "command_status", map[string]any{"job": "123"}, fmt.Sprintf("prior-%d", i), &cfg)
		toolloop.RecordOutcome(state, "command_status", map[string]any{"job": "123"}, fmt.Sprintf("prior-%d", i), "same", "", &cfg)
	}

	resp, err := RunAgenticLoop(ctx, AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "command_status"}},
		Executor:        reg,
		MaxIterations:   10,
		ForceText:       false,
		LogPrefix:       "test",
		ToolEventSink:   capture.sink,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executed.Load() != 0 {
		t.Fatalf("expected critical detection to block execution, got %d executions", executed.Load())
	}
	if resp.Outcome != TurnOutcomeBlocked || resp.StopReason != TurnStopReasonLoopBlocked {
		t.Fatalf("unexpected classification: outcome=%q stop_reason=%q", resp.Outcome, resp.StopReason)
	}
	if len(resp.HistoryDelta) < 2 || !strings.Contains(resp.HistoryDelta[1].Content, "CRITICAL:") {
		t.Fatalf("expected critical loop block in tool result history, got %+v", resp.HistoryDelta)
	}
	var loopDecision ToolLoopDecision
	var errorEvent *ToolLifecycleEvent
	for _, evt := range capture.snapshot() {
		if evt.Type == ToolLifecycleEventProgress {
			if decision, ok := evt.Data.(ToolLoopDecision); ok {
				loopDecision = decision
			}
		}
		if evt.Type == ToolLifecycleEventError {
			copy := evt
			errorEvent = &copy
		}
	}
	if loopDecision.Kind != ToolDecisionKindLoopDetection || !loopDecision.Blocked || loopDecision.Level != string(toolloop.Critical) {
		t.Fatalf("unexpected loop decision: %+v", loopDecision)
	}
	if errorEvent == nil {
		t.Fatal("expected loop block error event")
	}
	if errorDecision, ok := errorEvent.Data.(ToolLoopDecision); !ok || !errorDecision.Blocked {
		t.Fatalf("expected loop decision on error event, got %+v", errorEvent)
	}
	for _, evt := range capture.snapshot() {
		if evt.Type == ToolLifecycleEventStart {
			t.Fatalf("critical loop block should not emit start event: %+v", evt)
		}
		if evt.ToolCallID == "tc1" && evt.SessionID != "sess-critical" {
			t.Fatalf("expected consistent session id, got event %+v", evt)
		}
	}
}

func TestRunAgenticLoop_MaxIterationsExhausted(t *testing.T) {
	// Provider always returns tool calls
	provider := &mockChatProvider{
		responses: make([]*LLMResponse, 5),
	}
	for i := range provider.responses {
		provider.responses[i] = &LLMResponse{
			ToolCalls:        []ToolCall{{ID: fmt.Sprintf("tc%d", i), Name: "tool"}},
			NeedsToolResults: true,
		}
	}

	executor := &mockToolExecutor{}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "loop forever"}},
		Executor:        executor,
		MaxIterations:   3,
		ForceText:       false,
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp.Content, "looping") {
		t.Errorf("expected loop failure message, got %q", resp.Content)
	}
	if resp.Outcome != TurnOutcomeFailed || resp.StopReason != TurnStopReasonMaxIterations {
		t.Fatalf("unexpected classification: outcome=%q stop_reason=%q", resp.Outcome, resp.StopReason)
	}
}

func TestRunAgenticLoop_HistoryDelta_PlainText(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			{Content: "just text", NeedsToolResults: false},
		},
	}
	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "hello"}},
		Executor:        &mockToolExecutor{},
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.HistoryDelta) != 1 {
		t.Fatalf("expected 1 delta message, got %d", len(resp.HistoryDelta))
	}
	if resp.HistoryDelta[0].Role != "assistant" || resp.HistoryDelta[0].Content != "just text" {
		t.Errorf("delta[0] = %+v, want assistant/'just text'", resp.HistoryDelta[0])
	}
}

func TestRunAgenticLoop_HistoryDelta_WithTools(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			{
				ToolCalls:        []ToolCall{{ID: "tc1", Name: "read_file", Args: map[string]any{"path": "/tmp"}}},
				NeedsToolResults: true,
			},
			{Content: "file contents are xyz", NeedsToolResults: false},
		},
	}
	executor := &mockToolExecutor{results: map[string]string{"read_file": "xyz"}}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "read file"}},
		Executor:        executor,
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expected delta: assistant(tool_call) + tool(result) + assistant(text)
	if len(resp.HistoryDelta) != 3 {
		t.Fatalf("expected 3 delta messages, got %d: %+v", len(resp.HistoryDelta), resp.HistoryDelta)
	}
	// 1. Assistant tool-call
	d0 := resp.HistoryDelta[0]
	if d0.Role != "assistant" || len(d0.ToolCalls) != 1 {
		t.Errorf("delta[0]: want assistant with 1 tool call, got %+v", d0)
	}
	if d0.ToolCalls[0].Name != "read_file" || d0.ToolCalls[0].ID != "tc1" {
		t.Errorf("delta[0].ToolCalls[0] = %+v", d0.ToolCalls[0])
	}
	// 2. Tool result
	d1 := resp.HistoryDelta[1]
	if d1.Role != "tool" || d1.ToolCallID != "tc1" || d1.Content != "xyz" {
		t.Errorf("delta[1]: want tool/tc1/xyz, got %+v", d1)
	}
	// 3. Final assistant text
	d2 := resp.HistoryDelta[2]
	if d2.Role != "assistant" || d2.Content != "file contents are xyz" {
		t.Errorf("delta[2]: want assistant/'file contents are xyz', got %+v", d2)
	}
}

func TestRunAgenticLoop_HistoryDelta_MultipleIterations(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			{ToolCalls: []ToolCall{{ID: "a1", Name: "tool_a"}}, NeedsToolResults: true},
			{ToolCalls: []ToolCall{{ID: "b1", Name: "tool_b"}, {ID: "b2", Name: "tool_c"}}, NeedsToolResults: true},
			{Content: "done after two rounds", NeedsToolResults: false},
		},
	}
	executor := &mockToolExecutor{results: map[string]string{
		"tool_a": "ra", "tool_b": "rb", "tool_c": "rc",
	}}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "multi"}},
		Executor:        executor,
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Iter 1: assistant(a1) + tool(a1) = 2
	// Iter 2: assistant(b1,b2) + tool(b1) + tool(b2) = 3
	// Final: assistant(text) = 1
	// Total = 6
	if len(resp.HistoryDelta) != 6 {
		t.Fatalf("expected 6 delta messages, got %d", len(resp.HistoryDelta))
	}
	// Verify structure
	if resp.HistoryDelta[0].Role != "assistant" || len(resp.HistoryDelta[0].ToolCalls) != 1 {
		t.Error("delta[0] should be assistant with 1 tool call")
	}
	if resp.HistoryDelta[1].Role != "tool" {
		t.Error("delta[1] should be tool result")
	}
	if resp.HistoryDelta[2].Role != "assistant" || len(resp.HistoryDelta[2].ToolCalls) != 2 {
		t.Error("delta[2] should be assistant with 2 tool calls")
	}
	if resp.HistoryDelta[5].Role != "assistant" || resp.HistoryDelta[5].Content != "done after two rounds" {
		t.Errorf("delta[5] should be final assistant text, got %+v", resp.HistoryDelta[5])
	}
}

func TestRunAgenticLoop_InterruptedAfterToolResultsReturnsAbortedPartialHistory(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	provider := &interruptOnCanceledProvider{}
	executor := toolExecutorFunc(func(_ context.Context, _ ToolCall) (string, error) {
		cancel(ErrTurnInterrupted)
		return "tool completed before interrupt", nil
	})

	_, err := RunAgenticLoop(ctx, AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "use tool then interrupt"}},
		Executor:        executor,
		LogPrefix:       "test-interrupt",
	})
	if err == nil {
		t.Fatal("expected interrupt error")
	}
	if !errors.Is(err, ErrTurnInterrupted) {
		t.Fatalf("expected ErrTurnInterrupted, got %v", err)
	}
	partial, ok := PartialTurnResult(err)
	if !ok {
		t.Fatal("expected partial turn result from interrupted loop")
	}
	if partial.Outcome != TurnOutcomeAborted || partial.StopReason != TurnStopReasonCancelled {
		t.Fatalf("expected interrupted loop to classify as aborted/cancelled, got outcome=%q stop_reason=%q", partial.Outcome, partial.StopReason)
	}
	if len(partial.HistoryDelta) != 2 {
		t.Fatalf("expected assistant tool call and completed tool result in partial history, got %+v", partial.HistoryDelta)
	}
	if partial.HistoryDelta[0].Role != "assistant" || len(partial.HistoryDelta[0].ToolCalls) != 1 {
		t.Fatalf("partial history should start with assistant tool call, got %+v", partial.HistoryDelta[0])
	}
	if partial.HistoryDelta[1].Role != "tool" || partial.HistoryDelta[1].ToolCallID != "tc-interrupt" || partial.HistoryDelta[1].Content != "tool completed before interrupt" {
		t.Fatalf("partial history should preserve completed tool result, got %+v", partial.HistoryDelta[1])
	}
	if len(provider.calls) != 2 {
		t.Fatalf("expected provider to be called for initial and post-tool boundaries, got %d", len(provider.calls))
	}
	second := provider.calls[1]
	if len(second) < 2 || second[len(second)-1].Role != "tool" || second[len(second)-1].Content != "tool completed before interrupt" {
		t.Fatalf("post-tool provider boundary should include completed tool result before cancellation is returned, got %+v", second)
	}
}

func TestRunAgenticLoop_HistoryDelta_LLMError_PartialResult(t *testing.T) {
	callCount := 0
	provider := &mockChatProvider{}
	// Override the Chat method to fail on second call
	originalChat := provider.Chat
	_ = originalChat
	failProvider := &failOnSecondCallProvider{
		first: &LLMResponse{
			ToolCalls:        []ToolCall{{ID: "tc1", Name: "tool_a"}},
			NeedsToolResults: true,
			Usage:            ProviderUsage{InputTokens: 11, OutputTokens: 7},
		},
	}

	executor := &mockToolExecutor{results: map[string]string{"tool_a": "ok"}}
	_ = callCount

	_, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        failProvider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "fail mid-loop"}},
		Executor:        executor,
		LogPrefix:       "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	partial, ok := PartialTurnResult(err)
	if !ok {
		t.Fatal("expected TurnExecutionError with partial result")
	}
	// Should have: assistant(tool_call) + tool(result) = 2 messages
	if len(partial.HistoryDelta) != 2 {
		t.Fatalf("expected 2 partial delta messages, got %d: %+v", len(partial.HistoryDelta), partial.HistoryDelta)
	}
	if partial.HistoryDelta[0].Role != "assistant" || len(partial.HistoryDelta[0].ToolCalls) != 1 {
		t.Error("partial delta[0] should be assistant tool-call")
	}
	if partial.HistoryDelta[1].Role != "tool" || partial.HistoryDelta[1].Content != "ok" {
		t.Errorf("partial delta[1] should be tool result, got %+v", partial.HistoryDelta[1])
	}
	if partial.Usage.InputTokens != 11 || partial.Usage.OutputTokens != 7 {
		t.Fatalf("expected partial usage to be preserved, got %+v", partial.Usage)
	}
}

type interruptOnCanceledProvider struct {
	calls     [][]LLMMessage
	callCount int
}

func (p *interruptOnCanceledProvider) Chat(ctx context.Context, messages []LLMMessage, _ []ToolDefinition, _ ChatOptions) (*LLMResponse, error) {
	captured := make([]LLMMessage, len(messages))
	copy(captured, messages)
	p.calls = append(p.calls, captured)
	p.callCount++
	if p.callCount == 1 {
		return &LLMResponse{
			ToolCalls:        []ToolCall{{ID: "tc-interrupt", Name: "interruptible_tool"}},
			NeedsToolResults: true,
		}, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return &LLMResponse{Content: "unexpected completion", NeedsToolResults: false}, nil
}

// failOnSecondCallProvider returns the first response, then errors.
type failOnSecondCallProvider struct {
	first     *LLMResponse
	callCount int
}

func (p *failOnSecondCallProvider) Chat(_ context.Context, _ []LLMMessage, _ []ToolDefinition, _ ChatOptions) (*LLMResponse, error) {
	p.callCount++
	if p.callCount == 1 {
		return p.first, nil
	}
	return nil, fmt.Errorf("API error: 500 internal server error")
}

func TestRunAgenticLoop_ForceSummary(t *testing.T) {
	provider := &forceSummaryProvider{}
	executor := &mockToolExecutor{}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "summarize"}},
		Tools:           []ToolDefinition{{Name: "tool"}},
		Executor:        executor,
		MaxIterations:   2,
		ForceText:       true,
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "forced summary response" {
		t.Errorf("got %q, want %q", resp.Content, "forced summary response")
	}
	if resp.Outcome != TurnOutcomeForcedSummary || resp.StopReason != TurnStopReasonForcedSummary {
		t.Fatalf("unexpected classification: outcome=%q stop_reason=%q", resp.Outcome, resp.StopReason)
	}
	if len(resp.HistoryDelta) != 7 {
		t.Fatalf("expected pending force-summary tool activity in history, got %d messages", len(resp.HistoryDelta))
	}
}

func TestRunAgenticLoop_SteeringDrainInitialCall(t *testing.T) {
	provider := &capturingChatProvider{
		responses: []*LLMResponse{{Content: "direct answer", NeedsToolResults: false}},
	}
	drainCalls := 0
	drain := func(context.Context) []InjectedUserInput {
		drainCalls++
		if drainCalls == 1 {
			return []InjectedUserInput{{Content: "[Additional user input received while you were working]\ninitial steering"}}
		}
		return nil
	}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "start"}},
		Executor:        &mockToolExecutor{},
		SteeringDrain:   drain,
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "direct answer" {
		t.Fatalf("got %q, want direct answer", resp.Content)
	}
	if len(provider.calls) != 1 {
		t.Fatalf("expected one provider call, got %d", len(provider.calls))
	}
	last := provider.calls[0][len(provider.calls[0])-1]
	if last.Role != "user" || !strings.Contains(last.Content, "initial steering") {
		t.Fatalf("expected drained steering as final initial-call user message, got %+v", last)
	}
}

func TestRunAgenticLoop_SteeringDrainAfterToolResultsBeforeNextCall(t *testing.T) {
	provider := &capturingChatProvider{
		responses: []*LLMResponse{
			{NeedsToolResults: true, ToolCalls: []ToolCall{{ID: "tc1", Name: "read_file"}}},
			{Content: "done", NeedsToolResults: false},
		},
	}
	executor := &mockToolExecutor{results: map[string]string{"read_file": "tool output"}}
	drainCalls := 0
	largeSteering := "[Additional user input received while you were working]\n" + strings.Repeat("please account for this ", 35)
	drain := func(context.Context) []InjectedUserInput {
		drainCalls++
		if drainCalls == 2 {
			return []InjectedUserInput{{Content: largeSteering}}
		}
		return nil
	}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider: provider,
		InitialMessages: []LLMMessage{
			{Role: "user", Content: strings.Repeat("old-history ", 60)},
			{Role: "assistant", Content: "prior answer"},
			{Role: "user", Content: "use a tool"},
		},
		Executor:            executor,
		SteeringDrain:       drain,
		LogPrefix:           "test",
		ContextWindowTokens: 1_000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "done" {
		t.Fatalf("got %q, want done", resp.Content)
	}
	if len(provider.calls) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(provider.calls))
	}
	if containsMessageContent(provider.calls[0], "please account for this") {
		t.Fatal("steering injected into the first provider call; want only the next model boundary")
	}
	second := provider.calls[1]
	if len(second) < 2 {
		t.Fatalf("second call too short: %+v", second)
	}
	toolResult := second[len(second)-2]
	steering := second[len(second)-1]
	if toolResult.Role != "tool" || toolResult.ToolCallID != "tc1" || toolResult.Content != "tool output" {
		t.Fatalf("expected required tool result immediately before steering, got %+v", toolResult)
	}
	if steering.Role != "user" || !strings.Contains(steering.Content, "please account for this") {
		t.Fatalf("expected drained steering as final user message, got %+v", steering)
	}
	if containsMessageContent(second, "old-history") {
		t.Fatal("expected post-steering preflight to trim older history from the sent request")
	}
}

func TestRunAgenticLoop_SteeringDrainForceSummaryKeepsSummaryPromptLast(t *testing.T) {
	provider := &capturingForceSummaryProvider{}
	executor := &mockToolExecutor{results: map[string]string{"tool": "tool output"}}
	drainCalls := 0
	drain := func(context.Context) []InjectedUserInput {
		drainCalls++
		if drainCalls == 3 {
			return []InjectedUserInput{{Content: "[Additional user input received while you were working]\ninclude this in the summary"}}
		}
		return nil
	}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "summarize"}},
		Tools:           []ToolDefinition{{Name: "tool"}},
		Executor:        executor,
		MaxIterations:   1,
		ForceText:       true,
		SteeringDrain:   drain,
		LogPrefix:       "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "forced summary response" {
		t.Fatalf("got %q, want forced summary response", resp.Content)
	}
	if len(provider.summaryMessages) == 0 {
		t.Fatal("expected to capture force-summary provider messages")
	}
	msgs := provider.summaryMessages
	if len(msgs) < 4 {
		t.Fatalf("summary call too short: %+v", msgs)
	}
	last := msgs[len(msgs)-1]
	steering := msgs[len(msgs)-2]
	pendingTool := msgs[len(msgs)-3]
	if !strings.Contains(last.Content, "Please summarise your findings") {
		t.Fatalf("expected synthetic summary prompt last, got %+v", last)
	}
	if steering.Role != "user" || !strings.Contains(steering.Content, "include this in the summary") {
		t.Fatalf("expected steering immediately before summary prompt, got %+v", steering)
	}
	if pendingTool.Role != "tool" || pendingTool.Content != "tool output" {
		t.Fatalf("expected pending tool result before steering, got %+v", pendingTool)
	}
}

func containsMessageContent(messages []LLMMessage, needle string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}

// ── Deferred tool discovery test ────────────────────────────────────────────

// toolCapturingProvider records the tool lists passed to each Chat call.
type toolCapturingProvider struct {
	responses []*LLMResponse
	callCount int
	toolLists [][]string // tool names per call
	mu        sync.Mutex
}

func (p *toolCapturingProvider) Chat(_ context.Context, _ []LLMMessage, tools []ToolDefinition, _ ChatOptions) (*LLMResponse, error) {
	p.mu.Lock()
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	p.toolLists = append(p.toolLists, names)
	idx := p.callCount
	p.callCount++
	p.mu.Unlock()

	if idx < len(p.responses) {
		return p.responses[idx], nil
	}
	return &LLMResponse{Content: "done"}, nil
}

func TestRunAgenticLoop_DeferredToolDiscovery(t *testing.T) {
	// Setup: one inline tool (read_file) + two deferred MCP tools.
	deferred := NewDeferredToolSet()
	deferred.Add(DeferredToolEntry{
		Name:    "mcp__server__web_search",
		Summary: "Search the web",
		Definition: ToolDefinition{
			Name:        "mcp__server__web_search",
			Description: "Search the web for information",
		},
	})
	deferred.Add(DeferredToolEntry{
		Name:    "mcp__server__web_fetch",
		Summary: "Fetch a URL",
		Definition: ToolDefinition{
			Name:        "mcp__server__web_fetch",
			Description: "Fetch content from a URL",
		},
	})

	// LLM calls:
	// 1. First call → model calls tool_search to find web tools
	// 2. Second call (after tool_search result) → model calls mcp__server__web_search
	// 3. Third call → model produces text (done)
	provider := &toolCapturingProvider{
		responses: []*LLMResponse{
			{
				ToolCalls: []ToolCall{{
					ID:   "tc1",
					Name: ToolSearchToolName,
					Args: map[string]any{"query": "select:mcp__server__web_search"},
				}},
				NeedsToolResults: true,
			},
			{
				ToolCalls: []ToolCall{{
					ID:   "tc2",
					Name: "mcp__server__web_search",
					Args: map[string]any{"query": "golang testing"},
				}},
				NeedsToolResults: true,
			},
			{
				Content: "Here are the search results.",
			},
		},
	}

	executor := &mockToolExecutor{
		results: map[string]string{
			"mcp__server__web_search": "search results for golang testing",
		},
	}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "search for golang testing"}},
		Tools:           []ToolDefinition{{Name: "read_file", Description: "Read a file"}},
		Executor:        executor,
		MaxIterations:   10,
		LogPrefix:       "test-deferred",
		DeferredTools:   deferred,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Here are the search results." {
		t.Errorf("got content %q, want %q", resp.Content, "Here are the search results.")
	}

	// Verify tool lists:
	// Call 1: should have tool_search + read_file (inline tools)
	if len(provider.toolLists) < 3 {
		t.Fatalf("expected at least 3 LLM calls, got %d", len(provider.toolLists))
	}
	call1Tools := provider.toolLists[0]
	hasToolSearch := false
	hasReadFile := false
	for _, name := range call1Tools {
		if name == ToolSearchToolName {
			hasToolSearch = true
		}
		if name == "read_file" {
			hasReadFile = true
		}
	}
	if !hasToolSearch {
		t.Errorf("call 1 should include tool_search, got %v", call1Tools)
	}
	if !hasReadFile {
		t.Errorf("call 1 should include read_file, got %v", call1Tools)
	}
	// Call 1 should NOT have the deferred tool yet.
	for _, name := range call1Tools {
		if name == "mcp__server__web_search" {
			t.Error("call 1 should NOT include mcp__server__web_search before discovery")
		}
	}

	// Call 2: should now include mcp__server__web_search (discovered via tool_search).
	call2Tools := provider.toolLists[1]
	hasDiscovered := false
	for _, name := range call2Tools {
		if name == "mcp__server__web_search" {
			hasDiscovered = true
		}
	}
	if !hasDiscovered {
		t.Errorf("call 2 should include discovered mcp__server__web_search, got %v", call2Tools)
	}

	// mcp__server__web_fetch should NOT appear (it was never searched for).
	for _, tools := range provider.toolLists {
		for _, name := range tools {
			if name == "mcp__server__web_fetch" {
				t.Error("mcp__server__web_fetch should never appear — it was not searched for")
			}
		}
	}
}

type forceSummaryProvider struct {
	callCount int
}

func (p *forceSummaryProvider) Chat(_ context.Context, _ []LLMMessage, tools []ToolDefinition, _ ChatOptions) (*LLMResponse, error) {
	p.callCount++
	if tools == nil {
		return &LLMResponse{Content: "forced summary response"}, nil
	}
	return &LLMResponse{
		ToolCalls:        []ToolCall{{ID: fmt.Sprintf("tc%d", p.callCount), Name: "tool"}},
		NeedsToolResults: true,
	}, nil
}

type capturingForceSummaryProvider struct {
	callCount       int
	summaryMessages []LLMMessage
}

func (p *capturingForceSummaryProvider) Chat(_ context.Context, messages []LLMMessage, tools []ToolDefinition, _ ChatOptions) (*LLMResponse, error) {
	p.callCount++
	if tools == nil {
		p.summaryMessages = append([]LLMMessage(nil), messages...)
		return &LLMResponse{Content: "forced summary response"}, nil
	}
	return &LLMResponse{
		ToolCalls:        []ToolCall{{ID: fmt.Sprintf("tc%d", p.callCount), Name: "tool"}},
		NeedsToolResults: true,
	}, nil
}
