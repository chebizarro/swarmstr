package signal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"metiq/internal/plugins/sdk"
)

// TestWebSocketReceiveDeliversEnvelopes stands up a fake signal-cli WebSocket
// receive stream that pushes both a JSON-RPC "receive" notification and a bare
// envelope, and asserts both are delivered event-driven.
func TestWebSocketReceiveDeliversEnvelopes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		// signal-cli JSON-RPC notification form.
		_ = wsjson.Write(context.Background(), conn, map[string]any{
			"jsonrpc": "2.0",
			"method":  "receive",
			"params": map[string]any{
				"envelope": map[string]any{
					"source":      "+15551112222",
					"timestamp":   1700000000000,
					"dataMessage": map[string]any{"message": "hi from signal"},
				},
			},
		})
		// Bare envelope form.
		_ = wsjson.Write(context.Background(), conn, map[string]any{
			"envelope": map[string]any{
				"source":      "+15553334444",
				"timestamp":   1700000000001,
				"dataMessage": map[string]any{"message": "bare form"},
			},
		})
		time.Sleep(250 * time.Millisecond)
	}))
	defer srv.Close()

	delivered := make(chan sdk.InboundChannelMessage, 2)
	bot := &signalBot{
		channelID: "sig-ch",
		apiURL:    srv.URL,
		account:   "+15550000000",
		done:      make(chan struct{}),
		onMessage: func(m sdk.InboundChannelMessage) { delivered <- m },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := bot.dialWS(ctx)
	if err != nil {
		t.Fatalf("dialWS: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")
	go bot.readWS(ctx, conn)

	got := map[string]string{}
	for i := 0; i < 2; i++ {
		select {
		case m := <-delivered:
			got[m.SenderID] = m.Text
		case <-ctx.Done():
			t.Fatalf("only received %d of 2 messages", i)
		}
	}
	if got["+15551112222"] != "hi from signal" {
		t.Fatalf("JSON-RPC notification not delivered: %+v", got)
	}
	if got["+15553334444"] != "bare form" {
		t.Fatalf("bare envelope not delivered: %+v", got)
	}
}

// TestDialWSFailsWithoutWebSocket verifies the fallback signal.
func TestDialWSFailsWithoutWebSocket(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	bot := &signalBot{apiURL: srv.URL, account: "+1", done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := bot.dialWS(ctx); err == nil {
		t.Fatal("expected dialWS failure without a WebSocket endpoint")
	}
}
