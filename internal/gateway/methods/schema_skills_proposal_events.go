package methods

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type SkillsProposalEventsListRequest struct {
	AgentID       string `json:"agent_id,omitempty"`
	ProposalID    string `json:"proposalId,omitempty"`
	AfterSequence int64  `json:"afterSequence,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type SkillsProposalEvaluateRequest struct {
	AgentID              string `json:"agent_id,omitempty"`
	ProposalID           string `json:"proposalId"`
	ExpectedRevisionHash string `json:"expectedRevisionHash,omitempty"`
	CorrelationID        string `json:"correlationId,omitempty"`
}

func (r SkillsProposalEventsListRequest) Normalize() (SkillsProposalEventsListRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.ProposalID = strings.TrimSpace(r.ProposalID)
	if r.AfterSequence < 0 {
		return r, fmt.Errorf("skills.proposals.events.list: afterSequence must not be negative")
	}
	if r.Limit <= 0 {
		r.Limit = 100
	}
	if r.Limit > 200 {
		return r, fmt.Errorf("skills.proposals.events.list: limit must not exceed 200")
	}
	return r, nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (r SkillsProposalEvaluateRequest) Normalize() (SkillsProposalEvaluateRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.ProposalID = strings.TrimSpace(r.ProposalID)
	r.ExpectedRevisionHash = strings.ToLower(strings.TrimSpace(r.ExpectedRevisionHash))
	r.CorrelationID = strings.TrimSpace(r.CorrelationID)
	if r.ProposalID == "" {
		return r, fmt.Errorf("skills.proposals.evaluate: proposalId is required")
	}
	if r.ExpectedRevisionHash != "" && !validSHA256(r.ExpectedRevisionHash) {
		return r, fmt.Errorf("skills.proposals.evaluate: expectedRevisionHash must be a SHA-256 hex digest")
	}
	if len(r.CorrelationID) > 256 {
		return r, fmt.Errorf("skills.proposals.evaluate: correlationId exceeds 256 bytes")
	}
	return r, nil
}

func DecodeSkillsProposalEventsListParams(params json.RawMessage) (SkillsProposalEventsListRequest, error) {
	return decodeMethodParams[SkillsProposalEventsListRequest](params)
}

func DecodeSkillsProposalEvaluateParams(params json.RawMessage) (SkillsProposalEvaluateRequest, error) {
	return decodeMethodParams[SkillsProposalEvaluateRequest](params)
}
