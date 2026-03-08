package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

// OpenAITranscriber transcribes audio using the OpenAI Whisper API.
// POST /v1/audio/transcriptions with multipart/form-data.
type OpenAITranscriber struct {
	APIKey  string       // overrides OPENAI_API_KEY env var
	BaseURL string       // defaults to https://api.openai.com
	Model   string       // defaults to "whisper-1"
	Client  *http.Client // defaults to 60s timeout client
}

// NewOpenAITranscriber creates a transcriber that reads OPENAI_API_KEY from the environment.
func NewOpenAITranscriber() *OpenAITranscriber { return &OpenAITranscriber{} }

// NewOpenAITranscriberWithBaseURL creates a transcriber with a custom base URL (useful for tests).
func NewOpenAITranscriberWithBaseURL(baseURL string) *OpenAITranscriber {
	return &OpenAITranscriber{BaseURL: baseURL}
}

// Configured reports whether an OpenAI API key is available.
func (t *OpenAITranscriber) Configured() bool {
	if strings.TrimSpace(t.APIKey) != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
}

// Transcribe sends audio bytes to the Whisper API and returns the transcript.
// mimeType is used to derive the file extension for the multipart upload.
func (t *OpenAITranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	apiKey := strings.TrimSpace(t.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if apiKey == "" {
		return "", fmt.Errorf("OpenAI API key not configured (set OPENAI_API_KEY)")
	}
	model := strings.TrimSpace(t.Model)
	if model == "" {
		model = "whisper-1"
	}
	baseURL := strings.TrimRight(strings.TrimSpace(t.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	ext := mimeTypeToAudioExt(mimeType)
	filename := "audio" + ext

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("model", model); err != nil {
		return "", fmt.Errorf("whisper form model: %w", err)
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("whisper form file: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(audio)); err != nil {
		return "", fmt.Errorf("whisper form write: %w", err)
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/audio/transcriptions", &buf)
	if err != nil {
		return "", fmt.Errorf("whisper request build: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper call: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("whisper returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var out struct {
		Text  string `json:"text"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("whisper decode: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("whisper error: %s", out.Error.Message)
	}
	return strings.TrimSpace(out.Text), nil
}

// mimeTypeToAudioExt maps a MIME type to a file extension for the multipart upload.
func mimeTypeToAudioExt(mimeType string) string {
	m := strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.Contains(m, "mp3") || strings.Contains(m, "mpeg"):
		return ".mp3"
	case strings.Contains(m, "mp4"):
		return ".mp4"
	case strings.Contains(m, "m4a"):
		return ".m4a"
	case strings.Contains(m, "wav"):
		return ".wav"
	case strings.Contains(m, "webm"):
		return ".webm"
	case strings.Contains(m, "ogg"):
		return ".ogg"
	case strings.Contains(m, "flac"):
		return ".flac"
	default:
		return ".mp3"
	}
}
