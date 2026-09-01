package channelmedia

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func TestKindBuckets(t *testing.T) {
	cases := []struct {
		item sdk.MediaPayloadInput
		want string
	}{
		{sdk.MediaPayloadInput{ContentType: "image/png"}, KindImage},
		{sdk.MediaPayloadInput{ContentType: "video/mp4"}, KindVideo},
		{sdk.MediaPayloadInput{ContentType: "audio/ogg; codecs=opus"}, KindAudio},
		{sdk.MediaPayloadInput{ContentType: "application/pdf"}, KindDocument},
		{sdk.MediaPayloadInput{Path: "https://cdn.example/photo.JPG?sig=abc"}, KindImage},
		{sdk.MediaPayloadInput{Path: "/tmp/clip.webm"}, KindVideo},
		{sdk.MediaPayloadInput{Path: "voice.opus"}, KindAudio},
		{sdk.MediaPayloadInput{Path: "notes.txt"}, KindDocument},
		{sdk.MediaPayloadInput{}, KindDocument},
	}
	for _, tc := range cases {
		if got := Kind(tc.item); got != tc.want {
			t.Errorf("Kind(%#v) = %q, want %q", tc.item, got, tc.want)
		}
	}
}

func TestIsHTTPURL(t *testing.T) {
	if !IsHTTPURL("https://example.com/a.png") || !IsHTTPURL("http://example.com/a") {
		t.Fatal("expected http(s) URLs to be accepted")
	}
	for _, path := range []string{"/tmp/a.png", "file:///tmp/a.png", "media-id-123", ""} {
		if IsHTTPURL(path) {
			t.Fatalf("expected %q to not be an HTTP URL", path)
		}
	}
}

func TestValidateUsesSharedContract(t *testing.T) {
	media := []sdk.MediaPayloadInput{{Path: "https://example.com/a.png", ContentType: "image/png", SizeBytes: 10}}
	if err := Validate(media, channels.MediaLimits{}); err != nil {
		t.Fatalf("valid media rejected: %v", err)
	}
	over := []sdk.MediaPayloadInput{{Path: "https://example.com/a.png", SizeBytes: 2 * channels.MB}}
	err := Validate(over, channels.MediaLimits{MaxBytes: channels.MB})
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected size limit violation, got %v", err)
	}
	if err := Validate([]sdk.MediaPayloadInput{{Path: " "}}, channels.MediaLimits{}); err == nil {
		t.Fatal("expected missing path violation")
	}
}

func TestReadLocalFileBoundedAndRejectsRemote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.bin")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, name, contentType, err := ReadLocalFile(sdk.MediaPayloadInput{Path: path}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "12345" || name != "asset.bin" || contentType != "application/octet-stream" {
		t.Fatalf("unexpected local media: data=%q name=%q contentType=%q", data, name, contentType)
	}
	if _, _, _, err := ReadLocalFile(sdk.MediaPayloadInput{Path: path}, 4); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected bounded read failure, got %v", err)
	}
	if _, _, _, err := ReadLocalFile(sdk.MediaPayloadInput{Path: "https://example.test/asset.bin"}, 5); err == nil || !strings.Contains(err.Error(), "stage the file locally") {
		t.Fatalf("expected remote URL rejection, got %v", err)
	}
}

func TestToChannelInputsAndBuildPayload(t *testing.T) {
	media := []sdk.MediaPayloadInput{
		{Path: "https://example.com/a.png", ContentType: "image/png", SizeBytes: 5},
		{Path: "https://example.com/b.pdf", ContentType: "application/pdf"},
	}
	inputs := ToChannelInputs(media)
	if len(inputs) != 2 || inputs[0].Path != media[0].Path || inputs[1].ContentType != "application/pdf" || inputs[0].SizeBytes != 5 {
		t.Fatalf("unexpected conversion: %#v", inputs)
	}
	payload := BuildPayload(media, true)
	if payload.MediaPath != media[0].Path || len(payload.MediaPaths) != 2 || len(payload.MediaTypes) != 2 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if ToChannelInputs(nil) != nil {
		t.Fatal("expected nil passthrough")
	}
}
