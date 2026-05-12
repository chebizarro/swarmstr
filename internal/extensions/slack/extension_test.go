package slack

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// ─── Plugin metadata ──────────────────────────────────────────────────────────

func TestPlugin_ID(t *testing.T) {
	p := &SlackPlugin{}
	if p.ID() != "slack" {
		t.Fatalf("expected slack, got %s", p.ID())
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &SlackPlugin{}
	if p.Type() != "Slack Bot" {
		t.Fatalf("expected Slack Bot, got %s", p.Type())
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &SlackPlugin{}
	schema := p.ConfigSchema()
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"bot_token", "channel_id"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &SlackPlugin{}
	caps := p.Capabilities()
	if !caps.Reactions || !caps.Threads || !caps.Edit {
		t.Error("expected Reactions, Threads, Edit capabilities")
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*SlackPlugin)(nil)
}

// ─── Connect ──────────────────────────────────────────────────────────────────

func TestConnect_MissingToken(t *testing.T) {
	p := &SlackPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Connect(ctx, "ch", map[string]any{"channel_id": "C123"}, func(sdk.InboundChannelMessage) {})
	if err == nil {
		t.Fatal("expected error with missing token")
	}
}

func TestConnect_MissingChannelID(t *testing.T) {
	p := &SlackPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Connect(ctx, "ch", map[string]any{"bot_token": "xoxb-test"}, func(sdk.InboundChannelMessage) {})
	if err == nil {
		t.Fatal("expected error with missing channel_id")
	}
}

func TestConnect_ValidConfig(t *testing.T) {
	p := &SlackPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handle, err := p.Connect(ctx, "slack-test", map[string]any{
		"bot_token":  "xoxb-test",
		"channel_id": "C123",
	}, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	handle.Close()
}

// ─── slackPost / Send / AddReaction / EditMessage / SendInThread ──────────────

func TestSlackPost_Success(t *testing.T) {
	var capturedURL string
	bot := &slackBot{
		channelID:      "slack-ch",
		token:          "xoxb-test",
		slackChannelID: "C123",
		done:           make(chan struct{}),
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			return jsonResponse(req, `{"ok":true}`), nil
		})},
	}
	err := bot.slackPost(context.Background(), slackAPIBase+"/reactions.add", []byte(`{}`))
	if err != nil {
		t.Fatalf("slackPost: %v", err)
	}
	if !strings.Contains(capturedURL, "/reactions.add") {
		t.Fatalf("unexpected URL: %s", capturedURL)
	}
}

func TestSlackPost_APIError(t *testing.T) {
	bot := &slackBot{
		token: "xoxb-test",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(req, `{"ok":false,"error":"channel_not_found"}`), nil
		})},
	}
	err := bot.slackPost(context.Background(), slackAPIBase+"/chat.postMessage", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error on API error response")
	}
	if !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("expected channel_not_found in error, got: %v", err)
	}
}

func TestAddReaction_PostsToReactionsAPI(t *testing.T) {
	var capturedURL string
	var capturedBody []byte
	bot := &slackBot{
		token:          "xoxb-test",
		slackChannelID: "C123",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			capturedBody, _ = io.ReadAll(req.Body)
			return jsonResponse(req, `{"ok":true}`), nil
		})},
	}
	err := bot.AddReaction(context.Background(), "slack-1234.5678", "thumbsup")
	if err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	if !strings.Contains(capturedURL, "/reactions.add") {
		t.Fatalf("wrong URL: %s", capturedURL)
	}
	if !strings.Contains(string(capturedBody), `"timestamp":"1234.5678"`) {
		t.Fatalf("wrong body: %s", capturedBody)
	}
}

func TestRemoveReaction_PostsToReactionsAPI(t *testing.T) {
	var capturedURL string
	bot := &slackBot{
		token:          "xoxb-test",
		slackChannelID: "C123",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			return jsonResponse(req, `{"ok":true}`), nil
		})},
	}
	err := bot.RemoveReaction(context.Background(), "slack-1234.5678", "thumbsup")
	if err != nil {
		t.Fatalf("RemoveReaction: %v", err)
	}
	if !strings.Contains(capturedURL, "/reactions.remove") {
		t.Fatalf("wrong URL: %s", capturedURL)
	}
}

func TestEditMessage_PostsToUpdateAPI(t *testing.T) {
	var capturedURL string
	bot := &slackBot{
		token:          "xoxb-test",
		slackChannelID: "C123",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			return jsonResponse(req, `{"ok":true}`), nil
		})},
	}
	err := bot.EditMessage(context.Background(), "slack-1234.5678", "updated text")
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if !strings.Contains(capturedURL, "/chat.update") {
		t.Fatalf("wrong URL: %s", capturedURL)
	}
}

