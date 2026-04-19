package toolbuiltin

import (
	"context"
	"time"

	"metiq/internal/store/state"
	"metiq/internal/timeouts"
)

type timeoutsConfigKey struct{}

// WithTimeoutsConfig attaches a TimeoutsConfig to the context for use by tools.
func WithTimeoutsConfig(ctx context.Context, cfg state.TimeoutsConfig) context.Context {
	return context.WithValue(ctx, timeoutsConfigKey{}, cfg)
}

func timeoutsFromCtx(ctx context.Context) state.TimeoutsConfig {
	if v, ok := ctx.Value(timeoutsConfigKey{}).(state.TimeoutsConfig); ok {
		return v
	}
	return state.TimeoutsConfig{}
}

func grepTimeout(ctx context.Context) time.Duration {
	return timeouts.GrepSearch(timeoutsFromCtx(ctx))
}

func imageTimeout(ctx context.Context) time.Duration {
	return timeouts.ImageFetch(timeoutsFromCtx(ctx))
}

func chainTimeout(ctx context.Context) time.Duration {
	return timeouts.ToolChainExec(timeoutsFromCtx(ctx))
}

func gitOpsTimeout(ctx context.Context) time.Duration {
	return timeouts.GitOps(timeoutsFromCtx(ctx))
}
