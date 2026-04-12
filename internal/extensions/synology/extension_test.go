package synology

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &SynologyPlugin{}
	if id := p.ID(); id != "synology-chat" {
		t.Fatalf("expected synology-chat, got %s", id)
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &SynologyPlugin{}
	if typ := p.Type(); typ != "Synology Chat" {
		t.Fatalf("expected Synology Chat, got %s", typ)
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &SynologyPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("ConfigSchema must not be nil")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"webhook_url", "incoming_token"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &SynologyPlugin{}
	_ = p.Capabilities() // no panic; minimal capabilities
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &SynologyPlugin{}
	if methods := p.GatewayMethods(); methods != nil {
		t.Errorf("expected nil, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*SynologyPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &synologyBot{channelID: "syn-1"}
	if b.ID() != "syn-1" {
		t.Errorf("expected syn-1, got %s", b.ID())
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// ─── Connect ──────────────────────────────────────────────────────────────────

func TestConnect_MissingRequiredConfig(t *testing.T) {
	p := &SynologyPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []map[string]any{
		{"incoming_token": "tok"},
		{"webhook_url": "http://x"},
		{},
	}
	for _, cfg := range cases {
		_, err := p.Connect(ctx, "ch", cfg, func(sdk.InboundChannelMessage) {})
		if err == nil {
			t.Errorf("expected error for config %v", cfg)
		}
	}
}

func TestConnect_ValidConfig(t *testing.T) {
	p := &SynologyPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := map[string]any{
		"webhook_url":    "http://nas.test/webhook",
		"incoming_token": "secret-token",
	}
	handle, err := p.Connect(ctx, "syn-test", cfg, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	handle.Close()
}

// ─── handlePush: JSON body ────────────────────────────────────────────────────

func TestHandlePush_JSONBody(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &synologyBot{
		channelID:      "syn-ch",
		incomingToken:  "tok",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body, _ := json.Marshal(map[string]string{
		"token":    "tok",
		"user_id":  "42",
		"username": "alice",
		"text":     "hello synology",
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	bot.handlePush(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(delivered) != 1 || delivered[0].Text != "hello synology" {
		t.Fatalf("expected message, got %+v", delivered)
	}
	if delivered[0].SenderID != "alice" {
		t.Fatalf("expected senderID=alice, got %s", delivered[0].SenderID)
	}
}

// ─── handlePush: form-encoded body ───────────────────────────────────────────

func TestHandlePush_FormEncoded(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &synologyBot{
		channelID:      "syn-ch",
		incomingToken:  "tok",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	form := url.Values{
		"token":    {"tok"},
		"user_id":  {"42"},
		"username": {"bob"},
		"text":     {"form hello"},
	}
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	bot.handlePush(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(delivered) != 1 || delivered[0].Text != "form hello" {
		t.Fatalf("expected form message, got %+v", delivered)
	}
}

// ─── handlePush: bad token ───────────────────────────────────────────────────

func TestHandlePush_BadToken(t *testing.T) {
	bot := &synologyBot{
		channelID:      "syn-ch",
		incomingToken:  "correct-token",
		allowedSenders: map[string]bool{},
		onMessage:      func(sdk.InboundChannelMessage) { t.Fatal("should not deliver") },
		done:           make(chan struct{}),
	}
	body, _ := json.Marshal(map[string]string{
		"token": "wrong-token",
		"text":  "sneaky",
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	bot.handlePush(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// ─── handlePush: empty text ──────────────────────────────────────────────────

func TestHandlePush_EmptyText(t *testing.T) {
	bot := &synologyBot{
		channelID:      "syn-ch",
		incomingToken:  "tok",
		allowedSenders: map[string]bool{},
		onMessage:      func(sdk.InboundChannelMessage) { t.Fatal("should not deliver") },
		done:           make(chan struct{}),
	}
	body, _ := json.Marshal(map[string]string{"token": "tok", "text": "  "})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	bot.handlePush(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ─── handlePush: allowed_senders filter ──────────────────────────────────────

func TestHandlePush_AllowedSenders(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &synologyBot{
		channelID:      "syn-ch",
		incomingToken:  "tok",
		allowedSenders: map[string]bool{"alice": true},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	// Blocked sender
	body, _ := json.Marshal(map[string]string{"token": "tok", "username": "eve", "text": "blocked"})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if len(delivered) != 0 {
		t.Fatal("expected 0 (filtered)")
	}

	// Allowed sender
	body, _ = json.Marshal(map[string]string{"token": "tok", "username": "alice", "text": "allowed"})
	req = httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	bot.handlePush(w, req)
	if len(delivered) != 1 {
		t.Fatal("expected 1 allowed message")
	}
}

// ─── handlePush: method not allowed ──────────────────────────────────────────

func TestHandlePush_MethodNotAllowed(t *testing.T) {
	bot := &synologyBot{done: make(chan struct{})}
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// ─── handlePush: fallback senderID to user_id ────────────────────────────────

func TestHandlePush_FallbackSenderID(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &synologyBot{
		channelID:      "syn-ch",
		incomingToken:  "tok",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body, _ := json.Marshal(map[string]string{"token": "tok", "user_id": "99", "text": "no username"})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if len(delivered) != 1 || delivered[0].SenderID != "99" {
		t.Fatalf("expected senderID=99 (fallback), got %+v", delivered)
	}
}

// ─── Send ─────────────────────────────────────────────────────────────────────

func TestSend_PostsToAPI(t *testing.T) {
	var capturedBody []byte
	bot := &synologyBot{
		webhookURL: "http://nas.test/webhook",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedBody, _ = io.ReadAll(req.Body)
			return &http.Response{
				StatusCode: 200,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		})},
	}
	err := bot.Send(context.Background(), "hello NAS")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(string(capturedBody), `"text":"hello NAS"`) {
		t.Fatalf("unexpected body: %s", capturedBody)
	}
}

func TestSend_ErrorOnHTTPFailure(t *testing.T) {
	bot := &synologyBot{
		webhookURL: "http://nas.test/webhook",
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
	bot := &synologyBot{channelID: "syn-close", done: make(chan struct{})}
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
