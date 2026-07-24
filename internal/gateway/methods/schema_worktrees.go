package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Worktree method schemas. Params mirror the OpenClaw worktrees.* wire
// contract; git worktree lifecycle is backed by internal/gateway/worktrees.

type WorktreesListRequest struct{}

type WorktreesBranchesRequest struct {
	RepoRoot                string `json:"repoRoot"`
	IncludeRepositoryStatus bool   `json:"includeRepositoryStatus,omitempty"`
}

type WorktreesCreateRequest struct {
	RepoRoot string `json:"repoRoot"`
	Name     string `json:"name,omitempty"`
	BaseRef  string `json:"baseRef,omitempty"`
}

type WorktreesRemoveRequest struct {
	ID    string `json:"id"`
	Force bool   `json:"force,omitempty"`
}

type WorktreesRestoreRequest struct {
	ID string `json:"id"`
}

type WorktreesGcRequest struct{}

func (r WorktreesBranchesRequest) Normalize() (WorktreesBranchesRequest, error) {
	r.RepoRoot = strings.TrimSpace(r.RepoRoot)
	if r.RepoRoot == "" {
		return r, fmt.Errorf("invalid worktrees.branches params: repoRoot is required")
	}
	return r, nil
}

func (r WorktreesCreateRequest) Normalize() (WorktreesCreateRequest, error) {
	r.RepoRoot = strings.TrimSpace(r.RepoRoot)
	r.Name = strings.TrimSpace(r.Name)
	r.BaseRef = strings.TrimSpace(r.BaseRef)
	if r.RepoRoot == "" {
		return r, fmt.Errorf("invalid worktrees.create params: repoRoot is required")
	}
	return r, nil
}

func (r WorktreesRemoveRequest) Normalize() (WorktreesRemoveRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("invalid worktrees.remove params: id is required")
	}
	return r, nil
}

func (r WorktreesRestoreRequest) Normalize() (WorktreesRestoreRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("invalid worktrees.restore params: id is required")
	}
	return r, nil
}

func DecodeWorktreesListParams(params json.RawMessage) (WorktreesListRequest, error) {
	return decodeMethodParams[WorktreesListRequest](params)
}

func DecodeWorktreesBranchesParams(params json.RawMessage) (WorktreesBranchesRequest, error) {
	return decodeMethodParams[WorktreesBranchesRequest](params)
}

func DecodeWorktreesCreateParams(params json.RawMessage) (WorktreesCreateRequest, error) {
	return decodeMethodParams[WorktreesCreateRequest](params)
}

func DecodeWorktreesRemoveParams(params json.RawMessage) (WorktreesRemoveRequest, error) {
	return decodeMethodParams[WorktreesRemoveRequest](params)
}

func DecodeWorktreesRestoreParams(params json.RawMessage) (WorktreesRestoreRequest, error) {
	return decodeMethodParams[WorktreesRestoreRequest](params)
}

func DecodeWorktreesGcParams(params json.RawMessage) (WorktreesGcRequest, error) {
	return decodeMethodParams[WorktreesGcRequest](params)
}
