package musicgen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type musicInvoker struct {
	result any
	err    error
	calls  []map[string]any
}

func (m *musicInvoker) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
	if p, ok := params.(map[string]any); ok {
		m.calls = append(m.calls, p)
	}
	return m.result, m.err
}

func TestPluginMusicProviderGenerateConfiguredAndHelpers(t *testing.T) {
	host := &musicInvoker{result: map[string]any{"audio_url": "https://cdn/song.mp3", "format": "mp3", "duration": float64(30)}}
	p := NewPluginProvider(" Music ", map[string]any{"name": "Music Maker", "extra": "copied"}, host)
	if p.ID() != "music" || p.Name() != "Music Maker" || !p.Configured() {
		t.Fatalf("unexpected provider state id=%q name=%q configured=%v", p.ID(), p.Name(), p.Configured())
	}
	res, err := p.Generate(context.Background(), MusicGenerationRequest{Prompt: "lofi", Duration: 15, Format: "wav", Model: "m", Genre: "jazz"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Provider != "music" || res.Audio.URL != "https://cdn/song.mp3" || len(host.calls) != 1 || host.calls[0]["genre"] != "jazz" {
		t.Fatalf("unexpected result=%+v calls=%+v", res, host.calls)
	}
	if got := firstNonEmpty(" ", " x "); got != "x" {
		t.Fatalf("firstNonEmpty trimmed = %q", got)
	}
	if !boolDefault("true", false) || boolDefault("", true) != true || !isMissingProviderMethod(musicErr("is not executable")) {
		t.Fatal("helper conversion mismatch")
	}
}

func TestMusicProviderFallbacksAndParseResult(t *testing.T) {
	if NewPluginProvider("x", nil, nil).Configured() {
		t.Fatal("nil host should not configure")
	}
	p := NewPluginProvider("x", nil, &musicInvoker{result: false})
	if p.Configured() {
		t.Fatal("configured false should be false")
	}
	for _, input := range []any{
		MusicGenerationResult{Audio: GeneratedAudio{URL: "https://direct"}},
		map[string]any{"url": "https://flat", "format": "wav"},
		map[string]any{"audio_url": "https://alt"},
	} {
		if _, err := parseResult(input, "x", "generate"); err != nil {
			t.Fatalf("parseResult(%#v): %v", input, err)
		}
	}
}

// TestMusicParseResultHardErrors locks in the parse-boundary contract: nil,
// undecodable, and audio-less responses must be hard errors carrying the
// provider and method for context (no silent empty success).
func TestMusicParseResultHardErrors(t *testing.T) {
	if _, err := parseResult(nil, "suno", "generate"); err == nil ||
		!strings.Contains(err.Error(), "suno") || !strings.Contains(err.Error(), "generate") ||
		!strings.Contains(err.Error(), "empty response") {
		t.Fatalf("nil response should be a contextual hard error, got %v", err)
	}
	if _, err := parseResult(map[string]any{"unrelated": "field"}, "udio", "generate"); err == nil ||
		!strings.Contains(err.Error(), "no audio payload") || !strings.Contains(err.Error(), "udio") {
		t.Fatalf("audio-less response should be a hard error, got %v", err)
	}
	if _, err := parseResult(make(chan int), "suno", "generate"); err == nil ||
		!strings.Contains(err.Error(), "encoding") {
		t.Fatalf("unmarshalable response should be a hard error, got %v", err)
	}
}

// TestMusicGatewayRequestShapeAndEmptyBody exercises the generic gateway adapter
// end to end: it asserts the outbound request shape (auth, path, JSON body) and
// parsing of a realistic response, then verifies an empty 200 body is a hard
// error rather than a silent empty success.
func TestMusicGatewayRequestShapeAndEmptyBody(t *testing.T) {
	var gotReq MusicGenerationRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/generate" {
			t.Fatalf("unexpected request line: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected headers: %v", r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode outbound body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"audio": map[string]any{"url": "https://cdn/track.mp3", "format": "mp3"}, "duration": 30})
	}))
	defer srv.Close()
	p := NewSunoProvider()
	t.Setenv("SUNO_API_KEY", "key")
	t.Setenv("SUNO_BASE_URL", srv.URL)
	res, err := p.Generate(context.Background(), MusicGenerationRequest{Prompt: "lofi beat", Duration: 20, Format: "mp3", Model: "v3", Genre: "lofi"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotReq.Prompt != "lofi beat" || gotReq.Duration != 20 || gotReq.Format != "mp3" || gotReq.Model != "v3" || gotReq.Genre != "lofi" {
		t.Fatalf("outbound request body mismatch: %+v", gotReq)
	}
	if res.Provider != "suno" || res.Audio.URL != "https://cdn/track.mp3" || res.Audio.Format != "mp3" || res.Duration != 30 {
		t.Fatalf("unexpected parsed result: %+v", res)
	}
	if p.Name() != "Suno (gateway)" {
		t.Fatalf("expected gateway-labeled name, got %q", p.Name())
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer empty.Close()
	t.Setenv("SUNO_BASE_URL", empty.URL)
	if _, err := p.Generate(context.Background(), MusicGenerationRequest{Prompt: "x"}); err == nil ||
		!strings.Contains(err.Error(), "empty response body") {
		t.Fatalf("empty 200 body should be a hard error, got %v", err)
	}
}

func TestMusicHTTPProviderGenerateAndErrors(t *testing.T) {
	p := NewSunoProvider()
	t.Setenv("SUNO_API_KEY", "")
	t.Setenv("SUNO_BASE_URL", "")
	if p.Configured() {
		t.Fatal("expected unconfigured")
	}
	if _, err := p.Generate(context.Background(), MusicGenerationRequest{Prompt: "x"}); err == nil {
		t.Fatal("expected unconfigured error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/generate" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected headers: %v", r.Header)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"audio": map[string]any{"base64": "YXVkaW8=", "format": "mp3"}})
	}))
	defer srv.Close()
	t.Setenv("SUNO_API_KEY", "key")
	t.Setenv("SUNO_BASE_URL", srv.URL)
	res, err := p.Generate(context.Background(), MusicGenerationRequest{Prompt: "x"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Provider != "suno" || res.Audio.Format != "mp3" {
		t.Fatalf("unexpected result: %+v", res)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "bad", http.StatusTeapot) }))
	defer bad.Close()
	udio := NewUdioProvider()
	t.Setenv("UDIO_API_KEY", "key")
	t.Setenv("UDIO_BASE_URL", bad.URL)
	if _, err := udio.Generate(context.Background(), MusicGenerationRequest{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "418") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
}

type musicErr string

func (e musicErr) Error() string { return string(e) }

func TestMusicRegistryRuntimeAndMetadataBranches(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.Get("missing"); ok {
		t.Fatal("unexpected provider")
	}
	if _, err := reg.Default(); err == nil {
		t.Fatal("expected empty default error")
	}
	if err := reg.Register(nil); err == nil {
		t.Fatal("expected nil register error")
	}
	host := &musicInvoker{result: map[string]any{"url": "https://song"}}
	p := NewPluginProvider("branch", nil, host)
	if p.Name() != "branch" {
		t.Fatalf("fallback name = %q", p.Name())
	}
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	if got, ok := reg.Get("BRANCH"); !ok || got.ID() != "branch" {
		t.Fatal("registry get failed")
	}
	rt := NewRuntime(nil, nil)
	if _, err := rt.Generate(context.Background(), "missing", MusicGenerationRequest{Prompt: "x"}); err == nil {
		t.Fatal("expected missing provider error")
	}
	udio := NewUdioProvider()
	if udio.Name() != "Udio (gateway)" || udio.ID() != "udio" {
		t.Fatal("udio metadata mismatch")
	}
	if _, err := NewPluginProvider("bad", nil, nil).Generate(context.Background(), MusicGenerationRequest{}); err == nil {
		t.Fatal("expected nil host generate error")
	}
}
