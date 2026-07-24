package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	SessionCatalogID     = "metiq-local"
	SessionCatalogHostID = "gateway-local"
)

type SessionCatalogCapabilities struct {
	ContinueSession bool `json:"continueSession"`
	Archive         bool `json:"archive"`
}
type SessionCatalogSession struct {
	ThreadID      string `json:"threadId"`
	Name          string `json:"name,omitempty"`
	CWD           string `json:"cwd,omitempty"`
	Status        string `json:"status"`
	CreatedAt     int64  `json:"createdAt,omitempty"`
	UpdatedAt     int64  `json:"updatedAt,omitempty"`
	RecencyAt     int64  `json:"recencyAt,omitempty"`
	Source        string `json:"source,omitempty"`
	ModelProvider string `json:"modelProvider,omitempty"`
	Archived      bool   `json:"archived"`
	SessionKey    string `json:"sessionKey,omitempty"`
	CanContinue   bool   `json:"canContinue"`
	CanArchive    bool   `json:"canArchive"`
}
type SessionCatalogHost struct {
	HostID     string                  `json:"hostId"`
	Label      string                  `json:"label"`
	Kind       string                  `json:"kind"`
	Connected  bool                    `json:"connected"`
	Sessions   []SessionCatalogSession `json:"sessions"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}
type SessionCatalog struct {
	ID           string                     `json:"id"`
	Label        string                     `json:"label"`
	Capabilities SessionCatalogCapabilities `json:"capabilities"`
	Hosts        []SessionCatalogHost       `json:"hosts"`
}
type SessionsCatalogListResult struct {
	Catalogs []SessionCatalog `json:"catalogs"`
}
type SessionCatalogTranscriptItem struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Model     string `json:"model,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}
type SessionsCatalogReadResult struct {
	HostID     string                         `json:"hostId"`
	Label      string                         `json:"label,omitempty"`
	ThreadID   string                         `json:"threadId"`
	Items      []SessionCatalogTranscriptItem `json:"items"`
	NextCursor string                         `json:"nextCursor,omitempty"`
}

type SessionsCatalogListRequest struct {
	CatalogID    string            `json:"catalogId,omitempty"`
	AgentID      string            `json:"agentId,omitempty"`
	ProgressID   string            `json:"progressId,omitempty"`
	Search       string            `json:"search,omitempty"`
	LimitPerHost int               `json:"limitPerHost,omitempty"`
	HostIDs      []string          `json:"hostIds,omitempty"`
	Cursors      map[string]string `json:"cursors,omitempty"`
}
type SessionsCatalogLocatorRequest struct {
	CatalogID string `json:"catalogId"`
	HostID    string `json:"hostId"`
	ThreadID  string `json:"threadId"`
}
type SessionsCatalogReadRequest struct {
	SessionsCatalogLocatorRequest
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}
type SessionsCatalogArchiveRequest struct {
	SessionsCatalogLocatorRequest
	ConfirmNoOtherRunner bool `json:"confirmNoOtherRunner"`
}

func DecodeSessionsCatalogListParams(params json.RawMessage) (SessionsCatalogListRequest, error) {
	var req SessionsCatalogListRequest
	if err := decodeCatalog(params, &req); err != nil {
		return req, err
	}
	req.CatalogID = strings.TrimSpace(req.CatalogID)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ProgressID = strings.TrimSpace(req.ProgressID)
	req.Search = strings.TrimSpace(req.Search)
	if req.CatalogID != "" && req.CatalogID != SessionCatalogID {
		return req, fmt.Errorf("unknown catalogId")
	}
	if req.Cursors != nil && req.CatalogID == "" {
		return req, fmt.Errorf("catalogId is required when cursors are provided")
	}
	if len([]rune(req.Search)) > 500 {
		return req, fmt.Errorf("search exceeds 500 characters")
	}
	if req.LimitPerHost == 0 {
		req.LimitPerHost = 50
	}
	if req.LimitPerHost < 1 || req.LimitPerHost > 200 {
		return req, fmt.Errorf("limitPerHost must be between 1 and 200")
	}
	for i := range req.HostIDs {
		req.HostIDs[i] = strings.TrimSpace(req.HostIDs[i])
		if req.HostIDs[i] == "" {
			return req, fmt.Errorf("hostIds entries must not be empty")
		}
	}
	return req, nil
}

func DecodeSessionsCatalogReadParams(params json.RawMessage) (SessionsCatalogReadRequest, error) {
	var req SessionsCatalogReadRequest
	if err := decodeCatalog(params, &req); err != nil {
		return req, err
	}
	req.SessionsCatalogLocatorRequest = normalizeCatalogLocator(req.SessionsCatalogLocatorRequest)
	if err := validateCatalogLocator(req.SessionsCatalogLocatorRequest); err != nil {
		return req, err
	}
	if req.Limit == 0 {
		req.Limit = 100
	}
	if req.Limit < 1 || req.Limit > 500 {
		return req, fmt.Errorf("limit must be between 1 and 500")
	}
	return req, nil
}

func DecodeSessionsCatalogContinueParams(params json.RawMessage) (SessionsCatalogLocatorRequest, error) {
	var req SessionsCatalogLocatorRequest
	if err := decodeCatalog(params, &req); err != nil {
		return req, err
	}
	req = normalizeCatalogLocator(req)
	return req, validateCatalogLocator(req)
}

func DecodeSessionsCatalogArchiveParams(params json.RawMessage) (SessionsCatalogArchiveRequest, error) {
	var req SessionsCatalogArchiveRequest
	if err := decodeCatalog(params, &req); err != nil {
		return req, err
	}
	req.SessionsCatalogLocatorRequest = normalizeCatalogLocator(req.SessionsCatalogLocatorRequest)
	if err := validateCatalogLocator(req.SessionsCatalogLocatorRequest); err != nil {
		return req, err
	}
	if !req.ConfirmNoOtherRunner {
		return req, fmt.Errorf("confirmNoOtherRunner must be true")
	}
	return req, nil
}

func decodeCatalog(params json.RawMessage, out any) error {
	if len(bytes.TrimSpace(params)) == 0 {
		params = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid params")
	}
	return nil
}
func normalizeCatalogLocator(req SessionsCatalogLocatorRequest) SessionsCatalogLocatorRequest {
	req.CatalogID = strings.TrimSpace(req.CatalogID)
	req.HostID = strings.TrimSpace(req.HostID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	return req
}
func validateCatalogLocator(req SessionsCatalogLocatorRequest) error {
	if req.CatalogID != SessionCatalogID || req.HostID != SessionCatalogHostID || req.ThreadID == "" {
		return fmt.Errorf("invalid catalog locator")
	}
	return nil
}
