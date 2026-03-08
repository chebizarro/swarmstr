package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DeepgramTranscriber transcribes audio using the Deepgram Speech-to-Text REST API.
// POST https://api.deepgram.com/v1/listen with raw audio bytes in the body.
//
// Environment variable: DEEPGRAM_API_KEY
//
// Docs: https://developers.deepgram.com/reference/pre-recorded
type DeepgramTranscriber struct {
	APIKey string // overrides DEEPGRAM_API_KEY env var
	Model  string // defaults to "nova-3"
	Lang   string // defaults to "en"
}

// NewDeepgramTranscriber creates a transcriber that reads DEEPGRAM_API_KEY from the environment.
func NewDeepgramTranscriber() *DeepgramTranscriber {
	return &DeepgramTranscriber{}
}

// Configured reports whether a Deepgram API key is available.
func (d *DeepgramTranscriber) Configured() bool {
	if strings.TrimSpace(d.APIKey) != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv("DEEPGRAM_API_KEY")) != ""
}

// Transcribe sends audio to the Deepgram API and returns the transcript.
func (d *DeepgramTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	apiKey := strings.TrimSpace(d.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("DEEPGRAM_API_KEY"))
	}
	if apiKey == "" {
		return "", fmt.Errorf("Deepgram API key not configured (set DEEPGRAM_API_KEY)")
	}

	model := strings.TrimSpace(d.Model)
	if model == "" {
		model = "nova-3"
	}
	lang := strings.TrimSpace(d.Lang)
	if lang == "" {
		lang = "en"
	}
	if mimeType == "" {
		mimeType = "audio/mpeg"
	}

	url := fmt.Sprintf("https://api.deepgram.com/v1/listen?model=%s&language=%s&smart_format=true&punctuate=true", model, lang)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(audio))
	if err != nil {
		return "", fmt.Errorf("deepgram request build: %w", err)
	}
	req.Header.Set("Authorization", "Token "+apiKey)
	req.Header.Set("Content-Type", mimeType)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("deepgram call: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("deepgram returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	// Deepgram response structure:
	// { "results": { "channels": [{ "alternatives": [{ "transcript": "..." }] }] } }
	var out struct {
		Results *struct {
			Channels []struct {
				Alternatives []struct {
					Transcript string `json:"transcript"`
				} `json:"alternatives"`
			} `json:"channels"`
		} `json:"results"`
		Error *struct {
			Message string `json:"message"`
		} `json:"err_msg,omitempty"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("deepgram decode: %w", err)
	}
	if out.Results != nil && len(out.Results.Channels) > 0 {
		alts := out.Results.Channels[0].Alternatives
		if len(alts) > 0 {
			return strings.TrimSpace(alts[0].Transcript), nil
		}
	}
	return "", fmt.Errorf("deepgram: no transcript in response")
}
