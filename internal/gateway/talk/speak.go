package talk

import (
	"context"
	"strings"

	"metiq/internal/tts"
)

// SpeakRequest is the resolved input to Speak.
type SpeakRequest struct {
	Text     string
	Provider string
	Voice    string
	Persona  string
	Format   string
}

// SpeakResult is the synthesized-audio payload returned by talk.speak.
type SpeakResult struct {
	AudioBase64  string `json:"audioBase64,omitempty"`
	AudioPath    string `json:"audioPath,omitempty"`
	Provider     string `json:"provider"`
	Voice        string `json:"voice"`
	OutputFormat string `json:"outputFormat"`
	MimeType     string `json:"mimeType"`
	Persona      string `json:"persona,omitempty"`
}

// Speak synthesizes req.Text through the live tts manager, honoring talk-config
// provider/voice-alias overrides and the requested persona. It returns a
// *ReasonedError (talk_unconfigured / synthesis_failed / invalid_audio_result)
// on failure so callers can branch on a stable machine code.
func Speak(ctx context.Context, mgr *tts.Manager, extra map[string]any, req SpeakRequest) (SpeakResult, error) {
	if strings.TrimSpace(req.Text) == "" {
		return SpeakResult{}, newReasonedError(ReasonInvalidAudioResult, "text is required")
	}
	if mgr == nil {
		return SpeakResult{}, newReasonedError(ReasonTalkUnconfigured, "tts manager is not initialised")
	}

	talk := talkConfig(extra)

	// Persona supplies default provider/voice unless the call overrides them.
	var persona Persona
	if req.Persona != "" && !IsClearPersona(req.Persona) {
		p, ok := PersonaByID(extra, req.Persona)
		if !ok {
			return SpeakResult{}, newReasonedError(ReasonTalkUnconfigured, "unknown persona %q", req.Persona)
		}
		persona = p
	}

	provider := firstNonBlank(req.Provider, persona.Provider, asString(talk["tts_provider"]))
	if provider == "" {
		provider = mgr.DefaultConfiguredProvider()
	}
	if provider == "" {
		return SpeakResult{}, newReasonedError(ReasonTalkUnconfigured, "no tts provider is configured")
	}

	p := mgr.Get(provider)
	if p == nil {
		return SpeakResult{}, newReasonedError(ReasonTalkUnconfigured, "unknown tts provider %q", provider)
	}
	if !p.Configured() {
		return SpeakResult{}, newReasonedError(ReasonTalkUnconfigured, "tts provider %q is not configured", provider)
	}

	voice := resolveVoiceAlias(talk, firstNonBlank(req.Voice, persona.Voice, asString(talk["voice_model"])))

	result, err := mgr.Convert(ctx, provider, req.Text, voice)
	if err != nil {
		return SpeakResult{}, newReasonedError(ReasonSynthesisFailed, "%s", err.Error())
	}
	// talk.speak synthesizes short utterances and must return inline audio; an
	// empty base64 payload signals a zero-length / invalid synthesis result.
	if result == nil || strings.TrimSpace(result.AudioBase64) == "" {
		return SpeakResult{}, newReasonedError(ReasonInvalidAudioResult, "synthesis returned no audio")
	}

	format := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(result.Format)), ".")
	if format == "" {
		format = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(req.Format)), ".")
	}

	return SpeakResult{
		AudioBase64:  result.AudioBase64,
		AudioPath:    result.AudioPath,
		Provider:     result.Provider,
		Voice:        result.Voice,
		OutputFormat: format,
		MimeType:     mimeTypeForFormat(format),
		Persona:      persona.ID,
	}, nil
}

// resolveVoiceAlias maps a voice through the talk config voice_aliases table
// ({alias: realVoice}) when present, else returns the input unchanged.
func resolveVoiceAlias(talk map[string]any, voice string) string {
	voice = strings.TrimSpace(voice)
	if voice == "" {
		return ""
	}
	aliases, ok := talk["voice_aliases"].(map[string]any)
	if !ok {
		return voice
	}
	for alias, mapped := range aliases {
		if strings.EqualFold(alias, voice) {
			if s := asString(mapped); s != "" {
				return s
			}
		}
	}
	return voice
}

func mimeTypeForFormat(format string) string {
	switch strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".") {
	case "mp3", "mpeg":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "ogg", "opus":
		return "audio/ogg"
	case "flac":
		return "audio/flac"
	case "aac", "m4a":
		return "audio/aac"
	case "pcm", "l16":
		return "audio/L16"
	case "":
		return ""
	default:
		return "audio/" + format
	}
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
