package ws

import (
	"encoding/json"
	"testing"
	"time"

	"metiq/internal/gateway/protocol"
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

// Compile-time guarantee that both emitters satisfy the interface.
var (
	_ EventEmitter = NoopEmitter{}
	_ EventEmitter = (*RuntimeEmitter)(nil)
)

// newRuntimeWithClient builds a minimal Runtime with a single client subscribed
// to the given event, so tests can observe whether an emitter actually delivers
// a frame to that client's queue.
func newRuntimeWithClient(event string) (*Runtime, *client) {
	c := &client{
		id:            "c1",
		subscriptions: map[string]struct{}{event: {}},
		eventQueue:    make(chan any, 4),
		eventDone:     make(chan struct{}),
	}
	r := &Runtime{
		clients:      map[string]*client{"c1": c},
		chatCoalesce: map[string]*chatChunkCoalescer{},
	}
	return r, c
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestRuntimeEmitter_DeliversToSubscribedClient asserts the emitter actually
// forwards the event and payload through the runtime to a subscribed client.
func TestRuntimeEmitter_DeliversToSubscribedClient(t *testing.T) {
	r, c := newRuntimeWithClient(EventHealth)
	e := NewRuntimeEmitter(r)

	e.Emit(EventHealth, HealthPayload{OK: true})

	select {
	case frame := <-c.eventQueue:
		m, ok := frame.(map[string]any)
		if !ok {
			t.Fatalf("expected map frame, got %T", frame)
		}
		if m["event"] != EventHealth {
			t.Errorf("delivered event = %v, want %q", m["event"], EventHealth)
		}
		p, ok := m["payload"].(HealthPayload)
		if !ok {
			t.Fatalf("payload type = %T, want HealthPayload", m["payload"])
		}
		if !p.OK {
			t.Errorf("delivered payload OK = false, want true")
		}
	default:
		t.Fatal("expected a frame delivered to the subscribed client")
	}
}

// TestNoopEmitter verifies the no-op emitter never delivers anything to a
// subscribed client (its whole contract is to discard events).
func TestNoopEmitter(t *testing.T) {
	_, c := newRuntimeWithClient(EventTick)
	var e EventEmitter = NoopEmitter{}

	e.Emit(EventTick, TickPayload{TS: 1})
	e.Emit(EventHealth, nil)

	select {
	case frame := <-c.eventQueue:
		t.Fatalf("NoopEmitter must not deliver anything, got %#v", frame)
	default:
	}
}

// TestRuntimeEmitter_nilRuntime verifies a nil-runtime emitter is a safe no-op
// that delivers nothing (rather than panicking on a nil Broadcast receiver).
func TestRuntimeEmitter_nilRuntime(t *testing.T) {
	_, c := newRuntimeWithClient(EventTick)
	e := NewRuntimeEmitter(nil)

	e.Emit(EventTick, TickPayload{TS: 1})

	select {
	case frame := <-c.eventQueue:
		t.Fatalf("nil-runtime emitter must not deliver anything, got %#v", frame)
	default:
	}
}

func TestAllPushEvents_containsCore(t *testing.T) {
	required := []string{
		EventTick, EventHealth, EventShutdown,
		EventAgentStatus, EventChat, EventChatMessage,
		EventCronTick, EventCronResult,
		EventConfigUpdated, EventMCPLifecycle,
		EventExecApprovalRequested, EventExecApprovalResolved,
		EventVoicewake, EventUpdateAvailable,
		EventChannelMessage, EventRelayHealth, EventDMHealth,
		EventNodePairRequested, EventNodePairResolved, EventNodeInvokeProgress,
		EventNodePresence, EventNodeRunnerInventoryChanged, EventNodeInvokeRequest, EventNodeInvokeInput, EventNodeInvokeCancel,
		EventDevicePairChanged, EventDevicePairRequested, EventDevicePairResolved,
		EventDevicePairSetupCompleted, EventDevicePairSetupDeliveryUncertain, EventPluginLoaded,
		EventToolStart, EventToolProgress, EventToolResult, EventToolError,
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

// TestQuestionAndTaskSuggestionEventsSubscribeAndDeliver guards the catalog
// entries for the daemon-emitted question/task-suggestion events
// (swarmstr-j5dq): events.subscribe rejects names outside the advertised
// catalog, so dropping them from AllPushEvents silently disables the Web UI's
// live handlers. The test drives the real subscribe path against the
// production catalog and asserts each event is accepted and delivered.
func TestQuestionAndTaskSuggestionEventsSubscribeAndDeliver(t *testing.T) {
	events := []string{EventQuestionRequested, EventQuestionResolved, EventTaskSuggestion}
	c := &client{
		id:            "c1",
		subscriptions: map[string]struct{}{},
		eventQueue:    make(chan any, 8),
		eventDone:     make(chan struct{}),
	}
	r := &Runtime{
		opts:         RuntimeOptions{Events: AllPushEvents},
		clients:      map[string]*client{"c1": c},
		chatCoalesce: map[string]*chatChunkCoalescer{},
	}

	params, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	handled, _, shape := r.handleInternalRequest(c, protocol.RequestFrame{Method: MethodEventsSubscribe, Params: params})
	if !handled || shape != nil {
		t.Fatalf("events.subscribe rejected question/task-suggestion events: handled=%v shape=%+v", handled, shape)
	}

	for _, name := range events {
		if !c.isSubscribed(name) {
			t.Errorf("client not subscribed to %q after events.subscribe", name)
			continue
		}
		r.Broadcast(name, map[string]any{"id": "x"})
		select {
		case frame := <-c.eventQueue:
			m, ok := frame.(map[string]any)
			if !ok {
				t.Fatalf("expected map frame for %q, got %T", name, frame)
			}
			if m["event"] != name {
				t.Errorf("delivered event = %v, want %q", m["event"], name)
			}
		default:
			t.Errorf("expected %q broadcast delivered to subscribed client", name)
		}
	}
}

func TestRuntimeCoalescesChatDeltasUntilFlush(t *testing.T) {
	c := &client{id: "c1", subscriptions: map[string]struct{}{EventChat: {}}, eventQueue: make(chan any, 4), eventDone: make(chan struct{})}
	r := &Runtime{opts: RuntimeOptions{DeltaCoalesceInterval: time.Hour}, clients: map[string]*client{"c1": c}, chatCoalesce: map[string]*chatChunkCoalescer{}}
	base := ChatEventBase{RunID: "run", SessionKey: "sess"}
	r.Broadcast(EventChat, ChatDeltaEvent{ChatEventBase: base, State: ChatStateDelta, DeltaText: "hel"})
	base.Seq = 1
	r.Broadcast(EventChat, ChatDeltaEvent{ChatEventBase: base, State: ChatStateDelta, DeltaText: "lo"})
	select {
	case frame := <-c.eventQueue:
		t.Fatalf("chunk emitted before coalesce flush: %#v", frame)
	default:
	}
	r.flushCoalescedChatChunk(chatChunkCoalesceKey("sess", "run"))
	select {
	case frame := <-c.eventQueue:
		m := frame.(map[string]any)
		payload := m["payload"].(ChatDeltaEvent)
		if payload.DeltaText != "hello" || payload.Seq != 1 {
			t.Fatalf("coalesced payload = %+v", payload)
		}
		if m["seq"].(int64) != 1 {
			t.Fatalf("seq = %#v", m["seq"])
		}
	default:
		t.Fatal("expected coalesced chunk after flush")
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

// TestPluginSurfaceChangedEventSubscribeAndDeliver guards the catalog entry
// for the plugin.surface.changed event (swarmstr-qmxu.2): events.subscribe
// must accept it and Broadcast must deliver it, so plugin.surface.refresh can
// prompt clients to re-fetch plugins.uiDescriptors.
func TestPluginSurfaceChangedEventSubscribeAndDeliver(t *testing.T) {
	c := &client{
		id:            "c1",
		subscriptions: map[string]struct{}{},
		eventQueue:    make(chan any, 4),
		eventDone:     make(chan struct{}),
	}
	r := &Runtime{
		opts:         RuntimeOptions{Events: AllPushEvents},
		clients:      map[string]*client{"c1": c},
		chatCoalesce: map[string]*chatChunkCoalescer{},
	}
	params, err := json.Marshal(map[string]any{"events": []string{EventPluginSurfaceChanged}})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	handled, _, shape := r.handleInternalRequest(c, protocol.RequestFrame{Method: MethodEventsSubscribe, Params: params})
	if !handled || shape != nil {
		t.Fatalf("events.subscribe rejected plugin.surface.changed: handled=%v shape=%+v", handled, shape)
	}
	if !c.isSubscribed(EventPluginSurfaceChanged) {
		t.Fatal("client not subscribed to plugin.surface.changed")
	}
	r.Broadcast(EventPluginSurfaceChanged, map[string]any{"scope": "all", "count": 2})
	select {
	case frame := <-c.eventQueue:
		m, ok := frame.(map[string]any)
		if !ok {
			t.Fatalf("expected map frame, got %T", frame)
		}
		if m["event"] != EventPluginSurfaceChanged {
			t.Errorf("delivered event = %v, want %q", m["event"], EventPluginSurfaceChanged)
		}
	default:
		t.Error("expected plugin.surface.changed broadcast delivered to subscribed client")
	}
}
