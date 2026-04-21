package main

// main_ops.go — Node invocation, cron scheduling, exec approvals, sandbox
// execution, secrets management, and wizard operations.
//
// Extracted from main.go to reduce god-file size. All functions remain in
// package main and reference the same globals/helpers as before.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"metiq/internal/cron"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	"metiq/internal/sandbox"
	"metiq/internal/store/state"
)

// ---------------------------------------------------------------------------
// Node invocation
// ---------------------------------------------------------------------------

func applyNodeInvoke(reg *nodeInvocationRegistry, req methods.NodeInvokeRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("node invoke runtime not configured")
	}
	rec := reg.Begin(req)
	return map[string]any{
		"ok":         true,
		"run_id":     rec.RunID,
		"node_id":    rec.NodeID,
		"command":    rec.Command,
		"status":     rec.Status,
		"created_at": rec.CreatedAt,
	}, nil
}

func applyNodeEvent(reg *nodeInvocationRegistry, req methods.NodeEventRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("node invoke runtime not configured")
	}
	rec, err := reg.AddEvent(req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":         true,
		"run_id":     rec.RunID,
		"node_id":    rec.NodeID,
		"status":     rec.Status,
		"updated_at": rec.UpdatedAt,
		"events":     rec.Events,
	}, nil
}

func applyNodeResult(reg *nodeInvocationRegistry, req methods.NodeResultRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("node invoke runtime not configured")
	}
	rec, err := reg.SetResult(req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":         true,
		"run_id":     rec.RunID,
		"node_id":    rec.NodeID,
		"status":     rec.Status,
		"result":     rec.Result,
		"error":      rec.Error,
		"updated_at": rec.UpdatedAt,
	}, nil
}

func applyCronList(reg *cronRegistry, req methods.CronListRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	jobs := reg.List(req.Limit)
	return map[string]any{"jobs": jobs, "count": len(jobs)}, nil
}

func applyCronStatus(reg *cronRegistry, req methods.CronStatusRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	job, ok := reg.Status(req.ID)
	if !ok {
		return nil, state.ErrNotFound
	}
	return map[string]any{"job": job}, nil
}

func applyCronAdd(reg *cronRegistry, req methods.CronAddRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	if _, err := cron.Parse(req.Schedule); err != nil {
		return nil, fmt.Errorf("invalid cron schedule %q: %w", req.Schedule, err)
	}
	job := reg.Add(req)
	return map[string]any{"ok": true, "job": job}, nil
}

func applyCronUpdate(reg *cronRegistry, req methods.CronUpdateRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	if req.Schedule != "" {
		if _, err := cron.Parse(req.Schedule); err != nil {
			return nil, fmt.Errorf("invalid cron schedule %q: %w", req.Schedule, err)
		}
	}
	job, err := reg.Update(req)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "job": job}, nil
}

