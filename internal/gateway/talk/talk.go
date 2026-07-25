// Package talk implements the gateway voice/talk long-tail surface: the talk.*
// discovery + synthesis methods (talk.catalog, talk.speak), the tts persona
// controls (tts.personas, tts.setPersona), the voicewake.routing.* config, the
// talk.session.* turn lifecycle over the gateway-relay transport, and the
// talk.client.* client-owned voice sessions.
//
// Capability honesty (swarmstr-0tfj): metiq's internal/tts manager is live, so
// talk.speak, tts.personas/setPersona, talk.catalog's speech section, and the
// voicewake routing config are implemented for real. The realtimevoice and
// realtimestt provider registries are plugin scaffolding that the daemon does
// not currently populate, and there is no managed-room / LiveKit transport, so
// the session/client audio-transport paths resolve a provider from those
// registries and return a clear ErrUnavailable when none is registered rather
// than shipping a fake stub. Managed-room-only operations (join, managed-room
// startTurn/endTurn) are accepted deviations that return ErrUnsupported.
package talk

import (
	"errors"
	"fmt"
	"strings"
)

// Structured error reasons surfaced by talk.speak and friends. They mirror the
// OpenClaw talk error taxonomy so clients can branch on a stable machine code.
const (
	ReasonTalkUnconfigured   = "talk_unconfigured"
	ReasonSynthesisFailed    = "synthesis_failed"
	ReasonInvalidAudioResult = "invalid_audio_result"
	ReasonUnavailable        = "unavailable"
	ReasonUnsupported        = "unsupported"
)

// ErrUnavailable marks a method whose runtime capability is genuinely absent in
// this build (no realtime audio provider registered / no managed-room infra).
// Handlers surface it as a clear error, matching the attach.* UNAVAILABLE
// precedent, instead of returning fabricated success data.
var ErrUnavailable = errors.New("unavailable")

// ErrUnsupported marks an operation that only exists for a transport metiq does
// not implement (managed-room / LiveKit). It is an accepted parity deviation.
var ErrUnsupported = errors.New("unsupported")

// ReasonedError couples a machine-readable reason code with a human message.
type ReasonedError struct {
	Reason  string
	Message string
}

func (e *ReasonedError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return e.Reason
	}
	return fmt.Sprintf("%s: %s", e.Reason, e.Message)
}

func newReasonedError(reason, format string, args ...any) *ReasonedError {
	return &ReasonedError{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// talkConfig extracts the cfg.Extra["talk"] map, or an empty map when absent.
func talkConfig(extra map[string]any) map[string]any {
	if extra == nil {
		return map[string]any{}
	}
	if raw, ok := extra["talk"].(map[string]any); ok && raw != nil {
		return raw
	}
	return map[string]any{}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
