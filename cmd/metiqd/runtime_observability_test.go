package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent/toolbuiltin"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
)

func TestRuntimeEventBufferTailFiltersStructuredEvents(t *testing.T) {
	buf := newRuntimeEventBuffer(8)
	buf.Append(gatewayws.EventToolStart, gatewayws.ToolLifecyclePayload{
		TS:         10,
		AgentID:    "alpha",
		SessionID:  "sess-1",
		ToolCallID: "call-1",
		ToolName:   "fetch",
	})
	buf.Append(gatewayws.EventChatMessage, gatewayws.ChatMessagePayload{
		TS:        11,
		AgentID:   "beta",
		SessionID: "sess-2",
		Direction: "inbound",
		Text:      "hi",
	})
	buf.Append(gatewayws.EventRelayHealth, gatewayws.RelayHealthPayload{
		TS:        12,
		URL:       "wss://relay.example",
		Reachable: true,
		Source:    "relay-monitor",
	})
	buf.Append(gatewayws.EventMCPLifecycle, gatewayws.MCPLifecyclePayload{
		TS:        13,
		Name:      "demo",
		State:     "connected",
		ToolCount: 2,
	})

	got := buf.Tail(0, 10, 32*1024, toolbuiltin.RuntimeObserveFilters{
		Events:    []string{gatewayws.EventToolStart},
		AgentID:   "alpha",
		SessionID: "sess-1",
	})
	events, ok := got["events"].([]map[string]any)
	if !ok {
		t.Fatalf("unexpected events payload type: %#v", got["events"])
	}
	if len(events) != 1 {
		t.Fatalf("expected one filtered event, got %#v", events)
	}
	if events[0]["event"] != gatewayws.EventToolStart {
		t.Fatalf("unexpected event: %#v", events[0])
	}
	if events[0]["agent_id"] != "alpha" || events[0]["session_id"] != "sess-1" {
		t.Fatalf("missing projected identity fields: %#v", events[0])
	}

	relayTail := buf.Tail(0, 10, 32*1024, toolbuiltin.RuntimeObserveFilters{
		Subsystem: "relay",
		Source:    "relay-monitor",
	})
	relayEvents, ok := relayTail["events"].([]map[string]any)
	if !ok || len(relayEvents) != 1 {
		t.Fatalf("expected one relay event, got %#v", relayTail["events"])
	}
	if relayEvents[0]["event"] != gatewayws.EventRelayHealth {
		t.Fatalf("unexpected relay event: %#v", relayEvents[0])
	}
	if relayEvents[0]["subsystem"] != "relay" || relayEvents[0]["source"] != "relay-monitor" {
		t.Fatalf("missing derived relay fields: %#v", relayEvents[0])
	}

	chatTail := buf.Tail(0, 10, 32*1024, toolbuiltin.RuntimeObserveFilters{
		Subsystem: "chat",
		Source:    "inbound",
	})
	chatEvents, ok := chatTail["events"].([]map[string]any)
	if !ok || len(chatEvents) != 1 {
		t.Fatalf("expected one chat event, got %#v", chatTail["events"])
	}
	if chatEvents[0]["event"] != gatewayws.EventChatMessage {
		t.Fatalf("unexpected chat event: %#v", chatEvents[0])
	}
	if chatEvents[0]["subsystem"] != "chat" || chatEvents[0]["source"] != "inbound" {
		t.Fatalf("missing derived chat fields: %#v", chatEvents[0])
	}

	mcpTail := buf.Tail(0, 10, 32*1024, toolbuiltin.RuntimeObserveFilters{Subsystem: "mcp"})
	mcpEvents, ok := mcpTail["events"].([]map[string]any)
	if !ok || len(mcpEvents) != 1 {
		t.Fatalf("expected one mcp event, got %#v", mcpTail["events"])
	}
	if mcpEvents[0]["event"] != gatewayws.EventMCPLifecycle || mcpEvents[0]["subsystem"] != "mcp" {
		t.Fatalf("unexpected mcp event projection: %#v", mcpEvents[0])
	}
}

