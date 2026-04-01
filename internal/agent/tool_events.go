package agent

// ToolLifecycleEventType classifies runtime tool execution signals. It mirrors
// the canonical src tool execution lifecycle surface: start, progress, result,
// and error updates emitted around a tool call.
type ToolLifecycleEventType string

const (
	ToolLifecycleEventStart    ToolLifecycleEventType = "start"
	ToolLifecycleEventProgress ToolLifecycleEventType = "progress"
	ToolLifecycleEventResult   ToolLifecycleEventType = "result"
	ToolLifecycleEventError    ToolLifecycleEventType = "error"
)

// ToolLifecycleEvent is the runtime-neutral payload emitted from the shared
// agentic loop. metiq maps this onto its WS event bus at the Nostr boundary.
type ToolLifecycleEvent struct {
	Type       ToolLifecycleEventType `json:"type"`
	TS         int64                  `json:"ts_ms"`
	SessionID  string                 `json:"session_id,omitempty"`
	TurnID     string                 `json:"turn_id,omitempty"`
	ToolCallID string                 `json:"tool_call_id"`
	ToolName   string                 `json:"tool_name"`
	Result     string                 `json:"result,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Data       any                    `json:"data,omitempty"`
}

// ToolLifecycleSink receives structured tool lifecycle events from the shared
// loop. Callers may leave this nil when no runtime event projection is needed.
type ToolLifecycleSink func(ToolLifecycleEvent)

func emitToolLifecycleEvent(sink ToolLifecycleSink, evt ToolLifecycleEvent) {
	if sink != nil {
		sink(evt)
	}
}
