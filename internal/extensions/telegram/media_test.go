package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestTelegramSendMediaSharedContract(t *testing.T) {
	var _ sdk.MediaHandle = (*telegramBot)(nil)

	var calls []struct {
		method  string
		payload map[string]any
	}
	bot := &telegramBot{
		channelID:  "tg-main",
		token:      "tok",
		lastChatID: "555",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(req.Body)
			var payload map[string]any
			_ = json.Unmarshal(raw, &payload)
			parts := strings.Split(req.URL.Path, "/")
			calls = append(calls, struct {
				method  string
				payload map[string]any
			}{parts[len(parts)-1], payload})
			return jsonResponse(req, `{"ok":true}`), nil
		})},
	}

	err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		Text: "caption text",
		Media: []sdk.MediaPayloadInput{
			{Path: "https://cdn.example/a.png", ContentType: "image/png"},
			{Path: "https://cdn.example/b.pdf", ContentType: "application/pdf"},
			{Path: "https://cdn.example/c.gif", ContentType: "image/gif"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected 3 API calls, got %d", len(calls))
	}
	if calls[0].method != "sendPhoto" || calls[0].payload["photo"] != "https://cdn.example/a.png" || calls[0].payload["caption"] != "caption text" {
		t.Fatalf("first call = %#v", calls[0])
	}
	if calls[1].method != "sendDocument" || calls[1].payload["caption"] != nil {
		t.Fatalf("second call = %#v", calls[1])
	}
	if calls[2].method != "sendAnimation" {
		t.Fatalf("third call = %#v", calls[2])
	}
	for _, call := range calls {
		if call.payload["chat_id"] != "555" {
			t.Fatalf("chat_id = %#v", call.payload["chat_id"])
		}
	}

	// payload.To overrides the last inbound chat.
	calls = nil
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		To:    "777",
		Media: []sdk.MediaPayloadInput{{Path: "https://cdn.example/a.png", ContentType: "image/png"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].payload["chat_id"] != "777" {
		t.Fatalf("override call = %#v", calls)
	}

	// Shared-contract validation runs before any HTTP traffic.
	calls = nil
	err = bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		Media: []sdk.MediaPayloadInput{{Path: ""}},
	})
	if err == nil || len(calls) != 0 {
		t.Fatalf("expected validation error with no API calls, got err=%v calls=%d", err, len(calls))
	}
}

// Telegram's JSON photo/document fields accept only a URL or file_id, so a
// staged local file must be uploaded as multipart form data.
func TestTelegramSendMediaUploadsLocalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(path, []byte("PNGDATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	var method, chatID, caption, filename, content string
	bot := &telegramBot{
		channelID:  "tg-main",
		token:      "tok",
		lastChatID: "555",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			method = req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
			if err := req.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("expected multipart upload: %v", err)
				return jsonResponse(req, `{"ok":true}`), nil
			}
			chatID, caption = req.FormValue("chat_id"), req.FormValue("caption")
			file, header, err := req.FormFile("photo")
			if err != nil {
				t.Errorf("photo form file: %v", err)
				return jsonResponse(req, `{"ok":true}`), nil
			}
			defer file.Close()
			raw, _ := io.ReadAll(file)
			filename, content = header.Filename, string(raw)
			return jsonResponse(req, `{"ok":true}`), nil
		})},
	}
	err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		Text:  "look",
		Media: []sdk.MediaPayloadInput{{Path: path, ContentType: "image/png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if method != "sendPhoto" || chatID != "555" || caption != "look" || filename != "chart.png" || content != "PNGDATA" {
		t.Fatalf("upload = method %q chat %q caption %q file %q content %q", method, chatID, caption, filename, content)
	}
}
