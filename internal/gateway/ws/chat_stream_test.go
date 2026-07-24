package ws

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"metiq/internal/gateway/protocol"
)

type chatContract struct {
	Required []string `json:"required"`
	Optional []string `json:"optional"`
}

func TestChatEventVariantsMatchOpenClawClosedUnion(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "openclaw_chat_event_contracts.json"))
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	contracts := map[string]chatContract{}
	if err := json.Unmarshal(raw, &contracts); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	base := ChatEventBase{RunID: "run-1", SessionKey: "session-1", AgentID: "main", SpawnedBy: "parent", Seq: 2}
	samples := map[string]any{
		"status":  ChatStatusEvent{ChatEventBase: base, State: ChatStateStatus, Phase: ChatPhaseStartingModel},
		"delta":   ChatDeltaEvent{ChatEventBase: base, State: ChatStateDelta, DeltaText: "hi", Replace: true, Message: ChatAssistantMessage("hi"), Usage: map[string]any{"outputTokens": 1}},
		"final":   ChatFinalEvent{ChatEventBase: base, State: ChatStateFinal, Message: ChatAssistantMessage("hi"), Usage: map[string]any{"outputTokens": 1}, StopReason: "model_text", Yielded: true},
		"aborted": ChatAbortedEvent{ChatEventBase: base, State: ChatStateAborted, Message: ChatAssistantMessage("partial"), ErrorMessage: "stopped", StopReason: "canceled"},
		"error":   ChatErrorEvent{ChatEventBase: base, State: ChatStateError, ErrorMessage: "failed", ErrorKind: ChatErrorTimeout, Usage: map[string]any{"inputTokens": 1}, StopReason: "timeout"},
	}
	for state, sample := range samples {
		t.Run(state, func(t *testing.T) {
			data, err := json.Marshal(sample)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			obj := map[string]any{}
			if err := json.Unmarshal(data, &obj); err != nil {
				t.Fatalf("decode: %v", err)
			}
			contract := contracts[state]
			allowed := map[string]struct{}{}
			for _, key := range append(contract.Required, contract.Optional...) {
				allowed[key] = struct{}{}
			}
			for _, key := range contract.Required {
				if _, ok := obj[key]; !ok {
					t.Fatalf("missing required key %q: %s", key, data)
				}
			}
			for key := range obj {
				if _, ok := allowed[key]; !ok {
					t.Fatalf("key %q is outside closed %s schema: %s", key, state, data)
				}
			}
			if obj["state"] != state {
				t.Fatalf("state = %#v, want %q", obj["state"], state)
			}
		})
	}
}

func TestChatStreamSequenceAndFirstTerminalWins(t *testing.T) {
	capture := &captureEmitter{}
	stream := NewChatStream(capture, "run-1", "session-1", "main")
	stream.Status(ChatPhasePreparingContext)
	stream.Delta("hel", false)
	stream.Delta("hello", true)
	stream.Final(ChatAssistantMessage("hello"), ChatUsage(2, 3), "model_text", false)
	stream.Error(nil, "late", ChatErrorUnknown, nil, "")

	if capture.Count() != 4 {
		t.Fatalf("event count = %d, want 4", capture.Count())
	}
	for i, event := range capture.events {
		if event != EventChat {
			t.Fatalf("event[%d] = %q, want chat", i, event)
		}
		base := chatEventBaseForTest(t, capture.payloads[i])
		if base.Seq != i || base.RunID != "run-1" || base.SessionKey != "session-1" {
			t.Fatalf("payload[%d] base = %+v", i, base)
		}
	}
	if delta, ok := capture.payloads[2].(ChatDeltaEvent); !ok || !delta.Replace || delta.DeltaText != "hello" {
		t.Fatalf("replacement delta = %#v", capture.payloads[2])
	}
}

