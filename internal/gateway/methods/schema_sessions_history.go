package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// SessionsCompactionQueryRequest identifies a persisted compaction checkpoint.
// CheckpointID is omitted for sessions.compaction.list.
type SessionsCompactionQueryRequest struct {
	Key          string `json:"key"`
	AgentID      string `json:"agentId,omitempty"`
	CheckpointID string `json:"checkpointId,omitempty"`
}

func (r SessionsCompactionQueryRequest) Normalize(requireCheckpoint bool) (SessionsCompactionQueryRequest, error) {
	r.Key = strings.TrimSpace(r.Key)
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.CheckpointID = strings.TrimSpace(r.CheckpointID)
	if r.Key == "" {
		return SessionsCompactionQueryRequest{}, fmt.Errorf("key is required")
	}
	if requireCheckpoint && r.CheckpointID == "" {
		return SessionsCompactionQueryRequest{}, fmt.Errorf("checkpointId is required")
	}
	return r, nil
}

func DecodeSessionsCompactionListParams(params json.RawMessage) (SessionsCompactionQueryRequest, error) {
	return decodeSessionsCompactionQuery(params, false)
}

func DecodeSessionsCompactionGetParams(params json.RawMessage) (SessionsCompactionQueryRequest, error) {
	return decodeSessionsCompactionQuery(params, true)
}

func DecodeSessionsCompactionBranchParams(params json.RawMessage) (SessionsCompactionQueryRequest, error) {
	return decodeSessionsCompactionQuery(params, true)
}

func DecodeSessionsCompactionRestoreParams(params json.RawMessage) (SessionsCompactionQueryRequest, error) {
	return decodeSessionsCompactionQuery(params, true)
}

func decodeSessionsCompactionQuery(params json.RawMessage, requireCheckpoint bool) (SessionsCompactionQueryRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		params = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var req SessionsCompactionQueryRequest
	if err := dec.Decode(&req); err != nil {
		return SessionsCompactionQueryRequest{}, fmt.Errorf("invalid params")
	}
	return req.Normalize(requireCheckpoint)
}

type SessionsHistoryMutationRequest struct {
	SessionKey  string `json:"sessionKey"`
	AgentID     string `json:"agentId,omitempty"`
	EntryID     string `json:"entryId,omitempty"`
	LeafEntryID string `json:"leafEntryId,omitempty"`
}

func decodeSessionsHistoryMutation(params json.RawMessage, requireEntry, requireLeaf bool) (SessionsHistoryMutationRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		params = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var req SessionsHistoryMutationRequest
	if err := dec.Decode(&req); err != nil {
		return SessionsHistoryMutationRequest{}, fmt.Errorf("invalid params")
	}
	req.SessionKey = strings.TrimSpace(req.SessionKey)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.EntryID = strings.TrimSpace(req.EntryID)
	req.LeafEntryID = strings.TrimSpace(req.LeafEntryID)
	if req.SessionKey == "" {
		return SessionsHistoryMutationRequest{}, fmt.Errorf("sessionKey is required")
	}
	if requireEntry && req.EntryID == "" {
		return SessionsHistoryMutationRequest{}, fmt.Errorf("entryId is required")
	}
	if requireLeaf && req.LeafEntryID == "" {
		return SessionsHistoryMutationRequest{}, fmt.Errorf("leafEntryId is required")
	}
	if !requireEntry && req.EntryID != "" {
		return SessionsHistoryMutationRequest{}, fmt.Errorf("invalid params")
	}
	if !requireLeaf && req.LeafEntryID != "" {
		return SessionsHistoryMutationRequest{}, fmt.Errorf("invalid params")
	}
	return req, nil
}

func DecodeSessionsBranchesListParams(params json.RawMessage) (SessionsHistoryMutationRequest, error) {
	return decodeSessionsHistoryMutation(params, false, false)
}

func DecodeSessionsBranchesSwitchParams(params json.RawMessage) (SessionsHistoryMutationRequest, error) {
	return decodeSessionsHistoryMutation(params, false, true)
}

func DecodeSessionsRewindParams(params json.RawMessage) (SessionsHistoryMutationRequest, error) {
	return decodeSessionsHistoryMutation(params, true, false)
}

func DecodeSessionsForkParams(params json.RawMessage) (SessionsHistoryMutationRequest, error) {
	return decodeSessionsHistoryMutation(params, true, false)
}

// SessionsSearchRequest is a bounded transcript full-text query compatible
// with the OpenClaw gateway v4 sessions.search surface.
type SessionsSearchRequest struct {
	AgentID     string   `json:"agentId,omitempty"`
	SessionKeys []string `json:"sessionKeys,omitempty"`
	Query       string   `json:"query"`
	Limit       int      `json:"limit,omitempty"`
}

func (r SessionsSearchRequest) Normalize() (SessionsSearchRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.Query = strings.TrimSpace(r.Query)
	if r.Query == "" {
		return SessionsSearchRequest{}, fmt.Errorf("query is required")
	}
	if len([]rune(r.Query)) > 4096 {
		return SessionsSearchRequest{}, fmt.Errorf("query exceeds 4096 characters")
	}
	if len(r.SessionKeys) > 200 {
		return SessionsSearchRequest{}, fmt.Errorf("sessionKeys exceeds 200 entries")
	}
	if r.SessionKeys != nil && len(r.SessionKeys) == 0 {
		return SessionsSearchRequest{}, fmt.Errorf("sessionKeys must not be empty")
	}
	seen := make(map[string]struct{}, len(r.SessionKeys))
	keys := make([]string, 0, len(r.SessionKeys))
	for _, key := range r.SessionKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			return SessionsSearchRequest{}, fmt.Errorf("sessionKeys entries must not be empty")
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	r.SessionKeys = keys
	if r.Limit == 0 {
		r.Limit = 10
	}
	if r.Limit < 1 || r.Limit > 25 {
		return SessionsSearchRequest{}, fmt.Errorf("limit must be between 1 and 25")
	}
	return r, nil
}

func DecodeSessionsSearchParams(params json.RawMessage) (SessionsSearchRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		params = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var req SessionsSearchRequest
	if err := dec.Decode(&req); err != nil {
		return SessionsSearchRequest{}, fmt.Errorf("invalid params")
	}
	return req.Normalize()
}
