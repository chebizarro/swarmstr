package methods

// Voice/talk long-tail param schemas (swarmstr-0tfj). Covers the tts persona
// controls, talk.catalog/talk.speak, voicewake.routing.*, the talk.session.*
// turn lifecycle, and the talk.client.* client-owned sessions. Param shapes
// mirror OpenClaw's gateway talk*/tts/voicewake-routing method contracts.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxTalkAudioChunkBase64Length bounds one appended audio chunk (~4 MiB raw).
const maxTalkAudioChunkBase64Length = (4*1024*1024 + 2) / 3 * 4

// ── Phase A: personas / catalog / speak / voicewake routing ─────────────────

// TTSPersonasRequest lists configured tts personas (no params).
type TTSPersonasRequest struct{}

func (r TTSPersonasRequest) Normalize() (TTSPersonasRequest, error) { return r, nil }

// TTSSetPersonaRequest selects the active tts persona. off/none/default clear it.
type TTSSetPersonaRequest struct {
	Persona   string `json:"persona,omitempty"`
	PersonaID string `json:"personaId,omitempty"`
	ID        string `json:"id,omitempty"`
}

func (r TTSSetPersonaRequest) Normalize() (TTSSetPersonaRequest, error) {
	r.Persona = strings.TrimSpace(firstNonEmptyStr(r.Persona, r.PersonaID, r.ID))
	r.PersonaID = ""
	r.ID = ""
	return r, nil
}

// TalkCatalogRequest reads the talk capability catalog (no params).
type TalkCatalogRequest struct{}

func (r TalkCatalogRequest) Normalize() (TalkCatalogRequest, error) { return r, nil }

// TalkSpeakRequest synthesizes speech through the live tts manager.
type TalkSpeakRequest struct {
	Text     string `json:"text"`
	Provider string `json:"provider,omitempty"`
	Voice    string `json:"voice,omitempty"`
	Persona  string `json:"persona,omitempty"`
	Format   string `json:"format,omitempty"`
}

func (r TalkSpeakRequest) Normalize() (TalkSpeakRequest, error) {
	r.Text = strings.TrimSpace(r.Text)
	if r.Text == "" {
		return r, fmt.Errorf("invalid talk.speak params: text is required")
	}
	r.Provider = strings.TrimSpace(r.Provider)
	r.Voice = strings.TrimSpace(r.Voice)
	r.Persona = strings.TrimSpace(r.Persona)
	r.Format = strings.TrimSpace(r.Format)
	return r, nil
}

// VoicewakeRoutingGetRequest reads the voicewake routing table (no params).
type VoicewakeRoutingGetRequest struct{}

func (r VoicewakeRoutingGetRequest) Normalize() (VoicewakeRoutingGetRequest, error) { return r, nil }

// VoicewakeRouteParam is one wake-trigger routing entry.
type VoicewakeRouteParam struct {
	Trigger string `json:"trigger"`
	Target  string `json:"target"`
	Mode    string `json:"mode,omitempty"`
}

// VoicewakeRoutingSetRequest replaces the voicewake routing table.
type VoicewakeRoutingSetRequest struct {
	Version       int                   `json:"version,omitempty"`
	DefaultTarget string                `json:"defaultTarget,omitempty"`
	Routes        []VoicewakeRouteParam `json:"routes,omitempty"`
}

func (r VoicewakeRoutingSetRequest) Normalize() (VoicewakeRoutingSetRequest, error) {
	if r.Version < 0 {
		return r, fmt.Errorf("invalid voicewake.routing.set params: version must be non-negative")
	}
	r.DefaultTarget = strings.TrimSpace(r.DefaultTarget)
	if r.Routes == nil {
		r.Routes = []VoicewakeRouteParam{}
	}
	return r, nil
}

// ── Phase B: talk.session.* turn lifecycle ──────────────────────────────────

