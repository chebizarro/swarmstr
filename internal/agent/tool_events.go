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
	// Trace carries task/run/step correlation IDs when the tool executes
	// inside a task context. Zero-value when not in a task.
	Trace TraceContext `json:"trace,omitempty"`
}

// ToolDecisionKind identifies the runtime decision source carried in Data.
type ToolDecisionKind string

const (
	ToolDecisionKindScheduler         ToolDecisionKind = "scheduler"
	ToolDecisionKindLoopDetection     ToolDecisionKind = "loop_detection"
	ToolDecisionKindMutationDuplicate ToolDecisionKind = "mutation_duplicate"
)

// ToolSchedulerDecision records how the shared src-shaped scheduler chose to
// run a tool call within the current batch.
type ToolSchedulerDecision struct {
	Kind             ToolDecisionKind `json:"kind"`
	Mode             string           `json:"mode"` // "serial" | "parallel"
	BatchIndex       int              `json:"batch_index"`
	BatchCount       int              `json:"batch_count"`
	BatchSize        int              `json:"batch_size"`
	BatchPosition    int              `json:"batch_position"`
	ConcurrencySafe  bool             `json:"concurrency_safe"`
	ConcurrencyLimit int              `json:"concurrency_limit,omitempty"`
}

// ToolLoopDecision records a loop-detector decision before execution continues
// or is blocked.
type ToolLoopDecision struct {
	Kind           ToolDecisionKind `json:"kind"`
	Blocked        bool             `json:"blocked"`
	Level          string           `json:"level,omitempty"`
	Detector       string           `json:"detector,omitempty"`
	Count          int              `json:"count,omitempty"`
	WarningKey     string           `json:"warning_key,omitempty"`
	PairedToolName string           `json:"paired_tool_name,omitempty"`
	Message        string           `json:"message,omitempty"`
}

// ToolMutationDecision records duplicate mutating tool-call protection. A
// blocked duplicate is surfaced as lifecycle progress plus an error event and
// is not executed again.
type ToolMutationDecision struct {
	Kind        ToolDecisionKind `json:"kind"`
	Blocked     bool             `json:"blocked"`
	Fingerprint string           `json:"fingerprint,omitempty"`
	Count       int              `json:"count,omitempty"`
	Message     string           `json:"message,omitempty"`
}

// ToolLifecycleSink receives structured tool lifecycle events from the shared
// loop. Callers may leave this nil when no runtime event projection is needed.
type ToolLifecycleSink func(ToolLifecycleEvent)

func emitToolLifecycleEvent(sink ToolLifecycleSink, evt ToolLifecycleEvent) {
	if sink != nil {
		sink(evt)
	}
}
