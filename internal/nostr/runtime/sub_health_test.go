package runtime

import (
	"testing"
	"time"
)

func TestSubHealthTrackerRecordEvent(t *testing.T) {
	tr := NewSubHealthTracker("test")
	before := time.Now()
	tr.RecordEvent()
	tr.RecordEvent()
	snap := tr.Snapshot([]string{"wss://r.example.com"}, 10*time.Minute)
	if snap.EventCount != 2 {
		t.Fatalf("event_count = %d, want 2", snap.EventCount)
	}
	if snap.LastEventAt.Before(before) {
		t.Fatalf("last_event_at %v is before %v", snap.LastEventAt, before)
	}
}

func TestSubHealthTrackerRecordReconnect(t *testing.T) {
	tr := NewSubHealthTracker("test")
	tr.RecordReconnect()
	tr.RecordReconnect()
	tr.RecordReconnect()
	snap := tr.Snapshot(nil, 0)
	if snap.ReconnectCount != 3 {
		t.Fatalf("reconnect_count = %d, want 3", snap.ReconnectCount)
	}
	if snap.LastReconnectAt.IsZero() {
		t.Fatal("last_reconnect_at is zero")
	}
}

func TestSubHealthTrackerRecordClosed(t *testing.T) {
	tr := NewSubHealthTracker("test")
	tr.RecordClosed("auth-required:")
	tr.RecordClosed("rate-limited:")
	snap := tr.Snapshot(nil, 0)
	if snap.LastClosedReason != "rate-limited:" {
		t.Fatalf("last_closed_reason = %q, want %q", snap.LastClosedReason, "rate-limited:")
	}
}

func TestSubHealthTrackerSnapshotIncludesLabel(t *testing.T) {
	tr := NewSubHealthTracker("control-rpc")
	snap := tr.Snapshot([]string{"wss://a", "wss://b"}, 10*time.Minute)
	if snap.Label != "control-rpc" {
		t.Fatalf("label = %q, want %q", snap.Label, "control-rpc")
	}
	if len(snap.BoundRelays) != 2 {
		t.Fatalf("bound_relays len = %d, want 2", len(snap.BoundRelays))
	}
	if snap.ReplayWindowMS != int64((10*time.Minute)/time.Millisecond) {
		t.Fatalf("replay_window_ms = %d, want %d", snap.ReplayWindowMS, int64((10*time.Minute)/time.Millisecond))
	}
}

func TestSubHealthTrackerSnapshotZeroValues(t *testing.T) {
	tr := NewSubHealthTracker("dm")
	snap := tr.Snapshot(nil, 30*time.Minute)
	if !snap.LastEventAt.IsZero() {
		t.Fatalf("last_event_at should be zero, got %v", snap.LastEventAt)
	}
	if !snap.LastReconnectAt.IsZero() {
		t.Fatalf("last_reconnect_at should be zero, got %v", snap.LastReconnectAt)
	}
	if snap.LastClosedReason != "" {
		t.Fatalf("last_closed_reason should be empty, got %q", snap.LastClosedReason)
	}
	if snap.EventCount != 0 {
		t.Fatalf("event_count = %d, want 0", snap.EventCount)
	}
	if snap.ReconnectCount != 0 {
		t.Fatalf("reconnect_count = %d, want 0", snap.ReconnectCount)
	}
}

func TestSubHealthTrackerConcurrentAccess(t *testing.T) {
	tr := NewSubHealthTracker("stress")
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			tr.RecordEvent()
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		tr.RecordReconnect()
	}
	<-done
	snap := tr.Snapshot([]string{"wss://r"}, time.Minute)
	if snap.EventCount != 1000 {
		t.Fatalf("event_count = %d, want 1000", snap.EventCount)
	}
	if snap.ReconnectCount != 1000 {
		t.Fatalf("reconnect_count = %d, want 1000", snap.ReconnectCount)
	}
}

func TestControlRPCBusHealthSnapshot(t *testing.T) {
	// Verify ControlRPCBus.HealthSnapshot works on a nil subHealth (defensive).
	bus := &ControlRPCBus{relays: []string{"wss://r"}}
	snap := bus.HealthSnapshot()
	if snap.Label != "control-rpc" {
		t.Fatalf("label = %q, want %q", snap.Label, "control-rpc")
	}
	if snap.ReplayWindowMS != int64(ControlRPCResubscribeWindow/time.Millisecond) {
		t.Fatalf("replay_window_ms = %d, want %d", snap.ReplayWindowMS, int64(ControlRPCResubscribeWindow/time.Millisecond))
	}
}

func TestDMBusHealthSnapshot(t *testing.T) {
	bus := &DMBus{relays: []string{"wss://r"}, replayWindow: 45 * time.Minute}
	snap := bus.HealthSnapshot()
	if snap.Label != "dm" {
		t.Fatalf("label = %q, want %q", snap.Label, "dm")
	}
	if snap.ReplayWindowMS != int64((45*time.Minute)/time.Millisecond) {
		t.Fatalf("replay_window_ms = %d, want %d", snap.ReplayWindowMS, int64((45*time.Minute)/time.Millisecond))
	}
}

func TestNIP17BusHealthSnapshot(t *testing.T) {
	bus := &NIP17Bus{relays: []string{"wss://r"}}
	snap := bus.HealthSnapshot()
	if snap.Label != "nip17" {
		t.Fatalf("label = %q, want %q", snap.Label, "nip17")
	}
	if snap.ReplayWindowMS != int64(NIP17GiftWrapBackfill/time.Millisecond) {
		t.Fatalf("replay_window_ms = %d, want %d", snap.ReplayWindowMS, int64(NIP17GiftWrapBackfill/time.Millisecond))
	}
}

func TestSubHealthReporterInterface(t *testing.T) {
	// Ensure DMBus and NIP17Bus satisfy SubHealthReporter.
	var _ SubHealthReporter = (*DMBus)(nil)
	var _ SubHealthReporter = (*NIP17Bus)(nil)
}
