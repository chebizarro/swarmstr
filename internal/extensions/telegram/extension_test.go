package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"metiq/internal/plugins/sdk"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func TestTelegramFetchUpdates_PopulatesThreadAndReplyMetadata(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &telegramBot{
		channelID: "telegram-main",
		token:     "token",
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done: make(chan struct{}),
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if !strings.Contains(req.URL.Path, "/bottoken/getUpdates") {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			return jsonResponse(req, `{
				"ok": true,
				"result": [
					{
						"update_id": 1,
						"message": {
							"message_id": 41,
							"text": "plain reply",
							"date": 1712300001,
							"reply_to_message": {"message_id": 40},
							"from": {"id": 123},
							"chat": {"id": 999}
						}
					},
					{
						"update_id": 2,
						"message": {
							"message_id": 42,
							"message_thread_id": 900,
							"text": "topic reply",
							"date": 1712300002,
							"reply_to_message": {"message_id": 41},
							"from": {"id": 123},
							"chat": {"id": 999}
						}
					}
				]
			}`), nil
		})},
	}

	bot.fetchUpdates(context.Background())

	if len(delivered) != 2 {
		t.Fatalf("expected 2 delivered messages, got %d", len(delivered))
	}
	if delivered[0].ThreadID != "" || delivered[0].ReplyToEventID != "tg-40" {
		t.Fatalf("expected plain reply to carry reply metadata without thread scoping, got %+v", delivered[0])
	}
	if delivered[1].ThreadID != "900" || delivered[1].ReplyToEventID != "tg-41" {
		t.Fatalf("expected topic thread metadata, got %+v", delivered[1])
	}
}

