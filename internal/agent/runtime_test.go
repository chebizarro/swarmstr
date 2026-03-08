package agent

import (
	"context"
	"strings"
	"testing"
)

// streamingEchoProvider is a Provider that also implements StreamingProvider,
// delivering the response word by word.
type streamingEchoProvider struct{}

func (streamingEchoProvider) Generate(_ context.Context, turn Turn) (ProviderResult, error) {
	return ProviderResult{Text: "ack: " + turn.UserText}, nil
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
