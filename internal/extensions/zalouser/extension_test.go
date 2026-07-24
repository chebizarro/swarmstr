package zalouser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

func TestZaloUserPluginSurface(t *testing.T) {
	p := &ZaloUserPlugin{}
	if p.ID() != "zalouser" || p.Type() == "" {
		t.Fatalf("unexpected identity: %q %q", p.ID(), p.Type())
	}
	caps := p.Capabilities()
	if !caps.Typing || !caps.Reactions || !caps.Threads || !caps.MultiAccount {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	if got := len(p.GatewayMethods()); got != 4 {
		t.Fatalf("expected 4 gateway methods, got %d", got)
	}
	var _ sdk.ChannelPlugin = p
	var _ sdk.ChannelPluginWithMethods = p
}

func TestBridgeAccountValidation(t *testing.T) {
	valid, err := bridgeAccountFromParams(map[string]any{"bridge_url": "http://127.0.0.1:18080/base", "profile": "default"})
	if err != nil || valid.Profile != "default" || valid.DefaultChat != "direct" {
		t.Fatalf("valid loopback config rejected: account=%+v err=%v", valid, err)
	}
	cases := []map[string]any{
		{},
		{"bridge_url": "https://bridge.example", "profile": "p"},
		{"bridge_url": "http://bridge.example", "profile": "p", "allow_remote_bridge": true, "bridge_token": "x"},
		{"bridge_url": "https://bridge.example?q=x", "profile": "p", "allow_remote_bridge": true, "bridge_token": "x"},
		{"bridge_url": "http://127.0.0.1", "profile": "p", "default_chat_type": "room"},
	}
	for i, cfg := range cases {
		if _, err := bridgeAccountFromParams(cfg); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestConnectUsesAuthenticatedWebSocket(t *testing.T) {
	accepted := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/profiles/work/events") {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepted <- r.Header.Clone()
		<-r.Context().Done()
		_ = conn.Close(websocket.StatusNormalClosure, "done")
	}))
	defer srv.Close()

	handle, err := (&ZaloUserPlugin{}).Connect(context.Background(), "zalo-main", map[string]any{
		"bridge_url": srv.URL, "bridge_token": "secret", "profile": "work",
	}, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	header := <-accepted
	if header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("missing bridge auth: %q", header.Get("Authorization"))
	}
	handle.Close()
}

func TestHandleEventFiltersDeduplicatesAndPreservesGroupThread(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &zaloUserBot{
		channelID: "z", allowedSenders: map[string]bool{"user-1": true},
		onMessage: func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		seen:      map[string]struct{}{}, messageRefs: map[string]string{}, activeTyping: map[string]string{},
	}
	event := bridgeEvent{Type: "message", EventID: "evt-1", MessageRef: "opaque-ref", ThreadID: "group-1", ChatType: "group", SenderID: "user-1", Text: "hello", Timestamp: 42000}
	bot.handleEvent(event)
	bot.handleEvent(event)
	bot.handleEvent(bridgeEvent{Type: "message", EventID: "evt-2", MessageRef: "r2", ThreadID: "g", SenderID: "blocked", Text: "no"})
	if len(delivered) != 1 || delivered[0].ThreadID != "group-1" || delivered[0].CreatedAt != 42 {
		t.Fatalf("unexpected delivery: %+v", delivered)
	}
	if bot.messageRefs["evt-1"] != "opaque-ref" {
		t.Fatalf("message ref not retained: %+v", bot.messageRefs)
	}
}

func TestSendClearsActiveTyping(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		payload["path"] = r.URL.Path
		mu.Lock()
		requests = append(requests, payload)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"message_id": "m1"})
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	bot := &zaloUserBot{
		channelID: "z", account: bridgeAccount{BaseURL: u, Profile: "p", DefaultChat: "direct"},
		httpClient: srv.Client(), done: make(chan struct{}), seen: map[string]struct{}{}, messageRefs: map[string]string{}, activeTyping: map[string]string{},
		cancel: func() {},
	}
	ctx := sdk.WithChannelReplyTarget(context.Background(), "user-1")
	if err := bot.SendTyping(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := bot.Send(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 || requests[0]["typing"] != true || !strings.HasSuffix(requests[1]["path"].(string), "/messages") || requests[2]["typing"] != false {
		t.Fatalf("unexpected lifecycle requests: %+v", requests)
	}
}

func TestReactionUsesOpaqueReference(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	bot := &zaloUserBot{
		account: bridgeAccount{BaseURL: u, Profile: "p", DefaultChat: "direct"}, httpClient: srv.Client(),
		seen: map[string]struct{}{}, messageRefs: map[string]string{"evt": "opaque"}, activeTyping: map[string]string{},
	}
	if err := bot.AddReaction(context.Background(), "evt", "👍"); err != nil {
		t.Fatal(err)
	}
	if received["message_ref"] != "opaque" || received["remove"] != false {
		t.Fatalf("unexpected reaction payload: %+v", received)
	}
	if err := bot.RemoveReaction(context.Background(), "missing", "👍"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired reference error, got %v", err)
	}
}

func TestSendInThreadUsesGroupConversation(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	bot := &zaloUserBot{
		account: bridgeAccount{BaseURL: u, Profile: "p", DefaultChat: "direct"}, httpClient: srv.Client(),
		activeTyping: map[string]string{},
	}
	var _ sdk.ThreadHandle = bot
	if err := bot.SendInThread(context.Background(), "group-42", "hello group"); err != nil {
		t.Fatal(err)
	}
	if received["to"] != "group-42" || received["chat_type"] != "group" || received["text"] != "hello group" {
		t.Fatalf("unexpected thread payload: %+v", received)
	}
}

func TestGatewayAccountResolution(t *testing.T) {
	channels.ConfigureChannelAccounts(state.NostrChannelsConfig{"personal": {Kind: "zalouser", Config: map[string]any{
		"bridge_url": "http://127.0.0.1:18888", "profile": "p", "default_to": "u1", "default_account": true,
	}}})
	t.Cleanup(func() { channels.ConfigureChannelAccounts(nil) })
	method := (&ZaloUserPlugin{}).GatewayMethods()[0]
	_, err := method.Handle(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "text") {
		t.Fatalf("expected post-resolution text validation, got %v", err)
	}
}
