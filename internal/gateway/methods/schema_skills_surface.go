package methods

// Skills discovery + staged-upload method schemas (WS-G skills.* long tail).
//
// Param shapes mirror OpenClaw's gateway-protocol
// packages/gateway-protocol/src/schema/agents-models-skills.ts contracts:
// skills.search/detail/securityVerdicts/skillCard and the chunked
// skills.upload.begin/chunk/commit flow. Upload payload hygiene follows the
// terminal.upload precedent: canonical padded base64 only, explicit byte caps.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	// MaxSkillUploadArchiveBytes bounds one staged skill archive (mirrors
	// OpenClaw DEFAULT_MAX_ARCHIVE_BYTES_ZIP).
	MaxSkillUploadArchiveBytes = 256 * 1024 * 1024
	// MaxSkillUploadChunkBytes bounds one uploaded chunk (mirrors OpenClaw
	// MAX_SKILL_UPLOAD_CHUNK_BYTES).
	MaxSkillUploadChunkBytes = 4 * 1024 * 1024
	// MaxSkillUploadChunkBase64Length is the base64 expansion of the chunk cap
	// (mirrors OpenClaw MAX_SKILL_UPLOAD_BASE64_LENGTH).
	MaxSkillUploadChunkBase64Length = (MaxSkillUploadChunkBytes + 2) / 3 * 4
	// maxSkillUploadIdempotencyKeyLength mirrors OpenClaw's 2048-char cap.
	maxSkillUploadIdempotencyKeyLength = 2048
	// maxSkillsSearchLimit mirrors the OpenClaw search limit ceiling.
	maxSkillsSearchLimit = 100
	// DefaultSkillsSearchLimit applies when a search request omits limit.
	DefaultSkillsSearchLimit = 20
)

var (
	skillUploadSha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	skillUploadSlugPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
)

