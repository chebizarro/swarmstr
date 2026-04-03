package runtime

import (
	"context"
	"reflect"
	"testing"

	nostr "fiatjaf.com/nostr"
)

func TestNewHub(t *testing.T) {
	keyer := &mockKeyer{}
	sel := NewRelaySelector([]string{"wss://r1.example.com"}, []string{"wss://r2.example.com"})

	hub, err := NewHub(context.Background(), keyer, sel)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	defer hub.Close()

	if hub.Pool() == nil {
		t.Fatal("expected non-nil pool")
	}
	if hub.Keyer() == nil {
		t.Fatal("expected non-nil keyer")
	}
	if hub.Selector() == nil {
		t.Fatal("expected non-nil selector")
	}
	if hub.PublicKey() == "" {
		t.Fatal("expected non-empty public key")
	}
}

func TestNewHub_NilKeyer(t *testing.T) {
	_, err := NewHub(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for nil keyer")
	}
}

func TestHub_ReadWriteRelays(t *testing.T) {
	keyer := &mockKeyer{}
	sel := NewRelaySelector([]string{"wss://read.example.com"}, []string{"wss://write.example.com"})

	hub, err := NewHub(context.Background(), keyer, sel)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	defer hub.Close()

	read := hub.ReadRelays()
	if len(read) != 1 || read[0] != "wss://read.example.com" {
		t.Fatalf("unexpected read relays: %v", read)
	}
	write := hub.WriteRelays()
	if len(write) != 1 || write[0] != "wss://write.example.com" {
		t.Fatalf("unexpected write relays: %v", write)
	}
}

func TestHub_ResolveRelays(t *testing.T) {
	keyer := &mockKeyer{}
	sel := NewRelaySelector([]string{"wss://default.example.com"}, nil)

	hub, err := NewHub(context.Background(), keyer, sel)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	defer hub.Close()

	// With override.
	override := []string{"wss://custom.example.com"}
	got := hub.ResolveRelays(override)
	if len(got) != 1 || got[0] != "wss://custom.example.com" {
		t.Fatalf("expected override relay, got: %v", got)
	}

	// Without override — falls back.
	got = hub.ResolveRelays(nil)
	if len(got) != 1 || got[0] != "wss://default.example.com" {
		t.Fatalf("expected fallback relay, got: %v", got)
	}
}

func TestHub_SubscribeRequiresOnEvent(t *testing.T) {
	keyer := &mockKeyer{}
	hub, _ := NewHub(context.Background(), keyer, NewRelaySelector([]string{"wss://r.example.com"}, nil))
	defer hub.Close()

	_, err := hub.Subscribe(context.Background(), SubOpts{
		ID:     "test",
		Relays: []string{"wss://r.example.com"},
	})
	if err == nil {
		t.Fatal("expected error when OnEvent is nil")
	}
}

func TestHub_SubscribeDuplicate(t *testing.T) {
	keyer := &mockKeyer{}
	hub, _ := NewHub(context.Background(), keyer, NewRelaySelector([]string{"wss://r.example.com"}, nil))
	defer hub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	noop := func(nostr.RelayEvent) {}

	_, err := hub.Subscribe(ctx, SubOpts{
		ID:      "dup-test",
		Relays:  []string{"wss://r.example.com"},
		OnEvent: noop,
	})
	if err != nil {
		t.Fatalf("first subscribe: %v", err)
	}

	_, err = hub.Subscribe(ctx, SubOpts{
		ID:      "dup-test",
		Relays:  []string{"wss://r.example.com"},
		OnEvent: noop,
	})
	if err == nil {
		t.Fatal("expected error for duplicate subscription ID")
	}

	cancel()
}

func TestHub_Unsubscribe(t *testing.T) {
	keyer := &mockKeyer{}
	hub, _ := NewHub(context.Background(), keyer, NewRelaySelector([]string{"wss://r.example.com"}, nil))
	defer hub.Close()

	ok := hub.Unsubscribe("nonexistent")
	if ok {
		t.Fatal("expected false for nonexistent subscription")
	}
}

func TestHub_Subscriptions(t *testing.T) {
	keyer := &mockKeyer{}
	hub, _ := NewHub(context.Background(), keyer, nil)
	defer hub.Close()

	subs := hub.Subscriptions()
	if len(subs) != 0 {
		t.Fatalf("expected 0 subscriptions, got %d", len(subs))
	}
}

func TestSubOptsDoesNotAdvertiseUnsupportedEOSECallback(t *testing.T) {
	typ := reflect.TypeOf(SubOpts{})
	if _, ok := typ.FieldByName("OnEOSE"); ok {
		t.Fatal("SubOpts must not expose unsupported OnEOSE callback")
	}
	if _, ok := typ.FieldByName("OnClosed"); !ok {
		t.Fatal("SubOpts should expose OnClosed callback")
	}
	if _, ok := typ.FieldByName("OnEnd"); !ok {
		t.Fatal("SubOpts should expose OnEnd callback")
	}
}

func TestShouldEmitOnEnd(t *testing.T) {
	if !shouldEmitOnEnd(context.Background()) {
		t.Fatal("expected active context to emit OnEnd")
	}

	subCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if shouldEmitOnEnd(subCtx) {
		t.Fatal("expected canceled context to suppress OnEnd")
	}
}
