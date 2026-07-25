package main

// control_rpc_node_surface.go — control-RPC handlers for the node.* plugin/
// skills surface (swarmstr-kmhu, BUCKET 3):
//
//	node.pluginSurface.refresh — ask a node to re-scan its plugin surface
//	node.pluginTools.update    — push an updated plugin-tool surface to a node
//	node.skills.update         — push an updated skills surface to a node
//
// These mirror OpenClaw's node-scoped surface ops. Unlike the gateway-local
// plugin.surface.refresh (which is deferred — it depends on a plugin UI-surface
// registry swarmstr does not yet have, swarmstr-5p0v), the node-scoped ops do
// NOT need that registry: the gateway's job is only to DELIVER the command to
// the node, and the node owns (and applies) its own surface.
//
// Delivery uses the durable node pending-command queue (internal/gateway/
// nodepending) — the exact channel node.invoke / node.pending.enqueue use — and
// is gated on the target node not being revoked (unpaired) via withActiveNode.
// A NIP-17 control DM nudges the node to pull. This is real, side-effecting
// backing (durable enqueue + active-node precondition), not a stub; the node
// applies the queued command when it next pulls.

import (
	"context"
	"encoding/json"
	"fmt"

	"metiq/internal/gateway/methods"
	"metiq/internal/gateway/nodepending"
	nostruntime "metiq/internal/nostr/runtime"
)

// enqueueNodeSurfaceCommand durably enqueues one surface command to a node
// (gated on the node being active/non-revoked) and nudges it via control DM.
func (h controlRPCHandler) enqueueNodeSurfaceCommand(ctx context.Context, nodeID, command string, args map[string]any, idempotencyKey string, ttlMS int) (map[string]any, error) {
	if h.deps.nodePending == nil {
		return nil, fmt.Errorf("node pending queue unavailable")
	}
	pending, err := withActiveNode(h.deps.nodeInvocations, nodeID, func() (map[string]any, error) {
		return h.deps.nodePending.Enqueue(nodepending.EnqueueRequest{
			NodeID:         nodeID,
			Command:        command,
			Args:           args,
			IdempotencyKey: idempotencyKey,
			TTLMS:          ttlMS,
		})
	})
	if err != nil {
		return nil, err
	}
	// Nudge the node to pull, mirroring node.invoke's control-DM dispatch.
	if payload, marshalErr := json.Marshal(map[string]any{"type": command, "args": args}); marshalErr == nil {
		// Detach from the RPC request context so the best-effort wake nudge is
		// not cancelled the instant the handler returns; sendControlDM applies
		// its own timeout. Delivery itself is guaranteed by the durable queue.
		go sendControlDM(context.WithoutCancel(ctx), nodeID, string(payload))
	}
	return map[string]any{
		"ok":       true,
		"node_id":  nodeID,
		"command":  command,
		"delivery": "node_pending_queue",
		"pending":  pending,
	}, nil
}

func (h controlRPCHandler) handleNodeSurfaceRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string) (nostruntime.ControlRPCResult, bool, error) {
	switch method {
	case methods.MethodNodePluginSurfaceRefresh:
		req, err := methods.DecodeNodePluginSurfaceRefreshParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := h.enqueueNodeSurfaceCommand(ctx, req.NodeID, "pluginSurface.refresh", nil, req.IdempotencyKey, req.TTLMS)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil

	case methods.MethodNodePluginToolsUpdate:
		req, err := methods.DecodeNodePluginToolsUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := h.enqueueNodeSurfaceCommand(ctx, req.NodeID, "pluginTools.update", map[string]any{"tools": req.Tools}, req.IdempotencyKey, req.TTLMS)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil

	case methods.MethodNodeSkillsUpdate:
		req, err := methods.DecodeNodeSkillsUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := h.enqueueNodeSurfaceCommand(ctx, req.NodeID, "skills.update", map[string]any{"skills": req.Skills}, req.IdempotencyKey, req.TTLMS)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil

	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
