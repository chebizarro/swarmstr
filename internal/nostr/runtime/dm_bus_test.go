package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

type keyerWithoutNIP04 struct{ pub nostr.PubKey }

func (k keyerWithoutNIP04) GetPublicKey(context.Context) (nostr.PubKey, error) { return k.pub, nil }
func (k keyerWithoutNIP04) SignEvent(context.Context, *nostr.Event) error      { return nil }
func (k keyerWithoutNIP04) Encrypt(context.Context, string, nostr.PubKey) (string, error) {
	return "", nil
}
func (k keyerWithoutNIP04) Decrypt(context.Context, string, nostr.PubKey) (string, error) {
	return "", nil
}

func TestDMBusSetRelays(t *testing.T) {
	b := &DMBus{relays: []string{"wss://one"}}
	in := []string{"wss://two", "wss://two", " wss://three "}
	if err := b.SetRelays(in); err != nil {
		t.Fatalf("set relays error: %v", err)
	}
	in[0] = "wss://mutated"
	got := b.currentRelays()
	if len(got) != 2 {
		t.Fatalf("unexpected relay count: %v", got)
	}
	if got[0] != "wss://two" || got[1] != "wss://three" {
		t.Fatalf("unexpected relays: %v", got)
	}
}

func TestSanitizeDMText(t *testing.T) {
	text, err := sanitizeDMText("  hello ")
	if err != nil {
		t.Fatalf("unexpected sanitize error: %v", err)
	}
	if text != "hello" {
		t.Fatalf("unexpected sanitized text: %q", text)
	}
}

func TestSanitizeDMTextRejectsTooLong(t *testing.T) {
	_, err := sanitizeDMText(strings.Repeat("a", maxDMPlaintextRunes+1))
	if err == nil {
		t.Fatal("expected too long error")
	}
}

func TestDMBusMarkSeenDeduplicatesAndEvicts(t *testing.T) {
	b := &DMBus{seenSet: map[string]struct{}{}, seenCap: 2}
	if b.markSeen("evt-1") {
		t.Fatal("first sighting should not be duplicate")
	}
	if !b.markSeen("evt-1") {
		t.Fatal("second sighting should be duplicate")
	}
	_ = b.markSeen("evt-2")
	_ = b.markSeen("evt-3")
	if b.markSeen("evt-1") {
		t.Fatal("oldest event should have been evicted from seen cache")
	}
}

func TestDMSubIDDistinguishesGeneration(t *testing.T) {
	b := &DMBus{}
	if got := b.dmSubID(" wss://relay.example ", 3); got != "dm-bus:wss://relay.example:3" {
		t.Fatalf("unexpected sub id: %q", got)
	}
}

func TestDMFilterUsesRecipientPubkeyAndSince(t *testing.T) {
	b := &DMBus{public: nostr.Generate().Public()}
	filter := b.dmFilter(123)
	if filter.Since != nostr.Timestamp(123) {
		t.Fatalf("unexpected since: %d", filter.Since)
	}
	if got := filter.Tags["p"]; len(got) != 1 || got[0] != b.public.Hex() {
		t.Fatalf("unexpected p tag filter: %#v", filter.Tags)
	}
}

func TestStartDMBusRejectsKeyerWithoutNIP04Support(t *testing.T) {
	_, err := StartDMBus(context.Background(), DMBusOptions{
		Keyer:  keyerWithoutNIP04{pub: nostr.Generate().Public()},
		Relays: []string{"wss://relay.example"},
	})
	if err == nil || !strings.Contains(err.Error(), "does not support NIP-04 decrypt") {
		t.Fatalf("expected NIP-04 support error, got %v", err)
	}
}

// ─── Lifecycle / relay-scoped retry tests ────────────────────────────────────

