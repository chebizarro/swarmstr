package runtime

import (
	"sync"
	"time"
)

// SubHealthSnapshot is a point-in-time view of a long-lived subscription's
// operational state. Each bus (ControlRPC, DM, NIP-17) exposes one via
// HealthSnapshot(), and WatchRegistry exposes one per active watch.
type SubHealthSnapshot struct {
	// Label identifies the subscription type (e.g. "control-rpc", "dm", "nip17").
	Label string `json:"label"`

	// BoundRelays lists the relays the subscription is currently bound to.
	BoundRelays []string `json:"bound_relays"`

	// LastEventAt is the wall-clock time of the most recent inbound event,
	// or zero if no event has been received yet.
	LastEventAt time.Time `json:"last_event_at,omitempty"`

	// LastReconnectAt is the wall-clock time of the most recent subscription
	// restart (rebind, retry, or initial start).
	LastReconnectAt time.Time `json:"last_reconnect_at,omitempty"`

	// LastClosedReason is the reason string from the most recent CLOSED
	// signal, or "" if none received.
	LastClosedReason string `json:"last_closed_reason,omitempty"`

	// ReplayWindow is the configured replay/backfill duration for this
	// subscription type.
	ReplayWindowMS int64 `json:"replay_window_ms"`

	// EventCount is the total number of inbound events processed since start.
	EventCount int64 `json:"event_count"`

	// ReconnectCount is the total number of subscription restarts since start.
	ReconnectCount int64 `json:"reconnect_count"`
}

// SubHealthTracker is a concurrency-safe tracker embedded in each bus to
// record subscription lifecycle events. The zero value is usable.
type SubHealthTracker struct {
	mu               sync.Mutex
	label            string
	lastEventAt      time.Time
	lastReconnectAt  time.Time
	lastClosedReason string
	eventCount       int64
	reconnectCount   int64
}

// NewSubHealthTracker creates a tracker with the given label.
func NewSubHealthTracker(label string) *SubHealthTracker {
	return &SubHealthTracker{label: label}
}

// RecordEvent marks that an inbound event was received.
func (t *SubHealthTracker) RecordEvent() {
	t.mu.Lock()
	t.lastEventAt = time.Now()
	t.eventCount++
	t.mu.Unlock()
}

// RecordReconnect marks that a subscription restart occurred.
func (t *SubHealthTracker) RecordReconnect() {
	t.mu.Lock()
	t.lastReconnectAt = time.Now()
	t.reconnectCount++
	t.mu.Unlock()
}

// RecordClosed records a CLOSED reason string.
func (t *SubHealthTracker) RecordClosed(reason string) {
	t.mu.Lock()
	t.lastClosedReason = reason
	t.mu.Unlock()
}

// UnixMillisOrZero returns t as Unix milliseconds, or 0 if t is zero.
func unixMillisOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// Snapshot returns a point-in-time copy of the tracked state.
// The caller must supply boundRelays and replayWindow (owned by the bus).
func (t *SubHealthTracker) Snapshot(boundRelays []string, replayWindow time.Duration) SubHealthSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	windowMS := int64(0)
	if replayWindow > 0 {
		windowMS = int64(replayWindow / time.Millisecond)
	}
	return SubHealthSnapshot{
		Label:            t.label,
		BoundRelays:      boundRelays,
		LastEventAt:      t.lastEventAt,
		LastReconnectAt:  t.lastReconnectAt,
		LastClosedReason: t.lastClosedReason,
		ReplayWindowMS:   windowMS,
		EventCount:       t.eventCount,
		ReconnectCount:   t.reconnectCount,
	}
}
