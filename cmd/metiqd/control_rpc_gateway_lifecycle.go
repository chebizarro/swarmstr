package main

// control_rpc_gateway_lifecycle.go — control-RPC handlers for the gateway
// lifecycle surface (gateway.restart.* + gateway.suspend.*, swarmstr-iiot /
// swarmstr-ngrd). Mirrors OpenClaw src/gateway/server-methods/restart.ts and
// suspend.ts (backed by infra/gateway-suspend-coordinator.ts).
//
// gateway.suspend.prepare/status/resume drive the cooperative suspend
// coordinator (internal/gateway/suspend): a durable suspension-id lifecycle
// (idle→preparing→suspended→resuming→idle) that closes a shared accepting-work
// gate so cooperative background dispatchers and interactive model/task turns
// stop accepting NEW work while a suspension is active. Work admitted before
// prepare drains under leases and is never hard-killed.

import (
	"context"
	"fmt"
	"os"

	"metiq/internal/gateway/methods"
	suspendpkg "metiq/internal/gateway/suspend"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// suspendInFlight builds the quiesce-accounting snapshot from the same readiness
// inspector gateway.restart.preflight uses (in-flight agent runs + active
// sessions). This is work the daemon reports but does not kill on suspend.
func (h controlRPCHandler) suspendInFlight() suspendpkg.InFlight {
	var inFlight suspendpkg.InFlight
	if h.deps.agentJobs != nil {
		inFlight.AgentRuns = h.deps.agentJobs.ActiveRuns()
	}
	if h.deps.sessionStore != nil {
		inFlight.ActiveSessions = len(h.deps.sessionStore.List())
	}
	if h.deps.suspendCoordinator != nil {
		inFlight.InteractiveLeases = h.deps.suspendCoordinator.ActiveWork()
	}
	return inFlight
}

// suspendResult renders the wire response shared by prepare/status/resume.
func suspendResult(rec suspendpkg.Record, inFlight suspendpkg.InFlight, pausedWorkers []string) map[string]any {
	suspended := rec.State == suspendpkg.StateSuspended
	active := rec.State != suspendpkg.StateIdle
	result := map[string]any{
		"ok":    true,
		"state": rec.State,
		// Full-daemon cooperative suspension: background dispatchers pause and
		// interactive model turns/task enqueues are atomically admission-gated.
		// Existing leases drain naturally; no in-flight work is hard-killed.
		"scope":             "full-daemon",
		"interactiveActive": rec.State == suspendpkg.StateIdle,
		"inFlight": map[string]any{
			"agentRuns":         inFlight.AgentRuns,
			"sessions":          inFlight.ActiveSessions,
			"interactiveLeases": inFlight.InteractiveLeases,
		},
		// quiesced == suspended AND no in-flight interactive work remaining. The
		// background dispatchers are paused regardless (gate closed); this reports
		// whether the interactive surface has drained.
		"quiesced": suspended && inFlight.Empty(),
		"pid":      os.Getpid(),
	}
	if rec.SuspensionID != "" {
		result["suspensionId"] = rec.SuspensionID
	}
	if rec.SinceMs != 0 {
		result["since"] = rec.SinceMs
	}
	if rec.Reason != "" {
		result["reason"] = rec.Reason
	}
	// pausedWorkers is meaningful as soon as prepare closes the admission gate.
	if active && len(pausedWorkers) > 0 {
		result["pausedWorkers"] = pausedWorkers
	} else {
		result["pausedWorkers"] = []string{}
	}
	return result
}

func (h controlRPCHandler) handleGatewayLifecycleRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	_ = cfg
	switch method {
	case methods.MethodGatewayRestartPreflight:
		if _, err := methods.DecodeGatewayRestartPreflightParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		inFlight := 0
		if h.deps.agentJobs != nil {
			inFlight = h.deps.agentJobs.ActiveRuns()
		}
		activeSessions := 0
		if h.deps.sessionStore != nil {
			activeSessions = len(h.deps.sessionStore.List())
		}
		result := map[string]any{
			"ok":             true,
			"ready":          inFlight == 0,
			"inFlightRuns":   inFlight,
			"activeSessions": activeSessions,
			"pid":            os.Getpid(),
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil

	case methods.MethodGatewayRestartRequest:
		req, err := methods.DecodeGatewayRestartRequestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.restartCh == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("restart scheduler unavailable")
		}
		// Non-blocking send: the scheduler channel is buffered and coalesces
		// duplicate requests (scheduleRestartIfNeeded semantics).
		scheduled := false
		select {
		case h.deps.restartCh <- req.DelayMS:
			scheduled = true
		default:
			// A restart is already queued; report it as accepted (idempotent).
			scheduled = true
		}
		result := map[string]any{
			"ok":        true,
			"scheduled": scheduled,
			"delayMs":   req.DelayMS,
			"pid":       os.Getpid(),
		}
		if req.Reason != "" {
			result["reason"] = req.Reason
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil

	case methods.MethodGatewaySuspendPrepare:
		req, err := methods.DecodeGatewaySuspendPrepareParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.suspendCoordinator == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("suspend coordinator unavailable")
		}
		rec, err := h.deps.suspendCoordinator.Prepare(req.Reason)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result := suspendResult(rec, h.suspendInFlight(), h.deps.suspendCoordinator.PausableWorkers())
		return nostruntime.ControlRPCResult{Result: result}, true, nil

	case methods.MethodGatewaySuspendStatus:
		if _, err := methods.DecodeGatewaySuspendStatusParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.suspendCoordinator == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("suspend coordinator unavailable")
		}
		rec := h.deps.suspendCoordinator.State()
		result := suspendResult(rec, h.suspendInFlight(), h.deps.suspendCoordinator.PausableWorkers())
		return nostruntime.ControlRPCResult{Result: result}, true, nil

	case methods.MethodGatewaySuspendResume:
		req, err := methods.DecodeGatewaySuspendResumeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.suspendCoordinator == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("suspend coordinator unavailable")
		}
		rec, err := h.deps.suspendCoordinator.Resume(req.SuspensionID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result := suspendResult(rec, h.suspendInFlight(), h.deps.suspendCoordinator.PausableWorkers())
		if h.deps.services != nil && h.deps.services.tasks.runner != nil {
			if resumeErr := h.deps.services.tasks.runner.ResumeQueued(ctx); resumeErr != nil {
				result["queuedResumeWarning"] = resumeErr.Error()
			}
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	}
	return nostruntime.ControlRPCResult{}, false, nil
}
