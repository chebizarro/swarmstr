package media

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
	"time"
)

// GoogleSTTTranscriber transcribes audio using the Google Cloud Speech-to-Text REST API v1.
//
// Authentication: reads the GOOGLE_API_KEY environment variable for simple key-based auth.
// For service-account auth, set GOOGLE_APPLICATION_CREDENTIALS to a JSON key file path
// (not supported in this implementation — use GOOGLE_API_KEY for simplicity).
//
// Environment variable: GOOGLE_API_KEY
//
// Docs: https://cloud.google.com/speech-to-text/docs/reference/rest/v1/speech/recognize
type GoogleSTTTranscriber struct {
	APIKey   string // overrides GOOGLE_API_KEY env var
	Language string // BCP-47 language code, e.g. "en-US" (default)
	Model    string // recognition model: "latest_long", "latest_short", "default" (default)
}

// NewGoogleSTTTranscriber creates a transcriber that reads GOOGLE_API_KEY from the environment.
func NewGoogleSTTTranscriber() *GoogleSTTTranscriber {
	return &GoogleSTTTranscriber{}
}

// Configured reports whether a Google API key is available.
func (g *GoogleSTTTranscriber) Configured() bool {
	if strings.TrimSpace(g.APIKey) != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != ""
}

// Transcribe sends audio to the Google Cloud Speech-to-Text API and returns the transcript.
func (g *GoogleSTTTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	apiKey := strings.TrimSpace(g.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
	}
	if apiKey == "" {
		return "", fmt.Errorf("Google STT API key not configured (set GOOGLE_API_KEY)")
	}

	lang := strings.TrimSpace(g.Language)
	if lang == "" {
		lang = "en-US"
	}
	model := strings.TrimSpace(g.Model)
	if model == "" {
		model = "default"
	}

	// Determine encoding from MIME type.
	encoding, sampleRate := googleAudioEncoding(mimeType)

	payload := map[string]any{
		"config": map[string]any{
			"encoding":                   encoding,
			"sampleRateHertz":            sampleRate,
			"languageCode":               lang,
			"model":                      model,
			"enableAutomaticPunctuation": true,
		},
		"audio": map[string]string{
			"content": base64.StdEncoding.EncodeToString(audio),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("google stt payload: %w", err)
	}

	url := "https://speech.googleapis.com/v1/speech:recognize?key=" + apiKey
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("google stt request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("google stt call: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("google stt returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	// Google STT response:
	// { "results": [{ "alternatives": [{ "transcript": "..." }] }] }
	var out struct {
		Results []struct {
			Alternatives []struct {
				Transcript string  `json:"transcript"`
				Confidence float64 `json:"confidence"`
			} `json:"alternatives"`
		} `json:"results"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("google stt decode: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("google stt error %d: %s", out.Error.Code, out.Error.Message)
	}
	var transcripts []string
	for _, r := range out.Results {
		if len(r.Alternatives) > 0 {
			transcripts = append(transcripts, r.Alternatives[0].Transcript)
		}
	}
	return strings.TrimSpace(strings.Join(transcripts, " ")), nil
}

// googleAudioEncoding maps a MIME type to a Google STT encoding string and sample rate.
func googleAudioEncoding(mimeType string) (string, int) {
	mt := strings.ToLower(mimeType)
	switch {
	case strings.Contains(mt, "flac"):
		return "FLAC", 16000
	case strings.Contains(mt, "wav"):
		return "LINEAR16", 16000
	case strings.Contains(mt, "ogg"):
		return "OGG_OPUS", 16000
	case strings.Contains(mt, "webm"):
		return "WEBM_OPUS", 48000
	default: // mp3, mpeg, etc.
		return "MP3", 16000
	}
}

func init() {
	RegisterTranscriber("google", func() Transcriber {
		return NewGoogleSTTTranscriber()
	})
}
