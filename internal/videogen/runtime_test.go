package videogen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"
)

type fakeVideoProvider struct{ checks int }

func (f *fakeVideoProvider) ID() string       { return "v" }
func (f *fakeVideoProvider) Name() string     { return "v" }
func (f *fakeVideoProvider) Configured() bool { return true }
func (f *fakeVideoProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Generate: true, SupportsAsync: true}
}
func (f *fakeVideoProvider) Generate(context.Context, VideoGenerationRequest) (*VideoGenerationResult, error) {
	return &VideoGenerationResult{Status: "pending", JobID: "j"}, nil
}
func (f *fakeVideoProvider) CheckJob(context.Context, string) (*VideoGenerationResult, error) {
	f.checks++
	return &VideoGenerationResult{Status: "completed", Videos: []GeneratedVideo{{Base64: base64.StdEncoding.EncodeToString([]byte("mp4")), Format: "mp4"}}}, nil
}
func TestRuntimePollsAndPersists(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	fp := &fakeVideoProvider{}
	_ = reg.Register(fp)
	rt := NewRuntime(reg, func() string { return dir }, time.Millisecond, time.Second)
	res, err := rt.Generate(context.Background(), "", VideoGenerationRequest{Prompt: "waves"})
	if err != nil {
		t.Fatal(err)
	}
	if fp.checks != 1 {
		t.Fatalf("checks=%d", fp.checks)
	}
	if len(res.Videos) != 1 || res.Videos[0].LocalPath == "" {
		t.Fatalf("bad videos %#v", res)
	}
	data, _ := os.ReadFile(res.Videos[0].LocalPath)
	if string(data) != "mp4" {
		t.Fatalf("data=%q", data)
	}
}
func TestToolConditionedValidation(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&fakeVideoProvider{})
	rt := NewRuntime(reg, nil, time.Millisecond, time.Millisecond)
	_, err := Tool(rt)(context.Background(), map[string]any{"prompt": "x", "mode": "image_to_video"})
	if err == nil {
		t.Fatal("expected source error")
	}
}
func TestToolJSON(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&fakeVideoProvider{})
	rt := NewRuntime(reg, func() string { return t.TempDir() }, time.Millisecond, time.Second)
	s, err := Tool(rt)(context.Background(), map[string]any{"prompt": "x"})
	if err != nil {
		t.Fatal(err)
	}
	var out VideoGenerationResult
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatal(err)
	}
}
