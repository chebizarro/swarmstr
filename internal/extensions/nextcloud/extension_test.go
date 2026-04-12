package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &NextcloudPlugin{}
	if id := p.ID(); id != "nextcloud-talk" {
		t.Fatalf("expected nextcloud-talk, got %s", id)
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &NextcloudPlugin{}
	if typ := p.Type(); typ != "Nextcloud Talk" {
		t.Fatalf("expected Nextcloud Talk, got %s", typ)
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &NextcloudPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("ConfigSchema must not be nil")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"base_url", "username", "app_password", "room_token"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &NextcloudPlugin{}
	caps := p.Capabilities()
	if !caps.Reactions {
		t.Error("expected Reactions capability")
	}
	if !caps.MultiAccount {
		t.Error("expected MultiAccount capability")
	}
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &NextcloudPlugin{}
	if methods := p.GatewayMethods(); methods != nil {
		t.Errorf("expected nil, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*NextcloudPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &nextcloudBot{channelID: "nc-1"}
	if b.ID() != "nc-1" {
		t.Errorf("expected nc-1, got %s", b.ID())
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonHTTPResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func ocsWrap(msgs []ncChatMsg) string {
	data, _ := json.Marshal(map[string]any{"ocs": map[string]any{"data": msgs}})
	return string(data)
}

// ─── Connect ──────────────────────────────────────────────────────────────────

func TestConnect_MissingRequiredConfig(t *testing.T) {
	p := &NextcloudPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []map[string]any{
		{"username": "u", "app_password": "pw", "room_token": "r"},
		{"base_url": "http://x", "app_password": "pw", "room_token": "r"},
		{"base_url": "http://x", "username": "u", "room_token": "r"},
		{"base_url": "http://x", "username": "u", "app_password": "pw"},
		{},
	}
	for _, cfg := range cases {
		_, err := p.Connect(ctx, "ch", cfg, func(sdk.InboundChannelMessage) {})
		if err == nil {
			t.Errorf("expected error for config %v", cfg)
		}
	}
}

func TestConnect_ValidConfig_PollingMode(t *testing.T) {
	p := &NextcloudPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := map[string]any{
		"base_url":     "http://localhost:9999",
		"username":     "bot",
		"app_password": "secret",
		"room_token":   "abc123",
	}
	handle, err := p.Connect(ctx, "nc-test", cfg, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	handle.Close()
}

// ─── pollOnce ─────────────────────────────────────────────────────────────────

func newTestBot(transport roundTripFunc) *nextcloudBot {
	return &nextcloudBot{
		channelID:      "nc-ch",
		baseURL:        "http://nc.test",
		username:       "bot",
		appPassword:    "pw",
		roomToken:      "room1",
		allowedSenders: map[string]bool{},
		done:           make(chan struct{}),
		seenIDs:        map[int64]struct{}{},
		httpClient:     &http.Client{Transport: transport},
	}
}

func TestPollOnce_DeliversMessages(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	var mu sync.Mutex
	bot := newTestBot(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(ocsWrap([]ncChatMsg{
			{ID: 1, ActorID: "alice", Message: "hello", Timestamp: 100},
			{ID: 2, ActorID: "bob", Message: "world", Timestamp: 200},
		})), nil
	}))
	bot.onMessage = func(msg sdk.InboundChannelMessage) {
		mu.Lock()
		delivered = append(delivered, msg)
		mu.Unlock()
	}

	bot.pollOnce(context.Background())
	if len(delivered) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(delivered))
	}
	if bot.lastMsgID != 2 {
		t.Fatalf("expected lastMsgID=2, got %d", bot.lastMsgID)
	}
}

func TestPollOnce_SkipsOwnMessages(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := newTestBot(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(ocsWrap([]ncChatMsg{
			{ID: 1, ActorID: "bot", Message: "my message", Timestamp: 100},
			{ID: 2, ActorID: "alice", Message: "their msg", Timestamp: 200},
		})), nil
	}))
	bot.onMessage = func(msg sdk.InboundChannelMessage) {
		delivered = append(delivered, msg)
	}

	bot.pollOnce(context.Background())
	if len(delivered) != 1 || delivered[0].Text != "their msg" {
		t.Fatalf("expected only alice's message, got %+v", delivered)
	}
}

func TestPollOnce_SkipsSeenMessages(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := newTestBot(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(ocsWrap([]ncChatMsg{
			{ID: 1, ActorID: "alice", Message: "old", Timestamp: 100},
		})), nil
	}))
	bot.seenIDs[1] = struct{}{}
	bot.onMessage = func(msg sdk.InboundChannelMessage) {
		delivered = append(delivered, msg)
	}

	bot.pollOnce(context.Background())
	if len(delivered) != 0 {
		t.Fatalf("expected 0 (seen), got %d", len(delivered))
	}
}

