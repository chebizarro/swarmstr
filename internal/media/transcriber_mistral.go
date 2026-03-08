package media

import (
	"context"
	"os"
	"strings"
)

// MistralTranscriber transcribes audio using Mistral's OpenAI-compatible audio API.
// Uses the same multipart/form-data format as OpenAI Whisper with Mistral's base URL.
//
// Environment variable: MISTRAL_API_KEY
//
// Note: Mistral audio transcription requires access to mistral-audio or compatible model.
// Check https://docs.mistral.ai for current model availability.
type MistralTranscriber struct {
	APIKey string // overrides MISTRAL_API_KEY env var
	Model  string // defaults to "mistral-audio-latest"
}

// NewMistralTranscriber creates a transcriber that reads MISTRAL_API_KEY from the environment.
func NewMistralTranscriber() *MistralTranscriber {
	return &MistralTranscriber{}
}

// Configured reports whether a Mistral API key is available.
func (m *MistralTranscriber) Configured() bool {
	if strings.TrimSpace(m.APIKey) != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv("MISTRAL_API_KEY")) != ""
}

// Transcribe sends audio to Mistral's audio API and returns the transcript.
func (m *MistralTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	apiKey := strings.TrimSpace(m.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("MISTRAL_API_KEY"))
	}
	model := strings.TrimSpace(m.Model)
	if model == "" {
		model = "mistral-audio-latest"
	}
	inner := &OpenAITranscriber{
		APIKey:  apiKey,
		BaseURL: "https://api.mistral.ai",
		Model:   model,
	}
	return inner.Transcribe(ctx, audio, mimeType)
}
