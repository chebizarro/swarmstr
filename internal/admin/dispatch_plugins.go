package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchPlugins(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodPluginsInstall:
		req, err := methods.DecodePluginsInstallParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.PluginsInstall == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("plugins provider not configured")
		}
		out, err := opts.PluginsInstall(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodPluginsUninstall:
		req, err := methods.DecodePluginsUninstallParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.PluginsUninstall == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("plugins provider not configured")
		}
		out, err := opts.PluginsUninstall(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodPluginsUpdate:
		req, err := methods.DecodePluginsUpdateParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.PluginsUpdate == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("plugins provider not configured")
		}
		out, err := opts.PluginsUpdate(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodPluginsRegistryList:
		req, err := methods.DecodePluginsRegistryListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.PluginsRegistryList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("plugins registry not configured")
		}
		out, err := opts.PluginsRegistryList(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodPluginsRegistryGet:
		req, err := methods.DecodePluginsRegistryGetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.PluginsRegistryGet == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("plugins registry not configured")
		}
		out, err := opts.PluginsRegistryGet(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("plugin not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodPluginsRegistrySearch:
		req, err := methods.DecodePluginsRegistrySearchParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.PluginsRegistrySearch == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("plugins registry not configured")
		}
		out, err := opts.PluginsRegistrySearch(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	default:
		return nil, 0, nil
	}
}
