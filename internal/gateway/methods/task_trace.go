package methods

import (
	"sort"
	"strings"

	"metiq/internal/planner"
	"metiq/internal/store/state"
)

// ─── tasks.trace ─────────────────────────────────────────────────────────────

// TasksTraceRequest is the input for the tasks.trace method.
type TasksTraceRequest struct {
	TaskID string `json:"task_id"`
	RunID  string `json:"run_id,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func (r TasksTraceRequest) Normalize() (TasksTraceRequest, error) {
	r.TaskID = strings.TrimSpace(r.TaskID)
	if r.TaskID == "" {
		return r, errRequiredField("task_id")
	}
	r.RunID = strings.TrimSpace(r.RunID)
	r.Limit = normalizeLimit(r.Limit, 200, 2000)
	return r, nil
}

func DecodeTasksTraceParams(params []byte) (TasksTraceRequest, error) {
	return decodeMethodParams[TasksTraceRequest](params)
}

// TasksTraceResponse carries the unified, time-ordered trace for a task/run.
type TasksTraceResponse struct {
	TaskID   string       `json:"task_id"`
	RunID    string       `json:"run_id,omitempty"`
	GoalID   string       `json:"goal_id,omitempty"`
	Events   []TraceEvent `json:"events"`
	Truncated bool        `json:"truncated,omitempty"`
}

// ─── Unified trace event ─────────────────────────────────────────────────────

// TraceEventKind classifies what subsystem produced the event.
type TraceEventKind string

const (
	TraceKindTurn         TraceEventKind = "turn"
	TraceKindTool         TraceEventKind = "tool"          // reserved: wired when tool lifecycle events are persisted
	TraceKindMemoryRecall TraceEventKind = "memory_recall"
	TraceKindVerification TraceEventKind = "verification"
	TraceKindDelegation   TraceEventKind = "delegation"
)

// TraceEvent is a single entry in the unified task trace timeline.
// All events share a common envelope; the detail payload is kind-specific.
type TraceEvent struct {
	Kind      TraceEventKind `json:"kind"`
	Timestamp int64          `json:"ts"` // unix millis
	TaskID    string         `json:"task_id,omitempty"`
	RunID     string         `json:"run_id,omitempty"`
	GoalID    string         `json:"goal_id,omitempty"`
	StepID    string         `json:"step_id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Summary   string         `json:"summary"`

	// Kind-specific detail (exactly one is non-nil per event).
	Turn         *TraceTurnDetail         `json:"turn,omitempty"`
	Tool         *TraceToolDetail         `json:"tool,omitempty"`
	MemoryRecall *TraceMemoryRecallDetail `json:"memory_recall,omitempty"`
	Verification *TraceVerificationDetail `json:"verification,omitempty"`
	Delegation   *TraceDelegationDetail   `json:"delegation,omitempty"`
}

