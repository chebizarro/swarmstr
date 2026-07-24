// Package ws – WebSocket gateway event bus.
//
// EventEmitter is a thin abstraction over ws.Runtime.Broadcast that lets the
// rest of the application push typed events to connected clients without
// importing the full ws package.  This keeps the event-emission call sites
// clean and testable.
package ws

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─── Event name constants ─────────────────────────────────────────────────────

const (
	// EventTick is emitted on a periodic heartbeat interval.
	EventTick = "tick"
	// EventHealth is emitted when the system health state changes.
	EventHealth = "health"
	// EventShutdown is emitted when the daemon is about to terminate.
	EventShutdown = "shutdown"

	// EventAgentStatus is emitted when an agent's active/idle state changes.
	EventAgentStatus = "agent.status"
	// EventAgentThinking is emitted while the agent LLM call is in flight.
	EventAgentThinking = "agent.thinking"

	// EventChat is the protocol-v4 chat state-union stream.
	EventChat = "chat"
	// EventChatMessage is retained for non-streaming inbound/outbound audit events.
	EventChatMessage = "chat.message"

	// EventCronTick is emitted when a cron job fires.
	EventCronTick = "cron.tick"
	// EventCronResult is emitted when a cron job completes.
	EventCronResult = "cron.result"

	// EventConfigUpdated is emitted when the live config is reloaded.
	EventConfigUpdated = "config.updated"
	// EventMCPLifecycle is emitted when an external MCP server changes runtime state.
	EventMCPLifecycle = "mcp.lifecycle"

	// EventPluginLoaded is emitted when a Goja plugin is loaded or reloaded.
	EventPluginLoaded = "plugin.loaded"

	// EventExecApprovalRequested is emitted when a node requests exec approval.
	EventExecApprovalRequested = "exec.approval.requested"
	// EventExecApprovalResolved is emitted when an exec approval is approved or denied.
	EventExecApprovalResolved = "exec.approval.resolved"

	// EventVoicewake is emitted when the daemon is woken via a voice trigger.
	EventVoicewake = "voice.wake"

	// EventUpdateAvailable is emitted when an OTA update check is triggered.
	EventUpdateAvailable = "update.available"

	// EventChannelMessage is emitted when a message arrives on or is sent to
	// a channel (NIP-29 group or other).
	EventChannelMessage = "channel.message"
	// EventChannelPairingRequested is emitted after a direct sender request is durably recorded.
	EventChannelPairingRequested = "channel.pairing.requested"
	// EventChannelPairingResolved is emitted after the durable account/store transition commits.
	EventChannelPairingResolved = "channel.pairing.resolved"
	// EventRelayHealth is emitted when the relay health monitor probes a relay.
	EventRelayHealth = "relay.health"
	// EventDMHealth is emitted when a DM transport health snapshot changes.
	EventDMHealth = "dm.health"

	// EventNodePairRequested is emitted when a node pair request is received.
	EventNodePairRequested = "node.pair.requested"
	// EventNodePairResolved is emitted when a node pair request is approved or rejected.
	EventNodePairResolved = "node.pair.resolved"
	// EventNodeInvokeProgress delivers accepted invocation chunks in sequence order.
	EventNodeInvokeProgress = "node.invoke.progress"

	// EventDevicePairResolved is emitted when a device pair request is approved or rejected.
	EventDevicePairResolved = "device.pair.resolved"

	// EventTalkMode is emitted when the voice/talk mode changes.
	EventTalkMode = "talk.mode"

	// EventChatChunk is emitted during streaming generation, delivering a single
	// text token or token group as it arrives from the provider.
	EventChatChunk = "chat.chunk"
	// EventThinkingDelta is emitted for provider reasoning/thinking deltas when
	// available separately from user-visible assistant text.
	EventThinkingDelta = "thinking.delta"

	// EventCanvasUpdate is emitted when an agent writes to a named canvas.
	EventCanvasUpdate = "canvas.update"

	// EventToolStart is emitted when the shared loop begins executing a tool call.
	EventToolStart = "tool.start"
	// EventToolProgress is emitted for in-flight tool progress updates.
	EventToolProgress = "tool.progress"
	// EventToolResult is emitted when a tool call completes successfully.
	EventToolResult = "tool.result"
	// EventToolError is emitted when a tool call fails or is blocked.
	EventToolError = "tool.error"
	// EventTurnResult is emitted when a turn completes or fails with its final
	// outcome classification and lightweight runtime telemetry.
	EventTurnResult = "turn.result"

	// OpenClaw compatibility alias events.
	EventCompatAgent = "agent"
	// EventCompatChat is retained as a source-compatible alias. Protocol v4 owns
	// the exact wire contract carried by EventChat.
	EventCompatChat             = EventChat
	EventCompatCron             = "cron"
	EventCompatPresence         = "presence"
	EventCompatHeartbeat        = "heartbeat"
	EventCompatVoicewakeChanged = "voicewake.changed"
)

