package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchCron(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodCronList:
		req, err := methods.DecodeCronListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.CronList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("cron provider not configured")
		}
		out, err := opts.CronList(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodCronStatus:
		req, err := methods.DecodeCronStatusParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.CronStatus == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("cron provider not configured")
		}
		out, err := opts.CronStatus(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodCronAdd:
		req, err := methods.DecodeCronAddParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.CronAdd == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("cron provider not configured")
		}
		out, err := opts.CronAdd(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodCronUpdate:
		req, err := methods.DecodeCronUpdateParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.CronUpdate == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("cron provider not configured")
		}
		out, err := opts.CronUpdate(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodCronRemove:
		req, err := methods.DecodeCronRemoveParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.CronRemove == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("cron provider not configured")
		}
		out, err := opts.CronRemove(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodCronRun:
		req, err := methods.DecodeCronRunParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.CronRun == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("cron provider not configured")
		}
		out, err := opts.CronRun(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodCronRuns:
		req, err := methods.DecodeCronRunsParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.CronRuns == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("cron provider not configured")
		}
		out, err := opts.CronRuns(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	default:
		return nil, 0, nil
	}
}
