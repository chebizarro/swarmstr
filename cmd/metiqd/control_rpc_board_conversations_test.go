package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/autoreply"
	boardpkg "metiq/internal/gateway/board"
	"metiq/internal/gateway/channels"
	conversationspkg "metiq/internal/gateway/conversations"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

func workspaceSurfaceCall(t *testing.T, h controlRPCHandler, method string, params string) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	result, handled, err := h.handleWorkspaceSurfaceRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, state.ConfigDoc{})
	if !handled {
		t.Fatalf("method %s was not handled by workspace surface dispatch", method)
	}
	return result, err
}

func newBoardTestHandler() controlRPCHandler {
	return newControlRPCHandler(controlRPCDeps{
		boardStore:        boardpkg.NewStore(),
		boardNotices:      boardpkg.NewNoticeDeduper(),
		steeringMailboxes: autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize),
	})
}

func TestBoardRPCLifecycle(t *testing.T) {
	h := newBoardTestHandler()

	result, err := workspaceSurfaceCall(t, h, methods.MethodBoardGet, `{"sessionKey":"sess"}`)
	if err != nil {
		t.Fatalf("board.get: %v", err)
	}
	snap, ok := result.Result.(boardpkg.Snapshot)
	if !ok || snap.Revision != 0 {
		t.Fatalf("unexpected empty snapshot: %#v", result.Result)
	}

	result, err = workspaceSurfaceCall(t, h, methods.MethodBoardUpdate, `{"sessionKey":"sess","ops":[{"kind":"tab_create","tabId":"main","title":"Main"}]}`)
	if err != nil {
		t.Fatalf("board.update: %v", err)
	}
	if snap = result.Result.(boardpkg.Snapshot); snap.Revision != 1 || len(snap.Tabs) != 1 {
		t.Fatalf("unexpected snapshot after update: %+v", snap)
	}

	result, err = workspaceSurfaceCall(t, h, methods.MethodBoardWidgetPut, `{"sessionKey":"sess","name":"chart","title":"Chart","content":{"kind":"html","html":"<p>hi</p>"},"declared":{"tools":["prompt"]}}`)
	if err != nil {
		t.Fatalf("board.widget.put: %v", err)
	}
	snap = result.Result.(boardpkg.Snapshot)
	widget := snap.Widgets[0]
	if widget.GrantState != boardpkg.GrantPending || widget.InstanceID == "" {
		t.Fatalf("unexpected widget: %+v", widget)
	}

	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardWidgetGrant, `{"sessionKey":"sess","name":"chart","decision":"granted","revision":`+jsonInt(widget.Revision)+`,"instanceId":"wrong"}`); err == nil {
		t.Fatal("expected instance conflict")
	}
	result, err = workspaceSurfaceCall(t, h, methods.MethodBoardWidgetGrant, `{"sessionKey":"sess","name":"chart","decision":"granted","revision":`+jsonInt(widget.Revision)+`,"instanceId":"`+widget.InstanceID+`"}`)
	if err != nil {
		t.Fatalf("board.widget.grant: %v", err)
	}
	if snap = result.Result.(boardpkg.Snapshot); snap.Widgets[0].GrantState != boardpkg.GrantGranted {
		t.Fatalf("grant not applied: %+v", snap.Widgets[0])
	}
}

func jsonInt(v int) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func TestBoardEventNoticeSteering(t *testing.T) {
	h := newBoardTestHandler()
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardWidgetPut, `{"sessionKey":"sess","name":"chart","content":{"kind":"html","html":"<p></p>"}}`); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Unknown widget errors.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardEvent, `{"sessionKey":"sess","widget":"nope","payload":1}`); err == nil {
		t.Fatal("expected widget-not-found error")
	}

	// No active run: acknowledged but not appended.
	result, err := workspaceSurfaceCall(t, h, methods.MethodBoardEvent, `{"sessionKey":"sess","widget":"chart","payload":{"clicked":true}}`)
	if err != nil {
		t.Fatalf("board.event: %v", err)
	}
	out := result.Result.(map[string]any)
	if out["ok"] != true || out["appended"] != false {
		t.Fatalf("unexpected board.event result: %+v", out)
	}

	// Active run mailbox receives the notice.
	mailbox := h.deps.steeringMailboxes.Get("sess")
	result, err = workspaceSurfaceCall(t, h, methods.MethodBoardEvent, `{"sessionKey":"sess","widget":"chart","payload":{"clicked":1}}`)
	if err != nil {
		t.Fatalf("board.event with mailbox: %v", err)
	}
	if out = result.Result.(map[string]any); out["appended"] != true {
		t.Fatalf("expected appended notice: %+v", out)
	}
	drained := mailbox.Drain()
	if len(drained) != 1 || !strings.Contains(drained[0].Text, `[dashboard] {"clicked":1} on widget chart`) || drained[0].Source != "board" {
		t.Fatalf("unexpected steering message: %+v", drained)
	}

	// Identical payload within the dedupe window is dropped.
	result, err = workspaceSurfaceCall(t, h, methods.MethodBoardEvent, `{"sessionKey":"sess","widget":"chart","payload":{"clicked":1}}`)
	if err != nil {
		t.Fatalf("board.event dedupe: %v", err)
	}
	if out = result.Result.(map[string]any); out["appended"] != false {
		t.Fatalf("expected deduped event: %+v", out)
	}
}

