package discord

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

func TestDiscordFetchMessages_PopulatesReplyAndThreadMetadata(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &discordBot{
		channelID:        "discord-main",
		token:            "Bot test-token",
		discordChannelID: "thread-123",
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done: make(chan struct{}),
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case req.URL.Path == "/api/v10/channels/thread-123":
				return jsonResponse(req, `{"id":"thread-123","type":11}`), nil
			case strings.HasPrefix(req.URL.Path, "/api/v10/channels/thread-123/messages"):
				return jsonResponse(req, `[
					{
						"id":"msg-2",
						"content":"thread reply",
						"timestamp":"2026-04-05T09:00:00Z",
						"message_reference":{"message_id":"msg-1"},
						"author":{"id":"user-1","username":"alice","bot":false}
					}
				]`), nil
			default:
				t.Fatalf("unexpected path: %s", req.URL.Path)
				return nil, nil
			}
		})},
	}

	bot.fetchMessages(context.Background())

	if len(delivered) != 1 {
		t.Fatalf("expected 1 delivered message, got %d", len(delivered))
	}
	if delivered[0].ThreadID != "thread-123" {
		t.Fatalf("expected thread channel id as ThreadID, got %+v", delivered[0])
	}
	if delivered[0].ReplyToEventID != "discord-msg-1" {
		t.Fatalf("expected reply metadata, got %+v", delivered[0])
	}
	if delivered[0].SenderID != "alice#user-1" {
		t.Fatalf("unexpected sender id: %+v", delivered[0])
	}
}
