package feishu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestSendMediaUploadsAndSendsFeishuPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var messages []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("missing authorization: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/open-apis/im/v1/images":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			if r.FormValue("image_type") != "message" {
				t.Errorf("image_type=%q", r.FormValue("image_type"))
			}
			file, header, err := r.FormFile("image")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			data, _ := io.ReadAll(file)
			if header.Filename != "photo.png" || string(data) != "png-bytes" {
				t.Errorf("upload filename=%q data=%q", header.Filename, data)
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"image_key":"img-key"}}`)
		case "/open-apis/im/v1/messages":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			messages = append(messages, body)
			_, _ = io.WriteString(w, `{"code":0}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	bot := &feishuBot{channelID: "feishu-test", chatID: "default-chat", baseURL: server.URL, httpClient: server.Client(), accessToken: "token"}
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		To: "target-chat", Text: "caption", Media: []sdk.MediaPayloadInput{{Path: path, ContentType: "image/png"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0]["msg_type"] != "text" || messages[1]["msg_type"] != "image" {
		t.Fatalf("unexpected messages: %#v", messages)
	}
	if messages[1]["receive_id"] != "target-chat" || !strings.Contains(messages[1]["content"].(string), "img-key") {
		t.Fatalf("unexpected media payload: %#v", messages[1])
	}
	if err := sdk.ValidateChannelCapabilityContract((&FeishuPlugin{}).Capabilities(), bot); err != nil {
		t.Fatalf("capability contract: %v", err)
	}
}

func TestSendMediaRejectsRemoteURL(t *testing.T) {
	bot := &feishuBot{channelID: "feishu-test"}
	err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{Media: []sdk.MediaPayloadInput{{Path: "https://example.test/photo.png"}}})
	if err == nil || !strings.Contains(err.Error(), "stage the file locally") {
		t.Fatalf("expected remote media rejection, got %v", err)
	}
}
