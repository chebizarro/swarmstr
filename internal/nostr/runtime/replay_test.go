package runtime

import (
	"testing"
	"time"
)

func TestResubscribeSinceWithinWindow(t *testing.T) {
	window := 30 * time.Minute
	got := ResubscribeSince(window)
	expected := time.Now().Add(-window).Unix()
	// Allow 2s tolerance.
	if got < expected-2 || got > expected+2 {
		t.Fatalf("ResubscribeSince(%v) = %d, want ~%d", window, got, expected)
	}
}

func TestResubscribeSinceClampsToZero(t *testing.T) {
	// A window larger than the current unix time should clamp to 0.
	got := ResubscribeSince(time.Duration(time.Now().Unix()+3600) * time.Second)
	if got != 0 {
		t.Fatalf("ResubscribeSince(huge) = %d, want 0", got)
	}
}

func TestResubscribeSinceZeroWindow(t *testing.T) {
	got := ResubscribeSince(0)
	now := time.Now().Unix()
	// A zero window should return approximately now.
	if got < now-2 || got > now+2 {
		t.Fatalf("ResubscribeSince(0) = %d, want ~%d", got, now)
	}
}

func TestControlRPCResubscribeWindowIs10Minutes(t *testing.T) {
	if ControlRPCResubscribeWindow != 10*time.Minute {
		t.Fatalf("ControlRPCResubscribeWindow = %v, want 10m", ControlRPCResubscribeWindow)
	}
}

func TestDMReplayWindowDefaultIs30Minutes(t *testing.T) {
	if DMReplayWindowDefault != 30*time.Minute {
		t.Fatalf("DMReplayWindowDefault = %v, want 30m", DMReplayWindowDefault)
	}
}

func TestNIP17GiftWrapBackfillExported(t *testing.T) {
	// Verify the exported constant matches the internal one.
	if NIP17GiftWrapBackfill != nip17GiftWrapBackfill {
		t.Fatalf("NIP17GiftWrapBackfill = %v, want %v", NIP17GiftWrapBackfill, nip17GiftWrapBackfill)
	}
}

func TestWatchSinceJitterIs30Seconds(t *testing.T) {
	if WatchSinceJitter != 30*time.Second {
		t.Fatalf("WatchSinceJitter = %v, want 30s", WatchSinceJitter)
	}
}

// TestReplayPolicyConsistency verifies that the replay window defaults in the
// bus implementations match the declared policy constants.
func TestReplayPolicyConsistency(t *testing.T) {
	// DMBus with zero replay window should use the default.
	b := &DMBus{replayWindow: 0}
	since := b.resubscribeSinceUnix()
	expected := ResubscribeSince(DMReplayWindowDefault)
	if since < expected-2 || since > expected+2 {
		t.Fatalf("DMBus default resubscribe = %d, want ~%d (DMReplayWindowDefault)", since, expected)
	}

	// DMBus with explicit replay window should use that.
	b2 := &DMBus{replayWindow: 15 * time.Minute}
	since2 := b2.resubscribeSinceUnix()
	expected2 := ResubscribeSince(15 * time.Minute)
	if since2 < expected2-2 || since2 > expected2+2 {
		t.Fatalf("DMBus explicit resubscribe = %d, want ~%d (15m)", since2, expected2)
	}
}
