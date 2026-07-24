package whatsappweb

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
	"github.com/coder/websocket/wsjson"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

func TestPluginSurface(t *testing.T) {
	p := &Plugin{}
	if p.ID() != "whatsappweb" || !strings.Contains(p.Type(), "Unofficial") {
		t.Fatalf("unexpected identity: %q %q", p.ID(), p.Type())
	}
	caps := p.Capabilities()
	if !caps.Typing || !caps.Reactions || !caps.Threads || !caps.MultiAccount || caps.Audio || caps.Edit {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	methods := p.GatewayMethods()
	if len(methods) != 8 {
		t.Fatalf("gateway methods=%d, want 8", len(methods))
	}
	want := []string{
		"whatsappweb.send", "whatsappweb.typing",
		"whatsappweb.add_reaction", "whatsappweb.remove_reaction",
		"whatsappweb.auth_status", "whatsappweb.auth_qr",
		"whatsappweb.auth_pair_code", "whatsappweb.logout",
	}
	for i := range want {
		if methods[i].Method != want[i] {
			t.Fatalf("method[%d]=%q want %q", i, methods[i].Method, want[i])
		}
	}
	var _ sdk.ChannelPlugin = p
	var _ sdk.ChannelPluginWithMethods = p
}

func TestBridgeAccountValidation(t *testing.T) {
	valid, err := bridgeAccountFromParams(map[string]any{
		"bridge_url": "http://127.0.0.1:18789/base", "account_id": "personal",
	}, "")
	if err != nil || valid.AccountID != "personal" || valid.ReactionLevel != "minimal" {
		t.Fatalf("valid loopback rejected: account=%+v err=%v", valid, err)
	}
	cases := []map[string]any{
		{},
		{"bridge_url": "https://bridge.example", "account_id": "p"},
		{"bridge_url": "http://bridge.example", "account_id": "p", "allow_remote_bridge": true, "bridge_token": "x"},
		{"bridge_url": "https://bridge.example?q=x", "account_id": "p", "allow_remote_bridge": true, "bridge_token": "x"},
		{"bridge_url": "http://127.0.0.1", "account_id": "p", "reaction_level": "everything"},
	}
	for i, cfg := range cases {
		if _, err := bridgeAccountFromParams(cfg, ""); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestHandleEventDeduplicatesAndPreservesGroupParticipant(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	b := &bot{
		channelID: "personal",
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		seen: map[string]struct{}{}, messageRefs: map[string]string{}, activeTyping: map[string]bool{},
	}
	event := bridgeEvent{
		Type: "message", EventID: "evt-1", MessageRef: "opaque",
		ChatJID: "group@g.us", ThreadID: "group@g.us", SenderJID: "participant@s.whatsapp.net",
		Text: "hello", Timestamp: 42, ReplyToEventID: "evt-0",
	}
	b.handleEvent(event)
	b.handleEvent(event)
	b.handleEvent(bridgeEvent{Type: "message", EventID: "bad", MessageRef: "r", ThreadID: "not-a-group", SenderJID: "p", Text: "no"})
	if len(delivered) != 1 {
		t.Fatalf("delivered=%+v", delivered)
	}
	got := delivered[0]
	if got.SenderID != "participant@s.whatsapp.net" || got.ThreadID != "group@g.us" || got.ReplyToEventID != "evt-0" || got.CreatedAt != 42 {
		t.Fatalf("unexpected inbound: %+v", got)
	}
	if b.messageRefs["evt-1"] != "opaque" {
		t.Fatalf("message reference not retained: %+v", b.messageRefs)
	}
}

func TestConnectAndOutboundLifecycleAgainstMockBridge(t *testing.T) {
	var mu sync.Mutex
	var operations []string
	eventsAccepted := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/events") {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			eventsAccepted <- struct{}{}
			_ = wsjson.Write(r.Context(), conn, bridgeEvent{
				Type: "message", EventID: "evt", MessageRef: "ref",
				ChatJID: "1555@s.whatsapp.net", SenderJID: "1555@s.whatsapp.net", Text: "inbound", Timestamp: 9,
			})
			<-r.Context().Done()
			_ = conn.Close(websocket.StatusNormalClosure, "done")
			return
		}
		var body struct {
			Input map[string]any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if strings.HasSuffix(r.URL.Path, "/session/start") {
			op = "session/start"
		}
		if strings.HasSuffix(r.URL.Path, "/session/stop") {
			op = "session/stop"
		}
		mu.Lock()
		operations = append(operations, op)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": "m1"}})
	}))
	defer srv.Close()

	inbound := make(chan sdk.InboundChannelMessage, 1)
	handle, err := (&Plugin{}).Connect(context.Background(), "personal", map[string]any{
		"bridge_url": srv.URL, "bridge_token": "secret", "default_to": "+15551234567",
	}, func(msg sdk.InboundChannelMessage) { inbound <- msg })
	if err != nil {
		t.Fatal(err)
	}
	<-eventsAccepted
	if msg := <-inbound; msg.SenderID != "1555@s.whatsapp.net" {
		t.Fatalf("unexpected inbound: %+v", msg)
	}
	typed := handle.(sdk.TypingHandle)
	if err := typed.SendTyping(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := handle.Send(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	threaded := handle.(sdk.ThreadHandle)
	if err := threaded.SendInThread(context.Background(), "group@g.us", "group hello"); err != nil {
		t.Fatal(err)
	}
	handle.Close()
	handle.Close()

	mu.Lock()
	defer mu.Unlock()
	got := strings.Join(operations, ",")
	if !strings.Contains(got, "session/start") || !strings.Contains(got, "typing,messages,typing") || !strings.Contains(got, "session/stop") {
		t.Fatalf("unexpected operation lifecycle: %s", got)
	}
}

func TestReactionLevelsAndOpaqueReference(t *testing.T) {
	if err := validateReaction("off", "👍"); err == nil {
		t.Fatal("off reaction level unexpectedly allowed reaction")
	}
	if err := validateReaction("ack", "✅"); err == nil {
		t.Fatal("ack reaction level unexpectedly allowed agent reaction")
	}
	if err := validateReaction("minimal", "🧪"); err == nil {
		t.Fatal("minimal reaction level unexpectedly allowed arbitrary reaction")
	}
	if err := validateReaction("extensive", "🧪"); err != nil {
		t.Fatalf("extensive reaction rejected: %v", err)
	}

	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input map[string]any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		received = body.Input
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	b := &bot{
		account:    bridgeAccount{BaseURL: u, AccountID: "p", ReactionLevel: "minimal"},
		httpClient: srv.Client(), messageRefs: map[string]string{"evt": "opaque"},
		activeTyping: map[string]bool{},
	}
	if err := b.AddReaction(context.Background(), "evt", "👍"); err != nil {
		t.Fatal(err)
	}
	if received["message_ref"] != "opaque" || received["remove"] != false {
		t.Fatalf("unexpected reaction payload: %+v", received)
	}
}

func TestGatewayAccountResolution(t *testing.T) {
	channels.ConfigureChannelAccounts(state.NostrChannelsConfig{
		"personal": {Kind: "whatsappweb", Config: map[string]any{
			"bridge_url": "http://127.0.0.1:18789", "default_to": "15551234567", "default_account": true,
		}},
	})
	t.Cleanup(func() { channels.ConfigureChannelAccounts(nil) })
	method := (&Plugin{}).GatewayMethods()[0]
	_, err := method.Handle(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "text") {
		t.Fatalf("expected post-resolution text validation, got %v", err)
	}
}

func TestPairCodeNormalizesPhoneWithoutEcho(t *testing.T) {
	var phone string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input map[string]any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		phone, _ = body.Input["phone_number"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"pairing_code": "1234-5678"}})
	}))
	defer srv.Close()
	channels.ConfigureChannelAccounts(state.NostrChannelsConfig{
		"personal": {Kind: "whatsappweb", Config: map[string]any{
			"bridge_url": srv.URL, "default_account": true,
		}},
	})
	t.Cleanup(func() { channels.ConfigureChannelAccounts(nil) })
	method := (&Plugin{}).GatewayMethods()[6]
	result, err := method.Handle(context.Background(), map[string]any{
		"account_id": "personal", "phone_number": "+1 (555) 123-4567",
	})
	if err != nil {
		t.Fatal(err)
	}
	if phone != "15551234567" || result["account_id"] != "personal" {
		t.Fatalf("phone=%q result=%+v", phone, result)
	}
}
