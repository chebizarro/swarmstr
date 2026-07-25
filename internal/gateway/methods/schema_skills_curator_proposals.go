package methods

// Curator + skill-workshop proposal method schemas (WS-G skills.* long tail,
// swarmstr-xfny.3/.4/.5).
//
// Param shapes mirror OpenClaw's gateway-protocol skills curator/proposal
// contracts, adapted to metiq: the agent workspace is addressed by the existing
// skills.* `agent_id` convention, and proposals carry an inline SKILL.md draft
// plus bounded support files.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ── curator ────────────────────────────────────────────────────────────────

// SkillsCuratorStatusRequest reads the curator status report for one workspace.
type SkillsCuratorStatusRequest struct {
	AgentID string `json:"agent_id,omitempty"`
}

// SkillsCuratorSkillRequest targets one skill for pin/unpin/restore.
type SkillsCuratorSkillRequest struct {
	AgentID string `json:"agent_id,omitempty"`
	Skill   string `json:"skill"`
}

func (r SkillsCuratorStatusRequest) Normalize() (SkillsCuratorStatusRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r, nil
}

func (r SkillsCuratorSkillRequest) Normalize() (SkillsCuratorSkillRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.Skill = strings.TrimSpace(r.Skill)
	if r.Skill == "" {
		return r, fmt.Errorf("invalid skills.curator params: skill is required")
	}
	return r, nil
}

// ── proposals ──────────────────────────────────────────────────────────────

// SkillProposalSupportFile is one inline support file staged with a proposal.
type SkillProposalSupportFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SkillsProposalsListRequest lists proposals for one workspace.
type SkillsProposalsListRequest struct {
	AgentID string `json:"agent_id,omitempty"`
}

// SkillsProposalsInspectRequest reads one proposal with its draft + support files.
type SkillsProposalsInspectRequest struct {
	AgentID    string `json:"agent_id,omitempty"`
	ProposalID string `json:"proposalId"`
}

// SkillsProposalsCreateRequest stages a proposal (create=new skill, update=existing).
type SkillsProposalsCreateRequest struct {
	AgentID         string                     `json:"agent_id,omitempty"`
	Title           string                     `json:"title"`
	Description     string                     `json:"description,omitempty"`
	Content         string                     `json:"content"`
	ProposedVersion string                     `json:"proposedVersion,omitempty"`
	SkillName       string                     `json:"skillName,omitempty"`
	SkillKey        string                     `json:"skillKey,omitempty"`
	SupportFiles    []SkillProposalSupportFile `json:"supportFiles,omitempty"`
}

// SkillsProposalsReviseRequest replaces the draft on a pending proposal.
type SkillsProposalsReviseRequest struct {
	AgentID         string                     `json:"agent_id,omitempty"`
	ProposalID      string                     `json:"proposalId"`
	Title           string                     `json:"title,omitempty"`
	Description     string                     `json:"description,omitempty"`
	Content         string                     `json:"content"`
	ProposedVersion string                     `json:"proposedVersion,omitempty"`
	SupportFiles    []SkillProposalSupportFile `json:"supportFiles,omitempty"`
}

// SkillsProposalsIDRequest targets one proposal by id (apply/reject/quarantine).
type SkillsProposalsIDRequest struct {
	AgentID    string `json:"agent_id,omitempty"`
	ProposalID string `json:"proposalId"`
	Reason     string `json:"reason,omitempty"`
}

func (r SkillsProposalsListRequest) Normalize() (SkillsProposalsListRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r, nil
}

func (r SkillsProposalsInspectRequest) Normalize() (SkillsProposalsInspectRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.ProposalID = strings.TrimSpace(r.ProposalID)
	if r.ProposalID == "" {
		return r, fmt.Errorf("invalid skills.proposals.inspect params: proposalId is required")
	}
	return r, nil
}

func normalizeProposalSupportFiles(in []SkillProposalSupportFile) ([]SkillProposalSupportFile, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]SkillProposalSupportFile, 0, len(in))
	for _, f := range in {
		path := strings.TrimSpace(f.Path)
		if path == "" {
			return nil, fmt.Errorf("support file path is required")
		}
		out = append(out, SkillProposalSupportFile{Path: path, Content: f.Content})
	}
	return out, nil
}

func (r SkillsProposalsCreateRequest) Normalize() (SkillsProposalsCreateRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.Title = strings.TrimSpace(r.Title)
	r.SkillName = strings.TrimSpace(r.SkillName)
	r.SkillKey = strings.TrimSpace(r.SkillKey)
	r.ProposedVersion = strings.TrimSpace(r.ProposedVersion)
	if r.Title == "" {
		return r, fmt.Errorf("invalid skills.proposals params: title is required")
	}
	if strings.TrimSpace(r.Content) == "" {
		return r, fmt.Errorf("invalid skills.proposals params: content is required")
	}
	if r.SkillName == "" && r.SkillKey == "" {
		return r, fmt.Errorf("invalid skills.proposals params: skillName or skillKey is required")
	}
	support, err := normalizeProposalSupportFiles(r.SupportFiles)
	if err != nil {
		return r, err
	}
	r.SupportFiles = support
	return r, nil
}

func (r SkillsProposalsReviseRequest) Normalize() (SkillsProposalsReviseRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.ProposalID = strings.TrimSpace(r.ProposalID)
	r.Title = strings.TrimSpace(r.Title)
	r.ProposedVersion = strings.TrimSpace(r.ProposedVersion)
	if r.ProposalID == "" {
		return r, fmt.Errorf("invalid skills.proposals.revise params: proposalId is required")
	}
	if strings.TrimSpace(r.Content) == "" {
		return r, fmt.Errorf("invalid skills.proposals.revise params: content is required")
	}
	support, err := normalizeProposalSupportFiles(r.SupportFiles)
	if err != nil {
		return r, err
	}
	r.SupportFiles = support
	return r, nil
}

func (r SkillsProposalsIDRequest) Normalize() (SkillsProposalsIDRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.ProposalID = strings.TrimSpace(r.ProposalID)
	if r.ProposalID == "" {
		return r, fmt.Errorf("invalid skills.proposals params: proposalId is required")
	}
	return r, nil
}

// ── decoders ─────────────────────────────────────────────────────────────────

func DecodeSkillsCuratorStatusParams(params json.RawMessage) (SkillsCuratorStatusRequest, error) {
	return decodeMethodParams[SkillsCuratorStatusRequest](params)
}

func DecodeSkillsCuratorSkillParams(params json.RawMessage) (SkillsCuratorSkillRequest, error) {
	return decodeMethodParams[SkillsCuratorSkillRequest](params)
}

func DecodeSkillsProposalsListParams(params json.RawMessage) (SkillsProposalsListRequest, error) {
	return decodeMethodParams[SkillsProposalsListRequest](params)
}

func DecodeSkillsProposalsInspectParams(params json.RawMessage) (SkillsProposalsInspectRequest, error) {
	return decodeMethodParams[SkillsProposalsInspectRequest](params)
}

func DecodeSkillsProposalsCreateParams(params json.RawMessage) (SkillsProposalsCreateRequest, error) {
	return decodeMethodParams[SkillsProposalsCreateRequest](params)
}

func DecodeSkillsProposalsReviseParams(params json.RawMessage) (SkillsProposalsReviseRequest, error) {
	return decodeMethodParams[SkillsProposalsReviseRequest](params)
}

func DecodeSkillsProposalsIDParams(params json.RawMessage) (SkillsProposalsIDRequest, error) {
	return decodeMethodParams[SkillsProposalsIDRequest](params)
}
