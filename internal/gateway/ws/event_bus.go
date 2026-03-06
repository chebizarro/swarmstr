// Package ws – WebSocket gateway event bus.
//
// EventEmitter is a thin abstraction over ws.Runtime.Broadcast that lets the
// rest of the application push typed events to connected clients without
// importing the full ws package.  This keeps the event-emission call sites
// clean and testable.
package ws

import "time"

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

	// EventChatMessage is emitted when a DM is received or a reply is sent.
	EventChatMessage = "chat.message"

	// EventCronTick is emitted when a cron job fires.
	EventCronTick = "cron.tick"
	// EventCronResult is emitted when a cron job completes.
	EventCronResult = "cron.result"

	// EventConfigUpdated is emitted when the live config is reloaded.
	EventConfigUpdated = "config.updated"

	// EventPluginLoaded is emitted when a Goja plugin is loaded or reloaded.
	EventPluginLoaded = "plugin.loaded"
)

// AllPushEvents is the canonical ordered list of events the server may push.
// Clients use this list to discover subscribable events.
var AllPushEvents = []string{
	EventTick,
	EventHealth,
	EventShutdown,
	EventAgentStatus,
	EventAgentThinking,
	EventChatMessage,
	EventCronTick,
	EventCronResult,
	EventConfigUpdated,
	EventPluginLoaded,
	// Presence events are also emitted by the ws runtime itself.
	"presence.updated",
	"connect.challenge",
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

// ─── NoopEmitter ──────────────────────────────────────────────────────────────

// NoopEmitter discards all events.  Used when the WS gateway is disabled.
type NoopEmitter struct{}

func (NoopEmitter) Emit(_ string, _ any) {}

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
	TS       int64  `json:"ts_ms"`
	AgentID  string `json:"agent_id"`
	Status   string `json:"status"` // "idle" | "thinking" | "error"
	Session  string `json:"session,omitempty"`
}

// ChatMessagePayload is the payload for EventChatMessage events.
type ChatMessagePayload struct {
	TS        int64  `json:"ts_ms"`
	AgentID   string `json:"agent_id,omitempty"`
	SessionID string `json:"session_id"`
	Direction string `json:"direction"` // "inbound" | "outbound"
	Text      string `json:"text,omitempty"`
	EventID   string `json:"event_id,omitempty"`
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
	TS        int64  `json:"ts_ms"`
	AgentID   string `json:"agent_id,omitempty"`
	JobID     string `json:"job_id"`
	Succeeded bool   `json:"succeeded"`
	DurationMS int64 `json:"duration_ms,omitempty"`
}

// ConfigUpdatedPayload is the payload for EventConfigUpdated events.
type ConfigUpdatedPayload struct {
	TS int64 `json:"ts_ms"`
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
