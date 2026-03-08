package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// GoogleTTSProvider implements Provider using the Google Cloud TTS REST API.
// Docs: https://cloud.google.com/text-to-speech/docs/reference/rest/v1/text/synthesize
// Reads GOOGLE_API_KEY from the environment at call time.
type GoogleTTSProvider struct{}

func (p *GoogleTTSProvider) ID() string   { return "google" }
func (p *GoogleTTSProvider) Name() string { return "Google Cloud TTS" }

func (p *GoogleTTSProvider) Voices() []string {
	return []string{
		"en-US-Neural2-A", "en-US-Neural2-C", "en-US-Neural2-D",
		"en-US-Neural2-E", "en-US-Neural2-F", "en-US-Neural2-H",
		"en-GB-Neural2-A", "en-GB-Neural2-B",
	}
}

func (p *GoogleTTSProvider) Configured() bool {
	return strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != ""
}

func (p *GoogleTTSProvider) Convert(ctx context.Context, text, voice string) ([]byte, string, error) {
	apiKey := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
	if apiKey == "" {
		return nil, "", fmt.Errorf("GOOGLE_API_KEY is not set")
	}
	if voice == "" {
		voice = "en-US-Neural2-A"
	}

	// Derive language code from voice name (first two dash-separated segments).
	langCode := "en-US"
	parts := strings.SplitN(voice, "-", 3)
	if len(parts) >= 2 {
		langCode = parts[0] + "-" + parts[1]
	}

	payload := map[string]any{
		"input": map[string]any{
			"text": text,
		},
		"voice": map[string]any{
			"languageCode": langCode,
			"name":         voice,
		},
		"audioConfig": map[string]any{
			"audioEncoding": "MP3",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("https://texttospeech.googleapis.com/v1/text:synthesize?key=%s", apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("call Google TTS API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("Google TTS API error %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result struct {
		AudioContent string `json:"audioContent"` // base64-encoded MP3
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, "", fmt.Errorf("parse Google TTS response: %w", err)
	}
	if result.AudioContent == "" {
		return nil, "", fmt.Errorf("Google TTS returned empty audioContent")
	}

	audio, err := base64.StdEncoding.DecodeString(result.AudioContent)
	if err != nil {
		return nil, "", fmt.Errorf("decode Google TTS audio: %w", err)
	}
	return audio, "mp3", nil
}