func TestObservedEventEmitterWritesToBufferAndInnerEmitter(t *testing.T) {
	buf := newRuntimeEventBuffer(4)
	capture := &capturingEmitter{}
	emitter := newObservedEventEmitter(capture, buf)

	emitter.Emit(gatewayws.EventChatMessage, gatewayws.ChatMessagePayload{
		TS:        20,
		SessionID: "sess-3",
		Direction: "outbound",
		Text:      "ack",
	})

	if len(capture.eventsByName(gatewayws.EventChatMessage)) != 1 {
		t.Fatalf("expected inner emitter to receive chat.message")
	}
	tail := buf.Tail(0, 10, 32*1024, toolbuiltin.RuntimeObserveFilters{Direction: "outbound"})
	events, ok := tail["events"].([]map[string]any)
	if !ok || len(events) != 1 {
		t.Fatalf("expected one buffered outbound event, got %#v", tail["events"])
	}
	payload, ok := events[0]["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected buffered payload map, got %#v", events[0]["payload"])
	}
	if payload["text"] != "ack" {
		raw, _ := json.Marshal(events[0])
		t.Fatalf("unexpected buffered payload: %s", raw)
	}
}

func TestObserveRuntimeActivityWaitsForNewEvent(t *testing.T) {
	eventBuf := newRuntimeEventBuffer(8)
	logBuf := newRuntimeLogBuffer(8)

	go func() {
		time.Sleep(20 * time.Millisecond)
		eventBuf.Append(gatewayws.EventToolError, gatewayws.ToolLifecyclePayload{
			TS:         30,
			AgentID:    "alpha",
			SessionID:  "sess-9",
			ToolCallID: "call-9",
			ToolName:   "fetch",
			Error:      "boom",
		})
	}()

	out, err := observeRuntimeActivity(context.Background(), eventBuf, logBuf, toolbuiltin.RuntimeObserveRequest{
		IncludeEvents: true,
		EventCursor:   0,
		EventLimit:    10,
		MaxBytes:      32 * 1024,
		WaitTimeoutMS: 500,
		Filters: toolbuiltin.RuntimeObserveFilters{
			Events:  []string{gatewayws.EventToolError},
			AgentID: "alpha",
		},
	})
	if err != nil {
		t.Fatalf("observeRuntimeActivity error: %v", err)
	}
	if timedOut, _ := out["timed_out"].(bool); timedOut {
		t.Fatalf("expected event arrival, got timeout: %#v", out)
	}
	events, _ := out["events"].(map[string]any)
	items, _ := events["events"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("expected one observed event, got %#v", out)
	}
}

func TestObserveRuntimeActivityWaitsForMatchingFilteredEvent(t *testing.T) {
	eventBuf := newRuntimeEventBuffer(8)
	logBuf := newRuntimeLogBuffer(8)

	go func() {
		time.Sleep(20 * time.Millisecond)
		eventBuf.Append(gatewayws.EventChatMessage, gatewayws.ChatMessagePayload{
			TS:        40,
			SessionID: "sess-chat",
			Direction: "inbound",
			Text:      "hello",
		})
		time.Sleep(20 * time.Millisecond)
		eventBuf.Append(gatewayws.EventRelayHealth, gatewayws.RelayHealthPayload{
			TS:        50,
			URL:       "wss://relay.example",
			Reachable: true,
			Source:    "relay-monitor",
		})
	}()

	out, err := observeRuntimeActivity(context.Background(), eventBuf, logBuf, toolbuiltin.RuntimeObserveRequest{
		IncludeEvents: true,
		EventCursor:   0,
		EventLimit:    10,
		MaxBytes:      32 * 1024,
		WaitTimeoutMS: 500,
		Filters: toolbuiltin.RuntimeObserveFilters{
			Subsystem: "relay",
			Source:    "relay-monitor",
		},
	})
	if err != nil {
		t.Fatalf("observeRuntimeActivity error: %v", err)
	}
	if timedOut, _ := out["timed_out"].(bool); timedOut {
		t.Fatalf("expected matching event arrival, got timeout: %#v", out)
	}
	events, _ := out["events"].(map[string]any)
	items, _ := events["events"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("expected one matching observed event, got %#v", out)
	}
	if items[0]["event"] != gatewayws.EventRelayHealth {
		t.Fatalf("unexpected filtered event: %#v", items[0])
	}
}

