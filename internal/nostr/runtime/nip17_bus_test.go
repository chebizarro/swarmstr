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
	rumor := unsignedRumorEvent(t, keyer, nostr.Event{
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

func TestNIP17TimestampBoundsAtExactThresholds(t *testing.T) {
	now := time.Unix(time.Now().Unix(), 0)
	if timestampTooFarFuture(now.Add(inboundEventMaxFutureSkew).Unix(), now, inboundEventMaxFutureSkew) {
		t.Fatal("timestamp at exact future skew threshold should be accepted")
	}
	if timestampTooOld(now.Add(-nip17MaxPastAge).Unix(), now, nip17MaxPastAge) {
		t.Fatal("timestamp at exact max-past-age threshold should be accepted")
	}
}

func TestNIP17HandleRumorDeduplicatesByRumorID(t *testing.T) {
	bus, _, recipient := newTestNIP17BusIdentity(t)
	sender := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	rumor := unsignedRumorEvent(t, sender, nostr.Event{
		Kind:      nostr.KindDirectMessage,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", recipient.Hex()}},
		Content:   "hello from two relays",
	})
	bus.ctx = context.Background()
	bus.seenSet = map[string]struct{}{}
	bus.seenCap = 16
	bus.messageQueue = make(chan InboundDM, 2)
	bus.onMessage = func(context.Context, InboundDM) error { return nil }

	bus.handleRumor(rumor)
	bus.handleRumor(rumor)

	select {
	case msg := <-bus.messageQueue:
		if msg.EventID != rumor.ID.Hex() || msg.Text != "hello from two relays" {
			t.Fatalf("unexpected message: %+v", msg)
		}
	default:
		t.Fatal("expected first rumor delivery to enqueue message")
	}
	select {
	case msg := <-bus.messageQueue:
		t.Fatalf("duplicate rumor should not enqueue: %+v", msg)
	default:
	}
	if !bus.markSeen17(rumor.ID.Hex()) {
		t.Fatal("rumor should remain enrolled in seen-set after first delivery")
	}
}

func TestNIP17BusCloseNilAndPartial(t *testing.T) {
	var nilBus *NIP17Bus
	nilBus.Close()
	(&NIP17Bus{}).Close()
}

func TestStartNIP17BusRejectsMismatchedHubPubKey(t *testing.T) {
	hubKey := newNIP04KeyerAdapter(mustSecretKey(t, "1111111111111111111111111111111111111111111111111111111111111111"))
	hub, err := NewHub(context.Background(), hubKey, nil)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	defer hub.Close()

	busKey := newNIP04KeyerAdapter(mustSecretKey(t, "2222222222222222222222222222222222222222222222222222222222222222"))
	_, err = StartNIP17Bus(context.Background(), NIP17BusOptions{
		Keyer:  busKey,
		Relays: []string{"wss://relay.example"},
		Hub:    hub,
	})
	if err == nil || err.Error() != "nip17 bus: hub pubkey does not match keyer pubkey" {
		t.Fatalf("expected hub mismatch error, got %v", err)
	}
}

func TestNIP17ReceiveLoopRestartsFromClosedGiftWrapStreamWithReplayWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type listenCall struct {
		relays []string
		since  nostr.Timestamp
		ch     chan nostr.Event
	}
	calls := make(chan listenCall, 2)
	bus := &NIP17Bus{
		ctx:          ctx,
		cancel:       cancel,
		relays:       []string{"wss://gift.example"},
		rebindCh:     make(chan struct{}, 1),
		messageQueue: make(chan InboundDM, 1),
		subHealth:    NewSubHealthTracker("nip17"),
		testListenGiftWraps: func(ctx context.Context, relays []string, since nostr.Timestamp) <-chan nostr.Event {
			ch := make(chan nostr.Event)
			calls <- listenCall{relays: append([]string{}, relays...), since: since, ch: ch}
			return ch
		},
		onError: func(error) {},
	}
	initialSince := nostr.Timestamp(time.Now().Add(-time.Hour).Unix())
	bus.wg.Add(1)
	go bus.receiveLoop(initialSince)

	first := receiveBeforeTestDeadline(t, calls, "first nip17 gift-wrap listener")
	if len(first.relays) != 1 || first.relays[0] != "wss://gift.example" {
		t.Fatalf("unexpected relays: %v", first.relays)
	}
	if first.since != initialSince {
		t.Fatalf("first since = %d, want %d", first.since, initialSince)
	}

	beforeReplay := normalizeNIP17Since(time.Now().Unix())
	close(first.ch)
	second := receiveBeforeTestDeadline(t, calls, "second nip17 gift-wrap listener")
	afterReplay := normalizeNIP17Since(time.Now().Unix())
	if int64(second.since) < beforeReplay || int64(second.since) > afterReplay {
		t.Fatalf("restart since = %d, want NIP-59 replay window within [%d,%d]", second.since, beforeReplay, afterReplay)
	}

	cancel()
	close(second.ch)
	bus.wg.Wait()
}

func TestNIP17EOSEChannelCanBeDisabledAfterFirstSignal(t *testing.T) {
	eoseCh := make(chan struct{})
	close(eoseCh)

	select {
	case <-eoseCh:
		eoseCh = nil
	default:
		t.Fatal("expected closed EOSE channel to be readable")
	}

	select {
	case <-eoseCh:
		t.Fatal("disabled EOSE channel should not fire again")
	default:
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

func unsignedRumorEvent(t *testing.T, keyer nostr.Keyer, evt nostr.Event) nostr.Event {
	t.Helper()
	pub, err := keyer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	evt.PubKey = pub
	evt.ID = evt.GetID()
	return evt
}
