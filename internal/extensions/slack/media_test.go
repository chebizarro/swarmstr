package slack

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

func TestSlackSendMediaSharedContract(t *testing.T) {
	var _ sdk.MediaHandle = (*slackBot)(nil)

	var payloads []map[string]any
	bot := &slackBot{
		channelID:      "sl-main",
		token:          "xoxb-tok",
		slackChannelID: "C123",
		httpClient: &http.Client{Transport: mediaRoundTrip(func(req *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(req.Body)
			var payload map[string]any
			_ = json.Unmarshal(raw, &payload)
			payloads = append(payloads, payload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":true,"ts":"111.222"}`)),
				Request:    req,
			}, nil
		})},
	}

	err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		Text: "release chart",
		Media: []sdk.MediaPayloadInput{
			{Path: "https://cdn.example/a.png", ContentType: "image/png"},
			{Path: "https://cdn.example/b.pdf", ContentType: "application/pdf"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 {
		t.Fatalf("expected one chat.postMessage call, got %d", len(payloads))
	}
	payload := payloads[0]
	if payload["channel"] != "C123" {
		t.Fatalf("channel = %#v", payload["channel"])
	}
	blocks, _ := payload["blocks"].([]any)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %#v", payload["blocks"])
	}
	image, _ := blocks[1].(map[string]any)
	if image["type"] != "image" || image["image_url"] != "https://cdn.example/a.png" {
		t.Fatalf("image block = %#v", image)
	}
	text, _ := payload["text"].(string)
	if !strings.Contains(text, "release chart") || !strings.Contains(text, "https://cdn.example/b.pdf") {
		t.Fatalf("fallback text = %q", text)
	}

	// payload.To overrides the bound channel.
	payloads = nil
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		To:    "C456",
		Media: []sdk.MediaPayloadInput{{Path: "https://cdn.example/a.png", ContentType: "image/png"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 || payloads[0]["channel"] != "C456" {
		t.Fatalf("override payloads = %#v", payloads)
	}

	// Shared-contract validation rejects bad media before any HTTP traffic.
	payloads = nil
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		Media: []sdk.MediaPayloadInput{{Path: ""}},
	}); err == nil || len(payloads) != 0 {
		t.Fatalf("expected validation error with no API calls, got err=%v calls=%d", err, len(payloads))
	}
}
