// Package extensions provides config-gated registration of built-in channel
// plugins.  Each extension package self-registers a lightweight constructor
// via sdk.RegisterChannelConstructor in its init().  RegisterConfigured reads
// the live config and only instantiates plugins whose kind matches a
// configured nostr_channels entry.
//
// To include an extension in the binary, add a blank import in the daemon's
// main.go (or in extensions/all.go):
//
//	import _ "metiq/internal/extensions/telegram"
//
// To exclude an extension, remove its import.  No code from excluded
// extensions is compiled into the binary.
package extensions

import (
	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

// RegisterConfigured inspects the config's NostrChannels and registers only
// the channel plugins whose kind matches a configured entry.  Returns the
// number of plugins registered.
func RegisterConfigured(cfg state.ConfigDoc) int {
	// Collect unique kinds referenced by the config.
	needed := map[string]struct{}{}
	for _, ch := range cfg.NostrChannels {
		if ch.Kind != "" {
			needed[ch.Kind] = struct{}{}
		}
	}

	ctors := sdk.ChannelConstructors()
	registered := 0
	for kind := range needed {
		ctor, ok := ctors[kind]
		if !ok {
			continue // not a built-in extension (may be a JS plugin)
		}
		channels.RegisterChannelPlugin(ctor())
		registered++
	}
	return registered
}

// AvailableKinds returns the set of built-in channel plugin kinds that have
// been compiled into this binary (i.e. whose packages were imported).
func AvailableKinds() []string {
	ctors := sdk.ChannelConstructors()
	kinds := make([]string, 0, len(ctors))
	for k := range ctors {
		kinds = append(kinds, k)
	}
	return kinds
}
