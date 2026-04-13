package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"metiq/internal/config"
	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchConfig(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodTalkConfig:
		req, err := methods.DecodeTalkConfigParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.TalkConfig == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("talk provider not configured")
		}
		out, err := opts.TalkConfig(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodConfigGet:
		if opts.GetConfig == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("config provider not configured")
		}
		cfg, err := opts.GetConfig(ctx)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		redacted := config.Redact(cfg)
		return map[string]any{
			"config":    redacted,
			"hash":      cfg.Hash(),
			"base_hash": cfg.Hash(),
		}, http.StatusOK, nil
	case methods.MethodListGet:
		if opts.GetList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("list provider not configured")
		}
		req, err := methods.DecodeListGetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		list, err := opts.GetList(ctx, req.Name)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return list, http.StatusOK, nil
	case methods.MethodListPut:
		if opts.PutList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("list provider not configured")
		}
		req, err := methods.DecodeListPutParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if req.ExpectedVersionSet || req.ExpectedEvent != "" {
			if opts.GetListWithEvent == nil {
				return nil, http.StatusNotImplemented, fmt.Errorf("list preconditions not supported")
			}
			current, evt, err := opts.GetListWithEvent(ctx, req.Name)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					if req.ExpectedVersionSet && req.ExpectedVersion == 0 && req.ExpectedEvent == "" {
						goto listPreconditionsSatisfied
					}
					return nil, http.StatusConflict, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  0,
						ExpectedEvent:   req.ExpectedEvent,
					}
				}
				return nil, http.StatusInternalServerError, err
			}
			if req.ExpectedVersionSet {
				if req.ExpectedVersion == 0 {
					return nil, http.StatusConflict, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				} else if current.Version != req.ExpectedVersion {
					return nil, http.StatusConflict, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				}
			}
			if req.ExpectedEvent != "" && evt.ID != req.ExpectedEvent {
				return nil, http.StatusConflict, &methods.PreconditionConflictError{
					Resource:        "list:" + req.Name,
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
		}
	listPreconditionsSatisfied:
		if err := opts.PutList(ctx, req.Name, state.ListDoc{
			Version: 1,
			Name:    req.Name,
			Items:   req.Items,
		}); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true}, http.StatusOK, nil
	case methods.MethodConfigPut:
		if opts.PutConfig == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("config provider not configured")
		}
		req, err := methods.DecodeConfigPutParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if req.ExpectedVersionSet || req.ExpectedEvent != "" {
			if opts.GetConfigWithEvent == nil {
				return nil, http.StatusNotImplemented, fmt.Errorf("config preconditions not supported")
			}
			current, evt, err := opts.GetConfigWithEvent(ctx)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					if req.ExpectedVersionSet && req.ExpectedVersion == 0 && req.ExpectedEvent == "" {
						goto configPreconditionsSatisfied
					}
					return nil, http.StatusConflict, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  0,
						ExpectedEvent:   req.ExpectedEvent,
					}
				}
				return nil, http.StatusInternalServerError, err
			}
			if req.ExpectedVersionSet {
				if req.ExpectedVersion == 0 {
					return nil, http.StatusConflict, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				} else if current.Version != req.ExpectedVersion {
					return nil, http.StatusConflict, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				}
			}
			if req.ExpectedEvent != "" && evt.ID != req.ExpectedEvent {
				return nil, http.StatusConflict, &methods.PreconditionConflictError{
					Resource:        "config",
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
		}
	configPreconditionsSatisfied:
		if req.BaseHash != "" {
			if opts.GetConfig == nil {
				return nil, http.StatusNotImplemented, fmt.Errorf("config base_hash precondition requires get config provider")
			}
			current, err := opts.GetConfig(ctx)
			if err != nil {
				return nil, http.StatusInternalServerError, err
			}
			if err := methods.CheckBaseHash(current, req.BaseHash); err != nil {
				return nil, http.StatusConflict, err
			}
		}
		if err := opts.PutConfig(ctx, req.Config); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true}, http.StatusOK, nil
	case methods.MethodConfigSet:
		if opts.GetConfig == nil || opts.PutConfig == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("config providers not configured")
		}
		req, err := methods.DecodeConfigSetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ConfigSet != nil {
			out, status, err := opts.ConfigSet(ctx, req)
			if err != nil {
				return nil, status, err
			}
			return methods.ApplyCompatResponseAliases(out), status, nil
		}
		current, err := opts.GetConfig(ctx)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		if err := methods.CheckBaseHash(current, req.BaseHash); err != nil {
			return nil, http.StatusConflict, err
		}
		if req.Raw != "" {
			next, err := methods.DecodeConfigDocFromRaw(req.Raw)
			if err != nil {
				return nil, http.StatusBadRequest, err
			}
			if err := opts.PutConfig(ctx, next); err != nil {
				return nil, http.StatusInternalServerError, err
			}
			return map[string]any{"ok": true, "path": "raw", "config": config.Redact(next), "hash": next.Hash()}, http.StatusOK, nil
		}
		next, err := methods.ApplyConfigSet(current, req.Key, req.Value)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if err := opts.PutConfig(ctx, next); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true, "path": req.Key, "config": config.Redact(next), "hash": next.Hash()}, http.StatusOK, nil
	case methods.MethodConfigApply:
		if opts.PutConfig == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("config provider not configured")
		}
		req, err := methods.DecodeConfigApplyParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ConfigApply != nil {
			out, status, err := opts.ConfigApply(ctx, req)
			if err != nil {
				return nil, status, err
			}
			return methods.ApplyCompatResponseAliases(out), status, nil
		}
		next := req.Config
		if req.Raw != "" {
			next, err = methods.DecodeConfigDocFromRaw(req.Raw)
			if err != nil {
				return nil, http.StatusBadRequest, err
			}
		}
		if req.BaseHash != "" {
			if opts.GetConfig == nil {
				return nil, http.StatusNotImplemented, fmt.Errorf("config base_hash precondition requires get config provider")
			}
			current, err := opts.GetConfig(ctx)
			if err != nil {
				return nil, http.StatusInternalServerError, err
			}
			if err := methods.CheckBaseHash(current, req.BaseHash); err != nil {
				return nil, http.StatusConflict, err
			}
		}
		if err := opts.PutConfig(ctx, next); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true, "config": config.Redact(next), "hash": next.Hash()}, http.StatusOK, nil
	case methods.MethodConfigPatch:
		if opts.GetConfig == nil || opts.PutConfig == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("config providers not configured")
		}
		req, err := methods.DecodeConfigPatchParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ConfigPatch != nil {
			out, status, err := opts.ConfigPatch(ctx, req)
			if err != nil {
				return nil, status, err
			}
			return methods.ApplyCompatResponseAliases(out), status, nil
		}
		current, err := opts.GetConfig(ctx)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		if err := methods.CheckBaseHash(current, req.BaseHash); err != nil {
			return nil, http.StatusConflict, err
		}
		patch := req.Patch
		if req.Raw != "" {
			patch, err = methods.DecodeConfigPatchFromRaw(req.Raw)
			if err != nil {
				return nil, http.StatusBadRequest, err
			}
		}
		next, err := methods.ApplyConfigPatch(current, patch)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if err := opts.PutConfig(ctx, next); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true, "config": config.Redact(next), "hash": next.Hash()}, http.StatusOK, nil
	case methods.MethodConfigSchema:
		if opts.GetConfig == nil {
			return methods.ConfigSchema(), http.StatusOK, nil
		}
		cfg, err := opts.GetConfig(ctx)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ConfigSchema(cfg), http.StatusOK, nil
	default:
		return nil, 0, nil
	}
}
