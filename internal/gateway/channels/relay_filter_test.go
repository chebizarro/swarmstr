package channels

import (
	"context"
	"reflect"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
)

func mustSignedRelayFilterEvent(t *testing.T) nostr.Event {
	t.Helper()
	k := testKeyer(t)
	evt := nostr.Event{
		Kind:      nostr.KindTextNote,
		CreatedAt: nostr.Now(),
		Content:   "hello from relay-filter test",
	}
	if err := k.SignEvent(context.Background(), &evt); err != nil {
		t.Fatalf("SignEvent: %v", err)
	}
	return evt
}

func TestIsVerifiedRelayFilterEvent(t *testing.T) {
	valid := mustSignedRelayFilterEvent(t)
	if !isVerifiedRelayFilterEvent(valid) {
		t.Fatal("expected signed event to verify")
	}

	tampered := valid
	tampered.Content = "forged content"
	if isVerifiedRelayFilterEvent(tampered) {
		t.Fatal("expected tampered event to be rejected")
	}
}

func TestRelayFilterClosedErrorHandlesNilRelay(t *testing.T) {
	err := relayFilterClosedError(nostr.RelayClosed{Reason: "rate-limited"})
	if err == nil {
		t.Fatal("expected CLOSED error")
	}
	if !strings.Contains(err.Error(), "<unknown relay>") {
		t.Fatalf("expected unknown relay marker, got %q", err)
	}
	if !strings.Contains(err.Error(), "rate-limited") {
		t.Fatalf("expected CLOSED reason in error, got %q", err)
	}
}

func TestRelayFilterClosedErrorUsesRelayURL(t *testing.T) {
	err := relayFilterClosedError(nostr.RelayClosed{
		Reason: "policy",
		Relay:  &nostr.Relay{URL: "wss://relay.example"},
	})
	if err == nil {
		t.Fatal("expected CLOSED error")
	}
	if !strings.Contains(err.Error(), "wss://relay.example") {
		t.Fatalf("expected relay URL in error, got %q", err)
	}
}

func TestRelayFilterClosedErrorReturnsNilForHandledAuth(t *testing.T) {
	if err := relayFilterClosedError(nostr.RelayClosed{HandledAuth: true}); err != nil {
		t.Fatalf("expected nil for handled auth close, got %v", err)
	}
}

func TestSanitizeRelayFilterRelays(t *testing.T) {
	got := sanitizeRelayFilterRelays([]string{" wss://relay-a ", "wss://relay-a", "", "  ", "wss://relay-b"})
	want := []string{"wss://relay-a", "wss://relay-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitized relays = %v, want %v", got, want)
	}
}

func TestNewRelayFilterChannelRejectsEmptyRelaysAfterSanitize(t *testing.T) {
	_, err := NewRelayFilterChannel(context.Background(), RelayFilterChannelOptions{
		ID:     "relay-filter-test",
		Keyer:  testKeyer(t),
		Relays: []string{" ", ""},
	})
	if err == nil {
		t.Fatal("expected error when relay list is empty after sanitize")
	}
}
