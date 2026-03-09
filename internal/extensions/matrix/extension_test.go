package matrix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"swarmstr/internal/plugins/sdk"
)

// ── Plugin metadata ───────────────────────────────────────────────────────────

func TestMatrixPlugin_ID(t *testing.T) {
	p := &MatrixPlugin{}
	if p.ID() != "matrix" {
		t.Fatalf("expected id='matrix', got %q", p.ID())
	}
}

func TestMatrixPlugin_Capabilities(t *testing.T) {
	p := &MatrixPlugin{}
	caps := p.Capabilities()
	if !caps.Reactions || !caps.Threads || !caps.Edit || !caps.MultiAccount {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}

func TestMatrixPlugin_Connect_MissingHomeserver(t *testing.T) {
	p := &MatrixPlugin{}
	_, err := p.Connect(context.Background(), "!room:server.com", map[string]any{
		"access_token": "tok",
	}, func(sdk.InboundChannelMessage) {})
	if err == nil {
		t.Fatal("expected error for missing homeserver_url")
	}
}

func TestMatrixPlugin_Connect_MissingCredentials(t *testing.T) {
	p := &MatrixPlugin{}
	_, err := p.Connect(context.Background(), "!room:server.com", map[string]any{
		"homeserver_url": "https://matrix.example.com",
		// no access_token and no username+password
	}, func(sdk.InboundChannelMessage) {})
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestMatrixPlugin_Connect_UsesConfigChannelID(t *testing.T) {
	p := &MatrixPlugin{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/account/whoami"):
			_, _ = w.Write([]byte(`{"user_id":"@bot:server.com"}`))
		case strings.Contains(r.URL.Path, "/directory/room/"):
			_, _ = w.Write([]byte(`{"room_id":"!resolved:server.com"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	h, err := p.Connect(context.Background(), "channel-map-key", map[string]any{
		"homeserver_url": srv.URL,
		"access_token":   "tok",
		"channel_id":     "#alias:server.com",
	}, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bot, ok := h.(*matrixBot)
	if !ok {
		t.Fatalf("unexpected handle type %T", h)
	}
	bot.Close()
	if bot.roomID != "!resolved:server.com" {
		t.Fatalf("expected resolved room id, got %q", bot.roomID)
	}
}

// ── matrixBot helpers ─────────────────────────────────────────────────────────

func newTestMatrixServer(handler http.Handler) (*httptest.Server, *matrixBot) {
	srv := httptest.NewServer(handler)
	bot := &matrixBot{
		channelID:      "test-ch",
		hsURL:          srv.URL,
		accessToken:    "tok",
		selfUserID:     "@bot:server.com",
		roomID:         "!room1:server.com",
		allowedSenders: map[string]bool{},
		httpClient:     srv.Client(),
		done:           make(chan struct{}),
	}
	return srv, bot
}

func TestMatrixBot_Send(t *testing.T) {
	var received map[string]any
	srv, bot := newTestMatrixServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/m.room.message/") {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"event_id":"$ev1"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.Send(context.Background(), "hello matrix"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received["msgtype"] != "m.text" {
		t.Fatalf("expected msgtype=m.text, got %v", received["msgtype"])
	}
	if received["body"] != "hello matrix" {
		t.Fatalf("expected body='hello matrix', got %v", received["body"])
	}
}

func TestMatrixBot_AddReaction(t *testing.T) {
	var received map[string]any
	srv, bot := newTestMatrixServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/m.reaction/") {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"event_id":"$ev2"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.AddReaction(context.Background(), "$original", "👍"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rel, _ := received["m.relates_to"].(map[string]any)
	if rel["rel_type"] != "m.annotation" {
		t.Fatalf("expected rel_type=m.annotation, got %v", rel["rel_type"])
	}
	if rel["key"] != "👍" {
		t.Fatalf("expected key=👍, got %v", rel["key"])
	}
}

func TestMatrixBot_RemoveReaction(t *testing.T) {
	called := false
	srv, bot := newTestMatrixServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/redact/") {
			called = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"event_id":"$redact1"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.RemoveReaction(context.Background(), "$ev2", "👍"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected redact endpoint to be called")
	}
}

func TestMatrixBot_SendInThread(t *testing.T) {
	var received map[string]any
	srv, bot := newTestMatrixServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/m.room.message/") {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"event_id":"$ev3"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.SendInThread(context.Background(), "$root1", "thread reply"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rel, _ := received["m.relates_to"].(map[string]any)
	if rel["rel_type"] != "m.thread" {
		t.Fatalf("expected rel_type=m.thread, got %v", rel["rel_type"])
	}
	if rel["event_id"] != "$root1" {
		t.Fatalf("expected event_id=$root1, got %v", rel["event_id"])
	}
}

func TestMatrixBot_EditMessage(t *testing.T) {
	var received map[string]any
	srv, bot := newTestMatrixServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/m.room.message/") {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"event_id":"$ev4"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.EditMessage(context.Background(), "$original", "updated text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rel, _ := received["m.relates_to"].(map[string]any)
	if rel["rel_type"] != "m.replace" {
		t.Fatalf("expected rel_type=m.replace, got %v", rel["rel_type"])
	}
}

// ── handleEvent ───────────────────────────────────────────────────────────────

func TestHandleEvent_Delivers(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &matrixBot{
		channelID:      "test-ch",
		selfUserID:     "@bot:server.com",
		allowedSenders: map[string]bool{},
		onMessage:      func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) },
	}

	content, _ := json.Marshal(map[string]any{"msgtype": "m.text", "body": "hello"})
	bot.handleEvent(matrixEvent{
		EventID: "$ev1",
		Type:    "m.room.message",
		Sender:  "@alice:server.com",
		Content: content,
	})

	if len(delivered) != 1 {
		t.Fatalf("expected 1 message, got %d", len(delivered))
	}
	if delivered[0].Text != "hello" {
		t.Fatalf("unexpected text: %q", delivered[0].Text)
	}
}

func TestHandleEvent_SkipsSelf(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &matrixBot{
		selfUserID:     "@bot:server.com",
		allowedSenders: map[string]bool{},
		onMessage:      func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) },
	}
	content, _ := json.Marshal(map[string]any{"msgtype": "m.text", "body": "I said this"})
	bot.handleEvent(matrixEvent{
		EventID: "$ev1", Type: "m.room.message",
		Sender:  "@bot:server.com",
		Content: content,
	})
	if len(delivered) != 0 {
		t.Fatal("expected self-message to be filtered")
	}
}

func TestHandleEvent_SkipsEdits(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &matrixBot{
		selfUserID:     "@bot:server.com",
		allowedSenders: map[string]bool{},
		onMessage:      func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) },
	}
	content, _ := json.Marshal(map[string]any{
		"msgtype": "m.text",
		"body":    "* edited",
		"m.relates_to": map[string]any{
			"rel_type": "m.replace",
			"event_id": "$original",
		},
	})
	bot.handleEvent(matrixEvent{
		EventID: "$ev2", Type: "m.room.message",
		Sender:  "@alice:server.com",
		Content: content,
	})
	if len(delivered) != 0 {
		t.Fatal("expected edit event to be filtered")
	}
}

func TestHandleEvent_AllowedSendersFilter(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &matrixBot{
		selfUserID:     "@bot:server.com",
		allowedSenders: map[string]bool{"@alice:server.com": true},
		onMessage:      func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) },
	}
	content, _ := json.Marshal(map[string]any{"msgtype": "m.text", "body": "hi"})
	// alice is allowed
	bot.handleEvent(matrixEvent{EventID: "$e1", Type: "m.room.message", Sender: "@alice:server.com", Content: content})
	// bob is not
	bot.handleEvent(matrixEvent{EventID: "$e2", Type: "m.room.message", Sender: "@bob:server.com", Content: content})
	if len(delivered) != 1 || delivered[0].SenderID != "@alice:server.com" {
		t.Fatalf("expected only alice's message, got %+v", delivered)
	}
}

func TestHandleEvent_NonTextType(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &matrixBot{
		selfUserID:     "@bot:server.com",
		allowedSenders: map[string]bool{},
		onMessage:      func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) },
	}
	// m.image should be ignored
	content, _ := json.Marshal(map[string]any{"msgtype": "m.image", "body": "img.png"})
	bot.handleEvent(matrixEvent{EventID: "$e1", Type: "m.room.message", Sender: "@alice:server.com", Content: content})
	if len(delivered) != 0 {
		t.Fatal("expected non-text message to be filtered")
	}
}
