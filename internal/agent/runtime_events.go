package agent

import "time"

// RuntimeEventType identifies the canonical event stream emitted by runtime
// operations and projected by gateway SSE/WS transports.
type RuntimeEventType string

const (
	RuntimeEventAssistantDelta RuntimeEventType = "assistant_delta"
	RuntimeEventToolStart      RuntimeEventType = "tool_start"
	RuntimeEventToolProgress   RuntimeEventType = "tool_progress"
	RuntimeEventToolResult     RuntimeEventType = "tool_result"
	RuntimeEventToolError      RuntimeEventType = "tool_error"
	RuntimeEventUsage          RuntimeEventType = "usage"
)

// RuntimeEvent is the provider/tool/session-neutral schema for runtime
// lifecycle events. Type-specific fields are optional so transports can forward
// a single stable envelope without bespoke per-event wrappers.
type RuntimeEvent struct {
	Type       RuntimeEventType `json:"type"`
	TS         int64            `json:"ts_ms"`
	SessionID  string           `json:"session_id,omitempty"`
	TurnID     string           `json:"turn_id,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolName   string           `json:"tool_name,omitempty"`
	Delta      string           `json:"delta,omitempty"`
	Result     string           `json:"result,omitempty"`
	Error      string           `json:"error,omitempty"`
	Usage      TurnUsage        `json:"usage,omitempty"`
	Data       any              `json:"data,omitempty"`
	Trace      TraceContext     `json:"trace,omitempty"`
}

// RuntimeEventSink receives canonical structured runtime lifecycle events.
type RuntimeEventSink func(RuntimeEvent)

func emitRuntimeEvent(sink RuntimeEventSink, evt RuntimeEvent) {
	if sink == nil {
		return
	}
	if evt.TS == 0 {
		evt.TS = time.Now().UnixMilli()
	}
	sink(evt)
}

func runtimeEventTypeFromToolLifecycle(t ToolLifecycleEventType) RuntimeEventType {
	switch t {
	case ToolLifecycleEventStart:
		return RuntimeEventToolStart
	case ToolLifecycleEventProgress:
		return RuntimeEventToolProgress
	case ToolLifecycleEventResult:
		return RuntimeEventToolResult
	case ToolLifecycleEventError:
		return RuntimeEventToolError
	default:
		return RuntimeEventType(t)
	}
}

// RuntimeEventFromToolLifecycle maps the existing tool lifecycle surface onto
// the canonical runtime event stream.
func RuntimeEventFromToolLifecycle(evt ToolLifecycleEvent) RuntimeEvent {
	return RuntimeEvent{
		Type:       runtimeEventTypeFromToolLifecycle(evt.Type),
		TS:         evt.TS,
		SessionID:  evt.SessionID,
		TurnID:     evt.TurnID,
		ToolCallID: evt.ToolCallID,
		ToolName:   evt.ToolName,
		Result:     evt.Result,
		Error:      evt.Error,
		Data:       evt.Data,
		Trace:      evt.Trace,
	}
}

func toolLifecycleSinkForRuntime(toolSink ToolLifecycleSink, runtimeSink RuntimeEventSink) ToolLifecycleSink {
	if toolSink == nil && runtimeSink == nil {
		return nil
	}
	return func(evt ToolLifecycleEvent) {
		emitToolLifecycleEvent(toolSink, evt)
		emitRuntimeEvent(runtimeSink, RuntimeEventFromToolLifecycle(evt))
	}
}

func hasUsage(usage TurnUsage) bool {
	return usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.CacheReadTokens != 0 || usage.CacheCreationTokens != 0
}
