package mattermost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"metiq/internal/plugins/sdk"
)

// TestWebSocketDeliversPostedEvent stands up a fake Mattermost WebSocket events
// server that requires authentication then pushes a "posted" event, and asserts
// the event is delivered to onMessage (event-driven inbound).
func TestWebSocketDeliversPostedEvent(t *testing.T) {
	const chanID = "mmchan123"
	gotAuth := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		var challenge struct {
			Action string `json:"action"`
			Data   struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		if err := wsjson.Read(context.Background(), conn, &challenge); err != nil {
			return
		}
		gotAuth <- challenge.Data.Token

		// hello event confirms successful authentication.
		_ = wsjson.Write(context.Background(), conn, map[string]any{"event": "hello", "seq_reply": 1, "status": "OK"})

		post := map[string]any{
			"id":         "post-1",
			"user_id":    "user-9",
			"message":    "hello bot",
			"channel_id": chanID,
			"create_at":  1700000000000,
		}
		postJSON, _ := json.Marshal(post)
		_ = wsjson.Write(context.Background(), conn, map[string]any{
			"event": "posted",
			"data": map[string]any{
				"post":        string(postJSON),
				"sender_name": "@alice",
			},
			"broadcast": map[string]any{"channel_id": chanID},
		})
		time.Sleep(250 * time.Millisecond)
	}))
	defer srv.Close()

	delivered := make(chan sdk.InboundChannelMessage, 1)
	bot := &mmBot{
		channelID:   "metiq-ch",
		baseURL:     srv.URL,
		token:       "secret-token",
		mmChannelID: chanID,
		done:        make(chan struct{}),
		onMessage:   func(m sdk.InboundChannelMessage) { delivered <- m },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := bot.dialWS(ctx)
	if err != nil {
		t.Fatalf("dialWS: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	select {
	case tok := <-gotAuth:
		if tok != "secret-token" {
			t.Fatalf("unexpected auth token %q", tok)
		}
	case <-ctx.Done():
		t.Fatal("server never received auth challenge")
	}

	go bot.readWS(ctx, conn)

	select {
	case m := <-delivered:
		if m.Text != "hello bot" {
			t.Fatalf("unexpected text %q", m.Text)
		}
		if m.EventID != "mm-post-1" {
			t.Fatalf("unexpected eventID %q", m.EventID)
		}
		if m.SenderID != "user-9" {
			t.Fatalf("unexpected sender %q", m.SenderID)
		}
	case <-ctx.Done():
		t.Fatal("posted event not delivered over WebSocket")
	}
}

// TestDialWSFailsWithoutWebSocket verifies the fallback signal: dialWS returns
// an error when the server has no WebSocket endpoint, which makes run() fall
// back to REST polling.
func TestDialWSFailsWithoutWebSocket(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no websocket here", http.StatusNotFound)
	}))
	defer srv.Close()

	bot := &mmBot{baseURL: srv.URL, token: "x", done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := bot.dialWS(ctx); err == nil {
		t.Fatal("expected dialWS to fail when server has no WebSocket endpoint")
	}
}