// fakeConversationChannel implements channels.Channel for nostr conversation sends.
type fakeConversationChannel struct {
	mu   sync.Mutex
	id   string
	sent []string
}

func (f *fakeConversationChannel) ID() string   { return f.id }
func (f *fakeConversationChannel) Type() string { return "nip29-group" }
func (f *fakeConversationChannel) Send(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}
func (f *fakeConversationChannel) Close() {}

func newConversationsTestHandler(t *testing.T) (controlRPCHandler, *fakeConversationChannel) {
	t.Helper()
	registry := channels.NewRegistry()
	ch := &fakeConversationChannel{id: "relay.example'group"}
	if err := registry.Add(ch); err != nil {
		t.Fatalf("add channel: %v", err)
	}
	h := newControlRPCHandler(controlRPCDeps{
		conversations: conversationspkg.NewRegistry(),
		channels:      registry,
	})
	return h, ch
}

func TestConversationsListAndSendNostrGroup(t *testing.T) {
	h, ch := newConversationsTestHandler(t)

	result, err := workspaceSurfaceCall(t, h, methods.MethodConversationsList, `{"agentId":"main"}`)
	if err != nil {
		t.Fatalf("conversations.list: %v", err)
	}
	listing := result.Result.(map[string]any)["conversations"].([]conversationspkg.Conversation)
	if len(listing) != 1 || listing[0].Channel != "nostr" || listing[0].Target != ch.ID() {
		t.Fatalf("unexpected conversations: %+v", listing)
	}
	ref := listing[0].ConversationRef

	result, err = workspaceSurfaceCall(t, h, methods.MethodConversationsSend, `{"agentId":"main","operationId":"op-1","conversationRef":"`+ref+`","message":"hello group"}`)
	if err != nil {
		t.Fatalf("conversations.send: %v", err)
	}
	sent := result.Result.(map[string]any)
	if sent["status"] != "sent" || sent["channel"] != "nostr" || sent["conversationRef"] != ref {
		t.Fatalf("unexpected send result: %+v", sent)
	}
	if len(ch.sent) != 1 || ch.sent[0] != "hello group" {
		t.Fatalf("channel did not receive message: %+v", ch.sent)
	}

	// Same operation id with identical input replays without re-sending.
	result, err = workspaceSurfaceCall(t, h, methods.MethodConversationsSend, `{"agentId":"main","operationId":"op-1","conversationRef":"`+ref+`","message":"hello group"}`)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay := result.Result.(map[string]any); replay["messageId"] != sent["messageId"] {
		t.Fatalf("expected cached replay, got %+v", replay)
	}
	if len(ch.sent) != 1 {
		t.Fatalf("replay must not re-send: %+v", ch.sent)
	}

	// Same operation id with different input is rejected.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodConversationsSend, `{"agentId":"main","operationId":"op-1","conversationRef":"`+ref+`","message":"different"}`); err == nil {
		t.Fatal("expected identity conflict")
	}

	// Unknown conversation is rejected.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodConversationsSend, `{"agentId":"main","operationId":"op-2","conversationRef":"conv_00000000000000000000000000000000","message":"x"}`); err == nil {
		t.Fatal("expected conversation-not-found error")
	}
}

