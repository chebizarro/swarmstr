package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"metiq/internal/gateway/methods"
	gatewayprotocol "metiq/internal/gateway/protocol"
	"metiq/internal/gateway/sessioncoord"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

var (
	errSessionSharingUnavailable    = errors.New("session sharing service unavailable")
	errObserverVisibilityRequiresWS = errors.New("sessions.observer.visibility requires a gateway WS connection")
)

// sessionCollabActor derives the collaboration actor for a control call.
// Requests without a WS principal (Nostr control, admin HTTP, internal
// dispatch) are already policy-gated operator surfaces and act as admin.
func sessionCollabActor(ctx context.Context) sessioncoord.Actor {
	principal, ok := gatewayws.PrincipalFromContext(ctx)
	if !ok {
		return sessioncoord.Actor{Admin: true}
	}
	return gatewayPrincipalCollabActor(principal)
}

// gatewayPrincipalCollabActor maps a WS control principal onto the
// collaboration actor model used for sharing roles and event filtering.
func gatewayPrincipalCollabActor(principal gatewayws.ControlPrincipal) sessioncoord.Actor {
	admin := !principal.ScopesEnforced
	if !admin {
		for _, scope := range principal.Scopes {
			if scope == gatewayprotocol.MethodScopeOperatorAdmin {
				admin = true
				break
			}
		}
	}
	return sessioncoord.Actor{Subject: strings.TrimSpace(principal.Subject), Admin: admin}
}

// visibilityScopedSessionMethods lists keyed session mutation methods that
// must pass the collaboration visibility matrix before dispatch. Read-only
// surfaces stay unscoped; sharing/member/suggestion methods authorize inside
// their own handlers.
var visibilityScopedSessionMethods = map[string]bool{
	methods.MethodChatSend:                  true,
	methods.MethodSessionsSend:              true,
	methods.MethodSessionsAbort:             true,
	methods.MethodSessionsPatch:             true,
	methods.MethodSessionsReset:             true,
	methods.MethodSessionsDelete:            true,
	methods.MethodSessionsCompact:           true,
	methods.MethodSessionsCompactionBranch:  true,
	methods.MethodSessionsCompactionRestore: true,
	methods.MethodSessionsBranchesSwitch:    true,
	methods.MethodSessionsRewind:            true,
	methods.MethodSessionsFork:              true,
	methods.MethodSessionsFilesSet:          true,
	methods.MethodSessionsDispatch:          true,
	methods.MethodSessionsReclaim:           true,
}