func applyCronRemove(reg *cronRegistry, req methods.CronRemoveRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	if err := reg.Remove(req.ID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": req.ID, "removed": true}, nil
}

func applyCronRun(reg *cronRegistry, req methods.CronRunRequest) (map[string]any, error) {
	if controlServices == nil {
		return nil, fmt.Errorf("daemon services not initialized")
	}
	return controlServices.applyCronRun(reg, req)
}

func (s *daemonServices) applyCronRun(reg *cronRegistry, req methods.CronRunRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	s.emitWSEvent(gatewayws.EventCronTick, gatewayws.CronTickPayload{
		TS:    time.Now().UnixMilli(),
		JobID: req.ID,
	})
	started := time.Now()
	run, err := reg.Run(req.ID)
	if err != nil {
		return nil, err
	}
	s.emitWSEvent(gatewayws.EventCronResult, gatewayws.CronResultPayload{
		TS:         time.Now().UnixMilli(),
		JobID:      req.ID,
		Succeeded:  run.Status == "done",
		DurationMS: time.Since(started).Milliseconds(),
	})
	return map[string]any{"ok": true, "run": run}, nil
}

func applyCronRuns(reg *cronRegistry, req methods.CronRunsRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	runs := reg.Runs(req.ID, req.Limit)
	return map[string]any{"runs": runs, "count": len(runs)}, nil
}

func applyExecApprovalsGet(reg *execApprovalsRegistry, _ methods.ExecApprovalsGetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	approvals := reg.GetGlobal()
	return map[string]any{"approvals": approvals, "count": len(approvals)}, nil
}

func applyExecApprovalsSet(reg *execApprovalsRegistry, req methods.ExecApprovalsSetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	approvals := reg.SetGlobal(req.Approvals)
	return map[string]any{"ok": true, "approvals": approvals, "count": len(approvals)}, nil
}

func applyExecApprovalsNodeGet(reg *execApprovalsRegistry, req methods.ExecApprovalsNodeGetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	approvals := reg.GetNode(req.NodeID)
	return map[string]any{"node_id": req.NodeID, "approvals": approvals, "count": len(approvals)}, nil
}

func applyExecApprovalsNodeSet(reg *execApprovalsRegistry, req methods.ExecApprovalsNodeSetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	approvals := reg.SetNode(req.NodeID, req.Approvals)
	return map[string]any{"ok": true, "node_id": req.NodeID, "approvals": approvals, "count": len(approvals)}, nil
}

func applyExecApprovalRequest(reg *execApprovalsRegistry, req methods.ExecApprovalRequestRequest) (map[string]any, error) {
	if controlServices == nil {
		return nil, fmt.Errorf("daemon services not initialized")
	}
	return controlServices.applyExecApprovalRequest(reg, req)
}

func (s *daemonServices) applyExecApprovalRequest(reg *execApprovalsRegistry, req methods.ExecApprovalRequestRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	rec := reg.Request(req)
	s.emitWSEvent(gatewayws.EventExecApprovalRequested, gatewayws.ExecApprovalRequestedPayload{
		TS:        time.Now().UnixMilli(),
		ID:        rec.ID,
		NodeID:    rec.NodeID,
		Command:   rec.Command,
		ExpiresAt: rec.ExpiresAt,
	})
	return map[string]any{"id": rec.ID, "status": "accepted", "requested": rec}, nil
}

func applyExecApprovalWaitDecision(ctx context.Context, reg *execApprovalsRegistry, req methods.ExecApprovalWaitDecisionRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	rec, resolved, err := reg.WaitForDecision(ctx, req.ID, req.TimeoutMS)
	if err != nil {
		return nil, err
	}
	if resolved {
		return map[string]any{"ok": true, "id": rec.ID, "resolved": true, "decision": rec.Decision, "record": rec}, nil
	}
	if rec.ExpiresAt > 0 && time.Now().UnixMilli() > rec.ExpiresAt {
		return map[string]any{"ok": false, "id": rec.ID, "resolved": false, "expired": true, "record": rec}, nil
	}
	if ctx.Err() != nil {
		return map[string]any{"ok": false, "id": rec.ID, "resolved": false, "cancelled": true, "record": rec}, nil
	}
	return map[string]any{"ok": true, "id": rec.ID, "resolved": false, "timed_out": true, "record": rec}, nil
}

func applyExecApprovalResolve(reg *execApprovalsRegistry, req methods.ExecApprovalResolveRequest) (map[string]any, error) {
	if controlServices == nil {
		return nil, fmt.Errorf("daemon services not initialized")
	}
	return controlServices.applyExecApprovalResolve(reg, req)
}

func (s *daemonServices) applyExecApprovalResolve(reg *execApprovalsRegistry, req methods.ExecApprovalResolveRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	rec, err := reg.Resolve(req)
	if err != nil {
		return nil, err
	}
	s.emitWSEvent(gatewayws.EventExecApprovalResolved, gatewayws.ExecApprovalResolvedPayload{
		TS:       time.Now().UnixMilli(),
		ID:       rec.ID,
		Decision: rec.Decision,
		NodeID:   rec.NodeID,
	})
	return map[string]any{"ok": true, "id": rec.ID, "decision": rec.Decision, "resolved": rec}, nil
}

func applySandboxRun(ctx context.Context, configState *runtimeConfigStore, req methods.SandboxRunRequest) (map[string]any, error) {
	if len(req.Cmd) == 0 {
		return nil, fmt.Errorf("sandbox.run: cmd is required")
	}

	// Build sandbox config from daemon config + request overrides.
	cfg := sandbox.Config{}
	daemonCfg := configState.Get()
	if daemonCfg.Extra != nil {
		if rawSandbox, ok := daemonCfg.Extra["sandbox"].(map[string]any); ok {
			cfg.Driver = getString(rawSandbox, "driver")
			cfg.MemoryLimit = getString(rawSandbox, "memory_limit")
			cfg.CPULimit = getString(rawSandbox, "cpu_limit")
			cfg.DockerImage = getString(rawSandbox, "docker_image")
			if v, ok := rawSandbox["timeout_s"].(float64); ok {
				cfg.TimeoutSeconds = int(v)
			}
			if v, ok := rawSandbox["network_disabled"].(bool); ok {
				cfg.NetworkDisabled = v
			}
		}
	}
	// Request overrides.
	if req.Driver != "" {
		cfg.Driver = req.Driver
	}
	if req.TimeoutSeconds > 0 {
		cfg.TimeoutSeconds = req.TimeoutSeconds
	}

	runner, err := sandbox.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("sandbox.run: %w", err)
	}

	result, err := runner.Run(ctx, req.Cmd, req.Env, req.Workdir)
	if err != nil {
		return nil, fmt.Errorf("sandbox.run: %w", err)
	}
	return map[string]any{
		"ok":        true,
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
		"timed_out": result.TimedOut,
		"driver":    result.Driver,
	}, nil
}

