package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"

	"swarmstr/internal/agent"
	"swarmstr/internal/tts"
)

// MediaPrefix is prepended to the audio file path in the tool result so
// that the channel dispatch layer can detect it and route audio via
// sdk.AudioHandle.SendAudio instead of sending raw text.
const MediaPrefix = "MEDIA:"

// TTSTool returns an agent.ToolFunc for the "tts" tool.
//
// The result JSON includes audio_path (the temp file written by the TTS
// provider) and a leading "MEDIA:<path>" line so channel-aware dispatch
// code can recognise and route the audio appropriately.
//
// Tool parameters:
//   - text (string, required) – text to synthesise
//   - provider (string, optional) – TTS provider ID (e.g. "openai", "elevenlabs")
//   - voice (string, optional) – provider-specific voice name
func TTSTool(mgr *tts.Manager) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		text := agent.ArgString(args, "text")
		if text == "" {
			return "", fmt.Errorf("tts: text is required")
		}
		providerID := agent.ArgString(args, "provider")
		voice := agent.ArgString(args, "voice")

		// Pick the first configured provider when none is specified.
		if providerID == "" {
			providerID = mgr.DefaultConfiguredProvider()
		}
		if providerID == "" {
			return "", fmt.Errorf("tts: no provider configured (set a TTS API key, e.g. OPENAI_API_KEY)")
		}

		result, err := mgr.Convert(ctx, providerID, text, voice)
		if err != nil {
			return "", fmt.Errorf("tts: %w", err)
		}

		// The leading MEDIA: line lets channel dispatch code detect and route
		// the audio to an AudioHandle instead of echoing a file path as text.
		out, _ := json.Marshal(map[string]any{
			"audio_path": result.AudioPath,
			"provider":   result.Provider,
			"voice":      result.Voice,
			"format":     result.Format,
		})
		return MediaPrefix + result.AudioPath + "\n" + string(out), nil
	}
}