// SkillsSearchRequest searches the local skill catalog.
// Metiq deviation: OpenClaw searches the ClawHub registry; Metiq ranks the
// merged local catalog (bundled + workspace + managed skills).
type SkillsSearchRequest struct {
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// SkillsDetailRequest reads detail for one skill slug (local skill key).
type SkillsDetailRequest struct {
	Slug string `json:"slug"`
}

// SkillsSecurityVerdictsRequest reads static security verdicts for the
// resolved skill catalog of one agent workspace.
type SkillsSecurityVerdictsRequest struct {
	AgentID string `json:"agent_id,omitempty"`
}

// SkillsSkillCardRequest reads the rendered skill card (SKILL.md) for one
// installed skill.
type SkillsSkillCardRequest struct {
	AgentID  string `json:"agent_id,omitempty"`
	SkillKey string `json:"skill_key"`
}

// SkillsUploadBeginRequest starts a chunked skill-archive upload.
type SkillsUploadBeginRequest struct {
	Kind           string `json:"kind"`
	Slug           string `json:"slug"`
	SizeBytes      int64  `json:"sizeBytes"`
	Sha256         string `json:"sha256,omitempty"`
	Force          bool   `json:"force,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// SkillsUploadChunkRequest uploads one base64-encoded chunk.
type SkillsUploadChunkRequest struct {
	UploadID   string `json:"uploadId"`
	Offset     int64  `json:"offset"`
	DataBase64 string `json:"dataBase64"`
}

// SkillsUploadCommitRequest commits a completed skill-archive upload.
type SkillsUploadCommitRequest struct {
	UploadID string `json:"uploadId"`
	Sha256   string `json:"sha256,omitempty"`
}

func (r SkillsSearchRequest) Normalize() (SkillsSearchRequest, error) {
	r.Query = strings.TrimSpace(r.Query)
	if r.Limit < 0 {
		return r, fmt.Errorf("invalid skills.search params: limit must be positive")
	}
	if r.Limit == 0 {
		r.Limit = DefaultSkillsSearchLimit
	}
	if r.Limit > maxSkillsSearchLimit {
		return r, fmt.Errorf("invalid skills.search params: limit must be between 1 and %d", maxSkillsSearchLimit)
	}
	return r, nil
}

func (r SkillsDetailRequest) Normalize() (SkillsDetailRequest, error) {
	r.Slug = strings.TrimSpace(r.Slug)
	if r.Slug == "" {
		return r, fmt.Errorf("invalid skills.detail params: slug is required")
	}
	return r, nil
}

func (r SkillsSecurityVerdictsRequest) Normalize() (SkillsSecurityVerdictsRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r, nil
}

func (r SkillsSkillCardRequest) Normalize() (SkillsSkillCardRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SkillKey = strings.TrimSpace(r.SkillKey)
	if r.SkillKey == "" {
		return r, fmt.Errorf("invalid skills.skillCard params: skillKey is required")
	}
	return r, nil
}

func (r SkillsUploadBeginRequest) Normalize() (SkillsUploadBeginRequest, error) {
	r.Kind = strings.TrimSpace(r.Kind)
	if r.Kind != "skill-archive" {
		return r, fmt.Errorf("unsupported upload kind")
	}
	r.Slug = strings.ToLower(strings.TrimSpace(r.Slug))
	if !skillUploadSlugPattern.MatchString(r.Slug) {
		return r, fmt.Errorf("invalid skill slug")
	}
	if r.SizeBytes < 1 {
		return r, fmt.Errorf("invalid sizeBytes")
	}
	if r.SizeBytes > MaxSkillUploadArchiveBytes {
		return r, fmt.Errorf("sizeBytes exceeds maximum archive size")
	}
	r.Sha256 = strings.ToLower(strings.TrimSpace(r.Sha256))
	if r.Sha256 != "" && !skillUploadSha256Pattern.MatchString(r.Sha256) {
		return r, fmt.Errorf("invalid sha256")
	}
	if len(r.IdempotencyKey) > maxSkillUploadIdempotencyKeyLength {
		return r, fmt.Errorf("invalid idempotencyKey")
	}
	return r, nil
}

func (r SkillsUploadChunkRequest) Normalize() (SkillsUploadChunkRequest, error) {
	r.UploadID = strings.TrimSpace(r.UploadID)
	if r.UploadID == "" {
		return r, fmt.Errorf("invalid skills.upload.chunk params: uploadId is required")
	}
	if r.Offset < 0 {
		return r, fmt.Errorf("invalid chunk offset")
	}
	if r.DataBase64 == "" {
		return r, fmt.Errorf("empty upload chunk")
	}
	if len(r.DataBase64) > MaxSkillUploadChunkBase64Length {
		return r, fmt.Errorf("upload chunk exceeds maximum size")
	}
	if len(r.DataBase64)%4 != 0 {
		return r, fmt.Errorf("invalid dataBase64")
	}
	return r, nil
}

// Content decodes the chunk payload, enforcing canonical padded base64 (no
// whitespace, exact padding, zero-valued unused bits) and the decoded-size
// cap, mirroring the terminal.upload hygiene.
func (r SkillsUploadChunkRequest) Content() ([]byte, error) {
	decoded, err := base64.StdEncoding.Strict().DecodeString(r.DataBase64)
	if err != nil {
		return nil, fmt.Errorf("invalid dataBase64")
	}
	if len(decoded) < 1 {
		return nil, fmt.Errorf("empty upload chunk")
	}
	if len(decoded) > MaxSkillUploadChunkBytes {
		return nil, fmt.Errorf("upload chunk exceeds maximum size")
	}
	// Strict() still ignores \r\n; a canonical payload round-trips exactly.
	if base64.StdEncoding.EncodeToString(decoded) != r.DataBase64 {
		return nil, fmt.Errorf("invalid dataBase64")
	}
	return decoded, nil
}

func (r SkillsUploadCommitRequest) Normalize() (SkillsUploadCommitRequest, error) {
	r.UploadID = strings.TrimSpace(r.UploadID)
	if r.UploadID == "" {
		return r, fmt.Errorf("invalid skills.upload.commit params: uploadId is required")
	}
	r.Sha256 = strings.ToLower(strings.TrimSpace(r.Sha256))
	if r.Sha256 != "" && !skillUploadSha256Pattern.MatchString(r.Sha256) {
		return r, fmt.Errorf("invalid sha256")
	}
	return r, nil
}

func DecodeSkillsSearchParams(params json.RawMessage) (SkillsSearchRequest, error) {
	return decodeMethodParams[SkillsSearchRequest](params)
}

func DecodeSkillsDetailParams(params json.RawMessage) (SkillsDetailRequest, error) {
	return decodeMethodParams[SkillsDetailRequest](params)
}

func DecodeSkillsSecurityVerdictsParams(params json.RawMessage) (SkillsSecurityVerdictsRequest, error) {
	return decodeMethodParams[SkillsSecurityVerdictsRequest](params)
}

func DecodeSkillsSkillCardParams(params json.RawMessage) (SkillsSkillCardRequest, error) {
	return decodeMethodParams[SkillsSkillCardRequest](params)
}

func DecodeSkillsUploadBeginParams(params json.RawMessage) (SkillsUploadBeginRequest, error) {
	return decodeMethodParams[SkillsUploadBeginRequest](params)
}

func DecodeSkillsUploadChunkParams(params json.RawMessage) (SkillsUploadChunkRequest, error) {
	return decodeMethodParams[SkillsUploadChunkRequest](params)
}

func DecodeSkillsUploadCommitParams(params json.RawMessage) (SkillsUploadCommitRequest, error) {
	return decodeMethodParams[SkillsUploadCommitRequest](params)
}
