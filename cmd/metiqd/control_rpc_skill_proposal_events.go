package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"metiq/internal/gateway/methods"
	proposalpkg "metiq/internal/gateway/skillproposal"
	nostruntime "metiq/internal/nostr/runtime"
	skillspkg "metiq/internal/skills"
	"metiq/internal/store/state"
)

func proposalEventStore(cfg state.ConfigDoc, agentID string) *proposalpkg.Store {
	return proposalpkg.NewStore(skillspkg.ResolveAgentWorkspaceDir(cfg, defaultAgentID(agentID)))
}

func appendSkillProposalEvent(cfg state.ConfigDoc, agentID string, rec skillspkg.ProposalRecord, eventType, correlationID string, payload map[string]any, evaluation *proposalpkg.Evaluation) error {
	_, err := proposalEventStore(cfg, agentID).Append(proposalpkg.Event{
		ProposalID: rec.ID, ProposedVersion: rec.ProposedVersion, RevisionHash: rec.DraftHash,
		Type: eventType, Actor: proposalpkg.Actor{Type: "gateway"}, CorrelationID: correlationID,
		Payload: payload, Evaluation: evaluation,
	})
	return err
}

func proposalEvaluationFromScan(rec skillspkg.ProposalRecord, correlationID string) (proposalpkg.Evaluation, error) {
	findings := make([]proposalpkg.Finding, 0, len(rec.Scan.Findings))
	decision := "pass"
	for _, finding := range rec.Scan.Findings {
		severity := "warn"
		if string(finding.Severity) == "error" {
			severity = "critical"
			decision = "block"
		} else if decision == "pass" {
			decision = "revise"
		}
		findings = append(findings, proposalpkg.Finding{RuleID: finding.Code, Severity: severity, Message: finding.Message, File: finding.Field})
	}
	summary := fmt.Sprintf("Metiq static proposal scan: %s (%d errors, %d warnings)", rec.Scan.State, rec.Scan.Counts.Error, rec.Scan.Counts.Warning)
	outcome := proposalpkg.EvaluationOutcome{
		PluginID: "metiq.core", EvaluatorID: "skill-static-scan", Status: "completed",
		Result: proposalpkg.EvaluationResult{
			Summary: summary, Findings: findings, EvaluatorVer: "1", Mode: "static",
			Decision: decision, Metrics: map[string]any{"errors": rec.Scan.Counts.Error, "warnings": rec.Scan.Counts.Warning},
		},
	}
	if decision != "pass" {
		outcome.Result.DecisionReason = summary
	}
	return proposalpkg.NewEvaluation(rec.ProposedVersion, rec.DraftHash, correlationID, []proposalpkg.EvaluationOutcome{outcome})
}

func (h controlRPCHandler) handleSkillProposalEventsRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	if method != methods.MethodSkillsProposalsEventsList && method != methods.MethodSkillsProposalsEvaluate {
		return nostruntime.ControlRPCResult{}, false, nil
	}
	switch method {
	case methods.MethodSkillsProposalsEventsList:
		req, err := methods.DecodeSkillsProposalEventsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := isKnownAgentID(ctx, h.deps.docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		events, next, err := proposalEventStore(cfg, req.AgentID).List(req.ProposalID, req.AfterSequence, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result := map[string]any{"events": events}
		if next != nil {
			result["nextSequence"] = *next
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSkillsProposalsEvaluate:
		req, err := methods.DecodeSkillsProposalEvaluateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := isKnownAgentID(ctx, h.deps.docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		rec, err := skillspkg.NewProposalStore(cfg, defaultAgentID(req.AgentID)).Load(req.ProposalID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("proposal %q not found", req.ProposalID)
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req.ExpectedRevisionHash != "" && !strings.EqualFold(req.ExpectedRevisionHash, rec.DraftHash) {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("proposal revision changed (expected %s, current %s); inspect and retry", req.ExpectedRevisionHash, rec.DraftHash)
		}
		evaluation, err := proposalEvaluationFromScan(rec, req.CorrelationID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := appendSkillProposalEvent(cfg, req.AgentID, rec, "evaluation_completed", req.CorrelationID, nil, &evaluation); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		recordRaw, err := json.Marshal(rec)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var record map[string]any
		if err := json.Unmarshal(recordRaw, &record); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		record["evaluation"] = evaluation
		return nostruntime.ControlRPCResult{Result: map[string]any{"record": record, "evaluation": evaluation}}, true, nil
	}
	return nostruntime.ControlRPCResult{}, false, nil
}