func TestSendInThread_PostsToThreadAPI(t *testing.T) {
	var capturedBody []byte
	bot := &slackBot{
		token:          "xoxb-test",
		slackChannelID: "C123",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedBody, _ = io.ReadAll(req.Body)
			return jsonResponse(req, `{"ok":true}`), nil
		})},
	}
	err := bot.SendInThread(context.Background(), "1234.5678", "thread reply")
	if err != nil {
		t.Fatalf("SendInThread: %v", err)
	}
	if !strings.Contains(string(capturedBody), `"thread_ts":"1234.5678"`) {
		t.Fatalf("missing thread_ts in body: %s", capturedBody)
	}
}

// ─── Events API webhook ──────────────────────────────────────────────────────

func TestHandleWebhook_UrlVerification(t *testing.T) {
	bot := &slackBot{channelID: "slack-ch", slackChannelID: "C123", done: make(chan struct{}), onMessage: func(sdk.InboundChannelMessage) {}}
	registerWebhook("slack-ch", bot)
	defer bot.Close()

	req := httptest.NewRequest(http.MethodPost, "/webhooks/slack/slack-ch", strings.NewReader(`{"type":"url_verification","challenge":"abc123"}`))
	w := httptest.NewRecorder()
	HandleWebhook("slack-ch", w, req)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "abc123" {
		t.Fatalf("unexpected url verification response: code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestHandleWebhook_MessageEvent(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &slackBot{
		channelID:      "slack-ch",
		slackChannelID: "C123",
		botUserID:      "UBOT",
		done:           make(chan struct{}),
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
	}
	registerWebhook("slack-ch", bot)
	defer bot.Close()

	body := `{"type":"event_callback","event_id":"Ev1","event":{"type":"message","channel":"C123","user":"U123","text":"real msg","ts":"171.003"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/slack/slack-ch", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleWebhook("slack-ch", w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(delivered) != 1 || delivered[0].Text != "real msg" || delivered[0].CreatedAt != 171 {
		t.Fatalf("expected delivered event, got %+v", delivered)
	}
}

func TestHandleWebhook_DeduplicatesEventID(t *testing.T) {
	count := 0
	bot := &slackBot{channelID: "slack-ch", slackChannelID: "C123", done: make(chan struct{}), onMessage: func(sdk.InboundChannelMessage) { count++ }}
	registerWebhook("slack-ch", bot)
	defer bot.Close()

	body := `{"type":"event_callback","event_id":"Ev1","event":{"type":"message","channel":"C123","user":"U123","text":"real msg","ts":"171.003"}}`
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/slack/slack-ch", strings.NewReader(body))
		w := httptest.NewRecorder()
		HandleWebhook("slack-ch", w, req)
	}
	if count != 1 {
		t.Fatalf("expected one delivered event, got %d", count)
	}
}

// ─── Close ────────────────────────────────────────────────────────────────────

func TestBotID(t *testing.T) {
	b := &slackBot{channelID: "slack-1"}
	if b.ID() != "slack-1" {
		t.Errorf("expected slack-1, got %s", b.ID())
	}
}

// ─── Events API thread metadata ──────────────────────────────────────────────

func TestSlackEvents_PopulatesThreadMetadata(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &slackBot{
		channelID:      "slack-main",
		slackChannelID: "C123",
		onMessage:      func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
		done:           make(chan struct{}),
	}

	bot.processSlackEvent(slackEvent{Type: "message", Channel: "C123", User: "U123", Text: "root message", Ts: "171.050"})
	bot.processSlackEvent(slackEvent{Type: "message", Channel: "C123", User: "U123", Text: "thread root", Ts: "171.100", ThreadTS: "171.100"})
	bot.processSlackEvent(slackEvent{Type: "message", Channel: "C123", User: "U123", Text: "thread reply", Ts: "171.200", ThreadTS: "171.100"})

	if len(delivered) != 3 {
		t.Fatalf("expected 3 delivered messages, got %d", len(delivered))
	}
	if delivered[0].ThreadID != "" || delivered[0].ReplyToEventID != "" {
		t.Fatalf("expected ordinary root message to have no thread metadata, got %+v", delivered[0])
	}
	if delivered[1].ThreadID != "" || delivered[1].ReplyToEventID != "" {
		t.Fatalf("expected top-level thread root to stay unthreaded in inbound metadata, got %+v", delivered[1])
	}
	if delivered[2].ThreadID != "171.100" || delivered[2].ReplyToEventID != "slack-171.100" {
		t.Fatalf("expected threaded reply metadata, got %+v", delivered[2])
	}
}
