package main

import (
	"context"
	"testing"

	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

type dmReplyTestBus struct {
	scheme string
	to     string
	text   string
}

func (b *dmReplyTestBus) SendDM(_ context.Context, toPubKey string, text string) error {
	b.to = toPubKey
	b.text = text
	return nil
}

func (b *dmReplyTestBus) SendDMWithScheme(_ context.Context, toPubKey string, text string, scheme string) error {
	b.to = toPubKey
	b.text = text
	b.scheme = scheme
	return nil
}

func (b *dmReplyTestBus) PublicKey() string        { return "tester" }
func (b *dmReplyTestBus) Relays() []string         { return nil }
func (b *dmReplyTestBus) SetRelays([]string) error { return nil }
func (b *dmReplyTestBus) Close()                   {}

func withDMReplyTransportState(t *testing.T, fn func()) {
	t.Helper()
	prevBus := controlDMBus
	prevNIP04 := controlNIP04Bus
	prevNIP17 := controlNIP17Bus
	defer func() {
		controlDMBus = prevBus
		controlNIP04Bus = prevNIP04
		controlNIP17Bus = prevNIP17
	}()
	fn()
}

func TestResolveDMReplyTransportAutoMirrorsInboundScheme(t *testing.T) {
	withDMReplyTransportState(t, func() {
		controlNIP04Bus = &nostruntime.DMBus{}
		controlNIP17Bus = &nostruntime.NIP17Bus{}

		bus, scheme, err := resolveDMReplyTransport(state.ConfigDoc{}, "nip04")
		if err != nil {
			t.Fatalf("resolveDMReplyTransport nip04: %v", err)
		}
		if scheme != "nip04" {
			t.Fatalf("scheme = %q, want nip04", scheme)
		}
		if _, ok := bus.(*nostruntime.DMBus); !ok {
			t.Fatalf("bus type = %T, want *nostruntime.DMBus", bus)
		}

		bus, scheme, err = resolveDMReplyTransport(state.ConfigDoc{}, "nip17")
		if err != nil {
			t.Fatalf("resolveDMReplyTransport nip17: %v", err)
		}
		if scheme != "nip17" {
			t.Fatalf("scheme = %q, want nip17", scheme)
		}
		if _, ok := bus.(*nostruntime.NIP17Bus); !ok {
			t.Fatalf("bus type = %T, want *nostruntime.NIP17Bus", bus)
		}
	})
}

func TestResolveDMReplyTransportForcedSchemeOverridesInbound(t *testing.T) {
	withDMReplyTransportState(t, func() {
		controlNIP04Bus = &nostruntime.DMBus{}
		controlNIP17Bus = &nostruntime.NIP17Bus{}

		cfg := state.ConfigDoc{DM: state.DMPolicy{ReplyScheme: "nip04"}}
		bus, scheme, err := resolveDMReplyTransport(cfg, "nip17")
		if err != nil {
			t.Fatalf("resolveDMReplyTransport forced nip04: %v", err)
		}
		if scheme != "nip04" {
			t.Fatalf("scheme = %q, want nip04", scheme)
		}
		if _, ok := bus.(*nostruntime.DMBus); !ok {
			t.Fatalf("bus type = %T, want *nostruntime.DMBus", bus)
		}

		cfg = state.ConfigDoc{DM: state.DMPolicy{ReplyScheme: "nip17"}}
		bus, scheme, err = resolveDMReplyTransport(cfg, "nip04")
		if err != nil {
			t.Fatalf("resolveDMReplyTransport forced nip17: %v", err)
		}
		if scheme != "nip17" {
			t.Fatalf("scheme = %q, want nip17", scheme)
		}
		if _, ok := bus.(*nostruntime.NIP17Bus); !ok {
			t.Fatalf("bus type = %T, want *nostruntime.NIP17Bus", bus)
		}
	})
}

func TestSendDMReplyWithTransportUsesExplicitScheme(t *testing.T) {
	bus := &dmReplyTestBus{}
	if err := sendDMReplyWithTransport(context.Background(), bus, "nip04", "peer", "hello"); err != nil {
		t.Fatalf("sendDMReplyWithTransport: %v", err)
	}
	if bus.scheme != "nip04" || bus.to != "peer" || bus.text != "hello" {
		t.Fatalf("unexpected send capture: %+v", bus)
	}
}

func TestWrapInboundDMReplyAutoUsesInboundReply(t *testing.T) {
	var inboundTo, inboundText string
	msg := wrapInboundDMReply(func() state.ConfigDoc { return state.ConfigDoc{} }, nostruntime.InboundDM{
		FromPubKey: "peer-auto",
		Scheme:     "nip04",
		Reply: func(_ context.Context, text string) error {
			inboundTo = "inbound"
			inboundText = text
			return nil
		},
	})
	if err := msg.Reply(context.Background(), "hello-auto"); err != nil {
		t.Fatalf("wrapped auto reply: %v", err)
	}
	if inboundTo != "inbound" || inboundText != "hello-auto" {
		t.Fatalf("expected inbound reply path, got to=%q text=%q", inboundTo, inboundText)
	}
}

func TestWrapInboundDMReplyRejectsUnknownInboundScheme(t *testing.T) {
	msg := wrapInboundDMReply(func() state.ConfigDoc { return state.ConfigDoc{} }, nostruntime.InboundDM{
		FromPubKey: "peer-unknown",
		Scheme:     "bogus",
		Reply: func(_ context.Context, _ string) error {
			t.Fatal("expected unknown inbound scheme to fail before using inbound reply")
			return nil
		},
	})
	if err := msg.Reply(context.Background(), "hello-unknown"); err == nil {
		t.Fatal("expected wrapped reply to reject unknown inbound scheme")
	}
}