// AllPushEvents is the canonical ordered list of events the server may push.
// Clients use this list to discover subscribable events.
var AllPushEvents = []string{
	EventTick,
	EventHealth,
	EventShutdown,
	EventAgentStatus,
	EventAgentThinking,
	EventChat,
	EventChatMessage,
	EventCronTick,
	EventCronResult,
	EventConfigUpdated,
	EventMCPLifecycle,
	EventPluginLoaded,
	EventExecApprovalRequested,
	EventExecApprovalResolved,
	EventVoicewake,
	EventUpdateAvailable,
	EventChannelMessage,
	EventChannelPairingRequested,
	EventChannelPairingResolved,
	EventRelayHealth,
	EventDMHealth,
	EventNodePairRequested,
	EventNodePairResolved,
	EventNodeInvokeProgress,
	EventDevicePairResolved,
	EventTalkMode,
	// Presence events are also emitted by the ws runtime itself.
	"presence.updated",
	"connect.challenge",
	EventThinkingDelta,
	EventCanvasUpdate,
	EventToolStart,
	EventToolProgress,
	EventToolResult,
	EventToolError,
	// EventChatChunk and EventTurnResult remain internal telemetry sources; v4
	// clients receive their typed EventChat projection instead.
	// OpenClaw compatibility aliases.
	EventCompatAgent,
	EventCompatCron,
	EventCompatPresence,
	EventCompatHeartbeat,
	EventCompatVoicewakeChanged,
}

// ─── EventEmitter interface ───────────────────────────────────────────────────

// EventEmitter can push a named event with an arbitrary payload to all
// subscribed WebSocket clients.
type EventEmitter interface {
	Emit(event string, payload any)
}

// ─── RuntimeEmitter ───────────────────────────────────────────────────────────

// RuntimeEmitter wraps a *Runtime and implements EventEmitter.
type RuntimeEmitter struct {
	rt *Runtime
}

// NewRuntimeEmitter returns an EventEmitter backed by the given WS runtime.
func NewRuntimeEmitter(rt *Runtime) EventEmitter {
	return &RuntimeEmitter{rt: rt}
}

func (e *RuntimeEmitter) Emit(event string, payload any) {
	if e.rt == nil {
		return
	}
	e.rt.Broadcast(event, payload)
}

func compatibilityEventAliases(event string) []string {
	switch event {
	case EventAgentStatus, EventAgentThinking:
		return []string{EventCompatAgent}
	case EventCronTick, EventCronResult:
		return []string{EventCompatCron}
	case "presence.updated":
		return []string{EventCompatPresence}
	case EventTick:
		return []string{EventCompatHeartbeat}
	case EventVoicewake:
		return []string{EventCompatVoicewakeChanged}
	default:
		return nil
	}
}

type compatibilityProjection struct {
	Event   string
	Payload any
}

func compatibilityEventProjections(event string, payload any) []compatibilityProjection {
	aliases := compatibilityEventAliases(event)
	if len(aliases) == 0 {
		return nil
	}
	out := make([]compatibilityProjection, 0, len(aliases))
	for _, alias := range aliases {
		projected := payload
		switch event {
		case EventAgentStatus:
			if p, ok := payload.(AgentStatusPayload); ok {
				runID := p.Session
				if runID == "" {
					runID = p.AgentID
				}
				projected = map[string]any{
					"runId":      runID,
					"sessionKey": p.Session,
					"seq":        0,
					"stream":     "lifecycle",
					"ts":         p.TS,
					"data": map[string]any{
						"phase": p.Status,
					},
				}
			}
		case EventCronTick:
			if p, ok := payload.(CronTickPayload); ok {
				projected = map[string]any{"action": "triggered", "jobId": p.JobID, "ts": p.TS}
			}
		case EventCronResult:
			if p, ok := payload.(CronResultPayload); ok {
				projected = map[string]any{"action": "finished", "jobId": p.JobID, "succeeded": p.Succeeded, "durationMs": p.DurationMS, "ts": p.TS}
			}
		case EventTick:
			if p, ok := payload.(TickPayload); ok {
				projected = map[string]any{"ts": p.TS, "uptimeMs": p.UptimeMS, "version": p.Version}
			}
		case EventVoicewake:
			if p, ok := payload.(VoicewakePayload); ok {
				triggers := []string{}
				if p.Trigger != "" {
					triggers = append(triggers, p.Trigger)
				}
				projected = map[string]any{"triggers": triggers, "source": p.Source, "ts": p.TS}
			}
		}
		out = append(out, compatibilityProjection{Event: alias, Payload: projected})
	}
	return out
}

