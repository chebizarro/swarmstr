package admin

import (
	"context"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchTasks(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodTasksCreate,
		methods.MethodTasksGet,
		methods.MethodTasksList,
		methods.MethodTasksCancel,
		methods.MethodTasksResume,
		methods.MethodTasksDoctor,
		methods.MethodTasksSummary,
		methods.MethodTasksAuditExport,
		methods.MethodTasksTrace:
		return delegateControlCall(ctx, opts, method, call.Params, "tasks provider not configured")
	default:
		return internalRoutingError("tasks", method)
	}
}
