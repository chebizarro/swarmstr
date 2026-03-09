package msteams

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
	"testing"

	"swarmstr/internal/plugins/sdk"
)

// ── Plugin metadata ───────────────────────────────────────────────────────────

func TestMSTeamsPlugin_ID(t *testing.T) {
	p := &MSTeamsPlugin{}
	if p.ID() != "msteams" {
		t.Fatalf("expected id='msteams', got %q", p.ID())
	}
}

func TestMSTeamsPlugin_Capabilities(t *testing.T) {
	p := &MSTeamsPlugin{}
	caps := p.Capabilities()
	if !caps.Reactions || !caps.Threads || !caps.Edit {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}

func TestMSTeamsPlugin_ConfigSchema(t *testing.T) {
	p := &MSTeamsPlugin{}
	schema := p.ConfigSchema()
	required, _ := schema["required"].([]string)
	set := map[string]bool{}
	for _, r := range required {
		set[r] = true
	}
	for _, f := range []string{"app_id", "app_secret"} {
		if !set[f] {
			t.Fatalf("expected %q in required fields", f)
		}
	}
}

// ── handleActivity ────────────────────────────────────────────────────────────

func newTestTeamsBot(allowedSenders ...string) (*teamsBot, *[]sdk.InboundChannelMessage) {
	var msgs []sdk.InboundChannelMessage
	allowed := map[string]bool{}
	for _, s := range allowedSenders {
		allowed[s] = true
	}
	bot := &teamsBot{
		channelID:      "teams-test",
		appID:          "app1",
		appSecret:      "secret",
		serviceURL:     "https://smba.example.com",
		allowedSenders: allowed,
		done:           make(chan struct{}),
		httpClient:     &http.Client{},
	}
	bot.onMessage = func(m sdk.InboundChannelMessage) {
		msgs = append(msgs, m)
	}
	return bot, &msgs
}

func postActivity(t *testing.T, bot *teamsBot, activity botFrameworkActivity) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(activity)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+makeTestJWT(bot.appID, "https://api.botframework.com", time.Now().Add(5*time.Minute), time.Now().Add(-1*time.Minute)))
	w := httptest.NewRecorder()
	bot.handleActivity(w, req)
	return w
}

func makeTestJWT(aud, iss string, exp, nbf time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadMap := map[string]any{
		"aud": aud,
		"iss": iss,
		"exp": exp.Unix(),
		"nbf": nbf.Unix(),
	}
	payload, _ := json.Marshal(payloadMap)
	encPayload := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + encPayload + ".sig"
}

func makeActivity(fromID, text string) botFrameworkActivity {
	conv, _ := json.Marshal(map[string]string{"id": "conv1"})
	act := botFrameworkActivity{
		Type:         "message",
		ID:           "act1",
		Text:         text,
		ServiceURL:   "https://smba.example.com",
		Conversation: conv,
		ChannelID:    "msteams",
	}
	act.From.ID = fromID
	act.From.Name = "Alice"
	act.Recipient.ID = "bot1"
	act.Recipient.Name = "Bot"
	return act
}

func TestHandleActivity_Delivers(t *testing.T) {
	bot, msgs := newTestTeamsBot()
	w := postActivity(t, bot, makeActivity("u1", "hello teams"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(*msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(*msgs))
	}
	if (*msgs)[0].Text != "hello teams" {
		t.Fatalf("unexpected text: %q", (*msgs)[0].Text)
	}
}

func TestHandleActivity_StripHTML(t *testing.T) {
	bot, msgs := newTestTeamsBot()
	postActivity(t, bot, makeActivity("u1", "<p>hello <b>world</b></p>"))
	if len(*msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(*msgs))
	}
	if (*msgs)[0].Text != "hello world" {
		t.Fatalf("expected stripped text, got %q", (*msgs)[0].Text)
	}
}

