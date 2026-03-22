// Package runtime — replay.go declares the replay/backfill policy for each
// long-lived subscription type.
//
// Long-lived Nostr subscriptions need explicit replay semantics:
//   - On startup: how far back to subscribe (Since) to cover offline window.
//   - On resubscribe: how far back to re-request after a relay reconnect or rebind.
//   - On inbound: how old an event can be before it's dropped as stale/replayed.
//
// This file centralises those constants and helpers so they're declared in one
// place instead of scattered across bus implementations.
package runtime

import "time"

// ─── Replay policy constants ─────────────────────────────────────────────────
//
// Each long-lived subscription type has a declared replay contract:
//
//   Control RPC (kind:21120)
//     Startup:     checkpoint − 2min, floored at now − 30min (via main checkpointSinceUnix)
//     Resubscribe: now − ControlRPCResubscribeWindow
//     Inbound:     maxReqAge (default 2min) + 30s future skew
//     Rationale:   control requests are short-lived; 10min overlap catches
//                  requests sent during brief outages without replaying hours.
//
//   NIP-04 DM (kind:4)
//     Startup:     checkpoint − 2min, floored at now − 30min
//     Resubscribe: now − DMReplayWindow (configurable, default 30min)
//     Inbound:     DMReplayWindow — events older than this are dropped
//     Rationale:   DMs should be surfaced if sent recently; 30min covers
//                  typical reconnect gaps while avoiding re-processing old history.
//
//   NIP-17 DM (kind:1059 gift wrap)
//     Startup:     normalizeNIP17Since(checkpoint) — up to NIP17GiftWrapBackfill
//     Resubscribe: normalizeNIP17Since(now) — same backfill window
//     Inbound:     no time-based drop (dedup by event ID only)
//     Rationale:   NIP-59 intentionally backdates gift wraps by up to ~10 hours.
//                  The subscription must look back far enough to see them.
//
//   Watch (nostr_watch tool)
//     Startup:     now − watchSinceJitter (30s)
//     Resubscribe: now − watchSinceJitter
//     Inbound:     dedup by event ID only (no time-based drop)
//     Rationale:   watches are TTL-bounded and user-initiated; jitter covers
//                  the brief connection setup window.
//
//   NIP-51 allowlist (kind:30000)
//     Startup:     no Since (full replaceable history)
//     Resubscribe: n/a (SubscribeManyNotifyEOSE, stays open)
//     Inbound:     event ID dedup
//     Rationale:   replaceable events — latest version wins. Full fetch needed.
//
//   NIP-65 self-sync (kind:10002)
//     Startup:     no Since (full replaceable history)
//     Resubscribe: n/a (SubscribeManyNotifyEOSE, stays open)
//     Inbound:     event ID dedup
//     Rationale:   replaceable event. One fetch + live updates.
//
//   DVM (kind:5xxx)
//     Startup:     now − DVMResubscribeWindow
//     Resubscribe: now − DVMResubscribeWindow
//     Inbound:     dedup by event ID (seen set)
//     Rationale:   DVM jobs have a 5-minute processing timeout; 10min replay
//                  covers reconnect gaps and ensures pending jobs aren't lost.

const (
	// ControlRPCResubscribeWindow is how far back the control bus looks when
	// restarting a subscription after a relay reconnect or rebind.
	ControlRPCResubscribeWindow = 10 * time.Minute

	// DMReplayWindowDefault is the default replay window for NIP-04 DM
	// subscriptions. Events older than this are dropped on inbound, and
	// resubscriptions look back this far.
	DMReplayWindowDefault = 30 * time.Minute

	// NIP17GiftWrapBackfill is how far back NIP-17 subscriptions look to
	// account for NIP-59's intentional timestamp backdating of gift wraps.
	// Exported alias for the package-level constant in nip17_bus.go.
	NIP17GiftWrapBackfill = nip17GiftWrapBackfill

	// WatchSinceJitter is subtracted from "now" when starting or restarting
	// a watch subscription to capture events during connection setup.
	WatchSinceJitter = 30 * time.Second

	// DVMResubscribeWindow is how far back DVM job subscriptions look when
	// starting or restarting. 10 minutes covers typical reconnect gaps while
	// keeping replay bounded.
	DVMResubscribeWindow = 10 * time.Minute
)

// ResubscribeSince returns the Since unix timestamp for a resubscription
// after a relay reconnect, given a replay window duration.
// The returned value is now − window, clamped to 0.
func ResubscribeSince(window time.Duration) int64 {
	since := time.Now().Add(-window).Unix()
	if since < 0 {
		return 0
	}
	return since
}
