package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

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
	b := &DMBus{
		ks:           newNIP04KeyerAdapter(recipientSK),
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
