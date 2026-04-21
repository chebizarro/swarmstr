package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Deepgram Transcriber
// ═══════════════════════════════════════════════════════════════════════════════

func TestDeepgramTranscriber_NoKey(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "")
	d := &DeepgramTranscriber{}
	_, err := d.Transcribe(context.Background(), []byte("audio"), "audio/mpeg")
	if err == nil || !strings.Contains(err.Error(), "API key not configured") {
		t.Errorf("err: %v", err)
	}
}

func TestDeepgramTranscriber_Configured_ExplicitKey(t *testing.T) {
	d := &DeepgramTranscriber{APIKey: "key123"}
	if !d.Configured() {
		t.Error("expected Configured=true with explicit key")
	}
}

func TestDeepgramTranscriber_Configured_EnvKey(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "envkey")
	d := &DeepgramTranscriber{}
	if !d.Configured() {
		t.Error("expected Configured=true with env key")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Google STT Transcriber
// ═══════════════════════════════════════════════════════════════════════════════

func TestGoogleSTTTranscriber_NoKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	g := &GoogleSTTTranscriber{}
	_, err := g.Transcribe(context.Background(), []byte("audio"), "audio/wav")
	if err == nil || !strings.Contains(err.Error(), "API key not configured") {
		t.Errorf("err: %v", err)
	}
}

func TestGoogleSTTTranscriber_Configured_ExplicitKey(t *testing.T) {
	g := &GoogleSTTTranscriber{APIKey: "key"}
	if !g.Configured() {
		t.Error("expected Configured=true")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Groq Transcriber (delegates to OpenAI-compatible)
// ═══════════════════════════════════════════════════════════════════════════════

func TestGroqTranscriber_NoKey(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "")
	g := &GroqTranscriber{}
	_, err := g.Transcribe(context.Background(), []byte("audio"), "audio/mpeg")
	if err == nil || !strings.Contains(err.Error(), "API key not configured") {
		t.Errorf("err: %v", err)
	}
}

func TestGroqTranscriber_Configured_ExplicitKey(t *testing.T) {
	g := &GroqTranscriber{APIKey: "grk-key"}
	if !g.Configured() {
		t.Error("expected Configured=true")
	}
}

func TestGroqTranscriber_WithMockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "hello from groq"})
	}))
	defer srv.Close()

	// Groq delegates to OpenAITranscriber. We can test it by creating an OpenAITranscriber
	// directly with the mock base URL (since GroqTranscriber constructs one internally
	// with a hardcoded base URL, we test the pattern indirectly).
	inner := &OpenAITranscriber{APIKey: "test-key", BaseURL: srv.URL, Model: "whisper-large-v3-turbo"}
	text, err := inner.Transcribe(context.Background(), []byte("audio"), "audio/mpeg")
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello from groq" {
		t.Errorf("text: %q", text)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Mistral Transcriber (delegates to OpenAI-compatible)
// ═══════════════════════════════════════════════════════════════════════════════

func TestMistralTranscriber_NoKey(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	m := &MistralTranscriber{}
	_, err := m.Transcribe(context.Background(), []byte("audio"), "audio/mpeg")
	if err == nil || !strings.Contains(err.Error(), "API key not configured") {
		t.Errorf("err: %v", err)
	}
}

func TestMistralTranscriber_Configured_ExplicitKey(t *testing.T) {
	m := &MistralTranscriber{APIKey: "mist-key"}
	if !m.Configured() {
		t.Error("expected Configured=true")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Minimax Transcriber
// ═══════════════════════════════════════════════════════════════════════════════

func TestMinimaxTranscriber_NoKey(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "")
	m := &MinimaxTranscriber{}
	_, err := m.Transcribe(context.Background(), []byte("audio"), "audio/mpeg")
	if err == nil || !strings.Contains(err.Error(), "API key not configured") {
		t.Errorf("err: %v", err)
	}
}

func TestMinimaxTranscriber_Configured_ExplicitKey(t *testing.T) {
	m := &MinimaxTranscriber{APIKey: "mm-key"}
	if !m.Configured() {
		t.Error("expected Configured=true")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Moonshot Transcriber
// ═══════════════════════════════════════════════════════════════════════════════

func TestMoonshotTranscriber_NoKey(t *testing.T) {
	t.Setenv("MOONSHOT_API_KEY", "")
	m := &MoonshotTranscriber{}
	_, err := m.Transcribe(context.Background(), []byte("audio"), "audio/mpeg")
	if err == nil || !strings.Contains(err.Error(), "API key not configured") {
		t.Errorf("err: %v", err)
	}
}

func TestMoonshotTranscriber_Configured_ExplicitKey(t *testing.T) {
	m := &MoonshotTranscriber{APIKey: "ms-key"}
	if !m.Configured() {
		t.Error("expected Configured=true")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// OpenAI Transcriber: additional error paths
// ═══════════════════════════════════════════════════════════════════════════════

func TestOpenAITranscriber_WhisperError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "invalid audio format"},
		})
	}))
	defer srv.Close()

	tr := &OpenAITranscriber{APIKey: "key", BaseURL: srv.URL}
	_, err := tr.Transcribe(context.Background(), []byte("bad"), "audio/mpeg")
	if err == nil || !strings.Contains(err.Error(), "invalid audio format") {
		t.Errorf("err: %v", err)
	}
}

