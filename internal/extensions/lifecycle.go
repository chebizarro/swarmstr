package extensions

import (
	"context"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

// NewConfiguredAccountRuntime is the extension-layer lifecycle hook. It
// refreshes the configured constructor/account catalog before creating the
// daemon-owned, per-account runtime. Providers may additionally implement
// channels.AccountLifecyclePlugin for custom start/stop behavior.
func NewConfiguredAccountRuntime(
	ctx context.Context,
	cfg state.ConfigDoc,
	onMessage func(sdk.InboundChannelMessage),
	onStart func(channels.AccountSnapshot, channels.AccountConnection),
	onStop func(channels.AccountSnapshot),
) *channels.AccountRuntime {
	RegisterConfigured(cfg)
	return channels.NewAccountRuntime(channels.AccountRuntimeOptions{
		Context: ctx, OnMessage: onMessage, OnStart: onStart, OnStop: onStop,
	})
}