// TraceTurnDetail carries turn-level telemetry.
type TraceTurnDetail struct {
	TurnID       string `json:"turn_id,omitempty"`
	Outcome      string `json:"outcome,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	InputTokens  int64  `json:"input_tokens,omitempty"`
	OutputTokens int64  `json:"output_tokens,omitempty"`
	LoopBlocked  bool   `json:"loop_blocked,omitempty"`
	Error        string `json:"error,omitempty"`
}

// TraceToolDetail carries tool lifecycle info.
type TraceToolDetail struct {
	EventType  string `json:"event_type,omitempty"`
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

// TraceMemoryRecallDetail carries memory recall subsystem info.
type TraceMemoryRecallDetail struct {
	Strategy         string `json:"strategy,omitempty"`
	Scope            string `json:"scope,omitempty"`
	IndexedHits      int    `json:"indexed_hits,omitempty"`
	FileHits         int    `json:"file_hits,omitempty"`
	InjectedAny      bool   `json:"injected_any"`
	IndexedLatencyMS int64  `json:"indexed_latency_ms,omitempty"`
	FileLatencyMS    int64  `json:"file_latency_ms,omitempty"`
}

// TraceVerificationDetail carries verification lifecycle info.
type TraceVerificationDetail struct {
	EventType  string  `json:"event_type"`
	CheckID    string  `json:"check_id,omitempty"`
	CheckType  string  `json:"check_type,omitempty"`
	Status     string  `json:"status,omitempty"`
	GateAction string  `json:"gate_action,omitempty"`
	ReviewerID string  `json:"reviewer_id,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// TraceDelegationDetail carries worker lifecycle info.
type TraceDelegationDetail struct {
	WorkerID    string  `json:"worker_id"`
	WorkerState string  `json:"worker_state"`
	Progress    float64 `json:"progress,omitempty"`
	Error       string  `json:"error,omitempty"`
	Message     string  `json:"message,omitempty"`
}

// ─── Trace assembler ─────────────────────────────────────────────────────────

// TraceInput bundles the subsystem telemetry needed to assemble a unified trace.
type TraceInput struct {
	Task              state.TaskSpec
	Runs              []state.TaskRun
	TurnTelemetry      []state.TurnTelemetry
	ToolLifecycle      []state.ToolLifecycleTelemetry
	MemoryRecall       []state.MemoryRecallSample
	VerificationEvents []planner.VerificationEvent
	WorkerEvents       []planner.WorkerEvent
}

// AssembleTaskTrace joins subsystem telemetry into a unified, time-ordered trace.
// If runID is non-empty, only events matching that run are included.
func AssembleTaskTrace(input TraceInput, runID string, limit int) TasksTraceResponse {
	taskID := input.Task.TaskID
	goalID := strings.TrimSpace(input.Task.GoalID)

	var events []TraceEvent

	// Turn telemetry → trace events.
	for _, tt := range input.TurnTelemetry {
		if runID != "" && strings.TrimSpace(tt.RunID) != runID {
			continue
		}
		summary := "turn"
		if tt.Outcome != "" {
			summary = "turn:" + tt.Outcome
		}
		if tt.Error != "" {
			summary = "turn:error"
		}
		events = append(events, TraceEvent{
			Kind:      TraceKindTurn,
			Timestamp: tt.EndedAtMS,
			TaskID:    firstNonEmptyStr(tt.TaskID, taskID),
			RunID:     tt.RunID,
			GoalID:    goalID,
			Summary:   summary,
			Turn: &TraceTurnDetail{
				TurnID:       tt.TurnID,
				Outcome:      tt.Outcome,
				StopReason:   tt.StopReason,
				DurationMS:   tt.DurationMS,
				InputTokens:  tt.InputTokens,
				OutputTokens: tt.OutputTokens,
				LoopBlocked:  tt.LoopBlocked,
				Error:        tt.Error,
			},
		})
	}

	// Tool lifecycle → trace events.
	for _, tl := range input.ToolLifecycle {
		if runID != "" && strings.TrimSpace(tl.RunID) != runID {
			continue
		}
		summary := "tool"
		if name := strings.TrimSpace(tl.ToolName); name != "" {
			summary = "tool:" + name
		}
		if eventType := strings.TrimSpace(tl.Type); eventType != "" {
			summary += ":" + eventType
		}
		if strings.TrimSpace(tl.Error) != "" {
			summary = "tool:error"
		}
		events = append(events, TraceEvent{
			Kind:      TraceKindTool,
			Timestamp: tl.TS,
			TaskID:    firstNonEmptyStr(tl.TaskID, taskID),
			RunID:     tl.RunID,
			GoalID:    goalID,
			StepID:    tl.StepID,
			SessionID: tl.SessionID,
			Summary:   summary,
			Tool: &TraceToolDetail{
				EventType:  tl.Type,
				ToolName:   tl.ToolName,
				ToolCallID: tl.ToolCallID,
				Result:     tl.Result,
				Error:      tl.Error,
			},
		})
	}

	// Memory recall → trace events.
	for _, mr := range input.MemoryRecall {
		if runID != "" && strings.TrimSpace(mr.RunID) != runID {
			continue
		}
		indexedHits := len(mr.IndexedSession) + len(mr.IndexedGlobal)
		fileHits := len(mr.FileSelected)
		summary := "memory_recall"
		if mr.InjectedAny {
			summary = "memory_recall:injected"
		}
		events = append(events, TraceEvent{
			Kind:      TraceKindMemoryRecall,
			Timestamp: mr.RecordedAtMS,
			TaskID:    firstNonEmptyStr(mr.TaskID, taskID),
			RunID:     mr.RunID,
			GoalID:    firstNonEmptyStr(mr.GoalID, goalID),
			Summary:   summary,
			MemoryRecall: &TraceMemoryRecallDetail{
				Strategy:         mr.Strategy,
				Scope:            mr.Scope,
				IndexedHits:      indexedHits,
				FileHits:         fileHits,
				InjectedAny:      mr.InjectedAny,
				IndexedLatencyMS: mr.IndexedLatencyMS,
				FileLatencyMS:    mr.FileLatencyMS,
			},
		})
	}

	// Verification events → trace events.
	for _, ve := range input.VerificationEvents {
		if runID != "" && strings.TrimSpace(ve.RunID) != runID {
			continue
		}
		summary := string(ve.Type)
		events = append(events, TraceEvent{
			Kind:      TraceKindVerification,
			Timestamp: ve.CreatedAt * 1000, // unix seconds → millis
			TaskID:    firstNonEmptyStr(ve.TaskID, taskID),
			RunID:     ve.RunID,
			GoalID:    firstNonEmptyStr(ve.GoalID, goalID),
			StepID:    ve.StepID,
			Summary:   summary,
			Verification: &TraceVerificationDetail{
				EventType:  string(ve.Type),
				CheckID:    ve.CheckID,
				CheckType:  ve.CheckType,
				Status:     ve.Status,
				GateAction: ve.GateAction,
				ReviewerID: ve.ReviewerID,
				Confidence: ve.Confidence,
			},
		})
	}

	// Worker/delegation events → trace events.
	for _, we := range input.WorkerEvents {
		if runID != "" && strings.TrimSpace(we.RunID) != runID {
			continue
		}
		summary := "delegation:" + string(we.State)
		var progress float64
		if we.Progress != nil {
			progress = we.Progress.PercentComplete
		}
		events = append(events, TraceEvent{
			Kind:      TraceKindDelegation,
			Timestamp: we.CreatedAt * 1000, // unix seconds → millis
			TaskID:    firstNonEmptyStr(we.TaskID, taskID),
			RunID:     we.RunID,
			GoalID:    firstNonEmptyStr(we.GoalID, goalID),
			StepID:    we.StepID,
			Summary:   summary,
			Delegation: &TraceDelegationDetail{
				WorkerID:    we.WorkerID,
				WorkerState: string(we.State),
				Progress:    progress,
				Error:       we.Error,
				Message:     we.Message,
			},
		})
	}

	// Sort by timestamp ascending, stable by kind for ties.
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Timestamp != events[j].Timestamp {
			return events[i].Timestamp < events[j].Timestamp
		}
		return kindOrder(events[i].Kind) < kindOrder(events[j].Kind)
	})

	truncated := false
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
		truncated = true
	}

	return TasksTraceResponse{
		TaskID:    taskID,
		RunID:     runID,
		GoalID:    goalID,
		Events:    events,
		Truncated: truncated,
	}
}

func kindOrder(k TraceEventKind) int {
	switch k {
	case TraceKindTurn:
		return 0
	case TraceKindTool:
		return 1
	case TraceKindMemoryRecall:
		return 2
	case TraceKindVerification:
		return 3
	case TraceKindDelegation:
		return 4
	default:
		return 5
	}
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
