package musicgen

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

type fakeMusicProvider struct{}

func (f fakeMusicProvider) ID() string       { return "m" }
func (f fakeMusicProvider) Name() string     { return "m" }
func (f fakeMusicProvider) Configured() bool { return true }
func (f fakeMusicProvider) Generate(context.Context, MusicGenerationRequest) (*MusicGenerationResult, error) {
	return &MusicGenerationResult{Audio: GeneratedAudio{Base64: base64.StdEncoding.EncodeToString([]byte("mp3")), Format: "mp3"}}, nil
}
func TestMusicToolPersistsAndPrefixes(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	_ = reg.Register(fakeMusicProvider{})
	rt := NewRuntime(reg, func() string { return dir })
	out, err := Tool(ToolOptions{Runtime: rt, MediaPrefix: "MEDIA:"})(context.Background(), map[string]any{"prompt": "jazz"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "MEDIA:") {
		t.Fatalf("missing MEDIA prefix: %s", out)
	}
	path := strings.TrimPrefix(strings.SplitN(out, "\n", 2)[0], "MEDIA:")
	data, _ := os.ReadFile(path)
	if string(data) != "mp3" {
		t.Fatalf("data=%q", data)
	}
}
