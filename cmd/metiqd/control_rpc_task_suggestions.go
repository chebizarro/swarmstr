package main

import (
	"context"
	"encoding/json"
	"fmt"

	"metiq/internal/gateway/methods"
	tasksuggestionspkg "metiq/internal/gateway/tasksuggestions"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// Task suggestion RPC handlers (WS-A/A7 taskSuggestions.* slice). The
// registry is process-local and ephemeral (ids vanish on restart, OpenClaw
// parity); live task.suggestion events keep open clients in sync. Accepting a
// suggestion seeds a new session through the shared sessions.create path —
// Metiq deviation: sessions.create does not auto-start a run, so acceptance
// completes once the session entry exists.

func (h controlRPCHandler) taskSuggestionRegistry() (*tasksuggestionspkg.Registry, error) {
	if h.deps.taskSuggestions == nil {
		return nil, fmt.Errorf("task suggestion surface is not available")
	}
	return h.deps.taskSuggestions, nil
}

func emitTaskSuggestionCreated(suggestion tasksuggestionspkg.Suggestion) {
	emitControlWSEvent(gatewayws.EventTaskSuggestion, gatewayws.TaskSuggestionPayload{
		Action:     "created",
		Suggestion: suggestion,
	})
}

func emitTaskSuggestionResolved(taskID, resolution string) {
	emitControlWSEvent(gatewayws.EventTaskSuggestion, gatewayws.TaskSuggestionPayload{
		Action:     "resolved",
		TaskID:     taskID,
		Resolution: resolution,
	})
}

func (h controlRPCHandler) handleTaskSuggestionsList(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeTaskSuggestionsListParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	registry, err := h.taskSuggestionRegistry()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	suggestions := registry.List(req.SessionKey, req.AgentID)
	return nostruntime.ControlRPCResult{Result: map[string]any{"suggestions": suggestions}}, true, nil
}

func (h controlRPCHandler) handleTaskSuggestionsCreate(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeTaskSuggestionsCreateParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	registry, err := h.taskSuggestionRegistry()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	created := registry.Create(tasksuggestionspkg.CreateParams{
		Title:      req.Title,
		Prompt:     req.Prompt,
		Tldr:       req.Tldr,
		CWD:        req.CWD,
		SessionKey: req.SessionKey,
		AgentID:    req.AgentID,
	})
	if created.Full {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("task suggestion registry is busy")
	}
	for _, evictedID := range created.EvictedPendingTaskIDs {
		emitTaskSuggestionResolved(evictedID, "expired")
	}
	emitTaskSuggestionCreated(created.Suggestion)
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"taskId":     created.Suggestion.ID,
		"suggestion": created.Suggestion,
	}}, true, nil
}

// createSuggestedTaskSession seeds the accepted suggestion's session through
// the shared sessions.create dispatch so labeling, parent linkage, and
// session-store bookkeeping stay in one place.
func (h controlRPCHandler) createSuggestedTaskSession(ctx context.Context, suggestion tasksuggestionspkg.Suggestion, cfg state.ConfigDoc) (string, error) {
	// sessions.create decodes raw params (no alias normalization pass), so
	// field names follow SessionsCreateRequest's camelCase tags.
	params, err := json.Marshal(map[string]any{
		"agentId":          suggestion.AgentID,
		"label":            suggestion.Title,
		"task":             suggestion.Prompt,
		"parentSessionKey": suggestion.SessionKey,
		"cwd":              suggestion.CWD,
	})
	if err != nil {
		return "", fmt.Errorf("encode suggested task session params: %w", err)
	}
	result, handled, err := h.handleSessionRPC(ctx, nostruntime.ControlRPCInbound{
		Method:   methods.MethodSessionsCreate,
		Params:   params,
		Internal: true,
	}, methods.MethodSessionsCreate, cfg)
	if err != nil {
		return "", err
	}
	if !handled {
		return "", fmt.Errorf("sessions.create did not respond")
	}
	payload, ok := result.Result.(map[string]any)
	if !ok {
		return "", fmt.Errorf("sessions.create returned no session key")
	}
	key, _ := payload["key"].(string)
	if key == "" {
		return "", fmt.Errorf("sessions.create returned no session key")
	}
	return key, nil
}

func (h controlRPCHandler) handleTaskSuggestionsAccept(ctx context.Context, in nostruntime.ControlRPCInbound, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeTaskSuggestionsAcceptParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	registry, err := h.taskSuggestionRegistry()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	acceptance := registry.BeginAcceptance(req.TaskID)
	switch acceptance.Status {
	case "accepted":
		// Retried accepts return the same idempotent result.
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"taskId": req.TaskID,
			"key":    acceptance.SessionKey,
		}}, true, nil
	case "claimed":
		// fall through to session creation below
	default:
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("task suggestion cannot be accepted: %s", acceptance.Status)
	}
	key, createErr := h.createSuggestedTaskSession(ctx, acceptance.Suggestion, cfg)
	if createErr != nil {
		if restored, ok := registry.CancelAcceptance(req.TaskID); ok {
			emitTaskSuggestionCreated(restored)
		}
		return nostruntime.ControlRPCResult{}, true, createErr
	}
	registry.CompleteAcceptance(req.TaskID, key)
	emitTaskSuggestionResolved(req.TaskID, "accepted")
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"taskId": req.TaskID,
		"key":    key,
	}}, true, nil
}

func (h controlRPCHandler) handleTaskSuggestionsDismiss(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeTaskSuggestionsDismissParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	registry, err := h.taskSuggestionRegistry()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	dismissed := registry.Dismiss(req.TaskID)
	if dismissed {
		emitTaskSuggestionResolved(req.TaskID, "dismissed")
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"taskId":    req.TaskID,
		"dismissed": dismissed,
	}}, true, nil
}
