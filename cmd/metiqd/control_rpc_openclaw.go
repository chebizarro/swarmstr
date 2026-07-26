package main

// control_rpc_openclaw.go — control-RPC handlers for the OpenClaw-branded
// control-surface compat aliases (swarmstr-i413).
//
// These four methods are OpenClaw's own names for data-plane functionality that
// Metiq already implements under its native method names. Each is a thin alias:
// it validates the OpenClaw-facing params, translates them to the native param
// shape, and re-dispatches through the normal handler (Internal=true) so the
// native handler's logic and response shape are reused verbatim — real
// functionality backed by a real subsystem, no fabricated stub.
//
//   - openclaw.chat          -> chat.send            (native DM transport)
//   - openclaw.chat.history  -> chat.history         (durable docs/transcript)
//   - openclaw.changes.list  -> sessions.files.list  (touched-files review)
//   - openclaw.approval.list -> approval.list        (durable approval ledger)
//
// The five openclaw.setup.* onboarding/activation methods
// (setup.detect/activate/auth.start/prepare.start/verify) are intentionally NOT
// handled here. They onboard/activate an OpenClaw application install, which has
// no meaningful equivalent for a nostr-key-native daemon that is not OpenClaw;
// they remain an honest UNAVAILABLE accepted deviation (unregistered → the
// gateway returns "method not found"). See docs/parity/gateway-method-parity.json
// notes and the product follow-up swarmstr-nuqy.

import (
	"context"
	"encoding/json"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) handleOpenclawRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	_ = cfg
	switch method {
	case methods.MethodOpenclawChat:
		req, err := methods.DecodeOpenclawChatParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.reDispatchOpenclaw(ctx, in, methods.MethodChatSend, req.ToNative())

	case methods.MethodOpenclawChatHistory:
		req, err := methods.DecodeOpenclawChatHistoryParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.reDispatchOpenclaw(ctx, in, methods.MethodChatHistory, req.ToNative())

	case methods.MethodOpenclawChangesList:
		req, err := methods.DecodeOpenclawChangesListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.reDispatchOpenclaw(ctx, in, methods.MethodSessionsFilesList, req.ToNative())

	case methods.MethodOpenclawApprovalList:
		req, err := methods.DecodeOpenclawApprovalListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.reDispatchOpenclaw(ctx, in, methods.MethodApprovalList, req.ToNative())

	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}

// reDispatchOpenclaw marshals the translated native params and re-dispatches the
// request to the native target method through the normal handler. Internal=true
// bypasses a redundant control-policy re-check: the openclaw.* alias has already
// been authorized at the same scope as its native target (see descriptors.go),
// so the target's own scope gate is not weakened. The native result is returned
// verbatim, preserving the OpenClaw-modeled response shape clients expect.
func (h controlRPCHandler) reDispatchOpenclaw(ctx context.Context, in nostruntime.ControlRPCInbound, target string, nativeParams any) (nostruntime.ControlRPCResult, bool, error) {
	params, err := json.Marshal(nativeParams)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	inner := nostruntime.ControlRPCInbound{
		Method:        target,
		Params:        params,
		FromPubKey:    in.FromPubKey,
		Authenticated: in.Authenticated,
		Internal:      true,
	}
	result, err := h.Handle(ctx, inner)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return result, true, nil
}