func TestObserveRuntimeActivityTimesOutWithoutNewData(t *testing.T) {
	eventBuf := newRuntimeEventBuffer(8)
	logBuf := newRuntimeLogBuffer(8)

	out, err := observeRuntimeActivity(context.Background(), eventBuf, logBuf, toolbuiltin.RuntimeObserveRequest{
		IncludeEvents: true,
		EventCursor:   0,
		EventLimit:    10,
		MaxBytes:      32 * 1024,
		WaitTimeoutMS: 25,
	})
	if err != nil {
		t.Fatalf("observeRuntimeActivity error: %v", err)
	}
	if timedOut, _ := out["timed_out"].(bool); !timedOut {
		t.Fatalf("expected timeout, got %#v", out)
	}
}

func TestObserveRuntimeActivityReturnsResetForStaleFilteredCursor(t *testing.T) {
	eventBuf := newRuntimeEventBuffer(3)
	logBuf := newRuntimeLogBuffer(8)
	for i := 0; i < 5; i++ {
		eventBuf.Append(gatewayws.EventChatMessage, gatewayws.ChatMessagePayload{
			TS:        int64(100 + i),
			SessionID: "sess-chat",
			Direction: "inbound",
			Text:      "hello",
		})
	}

	started := time.Now()
	out, err := observeRuntimeActivity(context.Background(), eventBuf, logBuf, toolbuiltin.RuntimeObserveRequest{
		IncludeEvents: true,
		EventCursor:   1,
		EventLimit:    10,
		MaxBytes:      32 * 1024,
		WaitTimeoutMS: 250,
		Filters:       toolbuiltin.RuntimeObserveFilters{Events: []string{gatewayws.EventRelayHealth}},
	})
	if err != nil {
		t.Fatalf("observeRuntimeActivity error: %v", err)
	}
	if time.Since(started) > 150*time.Millisecond {
		t.Fatalf("stale reset response took too long: %#v", out)
	}
	if timedOut, _ := out["timed_out"].(bool); timedOut {
		t.Fatalf("expected reset progress, got timeout: %#v", out)
	}
	events, _ := out["events"].(map[string]any)
	if reset, _ := events["reset"].(bool); !reset {
		t.Fatalf("expected reset=true, got %#v", events)
	}
	if cursor, _ := events["cursor"].(int64); cursor != 5 {
		t.Fatalf("expected cursor to advance to retained tail, got %#v", events)
	}
	items, _ := events["events"].([]map[string]any)
	if len(items) != 0 {
		t.Fatalf("expected zero matching events after reset, got %#v", items)
	}
}

func TestObserveRuntimeActivityReturnsTruncationForOversizedFirstEvent(t *testing.T) {
	eventBuf := newRuntimeEventBuffer(8)
	logBuf := newRuntimeLogBuffer(8)
	eventBuf.Append(gatewayws.EventToolError, gatewayws.ToolLifecyclePayload{
		TS:         30,
		AgentID:    "alpha",
		SessionID:  "sess-9",
		ToolCallID: "call-9",
		ToolName:   "fetch",
		Error:      strings.Repeat("boom", 128),
	})

	started := time.Now()
	out, err := observeRuntimeActivity(context.Background(), eventBuf, logBuf, toolbuiltin.RuntimeObserveRequest{
		IncludeEvents: true,
		EventCursor:   0,
		EventLimit:    10,
		MaxBytes:      32,
		WaitTimeoutMS: 250,
		Filters:       toolbuiltin.RuntimeObserveFilters{Events: []string{gatewayws.EventToolError}},
	})
	if err != nil {
		t.Fatalf("observeRuntimeActivity error: %v", err)
	}
	if time.Since(started) > 150*time.Millisecond {
		t.Fatalf("truncation response took too long: %#v", out)
	}
	if timedOut, _ := out["timed_out"].(bool); timedOut {
		t.Fatalf("expected truncation progress, got timeout: %#v", out)
	}
	events, _ := out["events"].(map[string]any)
	if truncated, _ := events["truncated"].(bool); !truncated {
		t.Fatalf("expected truncated=true, got %#v", events)
	}
	if cursor, _ := events["cursor"].(int64); cursor != 1 {
		t.Fatalf("expected cursor to advance past oversized event, got %#v", events)
	}
	items, _ := events["events"].([]map[string]any)
	if len(items) != 0 {
		t.Fatalf("expected zero events due to truncation, got %#v", items)
	}
}

