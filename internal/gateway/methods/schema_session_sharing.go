package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SessionVisibilitySetRequest mirrors OpenClaw SessionVisibilitySetParams.
type SessionVisibilitySetRequest struct {
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agent_id,omitempty"`
	Visibility string `json:"visibility"`
}

func (r SessionVisibilitySetRequest) Normalize() (SessionVisibilitySetRequest, error) {
	r.SessionKey, r.AgentID = strings.TrimSpace(r.SessionKey), strings.TrimSpace(r.AgentID)
	r.Visibility = strings.TrimSpace(r.Visibility)
	if r.SessionKey == "" || r.Visibility == "" {
		return r, fmt.Errorf("sessionKey and visibility are required")
	}
	return r, nil
}

func DecodeSessionVisibilitySetParams(raw json.RawMessage) (SessionVisibilitySetRequest, error) {
	return decodeMethodParams[SessionVisibilitySetRequest](raw)
}

// SessionMembersListRequest mirrors OpenClaw SessionMembersListParams.
type SessionMembersListRequest struct {
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agent_id,omitempty"`
}

func (r SessionMembersListRequest) Normalize() (SessionMembersListRequest, error) {
	r.SessionKey, r.AgentID = strings.TrimSpace(r.SessionKey), strings.TrimSpace(r.AgentID)
	if r.SessionKey == "" {
		return r, fmt.Errorf("sessionKey is required")
	}
	return r, nil
}

func DecodeSessionMembersListParams(raw json.RawMessage) (SessionMembersListRequest, error) {
	return decodeMethodParams[SessionMembersListRequest](raw)
}

// SessionMemberMutateRequest mirrors OpenClaw SessionMemberAdd/RemoveParams.
type SessionMemberMutateRequest struct {
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agent_id,omitempty"`
	IdentityID string `json:"identityId"`
}

func (r SessionMemberMutateRequest) Normalize() (SessionMemberMutateRequest, error) {
	r.SessionKey, r.AgentID = strings.TrimSpace(r.SessionKey), strings.TrimSpace(r.AgentID)
	r.IdentityID = strings.TrimSpace(r.IdentityID)
	if r.SessionKey == "" || r.IdentityID == "" {
		return r, fmt.Errorf("sessionKey and identityId are required")
	}
	return r, nil
}

func DecodeSessionMemberMutateParams(raw json.RawMessage) (SessionMemberMutateRequest, error) {
	return decodeMethodParams[SessionMemberMutateRequest](raw)
}

// SessionsObserverVisibilityRequest mirrors OpenClaw SessionsObserverVisibilityParams.
type SessionsObserverVisibilityRequest struct {
	Visible bool `json:"visible"`
}

func DecodeSessionsObserverVisibilityParams(raw json.RawMessage) (SessionsObserverVisibilityRequest, error) {
	return decodeMethodParams[SessionsObserverVisibilityRequest](raw)
}
