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

func (p runtimeEventTestProvider) Stream(_ context.Context, turn Turn, onChunk func(string)) (ProviderResult, error) {
	if onChunk != nil {
		onChunk("hel")
		onChunk("lo")
	}
	if len(p.result.ToolCalls) > 0 {
		emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{Type: RuntimeEventAssistantToolCallDelta, SessionID: turn.SessionID, TurnID: turn.TurnID, ContentBlockIndex: 1, ToolCallID: p.result.ToolCalls[0].ID, ToolName: p.result.ToolCalls[0].Name, Delta: `{"q":`})
		emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{Type: RuntimeEventAssistantToolCallDelta, SessionID: turn.SessionID, TurnID: turn.TurnID, ContentBlockIndex: 1, ToolCallID: p.result.ToolCalls[0].ID, ToolName: p.result.ToolCalls[0].Name, Delta: `"test"}`})
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
	wantTypes := []RuntimeEventType{RuntimeEventStreamStart, RuntimeEventAssistantDelta, RuntimeEventAssistantDelta, RuntimeEventUsage, RuntimeEventAssistantMessage, RuntimeEventStreamEnd}
	if len(events) != len(wantTypes) {
		t.Fatalf("events len = %d, want %d: %#v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event[%d].Type = %q, want %q", i, events[i].Type, want)
		}
	}
	if events[1].Delta != "hel" || events[2].Delta != "lo" {
		t.Fatalf("delta events = %#v", events[1:3])
	}
	if events[4].Message == nil || events[4].Message.Content != "hello" {
		t.Fatalf("assistant message event = %#v", events[4])
	}
}

func TestRuntimeEventSinkEmitsTypedAssistantToolCallEventsInOrder(t *testing.T) {
	runtime, err := NewProviderRuntime(runtimeEventTestProvider{result: ProviderResult{
		Text:      "checking",
		ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup", Args: map[string]any{"q": "test"}}},
	}}, runtimeEventTestExecutor{})
	if err != nil {
		t.Fatalf("NewProviderRuntime: %v", err)
	}

	var events []RuntimeEvent
	_, err = runtime.ProcessTurnStreaming(context.Background(), Turn{
		SessionID: "sess-3",
		TurnID:    "turn-3",
		UserText:  "hi",
		RuntimeEventSink: func(evt RuntimeEvent) {
			events = append(events, evt)
		},
	}, nil)
	if err != nil {
		t.Fatalf("ProcessTurnStreaming: %v", err)
	}

	wantTypes := []RuntimeEventType{
		RuntimeEventStreamStart,
		RuntimeEventAssistantDelta,
		RuntimeEventAssistantDelta,
		RuntimeEventAssistantToolCallDelta,
		RuntimeEventAssistantToolCallDelta,
		RuntimeEventToolStart,
		RuntimeEventToolResult,
		RuntimeEventAssistantMessage,
		RuntimeEventStreamEnd,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("events len = %d, want %d: %#v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event[%d].Type = %q, want %q", i, events[i].Type, want)
		}
	}
	if events[3].ContentBlockIndex != 1 || events[3].ToolCallID != "call-1" || events[3].ToolName != "lookup" || events[3].Delta == "" {
		t.Fatalf("tool-call delta missing details: %#v", events[3])
	}
	if events[7].Message == nil || len(events[7].Message.ToolCalls) != 1 || events[7].Message.ToolCalls[0].Name != "lookup" {
		t.Fatalf("assistant terminal message = %#v", events[7])
	}
}
