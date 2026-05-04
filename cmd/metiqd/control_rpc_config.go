package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"metiq/internal/config"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/policy"
	securitypkg "metiq/internal/security"
	"metiq/internal/store/state"
)

var runtimeConfigCommitMu sync.Mutex

type configMutationDurabilityError struct {
	Stage       string
	Err         error
	RollbackErr error
	Partial     bool
}

func (e *configMutationDurabilityError) Error() string {
	if e == nil {
		return ""
	}
	stage := strings.TrimSpace(e.Stage)
	if stage == "" {
		stage = "unknown"
	}
	if e.RollbackErr != nil {
		return fmt.Sprintf("config mutation durable commit failed at %s: %v; rollback failed: %v; disk may differ from relay/live state", stage, e.Err, e.RollbackErr)
	}
	return fmt.Sprintf("config mutation durable commit failed at %s: %v; file rollback succeeded", stage, e.Err)
}

func (e *configMutationDurabilityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type configMutationCommitRequest struct {
	BaseHash           string
	RestartDelayMS     int
	ExpectedVersion    int
	ExpectedVersionSet bool
	ExpectedEvent      string
	SkipIfUnchanged    bool
	BuildNext          func(current state.ConfigDoc) (state.ConfigDoc, error)
	PrepareNext        func(current, next state.ConfigDoc) (state.ConfigDoc, error)
}

type configMutationCommitResult struct {
	Current        state.ConfigDoc
	Next           state.ConfigDoc
	RuntimeApplied bool
	RestartPending bool
}

func commitRuntimeConfigMutation(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req configMutationCommitRequest) (configMutationCommitResult, error) {
	if docsRepo == nil {
		return configMutationCommitResult{}, fmt.Errorf("config repository is not configured")
	}
	if configState == nil {
		return configMutationCommitResult{}, fmt.Errorf("runtime config state is not configured")
	}
	if req.BuildNext == nil {
		return configMutationCommitResult{}, fmt.Errorf("config mutation builder is not configured")
	}

	result, err := commitRuntimeConfigMutationLocked(ctx, docsRepo, configState, req)
	if err != nil {
		return configMutationCommitResult{}, err
	}
	result.RestartPending = scheduleRestartIfNeeded(result.Current, result.Next, req.RestartDelayMS)
	return result, nil
}

func commitRuntimeConfigMutationLocked(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req configMutationCommitRequest) (configMutationCommitResult, error) {
	runtimeConfigCommitMu.Lock()
	defer runtimeConfigCommitMu.Unlock()

	current := policy.NormalizeConfig(configState.Get())
	if req.ExpectedVersionSet || strings.TrimSpace(req.ExpectedEvent) != "" {
		if err := checkConfigMutationPreconditions(ctx, docsRepo, req); err != nil {
			return configMutationCommitResult{}, err
		}
	}

	next, err := req.BuildNext(current)
	if err != nil {
		return configMutationCommitResult{}, err
	}
	if err := methods.CheckBaseHash(current, req.BaseHash); err != nil {
		return configMutationCommitResult{}, err
	}
	next = policy.NormalizeConfig(next)
	if req.PrepareNext != nil {
		next, err = req.PrepareNext(current, next)
		if err != nil {
			return configMutationCommitResult{}, err
		}
		next = policy.NormalizeConfig(next)
	}
	if err := policy.ValidateConfig(next); err != nil {
		return configMutationCommitResult{}, err
	}
	if req.SkipIfUnchanged && current.Hash() == next.Hash() {
		return configMutationCommitResult{Current: current, Next: next}, nil
	}
	if err := persistRuntimeConfigFile(next); err != nil {
		return configMutationCommitResult{}, err
	}
	if _, err := docsRepo.PutConfig(ctx, next); err != nil {
		rollbackErr := persistRuntimeConfigFile(current)
		return configMutationCommitResult{}, &configMutationDurabilityError{
			Stage:       "repo_persist",
			Err:         err,
			RollbackErr: rollbackErr,
			Partial:     rollbackErr != nil,
		}
	}

	result := configMutationCommitResult{Current: current, Next: next}
	if configState.Get().Hash() != next.Hash() {
		configState.Set(next)
		result.RuntimeApplied = true
	}
	return result, nil
}

func checkConfigMutationPreconditions(ctx context.Context, docsRepo *state.DocsRepository, req configMutationCommitRequest) error {
	expectedEvent := strings.TrimSpace(req.ExpectedEvent)
	current, evt, err := docsRepo.GetConfigWithEvent(ctx)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			if req.ExpectedVersionSet && req.ExpectedVersion == 0 && expectedEvent == "" {
				return nil
			}
			return &methods.PreconditionConflictError{
				Resource:        "config",
				ExpectedVersion: req.ExpectedVersion,
				CurrentVersion:  0,
				ExpectedEvent:   expectedEvent,
			}
		}
		return err
	}
	if req.ExpectedVersionSet {
		if req.ExpectedVersion == 0 || current.Version != req.ExpectedVersion {
			return &methods.PreconditionConflictError{
				Resource:        "config",
				ExpectedVersion: req.ExpectedVersion,
				CurrentVersion:  current.Version,
				ExpectedEvent:   expectedEvent,
				CurrentEvent:    evt.ID,
			}
		}
	}
	if expectedEvent != "" && evt.ID != expectedEvent {
		return &methods.PreconditionConflictError{
			Resource:        "config",
			ExpectedVersion: req.ExpectedVersion,
			CurrentVersion:  current.Version,
			ExpectedEvent:   expectedEvent,
			CurrentEvent:    evt.ID,
		}
	}
	return nil
}