// TalkSessionCreateRequest opens (or resumes) a server-owned voice session.
type TalkSessionCreateRequest struct {
	SessionID    string `json:"session_id,omitempty"`
	Mode         string `json:"mode,omitempty"`
	Transport    string `json:"transport,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Voice        string `json:"voice,omitempty"`
	Language     string `json:"language,omitempty"`
	SystemPrompt string `json:"systemPrompt,omitempty"`
}

func (r TalkSessionCreateRequest) Normalize() (TalkSessionCreateRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Mode = strings.ToLower(strings.TrimSpace(r.Mode))
	if r.Mode == "" {
		r.Mode = "realtime"
	}
	r.Transport = strings.ToLower(strings.TrimSpace(r.Transport))
	if r.Transport == "" {
		r.Transport = "gateway-relay"
	}
	r.Provider = strings.TrimSpace(r.Provider)
	r.Voice = strings.TrimSpace(r.Voice)
	r.Language = strings.TrimSpace(r.Language)
	r.SystemPrompt = strings.TrimSpace(r.SystemPrompt)
	return r, nil
}

// TalkSessionRef is the shared shape for session-scoped calls.
type TalkSessionRef struct {
	SessionID string `json:"session_id"`
}

func (r TalkSessionRef) normalizeRef(method string) (TalkSessionRef, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid %s params: session_id is required", method)
	}
	return r, nil
}

type TalkSessionJoinRequest struct{ TalkSessionRef }

func (r TalkSessionJoinRequest) Normalize() (TalkSessionJoinRequest, error) {
	ref, err := r.normalizeRef("talk.session.join")
	return TalkSessionJoinRequest{ref}, err
}

type TalkSessionStartTurnRequest struct{ TalkSessionRef }

func (r TalkSessionStartTurnRequest) Normalize() (TalkSessionStartTurnRequest, error) {
	ref, err := r.normalizeRef("talk.session.startTurn")
	return TalkSessionStartTurnRequest{ref}, err
}

type TalkSessionEndTurnRequest struct{ TalkSessionRef }

func (r TalkSessionEndTurnRequest) Normalize() (TalkSessionEndTurnRequest, error) {
	ref, err := r.normalizeRef("talk.session.endTurn")
	return TalkSessionEndTurnRequest{ref}, err
}

type TalkSessionCancelTurnRequest struct{ TalkSessionRef }

func (r TalkSessionCancelTurnRequest) Normalize() (TalkSessionCancelTurnRequest, error) {
	ref, err := r.normalizeRef("talk.session.cancelTurn")
	return TalkSessionCancelTurnRequest{ref}, err
}

type TalkSessionCancelOutputRequest struct{ TalkSessionRef }

func (r TalkSessionCancelOutputRequest) Normalize() (TalkSessionCancelOutputRequest, error) {
	ref, err := r.normalizeRef("talk.session.cancelOutput")
	return TalkSessionCancelOutputRequest{ref}, err
}

type TalkSessionCloseRequest struct{ TalkSessionRef }

func (r TalkSessionCloseRequest) Normalize() (TalkSessionCloseRequest, error) {
	ref, err := r.normalizeRef("talk.session.close")
	return TalkSessionCloseRequest{ref}, err
}

// TalkSessionAppendAudioRequest streams a base64 audio chunk into the session.
type TalkSessionAppendAudioRequest struct {
	SessionID   string `json:"session_id"`
	AudioBase64 string `json:"audioBase64"`
}

func (r TalkSessionAppendAudioRequest) Normalize() (TalkSessionAppendAudioRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid talk.session.appendAudio params: session_id is required")
	}
	if r.AudioBase64 == "" {
		return r, fmt.Errorf("invalid talk.session.appendAudio params: audioBase64 is required")
	}
	if len(r.AudioBase64) > maxTalkAudioChunkBase64Length {
		return r, fmt.Errorf("audio chunk exceeds maximum size")
	}
	return r, nil
}

// TalkSessionAcknowledgeMarkRequest acknowledges a played-back audio mark.
type TalkSessionAcknowledgeMarkRequest struct {
	SessionID string `json:"session_id"`
	Mark      string `json:"mark"`
}

func (r TalkSessionAcknowledgeMarkRequest) Normalize() (TalkSessionAcknowledgeMarkRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid talk.session.acknowledgeMark params: session_id is required")
	}
	r.Mark = strings.TrimSpace(r.Mark)
	if r.Mark == "" {
		return r, fmt.Errorf("invalid talk.session.acknowledgeMark params: mark is required")
	}
	return r, nil
}

// TalkSessionSubmitToolResultRequest returns a realtime tool-call result.
type TalkSessionSubmitToolResultRequest struct {
	SessionID  string          `json:"session_id"`
	ToolCallID string          `json:"toolCallId"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
}

