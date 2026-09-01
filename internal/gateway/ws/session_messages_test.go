package ws

import (
	"encoding/json"
	"testing"

	"metiq/internal/gateway/protocol"
)

func TestSessionMessagesSubscriptionIsConnectionScoped(t *testing.T) {
	c := &client{subscriptions: map[string]struct{}{}, watchedSessions: map[string]struct{}{}, eventQueue: make(chan any, 4), eventDone: make(chan struct{})}
	r := &Runtime{opts: RuntimeOptions{Events: []string{EventChat, EventChatMessage}}, clients: map[string]*client{"c1": c}}

	handled, payload, shape := r.handleInternalRequest(c, protocol.RequestFrame{Method: MethodSessionsMessagesSubscribe, Params: json.RawMessage(`{"key":"session-a"}`)})
	if !handled || shape != nil || payload.(map[string]any)["key"] != "session-a" {
		t.Fatalf("subscribe handled=%v payload=%#v error=%+v", handled, payload, shape)
	}
	r.Broadcast(EventChat, ChatDeltaEvent{ChatEventBase: ChatEventBase{RunID: "b", SessionKey: "session-b"}, State: ChatStateDelta, DeltaText: "hidden"})
	select {
	case frame := <-c.eventQueue:
		t.Fatalf("received unrelated session frame: %#v", frame)
	default:
	}
	r.Broadcast(EventChat, ChatDeltaEvent{ChatEventBase: ChatEventBase{RunID: "a", SessionKey: "session-a"}, State: ChatStateDelta, DeltaText: "visible"})
	frame := <-c.eventQueue
	if got := frame.(map[string]any)["payload"].(ChatDeltaEvent); got.SessionKey != "session-a" {
		t.Fatalf("payload=%+v", got)
	}

	handled, _, shape = r.handleInternalRequest(c, protocol.RequestFrame{Method: MethodSessionsMessagesUnsubscribe, Params: json.RawMessage(`{"key":"session-a"}`)})
	if !handled || shape != nil || c.isSubscribed(EventChat) {
		t.Fatalf("unsubscribe handled=%v error=%+v subscriptions=%v", handled, shape, c.listSubscriptions())
	}
}

func TestSessionsViewersSetReplacesConnectionAudience(t *testing.T) {
	c := &client{subscriptions: map[string]struct{}{}, watchedSessions: map[string]struct{}{}, eventQueue: make(chan any, 4), eventDone: make(chan struct{})}
	r := &Runtime{opts: RuntimeOptions{Events: []string{EventChat, EventChatMessage}}, clients: map[string]*client{"c1": c}}
	handled, payload, shape := r.handleInternalRequest(c, protocol.RequestFrame{Method: MethodSessionsViewersSet, Params: json.RawMessage(`{"sessionKeys":["session-a","session-a"]}`)})
	if !handled || shape != nil {
		t.Fatalf("viewers.set handled=%v payload=%#v error=%+v", handled, payload, shape)
	}
	keys := payload.(map[string]any)["sessionKeys"].([]string)
	if len(keys) != 1 || keys[0] != "session-a" {
		t.Fatalf("normalized keys=%v", keys)
	}
	r.Broadcast(EventChat, ChatDeltaEvent{ChatEventBase: ChatEventBase{RunID: "b", SessionKey: "session-b"}, State: ChatStateDelta})
	select {
	case frame := <-c.eventQueue:
		t.Fatalf("received unrelated session frame: %#v", frame)
	default:
	}
	r.Broadcast(EventChat, ChatDeltaEvent{ChatEventBase: ChatEventBase{RunID: "a", SessionKey: "session-a"}, State: ChatStateDelta})
	<-c.eventQueue

	handled, _, shape = r.handleInternalRequest(c, protocol.RequestFrame{Method: MethodSessionsViewersSet, Params: json.RawMessage(`{"sessionKeys":[]}`)})
	if !handled || shape != nil {
		t.Fatalf("clear viewers handled=%v error=%+v", handled, shape)
	}
	r.Broadcast(EventChat, ChatDeltaEvent{ChatEventBase: ChatEventBase{RunID: "a2", SessionKey: "session-a"}, State: ChatStateDelta})
	select {
	case frame := <-c.eventQueue:
		t.Fatalf("received frame after empty viewers declaration: %#v", frame)
	default:
	}
}

func TestSessionMessagesSubscriptionRejectsMissingKey(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{Events: []string{EventChat}}}
	c := &client{subscriptions: map[string]struct{}{}, watchedSessions: map[string]struct{}{}}
	handled, _, shape := r.handleInternalRequest(c, protocol.RequestFrame{Method: MethodSessionsMessagesSubscribe, Params: json.RawMessage(`{}`)})
	if !handled || shape == nil {
		t.Fatalf("handled=%v error=%+v", handled, shape)
	}
}
