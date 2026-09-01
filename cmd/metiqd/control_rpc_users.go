package main

// control_rpc_users.go — control-RPC handlers for the durable user-profile
// surface (users.list / users.self / users.linkEmail / users.setDisplayName /
// users.setAvatar, swarmstr-5lln). Backed by internal/gateway/userprofiles.
//
// Metiq deviation (parity triage nostr-user-identity, accepted-deviation):
// users.self resolves the caller's nostr pubkey (ControlRPCInbound.FromPubKey)
// to a durable profile rather than an authenticated e-mail account. The wire
// projection matches OpenClaw's UserProfile shape.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"metiq/internal/gateway/methods"
	userprofilespkg "metiq/internal/gateway/userprofiles"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) handleUsersRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	_ = ctx
	switch method {
	case methods.MethodUsersList, methods.MethodUsersSelf, methods.MethodUsersLinkEmail,
		methods.MethodUsersSetDisplayName, methods.MethodUsersSetAvatar,
		methods.MethodUsersPrefsGet, methods.MethodUsersPrefsSet, methods.MethodUsersSetRole:
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}

	store := h.deps.userProfiles
	if store == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("user profile store unavailable")
	}

	switch method {
	case methods.MethodUsersList:
		if _, err := methods.DecodeUsersListParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"profiles": store.List()}}, true, nil

	case methods.MethodUsersSelf:
		if _, err := methods.DecodeUsersSelfParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		identity := in.FromPubKey
		if identity == "" {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("users.self requires an authenticated caller")
		}
		profile, err := store.EnsureForIdentity(identity)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"profile": profile}}, true, nil

	case methods.MethodUsersLinkEmail:
		req, err := methods.DecodeUsersLinkEmailParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		profile, err := store.LinkEmail(req.Email, req.TargetProfileID)
		if err != nil {
			return usersProfileError(err, req.TargetProfileID)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"profile": profile}}, true, nil

	case methods.MethodUsersSetDisplayName:
		req, err := methods.DecodeUsersSetDisplayNameParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		profile, err := store.SetDisplayName(req.ProfileID, req.DisplayName)
		if err != nil {
			return usersProfileError(err, req.ProfileID)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"profile": profile}}, true, nil

	case methods.MethodUsersPrefsGet:
		req, err := methods.DecodeUsersPrefsGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		identity := strings.TrimSpace(in.FromPubKey)
		if identity == "" {
			return nostruntime.ControlRPCResult{Result: map[string]any{"status": "no_durable_identity"}}, true, nil
		}
		entries, err := store.Preferences(identity, req.Keys)
		if errors.Is(err, userprofilespkg.ErrNotFound) {
			return nostruntime.ControlRPCResult{Result: map[string]any{"status": "no_durable_identity"}}, true, nil
		}
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"status": "ok", "entries": entries}}, true, nil

	case methods.MethodUsersPrefsSet:
		req, err := methods.DecodeUsersPrefsSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		identity := strings.TrimSpace(in.FromPubKey)
		if identity == "" {
			return nostruntime.ControlRPCResult{Result: map[string]any{"status": "no_durable_identity"}}, true, nil
		}
		entries, err := store.SetPreferences(identity, req.Entries)
		if errors.Is(err, userprofilespkg.ErrNotFound) {
			return nostruntime.ControlRPCResult{Result: map[string]any{"status": "no_durable_identity"}}, true, nil
		}
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"status": "ok", "entries": entries}}, true, nil

	case methods.MethodUsersSetRole:
		req, err := methods.DecodeUsersSetRoleParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req.Role != nil && !configuredGatewayRole(cfg, *req.Role) {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown gateway role %q", *req.Role)
		}
		profile, err := store.SetRole(req.ProfileID, req.Role)
		if err != nil {
			return usersProfileError(err, req.ProfileID)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"profile": profile}}, true, nil

	case methods.MethodUsersSetAvatar:
		req, err := methods.DecodeUsersSetAvatarParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		avatar, err := req.Avatar()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		profile, err := store.SetAvatar(req.ProfileID, avatar, req.Mime)
		if err != nil {
			return usersProfileError(err, req.ProfileID)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"profile": profile}}, true, nil
	}
	return nostruntime.ControlRPCResult{}, false, nil
}

func configuredGatewayRole(cfg state.ConfigDoc, role string) bool {
	gateway, ok := cfg.Extra["gateway"].(map[string]any)
	if !ok {
		return false
	}
	roles, ok := gateway["roles"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = roles[strings.TrimSpace(role)]
	return ok
}

func usersProfileError(err error, id string) (nostruntime.ControlRPCResult, bool, error) {
	if errors.Is(err, userprofilespkg.ErrNotFound) {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("user profile %q not found", id)
	}
	return nostruntime.ControlRPCResult{}, true, err
}
