package ws

import (
	"testing"
	"time"
)

// ─── captureEmitter ────────────────────────────────────────────────────────────

type captureEmitter struct {
	events   []string
	payloads []any
}

func (c *captureEmitter) Emit(event string, payload any) {
	c.events = append(c.events, event)
	c.payloads = append(c.payloads, payload)
}

func (c *captureEmitter) Last() (string, any) {
	if len(c.events) == 0 {
		return "", nil
	}
	i := len(c.events) - 1
	return c.events[i], c.payloads[i]
}

func (c *captureEmitter) Count() int { return len(c.events) }

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestNoopEmitter(t *testing.T) {
	e := NoopEmitter{}
	// Must not panic.
	e.Emit(EventTick, TickPayload{TS: 1})
	e.Emit(EventHealth, nil)
}

func TestRuntimeEmitter_nilRuntime(t *testing.T) {
	e := NewRuntimeEmitter(nil)
	// Must not panic with nil runtime.
	e.Emit(EventTick, TickPayload{TS: 1})
}

func TestAllPushEvents_containsCore(t *testing.T) {
	required := []string{
		EventTick, EventHealth, EventShutdown,
		EventAgentStatus, EventChatMessage,
		EventCronTick, EventCronResult,
		EventConfigUpdated, EventMCPLifecycle,
		EventExecApprovalRequested, EventExecApprovalResolved,
		EventVoicewake, EventUpdateAvailable,
		EventChannelMessage, EventRelayHealth, EventDMHealth,
		EventNodePairRequested, EventNodePairResolved,
		EventDevicePairResolved, EventPluginLoaded,
		EventToolStart, EventToolProgress, EventToolResult, EventToolError,
		EventTurnResult,
	}
	set := make(map[string]struct{}, len(AllPushEvents))
	for _, e := range AllPushEvents {
		set[e] = struct{}{}
	}
	for _, name := range required {
		if _, ok := set[name]; !ok {
			t.Errorf("AllPushEvents missing %q", name)
		}
	}
}

func TestEmitTick(t *testing.T) {
	e := &captureEmitter{}
	start := time.Now().Add(-5 * time.Second)
	EmitTick(e, start, "v1")
	if e.Count() != 1 {
		t.Fatalf("expected 1 event, got %d", e.Count())
	}
	name, payload := e.Last()
	if name != EventTick {
		t.Errorf("expected %q, got %q", EventTick, name)
	}
	tp, ok := payload.(TickPayload)
	if !ok {
		t.Fatalf("expected TickPayload, got %T", payload)
	}
	if tp.UptimeMS < 5000 {
		t.Errorf("uptime_ms should be >= 5000, got %d", tp.UptimeMS)
	}
	if tp.Version != "v1" {
		t.Errorf("expected version v1, got %q", tp.Version)
	}
}

func TestCaptureEmitter_multiple(t *testing.T) {
	e := &captureEmitter{}
	e.Emit(EventHealth, HealthPayload{OK: true})
	e.Emit(EventShutdown, ShutdownPayload{Reason: "test"})
	if e.Count() != 2 {
		t.Fatalf("expected 2, got %d", e.Count())
	}
	name, _ := e.Last()
	if name != EventShutdown {
		t.Errorf("expected shutdown, got %q", name)
	}
}

func TestNewPayloadTypes(t *testing.T) {
	e := &captureEmitter{}

	e.Emit(EventExecApprovalRequested, ExecApprovalRequestedPayload{ID: "req-1", NodeID: "n1"})
	e.Emit(EventExecApprovalResolved, ExecApprovalResolvedPayload{ID: "req-1", Decision: "approved"})
	e.Emit(EventVoicewake, VoicewakePayload{Trigger: "hey metiq"})
	e.Emit(EventUpdateAvailable, UpdateAvailablePayload{Version: "2.0"})
	e.Emit(EventChannelMessage, ChannelMessagePayload{ChannelID: "ch1", Direction: "inbound"})
	e.Emit(EventRelayHealth, RelayHealthPayload{URL: "wss://relay", Reachable: true})
	e.Emit(EventDMHealth, DMHealthPayload{Label: "nip17", Healthy: true})
	e.Emit(EventMCPLifecycle, MCPLifecyclePayload{Name: "demo", State: "connected"})

	if e.Count() != 8 {
		t.Fatalf("expected 8 events, got %d", e.Count())
	}
	// Spot-check last payload
	name, payload := e.Last()
	if name != EventMCPLifecycle {
		t.Errorf("expected mcp.lifecycle, got %q", name)
	}
	mp, ok := payload.(MCPLifecyclePayload)
	if !ok {
		t.Fatalf("expected MCPLifecyclePayload, got %T", payload)
	}
	if mp.Name != "demo" || mp.State != "connected" {
		t.Errorf("unexpected mcp lifecycle payload: %+v", mp)
	}
}