// ─── NoopEmitter ──────────────────────────────────────────────────────────────

// NoopEmitter discards all events.  Used when the WS gateway is disabled.
type NoopEmitter struct{}

func (NoopEmitter) Emit(_ string, _ any) {}

// ─── Protocol-v4 chat stream ─────────────────────────────────────────────────

type ChatState string

const (
	ChatStateStatus  ChatState = "status"
	ChatStateDelta   ChatState = "delta"
	ChatStateFinal   ChatState = "final"
	ChatStateAborted ChatState = "aborted"
	ChatStateError   ChatState = "error"
)

type ChatRunStartupPhase string

const (
	ChatPhasePreparingWorkspace      ChatRunStartupPhase = "preparing_workspace"
	ChatPhaseProvisioningEnvironment ChatRunStartupPhase = "provisioning_environment"
	ChatPhasePreparingContext        ChatRunStartupPhase = "preparing_context"
	ChatPhaseStartingModel           ChatRunStartupPhase = "starting_model"
)

type ChatErrorKind string

const (
	ChatErrorRefusal       ChatErrorKind = "refusal"
	ChatErrorTimeout       ChatErrorKind = "timeout"
	ChatErrorRateLimit     ChatErrorKind = "rate_limit"
	ChatErrorContextLength ChatErrorKind = "context_length"
	ChatErrorUnknown       ChatErrorKind = "unknown"
)

// ChatEventBase is shared by every member of the closed protocol-v4 chat union.
type ChatEventBase struct {
	RunID      string `json:"runId"`
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agentId,omitempty"`
	SpawnedBy  string `json:"spawnedBy,omitempty"`
	Seq        int    `json:"seq"`
}

type ChatStatusEvent struct {
	ChatEventBase
	State ChatState           `json:"state"`
	Phase ChatRunStartupPhase `json:"phase"`
}

type ChatDeltaEvent struct {
	ChatEventBase
	State     ChatState `json:"state"`
	Message   any       `json:"message,omitempty"`
	DeltaText string    `json:"deltaText"`
	Replace   bool      `json:"replace,omitempty"`
	Usage     any       `json:"usage,omitempty"`
}

type ChatFinalEvent struct {
	ChatEventBase
	State      ChatState `json:"state"`
	Message    any       `json:"message,omitempty"`
	Usage      any       `json:"usage,omitempty"`
	StopReason string    `json:"stopReason,omitempty"`
	Yielded    bool      `json:"yielded,omitempty"`
}

