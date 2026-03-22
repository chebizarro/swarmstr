package runtime

import (
	"testing"
	"time"
)

func TestNormalizeNIP17SinceDefaultsToGiftWrapBackfillWindow(t *testing.T) {
	before := time.Now().Add(-nip17GiftWrapBackfill).Unix()
	got := normalizeNIP17Since(0)
	after := time.Now().Add(-nip17GiftWrapBackfill).Unix()
	if got < before || got > after {
		t.Fatalf("expected default since within [%d, %d], got %d", before, after, got)
	}
}

func TestNormalizeNIP17SinceBackfillsCheckpointByGiftWrapWindow(t *testing.T) {
	now := time.Now().Unix()
	recent := now - 120
	got := normalizeNIP17Since(recent)
	floor := time.Now().Add(-nip17GiftWrapBackfill).Unix()
	if got != floor {
		t.Fatalf("expected adjusted since clamped to floor %d, got %d", floor, got)
	}
}

func TestNormalizeNIP17SinceClampsToZero(t *testing.T) {
	// Since values far in the past are clamped to the backfill floor rather than
	// scanning arbitrarily far back.
	floor := time.Now().Add(-nip17GiftWrapBackfill).Unix()
	if got := normalizeNIP17Since(60); got != floor {
		t.Fatalf("expected floor clamp %d, got %d", floor, got)
	}
}
