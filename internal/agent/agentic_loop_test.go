package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
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
	execCount atomic.Int32
	results   map[string]string
}

func (m *mockToolExecutor) Execute(_ context.Context, call ToolCall) (string, error) {
	m.execCount.Add(1)
	if r, ok := m.results[call.Name]; ok {
		return r, nil
	}
	return "ok", nil
}

func (m *mockToolExecutor) Definitions() []ToolDefinition { return nil }

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

	executor := &mockToolExecutor{results: map[string]string{
		"tool_a": "a_result",
		"tool_b": "b_result",
		"tool_c": "c_result",
	}}

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
	// All 3 tools should have been executed
	if executor.execCount.Load() != 3 {
		t.Errorf("expected 3 tool executions, got %d", executor.execCount.Load())
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
	criticalExec := &ToolRegistry{tools: map[string]ToolFunc{
		"stuck_tool": func(_ context.Context, _ map[string]any) (string, error) {
			return "", fmt.Errorf("CRITICAL: tool loop detected")
		},
	}, descriptors: map[string]ToolDescriptor{}}

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
