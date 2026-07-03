package bluebubbles

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"metiq/internal/plugins/sdk"
)

// TestSocketIODeliversNewMessage stands up a fake Engine.IO v4 / Socket.IO
// server that completes the handshake and emits a "new-message" event, and
// asserts the message is delivered event-driven.
func TestSocketIODeliversNewMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		ctx := context.Background()

		// Engine.IO OPEN packet.
		_ = conn.Write(ctx, websocket.MessageText, []byte(`0{"pingInterval":25000,"pingTimeout":20000}`))

		// Expect Socket.IO CONNECT ("40") to the default namespace.
		_, d, err := conn.Read(ctx)
		if err != nil || string(d) != "40" {
			return
		}
		// CONNECT acknowledgement.
		_ = conn.Write(ctx, websocket.MessageText, []byte(`40{"sid":"abc"}`))

		// new-message EVENT (Engine.IO MESSAGE '4' + Socket.IO EVENT '2').
		msg := map[string]any{
			"guid":        "guid-1",
			"text":        "imessage push",
			"isFromMe":    false,
			"handle":      map[string]any{"address": "+15551234567"},
			"dateCreated": 1700000000000,
		}
		payload, _ := json.Marshal([]any{"new-message", msg})
		_ = conn.Write(ctx, websocket.MessageText, append([]byte("42"), payload...))
		time.Sleep(250 * time.Millisecond)
	}))
	defer srv.Close()

	delivered := make(chan sdk.InboundChannelMessage, 1)
	bot := &bbBot{
		channelID:      "bb-ch",
		serverURL:      srv.URL,
		password:       "pw",
		allowedSenders: map[string]bool{},
		seenGUIDs:      map[string]struct{}{},
		done:           make(chan struct{}),
		onMessage:      func(m sdk.InboundChannelMessage) { delivered <- m },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := bot.socketConnect(ctx)
	if err != nil {
		t.Fatalf("socketConnect: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")
	go bot.socketServe(ctx, conn)

	select {
	case m := <-delivered:
		if m.Text != "imessage push" {
			t.Fatalf("unexpected text %q", m.Text)
		}
		if m.EventID != "guid-1" {
			t.Fatalf("unexpected eventID %q", m.EventID)
		}
		if m.SenderID != "+15551234567" {
			t.Fatalf("unexpected sender %q", m.SenderID)
		}
	case <-ctx.Done():
		t.Fatal("new-message event not delivered over Socket.IO")
	}
}

// TestSocketConnectFailsWithoutSocketIO verifies the fallback signal: when the
// server has no Socket.IO endpoint, socketConnect errors and run() falls back to
// REST polling.
func TestSocketConnectFailsWithoutSocketIO(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no socket.io", http.StatusNotFound)
	}))
	defer srv.Close()

	bot := &bbBot{serverURL: srv.URL, password: "pw", done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := bot.socketConnect(ctx); err == nil {
		t.Fatal("expected socketConnect to fail without a Socket.IO endpoint")
	}
}
