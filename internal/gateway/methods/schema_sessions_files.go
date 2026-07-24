package methods

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"metiq/internal/workspace"
)

type SessionsFilesListRequest struct {
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agentId,omitempty"`
	Path       string `json:"path,omitempty"`
	Search     string `json:"search,omitempty"`
}

type SessionsFilesGetRequest struct {
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agentId,omitempty"`
	Path       string `json:"path"`
}

type SessionsFilesSetRequest struct {
	SessionKey   string `json:"sessionKey"`
	AgentID      string `json:"agentId,omitempty"`
	Path         string `json:"path"`
	Content      string `json:"content"`
	ExpectedHash string `json:"expectedHash"`
}

type SessionsFilesRevealRequest struct {
	Key     string `json:"key"`
	AgentID string `json:"agentId,omitempty"`
}

type SessionsFilesListResult struct {
	SessionKey string                  `json:"sessionKey"`
	Root       string                  `json:"root,omitempty"`
	Files      []workspace.FileEntry   `json:"files"`
	Browser    workspace.BrowserResult `json:"browser"`
}

type SessionsFilesGetResult struct {
	SessionKey string              `json:"sessionKey"`
	Root       string              `json:"root,omitempty"`
	File       workspace.FileEntry `json:"file"`
}

type sessionsFilesCompat struct {
	SessionKey        string  `json:"sessionKey,omitempty"`
	SessionKeySnake   string  `json:"session_key,omitempty"`
	Key               string  `json:"key,omitempty"`
	AgentID           string  `json:"agentId,omitempty"`
	AgentIDSnake      string  `json:"agent_id,omitempty"`
	Path              string  `json:"path,omitempty"`
	Search            string  `json:"search,omitempty"`
	Content           *string `json:"content,omitempty"`
	ExpectedHash      string  `json:"expectedHash,omitempty"`
	ExpectedHashSnake string  `json:"expected_hash,omitempty"`
}

func decodeSessionsFilesCompat(params json.RawMessage) (sessionsFilesCompat, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		params = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var raw sessionsFilesCompat
	if err := dec.Decode(&raw); err != nil {
		return raw, fmt.Errorf("invalid params")
	}
	if raw.SessionKey == "" {
		raw.SessionKey = raw.SessionKeySnake
	}
	if raw.SessionKey == "" {
		raw.SessionKey = raw.Key
	}
	if raw.AgentID == "" {
		raw.AgentID = raw.AgentIDSnake
	}
	if raw.ExpectedHash == "" {
		raw.ExpectedHash = raw.ExpectedHashSnake
	}
	raw.SessionKey = strings.TrimSpace(raw.SessionKey)
	raw.AgentID = strings.TrimSpace(raw.AgentID)
	raw.Path = strings.TrimSpace(raw.Path)
	raw.Search = strings.TrimSpace(raw.Search)
	return raw, nil
}

func DecodeSessionsFilesListParams(params json.RawMessage) (SessionsFilesListRequest, error) {
	raw, err := decodeSessionsFilesCompat(params)
	if err != nil {
		return SessionsFilesListRequest{}, err
	}
	if raw.SessionKey == "" {
		return SessionsFilesListRequest{}, fmt.Errorf("sessionKey is required")
	}
	if len([]rune(raw.Search)) > 500 {
		return SessionsFilesListRequest{}, fmt.Errorf("search exceeds 500 characters")
	}
	return SessionsFilesListRequest{SessionKey: raw.SessionKey, AgentID: raw.AgentID, Path: raw.Path, Search: raw.Search}, nil
}

func DecodeSessionsFilesGetParams(params json.RawMessage) (SessionsFilesGetRequest, error) {
	raw, err := decodeSessionsFilesCompat(params)
	if err != nil {
		return SessionsFilesGetRequest{}, err
	}
	if raw.SessionKey == "" || raw.Path == "" {
		return SessionsFilesGetRequest{}, fmt.Errorf("sessionKey and path are required")
	}
	return SessionsFilesGetRequest{SessionKey: raw.SessionKey, AgentID: raw.AgentID, Path: raw.Path}, nil
}

func DecodeSessionsFilesSetParams(params json.RawMessage) (SessionsFilesSetRequest, error) {
	raw, err := decodeSessionsFilesCompat(params)
	if err != nil {
		return SessionsFilesSetRequest{}, err
	}
	if raw.SessionKey == "" || raw.Path == "" || raw.Content == nil {
		return SessionsFilesSetRequest{}, fmt.Errorf("sessionKey, path, and content are required")
	}
	if len(*raw.Content) > workspace.MaxSessionFileBytes || !utf8.ValidString(*raw.Content) {
		return SessionsFilesSetRequest{}, fmt.Errorf("content exceeds file limits")
	}
	if len(raw.ExpectedHash) != 64 || strings.ToLower(raw.ExpectedHash) != raw.ExpectedHash {
		return SessionsFilesSetRequest{}, fmt.Errorf("expectedHash must be a lowercase SHA-256 hash")
	}
	if _, err := hex.DecodeString(raw.ExpectedHash); err != nil {
		return SessionsFilesSetRequest{}, fmt.Errorf("expectedHash must be a lowercase SHA-256 hash")
	}
	return SessionsFilesSetRequest{SessionKey: raw.SessionKey, AgentID: raw.AgentID, Path: raw.Path, Content: *raw.Content, ExpectedHash: raw.ExpectedHash}, nil
}

func DecodeSessionsFilesRevealParams(params json.RawMessage) (SessionsFilesRevealRequest, error) {
	raw, err := decodeSessionsFilesCompat(params)
	if err != nil {
		return SessionsFilesRevealRequest{}, err
	}
	if raw.SessionKey == "" {
		return SessionsFilesRevealRequest{}, fmt.Errorf("key is required")
	}
	return SessionsFilesRevealRequest{Key: raw.SessionKey, AgentID: raw.AgentID}, nil
}
