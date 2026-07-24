package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SessionDiscussionRequest mirrors OpenClaw SessionDiscussionInfo/OpenParams.
type SessionDiscussionRequest struct {
	SessionKey string `json:"sessionKey"`
}

func (r SessionDiscussionRequest) Normalize() (SessionDiscussionRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	if r.SessionKey == "" {
		return r, fmt.Errorf("sessionKey is required")
	}
	return r, nil
}

func DecodeSessionDiscussionParams(raw json.RawMessage) (SessionDiscussionRequest, error) {
	return decodeMethodParams[SessionDiscussionRequest](raw)
}

// SessionsObserverAskRequest mirrors OpenClaw SessionsObserverAskParams.
type SessionsObserverAskRequest struct {
	SessionKey string `json:"sessionKey"`
	Question   string `json:"question"`
}

func (r SessionsObserverAskRequest) Normalize() (SessionsObserverAskRequest, error) {
	r.SessionKey, r.Question = strings.TrimSpace(r.SessionKey), strings.TrimSpace(r.Question)
	if r.SessionKey == "" || r.Question == "" {
		return r, fmt.Errorf("sessionKey and question are required")
	}
	return r, nil
}

func DecodeSessionsObserverAskParams(raw json.RawMessage) (SessionsObserverAskRequest, error) {
	return decodeMethodParams[SessionsObserverAskRequest](raw)
}
