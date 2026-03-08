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

// ElevenLabsProvider implements Provider using the ElevenLabs TTS API.
// Docs: https://api.elevenlabs.io/docs
// Reads ELEVENLABS_API_KEY from the environment at call time.
//
// Voice IDs are ElevenLabs-specific UUIDs.  The Voices() method returns common
// default voice names; use the ElevenLabs dashboard to obtain custom voice IDs.
type ElevenLabsProvider struct{}

// Well-known ElevenLabs default voice IDs.
var elevenLabsVoices = []struct {
	ID   string
	Name string
}{
	{"21m00Tcm4TlvDq8ikWAM", "Rachel"},
	{"AZnzlk1XvdvUeBnXmlld", "Domi"},
	{"EXAVITQu4vr4xnSDxMaL", "Bella"},
	{"ErXwobaYiN019PkySvjV", "Antoni"},
	{"MF3mGyEYCl7XYWbV9V6O", "Elli"},
	{"TxGEqnHWrfWFTfGW9XjX", "Josh"},
	{"VR6AewLTigWG4xSOukaG", "Arnold"},
	{"pNInz6obpgDQGcFmaJgB", "Adam"},
	{"yoZ06aMxZJJ28mfd3POQ", "Sam"},
}

func (p *ElevenLabsProvider) ID() string   { return "elevenlabs" }
func (p *ElevenLabsProvider) Name() string { return "ElevenLabs TTS" }

func (p *ElevenLabsProvider) Voices() []string {
	names := make([]string, len(elevenLabsVoices))
	for i, v := range elevenLabsVoices {
		names[i] = v.Name
	}
	return names
}

func (p *ElevenLabsProvider) Configured() bool {
	return strings.TrimSpace(os.Getenv("ELEVENLABS_API_KEY")) != ""
}

// resolveVoiceID maps a voice name (case-insensitive) to its ElevenLabs voice
// ID.  If no match is found, the input is returned as-is (allowing callers to
// pass voice IDs directly).
func (p *ElevenLabsProvider) resolveVoiceID(voice string) string {
	lower := strings.ToLower(strings.TrimSpace(voice))
	for _, v := range elevenLabsVoices {
		if strings.ToLower(v.Name) == lower {
			return v.ID
		}
		if v.ID == voice {
			return v.ID
		}
	}
	return voice // assume it's already a voice ID
}

func (p *ElevenLabsProvider) Convert(ctx context.Context, text, voice string) ([]byte, string, error) {
	apiKey := strings.TrimSpace(os.Getenv("ELEVENLABS_API_KEY"))
	if apiKey == "" {
		return nil, "", fmt.Errorf("ELEVENLABS_API_KEY is not set")
	}
	if voice == "" {
		voice = "Rachel"
	}
	voiceID := p.resolveVoiceID(voice)

	payload := map[string]any{
		"text":     text,
		"model_id": "eleven_monolingual_v1",
		"voice_settings": map[string]any{
			"stability":        0.5,
			"similarity_boost": 0.75,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s", voiceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("xi-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("call ElevenLabs TTS API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("ElevenLabs TTS API error %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return respBody, "mp3", nil
}
