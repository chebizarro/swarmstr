package nextcloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestSendMediaUploadsWebDAVAndSharesToTalk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("pdf-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	var uploadPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "bot" || pass != "app-pass" {
			t.Errorf("unexpected auth user=%q pass=%q", user, pass)
		}
		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/remote.php/dav/files/bot/Talk/"):
			uploadPath = r.URL.Path
			data, _ := io.ReadAll(r.Body)
			if string(data) != "pdf-data" || r.Header.Get("Content-Type") != "application/pdf" {
				t.Errorf("upload data=%q contentType=%q", data, r.Header.Get("Content-Type"))
			}
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/ocs/v2.php/apps/files_sharing/api/v1/shares":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("shareType") != "10" || r.Form.Get("shareWith") != "room-2" || !strings.HasPrefix(r.Form.Get("path"), "/Talk/metiq-") || !strings.Contains(r.Form.Get("talkMetaData"), `"caption":"caption"`) {
				t.Errorf("unexpected share form: %#v", r.Form)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	bot := &nextcloudBot{channelID: "nc-test", baseURL: server.URL, username: "bot", appPassword: "app-pass", roomToken: "room-1", httpClient: server.Client()}
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		To: "room-2", Text: "caption", Media: []sdk.MediaPayloadInput{{Path: path, ContentType: "application/pdf"}},
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(uploadPath, "-report.pdf") {
		t.Fatalf("unexpected upload path %q", uploadPath)
	}
	if err := sdk.ValidateChannelCapabilityContract((&NextcloudPlugin{}).Capabilities(), bot); err != nil {
		t.Fatalf("capability contract: %v", err)
	}
}

func TestSendMediaRejectsRemoteURL(t *testing.T) {
	bot := &nextcloudBot{channelID: "nc-test"}
	err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{Media: []sdk.MediaPayloadInput{{Path: "https://example.test/report.pdf"}}})
	if err == nil || !strings.Contains(err.Error(), "stage the file locally") {
		t.Fatalf("expected remote media rejection, got %v", err)
	}
}