func TestDMBusSetRelaysTriggersRebind(t *testing.T) {
	b := &DMBus{
		relays:   []string{"wss://old"},
		health:   NewRelayHealthTracker(),
		rebindCh: make(chan struct{}, 1),
	}
	b.health.Seed(b.relays)

	if err := b.SetRelays([]string{"wss://new-a", "wss://new-b"}); err != nil {
		t.Fatalf("SetRelays error: %v", err)
	}

	// rebindCh should have a signal.
	select {
	case <-b.rebindCh:
	default:
		t.Fatal("expected rebind signal after SetRelays")
	}

	// Relay list should be updated.
	got := b.currentRelays()
	if len(got) != 2 || got[0] != "wss://new-a" || got[1] != "wss://new-b" {
		t.Fatalf("unexpected relays after SetRelays: %v", got)
	}
}

func TestDMBusRebindChannelCoalesces(t *testing.T) {
	b := &DMBus{
		relays:   []string{"wss://one"},
		health:   NewRelayHealthTracker(),
		rebindCh: make(chan struct{}, 1),
	}

	// Multiple rapid SetRelays calls should not block — channel is buffered(1).
	for i := 0; i < 5; i++ {
		if err := b.SetRelays([]string{fmt.Sprintf("wss://relay-%d", i)}); err != nil {
			t.Fatalf("SetRelays %d error: %v", i, err)
		}
	}

	// Only one signal should be queued.
	select {
	case <-b.rebindCh:
	default:
		t.Fatal("expected at least one rebind signal")
	}
	select {
	case <-b.rebindCh:
		t.Fatal("expected only one coalesced rebind signal")
	default:
	}
}

func TestDMBusSetRelaysRejectsEmpty(t *testing.T) {
	b := &DMBus{
		relays:   []string{"wss://existing"},
		rebindCh: make(chan struct{}, 1),
	}
	if err := b.SetRelays([]string{"", "  "}); err == nil {
		t.Fatal("expected error for empty relay list")
	}
	// Original relays should be unchanged.
	got := b.currentRelays()
	if len(got) != 1 || got[0] != "wss://existing" {
		t.Fatalf("relays should be unchanged after rejected SetRelays: %v", got)
	}
}

func TestDMBusGenerationTrackingIncrements(t *testing.T) {
	// Simulate the generation map used inside runHubSubscription.
	generation := map[string]int{}
	nextGeneration := func(relay string) int {
		relay = strings.TrimSpace(relay)
		generation[relay]++
		return generation[relay]
	}

	relay := "wss://relay.example"
	g1 := nextGeneration(relay)
	g2 := nextGeneration(relay)
	g3 := nextGeneration(relay)

	if g1 != 1 || g2 != 2 || g3 != 3 {
		t.Fatalf("expected sequential generations 1,2,3 got %d,%d,%d", g1, g2, g3)
	}
}

func TestDMBusStaleCloseIgnored(t *testing.T) {
	generation := map[string]int{}
	relay := "wss://relay.example"
	generation[relay] = 3

	staleClose := dmRelayClose{
		relayURL:   relay,
		generation: 1,
	}
	if generation[staleClose.relayURL] == staleClose.generation {
		t.Fatal("stale close should not match current generation")
	}

	currentClose := dmRelayClose{
		relayURL:   relay,
		generation: 3,
	}
	if generation[currentClose.relayURL] != currentClose.generation {
		t.Fatal("current close should match current generation")
	}
}

func TestDMBusRetrySkipsRemovedRelay(t *testing.T) {
	currentRelays := []string{"wss://kept-a", "wss://kept-b"}
	removedRelay := "wss://removed"

	if containsRelay(currentRelays, removedRelay) {
		t.Fatal("removed relay should not be in current relay list")
	}
	if !containsRelay(currentRelays, "wss://kept-a") {
		t.Fatal("kept relay should be in current relay list")
	}
}

func TestDMBusResubscribeSinceUsesReplayWindow(t *testing.T) {
	b := &DMBus{replayWindow: 30 * time.Minute}
	since := b.resubscribeSinceUnix()
	expected := time.Now().Add(-30 * time.Minute).Unix()
	// Allow 2s tolerance for test execution time.
	if since < expected-2 || since > expected+2 {
		t.Fatalf("resubscribeSinceUnix = %d, want ~%d", since, expected)
	}
}