type ChatAbortedEvent struct {
	ChatEventBase
	State        ChatState `json:"state"`
	Message      any       `json:"message,omitempty"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	StopReason   string    `json:"stopReason,omitempty"`
}

type ChatErrorEvent struct {
	ChatEventBase
	State        ChatState     `json:"state"`
	Message      any           `json:"message,omitempty"`
	ErrorMessage string        `json:"errorMessage,omitempty"`
	ErrorKind    ChatErrorKind `json:"errorKind,omitempty"`
	Usage        any           `json:"usage,omitempty"`
	StopReason   string        `json:"stopReason,omitempty"`
}

var generatedChatRunID uint64

// ChatStream serializes one logical run and guarantees monotonically increasing
// payload sequence numbers. It is safe for provider callbacks to call concurrently.
type ChatStream struct {
	mu         sync.Mutex
	emitter    EventEmitter
	base       ChatEventBase
	terminated bool
}

func NewChatStream(emitter EventEmitter, runID, sessionKey, agentID string) *ChatStream {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		runID = fmt.Sprintf("gateway-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&generatedChatRunID, 1))
	}
	return &ChatStream{emitter: emitter, base: ChatEventBase{RunID: runID, SessionKey: strings.TrimSpace(sessionKey), AgentID: strings.TrimSpace(agentID)}}
}

func (s *ChatStream) RunID() string {
	if s == nil {
		return ""
	}
	return s.base.RunID
}

func (s *ChatStream) nextBaseLocked() ChatEventBase {
	base := s.base
	s.base.Seq++
	return base
}

func (s *ChatStream) emit(build func(ChatEventBase) any, terminal bool) {
	if s == nil || s.emitter == nil || strings.TrimSpace(s.base.SessionKey) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminated {
		return
	}
	payload := build(s.nextBaseLocked())
	if terminal {
		s.terminated = true
	}
	s.emitter.Emit(EventChat, payload)
}

func (s *ChatStream) Status(phase ChatRunStartupPhase) {
	s.emit(func(base ChatEventBase) any {
		return ChatStatusEvent{ChatEventBase: base, State: ChatStateStatus, Phase: phase}
	}, false)
}

func (s *ChatStream) Delta(text string, replace bool) {
	s.DeltaWithMetadata(text, replace, nil, nil)
}

// DeltaWithMetadata emits an incremental or authoritative replacement delta.
// Protocol v4 permits message and usage snapshots alongside deltaText so a
// provider-neutral runtime can correct normalized output without a second
// provider-specific transport path.
func (s *ChatStream) DeltaWithMetadata(text string, replace bool, message, usage any) {
	if text == "" && message == nil && usage == nil {
		return
	}
	s.emit(func(base ChatEventBase) any {
		return ChatDeltaEvent{ChatEventBase: base, State: ChatStateDelta, DeltaText: text, Replace: replace, Message: message, Usage: usage}
	}, false)
}

func (s *ChatStream) Final(message, usage any, stopReason string, yielded bool) {
	s.emit(func(base ChatEventBase) any {
		return ChatFinalEvent{ChatEventBase: base, State: ChatStateFinal, Message: message, Usage: usage, StopReason: strings.TrimSpace(stopReason), Yielded: yielded}
	}, true)
}

func (s *ChatStream) Aborted(message any, errorMessage, stopReason string) {
	s.emit(func(base ChatEventBase) any {
		return ChatAbortedEvent{ChatEventBase: base, State: ChatStateAborted, Message: message, ErrorMessage: strings.TrimSpace(errorMessage), StopReason: strings.TrimSpace(stopReason)}
	}, true)
}

func (s *ChatStream) Error(message any, errorMessage string, kind ChatErrorKind, usage any, stopReason string) {
	s.emit(func(base ChatEventBase) any {
		return ChatErrorEvent{ChatEventBase: base, State: ChatStateError, Message: message, ErrorMessage: strings.TrimSpace(errorMessage), ErrorKind: kind, Usage: usage, StopReason: strings.TrimSpace(stopReason)}
	}, true)
}

func ChatAssistantMessage(text string) any {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": text}}, "timestamp": time.Now().UnixMilli()}
}

func ChatUsage(inputTokens, outputTokens int64) any {
	if inputTokens == 0 && outputTokens == 0 {
		return nil
	}
	return map[string]any{"inputTokens": inputTokens, "outputTokens": outputTokens}
}

// ─── Typed payload helpers ────────────────────────────────────────────────────

// TickPayload is the payload for EventTick events.
type TickPayload struct {
	TS       int64  `json:"ts_ms"`
	UptimeMS int64  `json:"uptime_ms"`
	Version  string `json:"version,omitempty"`
}

// HealthPayload is the payload for EventHealth events.
type HealthPayload struct {
	TS   int64          `json:"ts_ms"`
	OK   bool           `json:"ok"`
	Info map[string]any `json:"info,omitempty"`
}

// ShutdownPayload is the payload for EventShutdown events.
type ShutdownPayload struct {
	TS     int64  `json:"ts_ms"`
	Reason string `json:"reason,omitempty"`
}

// AgentStatusPayload is the payload for EventAgentStatus events.
type AgentStatusPayload struct {
	TS             int64  `json:"ts_ms"`
	AgentID        string `json:"agent_id"`
	Status         string `json:"status"` // "idle" | "thinking" | "error" | "busy"
	Session        string `json:"session,omitempty"`
	ActiveRuns     int    `json:"active_runs,omitempty"`
	LastActivityAt int64  `json:"last_activity_at_ms,omitempty"`
}

// ChatMessagePayload is the payload for EventChatMessage events.
type ChatMessagePayload struct {
	TS        int64  `json:"ts_ms"`
	AgentID   string `json:"agent_id,omitempty"`
	SessionID string `json:"session_id"`
	Direction string `json:"direction"` // "inbound" | "outbound"
	Text      string `json:"text,omitempty"`
	EventID   string `json:"event_id,omitempty"`
	// Task correlation — populated when the message is part of a task.
	TaskID string `json:"task_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`
}