func TestOpenAITranscriber_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	tr := &OpenAITranscriber{APIKey: "key", BaseURL: srv.URL}
	_, err := tr.Transcribe(context.Background(), []byte("audio"), "audio/mpeg")
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("err: %v", err)
	}
}

func TestOpenAITranscriber_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	tr := &OpenAITranscriber{APIKey: "key", BaseURL: srv.URL}
	_, err := tr.Transcribe(context.Background(), []byte("audio"), "audio/mpeg")
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("err: %v", err)
	}
}

func TestNewOpenAITranscriberWithBaseURL_SetsField(t *testing.T) {
	tr := NewOpenAITranscriberWithBaseURL("http://localhost:1234")
	if tr.BaseURL != "http://localhost:1234" {
		t.Errorf("baseURL: %q", tr.BaseURL)
	}
}

func TestOpenAITranscriber_DefaultModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check model field in multipart form
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("model") != "whisper-1" {
			t.Errorf("model: %q", r.FormValue("model"))
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "default model ok"})
	}))
	defer srv.Close()

	tr := &OpenAITranscriber{APIKey: "key", BaseURL: srv.URL}
	text, err := tr.Transcribe(context.Background(), []byte("audio"), "")
	if err != nil {
		t.Fatal(err)
	}
	if text != "default model ok" {
		t.Errorf("text: %q", text)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// ExtractPDFText: when pdftotext is available
// ═══════════════════════════════════════════════════════════════════════════════

func TestExtractPDFText_Available(t *testing.T) {
	if !PDFExtractorAvailable() {
		t.Skip("pdftotext not installed")
	}
	// An empty/invalid PDF should produce an error from pdftotext
	_, err := ExtractPDFText(context.Background(), []byte("not a real PDF"))
	if err == nil {
		t.Error("expected error for invalid PDF data")
	}
}

func TestExtractPDFText_CancelledContext(t *testing.T) {
	if !PDFExtractorAvailable() {
		t.Skip("pdftotext not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ExtractPDFText(ctx, []byte("data"))
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// fetchURL: error path
// ═══════════════════════════════════════════════════════════════════════════════

func TestFetchURL_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	_, err := fetchURL(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err: %v", err)
	}
}

func TestFetchURL_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fetchURL(ctx, srv.URL)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// DefaultTranscriber: env var based selection
// ═══════════════════════════════════════════════════════════════════════════════

func TestDefaultTranscriber_DeepgramKey(t *testing.T) {
	// Clear all keys then set Deepgram
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GROQ_API_KEY", "")
	t.Setenv("DEEPGRAM_API_KEY", "dg-key")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("MINIMAX_API_KEY", "")
	t.Setenv("MOONSHOT_API_KEY", "")
	t.Setenv("MISTRAL_API_KEY", "")

	tr := DefaultTranscriber()
	if tr == nil {
		t.Fatal("expected non-nil transcriber")
	}
}

func TestDefaultTranscriber_OpenAIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("GROQ_API_KEY", "")
	t.Setenv("DEEPGRAM_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("MINIMAX_API_KEY", "")
	t.Setenv("MOONSHOT_API_KEY", "")
	t.Setenv("MISTRAL_API_KEY", "")

	tr := DefaultTranscriber()
	if tr == nil {
		t.Fatal("expected non-nil transcriber")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// RegisterTranscriber: duplicate registration panics
// ═══════════════════════════════════════════════════════════════════════════════

func TestRegisterTranscriber_DuplicatePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic on duplicate registration")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "already registered") {
			t.Errorf("unexpected panic: %v", r)
		}
	}()
	// "openai" is already registered by init(); this should panic.
	RegisterTranscriber("openai", func() Transcriber {
		return NewOpenAITranscriber()
	})
}