func TestObserveRuntimeActivityReturnsTruncationForOversizedFirstLog(t *testing.T) {
	eventBuf := newRuntimeEventBuffer(8)
	logBuf := newRuntimeLogBuffer(8)
	logBuf.Append("error", strings.Repeat("x", 256))

	started := time.Now()
	out, err := observeRuntimeActivity(context.Background(), eventBuf, logBuf, toolbuiltin.RuntimeObserveRequest{
		IncludeLogs:   true,
		LogCursor:     0,
		LogLimit:      10,
		MaxBytes:      16,
		WaitTimeoutMS: 250,
	})
	if err != nil {
		t.Fatalf("observeRuntimeActivity error: %v", err)
	}
	if time.Since(started) > 150*time.Millisecond {
		t.Fatalf("oversized log truncation response took too long: %#v", out)
	}
	if timedOut, _ := out["timed_out"].(bool); timedOut {
		t.Fatalf("expected log truncation progress, got timeout: %#v", out)
	}
	logs, _ := out["logs"].(map[string]any)
	if truncated, _ := logs["truncated"].(bool); !truncated {
		t.Fatalf("expected truncated=true, got %#v", logs)
	}
	if cursor, _ := logs["cursor"].(int64); cursor != 1 {
		t.Fatalf("expected cursor to advance past oversized log, got %#v", logs)
	}
	lines, _ := logs["lines"].([]string)
	if len(lines) != 0 {
		t.Fatalf("expected zero log lines due to truncation, got %#v", lines)
	}
}

type staticDMHealthReporter struct {
	snapshot nostruntime.SubHealthSnapshot
}

func (r *staticDMHealthReporter) HealthSnapshot() nostruntime.SubHealthSnapshot {
	return r.snapshot
}

func TestDMHealthObserverEmitsStartupAndChangeSnapshots(t *testing.T) {
	capture := &capturingEmitter{}
	observer := newDMHealthObserver(capture)
	reporter := &staticDMHealthReporter{snapshot: nostruntime.SubHealthSnapshot{
		Label:           "nip17",
		BoundRelays:     []string{"wss://relay.example"},
		ReplayWindowMS:  60000,
		ReconnectCount:  1,
		LastReconnectAt: time.UnixMilli(1000),
	}}

	observer.EmitStartup(reporter)
	events := capture.eventsByName(gatewayws.EventDMHealth)
	if len(events) != 1 {
		t.Fatalf("expected 1 startup dm.health event, got %d", len(events))
	}
	startup, ok := events[0].(gatewayws.DMHealthPayload)
	if !ok {
		t.Fatalf("unexpected startup payload type: %T", events[0])
	}
	if startup.Label != "nip17" || !startup.Healthy || startup.Source != "startup" {
		t.Fatalf("unexpected startup dm.health payload: %+v", startup)
	}

	observer.EmitTick(reporter)
	events = capture.eventsByName(gatewayws.EventDMHealth)
	if len(events) != 2 {
		t.Fatalf("expected periodic dm.health event, got %d", len(events))
	}
	periodic := events[1].(gatewayws.DMHealthPayload)
	if periodic.Source != "periodic" {
		t.Fatalf("expected periodic source, got %+v", periodic)
	}

	reporter.snapshot.EventCount = 99
	reporter.snapshot.LastEventAt = time.UnixMilli(2000)
	observer.EmitTick(reporter)
	events = capture.eventsByName(gatewayws.EventDMHealth)
	if len(events) != 3 {
		t.Fatalf("expected activity-only periodic dm.health event, got %d", len(events))
	}
	activity := events[2].(gatewayws.DMHealthPayload)
	if activity.Source != "periodic" {
		t.Fatalf("expected activity-only update to stay periodic, got %+v", activity)
	}

	reporter.snapshot.LastClosedReason = "relay closed"
	reporter.snapshot.ReconnectCount = 2
	observer.EmitTick(reporter)
	events = capture.eventsByName(gatewayws.EventDMHealth)
	if len(events) != 4 {
		t.Fatalf("expected changed dm.health event, got %d", len(events))
	}
	changed := events[3].(gatewayws.DMHealthPayload)
	if changed.Source != "change" || changed.Healthy {
		t.Fatalf("expected unhealthy change payload, got %+v", changed)
	}
	if changed.LastClosedReason != "relay closed" || changed.ReconnectCount != 2 {
		t.Fatalf("unexpected changed dm.health payload: %+v", changed)
	}
}

