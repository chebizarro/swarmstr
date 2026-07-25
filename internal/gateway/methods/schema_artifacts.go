package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Artifact method schemas. Params mirror the OpenClaw artifacts.* wire
// contract: list takes optional scope filters, get/download add a required
// artifact id. Artifacts are backed by the workspace content-addressed store
// in internal/gateway/artifacts.
//
// Note: runId/taskId/agentId arrive canonicalized as run_id/task_id/agent_id
// by normalizeObjectParamAliases, so the structs bind the canonical keys.

type ArtifactsListRequest struct {
	SessionKey string `json:"sessionKey,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
}

type ArtifactsGetRequest struct {
	SessionKey string `json:"sessionKey,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	ArtifactID string `json:"artifactId"`
}

// ArtifactsDownloadRequest shares the lookup shape of artifacts.get.
type ArtifactsDownloadRequest struct {
	SessionKey string `json:"sessionKey,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	ArtifactID string `json:"artifactId"`
}

func (r ArtifactsListRequest) Normalize() (ArtifactsListRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.RunID = strings.TrimSpace(r.RunID)
	r.TaskID = strings.TrimSpace(r.TaskID)
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r, nil
}

func normalizeArtifactLookup(r ArtifactsGetRequest, method string) (ArtifactsGetRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.RunID = strings.TrimSpace(r.RunID)
	r.TaskID = strings.TrimSpace(r.TaskID)
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.ArtifactID = strings.TrimSpace(r.ArtifactID)
	if r.ArtifactID == "" {
		return r, fmt.Errorf("invalid %s params: artifactId is required", method)
	}
	return r, nil
}

func (r ArtifactsGetRequest) Normalize() (ArtifactsGetRequest, error) {
	return normalizeArtifactLookup(r, MethodArtifactsGet)
}

func (r ArtifactsDownloadRequest) Normalize() (ArtifactsDownloadRequest, error) {
	norm, err := normalizeArtifactLookup(ArtifactsGetRequest(r), MethodArtifactsDownload)
	return ArtifactsDownloadRequest(norm), err
}

func DecodeArtifactsListParams(params json.RawMessage) (ArtifactsListRequest, error) {
	return decodeMethodParams[ArtifactsListRequest](params)
}

func DecodeArtifactsGetParams(params json.RawMessage) (ArtifactsGetRequest, error) {
	return decodeMethodParams[ArtifactsGetRequest](params)
}

func DecodeArtifactsDownloadParams(params json.RawMessage) (ArtifactsDownloadRequest, error) {
	return decodeMethodParams[ArtifactsDownloadRequest](params)
}
