package agent

import (
	"context"
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
	})
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
	})
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
}

func TestRunAgenticLoop_LoopCritical_BlocksExecution(t *testing.T) {
	provider := &mockChatProvider{
		responses: []*LLMResponse{{ToolCalls: []ToolCall{{ID: "tc1", Name: "poll", Args: map[string]any{"job": "123"}}}, NeedsToolResults: true}},
	}
	reg := NewToolRegistry()
	loopReg := toolloop.NewRegistry()
	cfg := toolloop.DefaultConfig()
	cfg.WarningThreshold = 2
	cfg.CriticalThreshold = 3
	cfg.GlobalCircuitBreakerThreshold = 6
	var executed atomic.Int32
	reg.SetLoopDetection(loopReg, cfg)
	reg.Register("poll", func(_ context.Context, _ map[string]any) (string, error) {
		executed.Add(1)
		return "tool output", nil
	})
	ctx := ContextWithSessionID(context.Background(), "sess-critical")
	state := loopReg.Get("sess-critical")
	for i := 0; i < 3; i++ {
		toolloop.RecordCall(state, "poll", map[string]any{"job": "123"}, fmt.Sprintf("prior-%d", i), &cfg)
		toolloop.RecordOutcome(state, "poll", map[string]any{"job": "123"}, fmt.Sprintf("prior-%d", i), "same", "", &cfg)
	}

	resp, err := RunAgenticLoop(ctx, AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "poll"}},
		Executor:        reg,
		MaxIterations:   10,
		ForceText:       false,
		LogPrefix:       "test",
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
