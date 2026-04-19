package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// streamingEchoProvider is a Provider that also implements StreamingProvider,
// delivering the response word by word.
type streamingEchoProvider struct{}

type mutatingToolProvider struct {
	tools     *ToolRegistry
	seenTools []ToolDefinition
}

func (streamingEchoProvider) Generate(_ context.Context, turn Turn) (ProviderResult, error) {
	return ProviderResult{Text: "ack: " + turn.UserText}, nil
}

func (p *mutatingToolProvider) Generate(_ context.Context, turn Turn) (ProviderResult, error) {
	p.seenTools = append([]ToolDefinition(nil), turn.Tools...)
	if p.tools != nil {
		p.tools.Remove("mcp_demo_echo")
	}
	return ProviderResult{ToolCalls: []ToolCall{{Name: "mcp_demo_echo"}}}, nil
}

func (streamingEchoProvider) Stream(_ context.Context, turn Turn, onChunk func(string)) (ProviderResult, error) {
	words := strings.Fields("ack: " + turn.UserText)
	var sb strings.Builder
	for i, w := range words {
		token := w
		if i < len(words)-1 {
			token += " "
		}
		sb.WriteString(token)
		if onChunk != nil {
			onChunk(token)
		}
	}
	return ProviderResult{Text: sb.String()}, nil
}

