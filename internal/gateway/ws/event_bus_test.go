package ws

import (
	"testing"
	"time"
)

// ─── captureEmitter ────────────────────────────────────────────────────────────

type captureEmitter struct {
	events []string
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
		EventConfigUpdated,
		EventExecApprovalRequested, EventExecApprovalResolved,
		EventVoicewake, EventUpdateAvailable,
		EventChannelMessage,
		EventNodePairRequested, EventNodePairResolved,
		EventDevicePairResolved, EventPluginLoaded,
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
	e.Emit(EventVoicewake, VoicewakePayload{Trigger: "hey swarmstr"})
	e.Emit(EventUpdateAvailable, UpdateAvailablePayload{Version: "2.0"})
	e.Emit(EventChannelMessage, ChannelMessagePayload{ChannelID: "ch1", Direction: "inbound"})

	if e.Count() != 5 {
		t.Fatalf("expected 5 events, got %d", e.Count())
	}
	// Spot-check last payload
	name, payload := e.Last()
	if name != EventChannelMessage {
		t.Errorf("expected channel.message, got %q", name)
	}
	cp, ok := payload.(ChannelMessagePayload)
	if !ok {
		t.Fatalf("expected ChannelMessagePayload, got %T", payload)
	}
	if cp.ChannelID != "ch1" {
		t.Errorf("expected ch1, got %q", cp.ChannelID)
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
