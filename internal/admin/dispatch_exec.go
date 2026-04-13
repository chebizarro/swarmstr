package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchExec(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodExecApprovalsGet:
		req, err := methods.DecodeExecApprovalsGetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ExecApprovalsGet == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("exec approvals provider not configured")
		}
		out, err := opts.ExecApprovalsGet(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodExecApprovalsSet:
		req, err := methods.DecodeExecApprovalsSetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ExecApprovalsSet == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("exec approvals provider not configured")
		}
		out, err := opts.ExecApprovalsSet(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodExecApprovalRequest:
		req, err := methods.DecodeExecApprovalRequestParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ExecApprovalRequest == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("exec approval provider not configured")
		}
		out, err := opts.ExecApprovalRequest(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodExecApprovalWaitDecision:
		req, err := methods.DecodeExecApprovalWaitDecisionParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ExecApprovalWaitDecision == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("exec approval provider not configured")
		}
		out, err := opts.ExecApprovalWaitDecision(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodExecApprovalResolve:
		req, err := methods.DecodeExecApprovalResolveParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ExecApprovalResolve == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("exec approvals provider not configured")
		}
		out, err := opts.ExecApprovalResolve(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	default:
		return internalRoutingError("exec", method)
	}
}
