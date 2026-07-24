package agent

import (
	"context"
	"errors"
	"testing"
)

type dualStreamTestProvider struct {
	typedCalls  int
	legacyCalls int
	fail        error
}

func (p *dualStreamTestProvider) Generate(context.Context, Turn) (ProviderResult, error) {
	return ProviderResult{Text: "buffered"}, nil
}

func (p *dualStreamTestProvider) Stream(context.Context, Turn, func(string)) (ProviderResult, error) {
	p.legacyCalls++
	return ProviderResult{Text: "legacy"}, nil
}

func (p *dualStreamTestProvider) StreamEvents(_ context.Context, _ Turn, emit ProviderStreamEventSink) (ProviderResult, error) {
	p.typedCalls++
	return runProviderEventStream(emit, func(emit ProviderStreamEventSink) (ProviderResult, error) {
		if p.fail != nil {
			return ProviderResult{}, p.fail
		}
		emit(ProviderStreamEvent{Type: ProviderStreamTextDelta, TextDelta: "hel"})
		emit(ProviderStreamEvent{Type: ProviderStreamThinkingDelta, ThinkingDelta: "plan"})
		emit(ProviderStreamEvent{Type: ProviderStreamToolCallDelta, ToolCallDelta: ProviderToolCallDelta{Index: 2, ID: "tc", Name: "lookup", ArgumentsDelta: `{"q":`}})
		emit(ProviderStreamEvent{Type: ProviderStreamUsage, Usage: ProviderUsage{InputTokens: 3, OutputTokens: 4}})
		emit(ProviderStreamEvent{Type: ProviderStreamTextDelta, TextDelta: "lo"})
		return ProviderResult{Text: "hello", Usage: ProviderUsage{InputTokens: 3, OutputTokens: 4}}, nil
	})
}

func TestProviderRuntimePrefersTypedStreamAndDeduplicatesUsage(t *testing.T) {
	provider := &dualStreamTestProvider{}
	runtime, err := NewProviderRuntime(provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	var chunks []string
	var events []RuntimeEvent
	result, err := runtime.ProcessTurnStreaming(context.Background(), Turn{UserText: "hi", SessionID: "s", TurnID: "t", RuntimeEventSink: func(evt RuntimeEvent) {
		events = append(events, evt)
	}}, func(chunk string) { chunks = append(chunks, chunk) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello" || provider.typedCalls != 1 || provider.legacyCalls != 0 {
		t.Fatalf("result=%#v typed=%d legacy=%d", result, provider.typedCalls, provider.legacyCalls)
	}
	if len(chunks) != 2 || chunks[0] != "hel" || chunks[1] != "lo" {
		t.Fatalf("chunks=%#v", chunks)
	}
	var usageCount int
	for _, evt := range events {
		if evt.Type == RuntimeEventUsage {
			usageCount++
		}
	}
	if usageCount != 1 {
		t.Fatalf("usage events=%d all=%#v", usageCount, events)
	}
	want := []RuntimeEventType{RuntimeEventStreamStart, RuntimeEventAssistantDelta, RuntimeEventThinkingDelta, RuntimeEventAssistantToolCallDelta, RuntimeEventUsage, RuntimeEventAssistantDelta, RuntimeEventAssistantMessage, RuntimeEventStreamEnd}
	if len(events) != len(want) {
		t.Fatalf("events=%#v", events)
	}
	for i := range want {
		if events[i].Type != want[i] {
			t.Fatalf("event[%d]=%q want %q", i, events[i].Type, want[i])
		}
	}
}

func TestProviderRuntimeTypedStreamErrorHasNoEnd(t *testing.T) {
	wantErr := errors.New("stream broke")
	provider := &dualStreamTestProvider{fail: wantErr}
	runtime, _ := NewProviderRuntime(provider, nil)
	var events []RuntimeEvent
	_, err := runtime.ProcessTurnStreaming(context.Background(), Turn{UserText: "hi", RuntimeEventSink: func(evt RuntimeEvent) { events = append(events, evt) }}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	if len(events) != 2 || events[0].Type != RuntimeEventStreamStart || events[1].Type != RuntimeEventStreamError || events[1].Error != wantErr.Error() {
		t.Fatalf("events=%#v", events)
	}
}

type leakedToolStreamProvider struct{}

func (leakedToolStreamProvider) Generate(context.Context, Turn) (ProviderResult, error) {
	return ProviderResult{}, nil
}

func (leakedToolStreamProvider) StreamEvents(_ context.Context, _ Turn, emit ProviderStreamEventSink) (ProviderResult, error) {
	raw := `{"name":"lookup","arguments":{"q":"x"}}`
	return runProviderEventStream(emit, func(emit ProviderStreamEventSink) (ProviderResult, error) {
		emit(ProviderStreamEvent{Type: ProviderStreamTextDelta, TextDelta: raw[:12]})
		emit(ProviderStreamEvent{Type: ProviderStreamTextDelta, TextDelta: raw[12:]})
		return ProviderResult{Text: raw}, nil
	})
}

func TestProviderRuntimeRepairsLeakedToolCallStreamIncrementally(t *testing.T) {
	runtime, _ := NewProviderRuntime(leakedToolStreamProvider{}, runtimeEventTestExecutor{})
	var chunks []string
	var events []RuntimeEvent
	result, err := runtime.ProcessTurnStreaming(context.Background(), Turn{UserText: "hi", Tools: []ToolDefinition{{Name: "lookup"}}, RuntimeEventSink: func(evt RuntimeEvent) { events = append(events, evt) }}, func(chunk string) { chunks = append(chunks, chunk) })
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 || len(result.ToolTraces) != 1 || result.ToolTraces[0].Call.Name != "lookup" {
		t.Fatalf("chunks=%#v result=%#v", chunks, result)
	}
	var toolDelta, assistantLeak bool
	for _, evt := range events {
		toolDelta = toolDelta || evt.Type == RuntimeEventAssistantToolCallDelta && evt.ToolName == "lookup"
		assistantLeak = assistantLeak || evt.Type == RuntimeEventAssistantDelta && evt.Delta != ""
	}
	if !toolDelta || assistantLeak {
		t.Fatalf("events=%#v", events)
	}
}

func TestRunProviderEventStreamTerminalContract(t *testing.T) {
	var types []ProviderStreamEventType
	_, err := runProviderEventStream(func(evt ProviderStreamEvent) { types = append(types, evt.Type) }, func(emit ProviderStreamEventSink) (ProviderResult, error) {
		emit(ProviderStreamEvent{Type: ProviderStreamTextDelta, TextDelta: "x"})
		return ProviderResult{Text: "x"}, nil
	})
	if err != nil || len(types) != 3 || types[0] != ProviderStreamStart || types[1] != ProviderStreamTextDelta || types[2] != ProviderStreamEnd {
		t.Fatalf("types=%#v err=%v", types, err)
	}
}
