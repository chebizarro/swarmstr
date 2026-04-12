package zap

import (
	"context"
	"encoding/json"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

// ─── parseZapReceipt ──────────────────────────────────────────────────────────

func TestParseZapReceipt_Basic(t *testing.T) {
	ev := nostr.Event{
		Kind:      9735,
		CreatedAt: nostr.Timestamp(1700000000),
	}
	r := parseZapReceipt(ev)
	if r.CreatedAt != 1700000000 {
		t.Errorf("created_at: %d", r.CreatedAt)
	}
}

func TestParseZapReceipt_WithDescription(t *testing.T) {
	// Build a zap request event to embed in the description tag.
	zapReq := nostr.Event{
		Kind:    9734,
		Content: "Great post!",
		Tags: nostr.Tags{
			{"amount", "21000"},
		},
	}
	zapReqJSON, _ := json.Marshal(zapReq)

	ev := nostr.Event{
		Kind:      9735,
		CreatedAt: nostr.Timestamp(1700000000),
		Tags: nostr.Tags{
			{"description", string(zapReqJSON)},
			{"bolt11", "lnbc210n1..."},
		},
	}

	r := parseZapReceipt(ev)
	if r.AmountMsat != 21000 {
		t.Errorf("amount_msat: got %d, want 21000", r.AmountMsat)
	}
	if r.Comment != "Great post!" {
		t.Errorf("comment: got %q", r.Comment)
	}
}

func TestParseZapReceipt_InvalidDescriptionJSON(t *testing.T) {
	ev := nostr.Event{
		Kind: 9735,
		Tags: nostr.Tags{
			{"description", "not json"},
		},
	}
	r := parseZapReceipt(ev)
	// Should not panic, just leave fields empty
	if r.AmountMsat != 0 {
		t.Errorf("expected 0 amount for invalid JSON, got %d", r.AmountMsat)
	}
}

func TestParseZapReceipt_NoAmountTag(t *testing.T) {
	zapReq := nostr.Event{
		Kind:    9734,
		Content: "hello",
		Tags:    nostr.Tags{{"relays", "wss://r1"}},
	}
	zapReqJSON, _ := json.Marshal(zapReq)
	ev := nostr.Event{
		Kind: 9735,
		Tags: nostr.Tags{{"description", string(zapReqJSON)}},
	}
	r := parseZapReceipt(ev)
	if r.AmountMsat != 0 {
		t.Errorf("expected 0, got %d", r.AmountMsat)
	}
	if r.Comment != "hello" {
		t.Errorf("comment: %q", r.Comment)
	}
}

// ─── ResolveLNURL ─────────────────────────────────────────────────────────────

func TestResolveLNURL_InvalidAddress(t *testing.T) {
	_, err := ResolveLNURL(context.Background(), "notavalidaddress")
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestResolveLNURL_UnreachableHost(t *testing.T) {
	_, err := ResolveLNURL(context.Background(), "test@127.0.0.1:1")
	if err == nil {
		t.Fatal("expected network error")
	}
}

func TestSendOpts_ZeroValue(t *testing.T) {
	var opts SendOpts
	if opts.Keyer != nil {
		t.Error("zero value keyer should be nil")
	}
	if len(opts.Relays) != 0 {
		t.Error("zero value relays should be empty")
	}
}

func TestReceiveOpts_ZeroValue(t *testing.T) {
	var opts ReceiveOpts
	if opts.RecipientPubkeyHex != "" {
		t.Error("zero value pubkey should be empty")
	}
}

func TestLnurlPayMetadata_JSONRoundTrip(t *testing.T) {
	meta := lnurlPayMetadata{
		Callback:    "https://pay.example.com/callback",
		MinSendable: 1000,
		MaxSendable: 100000000,
		NostrPubkey: "abcdef",
		AllowsNostr: true,
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	var decoded lnurlPayMetadata
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != meta {
		t.Errorf("mismatch: %+v vs %+v", meta, decoded)
	}
}

// ─── Send validation ──────────────────────────────────────────────────────────

func TestSend_NilKeyer(t *testing.T) {
	_, err := Send(context.Background(), SendOpts{}, "user@example.com", "abc", "", 1000, "")
	if err == nil {
		t.Fatal("expected error for nil keyer")
	}
}

func TestSend_InvalidLNAddress(t *testing.T) {
	sk := nostr.Generate()
	kr := keyer.NewPlainKeySigner(sk)
	_, err := Send(context.Background(), SendOpts{Keyer: &kr, Relays: []string{"wss://r1"}},
		"invalid", "abc", "", 1000, "")
	if err == nil {
		t.Fatal("expected error for invalid LN address")
	}
}

// ─── StartReceiver validation ─────────────────────────────────────────────────

func TestStartReceiver_EmptyPubkey(t *testing.T) {
	_, err := StartReceiver(context.Background(), ReceiveOpts{
		OnZap:  func(ZapReceipt) {},
		Relays: []string{"wss://r1"},
	})
	if err == nil {
		t.Fatal("expected error for empty pubkey")
	}
}

func TestStartReceiver_NilOnZap(t *testing.T) {
	_, err := StartReceiver(context.Background(), ReceiveOpts{
		RecipientPubkeyHex: "abc",
		Relays:             []string{"wss://r1"},
	})
	if err == nil {
		t.Fatal("expected error for nil OnZap")
	}
}

func TestStartReceiver_NoRelays(t *testing.T) {
	_, err := StartReceiver(context.Background(), ReceiveOpts{
		RecipientPubkeyHex: "abc",
		OnZap:              func(ZapReceipt) {},
	})
	if err == nil {
		t.Fatal("expected error for empty relays")
	}
}

// ─── ZapResult / ZapReceipt struct ────────────────────────────────────────────

func TestZapResult_JSONRoundTrip(t *testing.T) {
	r := ZapResult{Invoice: "lnbc100n1...", ZapRequestID: "abc123"}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ZapResult
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != r {
		t.Errorf("round-trip: %+v vs %+v", r, decoded)
	}
}

func TestZapReceipt_JSONRoundTrip(t *testing.T) {
	r := ZapReceipt{
		ID:           "id1",
		SenderPubkey: "pk1",
		AmountMsat:   21000,
		Comment:      "zap!",
		ZapRequestID: "zr1",
		CreatedAt:    1700000000,
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ZapReceipt
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != r {
		t.Errorf("round-trip: %+v vs %+v", r, decoded)
	}
}
