package telegram

import (
	"context"
	"io"
	"net/http"
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