func TestProviderRuntime_ProcessTurn(t *testing.T) {
	rt, _ := NewProviderRuntime(EchoProvider{}, nil)
	result, err := rt.ProcessTurn(context.Background(), Turn{UserText: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "hello") {
		t.Fatalf("expected 'hello' in result, got %q", result.Text)
	}
	if result.Outcome != TurnOutcomeCompleted || result.StopReason != TurnStopReasonModelText {
		t.Fatalf("unexpected classification: outcome=%q stop_reason=%q", result.Outcome, result.StopReason)
	}
}

func TestProviderRuntime_ProcessTurn_UsesPerTurnToolSnapshot(t *testing.T) {
	tools := NewToolRegistry()
	tools.RegisterWithDef("mcp_demo_echo", func(_ context.Context, _ map[string]any) (string, error) {
		return "snapshot-ok", nil
	}, ToolDefinition{Name: "mcp_demo_echo", Description: "demo"})
	provider := &mutatingToolProvider{tools: tools}
	rt, _ := NewProviderRuntime(provider, tools)

	result, err := rt.ProcessTurn(context.Background(), Turn{UserText: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provider.seenTools) != 1 || provider.seenTools[0].Name != "mcp_demo_echo" {
		t.Fatalf("expected provider to see frozen MCP tool surface, got %+v", provider.seenTools)
	}
	if len(result.ToolTraces) != 1 || result.ToolTraces[0].Result != "snapshot-ok" || result.ToolTraces[0].Error != "" {
		t.Fatalf("expected frozen tool snapshot to execute successfully, got %+v", result.ToolTraces)
	}
	if _, ok := tools.Descriptor("mcp_demo_echo"); ok {
		t.Fatal("expected live registry mutation to remove the original tool")
	}
}

func TestProviderRuntime_ProcessTurnStreaming_WithStreamingProvider(t *testing.T) {
	rt, _ := NewProviderRuntime(streamingEchoProvider{}, nil)
	var chunks []string
	result, err := rt.ProcessTurnStreaming(context.Background(), Turn{UserText: "world"}, func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected chunks to be delivered")
	}
	joined := strings.Join(chunks, "")
	if !strings.Contains(joined, "world") {
		t.Fatalf("expected 'world' in chunks, got %q", joined)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty result.Text")
	}
	// Verify chunks assemble to the full text.
	if strings.TrimSpace(joined) != strings.TrimSpace(result.Text) {
		t.Fatalf("chunks %q don't match result.Text %q", joined, result.Text)
	}
}

func TestProviderRuntime_ProcessTurnStreaming_FallbackNoStreaming(t *testing.T) {
	// EchoProvider does NOT implement StreamingProvider.
	// ProcessTurnStreaming should fall back to Generate + single onChunk call.
	rt, _ := NewProviderRuntime(EchoProvider{}, nil)
	callCount := 0
	result, err := rt.ProcessTurnStreaming(context.Background(), Turn{UserText: "fallback"}, func(chunk string) {
		callCount++
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected exactly 1 chunk call for non-streaming provider, got %d", callCount)
	}
	if !strings.Contains(result.Text, "fallback") {
		t.Fatalf("expected 'fallback' in result, got %q", result.Text)
	}
}

func TestProviderRuntime_ProcessTurnStreaming_NilChunkHandler(t *testing.T) {
	rt, _ := NewProviderRuntime(streamingEchoProvider{}, nil)
	// Should not panic with nil onChunk.
	result, err := rt.ProcessTurnStreaming(context.Background(), Turn{UserText: "nil"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty result.Text")
	}
}

func TestProviderRuntime_StreamingRuntime_InterfaceCheck(t *testing.T) {
	rt, _ := NewProviderRuntime(streamingEchoProvider{}, nil)
	if _, ok := any(rt).(StreamingRuntime); !ok {
		t.Fatal("ProviderRuntime should implement StreamingRuntime")
	}
}

func TestClassifyTurnError(t *testing.T) {
	if outcome, stopReason, ok := ClassifyTurnError(context.DeadlineExceeded); !ok || outcome != TurnOutcomeAborted || stopReason != TurnStopReasonCancelled {
		t.Fatalf("deadline classification mismatch outcome=%q stop_reason=%q ok=%v", outcome, stopReason, ok)
	}
	err := &TurnExecutionError{
		Cause: errors.New("provider failed"),
		Partial: TurnResult{
			Outcome:    TurnOutcomeBlocked,
			StopReason: TurnStopReasonLoopBlocked,
		},
	}
	if outcome, stopReason, ok := ClassifyTurnError(err); !ok || outcome != TurnOutcomeBlocked || stopReason != TurnStopReasonLoopBlocked {
		t.Fatalf("partial classification mismatch outcome=%q stop_reason=%q ok=%v", outcome, stopReason, ok)
	}
}

func TestClassifyTurnResult(t *testing.T) {
	outcome, stopReason := ClassifyTurnResult(TurnResult{Text: "ok"})
	if outcome != TurnOutcomeCompleted || stopReason != TurnStopReasonModelText {
		t.Fatalf("plain text classification mismatch outcome=%q stop_reason=%q", outcome, stopReason)
	}
}

// ─── Streaming tool-call fallback ────────────────────────────────────────────

// streamingToolProvider returns tool calls from Stream() but produces text
// from Generate() (simulating the agentic loop's synthesis behaviour).
type streamingToolProvider struct{}

func (streamingToolProvider) Generate(_ context.Context, turn Turn) (ProviderResult, error) {
	return ProviderResult{Text: "synthesised answer from agentic loop"}, nil
}

func (streamingToolProvider) Stream(_ context.Context, _ Turn, _ func(string)) (ProviderResult, error) {
	return ProviderResult{
		ToolCalls: []ToolCall{{ID: "tc1", Name: "search", Args: map[string]any{"q": "test"}}},
	}, nil
}

func TestProviderRuntime_ProcessTurnStreaming_ToolCallFallsBackToGenerate(t *testing.T) {
	// When the streaming response returns tool calls, ProcessTurnStreaming
	// should fall back to Generate (the agentic loop) and produce a real
	// text response instead of the old "tool execution complete" placeholder.
	tools := NewToolRegistry()
	tools.RegisterWithDef("search", func(_ context.Context, _ map[string]any) (string, error) {
		return "search result", nil
	}, ToolDefinition{Name: "search", Description: "search tool"})

	rt, _ := NewProviderRuntime(streamingToolProvider{}, tools)
	var chunks []string
	result, err := rt.ProcessTurnStreaming(context.Background(), Turn{UserText: "hello"}, func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text == "tool execution complete" {
		t.Fatal("should not produce dead-end 'tool execution complete' placeholder")
	}
	if !strings.Contains(result.Text, "synthesised answer") {
		t.Fatalf("expected synthesised answer from Generate fallback, got %q", result.Text)
	}
	// The Generate response text should have been streamed to onChunk.
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk from the Generate fallback")
	}
}

func TestProviderRuntime_ProcessTurnStreaming_NoToolCalls_NoFallback(t *testing.T) {
	// When streaming returns text without tool calls, no fallback should occur.
	rt, _ := NewProviderRuntime(streamingEchoProvider{}, nil)
	result, err := rt.ProcessTurnStreaming(context.Background(), Turn{UserText: "hello"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "hello") {
		t.Fatalf("expected echoed text, got %q", result.Text)
	}
}

// ─── buildResult safety-net summary ──────────────────────────────────────────

// toolOnlyProvider returns only tool calls and no text — exercises the
// buildResult safety net when the agentic loop isn't used.
type toolOnlyProvider struct{}

func (toolOnlyProvider) Generate(_ context.Context, _ Turn) (ProviderResult, error) {
	return ProviderResult{
		ToolCalls: []ToolCall{{ID: "tc1", Name: "lookup", Args: map[string]any{"key": "x"}}},
	}, nil
}

func TestBuildResult_ToolOnlySummarisesResults(t *testing.T) {
	// When a provider returns tool calls without text and no streaming
	// fallback runs, buildResult should produce a useful summary instead
	// of the old "tool execution complete" placeholder.
	tools := NewToolRegistry()
	tools.RegisterWithDef("lookup", func(_ context.Context, _ map[string]any) (string, error) {
		return "value=42", nil
	}, ToolDefinition{Name: "lookup", Description: "lookup tool"})

	rt, _ := NewProviderRuntime(toolOnlyProvider{}, tools)
	result, err := rt.ProcessTurn(context.Background(), Turn{UserText: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text == "tool execution complete" {
		t.Fatal("should not produce dead-end 'tool execution complete' placeholder")
	}
	if !strings.Contains(result.Text, "lookup") || !strings.Contains(result.Text, "value=42") {
		t.Fatalf("expected tool summary with name and result, got %q", result.Text)
	}
	if result.Outcome != TurnOutcomeCompletedWithTools {
		t.Fatalf("expected CompletedWithTools outcome, got %q", result.Outcome)
	}
}

func TestBuildResult_ToolErrorSummarised(t *testing.T) {
	tools := NewToolRegistry()
	tools.RegisterWithDef("broken", func(_ context.Context, _ map[string]any) (string, error) {
		return "", errors.New("connection refused")
	}, ToolDefinition{Name: "broken", Description: "broken tool"})

	provider := &toolOnlyProviderNamed{name: "broken"}
	rt, _ := NewProviderRuntime(provider, tools)
	result, err := rt.ProcessTurn(context.Background(), Turn{UserText: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "broken") || !strings.Contains(result.Text, "connection refused") {
		t.Fatalf("expected error summary, got %q", result.Text)
	}
}

type toolOnlyProviderNamed struct{ name string }

func (p *toolOnlyProviderNamed) Generate(_ context.Context, _ Turn) (ProviderResult, error) {
	return ProviderResult{
		ToolCalls: []ToolCall{{ID: "tc1", Name: p.name, Args: map[string]any{}}},
	}, nil
}
