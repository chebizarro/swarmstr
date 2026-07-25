package main

// control_rpc_gateway_lifecycle.go — control-RPC handlers for the gateway
// lifecycle surface (gateway.restart.preflight / gateway.restart.request,
// swarmstr-iiot). Mirrors OpenClaw src/gateway/server-methods/restart.ts.
//
// gateway.suspend.prepare / gateway.suspend.status / gateway.suspend.resume are
// intentionally NOT handled here: the metiqd daemon has no cooperative
// suspend/resume (host-hibernation) machinery, so exposing them would be a fake
// stub. They stay an honest UNAVAILABLE gap (unregistered -> parity status
// missing). The shared `gateway` triage prefix is locked to the
// core-runtime/implement category, so a per-method accepted-deviation is not
// expressible in the parity matrix; tracked by a follow-up swarmstr issue.

import (
	"context"
	"fmt"
	"os"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) handleGatewayLifecycleRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	_ = ctx
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
	}
	return nostruntime.ControlRPCResult{}, false, nil
}
