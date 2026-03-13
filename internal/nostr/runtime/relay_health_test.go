package runtime

import (
	"testing"
	"time"
)

func TestRelayHealthTrackerSortRelaysByScore(t *testing.T) {
	trk := NewRelayHealthTracker()
	relays := []string{"wss://a", "wss://b", "wss://c"}
	trk.Seed(relays)

	trk.RecordFailure("wss://a")
	trk.RecordFailure("wss://a")
	trk.RecordSuccess("wss://b")
	trk.RecordSuccess("wss://b")

	ordered := trk.SortRelays(relays)
	if len(ordered) != 3 {
		t.Fatalf("unexpected order length: %d", len(ordered))
	}
	if ordered[0] != "wss://b" {
		t.Fatalf("expected healthiest relay first, got %v", ordered)
	}
}

func TestRelayHealthTrackerAllowedBackoff(t *testing.T) {
	trk := NewRelayHealthTracker()
	trk.baseBackoff = 50 * time.Millisecond
	trk.maxBackoff = 100 * time.Millisecond
	trk.Seed([]string{"wss://a"})

	trk.RecordFailure("wss://a")
	trk.RecordFailure("wss://a")
	if trk.Allowed("wss://a", time.Now()) {
		t.Fatal("expected relay to be temporarily blocked after repeated failures")
	}
	if !trk.Allowed("wss://a", time.Now().Add(70*time.Millisecond)) {
		t.Fatal("expected relay to be allowed after backoff window")
	}
}

func TestRelayHealthTrackerCandidatesFallbackToOrderedWhenAllCoolingDown(t *testing.T) {
	trk := NewRelayHealthTracker()
	trk.baseBackoff = 5 * time.Second
	trk.maxBackoff = 5 * time.Second
	relays := []string{"wss://a", "wss://b"}
	trk.Seed(relays)
	trk.RecordFailure("wss://a")
	trk.RecordFailure("wss://a")
	trk.RecordFailure("wss://b")
	trk.RecordFailure("wss://b")

	candidates := trk.Candidates(relays, time.Now())
	if len(candidates) != len(relays) {
		t.Fatalf("expected fallback to full relay set, got %v", candidates)
	}
}