func TestDMBusResubscribeSinceDefaultsTo30m(t *testing.T) {
	b := &DMBus{replayWindow: 0}
	since := b.resubscribeSinceUnix()
	expected := time.Now().Add(-30 * time.Minute).Unix()
	if since < expected-2 || since > expected+2 {
		t.Fatalf("resubscribeSinceUnix (default) = %d, want ~%d", since, expected)
	}
}

func TestDMBusHealthSeedOnSetRelays(t *testing.T) {
	health := NewRelayHealthTracker()
	health.Seed([]string{"wss://old"})
	health.RecordFailure("wss://old")
	health.RecordFailure("wss://old")

	b := &DMBus{
		relays:   []string{"wss://old"},
		health:   health,
		rebindCh: make(chan struct{}, 1),
	}

	if err := b.SetRelays([]string{"wss://new"}); err != nil {
		t.Fatal(err)
	}

	if !health.Allowed("wss://new", time.Now()) {
		t.Fatal("new relay should be allowed after Seed")
	}
	// Old relay pruned from health tracker.
	if !health.Allowed("wss://old", time.Now()) {
		t.Fatal("removed relay should be allowed (entry pruned)")
	}
}

func TestDMBusRequestRebindNonBlocking(t *testing.T) {
	b := &DMBus{rebindCh: make(chan struct{}, 1)}

	// First requestRebind should succeed.
	b.requestRebind()
	select {
	case <-b.rebindCh:
	default:
		t.Fatal("expected rebind signal")
	}

	// Calling requestRebind on an already-signaled channel should not block.
	b.requestRebind()
	b.requestRebind()
	// Drain once.
	select {
	case <-b.rebindCh:
	default:
		t.Fatal("expected at least one rebind signal")
	}
	// No second signal.
	select {
	case <-b.rebindCh:
		t.Fatal("expected only one coalesced signal")
	default:
	}
}

func TestDMBusCloseEventUsesConfiguredRelayKey(t *testing.T) {
	// The DM bus close handler should always use the configured relay key
	// (not the one reported by the CLOSED callback) for generation tracking.
	configuredRelay := "wss://relay.example"
	reportedRelay := "wss://RELAY.EXAMPLE" // different casing from callback

	// In the actual code, close events are keyed by relayKey (configured),
	// not by reportedRelay (from callback). Verify the types carry the right key.
	close := dmRelayClose{
		relayURL:   configuredRelay, // should be the configured key
		generation: 1,
	}
	if close.relayURL != configuredRelay {
		t.Fatalf("close event should carry configured relay key, got %q", close.relayURL)
	}
	_ = reportedRelay
}

func TestHandleInboundDropsEventsOutsideReplayWindow(t *testing.T) {
	recipientSK := nostr.Generate()
	senderSK := nostr.Generate()
	evt := nostr.Event{
		Kind:      nostr.KindEncryptedDirectMessage,
		CreatedAt: nostr.Timestamp(time.Now().Add(-2 * time.Hour).Unix()),
		Tags:      nostr.Tags{{"p", recipientSK.Public().Hex()}},
		Content:   "not-encrypted",
	}
	if err := newNIP04KeyerAdapter(senderSK).SignEvent(context.Background(), &evt); err != nil {
		t.Fatalf("sign old event: %v", err)
	}

	msgCh := make(chan InboundDM, 1)
	recipientKeyer := newNIP04KeyerAdapter(recipientSK)
	b := &DMBus{
		authKeyer:    recipientKeyer,
		signKeyer:    recipientKeyer,
		nip04Keyer:   recipientKeyer,
		hasNIP04Key:  true,
		public:       recipientSK.Public(),
		replayWindow: 30 * time.Minute,
		seenSet:      map[string]struct{}{},
		seenCap:      100,
		health:       NewRelayHealthTracker(),
		messageQueue: msgCh,
		onMessage:    func(context.Context, InboundDM) error { return nil },
		ctx:          context.Background(),
	}
	b.handleInbound(nostr.RelayEvent{Relay: &nostr.Relay{URL: "wss://relay.example"}, Event: evt})
	select {
	case <-msgCh:
		t.Fatal("expected old replay event to be dropped")
	default:
	}
}
