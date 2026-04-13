package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchNodes(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodNodePairRequest:
		req, err := methods.DecodeNodePairRequestParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePairRequest == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pairing provider not configured")
		}
		out, err := opts.NodePairRequest(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodePairList:
		req, err := methods.DecodeNodePairListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePairList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pairing provider not configured")
		}
		out, err := opts.NodePairList(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodePairApprove:
		req, err := methods.DecodeNodePairApproveParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePairApprove == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pairing provider not configured")
		}
		out, err := opts.NodePairApprove(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodePairReject:
		req, err := methods.DecodeNodePairRejectParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePairReject == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pairing provider not configured")
		}
		out, err := opts.NodePairReject(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodePairVerify:
		req, err := methods.DecodeNodePairVerifyParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePairVerify == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pairing provider not configured")
		}
		out, err := opts.NodePairVerify(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodDevicePairList:
		req, err := methods.DecodeDevicePairListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DevicePairList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device pairing provider not configured")
		}
		out, err := opts.DevicePairList(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodDevicePairApprove:
		req, err := methods.DecodeDevicePairApproveParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DevicePairApprove == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device pairing provider not configured")
		}
		out, err := opts.DevicePairApprove(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodDevicePairReject:
		req, err := methods.DecodeDevicePairRejectParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DevicePairReject == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device pairing provider not configured")
		}
		out, err := opts.DevicePairReject(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodDevicePairRemove:
		req, err := methods.DecodeDevicePairRemoveParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DevicePairRemove == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device pairing provider not configured")
		}
		out, err := opts.DevicePairRemove(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodDeviceTokenRotate:
		req, err := methods.DecodeDeviceTokenRotateParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DeviceTokenRotate == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device token provider not configured")
		}
		out, err := opts.DeviceTokenRotate(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodDeviceTokenRevoke:
		req, err := methods.DecodeDeviceTokenRevokeParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DeviceTokenRevoke == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device token provider not configured")
		}
		out, err := opts.DeviceTokenRevoke(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodeList:
		req, err := methods.DecodeNodeListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodeList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node provider not configured")
		}
		out, err := opts.NodeList(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodeDescribe:
		req, err := methods.DecodeNodeDescribeParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodeDescribe == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node provider not configured")
		}
		out, err := opts.NodeDescribe(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodeRename:
		req, err := methods.DecodeNodeRenameParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodeRename == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node provider not configured")
		}
		out, err := opts.NodeRename(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodeCanvasCapabilityRefresh:
		req, err := methods.DecodeNodeCanvasCapabilityRefreshParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodeCanvasCapabilityRefresh == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node provider not configured")
		}
		out, err := opts.NodeCanvasCapabilityRefresh(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodeInvoke:
		req, err := methods.DecodeNodeInvokeParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodeInvoke == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node invoke provider not configured")
		}
		out, err := opts.NodeInvoke(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodeEvent:
		req, err := methods.DecodeNodeEventParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodeEvent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node event provider not configured")
		}
		out, err := opts.NodeEvent(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodeResult, methods.MethodNodeInvokeResult:
		req, err := methods.DecodeNodeResultParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodeResult == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node result provider not configured")
		}
		out, err := opts.NodeResult(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodePendingEnqueue:
		req, err := methods.DecodeNodePendingEnqueueParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePendingEnqueue == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pending provider not configured")
		}
		out, err := opts.NodePendingEnqueue(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodePendingPull:
		req, err := methods.DecodeNodePendingPullParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePendingPull == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pending provider not configured")
		}
		out, err := opts.NodePendingPull(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodePendingAck:
		req, err := methods.DecodeNodePendingAckParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePendingAck == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pending provider not configured")
		}
		out, err := opts.NodePendingAck(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodNodePendingDrain:
		req, err := methods.DecodeNodePendingDrainParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePendingDrain == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pending provider not configured")
		}
		out, err := opts.NodePendingDrain(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodExecApprovalsNodeGet:
		req, err := methods.DecodeExecApprovalsNodeGetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ExecApprovalsNodeGet == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("exec approvals provider not configured")
		}
		out, err := opts.ExecApprovalsNodeGet(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodExecApprovalsNodeSet:
		req, err := methods.DecodeExecApprovalsNodeSetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ExecApprovalsNodeSet == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("exec approvals provider not configured")
		}
		out, err := opts.ExecApprovalsNodeSet(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	default:
		return nil, 0, nil
	}
}