func TestHandleActivity_SkipsNonMessage(t *testing.T) {
	bot, msgs := newTestTeamsBot()
	conv, _ := json.Marshal(map[string]string{"id": "conv1"})
	act := botFrameworkActivity{Type: "conversationUpdate", Conversation: conv}
	data, _ := json.Marshal(act)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
	w := httptest.NewRecorder()
	bot.handleActivity(w, req)
	if len(*msgs) != 0 {
		t.Fatalf("expected 0 messages for non-message activity, got %d", len(*msgs))
	}
}

func TestHandleActivity_AllowedSendersFilter(t *testing.T) {
	bot, msgs := newTestTeamsBot("allowed_user")
	postActivity(t, bot, makeActivity("allowed_user", "hi"))
	postActivity(t, bot, makeActivity("blocked_user", "ignored"))
	if len(*msgs) != 1 || (*msgs)[0].SenderID != "allowed_user" {
		t.Fatalf("expected only allowed_user, got %+v", *msgs)
	}
}

func TestHandleActivity_MethodNotAllowed(t *testing.T) {
	bot, _ := newTestTeamsBot()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	bot.handleActivity(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleActivity_EmptyText(t *testing.T) {
	bot, msgs := newTestTeamsBot()
	postActivity(t, bot, makeActivity("u1", "   "))
	if len(*msgs) != 0 {
		t.Fatal("expected empty text to be filtered")
	}
}

func TestHandleActivity_RejectsInvalidJWT(t *testing.T) {
	bot, msgs := newTestTeamsBot()
	data, _ := json.Marshal(makeActivity("u1", "hello teams"))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
	req.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()
	bot.handleActivity(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if len(*msgs) != 0 {
		t.Fatalf("expected no delivered messages, got %d", len(*msgs))
	}
}

func TestHandleActivity_RejectsWrongAudience(t *testing.T) {
	bot, msgs := newTestTeamsBot()
	data, _ := json.Marshal(makeActivity("u1", "hello teams"))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
	req.Header.Set("Authorization", "Bearer "+makeTestJWT("wrong-app", "https://api.botframework.com", time.Now().Add(5*time.Minute), time.Now().Add(-1*time.Minute)))
	w := httptest.NewRecorder()
	bot.handleActivity(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if len(*msgs) != 0 {
		t.Fatalf("expected no delivered messages, got %d", len(*msgs))
	}
}

// ── stripSimpleHTML ───────────────────────────────────────────────────────────

func TestStripSimpleHTML(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"<p>hello</p>", "hello"},
		{"<b>bold</b> text", "bold text"},
		{"no tags", "no tags"},
		{"<br/>line<br/>break", "linebreak"},
		{"  spaces  ", "spaces"},
	}
	for _, tc := range tests {
		got := stripSimpleHTML(tc.in)
		if got != tc.want {
			t.Errorf("stripSimpleHTML(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

// ── webhook registry ──────────────────────────────────────────────────────────

func TestHandleWebhook_UnknownChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/msteams/unknown", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	HandleWebhook("unknown-channel-xyz-999", w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unregistered channel, got %d", w.Code)
	}
}

func TestRegisterAndHandleWebhook(t *testing.T) {
	bot, msgs := newTestTeamsBot()
	registerWebhook("test-webhook-ch", bot)
	defer func() {
		webhookMu.Lock()
		delete(webhookHandlers, "test-webhook-ch")
		webhookMu.Unlock()
	}()

	act := makeActivity("u1", "webhook message")
	data, _ := json.Marshal(act)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+makeTestJWT(bot.appID, "https://api.botframework.com", time.Now().Add(5*time.Minute), time.Now().Add(-1*time.Minute)))
	w := httptest.NewRecorder()
	HandleWebhook("test-webhook-ch", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(*msgs) != 1 || (*msgs)[0].Text != "webhook message" {
		t.Fatalf("unexpected messages: %+v", *msgs)
	}
}