func TestHandleWebhook_UnknownChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram/unknown", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	HandleWebhook("unknown-channel-xyz", w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestTelegramPlugin_Connect_WebhookModeConfiguresAndDispatches(t *testing.T) {
	prevFactory := newTelegramHTTPClient
	defer func() { newTelegramHTTPClient = prevFactory }()

	var setWebhookCalls int
	var deleteWebhookCalls int
	var delivered []sdk.InboundChannelMessage
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/setWebhook"):
			setWebhookCalls++
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode setWebhook body: %v", err)
			}
			if body["url"] != "https://example.test/webhooks/telegram/telegram-main" {
				t.Fatalf("unexpected webhook url: %#v", body)
			}
			if strings.TrimSpace(body["secret_token"].(string)) == "" {
				t.Fatalf("expected non-empty secret token in setWebhook body")
			}
			return jsonResponse(req, `{"ok":true}`), nil
		case strings.HasSuffix(req.URL.Path, "/deleteWebhook"):
			deleteWebhookCalls++
			return jsonResponse(req, `{"ok":true}`), nil
		default:
			t.Fatalf("unexpected path: %s", req.URL.Path)
			return nil, nil
		}
	})}
	newTelegramHTTPClient = func(timeout time.Duration) *http.Client {
		return client
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &TelegramPlugin{}
	handle, err := p.Connect(ctx, "telegram-main", map[string]any{
		"token":       "token",
		"webhook_url": "https://example.test/webhooks/telegram/telegram-main",
	}, func(msg sdk.InboundChannelMessage) {
		delivered = append(delivered, msg)
	})
	if err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	defer handle.Close()

	if setWebhookCalls != 1 {
		t.Fatalf("expected one setWebhook call, got %d", setWebhookCalls)
	}

	payload := `{"update_id":99,"message":{"message_id":77,"text":"webhook hello","date":1712300100,"from":{"id":321},"chat":{"id":654}}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram/telegram-main", strings.NewReader(payload))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", deriveTelegramWebhookSecret("token", "telegram-main"))
	w := httptest.NewRecorder()
	HandleWebhook("telegram-main", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(delivered) != 1 || delivered[0].Text != "webhook hello" {
		t.Fatalf("unexpected delivered messages: %+v", delivered)
	}

	handle.Close()
	if deleteWebhookCalls == 0 {
		t.Fatalf("expected deleteWebhook to run on Close")
	}
}

// ── Plugin metadata ───────────────────────────────────────────────────────────

func TestPlugin_ID(t *testing.T) {
	p := &TelegramPlugin{}
	if p.ID() != "telegram" {
		t.Fatalf("expected telegram, got %s", p.ID())
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &TelegramPlugin{}
	if p.Type() == "" {
		t.Fatal("Type should not be empty")
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &TelegramPlugin{}
	schema := p.ConfigSchema()
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["token"]; !ok {
		t.Error("missing token in schema")
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &TelegramPlugin{}
	caps := p.Capabilities()
	if !caps.Threads || !caps.Typing || !caps.Edit {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*TelegramPlugin)(nil)
}

// ── processUpdate ─────────────────────────────────────────────────────────────

func TestProcessUpdate_SkipsNilMessage(t *testing.T) {
	called := false
	bot := &telegramBot{
		channelID: "tg-1",
		onMessage: func(sdk.InboundChannelMessage) { called = true },
		done:      make(chan struct{}),
	}
	bot.processUpdate(telegramUpdate{UpdateID: 1, Message: nil})
	if called {
		t.Fatal("should skip nil message")
	}
}

func TestProcessUpdate_SkipsEmptyText(t *testing.T) {
	called := false
	bot := &telegramBot{
		channelID: "tg-1",
		onMessage: func(sdk.InboundChannelMessage) { called = true },
		done:      make(chan struct{}),
	}
	bot.processUpdate(telegramUpdate{
		UpdateID: 1,
		Message:  &telegramMessage{MessageID: 1, Text: "", Date: 1000},
	})
	if called {
		t.Fatal("should skip empty text")
	}
}

func TestProcessUpdate_AllowedUsersFilter(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &telegramBot{
		channelID:    "tg-1",
		allowedUsers: []int64{100},
		onMessage:    func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) },
		done:         make(chan struct{}),
	}
	// Allowed user
	bot.processUpdate(telegramUpdate{
		UpdateID: 1,
		Message: &telegramMessage{
			MessageID: 1, Text: "hi", Date: 1000,
			From: &struct {
				ID       int64  `json:"id"`
				Username string `json:"username"`
			}{ID: 100},
		},
	})
	// Blocked user
	bot.processUpdate(telegramUpdate{
		UpdateID: 2,
		Message: &telegramMessage{
			MessageID: 2, Text: "hi", Date: 1001,
			From: &struct {
				ID       int64  `json:"id"`
				Username string `json:"username"`
			}{ID: 999},
		},
	})
	if len(delivered) != 1 {
		t.Fatalf("expected 1 delivered, got %d", len(delivered))
	}
}

func TestProcessUpdate_TracksLastChatID(t *testing.T) {
	bot := &telegramBot{
		channelID: "tg-1",
		onMessage: func(sdk.InboundChannelMessage) {},
		done:      make(chan struct{}),
	}
	bot.processUpdate(telegramUpdate{
		UpdateID: 1,
		Message: &telegramMessage{
			MessageID: 1, Text: "hello", Date: 1000,
			Chat: &struct {
				ID int64 `json:"id"`
			}{ID: 42},
		},
	})
	bot.mu.Lock()
	chatID := bot.lastChatID
	bot.mu.Unlock()
	if chatID != "42" {
		t.Fatalf("expected lastChatID=42, got %s", chatID)
	}
}

// ── Send / SendTyping / EditMessage / SendInThread ────────────────────────────

func TestSend_NoChatID(t *testing.T) {
	bot := &telegramBot{channelID: "tg-1", token: "tok", done: make(chan struct{})}
	err := bot.Send(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error when no chatID known")
	}
}

func TestSendTyping_NoChatID_NoError(t *testing.T) {
	bot := &telegramBot{channelID: "tg-1", token: "tok", done: make(chan struct{})}
	err := bot.SendTyping(context.Background(), 3)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestSendTyping_PostsChatAction(t *testing.T) {
	var gotPath string
	bot := &telegramBot{
		channelID:  "tg-1",
		token:      "tok",
		lastChatID: "42",
		done:       make(chan struct{}),
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			return jsonResponse(req, `{"ok":true}`), nil
		})},
	}
	err := bot.SendTyping(context.Background(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/sendChatAction") {
		t.Fatalf("expected /sendChatAction path, got %s", gotPath)
	}
}

func TestEditMessage_NoChatID(t *testing.T) {
	bot := &telegramBot{channelID: "tg-1", token: "tok", done: make(chan struct{})}
	err := bot.EditMessage(context.Background(), "tg-123", "new text")
	if err == nil {
		t.Fatal("expected error when no chatID known")
	}
}

func TestEditMessage_InvalidEventID(t *testing.T) {
	bot := &telegramBot{
		channelID:  "tg-1",
		token:      "tok",
		lastChatID: "42",
		done:       make(chan struct{}),
	}
	err := bot.EditMessage(context.Background(), "invalid", "new text")
	if err == nil {
		t.Fatal("expected error for invalid eventID")
	}
}

func TestEditMessage_PostsEditAPI(t *testing.T) {
	var gotPath string
	bot := &telegramBot{
		channelID:  "tg-1",
		token:      "tok",
		lastChatID: "42",
		done:       make(chan struct{}),
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			return jsonResponse(req, `{"ok":true}`), nil
		})},
	}
	err := bot.EditMessage(context.Background(), "tg-123", "new text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/editMessageText") {
		t.Fatalf("expected /editMessageText path, got %s", gotPath)
	}
}

func TestSendInThread_NoChatID(t *testing.T) {
	bot := &telegramBot{channelID: "tg-1", token: "tok", done: make(chan struct{})}
	err := bot.SendInThread(context.Background(), "42", "reply")
	if err == nil {
		t.Fatal("expected error when no chatID known")
	}
}

func TestSendInThread_PostsSendMessage(t *testing.T) {
	var gotBody map[string]any
	bot := &telegramBot{
		channelID:  "tg-1",
		token:      "tok",
		lastChatID: "42",
		done:       make(chan struct{}),
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			json.NewDecoder(req.Body).Decode(&gotBody)
			return jsonResponse(req, `{"ok":true}`), nil
		})},
	}
	err := bot.SendInThread(context.Background(), "100", "reply text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["reply_to_message_id"] != float64(100) {
		t.Fatalf("expected reply_to_message_id=100, got %v", gotBody["reply_to_message_id"])
	}
}

func TestClose_Idempotent(t *testing.T) {
	bot := &telegramBot{channelID: "tg-1", done: make(chan struct{})}
	bot.Close()
	bot.Close() // should not panic
}

func TestDeriveTelegramWebhookSecret_Deterministic(t *testing.T) {
	s1 := deriveTelegramWebhookSecret("token", "ch1")
	s2 := deriveTelegramWebhookSecret("token", "ch1")
	if s1 != s2 {
		t.Fatal("expected deterministic secret")
	}
	s3 := deriveTelegramWebhookSecret("token", "ch2")
	if s1 == s3 {
		t.Fatal("different channels should produce different secrets")
	}
}

func TestTelegramHandleWebhook_RejectsSecretMismatch(t *testing.T) {
	bot := &telegramBot{
		channelID:     "telegram-secret",
		webhookSecret: "expected-secret",
		onMessage:     func(sdk.InboundChannelMessage) { t.Fatal("unexpected message delivery") },
		done:          make(chan struct{}),
		allowedUsers:  nil,
	}
	registerWebhook("telegram-secret", bot)
	defer func() {
		webhookMu.Lock()
		delete(webhookHandlers, "telegram-secret")
		webhookMu.Unlock()
	}()

	req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram/telegram-secret", strings.NewReader(`{"update_id":1}`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong-secret")
	w := httptest.NewRecorder()
	HandleWebhook("telegram-secret", w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
