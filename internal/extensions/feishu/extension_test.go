package feishu

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
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
	p := &FeishuPlugin{}
	if id := p.ID(); id != "feishu" {
		t.Fatalf("expected feishu, got %s", id)
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &FeishuPlugin{}
	if typ := p.Type(); typ == "" {
		t.Fatal("Type must not be empty")
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &FeishuPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("ConfigSchema must not be nil")
	}
	for _, key := range []string{"app_id", "app_secret", "chat_id"} {
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &FeishuPlugin{}
	caps := p.Capabilities()
	if !caps.Typing {
		t.Error("expected Typing capability")
	}
	if !caps.Threads {
		t.Error("expected Threads capability")
	}
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &FeishuPlugin{}
	methods := p.GatewayMethods()
	if methods != nil {
		t.Errorf("expected nil GatewayMethods, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*FeishuPlugin)(nil)
}

func TestDecryptEvent_RoundTrip(t *testing.T) {
	key := "test-encrypt-key-12345"
	plaintext := []byte(`{"event":"hello"}`)

	// Encrypt with AES-CBC + PKCS7 using same key derivation as the code.
	h := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(h[:])
	if err != nil {
		t.Fatal(err)
	}
	// Pad plaintext to block size.
	padLen := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := make([]byte, len(plaintext)+padLen)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}
	iv := make([]byte, aes.BlockSize) // zero IV for test
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)

	encoded := base64.StdEncoding.EncodeToString(append(iv, ct...))

	got, err := decryptEvent(key, encoded)
	if err != nil {
		t.Fatalf("decryptEvent: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("expected %q, got %q", plaintext, got)
	}
}

func TestDecryptEvent_BadBase64(t *testing.T) {
	_, err := decryptEvent("key", "not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for bad base64")
	}
}

func TestDecryptEvent_TooShort(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("tiny"))
	_, err := decryptEvent("key", short)
	if err == nil {
		t.Fatal("expected error for short ciphertext")
	}
}

func TestDecryptEvent_BadPadding(t *testing.T) {
	key := "test-key"
	h := sha256.Sum256([]byte(key))
	block, _ := aes.NewCipher(h[:])

	// Craft ciphertext with invalid padding (last byte = 0).
	iv := make([]byte, aes.BlockSize)
	plain := make([]byte, aes.BlockSize) // all zeros = pad byte 0 which is invalid
	ct := make([]byte, aes.BlockSize)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, plain)

	encoded := base64.StdEncoding.EncodeToString(append(iv, ct...))
	_, err := decryptEvent(key, encoded)
	if err == nil {
		t.Fatal("expected error for invalid padding")
	}
}

func TestBotID(t *testing.T) {
	b := &feishuBot{channelID: "test-ch"}
	if b.ID() != "test-ch" {
		t.Errorf("expected test-ch, got %s", b.ID())
	}
}

