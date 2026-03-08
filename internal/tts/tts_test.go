package tts_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"swarmstr/internal/tts"
)

// --- stub provider ---

type stubProvider struct {
	id         string
	name       string
	voices     []string
	configured bool
	data       []byte
	format     string
	err        error
}

func (p *stubProvider) ID() string                    { return p.id }
func (p *stubProvider) Name() string                  { return p.name }
func (p *stubProvider) Voices() []string              { return p.voices }
func (p *stubProvider) Configured() bool              { return p.configured }
func (p *stubProvider) Convert(_ context.Context, _, _ string) ([]byte, string, error) {
	return p.data, p.format, p.err
}

// --- TestManager_Providers ---

func TestManager_Providers(t *testing.T) {
	mgr := tts.NewManager()
	providers := mgr.Providers()
	if len(providers) == 0 {
		t.Fatal("expected at least one provider")
	}
	ids := map[string]bool{}
	for _, p := range providers {
		id, _ := p["id"].(string)
		if id == "" {
			t.Fatalf("provider missing id: %#v", p)
		}
		ids[id] = true
		if _, ok := p["name"]; !ok {
			t.Fatalf("provider %q missing name", id)
		}
		if _, ok := p["configured"]; !ok {
			t.Fatalf("provider %q missing configured", id)
		}
		if _, ok := p["voices"]; !ok {
			t.Fatalf("provider %q missing voices", id)
		}
	}
	if !ids["openai"] {
		t.Error("expected openai provider to be registered")
	}
	if !ids["kokoro"] {
		t.Error("expected kokoro provider to be registered")
	}
}

// --- TestManager_Register_Get ---

func TestManager_Register_Get(t *testing.T) {
	mgr := tts.NewManager()
	stub := &stubProvider{id: "mock", name: "Mock", voices: []string{"v1"}, configured: true, data: []byte("audio"), format: "mp3"}
	mgr.Register(stub)
	if got := mgr.Get("mock"); got == nil {
		t.Fatal("expected to find registered provider")
	}
	if got := mgr.Get("MOCK"); got == nil {
		t.Fatal("provider lookup should be case-insensitive")
	}
}

// --- TestManager_Convert_StubProvider ---

func TestManager_Convert_StubProvider(t *testing.T) {
	mgr := tts.NewManager()
	mgr.Register(&stubProvider{
		id: "stub", name: "Stub", voices: []string{"v1"},
		configured: true, data: []byte("fakeaudio"), format: "mp3",
	})
	res, err := mgr.Convert(context.Background(), "stub", "hello", "v1")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Provider != "stub" {
		t.Errorf("provider: got %q want %q", res.Provider, "stub")
	}
	if res.Voice != "v1" {
		t.Errorf("voice: got %q want %q", res.Voice, "v1")
	}
	if res.Format != "mp3" {
		t.Errorf("format: got %q want %q", res.Format, "mp3")
	}
	if res.AudioPath == "" {
		t.Error("expected non-empty AudioPath")
	}
	if res.AudioBase64 == "" {
		t.Error("expected non-empty AudioBase64 for small output")
	}
	// Clean up.
	os.Remove(res.AudioPath)
}

// --- TestManager_Convert_UnknownProvider ---

func TestManager_Convert_UnknownProvider(t *testing.T) {
	mgr := tts.NewManager()
	_, err := mgr.Convert(context.Background(), "no-such-provider", "hi", "")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

// --- TestManager_Convert_UnconfiguredProvider ---

func TestManager_Convert_UnconfiguredProvider(t *testing.T) {
	mgr := tts.NewManager()
	mgr.Register(&stubProvider{id: "uncfg", name: "Uncfg", voices: nil, configured: false})
	_, err := mgr.Convert(context.Background(), "uncfg", "hi", "")
	if err == nil {
		t.Fatal("expected error for unconfigured provider")
	}
}

// --- TestManager_Convert_DefaultVoice ---

func TestManager_Convert_DefaultVoice(t *testing.T) {
	mgr := tts.NewManager()
	mgr.Register(&stubProvider{
		id: "v", name: "V", voices: []string{"first-voice"},
		configured: true, data: []byte("x"), format: "wav",
	})
	res, err := mgr.Convert(context.Background(), "v", "hi", "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Voice != "first-voice" {
		t.Errorf("expected default voice %q, got %q", "first-voice", res.Voice)
	}
	os.Remove(res.AudioPath)
}

// --- TestOpenAIProvider_Convert_MockServer ---

func TestOpenAIProvider_Convert_MockServer(t *testing.T) {
	fakeAudio := []byte("FAKE_AUDIO_DATA")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("missing Authorization header")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if body["voice"] != "alloy" {
			t.Errorf("unexpected voice: %v", body["voice"])
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		w.Write(fakeAudio) //nolint:errcheck
	}))
	defer srv.Close()

	// Point the provider at our test server by monkey-patching via env.
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_TTS_BASE_URL", srv.URL) // read by provider if set

	// Use the manager with a custom openai provider backed by the mock server.
	mgr := tts.NewManager()
	mgr.Register(tts.NewOpenAIProviderWithBaseURL(srv.URL))

	res, err := mgr.Convert(context.Background(), "openai", "Hello world", "alloy")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	data, readErr := os.ReadFile(res.AudioPath)
	if readErr != nil {
		t.Fatalf("ReadFile(%s): %v", res.AudioPath, readErr)
	}
	if !bytes.Equal(data, fakeAudio) {
		t.Errorf("audio data mismatch: got %q want %q", data, fakeAudio)
	}
	os.Remove(res.AudioPath)
}

// --- TestOpenAIProvider_Configured ---

func TestOpenAIProvider_Configured(t *testing.T) {
	os.Unsetenv("OPENAI_API_KEY")
	p := tts.NewOpenAIProviderWithBaseURL("http://localhost")
	if p.Configured() {
		t.Error("should not be configured without API key")
	}
	t.Setenv("OPENAI_API_KEY", "key")
	if !p.Configured() {
		t.Error("should be configured with API key")
	}
}
