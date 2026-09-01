package main

import (
	"context"
	"encoding/json"
	"testing"

	"metiq/internal/gateway/methods"
	proposalpkg "metiq/internal/gateway/skillproposal"
	nostruntime "metiq/internal/nostr/runtime"
	skillspkg "metiq/internal/skills"
)

func TestSkillProposalEvaluatePersistsRevisionBoundEvent(t *testing.T) {
	cfg, proposalID := setupRevisionWorkspace(t)
	rec, err := skillspkg.NewProposalStore(cfg, "main").Load(proposalID)
	if err != nil {
		t.Fatal(err)
	}
	h := newControlRPCHandler(controlRPCDeps{})
	call := func(method string, params map[string]any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(params)
		result, handled, err := h.handleSkillProposalEventsRPC(context.Background(), nostruntime.ControlRPCInbound{Method: method, Params: raw, Internal: true}, method, cfg)
		if err != nil || !handled {
			t.Fatalf("%s handled=%v err=%v", method, handled, err)
		}
		return result.Result.(map[string]any)
	}
	evaluated := call(methods.MethodSkillsProposalsEvaluate, map[string]any{
		"proposalId": proposalID, "expectedRevisionHash": rec.DraftHash, "correlationId": "corr-1",
	})
	evaluation := evaluated["evaluation"].(proposalpkg.Evaluation)
	if evaluation.RevisionHash != rec.DraftHash || len(evaluation.Outcomes) != 1 || evaluation.Outcomes[0].Status != "completed" {
		t.Fatalf("evaluation=%+v", evaluation)
	}
	listed := call(methods.MethodSkillsProposalsEventsList, map[string]any{"proposalId": proposalID})
	events := listed["events"].([]proposalpkg.Event)
	if len(events) != 1 || events[0].Type != "evaluation_completed" || events[0].Evaluation == nil || events[0].CorrelationID != "corr-1" {
		t.Fatalf("events=%+v", events)
	}

	stalePrefix := "a"
	if rec.DraftHash[0] == 'a' {
		stalePrefix = "b"
	}
	raw, _ := json.Marshal(map[string]any{"proposalId": proposalID, "expectedRevisionHash": stalePrefix + rec.DraftHash[1:]})
	if _, _, err := h.handleSkillProposalEventsRPC(context.Background(), nostruntime.ControlRPCInbound{Method: methods.MethodSkillsProposalsEvaluate, Params: raw, Internal: true}, methods.MethodSkillsProposalsEvaluate, cfg); err == nil {
		t.Fatal("expected stale revision error")
	}
}
