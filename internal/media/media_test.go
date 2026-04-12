package media

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── MediaAttachment helpers ─────────────────────────────────────────────────

func TestMediaAttachment_TypeHelpers(t *testing.T) {
	cases := []struct {
		att   MediaAttachment
		image bool
		audio bool
		pdf   bool
	}{
		{MediaAttachment{Type: "image"}, true, false, false},
		{MediaAttachment{Type: "IMAGE"}, true, false, false},
		{MediaAttachment{Type: "audio"}, false, true, false},
		{MediaAttachment{Type: "pdf"}, false, false, true},
		{MediaAttachment{Type: "PDF"}, false, false, true},
		{MediaAttachment{Type: "other"}, false, false, false},
	}
	for _, c := range cases {
		if got := c.att.IsImage(); got != c.image {
			t.Errorf("IsImage(%q) = %v, want %v", c.att.Type, got, c.image)
		}
		if got := c.att.IsAudio(); got != c.audio {
			t.Errorf("IsAudio(%q) = %v, want %v", c.att.Type, got, c.audio)
		}
		if got := c.att.IsPDF(); got != c.pdf {
			t.Errorf("IsPDF(%q) = %v, want %v", c.att.Type, got, c.pdf)
		}
	}
}

// ─── ResolveImage ────────────────────────────────────────────────────────────

func TestResolveImage_Base64(t *testing.T) {
	data := "aGVsbG8="
	ref, err := ResolveImage(MediaAttachment{Type: "image", Base64: data, MimeType: "image/png"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Base64 != data {
		t.Errorf("Base64 = %q, want %q", ref.Base64, data)
	}
	if ref.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want image/png", ref.MimeType)
	}
	if ref.URL != "" {
		t.Errorf("URL should be empty, got %q", ref.URL)
	}
}

func TestResolveImage_URL(t *testing.T) {
	ref, err := ResolveImage(MediaAttachment{Type: "image", URL: "https://example.com/photo.jpg"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.URL != "https://example.com/photo.jpg" {
		t.Errorf("URL = %q", ref.URL)
	}
	if ref.Base64 != "" {
		t.Errorf("Base64 should be empty")
	}
}

func TestResolveImage_DefaultMimeType(t *testing.T) {
	ref, err := ResolveImage(MediaAttachment{Type: "image", Base64: "aGVsbG8="})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.MimeType != "image/jpeg" {
		t.Errorf("default MimeType = %q, want image/jpeg", ref.MimeType)
	}
}

func TestResolveImage_Empty(t *testing.T) {
	_, err := ResolveImage(MediaAttachment{Type: "image"})
	if err == nil {
		t.Error("expected error for empty attachment")
	}
}

// ─── InlineImageText ─────────────────────────────────────────────────────────

func TestInlineImageText_URL(t *testing.T) {
	att := MediaAttachment{Type: "image", URL: "https://example.com/img.png"}
	got := InlineImageText(att)
	if !strings.Contains(got, "https://example.com/img.png") {
		t.Errorf("expected URL in text, got %q", got)
	}
}

func TestInlineImageText_Base64WithFilename(t *testing.T) {
	att := MediaAttachment{Type: "image", Base64: "abc", Filename: "photo.jpg"}
	got := InlineImageText(att)
	if !strings.Contains(got, "photo.jpg") {
		t.Errorf("expected filename in text, got %q", got)
	}
}

// ─── FetchAudioBytes ─────────────────────────────────────────────────────────

func TestFetchAudioBytes_Base64(t *testing.T) {
	raw := []byte("fake audio data")
	b64 := base64.StdEncoding.EncodeToString(raw)
	att := MediaAttachment{Type: "audio", Base64: b64, MimeType: "audio/wav"}
	data, mt, err := FetchAudioBytes(context.Background(), att)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "fake audio data" {
		t.Errorf("data mismatch: %q", string(data))
	}
	if mt != "audio/wav" {
		t.Errorf("mimeType = %q, want audio/wav", mt)
	}
}

func TestFetchAudioBytes_DefaultMimeType(t *testing.T) {
	raw := []byte("audio")
	b64 := base64.StdEncoding.EncodeToString(raw)
	att := MediaAttachment{Type: "audio", Base64: b64}
	_, mt, err := FetchAudioBytes(context.Background(), att)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mt != "audio/mpeg" {
		t.Errorf("default mimeType = %q, want audio/mpeg", mt)
	}
}

func TestFetchAudioBytes_Empty(t *testing.T) {
	_, _, err := FetchAudioBytes(context.Background(), MediaAttachment{Type: "audio"})
	if err == nil {
		t.Error("expected error for empty attachment")
	}
}

// ─── OpenAITranscriber ───────────────────────────────────────────────────────

func TestOpenAITranscriber_Configured_EnvKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	tr := NewOpenAITranscriber()
	if !tr.Configured() {
		t.Error("expected Configured() = true when OPENAI_API_KEY is set")
	}
}

func TestOpenAITranscriber_Configured_ExplicitKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	tr := &OpenAITranscriber{APIKey: "explicit-key"}
	if !tr.Configured() {
		t.Error("expected Configured() = true with explicit APIKey")
	}
}

func TestOpenAITranscriber_Configured_NoKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	tr := NewOpenAITranscriber()
	if tr.Configured() {
		t.Error("expected Configured() = false when no key available")
	}
}

