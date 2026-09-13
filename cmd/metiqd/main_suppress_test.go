package main

import (
	"testing"

	"metiq/internal/agent"
)

func TestSuppressStreamEnd(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		result := suppressStreamEnd(nil)
		if result != nil {
			t.Error("expected nil result for nil input")
		}
	})

	t.Run("suppresses stream end events", func(t *testing.T) {
		var received []agent.RuntimeEvent
		sink := func(evt agent.RuntimeEvent) {
			received = append(received, evt)
		}

		wrapped := suppressStreamEnd(sink)

		wrapped(agent.RuntimeEvent{Type: agent.RuntimeEventAssistantDelta, Delta: "hello"})
		wrapped(agent.RuntimeEvent{Type: agent.RuntimeEventStreamEnd})
		wrapped(agent.RuntimeEvent{Type: agent.RuntimeEventAssistantDelta, Delta: " world"})

		if len(received) != 2 {
			t.Fatalf("expected 2 events, got %d", len(received))
		}
		if received[0].Delta != "hello" {
			t.Errorf("expected first delta 'hello', got %q", received[0].Delta)
		}
		if received[1].Delta != " world" {
			t.Errorf("expected second delta ' world', got %q", received[1].Delta)
		}
	})

	t.Run("passes through non-stream-end events", func(t *testing.T) {
		types := []agent.RuntimeEventType{
			agent.RuntimeEventAssistantDelta,
			agent.RuntimeEventAssistantMessage,
			agent.RuntimeEventToolResult,
			agent.RuntimeEventToolError,
			agent.RuntimeEventUsage,
		}

		var received int
		sink := func(evt agent.RuntimeEvent) {
			received++
		}
		wrapped := suppressStreamEnd(sink)

		for _, typ := range types {
			wrapped(agent.RuntimeEvent{Type: typ})
		}

		if received != len(types) {
			t.Errorf("expected %d events, got %d", len(types), received)
		}
	})
}