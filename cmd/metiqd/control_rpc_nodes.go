package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/gateway/nodepending"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
)

func (h controlRPCHandler) handleNodeRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string) (nostruntime.ControlRPCResult, bool, error) {
	docsRepo := h.deps.docsRepo
	configState := h.deps.configState

	_ = docsRepo
	_ = configState
	switch method {
	case methods.MethodNodePairRequest:
		req, err := methods.DecodeNodePairRequestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodePairRequest(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		requestID := ""
		if id, ok := out["request_id"].(string); ok {
			requestID = id
		}
		controlServices.emitWSEvent(gatewayws.EventNodePairRequested, gatewayws.NodePairRequestedPayload{
			TS:        time.Now().UnixMilli(),
			RequestID: requestID,
			Label:     req.DisplayName,
		})
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodePairList:
		req, err := methods.DecodeNodePairListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodePairList(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodePairApprove:
		req, err := methods.DecodeNodePairApproveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodePairApprove(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		nodeID := ""
		approvalToken := ""
		if node, ok := out["node"].(map[string]any); ok {
			if id, ok := node["node_id"].(string); ok {
				nodeID = id
			}
			if tok, ok := node["token"].(string); ok {
				approvalToken = tok
			}
		}
		controlServices.emitWSEvent(gatewayws.EventNodePairResolved, gatewayws.NodePairResolvedPayload{
			TS:        time.Now().UnixMilli(),
			RequestID: req.RequestID,
			NodeID:    nodeID,
			Decision:  "approved",
		})
		// Notify the node via NIP-17 DM if node_id looks like a Nostr pubkey.
		if nodeID != "" && approvalToken != "" {
			go sendControlDM(ctx, nodeID, fmt.Sprintf(`{"type":"pair.approved","request_id":%q,"token":%q}`, req.RequestID, approvalToken))
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodePairReject:
		req, err := methods.DecodeNodePairRejectParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodePairReject(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		nodeID := ""
		if id, ok := out["node_id"].(string); ok {
			nodeID = id
		}
		controlServices.emitWSEvent(gatewayws.EventNodePairResolved, gatewayws.NodePairResolvedPayload{
			TS:        time.Now().UnixMilli(),
			RequestID: req.RequestID,
			NodeID:    nodeID,
			Decision:  "rejected",
		})
		// Notify the node via NIP-17 DM if node_id looks like a Nostr pubkey.
		if nodeID != "" {
			go sendControlDM(ctx, nodeID, fmt.Sprintf(`{"type":"pair.rejected","request_id":%q}`, req.RequestID))
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodePairVerify:
		req, err := methods.DecodeNodePairVerifyParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodePairVerify(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodDevicePairList:
		req, err := methods.DecodeDevicePairListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyDevicePairList(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodDevicePairApprove:
		req, err := methods.DecodeDevicePairApproveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyDevicePairApprove(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		deviceID := ""
		label := ""
		if device, ok := out["device"].(map[string]any); ok {
			if id, ok := device["id"].(string); ok {
				deviceID = id
			}
			if l, ok := device["label"].(string); ok {
				label = l
			}
		}
		controlServices.emitWSEvent(gatewayws.EventDevicePairResolved, gatewayws.DevicePairResolvedPayload{
			TS:       time.Now().UnixMilli(),
			DeviceID: deviceID,
			Label:    label,
			Decision: "approved",
		})
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodDevicePairReject:
		req, err := methods.DecodeDevicePairRejectParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyDevicePairReject(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		deviceID := ""
		if device, ok := out["device"].(map[string]any); ok {
			if id, ok := device["id"].(string); ok {
				deviceID = id
			}
		}
		controlServices.emitWSEvent(gatewayws.EventDevicePairResolved, gatewayws.DevicePairResolvedPayload{
			TS:       time.Now().UnixMilli(),
			DeviceID: deviceID,
			Decision: "rejected",
		})
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodDevicePairRemove:
		req, err := methods.DecodeDevicePairRemoveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyDevicePairRemove(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodDeviceTokenRotate:
		req, err := methods.DecodeDeviceTokenRotateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyDeviceTokenRotate(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodDeviceTokenRevoke:
		req, err := methods.DecodeDeviceTokenRevokeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyDeviceTokenRevoke(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodeList:
		req, err := methods.DecodeNodeListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodeList(configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodeDescribe:
		req, err := methods.DecodeNodeDescribeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodeDescribe(configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodeRename:
		req, err := methods.DecodeNodeRenameParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodeRename(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodeCanvasCapabilityRefresh:
		req, err := methods.DecodeNodeCanvasCapabilityRefreshParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodeCanvasCapabilityRefresh(configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodeInvoke:
		req, err := methods.DecodeNodeInvokeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodeInvoke(h.deps.nodeInvocations, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		// Dispatch the invocation to the target node via NIP-17 DM if its
		// node_id looks like a Nostr pubkey (hex or npub).
		if req.NodeID != "" {
			runID, _ := out["run_id"].(string)
			payload, _ := json.Marshal(map[string]any{
				"type":    "node.invoke",
				"run_id":  runID,
				"command": req.Command,
				"args":    req.Args,
			})
			go sendControlDM(ctx, req.NodeID, string(payload))
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodeEvent:
		req, err := methods.DecodeNodeEventParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodeEvent(h.deps.nodeInvocations, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodeResult, methods.MethodNodeInvokeResult:
		req, err := methods.DecodeNodeResultParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyNodeResult(h.deps.nodeInvocations, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodePendingEnqueue:
		req, err := methods.DecodeNodePendingEnqueueParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := h.deps.nodePending.Enqueue(nodepending.EnqueueRequest{NodeID: req.NodeID, Command: req.Command, Args: req.Args, IdempotencyKey: req.IdempotencyKey, TTLMS: req.TTLMS})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodePendingPull:
		req, err := methods.DecodeNodePendingPullParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := h.deps.nodePending.Pull(req.NodeID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodePendingAck:
		req, err := methods.DecodeNodePendingAckParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := h.deps.nodePending.Ack(nodepending.AckRequest{NodeID: req.NodeID, IDs: req.IDs})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodNodePendingDrain:
		req, err := methods.DecodeNodePendingDrainParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := h.deps.nodePending.Drain(nodepending.DrainRequest{NodeID: req.NodeID, MaxItems: req.MaxItems})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodCanvasGet:
		var req methods.CanvasGetRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("invalid params: %w", err)
		}
		c := h.deps.canvasHost.GetCanvas(req.ID)
		if c == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("canvas %q not found", req.ID)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"canvas": c}}, true, nil
	case methods.MethodCanvasList:
		canvases := h.deps.canvasHost.ListCanvases()
		return nostruntime.ControlRPCResult{Result: map[string]any{"canvases": canvases, "count": len(canvases)}}, true, nil
	case methods.MethodCanvasUpdate:
		var req methods.CanvasUpdateRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("invalid params: %w", err)
		}
		if err := h.deps.canvasHost.UpdateCanvas(req.ID, req.ContentType, req.Data); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "canvas_id": req.ID}}, true, nil
	case methods.MethodCanvasDelete:
		var req methods.CanvasDeleteRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("invalid params: %w", err)
		}
		removed := h.deps.canvasHost.DeleteCanvas(req.ID)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "removed": removed, "canvas_id": req.ID}}, true, nil
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
