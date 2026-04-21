package tts

import (
	"os"
	"testing"
)

// ─── ElevenLabs Provider ─────────────────────────────────────────────────────

func TestElevenLabsProvider_ID(t *testing.T) {
	p := &ElevenLabsProvider{}
	if p.ID() != "elevenlabs" {
		t.Errorf("ID = %q", p.ID())
	}
}

func TestElevenLabsProvider_Name(t *testing.T) {
	p := &ElevenLabsProvider{}
	if p.Name() != "ElevenLabs TTS" {
		t.Errorf("Name = %q", p.Name())
	}
}

func TestElevenLabsProvider_Voices(t *testing.T) {
	p := &ElevenLabsProvider{}
	voices := p.Voices()
	if len(voices) == 0 {
		t.Fatal("expected non-empty voices")
	}
	// Should contain Rachel (first default)
	found := false
	for _, v := range voices {
		if v == "Rachel" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Rachel in voices: %v", voices)
	}
}

func TestElevenLabsProvider_Configured(t *testing.T) {
	os.Unsetenv("ELEVENLABS_API_KEY")
	p := &ElevenLabsProvider{}
	if p.Configured() {
		t.Error("should not be configured without API key")
	}
	t.Setenv("ELEVENLABS_API_KEY", "test-key")
	if !p.Configured() {
		t.Error("should be configured with API key")
	}
}

func TestElevenLabsProvider_Configured_Whitespace(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "   ")
	p := &ElevenLabsProvider{}
	if p.Configured() {
		t.Error("whitespace-only key should not be configured")
	}
}

// ─── Google TTS Provider ─────────────────────────────────────────────────────

func TestGoogleTTSProvider_ID(t *testing.T) {
	p := &GoogleTTSProvider{}
	if p.ID() != "google" {
		t.Errorf("ID = %q", p.ID())
	}
}

func TestGoogleTTSProvider_Name(t *testing.T) {
	p := &GoogleTTSProvider{}
	if p.Name() != "Google Cloud TTS" {
		t.Errorf("Name = %q", p.Name())
	}
}

func TestGoogleTTSProvider_Voices(t *testing.T) {
	p := &GoogleTTSProvider{}
	voices := p.Voices()
	if len(voices) == 0 {
		t.Fatal("expected non-empty voices")
	}
}

func TestGoogleTTSProvider_Configured(t *testing.T) {
	os.Unsetenv("GOOGLE_API_KEY")
	p := &GoogleTTSProvider{}
	if p.Configured() {
		t.Error("should not be configured without API key")
	}
	t.Setenv("GOOGLE_API_KEY", "test-key")
	if !p.Configured() {
		t.Error("should be configured with API key")
	}
}

// ─── Kokoro Provider ─────────────────────────────────────────────────────────

func TestKokoroProvider_ID(t *testing.T) {
	p := &KokoroProvider{}
	if p.ID() != "kokoro" {
		t.Errorf("ID = %q", p.ID())
	}
}

func TestKokoroProvider_Name(t *testing.T) {
	p := &KokoroProvider{}
	if p.Name() != "Kokoro TTS (local)" {
		t.Errorf("Name = %q", p.Name())
	}
}

func TestKokoroProvider_Voices(t *testing.T) {
	p := &KokoroProvider{}
	voices := p.Voices()
	if len(voices) == 0 {
		t.Fatal("expected non-empty voices")
	}
}

// ─── OpenAI Provider metadata ────────────────────────────────────────────────

func TestOpenAIProvider_ID(t *testing.T) {
	p := &OpenAIProvider{}
	if p.ID() != "openai" {
		t.Errorf("ID = %q", p.ID())
	}
}

func TestOpenAIProvider_Name(t *testing.T) {
	p := &OpenAIProvider{}
	if p.Name() != "OpenAI TTS" {
		t.Errorf("Name = %q", p.Name())
	}
}

func TestOpenAIProvider_Voices(t *testing.T) {
	p := &OpenAIProvider{}
	voices := p.Voices()
	if len(voices) == 0 {
		t.Fatal("expected non-empty voices")
	}
}

func TestOpenAIProvider_Configured_NoKey(t *testing.T) {
	os.Unsetenv("OPENAI_API_KEY")
	p := &OpenAIProvider{}
	if p.Configured() {
		t.Error("should not be configured without API key")
	}
}

// ─── NewOpenAIProviderWithBaseURL ────────────────────────────────────────────

func TestNewOpenAIProviderWithBaseURL(t *testing.T) {
	p := NewOpenAIProviderWithBaseURL("http://test")
	if p.baseURL != "http://test" {
		t.Errorf("baseURL = %q", p.baseURL)
	}
}

// ─── ElevenLabs voice list completeness ──────────────────────────────────────

func TestElevenLabsVoices_Count(t *testing.T) {
	if len(elevenLabsVoices) != 9 {
		t.Errorf("expected 9 default voices, got %d", len(elevenLabsVoices))
	}
}

func TestElevenLabsVoices_UniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range elevenLabsVoices {
		if seen[v.ID] {
			t.Errorf("duplicate voice ID: %s", v.ID)
		}
		seen[v.ID] = true
	}
}