// CronTickPayload is the payload for EventCronTick events.
type CronTickPayload struct {
	TS      int64  `json:"ts_ms"`
	AgentID string `json:"agent_id,omitempty"`
	JobID   string `json:"job_id"`
	Name    string `json:"name,omitempty"`
}

// CronResultPayload is the payload for EventCronResult events.
type CronResultPayload struct {
	TS         int64  `json:"ts_ms"`
	AgentID    string `json:"agent_id,omitempty"`
	JobID      string `json:"job_id"`
	Succeeded  bool   `json:"succeeded"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// ConfigUpdatedPayload is the payload for EventConfigUpdated events.
type ConfigUpdatedPayload struct {
	TS int64 `json:"ts_ms"`
}

// MCPLifecyclePayload is the payload for EventMCPLifecycle events.
type MCPLifecyclePayload struct {
	TS                int64           `json:"ts_ms"`
	Name              string          `json:"name"`
	State             string          `json:"state,omitempty"`
	PreviousState     string          `json:"previous_state,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	Removed           bool            `json:"removed,omitempty"`
	Healthy           bool            `json:"healthy,omitempty"`
	Enabled           bool            `json:"enabled,omitempty"`
	RuntimePresent    bool            `json:"runtime_present,omitempty"`
	Source            string          `json:"source,omitempty"`
	Precedence        int             `json:"precedence,omitempty"`
	Signature         string          `json:"signature,omitempty"`
	Transport         string          `json:"transport,omitempty"`
	Command           string          `json:"command,omitempty"`
	URL               string          `json:"url,omitempty"`
	ToolCount         int             `json:"tool_count,omitempty"`
	LastError         string          `json:"last_error,omitempty"`
	ReconnectAttempts int             `json:"reconnect_attempts,omitempty"`
	LastAttemptAtMS   int64           `json:"last_attempt_at_ms,omitempty"`
	LastConnectedAtMS int64           `json:"last_connected_at_ms,omitempty"`
	LastFailedAtMS    int64           `json:"last_failed_at_ms,omitempty"`
	UpdatedAtMS       int64           `json:"updated_at_ms,omitempty"`
	PolicyStatus      string          `json:"policy_status,omitempty"`
	PolicyReason      string          `json:"policy_reason,omitempty"`
	Capabilities      map[string]bool `json:"capabilities,omitempty"`
}

// ExecApprovalRequestedPayload is the payload for EventExecApprovalRequested.
type ExecApprovalRequestedPayload struct {
	TS                   int64    `json:"ts_ms"`
	ID                   string   `json:"id"`
	NodeID               string   `json:"node_id,omitempty"`
	Command              string   `json:"command,omitempty"`
	CommandArgv          []string `json:"command_argv,omitempty"`
	CWD                  *string  `json:"cwd,omitempty"`
	Host                 *string  `json:"host,omitempty"`
	AnalysisWarnings     []string `json:"analysis_warnings,omitempty"`
	AnalysisSummary      string   `json:"analysis_summary,omitempty"`
	AnalysisSignature    string   `json:"analysis_signature,omitempty"`
	AllowAlwaysAvailable bool     `json:"allow_always_available,omitempty"`
	AllowAlwaysReason    string   `json:"allow_always_reason,omitempty"`
	ApprovalMode         string   `json:"approval_mode,omitempty"`
	RequestedAt          int64    `json:"requested_at,omitempty"`
	ExpiresAt            int64    `json:"expires_at,omitempty"`
	TimeoutMS            int      `json:"timeout_ms,omitempty"`
}

// ExecApprovalResolvedPayload is the payload for EventExecApprovalResolved.
type ExecApprovalResolvedPayload struct {
	TS       int64  `json:"ts_ms"`
	ID       string `json:"id"`
	Decision string `json:"decision"` // "approved" | "denied"
	NodeID   string `json:"node_id,omitempty"`
}

