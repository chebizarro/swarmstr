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

// MinimaxTranscriber transcribes audio using the Minimax Speech-to-Text API.
// Minimax (海螺AI) is a Chinese AI company with a multilingual STT service
// optimised for Mandarin Chinese and other CJK languages.
//
// Environment variable: MINIMAX_API_KEY
//
// Docs: https://www.minimax.chat/document/T2A%20V2
// Note: The Minimax API uses a multipart form upload similar to OpenAI Whisper.
type MinimaxTranscriber struct {
	APIKey string // overrides MINIMAX_API_KEY env var
	Model  string // defaults to "speech-01-turbo"
	Lang   string // language hint, e.g. "zh" for Mandarin (optional)
}

// NewMinimaxTranscriber creates a transcriber that reads MINIMAX_API_KEY from the environment.
func NewMinimaxTranscriber() *MinimaxTranscriber {
	return &MinimaxTranscriber{}
}

// Configured reports whether a Minimax API key is available.
func (m *MinimaxTranscriber) Configured() bool {
	if strings.TrimSpace(m.APIKey) != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv("MINIMAX_API_KEY")) != ""
}

// Transcribe sends audio to the Minimax API and returns the transcript.
func (m *MinimaxTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	apiKey := strings.TrimSpace(m.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("MINIMAX_API_KEY"))
	}
	if apiKey == "" {
		return "", fmt.Errorf("Minimax API key not configured (set MINIMAX_API_KEY)")
	}

	model := strings.TrimSpace(m.Model)
	if model == "" {
		model = "speech-01-turbo"
	}

	// Minimax STT uses an OpenAI-compatible multipart form upload endpoint.
	const apiURL = "https://api.minimax.chat/v1/audio/transcriptions"

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// File field.
	filename := "audio" + mimeTypeToAudioExt(mimeType)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("minimax form file: %w", err)
	}
	if _, err := fw.Write(audio); err != nil {
		return "", fmt.Errorf("minimax form write: %w", err)
	}

	// Model field.
	if err := mw.WriteField("model", model); err != nil {
		return "", fmt.Errorf("minimax form model: %w", err)
	}

	// Optional language hint.
	if lang := strings.TrimSpace(m.Lang); lang != "" {
		_ = mw.WriteField("language", lang)
	}

	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("minimax form close: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, &buf)
	if err != nil {
		return "", fmt.Errorf("minimax request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("minimax call: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("minimax returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	// OpenAI-compatible response: { "text": "..." }
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("minimax decode: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("minimax error: %s", out.Error)
	}
	return strings.TrimSpace(out.Text), nil
}

func init() {
	RegisterTranscriber("minimax", func() Transcriber {
		return NewMinimaxTranscriber()
	})
}