func TestOpenAITranscriber_Transcribe_MockServer(t *testing.T) {
	// Mock Whisper endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/audio/transcriptions") {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-api-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if model := r.FormValue("model"); model != "whisper-1" {
			http.Error(w, "wrong model: "+model, http.StatusBadRequest)
			return
		}
		// Check that a file was uploaded.
		_, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "no file: "+err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"text": "Hello, world!"})
	}))
	defer srv.Close()

	tr := &OpenAITranscriber{
		APIKey:  "test-api-key",
		BaseURL: srv.URL,
	}
	audio := []byte("fake mp3 data")
	text, err := tr.Transcribe(context.Background(), audio, "audio/mpeg")
	if err != nil {
		t.Fatalf("Transcribe error: %v", err)
	}
	if text != "Hello, world!" {
		t.Errorf("transcript = %q, want %q", text, "Hello, world!")
	}
}

func TestOpenAITranscriber_Transcribe_NoKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	tr := NewOpenAITranscriber()
	_, err := tr.Transcribe(context.Background(), []byte("audio"), "audio/mpeg")
	if err == nil {
		t.Error("expected error when no API key configured")
	}
}

func TestOpenAITranscriber_Transcribe_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	tr := &OpenAITranscriber{APIKey: "key", BaseURL: srv.URL}
	_, err := tr.Transcribe(context.Background(), []byte("audio"), "audio/mp3")
	if err == nil {
		t.Error("expected error on server 500")
	}
}

// ─── mimeTypeToAudioExt ──────────────────────────────────────────────────────

func TestMimeTypeToAudioExt(t *testing.T) {
	cases := []struct {
		mime string
		ext  string
	}{
		{"audio/mpeg", ".mp3"},
		{"audio/mp3", ".mp3"},
		{"audio/mp4", ".mp4"},
		{"audio/m4a", ".m4a"},
		{"audio/wav", ".wav"},
		{"audio/webm", ".webm"},
		{"audio/ogg", ".ogg"},
		{"audio/flac", ".flac"},
		{"", ".mp3"},
		{"application/octet-stream", ".mp3"},
	}
	for _, c := range cases {
		if got := mimeTypeToAudioExt(c.mime); got != c.ext {
			t.Errorf("mimeTypeToAudioExt(%q) = %q, want %q", c.mime, got, c.ext)
		}
	}
}

// ─── PDFExtractorAvailable ───────────────────────────────────────────────────

func TestPDFExtractorAvailable(t *testing.T) {
	// We don't require pdftotext to be installed; just ensure the function returns without panic.
	_ = PDFExtractorAvailable()
}

func TestExtractPDFText_NotAvailable(t *testing.T) {
	if PDFExtractorAvailable() {
		t.Skip("pdftotext is installed; skipping not-available test")
	}
	_, err := ExtractPDFText(context.Background(), []byte("%PDF-1.4"))
	if err == nil {
		t.Error("expected error when pdftotext is not installed")
	}
}

// ─── FetchPDFBytes ───────────────────────────────────────────────────────────

func TestFetchPDFBytes_Base64(t *testing.T) {
	raw := []byte("%PDF-1.4 fake")
	b64 := base64.StdEncoding.EncodeToString(raw)
	att := MediaAttachment{Type: "pdf", Base64: b64}
	data, err := FetchPDFBytes(context.Background(), att)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != string(raw) {
		t.Errorf("data mismatch")
	}
}

func TestFetchPDFBytes_Empty(t *testing.T) {
	_, err := FetchPDFBytes(context.Background(), MediaAttachment{Type: "pdf"})
	if err == nil {
		t.Error("expected error for empty pdf attachment")
	}
}

// ─── fetchURL (via FetchPDFBytes with URL) ───────────────────────────────────

func TestFetchURL_MockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "content from server")
	}))
	defer srv.Close()

	att := MediaAttachment{Type: "pdf", URL: srv.URL + "/file.pdf"}
	data, err := FetchPDFBytes(context.Background(), att)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "content from server" {
		t.Errorf("data = %q", string(data))
	}
}

// ─── Registry ─────────────────────────────────────────────────────────────────

func TestListTranscribers(t *testing.T) {
	names := ListTranscribers()
	if len(names) == 0 {
		t.Fatal("expected at least some registered transcribers")
	}
	// Check known ones are present
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	for _, expected := range []string{"openai", "groq", "deepgram", "mistral"} {
		if !found[expected] {
			t.Errorf("missing registered transcriber: %s", expected)
		}
	}
}

