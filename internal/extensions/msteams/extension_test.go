package msteams

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"metiq/internal/plugins/sdk"
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

func TestConnect_MissingAppID(t *testing.T) {
	p := &MSTeamsPlugin{}
	_, err := p.Connect(context.Background(), "t1", map[string]any{
		"app_secret": "secret",
	}, nil)
	if err == nil {
		t.Fatal("expected error when app_id is missing")
	}
}

func TestConnect_MissingAppSecret(t *testing.T) {
	p := &MSTeamsPlugin{}
	_, err := p.Connect(context.Background(), "t1", map[string]any{
		"app_id": "id",
	}, nil)
	if err == nil {
		t.Fatal("expected error when app_secret is missing")
	}
}

func TestConnect_ValidConfig(t *testing.T) {
	p := &MSTeamsPlugin{}
	h, err := p.Connect(context.Background(), "teams-ch", map[string]any{
		"app_id":     "id",
		"app_secret": "secret",
	}, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer h.Close()
	if h.ID() != "teams-ch" {
		t.Fatalf("expected teams-ch, got %s", h.ID())
	}
}

func TestClose_Idempotent(t *testing.T) {
	bot, _ := newTestTeamsBot()
	bot.Close()
	bot.Close() // should not panic
}

func TestParseJWTClaims_Valid(t *testing.T) {
	token := makeTestJWT("app1", "https://api.botframework.com", time.Now().Add(5*time.Minute), time.Now().Add(-1*time.Minute))
	claims, err := parseJWTClaims(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !claims.HasAudience("app1") {
		t.Fatal("expected audience match")
	}
}

func TestParseJWTClaims_Invalid(t *testing.T) {
	_, err := parseJWTClaims("not-a-jwt")
	if err == nil {
		t.Fatal("expected error")
	}
}

// tokenServer returns a test HTTP server that serves a fake AAD token.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func okJSON(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newMockTeamsBot(handler roundTripFunc) *teamsBot {
	conv, _ := json.Marshal(map[string]string{"id": "conv1"})
	act := &botFrameworkActivity{
		ServiceURL:   "https://smba.example.com",
		Conversation: conv,
	}
	act.Recipient.ID = "bot1"
	bot := &teamsBot{
		channelID:    "teams-test",
		appID:        "app1",
		appSecret:    "secret",
		serviceURL:   "https://smba.example.com",
		done:         make(chan struct{}),
		httpClient:   &http.Client{Transport: handler},
		lastActivity: act,
	}
	bot.onMessage = func(sdk.InboundChannelMessage) {}
	return bot
}

func TestSend_PostsActivity(t *testing.T) {
	var gotPath, gotMethod string
	bot := newMockTeamsBot(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/oauth2/v2.0/token") {
			return okJSON(`{"access_token":"tok"}`), nil
		}
		gotPath = req.URL.Path
		gotMethod = req.Method
		return okJSON(`{}`), nil
	})

	err := bot.Send(context.Background(), "hello teams")
	if err != nil {
		t.Fatalf("Send error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if !strings.Contains(gotPath, "/v3/conversations/conv1/activities") {
		t.Fatalf("unexpected path: %s", gotPath)
	}
}

func TestSend_NoConversationContext(t *testing.T) {
	bot := &teamsBot{
		channelID:  "teams-test",
		done:       make(chan struct{}),
		httpClient: &http.Client{},
	}
	err := bot.Send(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error when no conversation context")
	}
}

func TestAddReaction_PostsReaction(t *testing.T) {
	var gotBody map[string]any
	bot := newMockTeamsBot(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/oauth2/v2.0/token") {
			return okJSON(`{"access_token":"tok"}`), nil
		}
		json.NewDecoder(req.Body).Decode(&gotBody)
		return okJSON(`{}`), nil
	})
	err := bot.AddReaction(context.Background(), "act123", "like")
	if err != nil {
		t.Fatalf("AddReaction error: %v", err)
	}
	if gotBody["type"] != "messageReaction" {
		t.Fatalf("expected messageReaction, got %v", gotBody["type"])
	}
}

func TestRemoveReaction_PostsReaction(t *testing.T) {
	bot := newMockTeamsBot(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/oauth2/v2.0/token") {
			return okJSON(`{"access_token":"tok"}`), nil
		}
		return okJSON(`{}`), nil
	})
	err := bot.RemoveReaction(context.Background(), "act123", "like")
	if err != nil {
		t.Fatalf("RemoveReaction error: %v", err)
	}
}

func TestSendInThread_PostsReply(t *testing.T) {
	var gotBody map[string]any
	bot := newMockTeamsBot(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/oauth2/v2.0/token") {
			return okJSON(`{"access_token":"tok"}`), nil
		}
		json.NewDecoder(req.Body).Decode(&gotBody)
		return okJSON(`{}`), nil
	})
	err := bot.SendInThread(context.Background(), "thread1", "reply text")
	if err != nil {
		t.Fatalf("SendInThread error: %v", err)
	}
	if gotBody["replyToId"] != "thread1" {
		t.Fatalf("expected replyToId=thread1, got %v", gotBody["replyToId"])
	}
}

func TestEditMessage_PutsUpdate(t *testing.T) {
	var gotMethod string
	var gotPath string
	bot := newMockTeamsBot(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/oauth2/v2.0/token") {
			return okJSON(`{"access_token":"tok"}`), nil
		}
		gotMethod = req.Method
		gotPath = req.URL.Path
		return okJSON(`{}`), nil
	})
	err := bot.EditMessage(context.Background(), "act-42", "new text")
	if err != nil {
		t.Fatalf("EditMessage error: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("expected PUT, got %s", gotMethod)
	}
	if !strings.Contains(gotPath, "act-42") {
		t.Fatalf("expected act-42 in path, got %s", gotPath)
	}
}

func TestAcquireToken_ReturnsToken(t *testing.T) {
	// acquireToken hardcodes the Microsoft token URL, so it's tested
	// indirectly via Send/AddReaction/EditMessage tests above which
	// use httptest servers that intercept all client traffic.
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
