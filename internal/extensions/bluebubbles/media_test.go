package bluebubbles

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

func TestSendMediaUploadsLocalAttachmentAndConforms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, []byte("image-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/message/text":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["chatGuid"] != "iMessage;-;+15550001111" || body["message"] != "caption" {
				t.Errorf("unexpected text payload: %#v", body)
			}
		case "/api/v1/message/attachment":
			if got := r.URL.Query().Get("chatGuid"); got != "iMessage;-;+15550001111" {
				t.Errorf("query chatGuid=%q", got)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			if got := r.FormValue("chatGuid"); got != "iMessage;-;+15550001111" {
				t.Errorf("chatGuid=%q", got)
			}
			file, header, err := r.FormFile("attachment")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			data, _ := io.ReadAll(file)
			if header.Filename != "photo.jpg" || string(data) != "image-bytes" {
				t.Errorf("attachment filename=%q data=%q", header.Filename, data)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	bot := &bbBot{channelID: "bb-test", serverURL: server.URL, password: "secret", chatGUID: "default", httpClient: server.Client()}
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		To: "iMessage;-;+15550001111", Text: "caption", Media: []sdk.MediaPayloadInput{{Path: path, ContentType: "image/jpeg"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/api/v1/message/text" || paths[1] != "/api/v1/message/attachment" {
		t.Fatalf("unexpected request paths: %#v", paths)
	}
	if _, ok := any(bot).(sdk.MediaHandle); !ok {
		t.Fatal("BlueBubbles handle does not implement sdk.MediaHandle")
	}
	if err := sdk.ValidateChannelCapabilityContract((&BlueBubblesPlugin{}).Capabilities(), bot); err != nil {
		t.Fatalf("capability contract: %v", err)
	}
}

func TestSendMediaRejectsRemoteURL(t *testing.T) {
	bot := &bbBot{channelID: "bb-test"}
	err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{Media: []sdk.MediaPayloadInput{{Path: "https://example.test/photo.jpg"}}})
	if err == nil || !strings.Contains(err.Error(), "stage the file locally") {
		t.Fatalf("expected remote media rejection, got %v", err)
	}
}