func TestResolveTranscriber_Known(t *testing.T) {
	for _, name := range []string{"openai", "groq", "deepgram", "mistral", "google", "moonshot", "minimax"} {
		tr, err := ResolveTranscriber(name)
		if err != nil {
			t.Errorf("ResolveTranscriber(%q): %v", name, err)
			continue
		}
		if tr == nil {
			t.Errorf("ResolveTranscriber(%q) returned nil", name)
		}
	}
}

func TestResolveTranscriber_Unknown(t *testing.T) {
	_, err := ResolveTranscriber("nonexistent")
	if err == nil {
		t.Error("expected error for unknown transcriber")
	}
	if !strings.Contains(err.Error(), "unknown transcriber") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveTranscriber_CaseInsensitive(t *testing.T) {
	tr, err := ResolveTranscriber("OpenAI")
	if err != nil {
		t.Fatal(err)
	}
	if tr == nil {
		t.Error("expected non-nil")
	}
}

func TestDefaultTranscriber_NoEnvKeys(t *testing.T) {
	// Without any API keys set, DefaultTranscriber should return nil
	// We can't unset env vars that might already be set in CI,
	// so just verify it doesn't panic
	_ = DefaultTranscriber()
}

// ─── Transcriber constructors and Configured() ───────────────────────────────

func TestNewDeepgramTranscriber(t *testing.T) {
	tr := NewDeepgramTranscriber()
	if tr == nil {
		t.Fatal("expected non-nil")
	}
	// Without DEEPGRAM_API_KEY, should not be configured
	// (may be configured if env var is set in CI)
	_ = tr.Configured()
}

func TestNewGoogleSTTTranscriber(t *testing.T) {
	tr := NewGoogleSTTTranscriber()
	if tr == nil {
		t.Fatal("expected non-nil")
	}
	_ = tr.Configured()
}

func TestNewGroqTranscriber(t *testing.T) {
	tr := NewGroqTranscriber()
	if tr == nil {
		t.Fatal("expected non-nil")
	}
	_ = tr.Configured()
}

func TestNewMinimaxTranscriber(t *testing.T) {
	tr := NewMinimaxTranscriber()
	if tr == nil {
		t.Fatal("expected non-nil")
	}
	_ = tr.Configured()
}

func TestNewMistralTranscriber(t *testing.T) {
	tr := NewMistralTranscriber()
	if tr == nil {
		t.Fatal("expected non-nil")
	}
	_ = tr.Configured()
}

func TestNewMoonshotTranscriber(t *testing.T) {
	tr := NewMoonshotTranscriber()
	if tr == nil {
		t.Fatal("expected non-nil")
	}
	_ = tr.Configured()
}

// ─── googleAudioEncoding ──────────────────────────────────────────────────────

func TestGoogleAudioEncoding(t *testing.T) {
	tests := []struct {
		mime     string
		wantEnc  string
		wantRate int
	}{
		{"audio/flac", "FLAC", 16000},
		{"audio/wav", "LINEAR16", 16000},
		{"audio/ogg", "OGG_OPUS", 16000},
		{"audio/webm", "WEBM_OPUS", 48000},
		{"audio/mpeg", "MP3", 16000},
		{"audio/mp3", "MP3", 16000},
		{"AUDIO/FLAC", "FLAC", 16000},
	}
	for _, tt := range tests {
		enc, rate := googleAudioEncoding(tt.mime)
		if enc != tt.wantEnc {
			t.Errorf("googleAudioEncoding(%q) enc = %q, want %q", tt.mime, enc, tt.wantEnc)
		}
		if rate != tt.wantRate {
			t.Errorf("googleAudioEncoding(%q) rate = %d, want %d", tt.mime, rate, tt.wantRate)
		}
	}
}

// ─── FetchAudioBytes with URL ─────────────────────────────────────────────────

func TestFetchAudioBytes_URL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte("fake audio data"))
	}))
	defer srv.Close()

	att := MediaAttachment{Type: "audio", URL: srv.URL + "/audio.mp3", MimeType: "audio/mpeg"}
	data, mime, err := FetchAudioBytes(context.Background(), att)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake audio data" {
		t.Errorf("data: %q", string(data))
	}
	if mime != "audio/mpeg" {
		t.Errorf("mime: %q", mime)
	}
}

// ─── JSON round-trip ──────────────────────────────────────────────────────────

func TestMediaAttachment_JSONRoundTrip(t *testing.T) {
	att := MediaAttachment{
		Type:     "image",
		URL:      "https://example.com/img.png",
		MimeType: "image/png",
		Filename: "img.png",
	}
	b, err := json.Marshal(att)
	if err != nil {
		t.Fatal(err)
	}
	var decoded MediaAttachment
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.URL != att.URL || decoded.Type != att.Type {
		t.Errorf("mismatch: %+v", decoded)
	}
}

func TestImageRef_Fields(t *testing.T) {
	ref := ImageRef{URL: "https://example.com/img.png", MimeType: "image/png"}
	if ref.URL == "" || ref.MimeType == "" {
		t.Error("expected non-empty fields")
	}
}
