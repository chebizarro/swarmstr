package admin

import (
	"context"
	"fmt"
	"net/http"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchRuntime(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodLogsTail:
		req, err := methods.DecodeLogsTailParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.TailLogs == nil {
			return map[string]any{"cursor": req.Cursor, "lines": []string{}, "truncated": false, "reset": false}, http.StatusOK, nil
		}
		out, err := opts.TailLogs(ctx, req.Cursor, req.Limit, req.MaxBytes)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodRuntimeObserve:
		req, err := methods.DecodeRuntimeObserveParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ObserveRuntime == nil {
			return map[string]any{"events": map[string]any{"cursor": req.EventCursor, "events": []map[string]any{}, "truncated": false, "reset": false}, "logs": map[string]any{"cursor": req.LogCursor, "lines": []string{}, "truncated": false, "reset": false}, "timed_out": false, "waited_ms": int64(0)}, http.StatusOK, nil
		}
		out, err := opts.ObserveRuntime(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodRelayPolicyGet:
		if opts.GetRelayPolicy == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("relay policy provider not configured")
		}
		policyView, err := opts.GetRelayPolicy(ctx)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return policyView, http.StatusOK, nil
	default:
		return internalRoutingError("runtime", method)
	}
}
