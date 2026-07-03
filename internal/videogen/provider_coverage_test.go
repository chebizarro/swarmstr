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
		VideoGenerationResult{Videos: []GeneratedVideo{{URL: "https://v"}}},
		map[string]any{"url": "https://v2"},
		map[string]any{"video": map[string]any{"local_path": "/tmp/v.mp4"}},
		map[string]any{"status": "processing", "job_id": "job-7"}, // in-flight async job is valid
	}
	for _, tc := range cases {
		if _, err := parseResult(tc, "x", "generate"); err != nil {
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

// TestVideoParseResultHardErrors locks in the parse-boundary contract: nil,
// undecodable, and payloads with no video/job/status must be hard errors that
// carry the provider and method (no silent empty success).
func TestVideoParseResultHardErrors(t *testing.T) {
	if _, err := parseResult(nil, "runway", "generate"); err == nil ||
		!strings.Contains(err.Error(), "runway") || !strings.Contains(err.Error(), "generate") ||
		!strings.Contains(err.Error(), "empty response") {
		t.Fatalf("nil response should be a contextual hard error, got %v", err)
	}
	if _, err := parseResult(map[string]any{"unrelated": "field"}, "pika", "check_job"); err == nil ||
		!strings.Contains(err.Error(), "no video, job id, or status") || !strings.Contains(err.Error(), "pika") {
		t.Fatalf("payload-less response should be a hard error, got %v", err)
	}
	if _, err := parseResult(make(chan int), "runway", "generate"); err == nil ||
		!strings.Contains(err.Error(), "encoding") {
		t.Fatalf("unmarshalable response should be a hard error, got %v", err)
	}
}

// TestVideoGatewayAsyncLifecycleAndEmptyBody drives the generic gateway adapter
// through a realistic async lifecycle: generate returns a pending job, CheckJob
// polls it to completion. It asserts the outbound request shape at each step and
// that an empty 200 body is a hard error.
func TestVideoGatewayAsyncLifecycleAndEmptyBody(t *testing.T) {
	var genReq VideoGenerationRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Fatalf("missing auth header: %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/generate":
			if r.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("expected json content-type, got %q", r.Header.Get("Content-Type"))
			}
			if err := json.NewDecoder(r.Body).Decode(&genReq); err != nil {
				t.Fatalf("decode outbound body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "processing", "job_id": "job-42"})
		case r.Method == http.MethodGet && r.URL.Path == "/jobs/job-42":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "videos": []map[string]any{{"url": "https://cdn/out.mp4", "format": "mp4", "width": 1280, "height": 720}}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	p := NewPikaProvider()
	t.Setenv("PIKA_API_KEY", "key")
	t.Setenv("PIKA_BASE_URL", srv.URL)
	if p.Name() != "Pika (gateway)" {
		t.Fatalf("expected gateway-labeled name, got %q", p.Name())
	}
	pending, err := p.Generate(context.Background(), VideoGenerationRequest{Prompt: "a dog surfing", Resolution: "1080P", AspectRatio: "16:9", Duration: 5, FPS: 24})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if genReq.Prompt != "a dog surfing" || genReq.Resolution != "1080P" || genReq.AspectRatio != "16:9" || genReq.Duration != 5 || genReq.FPS != 24 {
		t.Fatalf("outbound request body mismatch: %+v", genReq)
	}
	if pending.Provider != "pika" || pending.Status != "processing" || pending.JobID != "job-42" || len(pending.Videos) != 0 {
		t.Fatalf("unexpected pending result: %+v", pending)
	}
	done, err := p.CheckJob(context.Background(), "job-42")
	if err != nil {
		t.Fatalf("CheckJob: %v", err)
	}
	if done.Status != "completed" || len(done.Videos) != 1 || done.Videos[0].URL != "https://cdn/out.mp4" || done.Videos[0].Width != 1280 {
		t.Fatalf("unexpected completed result: %+v", done)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer empty.Close()
	t.Setenv("PIKA_BASE_URL", empty.URL)
	if _, err := p.Generate(context.Background(), VideoGenerationRequest{Prompt: "x"}); err == nil ||
		!strings.Contains(err.Error(), "empty response body") {
		t.Fatalf("empty 200 body should be a hard error, got %v", err)
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
	if !p.Configured() || p.ID() != "runway" || p.Name() != "Runway (gateway)" || !p.Capabilities().ImageToVideo {
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
