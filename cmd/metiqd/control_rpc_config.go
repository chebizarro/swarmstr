package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"metiq/internal/config"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/policy"
	securitypkg "metiq/internal/security"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) handleConfigRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	dmBus := h.deps.dmBus
	controlBus := h.deps.controlBus
	docsRepo := h.deps.docsRepo
	configState := h.deps.configState

	_ = dmBus
	_ = controlBus
	switch method {
	case methods.MethodConfigGet:
		redacted := config.Redact(cfg)
		// Include base_hash so OpenClaw clients can use optimistic-lock semantics on mutations.
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"config":    redacted,
			"hash":      cfg.Hash(),
			"base_hash": cfg.Hash(),
		}}, true, nil
	case methods.MethodRelayPolicyGet:
		dmRelays := []string{}
		controlRelays := []string{}
		if dmBus != nil {
			dmRelays = dmBus.Relays()
		}
		if controlBus != nil {
			controlRelays = controlBus.Relays()
		}
		return nostruntime.ControlRPCResult{Result: methods.RelayPolicyResponse{
			ReadRelays:           append([]string{}, cfg.Relays.Read...),
			WriteRelays:          append([]string{}, cfg.Relays.Write...),
			RuntimeDMRelays:      dmRelays,
			RuntimeControlRelays: controlRelays,
		}}, true, nil
	case methods.MethodListGet:
		req, err := methods.DecodeListGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		list, err := docsRepo.GetList(ctx, req.Name)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: list}, true, nil
	case methods.MethodListPut:
		req, err := methods.DecodeListPutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req.ExpectedVersionSet || req.ExpectedEvent != "" {
			current, evt, err := docsRepo.GetListWithEvent(ctx, req.Name)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					if req.ExpectedVersionSet && req.ExpectedVersion == 0 && req.ExpectedEvent == "" {
						goto controlListPreconditionsSatisfied
					}
					return nostruntime.ControlRPCResult{}, true, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  0,
						ExpectedEvent:   req.ExpectedEvent,
					}
				}
				return nostruntime.ControlRPCResult{}, true, err
			}
			if req.ExpectedVersionSet {
				if req.ExpectedVersion == 0 {
					return nostruntime.ControlRPCResult{}, true, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				} else if current.Version != req.ExpectedVersion {
					return nostruntime.ControlRPCResult{}, true, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				}
			}
			if req.ExpectedEvent != "" && evt.ID != req.ExpectedEvent {
				return nostruntime.ControlRPCResult{}, true, &methods.PreconditionConflictError{
					Resource:        "list:" + req.Name,
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
		}
	controlListPreconditionsSatisfied:
		newVersion := 1
		if req.ExpectedVersionSet && req.ExpectedVersion > 0 {
			newVersion = req.ExpectedVersion + 1
		}
		if _, err := docsRepo.PutList(ctx, req.Name, state.ListDoc{Version: newVersion, Name: req.Name, Items: req.Items}); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, true, nil
	case methods.MethodConfigPut:
		req, err := methods.DecodeConfigPutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req.ExpectedVersionSet || req.ExpectedEvent != "" {
			current, evt, err := docsRepo.GetConfigWithEvent(ctx)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					if req.ExpectedVersionSet && req.ExpectedVersion == 0 && req.ExpectedEvent == "" {
						goto controlConfigPreconditionsSatisfied
					}
					return nostruntime.ControlRPCResult{}, true, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  0,
						ExpectedEvent:   req.ExpectedEvent,
					}
				}
				return nostruntime.ControlRPCResult{}, true, err
			}
			if req.ExpectedVersionSet {
				if req.ExpectedVersion == 0 {
					return nostruntime.ControlRPCResult{}, true, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				} else if current.Version != req.ExpectedVersion {
					return nostruntime.ControlRPCResult{}, true, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				}
			}
			if req.ExpectedEvent != "" && evt.ID != req.ExpectedEvent {
				return nostruntime.ControlRPCResult{}, true, &methods.PreconditionConflictError{
					Resource:        "config",
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
		}
	controlConfigPreconditionsSatisfied:
		req.Config = policy.NormalizeConfig(req.Config)
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := policy.ValidateConfig(req.Config); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		newVersion := 1
		if req.ExpectedVersionSet && req.ExpectedVersion > 0 {
			newVersion = req.ExpectedVersion + 1
		}
		req.Config.Version = newVersion
		if err := persistRuntimeConfigFile(req.Config); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if _, err := docsRepo.PutConfig(ctx, req.Config); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		configState.Set(req.Config)
		restartPending := scheduleRestartIfNeeded(cfg, req.Config, 0)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": req.Config.Hash(), "restart_pending": restartPending}}, true, nil
	case methods.MethodConfigSet:
		req, err := methods.DecodeConfigSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		next, err := methods.ApplyConfigSet(cfg, req.Key, req.Value)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		next = policy.NormalizeConfig(next)
		if err := policy.ValidateConfig(next); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := persistRuntimeConfigFile(next); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if _, err := docsRepo.PutConfig(ctx, next); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		configState.Set(next)
		restartPending := scheduleRestartIfNeeded(cfg, next, 0)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": next.Hash(), "restart_pending": restartPending}}, true, nil
	case methods.MethodConfigApply:
		req, err := methods.DecodeConfigApplyParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		next := policy.NormalizeConfig(req.Config)
		if err := policy.ValidateConfig(next); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := persistRuntimeConfigFile(next); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if _, err := docsRepo.PutConfig(ctx, next); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		configState.Set(next)
		restartPending := scheduleRestartIfNeeded(cfg, next, req.RestartDelayMS)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": next.Hash(), "restart_pending": restartPending}}, true, nil
	case methods.MethodConfigPatch:
		req, err := methods.DecodeConfigPatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		next, err := methods.ApplyConfigPatch(cfg, req.Patch)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		next = policy.NormalizeConfig(next)
		if err := policy.ValidateConfig(next); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := persistRuntimeConfigFile(next); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if _, err := docsRepo.PutConfig(ctx, next); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		configState.Set(next)
		restartPending := scheduleRestartIfNeeded(cfg, next, req.RestartDelayMS)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": next.Hash(), "restart_pending": restartPending}}, true, nil
	case methods.MethodConfigSchema:
		return nostruntime.ControlRPCResult{Result: methods.ConfigSchema(cfg)}, true, nil
	case methods.MethodConfigSchemaLookup:
		// Look up a schema property by dot-notation path (e.g. "agents.model").
		// Returns the full schema when path is empty.
		path := ""
		if in.Params != nil {
			var p struct {
				Path  string `json:"path"`
				Field string `json:"field"`
			}
			if err := json.Unmarshal(in.Params, &p); err == nil {
				path = strings.TrimSpace(p.Path)
				if path == "" {
					path = strings.TrimSpace(p.Field)
				}
			}
		}
		full := methods.ConfigSchema(cfg)
		if path == "" {
			return nostruntime.ControlRPCResult{Result: full}, true, nil
		}
		// Walk the schema map by dot-separated segments.
		var cur any = full
		for _, seg := range strings.Split(path, ".") {
			m, ok := cur.(map[string]any)
			if !ok {
				cur = nil
				break
			}
			if v, found := m[seg]; found {
				cur = v
			} else if props, hasProps := m["properties"].(map[string]any); hasProps {
				cur = props[seg]
			} else {
				cur = nil
				break
			}
		}
		if cur == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("schema path %q not found", path)
		}
		return nostruntime.ControlRPCResult{Result: cur}, true, nil
	case methods.MethodSecurityAudit:
		// Run security posture checks and return findings.
		report := securitypkg.Audit(securitypkg.AuditOptions{
			ConfigDoc: &cfg,
		})
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"findings": report.Findings,
			"critical": report.Critical,
			"warn":     report.Warn,
			"info":     report.Info,
		}}, true, nil

		// ── ACP (Agent Control Protocol) ────────────────────────────────────────
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
