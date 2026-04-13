package admin

import (
	"context"
	"fmt"
	"net/http"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchChannels(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodChannelsStatus:
		req, err := methods.DecodeChannelsStatusParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ChannelsStatus == nil {
			return map[string]any{"channels": []map[string]any{{"id": "nostr", "connected": true}}}, http.StatusOK, nil
		}
		out, err := opts.ChannelsStatus(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodChannelsLogout:
		req, err := methods.DecodeChannelsLogoutParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ChannelsLogout == nil {
			return map[string]any{"ok": true, "channel": req.Channel}, http.StatusOK, nil
		}
		out, err := opts.ChannelsLogout(ctx, req.Channel)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodChannelsJoin,
		methods.MethodChannelsLeave,
		methods.MethodChannelsList,
		methods.MethodChannelsSend:
		return delegateControlCall(ctx, opts, method, call.Params, "channel runtime not configured")
	case methods.MethodUsageCost:
		req, err := methods.DecodeUsageCostParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.UsageCost == nil {
			return map[string]any{"ok": true, "total_usd": 0}, http.StatusOK, nil
		}
		out, err := opts.UsageCost(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	default:
		return nil, 0, fmt.Errorf("unknown channel method %q", method)
	}
}
