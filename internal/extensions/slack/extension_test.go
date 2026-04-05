package slack

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

func TestSlackFetchMessages_PopulatesThreadMetadata(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &slackBot{
		channelID:      "slack-main",
		token:          "xoxb-test",
		slackChannelID: "C123",
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done: make(chan struct{}),
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/api/conversations.history" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			return jsonResponse(req, `{
				"ok": true,
				"messages": [
					{"type":"message","user":"U123","text":"thread reply","ts":"171.200","thread_ts":"171.100"},
					{"type":"message","user":"U123","text":"thread root","ts":"171.100","thread_ts":"171.100"},
					{"type":"message","user":"U123","text":"root message","ts":"171.050"}
				]
			}`), nil
		})},
	}

	bot.fetchMessages(context.Background())

	if len(delivered) != 3 {
		t.Fatalf("expected 3 delivered messages, got %d", len(delivered))
	}
	if delivered[0].ThreadID != "" || delivered[0].ReplyToEventID != "" {
		t.Fatalf("expected ordinary root message to have no thread metadata, got %+v", delivered[0])
	}
	if delivered[1].ThreadID != "" || delivered[1].ReplyToEventID != "" {
		t.Fatalf("expected top-level thread root to stay unthreaded in inbound metadata, got %+v", delivered[1])
	}
	if delivered[2].ThreadID != "171.100" {
		t.Fatalf("expected threaded reply to use root ts as ThreadID, got %+v", delivered[2])
	}
	if delivered[2].ReplyToEventID != "slack-171.100" {
		t.Fatalf("expected threaded reply to point at root event, got %+v", delivered[2])
	}
}
