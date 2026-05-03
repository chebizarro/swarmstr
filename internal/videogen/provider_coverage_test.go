package videogen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type videoInvoker struct {
	result any
	err    error
	calls  []videoCall
}

type videoCall struct {
	providerID string
	method     string
	params     map[string]any
}

func (v *videoInvoker) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
	m, _ := params.(map[string]any)
	v.calls = append(v.calls, videoCall{providerID: providerID, method: method, params: m})
	return v.result, v.err
}

func TestPluginProviderGenerateAndCheckJob(t *testing.T) {
	host := &videoInvoker{result: map[string]any{"url": "https://cdn/video.mp4"}}
	p := NewPluginProvider(" DemoVideo ", map[string]any{
		"name": "Demo Video",
		"capabilities": map[string]any{
			"imageToVideo":   true,
			"video_to_video": "true",
			"supportsAsync":  false,
			"resolutions":    []any{"720P", "1080P"},
			"aspectRatios":   []string{"16:9"},
			"maxDuration":    float64(12),
		},
	}, host)
	if p.ID() != "demovideo" || p.Name() != "Demo Video" || !p.Configured() {
		t.Fatalf("unexpected provider metadata: id=%q name=%q configured=%v", p.ID(), p.Name(), p.Configured())
	}
	caps := p.Capabilities()
	if !caps.ImageToVideo || !caps.VideoToVideo || caps.SupportsAsync || caps.MaxDuration != 12 || len(caps.Resolutions) != 2 {
		t.Fatalf("unexpected caps: %+v", caps)
	}
	res, err := p.Generate(context.Background(), VideoGenerationRequest{Prompt: "waves", Mode: "image-to-video"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Provider != "demovideo" || res.Status != "completed" || len(res.Videos) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := host.calls[len(host.calls)-1].params["mode"]; got != "imageToVideo" {
		t.Fatalf("plugin mode = %v", got)
	}

	host.result = map[string]any{"videos": []map[string]any{{"base64": base64.StdEncoding.EncodeToString([]byte("mp4")), "format": "mov"}}, "status": "completed", "job_id": "job-1"}
	res, err = p.CheckJob(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("CheckJob: %v", err)
	}
	if res.Provider != "demovideo" || res.JobID != "job-1" || res.Videos[0].Format != "mov" {
		t.Fatalf("unexpected check result: %+v", res)
	}
}

func TestPluginProviderConfiguredFallbacks(t *testing.T) {
	if NewPluginProvider("x", nil, nil).Configured() {
		t.Fatal("nil host should not be configured")
	}
	p := NewPluginProvider("x", nil, &videoInvoker{result: "false"})
	if p.Configured() {
		t.Fatal("string false should be false")
	}
	p = NewPluginProvider("x", nil, &videoInvoker{err: errString("unknown provider method configured")})
	if !p.Configured() {
		t.Fatal("missing configured method should default to true")
	}
}

func TestParseVideoResultAndHelpers(t *testing.T) {
	cases := []any{
		nil,
		VideoGenerationResult{Videos: []GeneratedVideo{{URL: "https://v"}}},
		map[string]any{"url": "https://v2"},
		map[string]any{"video": map[string]any{"local_path": "/tmp/v.mp4"}},
	}
	for _, tc := range cases {
		if _, err := parseResult(tc); err != nil {
			t.Fatalf("parseResult(%#v): %v", tc, err)
		}
	}
	if canonicalMode("video-to-video") != "video_to_video" || pluginMode("video_to_video") != "videoToVideo" {
		t.Fatal("mode normalization mismatch")
	}
	if boolDefault("1", false) != true || intValue(int64(4)) != 4 || intValue(3.9) != 3 || len(stringSlice([]any{"a", 7, "b"})) != 2 {
		t.Fatal("helper conversion mismatch")
	}
	if !isMissingProviderMethod(errString("not a function")) || normalizeID(" X ") != "x" {
		t.Fatal("helper predicate mismatch")
	}
}

func TestHTTPVideoProviderGenerateAndCheckJob(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing auth header: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "url": "https://cdn/out.mp4"})
	}))
	defer srv.Close()
	t.Setenv("RUNWAY_API_KEY", "test-key")
	t.Setenv("RUNWAY_BASE_URL", srv.URL)
	p := NewRunwayProvider()
	if !p.Configured() || p.ID() != "runway" || p.Name() != "Runway" || !p.Capabilities().ImageToVideo {
		t.Fatalf("unexpected HTTP provider metadata")
	}
	if _, err := p.Generate(context.Background(), VideoGenerationRequest{Prompt: "x"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := p.CheckJob(context.Background(), "abc"); err != nil {
		t.Fatalf("CheckJob: %v", err)
	}
	joined := strings.Join(seen, ",")
	if !strings.Contains(joined, "POST /v1/video/generations") || !strings.Contains(joined, "GET /v1/video/generations/abc") {
		t.Fatalf("unexpected requests: %v", seen)
	}
}

func TestHTTPVideoProviderErrors(t *testing.T) {
	p := NewPikaProvider()
	t.Setenv("PIKA_API_KEY", "")
	t.Setenv("PIKA_BASE_URL", "")
	if _, err := p.Generate(context.Background(), VideoGenerationRequest{Prompt: "x"}); err == nil {
		t.Fatal("expected unconfigured error")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer srv.Close()
	t.Setenv("PIKA_API_KEY", "key")
	t.Setenv("PIKA_BASE_URL", srv.URL)
	if _, err := p.Generate(context.Background(), VideoGenerationRequest{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
