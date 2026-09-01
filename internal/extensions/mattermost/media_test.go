package mattermost

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

func TestSendMediaUploadsFileAndCreatesPost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("pdf-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var post map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/v4/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			if r.FormValue("channel_id") != "target-channel" {
				t.Errorf("channel_id=%q", r.FormValue("channel_id"))
			}
			file, header, err := r.FormFile("files")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			data, _ := io.ReadAll(file)
			if header.Filename != "report.pdf" || string(data) != "pdf-bytes" {
				t.Errorf("upload filename=%q data=%q", header.Filename, data)
			}
			_, _ = io.WriteString(w, `{"file_infos":[{"id":"file-1"}]}`)
		case "/api/v4/posts":
			if err := json.NewDecoder(r.Body).Decode(&post); err != nil {
				t.Error(err)
			}
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	bot := &mmBot{channelID: "mm-test", baseURL: server.URL, token: "token", mmChannelID: "default-channel", httpClient: server.Client()}
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		To: "target-channel", Text: "caption", ReplyToID: "mm-root-1", Media: []sdk.MediaPayloadInput{{Path: path, ContentType: "application/pdf"}},
	}); err != nil {
		t.Fatal(err)
	}
	fileIDs, ok := post["file_ids"].([]any)
	if post["channel_id"] != "target-channel" || post["message"] != "caption" || post["root_id"] != "root-1" || !ok || len(fileIDs) != 1 || fileIDs[0] != "file-1" {
		t.Fatalf("unexpected post payload: %#v", post)
	}
	if err := sdk.ValidateChannelCapabilityContract((&MattermostPlugin{}).Capabilities(), bot); err != nil {
		t.Fatalf("capability contract: %v", err)
	}
}

func TestSendMediaRejectsRemoteURL(t *testing.T) {
	bot := &mmBot{channelID: "mm-test"}
	err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{Media: []sdk.MediaPayloadInput{{Path: "https://example.test/report.pdf"}}})
	if err == nil || !strings.Contains(err.Error(), "stage the file locally") {
		t.Fatalf("expected remote media rejection, got %v", err)
	}
}
