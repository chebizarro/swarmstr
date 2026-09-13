package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestProviderRuntime_ProcessTurn_RunsPostSamplingHooks(t *testing.T) {
	rt, _ := NewProviderRuntime(EchoProvider{}, nil)
	done := make(chan string, 1)
	result, err := rt.ProcessTurn(context.Background(), Turn{
		SessionID: "hook-session",
		UserText:  "hello hook",
		PostSamplingHooks: []PostSamplingHook{PostSamplingHookFunc(func(_ context.Context, turn Turn, result TurnResult) {
			if turn.SessionID != "hook-session" {
				done <- "bad session: " + turn.SessionID
				return
			}
			if !strings.Contains(result.Text, "hello hook") {
				done <- "bad result: " + result.Text
				return
			}
			done <- "ok"
		})},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text == "" {
		t.Fatalf("expected result text")
	}
	select {
	case got := <-done:
		if got != "ok" {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("post-sampling hook was not called")
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
	if outcome, stopReason, ok := ClassifyTurnError(ErrTurnInterrupted); !ok || outcome != TurnOutcomeAborted || stopReason != TurnStopReasonCancelled {
		t.Fatalf("interrupt classification mismatch outcome=%q stop_reason=%q ok=%v", outcome, stopReason, ok)
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

func TestTurnCancellationCauseUsesInterruptCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(ErrTurnInterrupted)
	got := turnCancellationCause(ctx, context.Canceled)
	if !errors.Is(got, ErrTurnInterrupted) {
		t.Fatalf("expected interrupt cause, got %v", got)
	}
}

func TestClassifyTurnResult(t *testing.T) {
	outcome, stopReason := ClassifyTurnResult(TurnResult{Text: "ok"})
	if outcome != TurnOutcomeCompleted || stopReason != TurnStopReasonModelText {
		t.Fatalf("plain text classification mismatch outcome=%q stop_reason=%q", outcome, stopReason)
	}
}

// ─── Streaming tool-call fallback ────────────────────────────────────────────

// streamingToolProvider returns tool calls from Stream() and records whether
// Generate() was invoked.
type streamingToolProvider struct {
	generateCalls int
}

func (p *streamingToolProvider) Generate(_ context.Context, _ Turn) (ProviderResult, error) {
	p.generateCalls++
	return ProviderResult{Text: "unexpected generate call"}, nil
}

func (p *streamingToolProvider) Stream(_ context.Context, _ Turn, _ func(string)) (ProviderResult, error) {
	return ProviderResult{
		Text:      "looking up",
		ToolCalls: []ToolCall{{ID: "tc1", Name: "search", Args: map[string]any{"q": "test"}}},
	}, nil
}

func TestProviderRuntime_ProcessTurnStreaming_ToolCallDoesNotFallbackToGenerate(t *testing.T) {
	// When the streaming response returns tool calls alongside streamed text,
	// ProcessTurnStreaming uses the streamed ProviderResult directly instead of
	// replaying the prompt with Generate(). The agentic-loop fallback only
	// applies to blank-text tool-call rounds (nothing user-visible streamed).
	tools := NewToolRegistry()
	tools.RegisterWithDef("search", func(_ context.Context, _ map[string]any) (string, error) {
		return "search result", nil
	}, ToolDefinition{Name: "search", Description: "search tool"})

	provider := &streamingToolProvider{}
	rt, _ := NewProviderRuntime(provider, tools)
	var chunks []string
	result, err := rt.ProcessTurnStreaming(context.Background(), Turn{UserText: "hello"}, func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.generateCalls != 0 {
		t.Fatalf("Generate calls = %d, want 0", provider.generateCalls)
	}
	if !strings.Contains(result.Text, "looking up") {
		t.Fatalf("expected streamed text, got %q", result.Text)
	}
	if len(result.ToolTraces) != 1 || result.ToolTraces[0].Call.Name != "search" || result.ToolTraces[0].Result != "search result" {
		t.Fatalf("expected streamed tool call to execute, got %#v", result.ToolTraces)
	}
	if len(result.HistoryDelta) == 0 || len(result.HistoryDelta[0].ToolCalls) != 1 {
		t.Fatalf("expected assistant tool-call history from streamed result, got %#v", result.HistoryDelta)
	}
	if len(chunks) != 0 {
		t.Fatalf("did not expect Generate fallback chunks, got %#v", chunks)
	}
}

// blankStreamToolProvider streams tool calls with no text, then returns a
// synthesized answer from Generate. It models a tool-use model whose first
// streamed round is a pure tool call — the case that previously produced a raw
// tool-log reply.
type blankStreamToolProvider struct {
	generateCalls int
	result        ProviderResult
}

func (p *blankStreamToolProvider) Stream(_ context.Context, _ Turn, _ func(string)) (ProviderResult, error) {
	return ProviderResult{
		ToolCalls: []ToolCall{{ID: "tc1", Name: "search", Args: map[string]any{"q": "test"}}},
	}, nil
}

func (p *blankStreamToolProvider) Generate(_ context.Context, _ Turn) (ProviderResult, error) {
	p.generateCalls++
	return p.result, nil
}

func TestProviderRuntime_ProcessTurnStreaming_BlankTextToolCallsFallbackToGenerate(t *testing.T) {
	// A blank-text tool-call round must fall back to the agentic loop so the
	// user gets a synthesized answer instead of the raw "[tool] result" safety
	// net.
	tools := NewToolRegistry()
	tools.RegisterWithDef("search", func(_ context.Context, _ map[string]any) (string, error) {
		return "search result", nil
	}, ToolDefinition{Name: "search", Description: "search tool"})

	provider := &blankStreamToolProvider{result: ProviderResult{
		Text:         "Here is the synthesized answer.",
		HistoryDelta: []ConversationMessage{{Role: "assistant", Content: "Here is the synthesized answer."}},
	}}
	rt, _ := NewProviderRuntime(provider, tools)
	var chunks []string
	result, err := rt.ProcessTurnStreaming(context.Background(), Turn{UserText: "hello"}, func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.generateCalls != 1 {
		t.Fatalf("Generate calls = %d, want 1", provider.generateCalls)
	}
	if result.Text != "Here is the synthesized answer." {
		t.Fatalf("expected synthesized answer, got %q", result.Text)
	}
	if strings.Contains(result.Text, "[search]") {
		t.Fatalf("did not expect raw tool-log fallback text, got %q", result.Text)
	}
	if len(result.ToolTraces) != 0 {
		t.Fatalf("streamed tool calls should not be executed when falling back, got %#v", result.ToolTraces)
	}
	if len(chunks) != 0 {
		t.Fatalf("no round-1 text should have been streamed, got %#v", chunks)
	}
	if len(result.HistoryDelta) != 1 || result.HistoryDelta[0].Content != "Here is the synthesized answer." {
		t.Fatalf("expected Generate history delta only, got %#v", result.HistoryDelta)
	}
}

func TestProviderRuntime_ProcessTurnStreaming_BlankTextToolCalls_NoExecutor(t *testing.T) {
	// With no tool executor the agentic fallback cannot run, so the buildResult
	// safety net still provides a degraded summary rather than no output.
	provider := &blankStreamToolProvider{result: ProviderResult{Text: "unused"}}
	rt, _ := NewProviderRuntime(provider, nil)
	result, err := rt.ProcessTurnStreaming(context.Background(), Turn{UserText: "hello"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.generateCalls != 0 {
		t.Fatalf("Generate calls = %d, want 0 when no executor is available", provider.generateCalls)
	}
	if !strings.Contains(result.Text, "search") {
		t.Fatalf("expected degraded tool summary, got %q", result.Text)
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

func TestIsEchoRuntime(t *testing.T) {
	echoRT, _ := NewProviderRuntime(EchoProvider{}, nil)
	if !IsEchoRuntime(echoRT) {
		t.Fatal("expected EchoProvider runtime to be detected")
	}

	realRT, _ := NewProviderRuntime(&OpenAIChatProvider{Model: "gpt-4o"}, nil)
	if IsEchoRuntime(realRT) {
		t.Fatal("expected non-echo runtime to return false")
	}

	// nil runtime
	if IsEchoRuntime(nil) {
		t.Fatal("nil runtime should return false")
	}
}

type toolOnlyProviderNamed struct{ name string }

func (p *toolOnlyProviderNamed) Generate(_ context.Context, _ Turn) (ProviderResult, error) {
	return ProviderResult{
		ToolCalls: []ToolCall{{ID: "tc1", Name: p.name, Args: map[string]any{}}},
	}, nil
}
