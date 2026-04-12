package line

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &LINEPlugin{}
	if id := p.ID(); id != "line" {
		t.Fatalf("expected line, got %s", id)
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &LINEPlugin{}
	if typ := p.Type(); typ != "LINE" {
		t.Fatalf("expected LINE, got %s", typ)
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &LINEPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("ConfigSchema must not be nil")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"channel_access_token", "channel_secret"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &LINEPlugin{}
	caps := p.Capabilities()
	if !caps.MultiAccount {
		t.Error("expected MultiAccount capability")
	}
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &LINEPlugin{}
	if methods := p.GatewayMethods(); methods != nil {
		t.Errorf("expected nil, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*LINEPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &lineBot{channelID: "line-123"}
	if b.ID() != "line-123" {
		t.Errorf("expected line-123, got %s", b.ID())
	}
}

func TestVerifySignature_Valid(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"events":[]}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	b := &lineBot{channelSecret: secret}
	if !b.verifySignature(body, sig) {
		t.Error("expected valid signature")
	}
}

func TestVerifySignature_Invalid(t *testing.T) {
	b := &lineBot{channelSecret: "secret"}
	if b.verifySignature([]byte("body"), "badsig") {
		t.Error("expected invalid signature")
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func makeWebhookBody(events []lineEvent) []byte {
	data, _ := json.Marshal(lineWebhookBody{Events: events})
	return data
}

// ─── Connect ──────────────────────────────────────────────────────────────────

func TestConnect_MissingRequiredConfig(t *testing.T) {
	p := &LINEPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []map[string]any{
		{"channel_secret": "s"},
		{"channel_access_token": "t"},
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
	p := &LINEPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := map[string]any{
		"channel_access_token": "tok",
		"channel_secret":       "sec",
	}
	handle, err := p.Connect(ctx, "line-test", cfg, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	handle.Close()
}

// ─── handlePush ───────────────────────────────────────────────────────────────

func TestHandlePush_DeliversTextMessage(t *testing.T) {
	secret := "test-secret"
	var delivered []sdk.InboundChannelMessage
	bot := &lineBot{
		channelID:      "line-ch",
		channelSecret:  secret,
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body := makeWebhookBody([]lineEvent{
		{
			Type:       "message",
			ReplyToken: "rt-1",
			Source:     lineSource{Type: "user", UserID: "U123"},
			Message:    lineMessage{ID: "msg-1", Type: "text", Text: "hello LINE"},
			Timestamp:  1700000000000,
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Line-Signature", signBody(secret, body))
	w := httptest.NewRecorder()

	bot.handlePush(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(delivered) != 1 {
		t.Fatalf("expected 1 message, got %d", len(delivered))
	}
	if delivered[0].Text != "hello LINE" {
		t.Fatalf("wrong text: %s", delivered[0].Text)
	}
	if delivered[0].SenderID != "U123" {
		t.Fatalf("wrong senderID: %s", delivered[0].SenderID)
	}
	if delivered[0].EventID != "msg-1" {
		t.Fatalf("wrong eventID: %s", delivered[0].EventID)
	}
}

func TestHandlePush_SkipsNonTextEvents(t *testing.T) {
	secret := "sec"
	var delivered []sdk.InboundChannelMessage
	bot := &lineBot{
		channelID:      "line-ch",
		channelSecret:  secret,
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body := makeWebhookBody([]lineEvent{
		{Type: "follow", Source: lineSource{UserID: "U1"}},
		{Type: "message", Source: lineSource{UserID: "U1"}, Message: lineMessage{Type: "image", ID: "img-1"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Line-Signature", signBody(secret, body))
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if len(delivered) != 0 {
		t.Fatalf("expected 0 (non-text), got %d", len(delivered))
	}
}

func TestHandlePush_BadSignature(t *testing.T) {
	bot := &lineBot{
		channelID:     "line-ch",
		channelSecret: "secret",
		done:          make(chan struct{}),
	}
	body := makeWebhookBody(nil)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Line-Signature", "invalid")
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandlePush_MissingSignature(t *testing.T) {
	bot := &lineBot{channelID: "ch", channelSecret: "s", done: make(chan struct{})}
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandlePush_AllowedSendersFilter(t *testing.T) {
	secret := "sec"
	var delivered []sdk.InboundChannelMessage
	bot := &lineBot{
		channelID:      "line-ch",
		channelSecret:  secret,
		allowedSenders: map[string]bool{"U-allowed": true},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body := makeWebhookBody([]lineEvent{
		{Type: "message", Source: lineSource{UserID: "U-blocked"}, Message: lineMessage{Type: "text", Text: "blocked", ID: "m1"}},
		{Type: "message", Source: lineSource{UserID: "U-allowed"}, Message: lineMessage{Type: "text", Text: "allowed", ID: "m2"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Line-Signature", signBody(secret, body))
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if len(delivered) != 1 || delivered[0].Text != "allowed" {
		t.Fatalf("expected only allowed, got %+v", delivered)
	}
}

func TestHandlePush_SkipsEmptyText(t *testing.T) {
	secret := "sec"
	var delivered []sdk.InboundChannelMessage
	bot := &lineBot{
		channelID:      "line-ch",
		channelSecret:  secret,
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}
	body := makeWebhookBody([]lineEvent{
		{Type: "message", Source: lineSource{UserID: "U1"}, Message: lineMessage{Type: "text", Text: "  ", ID: "m1"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Line-Signature", signBody(secret, body))
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if len(delivered) != 0 {
		t.Fatalf("expected 0 (empty text), got %d", len(delivered))
	}
}

func TestHandlePush_CachesReplyToken(t *testing.T) {
	secret := "sec"
	bot := &lineBot{
		channelID:      "line-ch",
		channelSecret:  secret,
		allowedSenders: map[string]bool{},
		onMessage:      func(sdk.InboundChannelMessage) {},
		done:           make(chan struct{}),
	}
	body := makeWebhookBody([]lineEvent{
		{Type: "message", ReplyToken: "rt-abc", Source: lineSource{UserID: "U1"}, Message: lineMessage{Type: "text", Text: "hi", ID: "m1"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Line-Signature", signBody(secret, body))
	w := httptest.NewRecorder()
	bot.handlePush(w, req)

	bot.replyTokensMu.Lock()
	rt := bot.replyTokens["U1"]
	bot.replyTokensMu.Unlock()
	if rt != "rt-abc" {
		t.Fatalf("expected cached reply token rt-abc, got %q", rt)
	}
}

func TestHandlePush_MethodNotAllowed(t *testing.T) {
	bot := &lineBot{done: make(chan struct{})}
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// ─── Send ─────────────────────────────────────────────────────────────────────

func TestSend_UsesReplyToken(t *testing.T) {
	var capturedURL string
	bot := &lineBot{
		channelID:   "line-ch",
		accessToken: "at",
		replyTokens: map[string]string{"U1": "rt-1"},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
		})},
	}
	ctx := sdk.WithChannelReplyTarget(context.Background(), "U1")
	err := bot.Send(ctx, "reply")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(capturedURL, "/reply") {
		t.Fatalf("expected reply URL, got %s", capturedURL)
	}
}

func TestSend_FallsToPush(t *testing.T) {
	var capturedURL string
	bot := &lineBot{
		channelID:   "line-ch",
		accessToken: "at",
		replyTokens: map[string]string{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
		})},
	}
	ctx := sdk.WithChannelReplyTarget(context.Background(), "U1")
	err := bot.Send(ctx, "push msg")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(capturedURL, "/push") {
		t.Fatalf("expected push URL, got %s", capturedURL)
	}
}

func TestSend_ErrorOnHTTPFailure(t *testing.T) {
	bot := &lineBot{
		channelID:   "line-ch",
		accessToken: "at",
		replyTokens: map[string]string{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("error"))}, nil
		})},
	}
	ctx := sdk.WithChannelReplyTarget(context.Background(), "U1")
	err := bot.Send(ctx, "fail")
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

// ─── Close ────────────────────────────────────────────────────────────────────

func TestBotClose_Idempotent(t *testing.T) {
	bot := &lineBot{channelID: "line-close", done: make(chan struct{})}
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