func (r TalkSessionSubmitToolResultRequest) Normalize() (TalkSessionSubmitToolResultRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid talk.session.submitToolResult params: session_id is required")
	}
	r.ToolCallID = strings.TrimSpace(r.ToolCallID)
	if r.ToolCallID == "" {
		return r, fmt.Errorf("invalid talk.session.submitToolResult params: toolCallId is required")
	}
	r.Error = strings.TrimSpace(r.Error)
	return r, nil
}

// TalkSessionSteerRequest steers the active session's agent run.
type TalkSessionSteerRequest struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

func (r TalkSessionSteerRequest) Normalize() (TalkSessionSteerRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid talk.session.steer params: session_id is required")
	}
	r.Text = strings.TrimSpace(r.Text)
	if r.Text == "" {
		return r, fmt.Errorf("invalid talk.session.steer params: text is required")
	}
	return r, nil
}

// ── Phase C: talk.client.* client-owned sessions ────────────────────────────

// TalkClientCreateRequest mints or resumes a client-owned voice session.
type TalkClientCreateRequest struct {
	SessionID string `json:"session_id,omitempty"`
	Transport string `json:"transport,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Voice     string `json:"voice,omitempty"`
	Language  string `json:"language,omitempty"`
	Model     string `json:"model,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

func (r TalkClientCreateRequest) Normalize() (TalkClientCreateRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Transport = strings.ToLower(strings.TrimSpace(r.Transport))
	if r.Transport == "" {
		r.Transport = "webrtc"
	}
	r.Provider = strings.TrimSpace(r.Provider)
	r.Voice = strings.TrimSpace(r.Voice)
	r.Language = strings.TrimSpace(r.Language)
	r.Model = strings.TrimSpace(r.Model)
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r, nil
}

// TalkClientTranscriptRequest appends a transcript entry to a client session.
type TalkClientTranscriptRequest struct {
	SessionID string `json:"session_id"`
	Role      string `json:"role,omitempty"`
	Text      string `json:"text"`
	Final     bool   `json:"final,omitempty"`
}

func (r TalkClientTranscriptRequest) Normalize() (TalkClientTranscriptRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid talk.client.transcript params: session_id is required")
	}
	r.Role = strings.ToLower(strings.TrimSpace(r.Role))
	if r.Role == "" {
		r.Role = "user"
	}
	r.Text = strings.TrimSpace(r.Text)
	if r.Text == "" {
		return r, fmt.Errorf("invalid talk.client.transcript params: text is required")
	}
	return r, nil
}

// TalkClientCloseRequest closes a client-owned session record.
type TalkClientCloseRequest struct {
	SessionID string `json:"session_id"`
}

func (r TalkClientCloseRequest) Normalize() (TalkClientCloseRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid talk.client.close params: session_id is required")
	}
	return r, nil
}