func TestPollOnce_AllowedSendersFilter(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := newTestBot(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(ocsWrap([]ncChatMsg{
			{ID: 1, ActorID: "alice", Message: "hi", Timestamp: 100},
			{ID: 2, ActorID: "eve", Message: "nope", Timestamp: 200},
		})), nil
	}))
	bot.allowedSenders = map[string]bool{"alice": true}
	bot.onMessage = func(msg sdk.InboundChannelMessage) {
		delivered = append(delivered, msg)
	}

	bot.pollOnce(context.Background())
	if len(delivered) != 1 || delivered[0].SenderID != "alice" {
		t.Fatalf("expected only alice, got %+v", delivered)
	}
}

func TestPollOnce_SkipsEmptyText(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := newTestBot(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(ocsWrap([]ncChatMsg{
			{ID: 1, ActorID: "alice", Message: "", Timestamp: 100},
			{ID: 2, ActorID: "alice", Message: "   ", Timestamp: 200},
		})), nil
	}))
	bot.onMessage = func(msg sdk.InboundChannelMessage) {
		delivered = append(delivered, msg)
	}

	bot.pollOnce(context.Background())
	if len(delivered) != 0 {
		t.Fatalf("expected 0 (empty text), got %d", len(delivered))
	}
}

// ─── Webhook push handler ─────────────────────────────────────────────────────

func TestHandlePush_DeliversMessage(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &nextcloudBot{
		channelID:      "nc-push",
		username:       "bot",
		allowedSenders: map[string]bool{},
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done: make(chan struct{}),
	}
	body, _ := json.Marshal(ncChatMsg{ID: 1, ActorID: "alice", Message: "hi from webhook", Timestamp: 300})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	bot.handlePush(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(delivered) != 1 || delivered[0].Text != "hi from webhook" {
		t.Fatalf("expected webhook message, got %+v", delivered)
	}
}

func TestHandlePush_SkipsOwnMessages(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &nextcloudBot{
		channelID:      "nc-push",
		username:       "bot",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body, _ := json.Marshal(ncChatMsg{ID: 1, ActorID: "bot", Message: "own", Timestamp: 100})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if len(delivered) != 0 {
		t.Fatal("expected 0 (own message)")
	}
}

func TestHandlePush_MethodNotAllowed(t *testing.T) {
	bot := &nextcloudBot{done: make(chan struct{})}
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandlePush_AllowedSendersFilter(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &nextcloudBot{
		channelID:      "nc-push",
		username:       "bot",
		allowedSenders: map[string]bool{"alice": true},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body, _ := json.Marshal(ncChatMsg{ID: 1, ActorID: "eve", Message: "blocked", Timestamp: 100})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if len(delivered) != 0 {
		t.Fatal("expected 0 (filtered sender)")
	}
}

// ─── Send ─────────────────────────────────────────────────────────────────────

func TestSend_PostsToAPI(t *testing.T) {
	var capturedURL string
	var capturedBody []byte
	bot := &nextcloudBot{
		baseURL:     "http://nc.test",
		username:    "bot",
		appPassword: "pw",
		roomToken:   "room1",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			capturedBody, _ = io.ReadAll(req.Body)
			return jsonHTTPResponse(`{}`), nil
		})},
	}
	err := bot.Send(context.Background(), "hello NC")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(capturedURL, "/ocs/v2.php/apps/spreed/api/v1/chat/room1") {
		t.Fatalf("unexpected URL: %s", capturedURL)
	}
	if !strings.Contains(string(capturedBody), `"message":"hello NC"`) {
		t.Fatalf("unexpected body: %s", capturedBody)
	}
}

func TestSend_ErrorOnHTTPFailure(t *testing.T) {
	bot := &nextcloudBot{
		baseURL:     "http://nc.test",
		username:    "bot",
		appPassword: "pw",
		roomToken:   "room1",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 500,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		})},
	}
	err := bot.Send(context.Background(), "fail")
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

// ─── Close ────────────────────────────────────────────────────────────────────

func TestBotClose_Idempotent(t *testing.T) {
	bot := &nextcloudBot{channelID: "nc-close", done: make(chan struct{})}
	bot.Close()
	bot.Close() // should not panic
}

// ─── HandleWebhook registry ──────────────────────────────────────────────────

func TestHandleWebhook_UnknownChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	w := httptest.NewRecorder()
	HandleWebhook("nonexistent-channel", w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
