package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	environmentspkg "metiq/internal/gateway/environments"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/sandbox"
	"metiq/internal/store/state"
)

// environments.* handlers (WS-A/A7 deferred slice). Long-lived isolated
// execution environments are provisioned from `extra.environments.profiles`
// config entries (each entry uses the same shape as `extra.sandbox`) and
// managed by internal/gateway/environments over the docker sandbox subsystem.
// Environment creation fails closed when docker is required but unavailable.

// environmentProviderID labels the only provisioning substrate available to
// gateway-managed environments today.
const environmentProviderID = "sandbox:docker"

func rawEnvironmentProfiles(cfg state.ConfigDoc) map[string]any {
	if cfg.Extra == nil {
		return nil
	}
	rawEnvs, ok := cfg.Extra["environments"].(map[string]any)
	if !ok {
		return nil
	}
	profiles, ok := rawEnvs["profiles"].(map[string]any)
	if !ok {
		return nil
	}
	return profiles
}

// environmentProfiles lists configured profiles without exposing provider
// settings, sorted by id for deterministic output.
func environmentProfiles(cfg state.ConfigDoc) []environmentspkg.Profile {
	raw := rawEnvironmentProfiles(cfg)
	out := make([]environmentspkg.Profile, 0, len(raw))
	for id := range raw {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		out = append(out, environmentspkg.Profile{ID: id, ProviderID: environmentProviderID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// environmentProfileConfig resolves one profile to a sandbox config.
func environmentProfileConfig(cfg state.ConfigDoc, profileID string) (sandbox.Config, error) {
	raw := rawEnvironmentProfiles(cfg)
	entry, ok := raw[profileID].(map[string]any)
	if !ok {
		return sandbox.Config{}, fmt.Errorf("unknown environment profile %q", profileID)
	}
	return sandboxConfigFromMap(entry), nil
}

func (h controlRPCHandler) environmentManager() (*environmentspkg.Manager, error) {
	if h.deps.environments == nil {
		return nil, fmt.Errorf("environments are not available")
	}
	return h.deps.environments, nil
}

func (h controlRPCHandler) handleEnvironmentsList(_ context.Context, in nostruntime.ControlRPCInbound, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	if _, err := methods.DecodeEnvironmentsListParams(in.Params); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	environments := []environmentspkg.Summary{environmentspkg.GatewayEnvironment()}
	if h.deps.environments != nil {
		environments = append(environments, h.deps.environments.List()...)
	}
	result := map[string]any{"environments": environments}
	if profiles := environmentProfiles(cfg); len(profiles) > 0 {
		result["profiles"] = profiles
	}
	return nostruntime.ControlRPCResult{Result: result}, true, nil
}

func (h controlRPCHandler) handleEnvironmentsStatus(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeEnvironmentsStatusParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if req.EnvironmentID == environmentspkg.GatewayEnvironment().ID {
		return nostruntime.ControlRPCResult{Result: environmentspkg.GatewayEnvironment()}, true, nil
	}
	mgr, err := h.environmentManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	summary, ok := mgr.Status(req.EnvironmentID)
	if !ok {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown environment %q", req.EnvironmentID)
	}
	return nostruntime.ControlRPCResult{Result: summary}, true, nil
}

func (h controlRPCHandler) handleEnvironmentsCreate(ctx context.Context, in nostruntime.ControlRPCInbound, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeEnvironmentsCreateParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	mgr, err := h.environmentManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	sandboxCfg, err := environmentProfileConfig(cfg, req.ProfileID)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	summary, err := mgr.Create(ctx, environmentspkg.CreateRequest{
		ProfileID:      req.ProfileID,
		IdempotencyKey: req.IdempotencyKey,
		Config:         sandboxCfg,
	})
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: summary}, true, nil
}

func (h controlRPCHandler) handleEnvironmentsDestroy(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeEnvironmentsDestroyParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	mgr, err := h.environmentManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	summary, err := mgr.Destroy(ctx, req.EnvironmentID, req.Force)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: summary}, true, nil
}
