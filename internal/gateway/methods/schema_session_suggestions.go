package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SessionSuggestionsAddRequest mirrors OpenClaw SessionSuggestionsAddParams.
type SessionSuggestionsAddRequest struct {
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agent_id,omitempty"`
	Text       string `json:"text"`
}

func (r SessionSuggestionsAddRequest) Normalize() (SessionSuggestionsAddRequest, error) {
	r.SessionKey, r.AgentID = strings.TrimSpace(r.SessionKey), strings.TrimSpace(r.AgentID)
	if r.SessionKey == "" || strings.TrimSpace(r.Text) == "" {
		return r, fmt.Errorf("sessionKey and text are required")
	}
	return r, nil
}

func DecodeSessionSuggestionsAddParams(raw json.RawMessage) (SessionSuggestionsAddRequest, error) {
	return decodeMethodParams[SessionSuggestionsAddRequest](raw)
}

// SessionSuggestionsListRequest mirrors OpenClaw SessionSuggestionsListParams.
type SessionSuggestionsListRequest struct {
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agent_id,omitempty"`
}

func (r SessionSuggestionsListRequest) Normalize() (SessionSuggestionsListRequest, error) {
	r.SessionKey, r.AgentID = strings.TrimSpace(r.SessionKey), strings.TrimSpace(r.AgentID)
	if r.SessionKey == "" {
		return r, fmt.Errorf("sessionKey is required")
	}
	return r, nil
}

func DecodeSessionSuggestionsListParams(raw json.RawMessage) (SessionSuggestionsListRequest, error) {
	return decodeMethodParams[SessionSuggestionsListRequest](raw)
}

// SessionSuggestionsResolveRequest mirrors OpenClaw SessionSuggestionsResolveParams.
type SessionSuggestionsResolveRequest struct {
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agent_id,omitempty"`
	ID         string `json:"id"`
	Resolution string `json:"resolution"`
}

func (r SessionSuggestionsResolveRequest) Normalize() (SessionSuggestionsResolveRequest, error) {
	r.SessionKey, r.AgentID = strings.TrimSpace(r.SessionKey), strings.TrimSpace(r.AgentID)
	r.ID, r.Resolution = strings.TrimSpace(r.ID), strings.TrimSpace(r.Resolution)
	if r.SessionKey == "" || r.ID == "" || r.Resolution == "" {
		return r, fmt.Errorf("sessionKey, id, and resolution are required")
	}
	return r, nil
}

func DecodeSessionSuggestionsResolveParams(raw json.RawMessage) (SessionSuggestionsResolveRequest, error) {
	return decodeMethodParams[SessionSuggestionsResolveRequest](raw)
}

// SessionTypingRequest mirrors OpenClaw SessionTypingParams. The sessionId
// wire field arrives as session_id after alias normalization.
type SessionTypingRequest struct {
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agent_id,omitempty"`
	SessionID  string `json:"session_id"`
	Typing     bool   `json:"typing"`
}

func (r SessionTypingRequest) Normalize() (SessionTypingRequest, error) {
	r.SessionKey, r.AgentID = strings.TrimSpace(r.SessionKey), strings.TrimSpace(r.AgentID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionKey == "" || r.SessionID == "" {
		return r, fmt.Errorf("sessionKey and sessionId are required")
	}
	return r, nil
}

func DecodeSessionTypingParams(raw json.RawMessage) (SessionTypingRequest, error) {
	return decodeMethodParams[SessionTypingRequest](raw)
}