func TestPairingAndPluginPayloads(t *testing.T) {
	e := &captureEmitter{}

	e.Emit(EventNodePairRequested, NodePairRequestedPayload{RequestID: "req-1", Label: "My Node"})
	e.Emit(EventNodePairResolved, NodePairResolvedPayload{RequestID: "req-1", Decision: "approved"})
	e.Emit(EventDevicePairResolved, DevicePairResolvedPayload{DeviceID: "dev-1", Decision: "rejected"})
	e.Emit(EventPluginLoaded, PluginLoadedPayload{PluginID: "my-plugin", Action: "installed"})

	if e.Count() != 4 {
		t.Fatalf("expected 4 events, got %d", e.Count())
	}
	name, payload := e.Last()
	if name != EventPluginLoaded {
		t.Errorf("expected plugin.loaded, got %q", name)
	}
	pp, ok := payload.(PluginLoadedPayload)
	if !ok {
		t.Fatalf("expected PluginLoadedPayload, got %T", payload)
	}
	if pp.Action != "installed" {
		t.Errorf("expected action=installed, got %q", pp.Action)
	}
}

func TestToolLifecyclePayloads(t *testing.T) {
	e := &captureEmitter{}

	e.Emit(EventToolStart, ToolLifecyclePayload{ToolCallID: "call-1", ToolName: "fetch", SessionID: "sess-1", TurnID: "turn-1"})
	e.Emit(EventToolProgress, ToolLifecyclePayload{ToolCallID: "call-1", ToolName: "fetch", Data: map[string]any{"phase": "stream"}})
	e.Emit(EventToolResult, ToolLifecyclePayload{ToolCallID: "call-1", ToolName: "fetch", Result: "ok"})
	e.Emit(EventToolError, ToolLifecyclePayload{ToolCallID: "call-2", ToolName: "write", Error: "permission denied"})

	if e.Count() != 4 {
		t.Fatalf("expected 4 events, got %d", e.Count())
	}
	name, payload := e.Last()
	if name != EventToolError {
		t.Fatalf("expected %q, got %q", EventToolError, name)
	}
	lp, ok := payload.(ToolLifecyclePayload)
	if !ok {
		t.Fatalf("expected ToolLifecyclePayload, got %T", payload)
	}
	if lp.ToolCallID != "call-2" || lp.ToolName != "write" || lp.Error != "permission denied" {
		t.Fatalf("unexpected lifecycle payload: %+v", lp)
	}
}

func TestTurnResultPayload(t *testing.T) {
	e := &captureEmitter{}
	e.Emit(EventTurnResult, TurnResultPayload{
		SessionID:   "sess-1",
		TurnID:      "turn-1",
		Outcome:     "completed_with_tools",
		StopReason:  "model_text",
		DurationMS:  250,
		LoopBlocked: false,
	})
	if e.Count() != 1 {
		t.Fatalf("expected 1 event, got %d", e.Count())
	}
	name, payload := e.Last()
	if name != EventTurnResult {
		t.Fatalf("expected %q, got %q", EventTurnResult, name)
	}
	tp, ok := payload.(TurnResultPayload)
	if !ok {
		t.Fatalf("expected TurnResultPayload, got %T", payload)
	}
	if tp.SessionID != "sess-1" || tp.TurnID != "turn-1" || tp.Outcome != "completed_with_tools" {
		t.Fatalf("unexpected turn result payload: %+v", tp)
	}
}