func applySecretsReload(req methods.SecretsReloadRequest) (map[string]any, error) {
	if controlServices == nil {
		return nil, fmt.Errorf("daemon services not initialized")
	}
	return controlServices.applySecretsReload(req)
}

func (s *daemonServices) applySecretsReload(_ methods.SecretsReloadRequest) (map[string]any, error) {
	if s.handlers.secretsStore == nil {
		return map[string]any{"ok": true, "count": 0, "warningCount": 0, "warnings": []string{}}, nil
	}
	count, warnings := s.handlers.secretsStore.Reload()
	return map[string]any{
		"ok":           true,
		"count":        count,
		"warningCount": len(warnings),
		"warnings":     warnings,
	}, nil
}

func applySecretsResolve(req methods.SecretsResolveRequest) (map[string]any, error) {
	if controlServices == nil {
		return nil, fmt.Errorf("daemon services not initialized")
	}
	return controlServices.applySecretsResolve(req)
}

func (s *daemonServices) applySecretsResolve(req methods.SecretsResolveRequest) (map[string]any, error) {
	assignments := make([]map[string]any, 0, len(req.TargetIDs))
	var diagnostics []string
	var inactive []string

	for _, id := range req.TargetIDs {
		entry := map[string]any{
			"path":         id,
			"pathSegments": strings.Split(id, "."),
		}
		if s.handlers.secretsStore == nil {
			entry["value"] = nil
			entry["found"] = false
			inactive = append(inactive, id)
		} else {
			v, found := s.handlers.secretsStore.Resolve(id)
			if found {
				// Never log the actual secret value — only indicate presence.
				entry["found"] = true
				entry["value"] = v // caller sees value; we redact in logs
			} else {
				entry["found"] = false
				entry["value"] = nil
				diagnostics = append(diagnostics, "unresolved ref: "+id)
				inactive = append(inactive, id)
			}
		}
		assignments = append(assignments, entry)
	}

	return map[string]any{
		"ok":               true,
		"assignments":      assignments,
		"diagnostics":      diagnostics,
		"inactiveRefPaths": inactive,
	}, nil
}

func wizardStepToMap(s *wizardStep) map[string]any {
	if s == nil {
		return nil
	}
	m := map[string]any{
		"id":       s.ID,
		"type":     s.Type,
		"prompt":   s.Prompt,
		"required": s.Required,
		"secret":   s.Secret,
	}
	if len(s.Options) > 0 {
		m["options"] = s.Options
	}
	if s.Default != "" {
		m["default"] = s.Default
	}
	return m
}

func applyWizardStart(reg *wizardRegistry, req methods.WizardStartRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("wizard runtime not configured")
	}
	rec, step := reg.Start(req)
	out := map[string]any{
		"session_id": rec.SessionID,
		"sessionId":  rec.SessionID,
		"status":     rec.Status,
		"mode":       rec.Mode,
		"done":       false,
	}
	if step != nil {
		out["step"] = wizardStepToMap(step)
	}
	return out, nil
}

func applyWizardNext(reg *wizardRegistry, req methods.WizardNextRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("wizard runtime not configured")
	}
	rec, step, done, err := reg.Next(req)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"session_id": rec.SessionID,
		"sessionId":  rec.SessionID,
		"status":     rec.Status,
		"done":       done,
	}
	if step != nil {
		out["step"] = wizardStepToMap(step)
	}
	if done && len(rec.Input) > 0 {
		out["result"] = rec.Input
	}
	return out, nil
}

func applyWizardCancel(reg *wizardRegistry, req methods.WizardCancelRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("wizard runtime not configured")
	}
	rec, err := reg.Cancel(req)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": rec.Status, "error": rec.Error}, nil
}

func applyWizardStatus(reg *wizardRegistry, req methods.WizardStatusRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("wizard runtime not configured")
	}
	rec, err := reg.Status(req)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"session_id": rec.SessionID,
		"sessionId":  rec.SessionID,
		"status":     rec.Status,
		"mode":       rec.Mode,
		"step":       rec.Step,
		"error":      rec.Error,
	}
	step := currentWizardStep(rec)
	if step != nil {
		out["currentStep"] = wizardStepToMap(step)
	}
	return out, nil
}
