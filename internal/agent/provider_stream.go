package agent

import (
	"context"
	"errors"
)

// ProviderStreamEventType identifies a provider-neutral streaming event.
type ProviderStreamEventType string

const (
	ProviderStreamStart         ProviderStreamEventType = "start"
	ProviderStreamTextDelta     ProviderStreamEventType = "text_delta"
	ProviderStreamThinkingDelta ProviderStreamEventType = "thinking_delta"
	ProviderStreamToolCallDelta ProviderStreamEventType = "tool_call_delta"
	ProviderStreamUsage         ProviderStreamEventType = "usage"
	ProviderStreamEnd           ProviderStreamEventType = "end"
	ProviderStreamError         ProviderStreamEventType = "error"
)

// ProviderToolCallDelta is an incremental provider tool-call fragment. ArgumentsDelta
// is the wire-order JSON fragment; ID and Name carry the latest known identity.
type ProviderToolCallDelta struct {
	Index          int    `json:"index"`
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
}

// ProviderStreamEvent is the canonical provider-neutral inference stream.
// Exactly one Start precedes data events. A successful stream ends with End;
// a failed stream ends with Error and never emits End.
type ProviderStreamEvent struct {
	Type          ProviderStreamEventType `json:"type"`
	TextDelta     string                  `json:"text_delta,omitempty"`
	ThinkingDelta string                  `json:"thinking_delta,omitempty"`
	ToolCallDelta ProviderToolCallDelta   `json:"tool_call_delta,omitempty"`
	Usage         ProviderUsage           `json:"usage,omitempty"`
	Err           error                   `json:"-"`
}

// ProviderStreamEventSink synchronously consumes provider events in wire order.
type ProviderStreamEventSink func(ProviderStreamEvent)

// EventStreamingProvider extends Provider with typed structured streaming.
// Runtime implementations prefer this interface over legacy StreamingProvider.
type EventStreamingProvider interface {
	Provider
	StreamEvents(context.Context, Turn, ProviderStreamEventSink) (ProviderResult, error)
}

// runProviderEventStream centralizes the terminal event contract for providers.
func runProviderEventStream(emit ProviderStreamEventSink, run func(ProviderStreamEventSink) (ProviderResult, error)) (result ProviderResult, err error) {
	if emit != nil {
		emit(ProviderStreamEvent{Type: ProviderStreamStart})
	}
	result, err = run(emit)
	if err != nil {
		if emit != nil {
			emit(ProviderStreamEvent{Type: ProviderStreamError, Err: err})
		}
		return result, err
	}
	if emit != nil {
		emit(ProviderStreamEvent{Type: ProviderStreamEnd})
	}
	return result, nil
}

// streamEventsAsLegacy preserves the historical text callback and RuntimeEventSink
// surface for direct StreamingProvider callers. Provider lifecycle events remain
// internal to the typed contract; runtime owns public stream lifecycle events.
func legacyStreamAsEvents(ctx context.Context, turn Turn, emit ProviderStreamEventSink, stream func(context.Context, Turn, func(string)) (ProviderResult, error)) (ProviderResult, error) {
	return runProviderEventStream(emit, func(emit ProviderStreamEventSink) (ProviderResult, error) {
		streamTurn := turn
		var lastUsage ProviderUsage
		streamTurn.RuntimeEventSink = func(evt RuntimeEvent) {
			if emit == nil {
				return
			}
			switch evt.Type {
			case RuntimeEventThinkingDelta:
				emit(ProviderStreamEvent{Type: ProviderStreamThinkingDelta, ThinkingDelta: evt.Delta})
			case RuntimeEventAssistantToolCallDelta:
				emit(ProviderStreamEvent{Type: ProviderStreamToolCallDelta, ToolCallDelta: ProviderToolCallDelta{Index: evt.ContentBlockIndex, ID: evt.ToolCallID, Name: evt.ToolName, ArgumentsDelta: evt.Delta}})
			case RuntimeEventUsage:
				lastUsage = ProviderUsage{InputTokens: evt.Usage.InputTokens, OutputTokens: evt.Usage.OutputTokens, CacheReadTokens: evt.Usage.CacheReadTokens, CacheCreationTokens: evt.Usage.CacheCreationTokens}
				if hasProviderUsage(lastUsage) {
					emit(ProviderStreamEvent{Type: ProviderStreamUsage, Usage: lastUsage})
				}
			}
		}
		result, err := stream(ctx, streamTurn, func(text string) {
			if emit != nil && text != "" {
				emit(ProviderStreamEvent{Type: ProviderStreamTextDelta, TextDelta: text})
			}
		})
		if err == nil && emit != nil && hasProviderUsage(result.Usage) && !providerUsageEqual(lastUsage, result.Usage) {
			emit(ProviderStreamEvent{Type: ProviderStreamUsage, Usage: result.Usage})
		}
		return result, err
	})
}

func streamEventsAsLegacy(ctx context.Context, turn Turn, onChunk func(string), provider EventStreamingProvider) (ProviderResult, error) {
	if provider == nil {
		return ProviderResult{}, errors.New("event streaming provider is required")
	}
	return provider.StreamEvents(ctx, turn, func(evt ProviderStreamEvent) {
		switch evt.Type {
		case ProviderStreamTextDelta:
			if onChunk != nil && evt.TextDelta != "" {
				onChunk(evt.TextDelta)
			}
		case ProviderStreamThinkingDelta:
			emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{
				Type: RuntimeEventThinkingDelta, SessionID: turn.SessionID, TurnID: turn.TurnID,
				Delta: evt.ThinkingDelta, Trace: turn.Trace,
			})
		case ProviderStreamToolCallDelta:
			d := evt.ToolCallDelta
			emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{
				Type: RuntimeEventAssistantToolCallDelta, SessionID: turn.SessionID, TurnID: turn.TurnID,
				ContentBlockIndex: d.Index, ToolCallID: d.ID, ToolName: d.Name, Delta: d.ArgumentsDelta, Trace: turn.Trace,
			})
		case ProviderStreamUsage:
			emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{
				Type: RuntimeEventUsage, SessionID: turn.SessionID, TurnID: turn.TurnID,
				Usage: providerUsageToTurnUsage(evt.Usage), Trace: turn.Trace,
			})
		}
	})
}

func providerUsageToTurnUsage(usage ProviderUsage) TurnUsage {
	return TurnUsage{
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		CacheReadTokens: usage.CacheReadTokens, CacheCreationTokens: usage.CacheCreationTokens,
	}
}

func providerUsageEqual(a, b ProviderUsage) bool {
	return a.InputTokens == b.InputTokens && a.OutputTokens == b.OutputTokens &&
		a.CacheReadTokens == b.CacheReadTokens && a.CacheCreationTokens == b.CacheCreationTokens
}

func hasProviderUsage(usage ProviderUsage) bool {
	return usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.CacheReadTokens != 0 || usage.CacheCreationTokens != 0
}
