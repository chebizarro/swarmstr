package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

type SessionsDispatchRequest struct {
	Key       string `json:"key"`
	AgentID   string `json:"agent_id,omitempty"`
	ProfileID string `json:"profileId,omitempty"`
	Backend   string `json:"backend,omitempty"`
}

func (r SessionsDispatchRequest) Normalize() (SessionsDispatchRequest, error) {
	r.Key, r.AgentID = strings.TrimSpace(r.Key), strings.TrimSpace(r.AgentID)
	r.ProfileID, r.Backend = strings.TrimSpace(r.ProfileID), strings.TrimSpace(r.Backend)
	if r.Backend == "" {
		r.Backend = r.ProfileID
	}
	if r.Key == "" || r.Backend == "" {
		return r, fmt.Errorf("key and profileId (or backend) are required")
	}
	return r, nil
}

func DecodeSessionsDispatchParams(raw json.RawMessage) (SessionsDispatchRequest, error) {
	return decodeMethodParams[SessionsDispatchRequest](raw)
}

type SessionsReclaimRequest struct {
	Key     string `json:"key"`
	AgentID string `json:"agent_id,omitempty"`
}

func (r SessionsReclaimRequest) Normalize() (SessionsReclaimRequest, error) {
	r.Key, r.AgentID = strings.TrimSpace(r.Key), strings.TrimSpace(r.AgentID)
	if r.Key == "" {
		return r, fmt.Errorf("key is required")
	}
	return r, nil
}
func DecodeSessionsReclaimParams(raw json.RawMessage) (SessionsReclaimRequest, error) {
	return decodeMethodParams[SessionsReclaimRequest](raw)
}

type SessionsGroupsListRequest struct{}

func DecodeSessionsGroupsListParams(raw json.RawMessage) (SessionsGroupsListRequest, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return SessionsGroupsListRequest{}, nil
	}
	return decodeMethodParams[SessionsGroupsListRequest](raw)
}

type SessionsGroupsPutRequest struct {
	Names []string `json:"names"`
}

func DecodeSessionsGroupsPutParams(raw json.RawMessage) (SessionsGroupsPutRequest, error) {
	return decodeMethodParams[SessionsGroupsPutRequest](raw)
}

type SessionsGroupsRenameRequest struct {
	Name string `json:"name"`
	To   string `json:"to"`
}

func DecodeSessionsGroupsRenameParams(raw json.RawMessage) (SessionsGroupsRenameRequest, error) {
	return decodeMethodParams[SessionsGroupsRenameRequest](raw)
}

type SessionsGroupsDeleteRequest struct {
	Name string `json:"name"`
}

func DecodeSessionsGroupsDeleteParams(raw json.RawMessage) (SessionsGroupsDeleteRequest, error) {
	return decodeMethodParams[SessionsGroupsDeleteRequest](raw)
}
