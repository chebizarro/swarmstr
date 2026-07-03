package tts_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"metiq/internal/tts"
)

// TestOpenAIProvider_RequestBodyShape asserts the full OpenAI TTS request body
// (model, input, voice) and Authorization header, and parses the audio bytes.
func TestOpenAIProvider_RequestBodyShape(t *testing.T) {
	fakeAudio := []byte("MP3BYTES")
	var body map[string]any
	var authHdr, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHdr = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(fakeAudio)
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "sk-tts")
	p := tts.NewOpenAIProviderWithBaseURL(srv.URL)
	data, format, err := p.Convert(context.Background(), "Hello world", "nova")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	if authHdr != "Bearer sk-tts" {
		t.Errorf("auth header: %q", authHdr)
	}
	if gotPath != "/v1/audio/speech" {
		t.Errorf("path: %q", gotPath)
	}
	if body["model"] != "tts-1" {
		t.Errorf("model: %v", body["model"])
	}
	if body["input"] != "Hello world" {
		t.Errorf("input: %v", body["input"])
	}
	if body["voice"] != "nova" {
		t.Errorf("voice: %v", body["voice"])
	}
	if format != "mp3" || !bytes.Equal(data, fakeAudio) {
		t.Errorf("unexpected audio: format=%q data=%q", format, data)
	}
}

// TestElevenLabsProvider_RequestAndResponse asserts the ElevenLabs request
// (voice-ID path, xi-api-key header, JSON body with model_id/voice_settings)
// and parses the returned audio bytes.
func TestElevenLabsProvider_RequestAndResponse(t *testing.T) {
	fakeAudio := []byte("ELEVEN_AUDIO")
	var body map[string]any
	var apiKeyHdr, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKeyHdr = r.Header.Get("xi-api-key")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(fakeAudio)
	}))
	defer srv.Close()

	t.Setenv("ELEVENLABS_API_KEY", "el-key")
	p := tts.NewElevenLabsProviderWithBaseURL(srv.URL)
	data, format, err := p.Convert(context.Background(), "Speak this", "Rachel")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	if apiKeyHdr != "el-key" {
		t.Errorf("xi-api-key header: %q", apiKeyHdr)
	}
	// "Rachel" resolves to its well-known ElevenLabs voice ID.
	if gotPath != "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM" {
		t.Errorf("path: %q", gotPath)
	}
	if body["text"] != "Speak this" {
		t.Errorf("text: %v", body["text"])
	}
	if body["model_id"] != "eleven_monolingual_v1" {
		t.Errorf("model_id: %v", body["model_id"])
	}
	if _, ok := body["voice_settings"].(map[string]any); !ok {
		t.Errorf("expected voice_settings object, got %#v", body["voice_settings"])
	}
	if format != "mp3" || !bytes.Equal(data, fakeAudio) {
		t.Errorf("unexpected audio: format=%q data=%q", format, data)
	}
}

func TestElevenLabsProvider_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid api key"))
	}))
	defer srv.Close()

	t.Setenv("ELEVENLABS_API_KEY", "bad")
	p := tts.NewElevenLabsProviderWithBaseURL(srv.URL)
	_, _, err := p.Convert(context.Background(), "hi", "Rachel")
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestGoogleTTSProvider_RequestAndResponse asserts the Google Cloud TTS request
// (key query param, input/voice/audioConfig body) and decodes the base64
// audioContent response.
func TestGoogleTTSProvider_RequestAndResponse(t *testing.T) {
	rawAudio := []byte("GOOGLE_MP3")
	var body map[string]any
	var keyParam, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyParam = r.URL.Query().Get("key")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"audioContent": base64.StdEncoding.EncodeToString(rawAudio),
		})
	}))
	defer srv.Close()

	t.Setenv("GOOGLE_API_KEY", "g-key")
	p := tts.NewGoogleTTSProviderWithBaseURL(srv.URL)
	data, format, err := p.Convert(context.Background(), "Read aloud", "en-GB-Neural2-A")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	if keyParam != "g-key" {
		t.Errorf("key query param: %q", keyParam)
	}
	if gotPath != "/v1/text:synthesize" {
		t.Errorf("path: %q", gotPath)
	}
	input, _ := body["input"].(map[string]any)
	if input == nil || input["text"] != "Read aloud" {
		t.Errorf("unexpected input: %#v", body["input"])
	}
	voice, _ := body["voice"].(map[string]any)
	if voice == nil || voice["name"] != "en-GB-Neural2-A" || voice["languageCode"] != "en-GB" {
		t.Errorf("unexpected voice: %#v", body["voice"])
	}
	audioCfg, _ := body["audioConfig"].(map[string]any)
	if audioCfg == nil || audioCfg["audioEncoding"] != "MP3" {
		t.Errorf("unexpected audioConfig: %#v", body["audioConfig"])
	}
	if format != "mp3" || !bytes.Equal(data, rawAudio) {
		t.Errorf("unexpected audio: format=%q data=%q", format, data)
	}
}

func TestGoogleTTSProvider_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("permission denied"))
	}))
	defer srv.Close()

	t.Setenv("GOOGLE_API_KEY", "g-key")
	p := tts.NewGoogleTTSProviderWithBaseURL(srv.URL)
	_, _, err := p.Convert(context.Background(), "hi", "en-US-Neural2-A")
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("unexpected error: %v", err)
	}
}
