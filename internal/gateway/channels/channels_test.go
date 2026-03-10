package channels

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

// ─── Registry ─────────────────────────────────────────────────────────────────

type stubChannel struct {
	id      string
	closed  bool
	sendErr error
	sent    []string
}

func (s *stubChannel) ID() string   { return s.id }
func (s *stubChannel) Type() string { return "stub" }
func (s *stubChannel) Close()       { s.closed = true }
func (s *stubChannel) Send(_ context.Context, text string) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, text)
	return nil
}

func TestRegistry_addAndGet(t *testing.T) {
	r := NewRegistry()
	ch := &stubChannel{id: "ch1"}
	if err := r.Add(ch); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, ok := r.Get("ch1")
	if !ok || got != ch {
		t.Error("expected to get the added channel")
	}
}

func TestRegistry_addDuplicateErrors(t *testing.T) {
	r := NewRegistry()
	r.Add(&stubChannel{id: "ch1"})
	err := r.Add(&stubChannel{id: "ch1"})
	if err == nil {
		t.Error("expected error for duplicate channel ID")
	}
}

func TestRegistry_remove(t *testing.T) {
	r := NewRegistry()
	ch := &stubChannel{id: "ch1"}
	r.Add(ch)
	if err := r.Remove("ch1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !ch.closed {
		t.Error("Remove should close the channel")
	}
	if _, ok := r.Get("ch1"); ok {
		t.Error("channel should be gone after remove")
	}
}

func TestRegistry_removeMissing(t *testing.T) {
	r := NewRegistry()
	if err := r.Remove("ghost"); err == nil {
		t.Error("expected error for missing channel")
	}
}

func TestRegistry_list(t *testing.T) {
	r := NewRegistry()
	r.Add(&stubChannel{id: "a"})
	r.Add(&stubChannel{id: "b"})
	list := r.List()
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	if list[0].ID != "a" || list[1].ID != "b" {
		t.Errorf("unexpected order: %v", list)
	}
}

func TestRegistry_closeAll(t *testing.T) {
	r := NewRegistry()
	ch1 := &stubChannel{id: "a"}
	ch2 := &stubChannel{id: "b"}
	r.Add(ch1)
	r.Add(ch2)
	r.CloseAll()
	if !ch1.closed || !ch2.closed {
		t.Error("CloseAll should close every channel")
	}
	if len(r.List()) != 0 {
		t.Error("CloseAll should clear the registry")
	}
}

func testKeyer(t *testing.T) nostr.Keyer {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("parse test key: %v", err)
	}
	return keyer.NewPlainKeySigner([32]byte(sk))
}

// ─── signer validation ────────────────────────────────────────────────────────

func TestTestKeyer_valid(t *testing.T) { _ = testKeyer(t) }

// ─── NIP29GroupChannelOptions validation ─────────────────────────────────────

func TestNewNIP29GroupChannel_missingKey(t *testing.T) {
	_, err := NewNIP29GroupChannel(context.Background(), NIP29GroupChannelOptions{
		GroupAddress: "relay.example.com'testgroup",
	})
	if err == nil {
		t.Error("expected error for missing keyer")
	}
}

func TestNewNIP29GroupChannel_missingAddress(t *testing.T) {
	_, err := NewNIP29GroupChannel(context.Background(), NIP29GroupChannelOptions{
		Keyer: testKeyer(t),
	})
	if err == nil {
		t.Error("expected error for missing group_address")
	}
}

func TestNewNIP29GroupChannel_badAddress(t *testing.T) {
	_, err := NewNIP29GroupChannel(context.Background(), NIP29GroupChannelOptions{
		Keyer:        testKeyer(t),
		GroupAddress: "noSlashInAddress",
	})
	if err == nil {
		t.Error("expected parse error for malformed group_address")
	}
}