func TestBotGetToken_Empty(t *testing.T) {
	b := &feishuBot{}
	if tok := b.getToken(); tok != "" {
		t.Errorf("expected empty token, got %q", tok)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func feishuMessageEvent(chatID, senderOpenID, senderType, msgID, text string) []byte {
	content, _ := json.Marshal(map[string]string{"text": text})
	event, _ := json.Marshal(map[string]any{
		"sender": map[string]any{
			"sender_id":   map[string]string{"open_id": senderOpenID},
			"sender_type": senderType,
		},
		"message": map[string]any{
			"message_id":   msgID,
			"chat_id":      chatID,
			"message_type": "text",
			"content":      string(content),
		},
	})
	envelope, _ := json.Marshal(map[string]any{
		"schema": "2.0",
		"header": map[string]string{
			"event_id":   "evt-" + msgID,
			"event_type": "im.message.receive_v1",
		},
		"event": json.RawMessage(event),
	})
	return envelope
}

// ─── Connect ──────────────────────────────────────────────────────────────────

func TestConnect_MissingRequiredConfig(t *testing.T) {
	p := &FeishuPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []map[string]any{
		{"app_secret": "s", "chat_id": "c"},
		{"app_id": "a", "chat_id": "c"},
		{"app_id": "a", "app_secret": "s"},
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
	p := &FeishuPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := map[string]any{
		"app_id":     "cli_test",
		"app_secret": "secret",
		"chat_id":    "oc_test",
	}
	handle, err := p.Connect(ctx, "feishu-test", cfg, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	handle.Close()
}

// ─── handleEvent ──────────────────────────────────────────────────────────────

func TestHandleEvent_DeliversTextMessage(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &feishuBot{
		channelID:      "fs-ch",
		chatID:         "oc_chat1",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
		seenEventIDs:   map[string]struct{}{},
	}
	body := feishuMessageEvent("oc_chat1", "ou_user1", "user", "msg-1", "你好")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	bot.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(delivered) != 1 {
		t.Fatalf("expected 1 message, got %d", len(delivered))
	}
	if delivered[0].Text != "你好" {
		t.Fatalf("wrong text: %s", delivered[0].Text)
	}
	if delivered[0].SenderID != "ou_user1" {
		t.Fatalf("wrong senderID: %s", delivered[0].SenderID)
	}
}

func TestHandleEvent_URLVerification(t *testing.T) {
	bot := &feishuBot{
		channelID:    "fs-ch",
		done:         make(chan struct{}),
		seenEventIDs: map[string]struct{}{},
	}
	body, _ := json.Marshal(map[string]any{
		"type":      "url_verification",
		"challenge": "challenge-token-123",
		"token":     "",
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "challenge-token-123") {
		t.Fatalf("expected challenge in response, got %s", w.Body.String())
	}
}

func TestHandleEvent_URLVerification_BadToken(t *testing.T) {
	bot := &feishuBot{
		channelID:         "fs-ch",
		verificationToken: "correct-token",
		done:              make(chan struct{}),
		seenEventIDs:      map[string]struct{}{},
	}
	body, _ := json.Marshal(map[string]any{
		"type":      "url_verification",
		"challenge": "c",
		"token":     "wrong-token",
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleEvent_DeduplicatesByEventID(t *testing.T) {
	var count int
	bot := &feishuBot{
		channelID:      "fs-ch",
		chatID:         "oc_chat1",
		allowedSenders: map[string]bool{},
		onMessage:      func(sdk.InboundChannelMessage) { count++ },
		done:           make(chan struct{}),
		seenEventIDs:   map[string]struct{}{},
	}
	body := feishuMessageEvent("oc_chat1", "ou_user1", "user", "msg-dup", "hi")

	// First time
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)

	// Second time (duplicate)
	req = httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w = httptest.NewRecorder()
	bot.handleEvent(w, req)

	if count != 1 {
		t.Fatalf("expected 1 delivery (dedup), got %d", count)
	}
}

func TestHandleEvent_SkipsBotMessages(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &feishuBot{
		channelID:      "fs-ch",
		chatID:         "oc_chat1",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
		seenEventIDs:   map[string]struct{}{},
	}
	body := feishuMessageEvent("oc_chat1", "ou_bot", "app", "msg-bot", "bot says hi")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if len(delivered) != 0 {
		t.Fatal("expected 0 (bot message)")
	}
}

func TestHandleEvent_SkipsWrongChat(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &feishuBot{
		channelID:      "fs-ch",
		chatID:         "oc_chat1",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
		seenEventIDs:   map[string]struct{}{},
	}
	body := feishuMessageEvent("oc_other", "ou_user1", "user", "msg-wrong", "hi")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if len(delivered) != 0 {
		t.Fatal("expected 0 (wrong chat)")
	}
}

func TestHandleEvent_AllowedSendersFilter(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &feishuBot{
		channelID:      "fs-ch",
		chatID:         "oc_chat1",
		allowedSenders: map[string]bool{"ou_allowed": true},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
		seenEventIDs:   map[string]struct{}{},
	}
	body := feishuMessageEvent("oc_chat1", "ou_blocked", "user", "msg-b", "blocked")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if len(delivered) != 0 {
		t.Fatal("expected 0 (filtered)")
	}
}

func TestHandleEvent_EmptyText(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &feishuBot{
		channelID:      "fs-ch",
		chatID:         "oc_chat1",
		allowedSenders: map[string]bool{},
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
		seenEventIDs:   map[string]struct{}{},
	}
	body := feishuMessageEvent("oc_chat1", "ou_user1", "user", "msg-empty", "  ")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if len(delivered) != 0 {
		t.Fatal("expected 0 (empty text)")
	}
}

func TestHandleEvent_MethodNotAllowed(t *testing.T) {
	bot := &feishuBot{done: make(chan struct{})}
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()
	bot.handleEvent(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// ─── Send ─────────────────────────────────────────────────────────────────────

func TestSend_PostsToAPI(t *testing.T) {
	var capturedURL string
	var capturedBody []byte
	bot := &feishuBot{
		channelID:   "fs-ch",
		chatID:      "oc_chat1",
		baseURL:     "http://feishu.test",
		accessToken: "tok",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			capturedBody, _ = io.ReadAll(req.Body)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
		})},
	}
	err := bot.Send(context.Background(), "hello feishu")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(capturedURL, "/open-apis/im/v1/messages") {
		t.Fatalf("unexpected URL: %s", capturedURL)
	}
	if !strings.Contains(string(capturedBody), `"receive_id":"oc_chat1"`) {
		t.Fatalf("unexpected body: %s", capturedBody)
	}
}

func TestSend_ErrorOnHTTPFailure(t *testing.T) {
	bot := &feishuBot{
		channelID: "fs-ch",
		chatID:    "oc_chat1",
		baseURL:   "http://feishu.test",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("error"))}, nil
		})},
	}
	err := bot.Send(context.Background(), "fail")
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestSendInThread_PostsReply(t *testing.T) {
	var capturedURL string
	bot := &feishuBot{
		channelID: "fs-ch",
		chatID:    "oc_chat1",
		baseURL:   "http://feishu.test",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
		})},
	}
	err := bot.SendInThread(context.Background(), "msg-parent", "thread reply")
	if err != nil {
		t.Fatalf("SendInThread: %v", err)
	}
	if !strings.Contains(capturedURL, "msg-parent/reply") {
		t.Fatalf("expected reply URL, got %s", capturedURL)
	}
}