func TestConversationsTurnReplyAndCancel(t *testing.T) {
	h, ch := newConversationsTestHandler(t)
	registry := h.deps.conversations

	// Observe a direct extension conversation is unnecessary for the nostr
	// group flow: list to register the group ref.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodConversationsList, `{"agentId":"main"}`); err != nil {
		t.Fatalf("list: %v", err)
	}
	ref := conversationspkg.BuildRef("nostr", "default", conversationspkg.KindGroup, ch.ID(), "")

	type turnOutcome struct {
		result nostruntime.ControlRPCResult
		err    error
	}
	done := make(chan turnOutcome, 1)
	go func() {
		result, err := workspaceSurfaceCall(t, h, methods.MethodConversationsTurn, `{"agentId":"main","turnId":"t-1","conversationRef":"`+ref+`","message":"ping","timeoutMs":5000}`)
		done <- turnOutcome{result, err}
	}()

	// Wait for the waiter to register, then deliver the correlated reply.
	deadline := time.Now().Add(2 * time.Second)
	for !registry.HasPendingTurn(ref) {
		if time.Now().After(deadline) {
			t.Fatal("turn waiter never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !registry.NotifyInbound(ref, conversationspkg.Reply{MessageID: "m-9", Text: "pong", Timestamp: 42}) {
		t.Fatal("reply was not consumed")
	}
	outcome := <-done
	if outcome.err != nil {
		t.Fatalf("conversations.turn: %v", outcome.err)
	}
	turnResult := outcome.result.Result.(map[string]any)
	if turnResult["status"] != "replied" || turnResult["correlationPersisted"] != false {
		t.Fatalf("unexpected turn result: %+v", turnResult)
	}
	reply := turnResult["reply"].(map[string]any)
	if reply["text"] != "pong" || reply["messageId"] != "m-9" {
		t.Fatalf("unexpected reply payload: %+v", reply)
	}
	if len(ch.sent) != 1 || ch.sent[0] != "ping" {
		t.Fatalf("turn message not delivered: %+v", ch.sent)
	}

	// Short timeout with no reply.
	result, err := workspaceSurfaceCall(t, h, methods.MethodConversationsTurn, `{"agentId":"main","turnId":"t-2","conversationRef":"`+ref+`","message":"ping2","timeoutMs":20}`)
	if err != nil {
		t.Fatalf("turn timeout: %v", err)
	}
	if got := result.Result.(map[string]any); got["status"] != "timeout" {
		t.Fatalf("expected timeout status: %+v", got)
	}

	// Cancel with no pending turn returns cancelled=false.
	result, err = workspaceSurfaceCall(t, h, methods.MethodConversationsTurnCancel, `{"agentId":"main","turnId":"t-2"}`)
	if err != nil {
		t.Fatalf("turn.cancel: %v", err)
	}
	if got := result.Result.(map[string]any); got["cancelled"] != false {
		t.Fatalf("expected cancelled=false: %+v", got)
	}

	// Cancel an in-flight turn.
	go func() {
		result, err := workspaceSurfaceCall(t, h, methods.MethodConversationsTurn, `{"agentId":"main","turnId":"t-3","conversationRef":"`+ref+`","message":"ping3","timeoutMs":60000}`)
		done <- turnOutcome{result, err}
	}()
	deadline = time.Now().Add(2 * time.Second)
	for !registry.HasPendingTurn(ref) {
		if time.Now().After(deadline) {
			t.Fatal("cancel-target waiter never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	result, err = workspaceSurfaceCall(t, h, methods.MethodConversationsTurnCancel, `{"agentId":"main","turnId":"t-3"}`)
	if err != nil {
		t.Fatalf("turn.cancel in-flight: %v", err)
	}
	if got := result.Result.(map[string]any); got["cancelled"] != true {
		t.Fatalf("expected cancelled=true: %+v", got)
	}
	outcome = <-done
	if outcome.err != nil {
		t.Fatalf("cancelled turn errored: %v", outcome.err)
	}
	if got := outcome.result.Result.(map[string]any); got["status"] != "unknown" {
		t.Fatalf("expected unknown status for cancelled turn: %+v", got)
	}
}

// fakeMediaHandle records SendMedia calls to prove conversations.send routes
// media through the shared outbound media dispatch helper.
type fakeMediaHandle struct {
	mu    sync.Mutex
	texts []string
	media []sdk.DirectTextMediaPayload
}

func (f *fakeMediaHandle) ID() string { return "acct-1" }
func (f *fakeMediaHandle) Send(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	return nil
}
func (f *fakeMediaHandle) Close() {}
func (f *fakeMediaHandle) SendMedia(_ context.Context, payload sdk.DirectTextMediaPayload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.media = append(f.media, payload)
	return nil
}

func TestDeliverExtensionConversationMessage(t *testing.T) {
	record := conversationspkg.Conversation{
		ConversationRef: "conv_x",
		Channel:         "telegram",
		AccountID:       "acct-1",
		Kind:            conversationspkg.KindDirect,
		Target:          "user-9",
	}
	raw := &fakeMediaHandle{}
	wrapped := channelHandleFunc(func(ctx context.Context, text string) error { return raw.Send(ctx, text) })

	// Plain text goes through Handle.Send.
	method, err := deliverExtensionConversationMessage(context.Background(), wrapped, raw, record, "hello there", nil)
	if err != nil || method != "text" {
		t.Fatalf("text delivery: method=%s err=%v", method, err)
	}
	if len(raw.texts) != 1 || raw.texts[0] != "hello there" {
		t.Fatalf("unexpected text sends: %+v", raw.texts)
	}

	// Media markers dispatch through dispatchChannelMediaReply → SendMedia
	// with the conversation target as recipient.
	method, err = deliverExtensionConversationMessage(context.Background(), wrapped, raw, record, toolbuiltin.MediaPrefix+"/tmp/pic.png", nil)
	if err != nil || method != "media" {
		t.Fatalf("media delivery: method=%s err=%v", method, err)
	}
	if len(raw.media) != 1 || raw.media[0].To != "user-9" || raw.media[0].Media[0].Path != "/tmp/pic.png" {
		t.Fatalf("unexpected media payload: %+v", raw.media)
	}
	if len(raw.texts) != 1 {
		t.Fatalf("media delivery must not also send text: %+v", raw.texts)
	}
}

// channelHandleFunc adapts a func to channels.Channel for tests.
type channelHandleFunc func(ctx context.Context, text string) error

func (f channelHandleFunc) ID() string   { return "acct-1" }
func (f channelHandleFunc) Type() string { return "extension" }
func (f channelHandleFunc) Send(ctx context.Context, text string) error {
	return f(ctx, text)
}
func (f channelHandleFunc) Close() {}