func TestChatStreamConcurrentSequenceIsMonotonic(t *testing.T) {
	capture := &lockedCaptureEmitter{}
	stream := NewChatStream(capture, "run-1", "session-1", "main")
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream.Delta("x", false)
		}()
	}
	wg.Wait()
	for i, payload := range capture.payloads {
		if base := chatEventBaseForTest(t, payload); base.Seq != i {
			t.Fatalf("payload[%d] seq = %d", i, base.Seq)
		}
	}
}

func TestChatDeltaCoalescingReplaceSemantics(t *testing.T) {
	c := &client{id: "c1", subscriptions: map[string]struct{}{EventChat: {}}, eventQueue: make(chan any, 4), eventDone: make(chan struct{})}
	r := &Runtime{opts: RuntimeOptions{DeltaCoalesceInterval: time.Hour}, clients: map[string]*client{"c1": c}, chatCoalesce: map[string]*chatChunkCoalescer{}}
	base := ChatEventBase{RunID: "run", SessionKey: "session"}
	r.Broadcast(EventChat, ChatDeltaEvent{ChatEventBase: base, State: ChatStateDelta, DeltaText: "old"})
	base.Seq++
	r.Broadcast(EventChat, ChatDeltaEvent{ChatEventBase: base, State: ChatStateDelta, DeltaText: "new", Replace: true})
	base.Seq++
	r.Broadcast(EventChat, ChatDeltaEvent{ChatEventBase: base, State: ChatStateDelta, DeltaText: "!", Replace: false})
	r.flushCoalescedChatChunk(chatChunkCoalesceKey("session", "run"))
	frame := (<-c.eventQueue).(map[string]any)
	got := frame["payload"].(ChatDeltaEvent)
	if got.DeltaText != "new!" || !got.Replace || got.Seq != 2 {
		t.Fatalf("coalesced replacement = %+v", got)
	}
}

func TestSessionSubscribeLifecycleUsesConnectionSubscriptions(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{Events: []string{EventChat, EventChatMessage, EventAgentStatus, EventToolStart}}}
	c := &client{subscriptions: map[string]struct{}{}}
	handled, payload, shape := r.handleInternalRequest(c, protocol.RequestFrame{Method: MethodSessionsSubscribe, Params: json.RawMessage(`{}`)})
	if !handled || shape != nil {
		t.Fatalf("subscribe handled=%v error=%+v", handled, shape)
	}
	result := payload.(map[string]any)
	if result["subscribed"] != true || !c.isSubscribed(EventChat) || !c.isSubscribed(EventToolStart) {
		t.Fatalf("subscribe result=%#v subscriptions=%v", result, c.listSubscriptions())
	}
	if c.isSubscribed(EventToolResult) {
		t.Fatal("must not subscribe to an event the server does not advertise")
	}

	handled, payload, shape = r.handleInternalRequest(c, protocol.RequestFrame{Method: MethodSessionsUnsubscribe, Params: json.RawMessage(`{}`)})
	if !handled || shape != nil || payload.(map[string]any)["subscribed"] != false {
		t.Fatalf("unsubscribe handled=%v payload=%#v error=%+v", handled, payload, shape)
	}
	if c.isSubscribed(EventChat) || c.isSubscribed(EventToolStart) {
		t.Fatalf("session subscriptions were not removed: %v", c.listSubscriptions())
	}
}

type lockedCaptureEmitter struct {
	mu       sync.Mutex
	payloads []any
}

func (c *lockedCaptureEmitter) Emit(_ string, payload any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.payloads = append(c.payloads, payload)
}

func chatEventBaseForTest(t *testing.T, payload any) ChatEventBase {
	t.Helper()
	switch p := payload.(type) {
	case ChatStatusEvent:
		return p.ChatEventBase
	case ChatDeltaEvent:
		return p.ChatEventBase
	case ChatFinalEvent:
		return p.ChatEventBase
	case ChatAbortedEvent:
		return p.ChatEventBase
	case ChatErrorEvent:
		return p.ChatEventBase
	default:
		t.Fatalf("unexpected chat payload %T", payload)
		return ChatEventBase{}
	}
}
