package main

import (
	"context"
	"fmt"

	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
)

func isSetupMethod(method string) bool {
	switch method {
	case methods.MethodSetupDetect, methods.MethodSetupAuthStart, methods.MethodSetupPrepareStart, methods.MethodSetupVerify, methods.MethodSetupActivate:
		return true
	default:
		return false
	}
}

// handleSetupRPC is deliberately reached before normal operator-pubkey policy:
// no operator identity exists yet. Its stronger bootstrap boundary requires a
// trusted loopback-WebSocket context marker and the durable setup token.
func (h controlRPCHandler) handleSetupRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string) (nostruntime.ControlRPCResult, error) {
	if !gatewayws.LocalConnectionFromContext(ctx) {
		return nostruntime.ControlRPCResult{}, fmt.Errorf("setup methods require a loopback gateway WebSocket")
	}
	if _, ok := gatewayws.ConnectionIDFromContext(ctx); !ok {
		return nostruntime.ControlRPCResult{}, fmt.Errorf("setup methods require a direct gateway WebSocket request")
	}
	if in.EventID != "" || in.RequestID != "" || in.RelayURL != "" {
		return nostruntime.ControlRPCResult{}, fmt.Errorf("setup methods are unavailable over the Nostr RPC bus")
	}
	if in.Internal {
		return nostruntime.ControlRPCResult{}, fmt.Errorf("setup methods are unavailable to internal redispatch")
	}
	if h.deps.onboarding == nil {
		return nostruntime.ControlRPCResult{}, fmt.Errorf("setup unavailable")
	}

	switch method {
	case methods.MethodSetupDetect:
		req, err := methods.DecodeSetupTokenParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("setup.detect: invalid params: %w", err)
		}
		result, err := h.deps.onboarding.Detect(req.SetupToken)
		return nostruntime.ControlRPCResult{Result: result}, err
	case methods.MethodSetupAuthStart:
		req, err := methods.DecodeSetupAuthStartParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("setup.auth.start: invalid params: %w", err)
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		result, err := h.deps.onboarding.AuthStart(ctx, req)
		return nostruntime.ControlRPCResult{Result: result}, err
	case methods.MethodSetupPrepareStart:
		req, err := methods.DecodeSetupPrepareStartParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("setup.prepare.start: invalid params: %w", err)
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		result, err := h.deps.onboarding.Prepare(req)
		return nostruntime.ControlRPCResult{Result: result}, err
	case methods.MethodSetupVerify:
		req, err := methods.DecodeSetupTokenParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("setup.verify: invalid params: %w", err)
		}
		result, err := h.deps.onboarding.Verify(ctx, req.SetupToken)
		return nostruntime.ControlRPCResult{Result: result}, err
	case methods.MethodSetupActivate:
		req, err := methods.DecodeSetupTokenParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("setup.activate: invalid params: %w", err)
		}
		result, err := h.deps.onboarding.Activate(req.SetupToken)
		return nostruntime.ControlRPCResult{Result: result}, err
	default:
		return nostruntime.ControlRPCResult{}, fmt.Errorf("unknown setup method %q", method)
	}
}