// VoicewakePayload is the payload for EventVoicewake.
type VoicewakePayload struct {
	TS      int64  `json:"ts_ms"`
	Trigger string `json:"trigger,omitempty"`
	Source  string `json:"source,omitempty"`
}

// UpdateAvailablePayload is the payload for EventUpdateAvailable.
type UpdateAvailablePayload struct {
	TS      int64  `json:"ts_ms"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`
}

// ChannelMessagePayload is the payload for EventChannelMessage.
type ChannelMessagePayload struct {
	TS        int64  `json:"ts_ms"`
	ChannelID string `json:"channel_id"`
	GroupID   string `json:"group_id,omitempty"`
	Relay     string `json:"relay,omitempty"`
	Direction string `json:"direction"` // "inbound" | "outbound"
	From      string `json:"from,omitempty"`
	Text      string `json:"text,omitempty"`
	EventID   string `json:"event_id,omitempty"`
}

// RelayHealthPayload is the payload for EventRelayHealth.
type RelayHealthPayload struct {
	TS        int64  `json:"ts_ms"`
	URL       string `json:"url"`
	Reachable bool   `json:"reachable"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
	Initial   bool   `json:"initial,omitempty"`
	Source    string `json:"source,omitempty"`
}

// DMHealthPayload is the payload for EventDMHealth.
type DMHealthPayload struct {
	TS               int64    `json:"ts_ms"`
	Label            string   `json:"label"`
	BoundRelays      []string `json:"bound_relays,omitempty"`
	LastEventAt      int64    `json:"last_event_at_ms,omitempty"`
	LastReconnectAt  int64    `json:"last_reconnect_at_ms,omitempty"`
	LastClosedReason string   `json:"last_closed_reason,omitempty"`
	ReplayWindowMS   int64    `json:"replay_window_ms,omitempty"`
	EventCount       int64    `json:"event_count,omitempty"`
	ReconnectCount   int64    `json:"reconnect_count,omitempty"`
	Healthy          bool     `json:"healthy"`
	Source           string   `json:"source,omitempty"`
}

// PluginLoadedPayload is the payload for EventPluginLoaded.
type PluginLoadedPayload struct {
	TS       int64  `json:"ts_ms"`
	PluginID string `json:"plugin_id"`
	Version  string `json:"version,omitempty"`
	Action   string `json:"action"` // "loaded" | "reloaded" | "installed"
}

// NodePairRequestedPayload is the payload for EventNodePairRequested.
type NodePairRequestedPayload struct {
	TS        int64  `json:"ts_ms"`
	RequestID string `json:"request_id"`
	NodeID    string `json:"node_id,omitempty"`
	Label     string `json:"label,omitempty"`
}

// NodePairResolvedPayload is the payload for EventNodePairResolved.
type NodePairResolvedPayload struct {
	TS        int64  `json:"ts_ms"`
	RequestID string `json:"request_id"`
	NodeID    string `json:"node_id,omitempty"`
	Decision  string `json:"decision"` // "approved" | "rejected"
}

// NodeInvokeProgressPayload is the payload for EventNodeInvokeProgress.
type NodeInvokeProgressPayload struct {
	TS       int64  `json:"ts_ms"`
	InvokeID string `json:"invoke_id"`
	NodeID   string `json:"node_id"`
	Seq      int    `json:"seq"`
	Chunk    string `json:"chunk"`
}

// DevicePairResolvedPayload is the payload for EventDevicePairResolved.
type DevicePairResolvedPayload struct {
	TS       int64  `json:"ts_ms"`
	DeviceID string `json:"device_id,omitempty"`
	Label    string `json:"label,omitempty"`
	Decision string `json:"decision"` // "approved" | "rejected"
}

// ChatChunkPayload is the payload for EventChatChunk events.
// It delivers a single streaming text chunk as it arrives from the LLM.
type ChatChunkPayload struct {
	TS        int64  `json:"ts_ms"`
	AgentID   string `json:"agent_id,omitempty"`
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id,omitempty"`
	Text      string `json:"text"`
	Done      bool   `json:"done,omitempty"` // true on the final chunk
}

// ThinkingDeltaPayload is the payload for EventThinkingDelta events.
type ThinkingDeltaPayload struct {
	TS        int64  `json:"ts_ms"`
	AgentID   string `json:"agent_id,omitempty"`
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id,omitempty"`
	Text      string `json:"text"`
}

