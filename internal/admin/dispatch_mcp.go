package admin

import (
	"context"
	"fmt"
	"net/http"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchMcp(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodMCPList:
		req, err := methods.DecodeMCPListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.MCPList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("mcp operations not configured")
		}
		out, err := opts.MCPList(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodMCPGet:
		req, err := methods.DecodeMCPGetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.MCPGet == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("mcp operations not configured")
		}
		out, err := opts.MCPGet(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodMCPPut:
		req, err := methods.DecodeMCPPutParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.MCPPut == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("mcp operations not configured")
		}
		out, err := opts.MCPPut(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodMCPRemove:
		req, err := methods.DecodeMCPRemoveParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.MCPRemove == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("mcp operations not configured")
		}
		out, err := opts.MCPRemove(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodMCPTest:
		req, err := methods.DecodeMCPTestParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.MCPTest == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("mcp operations not configured")
		}
		out, err := opts.MCPTest(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodMCPReconnect:
		req, err := methods.DecodeMCPReconnectParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.MCPReconnect == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("mcp operations not configured")
		}
		out, err := opts.MCPReconnect(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodMCPAuthStart:
		req, err := methods.DecodeMCPAuthStartParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.MCPAuthStart == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("mcp auth not configured")
		}
		out, err := opts.MCPAuthStart(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodMCPAuthRefresh:
		req, err := methods.DecodeMCPAuthRefreshParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.MCPAuthRefresh == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("mcp auth not configured")
		}
		out, err := opts.MCPAuthRefresh(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodMCPAuthClear:
		req, err := methods.DecodeMCPAuthClearParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.MCPAuthClear == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("mcp auth not configured")
		}
		out, err := opts.MCPAuthClear(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodSecretsReload:
		req, err := methods.DecodeSecretsReloadParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SecretsReload == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("secrets provider not configured")
		}
		out, err := opts.SecretsReload(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodSecretsResolve:
		req, err := methods.DecodeSecretsResolveParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SecretsResolve == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("secrets provider not configured")
		}
		out, err := opts.SecretsResolve(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	default:
		return internalRoutingError("mcp", method)
	}
}