// TalkClientToolCallRequest bridges a realtime agent-consult tool into a run.
type TalkClientToolCallRequest struct {
	SessionID  string          `json:"session_id"`
	Tool       string          `json:"tool,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	AgentID    string          `json:"agent_id,omitempty"`
}

func (r TalkClientToolCallRequest) Normalize() (TalkClientToolCallRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid talk.client.toolCall params: session_id is required")
	}
	r.Tool = strings.TrimSpace(firstNonEmptyStr(r.Tool, r.Name))
	r.Name = ""
	if r.Tool == "" {
		return r, fmt.Errorf("invalid talk.client.toolCall params: tool is required")
	}
	r.ToolCallID = strings.TrimSpace(r.ToolCallID)
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r, nil
}

// TalkClientSteerRequest steers the client session's active agent run.
type TalkClientSteerRequest struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

func (r TalkClientSteerRequest) Normalize() (TalkClientSteerRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid talk.client.steer params: session_id is required")
	}
	r.Text = strings.TrimSpace(r.Text)
	if r.Text == "" {
		return r, fmt.Errorf("invalid talk.client.steer params: text is required")
	}
	return r, nil
}

// ── Decoders ────────────────────────────────────────────────────────────────

func DecodeTTSPersonasParams(p json.RawMessage) (TTSPersonasRequest, error) {
	return decodeMethodParams[TTSPersonasRequest](p)
}
func DecodeTTSSetPersonaParams(p json.RawMessage) (TTSSetPersonaRequest, error) {
	return decodeMethodParams[TTSSetPersonaRequest](p)
}
func DecodeTalkCatalogParams(p json.RawMessage) (TalkCatalogRequest, error) {
	return decodeMethodParams[TalkCatalogRequest](p)
}
func DecodeTalkSpeakParams(p json.RawMessage) (TalkSpeakRequest, error) {
	return decodeMethodParams[TalkSpeakRequest](p)
}
func DecodeVoicewakeRoutingGetParams(p json.RawMessage) (VoicewakeRoutingGetRequest, error) {
	return decodeMethodParams[VoicewakeRoutingGetRequest](p)
}
func DecodeVoicewakeRoutingSetParams(p json.RawMessage) (VoicewakeRoutingSetRequest, error) {
	return decodeMethodParams[VoicewakeRoutingSetRequest](p)
}
func DecodeTalkSessionCreateParams(p json.RawMessage) (TalkSessionCreateRequest, error) {
	return decodeMethodParams[TalkSessionCreateRequest](p)
}
func DecodeTalkSessionJoinParams(p json.RawMessage) (TalkSessionJoinRequest, error) {
	return decodeMethodParams[TalkSessionJoinRequest](p)
}
func DecodeTalkSessionAppendAudioParams(p json.RawMessage) (TalkSessionAppendAudioRequest, error) {
	return decodeMethodParams[TalkSessionAppendAudioRequest](p)
}
func DecodeTalkSessionStartTurnParams(p json.RawMessage) (TalkSessionStartTurnRequest, error) {
	return decodeMethodParams[TalkSessionStartTurnRequest](p)
}
func DecodeTalkSessionEndTurnParams(p json.RawMessage) (TalkSessionEndTurnRequest, error) {
	return decodeMethodParams[TalkSessionEndTurnRequest](p)
}
func DecodeTalkSessionCancelTurnParams(p json.RawMessage) (TalkSessionCancelTurnRequest, error) {
	return decodeMethodParams[TalkSessionCancelTurnRequest](p)
}
func DecodeTalkSessionCancelOutputParams(p json.RawMessage) (TalkSessionCancelOutputRequest, error) {
	return decodeMethodParams[TalkSessionCancelOutputRequest](p)
}
func DecodeTalkSessionAcknowledgeMarkParams(p json.RawMessage) (TalkSessionAcknowledgeMarkRequest, error) {
	return decodeMethodParams[TalkSessionAcknowledgeMarkRequest](p)
}
func DecodeTalkSessionSubmitToolResultParams(p json.RawMessage) (TalkSessionSubmitToolResultRequest, error) {
	return decodeMethodParams[TalkSessionSubmitToolResultRequest](p)
}
func DecodeTalkSessionSteerParams(p json.RawMessage) (TalkSessionSteerRequest, error) {
	return decodeMethodParams[TalkSessionSteerRequest](p)
}
func DecodeTalkSessionCloseParams(p json.RawMessage) (TalkSessionCloseRequest, error) {
	return decodeMethodParams[TalkSessionCloseRequest](p)
}
func DecodeTalkClientCreateParams(p json.RawMessage) (TalkClientCreateRequest, error) {
	return decodeMethodParams[TalkClientCreateRequest](p)
}
func DecodeTalkClientTranscriptParams(p json.RawMessage) (TalkClientTranscriptRequest, error) {
	return decodeMethodParams[TalkClientTranscriptRequest](p)
}
func DecodeTalkClientCloseParams(p json.RawMessage) (TalkClientCloseRequest, error) {
	return decodeMethodParams[TalkClientCloseRequest](p)
}
func DecodeTalkClientToolCallParams(p json.RawMessage) (TalkClientToolCallRequest, error) {
	return decodeMethodParams[TalkClientToolCallRequest](p)
}
func DecodeTalkClientSteerParams(p json.RawMessage) (TalkClientSteerRequest, error) {
	return decodeMethodParams[TalkClientSteerRequest](p)
}
