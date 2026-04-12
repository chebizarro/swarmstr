package zalo

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &ZaloPlugin{}
	if id := p.ID(); id != "zalo" {
		t.Fatalf("expected zalo, got %s", id)
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &ZaloPlugin{}
	if typ := p.Type(); typ != "Zalo OA" {
		t.Fatalf("expected Zalo OA, got %s", typ)
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &ZaloPlugin{}
	schema := p.ConfigSchema()
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"app_id", "app_secret", "refresh_token", "oa_id"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &ZaloPlugin{}
	_ = p.Capabilities() // no panic
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &ZaloPlugin{}
	if methods := p.GatewayMethods(); methods != nil {
		t.Errorf("expected nil, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*ZaloPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &zaloBot{channelID: "zalo-1"}
	if b.ID() != "zalo-1" {
		t.Errorf("expected zalo-1, got %s", b.ID())
	}
}

func TestBotGetToken_Empty(t *testing.T) {
	b := &zaloBot{}
	if tok := b.getToken(); tok != "" {
		t.Errorf("expected empty token, got %q", tok)
	}
}

func TestVerifySignature_Valid(t *testing.T) {
	secret := "test-app-secret"
	body := []byte(`{"event_name":"user_send_text"}`)

	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	mac := hex.EncodeToString(h.Sum(nil))

	b := &zaloBot{appSecret: secret}
	if !b.verifySignature(body, mac) {
		t.Error("expected valid signature")
	}
}

func TestVerifySignature_Invalid(t *testing.T) {
	b := &zaloBot{appSecret: "secret"}
	if b.verifySignature([]byte("body"), "deadbeef") {
		t.Error("expected invalid signature")
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func zaloMAC(secret string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func zaloEvent(eventName, senderID, oaID, msgID, text string, ts int64) []byte {
	data, _ := json.Marshal(map[string]any{
		"event_name": eventName,
		"timestamp":  ts,
		"sender":     map[string]string{"id": senderID},
		"recipient":  map[string]string{"id": oaID},
		"message":    map[string]string{"msg_id": msgID, "text": text},
	})
	return data
}

// ─── Connect ──────────────────────────────────────────────────────────────────

func TestConnect_MissingRequiredConfig(t *testing.T) {
	p := &ZaloPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []map[string]any{
		{"app_secret": "s", "refresh_token": "r", "oa_id": "o"},
		{"app_id": "a", "refresh_token": "r", "oa_id": "o"},
		{"app_id": "a", "app_secret": "s", "oa_id": "o"},
		{"app_id": "a", "app_secret": "s", "refresh_token": "r"},
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
	p := &ZaloPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := map[string]any{
		"app_id":        "aid",
		"app_secret":    "asec",
		"refresh_token": "rt",
		"oa_id":         "oa1",
	}
	handle, err := p.Connect(ctx, "zalo-test", cfg, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	handle.Close()
}

// ─── handleEvent ──────────────────────────────────────────────────────────────

func TestHandleEvent_DeliversTextMessage(t *testing.T) {
	secret := "app-secret"
	var delivered []sdk.InboundChannelMessage
	bot := &zaloBot{
		channelID:      "zalo-ch",
		appSecret:      secret,
		oaID:           "oa1",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body := zaloEvent("user_send_text", "sender-1", "oa1", "msg-1", "xin chào", 1700000000000)
	req := httptest.NewRequest(http.MethodPost, "/webhook?mac="+zaloMAC(secret, body), bytes.NewReader(body))
	w := httptest.NewRecorder()

	bot.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(delivered) != 1 {
		t.Fatalf("expected 1 message, got %d", len(delivered))
	}
	if delivered[0].Text != "xin chào" {
		t.Fatalf("wrong text: %s", delivered[0].Text)
	}
	if delivered[0].SenderID != "sender-1" {
		t.Fatalf("wrong senderID: %s", delivered[0].SenderID)
	}
}

func TestHandleEvent_SkipsWrongEventName(t *testing.T) {
	secret := "sec"
	var delivered []sdk.InboundChannelMessage
	bot := &zaloBot{
		channelID:      "zalo-ch",
		appSecret:      secret,
		oaID:           "oa1",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body := zaloEvent("user_followed", "sender-1", "oa1", "", "", 0)
	req := httptest.NewRequest(http.MethodPost, "/webhook?mac="+zaloMAC(secret, body), bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if len(delivered) != 0 {
		t.Fatal("expected 0 (wrong event)")
	}
}

func TestHandleEvent_SkipsWrongOAID(t *testing.T) {
	secret := "sec"
	var delivered []sdk.InboundChannelMessage
	bot := &zaloBot{
		channelID:      "zalo-ch",
		appSecret:      secret,
		oaID:           "oa1",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body := zaloEvent("user_send_text", "sender-1", "oa-other", "msg-1", "text", 0)
	req := httptest.NewRequest(http.MethodPost, "/webhook?mac="+zaloMAC(secret, body), bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if len(delivered) != 0 {
		t.Fatal("expected 0 (wrong OA)")
	}
}

func TestHandleEvent_BadMAC(t *testing.T) {
	bot := &zaloBot{
		channelID: "zalo-ch",
		appSecret: "real-secret",
		done:      make(chan struct{}),
	}
	body := zaloEvent("user_send_text", "s", "o", "m", "t", 0)
	req := httptest.NewRequest(http.MethodPost, "/webhook?mac=badmac", bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleEvent_MissingMAC(t *testing.T) {
	bot := &zaloBot{channelID: "ch", appSecret: "s", done: make(chan struct{})}
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleEvent_MACFromHeader(t *testing.T) {
	secret := "sec"
	var delivered []sdk.InboundChannelMessage
	bot := &zaloBot{
		channelID:      "zalo-ch",
		appSecret:      secret,
		oaID:           "oa1",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body := zaloEvent("user_send_text", "sender-1", "oa1", "msg-1", "header mac", 0)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Zalo-Mac", zaloMAC(secret, body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if len(delivered) != 1 {
		t.Fatalf("expected 1 (mac from header), got %d", len(delivered))
	}
}

func TestHandleEvent_AllowedSendersFilter(t *testing.T) {
	secret := "sec"
	var delivered []sdk.InboundChannelMessage
	bot := &zaloBot{
		channelID:      "zalo-ch",
		appSecret:      secret,
		oaID:           "oa1",
		allowedSenders: map[string]bool{"allowed-user": true},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body := zaloEvent("user_send_text", "blocked-user", "oa1", "m1", "nope", 0)
	req := httptest.NewRequest(http.MethodPost, "/webhook?mac="+zaloMAC(secret, body), bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if len(delivered) != 0 {
		t.Fatal("expected 0 (filtered)")
	}
}

func TestHandleEvent_EmptyText(t *testing.T) {
	secret := "sec"
	var delivered []sdk.InboundChannelMessage
	bot := &zaloBot{
		channelID:      "zalo-ch",
		appSecret:      secret,
		oaID:           "oa1",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body := zaloEvent("user_send_text", "s1", "oa1", "m1", "  ", 0)
	req := httptest.NewRequest(http.MethodPost, "/webhook?mac="+zaloMAC(secret, body), bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if len(delivered) != 0 {
		t.Fatal("expected 0 (empty text)")
	}
}

func TestHandleEvent_MethodNotAllowed(t *testing.T) {
	bot := &zaloBot{done: make(chan struct{})}
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// ─── sendCS ───────────────────────────────────────────────────────────────────

func TestSendCS_PostsToAPI(t *testing.T) {
	var capturedBody []byte
	bot := &zaloBot{
		accessToken: "test-token",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedBody, _ = io.ReadAll(req.Body)
			return &http.Response{
				StatusCode: 200,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":0}`)),
			}, nil
		})},
	}
	err := bot.sendCS(context.Background(), "recipient-1", "hello zalo")
	if err != nil {
		t.Fatalf("sendCS: %v", err)
	}
	if !strings.Contains(string(capturedBody), `"text":"hello zalo"`) {
		t.Fatalf("unexpected body: %s", capturedBody)
	}
}

func TestSendCS_ReturnsErrorOnAPIError(t *testing.T) {
	bot := &zaloBot{
		accessToken: "tok",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":-201,"message":"invalid token"}`)),
			}, nil
		})},
	}
	err := bot.sendCS(context.Background(), "r", "text")
	if err == nil {
		t.Fatal("expected error on API error")
	}
}

func TestSend_RequiresReplyTarget(t *testing.T) {
	bot := &zaloBot{}
	err := bot.Send(context.Background(), "text")
	if err == nil {
		t.Fatal("expected error with no reply target")
	}
}

// ─── Token management ─────────────────────────────────────────────────────────

func TestRefreshAccessToken_ParsesResponse(t *testing.T) {
	bot := &zaloBot{
		appID:     "aid",
		appSecret: "asec",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"access_token":"new-at","refresh_token":"new-rt","expires_in":3600}`)),
			}, nil
		})},
	}
	err := bot.refreshAccessToken(context.Background())
	if err != nil {
		t.Fatalf("refreshAccessToken: %v", err)
	}
	if bot.getToken() != "new-at" {
		t.Fatalf("expected new-at, got %s", bot.getToken())
	}
}

func TestRefreshAccessToken_ReturnsErrorOnAPIError(t *testing.T) {
	bot := &zaloBot{
		appID:     "aid",
		appSecret: "asec",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":-100,"message":"bad refresh"}`)),
			}, nil
		})},
	}
	err := bot.refreshAccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error on API error")
	}
}

// ─── Close ────────────────────────────────────────────────────────────────────

func TestBotClose_Idempotent(t *testing.T) {
	bot := &zaloBot{channelID: "zalo-close", done: make(chan struct{})}
	bot.Close()
	bot.Close() // should not panic
}

// ─── HandleWebhook registry ──────────────────────────────────────────────────

func TestHandleWebhook_UnknownChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	w := httptest.NewRecorder()
	HandleWebhook("nonexistent", w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
