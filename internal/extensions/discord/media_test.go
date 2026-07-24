package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

type mediaRoundTrip func(*http.Request) (*http.Response, error)

func (f mediaRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestDiscordSendMediaSharedContract(t *testing.T) {
	var _ sdk.MediaHandle = (*discordBot)(nil)

	var urls []string
	var payloads []map[string]any
	bot := &discordBot{
		channelID:        "dc-main",
		token:            "Bot tok",
		discordChannelID: "123",
		httpClient: &http.Client{Transport: mediaRoundTrip(func(req *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(req.Body)
			var payload map[string]any
			_ = json.Unmarshal(raw, &payload)
			urls = append(urls, req.URL.Path)
			payloads = append(payloads, payload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"id":"999"}`)),
				Request:    req,
			}, nil
		})},
	}

	err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		Text: "look at this",
		Media: []sdk.MediaPayloadInput{
			{Path: "https://cdn.example/a.png", ContentType: "image/png"},
			{Path: "https://cdn.example/b.pdf", ContentType: "application/pdf"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 {
		t.Fatalf("expected one message, got %d", len(payloads))
	}
	if !strings.Contains(urls[0], "/channels/123/messages") {
		t.Fatalf("unexpected URL %q", urls[0])
	}
	content, _ := payloads[0]["content"].(string)
	if !strings.Contains(content, "look at this") || !strings.Contains(content, "https://cdn.example/b.pdf") {
		t.Fatalf("content = %q", content)
	}
	embeds, _ := payloads[0]["embeds"].([]any)
	if len(embeds) != 1 {
		t.Fatalf("embeds = %#v", payloads[0]["embeds"])
	}
	embed, _ := embeds[0].(map[string]any)
	if embed["url"] != "https://cdn.example/a.png" {
		t.Fatalf("embed = %#v", embed)
	}

	// payload.To overrides the bound channel.
	urls = nil
	payloads = nil
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		To:    "456",
		Media: []sdk.MediaPayloadInput{{Path: "https://cdn.example/a.png", ContentType: "image/png"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || !strings.Contains(urls[0], "/channels/456/messages") {
		t.Fatalf("override urls = %#v", urls)
	}

	// Shared-contract validation rejects bad media before any HTTP traffic.
	payloads = nil
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		Media: []sdk.MediaPayloadInput{{Path: ""}},
	}); err == nil || len(payloads) != 0 {
		t.Fatalf("expected validation error with no API calls, got err=%v calls=%d", err, len(payloads))
	}
}
