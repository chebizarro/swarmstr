package whatsapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestWhatsAppSendMediaSharedContract(t *testing.T) {
	var _ sdk.MediaHandle = (*whatsappBot)(nil)

	var payloads []map[string]any
	bot := &whatsappBot{
		channelID:        "wa-main",
		token:            "tok",
		phoneNumberID:    "555000",
		defaultRecipient: "15551234567",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(req.Body)
			var payload map[string]any
			_ = json.Unmarshal(raw, &payload)
			payloads = append(payloads, payload)
			return jsonResponse(req, http.StatusOK, `{"messages":[{"id":"wamid.X"}]}`), nil
		})},
	}

	err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		Text: "caption text",
		Media: []sdk.MediaPayloadInput{
			{Path: "https://cdn.example/a.png", ContentType: "image/png"},
			{Path: "uploaded-media-id-1", ContentType: "application/pdf"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 2 {
		t.Fatalf("expected 2 API calls, got %d", len(payloads))
	}
	first := payloads[0]
	if first["type"] != "image" || first["to"] != "15551234567" {
		t.Fatalf("first payload = %#v", first)
	}
	image, _ := first["image"].(map[string]any)
	if image["link"] != "https://cdn.example/a.png" || image["caption"] != "caption text" {
		t.Fatalf("first media = %#v", image)
	}
	second := payloads[1]
	if second["type"] != "document" {
		t.Fatalf("second payload = %#v", second)
	}
	document, _ := second["document"].(map[string]any)
	if document["id"] != "uploaded-media-id-1" || document["caption"] != nil || document["link"] != nil {
		t.Fatalf("second media = %#v", document)
	}

	// payload.To overrides the default recipient.
	payloads = nil
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		To:    "15559999999",
		Media: []sdk.MediaPayloadInput{{Path: "https://cdn.example/a.png", ContentType: "image/png"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 || payloads[0]["to"] != "15559999999" {
		t.Fatalf("override payloads = %#v", payloads)
	}

	// Shared-contract validation rejects bad media before any HTTP traffic.
	payloads = nil
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		Media: []sdk.MediaPayloadInput{{Path: "", SizeBytes: -1}},
	}); err == nil || len(payloads) != 0 {
		t.Fatalf("expected validation error with no API calls, got err=%v calls=%d", err, len(payloads))
	}

	// No recipient anywhere fails explicitly.
	bot.defaultRecipient = ""
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		Media: []sdk.MediaPayloadInput{{Path: "https://cdn.example/a.png"}},
	}); err == nil {
		t.Fatal("expected missing recipient error")
	}
}
