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
	}, definitions: map[string]ToolDefinition{}}

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
}

func TestRunAgenticLoop_ForceSummary(t *testing.T) {
	callCount := 0
	// Provider that returns tool calls N times, then text on final call
	provider := &mockChatProvider{
		responses: []*LLMResponse{
			// Initial: tool call
			{ToolCalls: []ToolCall{{ID: "tc1", Name: "tool"}}, NeedsToolResults: true},
			// Iteration 1: another tool call
			{ToolCalls: []ToolCall{{ID: "tc2", Name: "tool"}}, NeedsToolResults: true},
			// Force summary call (with nil tools): text
			{Content: "forced summary response"},
		},
	}
	_ = callCount

	executor := &mockToolExecutor{}

	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []LLMMessage{{Role: "user", Content: "summarize"}},
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
}