func TestLogRelayHealthResultsEmitsStructuredEvents(t *testing.T) {
	capture := &capturingEmitter{}
	svc := &daemonServices{
		relay: relayPolicyServices{
			healthState: map[string]bool{},
		},
		emitter: capture,
	}

	svc.logRelayHealthResults(true, []nostruntime.RelayHealthResult{{
		URL:       "wss://relay.example",
		Reachable: false,
		Err:       errors.New("dial tcp: timeout"),
	}})

	events := capture.eventsByName(gatewayws.EventRelayHealth)
	if len(events) != 1 {
		t.Fatalf("expected 1 relay.health event, got %d", len(events))
	}
	payload, ok := events[0].(gatewayws.RelayHealthPayload)
	if !ok {
		t.Fatalf("unexpected relay.health payload type: %T", events[0])
	}
	if payload.URL != "wss://relay.example" || payload.Reachable || !payload.Initial {
		t.Fatalf("unexpected relay.health payload: %+v", payload)
	}
	if payload.Source != "relay-monitor" || payload.Error == "" {
		t.Fatalf("expected relay-monitor source and error, got %+v", payload)
	}
}

func TestRuntimeHeartbeatLoopFeedsBufferedEmitterWithoutWSRuntime(t *testing.T) {
	prevEmitter := controlWsEmitter
	defer setControlWSEmitter(prevEmitter)

	eventBuf := newRuntimeEventBuffer(16)
	capture := &capturingEmitter{}
	setControlWSEmitter(newObservedEventEmitter(capture, eventBuf))

	ctx, cancel := context.WithCancel(context.Background())
	shutdownEmitter := newRuntimeShutdownEmitter(emitControlWSEvent)
	done := startRuntimeHeartbeatLoop(ctx, time.Now().Add(-2*time.Second), "metiqd-test", 10*time.Millisecond, shutdownEmitter)

	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		if len(capture.eventsByName(gatewayws.EventHealth)) >= 1 && len(capture.eventsByName(gatewayws.EventTick)) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("expected heartbeat events, got health=%d tick=%d", len(capture.eventsByName(gatewayws.EventHealth)), len(capture.eventsByName(gatewayws.EventTick)))
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	<-done
	if len(capture.eventsByName(gatewayws.EventShutdown)) != 1 {
		t.Fatalf("expected shutdown event on cancel, got %d", len(capture.eventsByName(gatewayws.EventShutdown)))
	}

	tail := eventBuf.Tail(0, 20, 32*1024, toolbuiltin.RuntimeObserveFilters{})
	items, ok := tail["events"].([]map[string]any)
	if !ok {
		t.Fatalf("unexpected buffered events payload: %#v", tail["events"])
	}
	haveHealth := false
	haveTick := false
	haveShutdown := false
	for _, item := range items {
		switch item["event"] {
		case gatewayws.EventHealth:
			haveHealth = true
		case gatewayws.EventTick:
			haveTick = true
		case gatewayws.EventShutdown:
			haveShutdown = true
		}
	}
	if !haveHealth || !haveTick || !haveShutdown {
		t.Fatalf("expected buffered health/tick/shutdown events, got %#v", items)
	}
}

func TestRuntimeShutdownEmitterEmitsOnlyOnce(t *testing.T) {
	capture := &capturingEmitter{}
	emitter := newRuntimeShutdownEmitter(capture.Emit)
	emitter.Emit("config change requires restart")
	emitter.Emit("daemon stopping")
	events := capture.eventsByName(gatewayws.EventShutdown)
	if len(events) != 1 {
		t.Fatalf("expected 1 shutdown event, got %d", len(events))
	}
	payload, ok := events[0].(gatewayws.ShutdownPayload)
	if !ok {
		t.Fatalf("unexpected shutdown payload type: %T", events[0])
	}
	if payload.Reason != "config change requires restart" {
		t.Fatalf("expected first shutdown reason to win, got %+v", payload)
	}
}
