package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// OpenAIProvider implements Provider using the OpenAI TTS API
// (POST https://api.openai.com/v1/audio/speech).
// It reads OPENAI_API_KEY from the environment at call time.
type OpenAIProvider struct {
	// baseURL overrides the default API endpoint (used in tests).
	baseURL string
}

// NewOpenAIProviderWithBaseURL creates an OpenAI provider that calls baseURL
// instead of https://api.openai.com (useful for tests).
func NewOpenAIProviderWithBaseURL(baseURL string) *OpenAIProvider {
	return &OpenAIProvider{baseURL: strings.TrimRight(baseURL, "/")}
}

func (p *OpenAIProvider) ID() string   { return "openai" }
func (p *OpenAIProvider) Name() string { return "OpenAI TTS" }

func (p *OpenAIProvider) Voices() []string {
	return []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}
}

func (p *OpenAIProvider) Configured() bool {
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
}

func (p *OpenAIProvider) Convert(ctx context.Context, text, voice string) ([]byte, string, error) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil, "", fmt.Errorf("OPENAI_API_KEY is not set")
	}
	if voice == "" {
		voice = "alloy"
	}

	payload := map[string]any{
		"model": "tts-1",
		"input": text,
		"voice": voice,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("marshal request: %w", err)
	}

	endpoint := "https://api.openai.com/v1/audio/speech"
	if p.baseURL != "" {
		endpoint = p.baseURL + "/v1/audio/speech"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("call OpenAI TTS API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(respBody))
		// Try to extract the error message from the JSON if it looks like JSON.
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if jsonErr := json.Unmarshal(respBody, &errResp); jsonErr == nil && errResp.Error.Message != "" {
			msg = errResp.Error.Message
		}
		return nil, "", fmt.Errorf("OpenAI TTS API error %d: %s", resp.StatusCode, msg)
	}

	return respBody, "mp3", nil
}
