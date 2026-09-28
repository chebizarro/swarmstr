package extensions

import (
	"context"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
	"metiq/internal/secrets"
	"metiq/internal/store/state"
)

// NewConfiguredAccountRuntime is the extension-layer lifecycle hook. It
// refreshes the configured constructor/account catalog before creating the
// daemon-owned, per-account runtime. Providers may additionally implement
// channels.AccountLifecyclePlugin for custom start/stop behavior. store
// resolves secret references in account config and persists rotated
// credentials; it may be nil when no account config uses secret references.
func NewConfiguredAccountRuntime(
	ctx context.Context,
	cfg state.ConfigDoc,
	store *secrets.Store,
	onMessage func(sdk.InboundChannelMessage),
	onStart func(channels.AccountSnapshot, channels.AccountConnection),
	onStop func(channels.AccountSnapshot),
) *channels.AccountRuntime {
	RegisterConfigured(cfg)
	return channels.NewAccountRuntime(channels.AccountRuntimeOptions{
		Context: ctx, OnMessage: onMessage, OnStart: onStart, OnStop: onStop, Secrets: store,
	})
}