func applyRuntimeConfigReloadIfChanged(configState *runtimeConfigStore, doc state.ConfigDoc, apply func(state.ConfigDoc)) bool {
	if configState == nil || apply == nil {
		return false
	}
	doc = policy.NormalizeConfig(doc)
	runtimeConfigCommitMu.Lock()
	defer runtimeConfigCommitMu.Unlock()
	if configState.Get().Hash() == doc.Hash() {
		return false
	}
	apply(doc)
	return true
}

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
		commit, err := commitRuntimeConfigMutation(ctx, docsRepo, configState, configMutationCommitRequest{
			BaseHash:           req.BaseHash,
			ExpectedVersion:    req.ExpectedVersion,
			ExpectedVersionSet: req.ExpectedVersionSet,
			ExpectedEvent:      req.ExpectedEvent,
			BuildNext: func(current state.ConfigDoc) (state.ConfigDoc, error) {
				return req.Config, nil
			},
			PrepareNext: func(current, next state.ConfigDoc) (state.ConfigDoc, error) {
				newVersion := 1
				if req.ExpectedVersionSet && req.ExpectedVersion > 0 {
					newVersion = req.ExpectedVersion + 1
				}
				next.Version = newVersion
				return next, nil
			},
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": commit.Next.Hash(), "restart_pending": commit.RestartPending}}, true, nil
	case methods.MethodConfigSet:
		req, err := methods.DecodeConfigSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		commit, err := commitRuntimeConfigMutation(ctx, docsRepo, configState, configMutationCommitRequest{
			BaseHash: req.BaseHash,
			BuildNext: func(current state.ConfigDoc) (state.ConfigDoc, error) {
				return methods.ApplyConfigSet(current, req.Key, req.Value)
			},
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": commit.Next.Hash(), "restart_pending": commit.RestartPending}}, true, nil
	case methods.MethodConfigApply:
		req, err := methods.DecodeConfigApplyParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		commit, err := commitRuntimeConfigMutation(ctx, docsRepo, configState, configMutationCommitRequest{
			BaseHash:       req.BaseHash,
			RestartDelayMS: req.RestartDelayMS,
			BuildNext: func(current state.ConfigDoc) (state.ConfigDoc, error) {
				return req.Config, nil
			},
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": commit.Next.Hash(), "restart_pending": commit.RestartPending}}, true, nil
	case methods.MethodConfigPatch:
		req, err := methods.DecodeConfigPatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		commit, err := commitRuntimeConfigMutation(ctx, docsRepo, configState, configMutationCommitRequest{
			BaseHash:       req.BaseHash,
			RestartDelayMS: req.RestartDelayMS,
			BuildNext: func(current state.ConfigDoc) (state.ConfigDoc, error) {
				return methods.ApplyConfigPatch(current, req.Patch)
			},
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": commit.Next.Hash(), "restart_pending": commit.RestartPending}}, true, nil
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