func TestSendTyping_NoOp(t *testing.T) {
	bot := &feishuBot{}
	err := bot.SendTyping(context.Background(), 3000)
	if err != nil {
		t.Fatalf("SendTyping should be no-op, got %v", err)
	}
}

func TestRemoveReaction_NoOp(t *testing.T) {
	bot := &feishuBot{}
	err := bot.RemoveReaction(context.Background(), "msg-1", "thumbsup")
	if err != nil {
		t.Fatalf("RemoveReaction should be no-op, got %v", err)
	}
}

func TestAddReaction_PostsToAPI(t *testing.T) {
	var capturedURL string
	bot := &feishuBot{
		baseURL: "http://feishu.test",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
		})},
	}
	err := bot.AddReaction(context.Background(), "msg-1", "thumbsup")
	if err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	if !strings.Contains(capturedURL, "msg-1/reactions") {
		t.Fatalf("unexpected URL: %s", capturedURL)
	}
}

// ─── Token refresh ────────────────────────────────────────────────────────────

func TestRefreshToken_ParsesResponse(t *testing.T) {
	bot := &feishuBot{
		appID:     "cli_test",
		appSecret: "secret",
		baseURL:   "http://feishu.test",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"code":0,"tenant_access_token":"new-tat","expire":7200}`)),
			}, nil
		})},
	}
	err := bot.refreshToken(context.Background())
	if err != nil {
		t.Fatalf("refreshToken: %v", err)
	}
	if bot.getToken() != "new-tat" {
		t.Fatalf("expected new-tat, got %s", bot.getToken())
	}
}

func TestRefreshToken_ReturnsErrorOnAPIError(t *testing.T) {
	bot := &feishuBot{
		appID:     "cli_test",
		appSecret: "secret",
		baseURL:   "http://feishu.test",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"code":99991,"msg":"invalid app"}`)),
			}, nil
		})},
	}
	err := bot.refreshToken(context.Background())
	if err == nil {
		t.Fatal("expected error on API error")
	}
}

// ─── Close ────────────────────────────────────────────────────────────────────

func TestBotClose_Idempotent(t *testing.T) {
	bot := &feishuBot{channelID: "fs-close", done: make(chan struct{})}
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

// ─── Interface compliance ─────────────────────────────────────────────────────

func TestBot_ImplementsOptionalInterfaces(t *testing.T) {
	var _ sdk.TypingHandle = (*feishuBot)(nil)
	var _ sdk.ReactionHandle = (*feishuBot)(nil)
	var _ sdk.ThreadHandle = (*feishuBot)(nil)
}
