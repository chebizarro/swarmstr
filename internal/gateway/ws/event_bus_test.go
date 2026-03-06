package ws

import (
	"sync"
	"testing"
	"time"
)

// captureEmitter records all emitted events for test assertions.
type captureEmitter struct {
	mu     sync.Mutex
	events []emittedEvent
}

type emittedEvent struct {
	Event   string
	Payload any
}

func (c *captureEmitter) Emit(event string, payload any) {
	c.mu.Lock()
	c.events = append(c.events, emittedEvent{Event: event, Payload: payload})
	c.mu.Unlock()
}

func (c *captureEmitter) Last() *emittedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 0 {
		return nil
	}
	e := c.events[len(c.events)-1]
	return &e
}

func (c *captureEmitter) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

func TestNoopEmitter(t *testing.T) {
	var e NoopEmitter
	// Should not panic.
	e.Emit(EventTick, TickPayload{TS: 1})
	e.Emit(EventShutdown, nil)
}

func TestRuntimeEmitter_nilRuntime(t *testing.T) {
	e := &RuntimeEmitter{rt: nil}
	// Should not panic with nil runtime.
	e.Emit(EventTick, nil)
}

func TestAllPushEvents_containsCore(t *testing.T) {
	required := []string{
		EventTick, EventHealth, EventShutdown,
		EventAgentStatus, EventChatMessage,
		EventCronTick, EventConfigUpdated,
		"presence.updated",
	}
	lookup := make(map[string]bool, len(AllPushEvents))
	for _, e := range AllPushEvents {
		lookup[e] = true
	}
	for _, req := range required {
		if !lookup[req] {
			t.Errorf("AllPushEvents missing %q", req)
		}
	}
}

func TestEmitTick(t *testing.T) {
	cap := &captureEmitter{}
	startedAt := time.Now().Add(-5 * time.Second)
	EmitTick(cap, startedAt, "test-v1")
	if cap.Count() != 1 {
		t.Fatalf("expected 1 emitted event, got %d", cap.Count())
	}
	last := cap.Last()
	if last.Event != EventTick {
		t.Errorf("event = %q, want %q", last.Event, EventTick)
	}
	p, ok := last.Payload.(TickPayload)
	if !ok {
		t.Fatalf("payload type = %T, want TickPayload", last.Payload)
	}
	if p.Version != "test-v1" {
		t.Errorf("version = %q, want \"test-v1\"", p.Version)
	}
	if p.UptimeMS < 5000 {
		t.Errorf("uptime_ms = %d, expected >= 5000", p.UptimeMS)
	}
}

func TestCaptureEmitter_multiple(t *testing.T) {
	cap := &captureEmitter{}
	cap.Emit(EventAgentStatus, AgentStatusPayload{AgentID: "a1", Status: "thinking"})
	cap.Emit(EventChatMessage, ChatMessagePayload{SessionID: "s1", Direction: "inbound"})
	cap.Emit(EventCronTick, CronTickPayload{JobID: "j1"})
	if cap.Count() != 3 {
		t.Errorf("expected 3 events, got %d", cap.Count())
	}
	last := cap.Last()
	if last.Event != EventCronTick {
		t.Errorf("last event = %q, want %q", last.Event, EventCronTick)
	}
}