// CanvasUpdatePayload is the payload for EventCanvasUpdate events.
type CanvasUpdatePayload struct {
	TS          int64  `json:"ts_ms"`
	CanvasID    string `json:"canvas_id"`
	ContentType string `json:"content_type"`
	Data        string `json:"data"`
}

// ToolLifecyclePayload is the payload for tool lifecycle events.
// It carries the correlation fields needed to inspect shared-loop execution.
type ToolLifecyclePayload struct {
	TS         int64  `json:"ts_ms"`
	AgentID    string `json:"agent_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
	Data       any    `json:"data,omitempty"`
	// Task correlation — populated when the tool runs inside a task context.
	GoalID       string `json:"goal_id,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	StepID       string `json:"step_id,omitempty"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
	ParentRunID  string `json:"parent_run_id,omitempty"`
}

// ToolDecisionKind classifies projected tool decision payloads carried on
// tool.progress/tool.error lifecycle events.
type ToolDecisionKind string

const (
	ToolDecisionKindScheduler     ToolDecisionKind = "scheduler"
	ToolDecisionKindLoopDetection ToolDecisionKind = "loop_detection"
)

// ToolSchedulerDecisionPayload is the gateway-owned projection of scheduler
// batch decisions emitted by the shared agent loop.
type ToolSchedulerDecisionPayload struct {
	Kind             ToolDecisionKind `json:"kind"`
	Mode             string           `json:"mode"` // "serial" | "parallel"
	BatchIndex       int              `json:"batch_index"`
	BatchCount       int              `json:"batch_count"`
	BatchSize        int              `json:"batch_size"`
	BatchPosition    int              `json:"batch_position"`
	ConcurrencySafe  bool             `json:"concurrency_safe"`
	ConcurrencyLimit int              `json:"concurrency_limit,omitempty"`
}

// ToolLoopDecisionPayload is the gateway-owned projection of loop-detection
// decisions emitted before a tool continues or is blocked.
type ToolLoopDecisionPayload struct {
	Kind           ToolDecisionKind `json:"kind"`
	Blocked        bool             `json:"blocked"`
	Level          string           `json:"level,omitempty"`
	Detector       string           `json:"detector,omitempty"`
	Count          int              `json:"count,omitempty"`
	WarningKey     string           `json:"warning_key,omitempty"`
	PairedToolName string           `json:"paired_tool_name,omitempty"`
	Message        string           `json:"message,omitempty"`
}

// TurnResultPayload is the payload for EventTurnResult events.
type TurnResultPayload struct {
	TS             int64  `json:"ts_ms"`
	AgentID        string `json:"agent_id,omitempty"`
	SessionID      string `json:"session_id"`
	TurnID         string `json:"turn_id,omitempty"`
	StartedAtMS    int64  `json:"started_at_ms,omitempty"`
	EndedAtMS      int64  `json:"ended_at_ms,omitempty"`
	DurationMS     int64  `json:"duration_ms,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
	StopReason     string `json:"stop_reason,omitempty"`
	LoopBlocked    bool   `json:"loop_blocked,omitempty"`
	Error          string `json:"error,omitempty"`
	FallbackUsed   bool   `json:"fallback_used,omitempty"`
	FallbackFrom   string `json:"fallback_from,omitempty"`
	FallbackTo     string `json:"fallback_to,omitempty"`
	FallbackReason string `json:"fallback_reason,omitempty"`
	InputTokens    int64  `json:"input_tokens,omitempty"`
	OutputTokens   int64  `json:"output_tokens,omitempty"`
	// Task correlation — populated when the turn runs inside a task context.
	GoalID       string `json:"goal_id,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	StepID       string `json:"step_id,omitempty"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
	ParentRunID  string `json:"parent_run_id,omitempty"`
}

// TalkModePayload is the payload for EventTalkMode events.
type TalkModePayload struct {
	TS   int64  `json:"ts_ms"`
	Mode string `json:"mode"` // "disabled" | "off" | "push-to-talk" | "always-on" | "hotword"
}

// ─── TickEmitter helper ───────────────────────────────────────────────────────

// EmitTick pushes a tick event with the current timestamp and uptime.
func EmitTick(e EventEmitter, startedAt time.Time, version string) {
	now := time.Now()
	e.Emit(EventTick, TickPayload{
		TS:       now.UnixMilli(),
		UptimeMS: now.Sub(startedAt).Milliseconds(),
		Version:  version,
	})
}
