package agent

import (
	"context"
	"testing"
)

type runtimeEventTestProvider struct {
	result ProviderResult
}

func (p runtimeEventTestProvider) Generate(context.Context, Turn) (ProviderResult, error) {
	return p.result, nil
}

func (p runtimeEventTestProvider) Stream(_ context.Context, _ Turn, onChunk func(string)) (ProviderResult, error) {
	if onChunk != nil {
		onChunk("hel")
		onChunk("lo")
	}
	return p.result, nil
}

type runtimeEventTestExecutor struct{}

func (runtimeEventTestExecutor) Execute(context.Context, ToolCall) (string, error) {
	return "tool output", nil
}

func TestRuntimeEventSinkMapsToolLifecycleAndUsage(t *testing.T) {
	runtime, err := NewProviderRuntime(runtimeEventTestProvider{result: ProviderResult{
		Text:      "done",
		ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup"}},
		Usage:     ProviderUsage{InputTokens: 3, OutputTokens: 5},
	}}, runtimeEventTestExecutor{})
	if err != nil {
		t.Fatalf("NewProviderRuntime: %v", err)
	}

	var events []RuntimeEvent
	_, err = runtime.ProcessTurn(context.Background(), Turn{
		SessionID: "sess-1",
		TurnID:    "turn-1",
		UserText:  "hi",
		RuntimeEventSink: func(evt RuntimeEvent) {
			events = append(events, evt)
		},
	})
	if err != nil {
		t.Fatalf("ProcessTurn: %v", err)
	}

	wantTypes := []RuntimeEventType{RuntimeEventToolStart, RuntimeEventToolResult, RuntimeEventUsage}
	if len(events) != len(wantTypes) {
		t.Fatalf("events len = %d, want %d: %#v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event[%d].Type = %q, want %q", i, events[i].Type, want)
		}
		if events[i].SessionID != "sess-1" || events[i].TurnID != "turn-1" {
			t.Fatalf("event[%d] correlation = %q/%q", i, events[i].SessionID, events[i].TurnID)
		}
	}
	if events[0].ToolCallID != "call-1" || events[0].ToolName != "lookup" {
		t.Fatalf("tool start event missing tool identity: %#v", events[0])
	}
	if events[1].Result != "tool output" {
		t.Fatalf("tool result = %q", events[1].Result)
	}
	if events[2].Usage.InputTokens != 3 || events[2].Usage.OutputTokens != 5 {
		t.Fatalf("usage event = %#v", events[2].Usage)
	}
}

func TestRuntimeEventSinkEmitsAssistantDeltaForStreaming(t *testing.T) {
	runtime, err := NewProviderRuntime(runtimeEventTestProvider{result: ProviderResult{
		Text:  "hello",
		Usage: ProviderUsage{InputTokens: 1, OutputTokens: 2},
	}}, nil)
	if err != nil {
		t.Fatalf("NewProviderRuntime: %v", err)
	}

	var chunks []string
	var events []RuntimeEvent
	_, err = runtime.ProcessTurnStreaming(context.Background(), Turn{
		SessionID: "sess-2",
		TurnID:    "turn-2",
		UserText:  "hi",
		RuntimeEventSink: func(evt RuntimeEvent) {
			events = append(events, evt)
		},
	}, func(text string) {
		chunks = append(chunks, text)
	})
	if err != nil {
		t.Fatalf("ProcessTurnStreaming: %v", err)
	}
	if len(chunks) != 2 || chunks[0] != "hel" || chunks[1] != "lo" {
		t.Fatalf("chunks = %#v", chunks)
	}
	wantTypes := []RuntimeEventType{RuntimeEventAssistantDelta, RuntimeEventAssistantDelta, RuntimeEventUsage}
	if len(events) != len(wantTypes) {
		t.Fatalf("events len = %d, want %d: %#v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event[%d].Type = %q, want %q", i, events[i].Type, want)
		}
	}
	if events[0].Delta != "hel" || events[1].Delta != "lo" {
		t.Fatalf("delta events = %#v", events[:2])
	}
}
