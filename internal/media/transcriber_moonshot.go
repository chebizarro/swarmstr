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

// MoonshotTranscriber transcribes audio using the Moonshot (Kimi) audio API.
// Moonshot AI (月之暗面) provides an OpenAI-compatible transcription endpoint
// at api.moonshot.cn, supporting multiple languages with Mandarin optimisation.
//
// Environment variable: MOONSHOT_API_KEY
//
// Docs: https://platform.moonshot.cn/docs/api/audio
type MoonshotTranscriber struct {
	APIKey string // overrides MOONSHOT_API_KEY env var
	Model  string // defaults to "moonshot-v1-8k-transcription"
	Lang   string // ISO-639-1 language code hint (optional; auto-detected if empty)
}

// NewMoonshotTranscriber creates a transcriber that reads MOONSHOT_API_KEY from the environment.
func NewMoonshotTranscriber() *MoonshotTranscriber {
	return &MoonshotTranscriber{}
}

// Configured reports whether a Moonshot API key is available.
func (m *MoonshotTranscriber) Configured() bool {
	if strings.TrimSpace(m.APIKey) != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv("MOONSHOT_API_KEY")) != ""
}

// Transcribe sends audio to the Moonshot Kimi API and returns the transcript.
func (m *MoonshotTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	apiKey := strings.TrimSpace(m.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("MOONSHOT_API_KEY"))
	}
	if apiKey == "" {
		return "", fmt.Errorf("Moonshot API key not configured (set MOONSHOT_API_KEY)")
	}

	model := strings.TrimSpace(m.Model)
	if model == "" {
		model = "moonshot-v1-8k-transcription"
	}

	// OpenAI-compatible multipart endpoint.
	const apiURL = "https://api.moonshot.cn/v1/audio/transcriptions"

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	filename := "audio" + mimeTypeToAudioExt(mimeType)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("moonshot form file: %w", err)
	}
	if _, err := fw.Write(audio); err != nil {
		return "", fmt.Errorf("moonshot form write: %w", err)
	}
	if err := mw.WriteField("model", model); err != nil {
		return "", fmt.Errorf("moonshot form model: %w", err)
	}
	if lang := strings.TrimSpace(m.Lang); lang != "" {
		_ = mw.WriteField("language", lang)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("moonshot form close: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, &buf)
	if err != nil {
		return "", fmt.Errorf("moonshot request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("moonshot call: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("moonshot returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	// OpenAI-compatible response.
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("moonshot decode: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("moonshot error: %s", out.Error)
	}
	return strings.TrimSpace(out.Text), nil
}

func init() {
	RegisterTranscriber("moonshot", func() Transcriber {
		return NewMoonshotTranscriber()
	})
}
