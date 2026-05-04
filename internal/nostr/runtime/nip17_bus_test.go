package runtime

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
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
	beforeFloor := time.Now().Add(-nip17GiftWrapBackfill).Unix()
	got := normalizeNIP17Since(recent)
	afterFloor := time.Now().Add(-nip17GiftWrapBackfill).Unix()
	if got < beforeFloor || got > afterFloor {
		t.Fatalf("expected adjusted since clamped within [%d, %d], got %d", beforeFloor, afterFloor, got)
	}
}

func TestNormalizeNIP17SinceClampsToZero(t *testing.T) {
	// Since values far in the past are clamped to the backfill floor rather than
	// scanning arbitrarily far back.
	beforeFloor := time.Now().Add(-nip17GiftWrapBackfill).Unix()
	got := normalizeNIP17Since(60)
	afterFloor := time.Now().Add(-nip17GiftWrapBackfill).Unix()
	if got < beforeFloor || got > afterFloor {
		t.Fatalf("expected floor clamp within [%d, %d], got %d", beforeFloor, afterFloor, got)
	}
	if got < 0 {
		t.Fatalf("expected non-negative clamp, got %d", got)
	}
}

func TestNIP17ValidateGiftWrapEvent(t *testing.T) {
	bus, keyer, recipient := newTestNIP17BusIdentity(t)
	evt := signedEvent(t, keyer, nostr.Event{
		Kind:      nostr.KindGiftWrap,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", recipient.Hex()}},
		Content:   "sealed-content",
	})
	if err := bus.validateGiftWrapEvent(evt, time.Now()); err != nil {
		t.Fatalf("expected valid gift wrap, got error: %v", err)
	}

	badTarget := evt
	badTarget.Tags = nostr.Tags{{"p", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}}
	if err := bus.validateGiftWrapEvent(badTarget, time.Now()); err == nil {
		t.Fatal("expected missing recipient-tag validation error")
	}

	badID := evt
	badID.Content = "mutated"
	if err := bus.validateGiftWrapEvent(badID, time.Now()); err == nil {
		t.Fatal("expected invalid id/signature validation error")
	}
}

func TestNIP17ValidateRumorEvent(t *testing.T) {
	bus, keyer, recipient := newTestNIP17BusIdentity(t)
	rumor := signedEvent(t, keyer, nostr.Event{
		Kind:      nostr.KindDirectMessage,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", recipient.Hex()}},
		Content:   "hello",
	})
	if err := bus.validateRumorEvent(rumor, time.Now()); err != nil {
		t.Fatalf("expected valid rumor, got error: %v", err)
	}

	wrongKind := rumor
	wrongKind.Kind = nostr.KindTextNote
	if err := bus.validateRumorEvent(wrongKind, time.Now()); err == nil {
		t.Fatal("expected kind validation error")
	}

	future := rumor
	future.CreatedAt = nostr.Timestamp(time.Now().Add(inboundEventMaxFutureSkew + time.Second).Unix())
	if err := bus.validateRumorEvent(future, time.Now()); err == nil {
		t.Fatal("expected future-skew validation error")
	}

	past := rumor
	past.CreatedAt = nostr.Timestamp(time.Now().Add(-nip17MaxPastAge - time.Second).Unix())
	if err := bus.validateRumorEvent(past, time.Now()); err == nil {
		t.Fatal("expected past-age validation error")
	}
}

func TestNIP17TimestampBounds(t *testing.T) {
	now := time.Now()
	if timestampTooFarFuture(now.Unix(), now, inboundEventMaxFutureSkew) {
		t.Fatal("expected current timestamp not to be future")
	}
	if !timestampTooFarFuture(now.Add(inboundEventMaxFutureSkew+time.Second).Unix(), now, inboundEventMaxFutureSkew) {
		t.Fatal("expected future timestamp to be rejected")
	}
	if !timestampTooOld(now.Add(-nip17MaxPastAge-time.Second).Unix(), now, nip17MaxPastAge) {
		t.Fatal("expected old timestamp to be rejected")
	}
}

func newTestNIP17BusIdentity(t *testing.T) (*NIP17Bus, nostr.Keyer, nostr.PubKey) {
	t.Helper()
	sk, err := ParseSecretKey("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("ParseSecretKey: %v", err)
	}
	keyer := newNIP04KeyerAdapter(sk)
	pub, err := keyer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	return &NIP17Bus{public: pub}, keyer, pub
}

func signedEvent(t *testing.T, keyer nostr.Keyer, evt nostr.Event) nostr.Event {
	t.Helper()
	if err := keyer.SignEvent(context.Background(), &evt); err != nil {
		t.Fatalf("SignEvent: %v", err)
	}
	return evt
}
