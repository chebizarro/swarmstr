package main

import (
	"context"
	"fmt"

	"metiq/internal/gateway/methods"
	questionspkg "metiq/internal/gateway/questions"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
)

// Question RPC handlers (WS-A/A7 question.* slice). The manager owns the
// durable pending/answered/cancelled/expired lifecycle; handlers translate
// wire params, broadcast question.requested / question.resolved events, and
// let question.waitAnswer park an agent turn on the answer exactly like
// exec.approval.waitDecision.

func (h controlRPCHandler) questionManager() (*questionspkg.Manager, error) {
	if h.deps.questions == nil {
		return nil, fmt.Errorf("question surface is not available")
	}
	return h.deps.questions, nil
}

func questionFromInputParam(q methods.QuestionInputParam) questionspkg.Question {
	options := make([]questionspkg.Option, 0, len(q.Options))
	for _, option := range q.Options {
		options = append(options, questionspkg.Option{Label: option.Label, Description: option.Description})
	}
	return questionspkg.Question{
		QuestionID:  q.QuestionID,
		Header:      q.Header,
		Question:    q.Question,
		Options:     options,
		MultiSelect: q.MultiSelect,
		IsOther:     q.IsOther,
		IsSecret:    q.IsSecret,
	}
}

func emitQuestionResolved(rec questionspkg.Record) {
	payload := gatewayws.QuestionResolvedPayload{ID: rec.ID, Status: rec.Status}
	if rec.Status == questionspkg.StatusAnswered {
		payload.Answers = rec.Answers
	}
	emitControlWSEvent(gatewayws.EventQuestionResolved, payload)
}

func (h controlRPCHandler) handleQuestionRequest(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeQuestionRequestParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	mgr, err := h.questionManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	qs := make([]questionspkg.Question, 0, len(req.Questions))
	for _, q := range req.Questions {
		qs = append(qs, questionFromInputParam(q))
	}
	rec, err := mgr.Request(questionspkg.RequestParams{
		ID:         req.ID,
		Questions:  qs,
		AgentID:    req.AgentID,
		SessionKey: req.SessionKey,
		TimeoutMS:  req.TimeoutMS,
	})
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	emitControlWSEvent(gatewayws.EventQuestionRequested, rec)
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"id":          rec.ID,
		"expiresAtMs": rec.ExpiresAtMs,
	}}, true, nil
}

func (h controlRPCHandler) handleQuestionWaitAnswer(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeQuestionWaitAnswerParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	mgr, err := h.questionManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	result, err := mgr.WaitAnswer(ctx, req.ID, req.TimeoutMS)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: result}, true, nil
}

func (h controlRPCHandler) handleQuestionResolve(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeQuestionResolveParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	mgr, err := h.questionManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	var rec questionspkg.Record
	var result questionspkg.ResolveResult
	if req.Cancel {
		rec, result, err = mgr.Cancel(req.ID, req.ResolvedBy)
	} else {
		rec, result, err = mgr.Resolve(req.ID, questionspkg.AnswerSet{Answers: req.Answers.Answers}, req.ResolvedBy)
	}
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	emitQuestionResolved(rec)
	return nostruntime.ControlRPCResult{Result: result}, true, nil
}

func (h controlRPCHandler) handleQuestionGet(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeQuestionGetParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	mgr, err := h.questionManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	rec, err := mgr.Get(req.ID)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"question": rec}}, true, nil
}

func (h controlRPCHandler) handleQuestionList(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	if _, err := methods.DecodeQuestionListParams(in.Params); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	mgr, err := h.questionManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"questions": mgr.List()}}, true, nil
}
