// Package toolbuiltin relay_selector.go — global NIP-65 relay selector hook.
//
// The main daemon registers the global relay selector at startup so that
// tool implementations can use the NIP-65 outbox model for relay routing
// (e.g. invalidating cache entries after publishing relay list updates).
package toolbuiltin

import (
 	"sync/atomic"
	nostruntime "swarmstr/internal/nostr/runtime"
)

// globalRelaySelector is the shared NIP-65 relay selector.
// Set at startup by SetRelaySelector.
var globalRelaySelector atomic.Pointer[nostruntime.RelaySelector]

// SetRelaySelector registers the global NIP-65 relay selector for use by tools.
func SetRelaySelector(sel *nostruntime.RelaySelector) {
	globalRelaySelector.Store(sel)
}

// GetRelaySelector returns the global NIP-65 relay selector, or nil if not set.
func GetRelaySelector() *nostruntime.RelaySelector {
	return globalRelaySelector.Load()
}
