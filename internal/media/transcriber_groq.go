package media

import (
	"context"
	"os"
	"strings"
)

// GroqTranscriber transcribes audio using Groq's OpenAI-compatible audio API.
// Model: whisper-large-v3-turbo (fast, high-quality, free-tier available).
//
// Uses the same multipart/form-data format as OpenAI Whisper; the base URL
// is overridden to https://api.groq.com/openai.
//
// Environment variable: GROQ_API_KEY
type GroqTranscriber struct {
	APIKey string // overrides GROQ_API_KEY env var
	Model  string // defaults to "whisper-large-v3-turbo"
}

// NewGroqTranscriber creates a transcriber that reads GROQ_API_KEY from the environment.
func NewGroqTranscriber() *GroqTranscriber {
	return &GroqTranscriber{}
}

// Configured reports whether a Groq API key is available.
func (g *GroqTranscriber) Configured() bool {
	if strings.TrimSpace(g.APIKey) != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv("GROQ_API_KEY")) != ""
}

// Transcribe sends audio to Groq's Whisper API and returns the transcript.
func (g *GroqTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	apiKey := strings.TrimSpace(g.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("GROQ_API_KEY"))
	}
	model := strings.TrimSpace(g.Model)
	if model == "" {
		model = "whisper-large-v3-turbo"
	}
	inner := &OpenAITranscriber{
		APIKey:  apiKey,
		BaseURL: "https://api.groq.com/openai",
		Model:   model,
	}
	return inner.Transcribe(ctx, audio, mimeType)
}
