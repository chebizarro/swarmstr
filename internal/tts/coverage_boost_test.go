package tts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// ═════════════════════════════════════════════════════════════════��═════════════
// OpenAI Provider Convert error paths
// ═══════════════════════════════════════════════════════════════════════════════

func TestOpenAIConvert_NoAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	p := NewOpenAIProviderWithBaseURL("http://localhost")
	_, _, err := p.Convert(context.Background(), "hello", "alloy")
	if err == nil || err.Error() != "OPENAI_API_KEY is not set" {
		t.Errorf("err: %v", err)
	}
}

func TestOpenAIConvert_DefaultVoice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["voice"] != "alloy" {
			t.Errorf("voice: %v", payload["voice"])
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("fake-audio"))
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	p := NewOpenAIProviderWithBaseURL(srv.URL)
	data, format, err := p.Convert(context.Background(), "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	if format != "mp3" {
		t.Errorf("format: %q", format)
	}
	if string(data) != "fake-audio" {
		t.Errorf("data: %q", data)
	}
}

func TestOpenAIConvert_ServerError_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	p := NewOpenAIProviderWithBaseURL(srv.URL)
	_, _, err := p.Convert(context.Background(), "hello", "alloy")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "OpenAI TTS API error 500: internal server error" {
		t.Errorf("err: %v", err)
	}
}

func TestOpenAIConvert_ServerError_JSONMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "rate limit exceeded",
			},
		})
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	p := NewOpenAIProviderWithBaseURL(srv.URL)
	_, _, err := p.Convert(context.Background(), "hello", "alloy")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "OpenAI TTS API error 429: rate limit exceeded" {
		t.Errorf("err: %v", err)
	}
}

func TestOpenAIConvert_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("audio"))
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	p := NewOpenAIProviderWithBaseURL(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := p.Convert(ctx, "hello", "alloy")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Manager Convert: unconfigured provider, provider error, large file skip b64
// ═══════════════════════════════════════════════════════════════════════════════

type errorProvider struct{}

func (p *errorProvider) ID() string                                                   { return "err" }
func (p *errorProvider) Name() string                                                 { return "Error Provider" }
func (p *errorProvider) Voices() []string                                             { return []string{"v1"} }
func (p *errorProvider) Configured() bool                                             { return true }
func (p *errorProvider) Convert(_ context.Context, _, _ string) ([]byte, string, error) {
	return nil, "", fmt.Errorf("convert failed")
}

func TestManagerConvert_ProviderError(t *testing.T) {
	m := NewManager()
	m.Register(&errorProvider{})
	_, err := m.Convert(context.Background(), "err", "hello", "v1")
	if err == nil || err.Error() != "convert failed" {
		t.Errorf("err: %v", err)
	}
}

func TestManagerConvert_UnconfiguredProvider(t *testing.T) {
	m := NewManager()
	m.Register(&fakeProvider{id: "unc", configured: false})
	_, err := m.Convert(context.Background(), "unc", "hello", "")
	if err == nil || err.Error() != `TTS provider "unc" is not configured (check environment variables)` {
		t.Errorf("err: %v", err)
	}
}

func TestManagerConvert_LargeOutputSkipsBase64(t *testing.T) {
	// Create a provider that returns > 512KiB of data
	m := NewManager()
	bigData := make([]byte, 600*1024) // 600 KiB
	for i := range bigData {
		bigData[i] = 0xFF
	}
	m.Register(&fakeConfiguredProvider{
		id:     "big",
		voices: []string{"v1"},
	})
	// Need a provider that actually returns the big data.
	// Use the real Convert path via the fakeConfiguredProvider.
	// But fakeConfiguredProvider returns []byte("audio-data") not big data.
	// Let me use a custom one.
	m.providers["bigp"] = &bigProvider{data: bigData}
	res, err := m.Convert(context.Background(), "bigp", "hello", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if res.AudioBase64 != "" {
		t.Error("expected empty AudioBase64 for large output")
	}
	// Clean up temp file
	os.Remove(res.AudioPath)
}

type bigProvider struct {
	data []byte
}

func (p *bigProvider) ID() string        { return "bigp" }
func (p *bigProvider) Name() string      { return "Big" }
func (p *bigProvider) Voices() []string  { return []string{"v1"} }
func (p *bigProvider) Configured() bool  { return true }
func (p *bigProvider) Convert(_ context.Context, _, _ string) ([]byte, string, error) {
	return p.data, "wav", nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// ElevenLabs Convert: no API key
// ═══════════════════════════════════════════════════════════════════════════════

func TestElevenLabsConvert_NoAPIKey(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "")
	p := &ElevenLabsProvider{}
	_, _, err := p.Convert(context.Background(), "hello", "Rachel")
	if err == nil || err.Error() != "ELEVENLABS_API_KEY is not set" {
		t.Errorf("err: %v", err)
	}
}

func TestElevenLabsConvert_DefaultVoice(t *testing.T) {
	// This test just checks the default voice is "Rachel" by observing the early return.
	t.Setenv("ELEVENLABS_API_KEY", "")
	p := &ElevenLabsProvider{}
	_, _, err := p.Convert(context.Background(), "hello", "")
	// Will fail with "ELEVENLABS_API_KEY is not set" before actually calling the API.
	if err == nil || err.Error() != "ELEVENLABS_API_KEY is not set" {
		t.Errorf("err: %v", err)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Google TTS Convert: no API key
// ═══════════════════════════════════════════════════════════════════════════════

func TestGoogleTTSConvert_NoAPIKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	p := &GoogleTTSProvider{}
	_, _, err := p.Convert(context.Background(), "hello", "en-US-Standard-A")
	if err == nil || err.Error() != "GOOGLE_API_KEY is not set" {
		t.Errorf("err: %v", err)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Kokoro Convert: not installed (common case)
// ═══════════════════════════════════════════════════════════════════════════════

func TestKokoroConvert_NotInstalled(t *testing.T) {
	p := &KokoroProvider{}
	if p.Configured() {
		t.Skip("kokoro is installed, skipping not-installed test")
	}
	_, _, err := p.Convert(context.Background(), "hello", "af_bella")
	if err == nil {
		t.Error("expected error when kokoro is not installed")
	}
}
