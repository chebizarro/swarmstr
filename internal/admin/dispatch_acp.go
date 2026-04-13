package admin

import (
	"context"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchACP(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodACPRegister,
		methods.MethodACPUnregister,
		methods.MethodACPPeers,
		methods.MethodACPDispatch,
		methods.MethodACPPipeline:
		return delegateControlCall(ctx, opts, method, call.Params, "acp provider not configured")
	default:
		return nil, 0, nil
	}
}