// sessionMutationKeyFromParams probes the canonical session key parameter
// names used across the v4 method surface.
func sessionMutationKeyFromParams(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var probe struct {
		Key         string `json:"key"`
		SessionKey  string `json:"sessionKey"`
		SessionKeyA string `json:"session_key"`
		SessionID   string `json:"session_id"`
		SessionIDA  string `json:"sessionId"`
		To          string `json:"to"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	for _, candidate := range []string{probe.Key, probe.SessionKey, probe.SessionKeyA, probe.SessionID, probe.SessionIDA, probe.To} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// authorizeSessionMutationVisibility is the central visibility-scoping choke
// point for keyed session mutations. Sessions without sharing state default to
// "shared" and admit every operator.
func (h controlRPCHandler) authorizeSessionMutationVisibility(ctx context.Context, in nostruntime.ControlRPCInbound, method string) error {
	if !visibilityScopedSessionMethods[method] {
		return nil
	}
	coordinator := h.deps.sessionCoordinator
	if coordinator == nil || in.Internal {
		return nil
	}
	key := sessionMutationKeyFromParams(in.Params)
	if key == "" {
		return nil
	}
	return coordinator.AuthorizeSessionMutation(ctx, key, sessionCollabActor(ctx))
}

// dispatchSuggestionText forwards an accepted suggestion into the session via
// the existing sessions.send path as an internal, attributed message.
func (h controlRPCHandler) dispatchSuggestionText(ctx context.Context, sessionKey string, suggestion sessioncoord.Suggestion) error {
	author := suggestion.Author.Label
	if author == "" {
		author = suggestion.Author.ID
	}
	params, err := json.Marshal(map[string]any{
		"to":              sessionKey,
		"text":            "[Suggested by " + author + "] " + suggestion.Text,
		"idempotency_key": "session-suggestion:" + suggestion.ID,
	})
	if err != nil {
		return err
	}
	result, err := h.Handle(ctx, nostruntime.ControlRPCInbound{
		Method:   methods.MethodSessionsSend,
		Params:   params,
		Internal: true,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.Error) != "" {
		return errors.New(result.Error)
	}
	return nil
}

func (h controlRPCHandler) handleSessionCollabRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	_ = cfg
	coordinator := h.deps.sessionCoordinator
	switch method {
	case methods.MethodSessionVisibilitySet, methods.MethodSessionMembersList,
		methods.MethodSessionMembersAdd, methods.MethodSessionMembersRemove,
		methods.MethodSessionsObserverVisibility,
		methods.MethodSessionSuggestionsAdd, methods.MethodSessionSuggestionsList,
		methods.MethodSessionSuggestionsResolve, methods.MethodSessionTyping:
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
	if coordinator == nil {
		return nostruntime.ControlRPCResult{}, true, errSessionSharingUnavailable
	}
	actor := sessionCollabActor(ctx)
	switch method {
	case methods.MethodSessionVisibilitySet:
		req, err := methods.DecodeSessionVisibilitySetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		visibility, err := coordinator.SetVisibility(ctx, req.SessionKey, req.Visibility, actor)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "sessionKey": req.SessionKey, "visibility": visibility}}, true, nil
	case methods.MethodSessionMembersList:
		req, err := methods.DecodeSessionMembersListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := coordinator.Members(ctx, req.SessionKey, actor)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionMembersAdd:
		req, err := methods.DecodeSessionMemberMutateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := coordinator.AddMember(ctx, req.SessionKey, req.IdentityID, actor); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "sessionKey": req.SessionKey, "identityId": req.IdentityID}}, true, nil
	case methods.MethodSessionMembersRemove:
		req, err := methods.DecodeSessionMemberMutateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := coordinator.RemoveMember(ctx, req.SessionKey, req.IdentityID, actor); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "sessionKey": req.SessionKey, "identityId": req.IdentityID}}, true, nil
	case methods.MethodSessionsObserverVisibility:
		req, err := methods.DecodeSessionsObserverVisibilityParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		connectionID, ok := gatewayws.ConnectionIDFromContext(ctx)
		if !ok {
			return nostruntime.ControlRPCResult{}, true, errObserverVisibilityRequiresWS
		}
		coordinator.SetObserverVisibility(connectionID, req.Visible)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, true, nil
	case methods.MethodSessionSuggestionsAdd:
		req, err := methods.DecodeSessionSuggestionsAddParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		suggestion, err := coordinator.AddSuggestion(ctx, req.SessionKey, req.Text, actor)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"suggestion": suggestion}}, true, nil
	case methods.MethodSessionSuggestionsList:
		req, err := methods.DecodeSessionSuggestionsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		role, suggestions, err := coordinator.ListSuggestions(ctx, req.SessionKey, actor)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"role": role, "suggestions": suggestions}}, true, nil
	case methods.MethodSessionSuggestionsResolve:
		req, err := methods.DecodeSessionSuggestionsResolveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		dispatch := func(suggestion sessioncoord.Suggestion) error {
			return h.dispatchSuggestionText(ctx, req.SessionKey, suggestion)
		}
		suggestion, err := coordinator.ResolveSuggestion(ctx, req.SessionKey, req.ID, req.Resolution, actor, dispatch)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"suggestion": suggestion}}, true, nil
	case methods.MethodSessionTyping:
		req, err := methods.DecodeSessionTypingParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		connectionID, _ := gatewayws.ConnectionIDFromContext(ctx)
		broadcast, err := coordinator.UpdateTyping(ctx, sessioncoord.TypingRequest{
			Key:          req.SessionKey,
			SessionID:    req.SessionID,
			ConnectionID: connectionID,
			Typing:       req.Typing,
		}, actor)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "broadcast": broadcast}}, true, nil
	}
	return nostruntime.ControlRPCResult{}, false, nil
}
