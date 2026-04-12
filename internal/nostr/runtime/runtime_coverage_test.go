package runtime

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

// ─── NormalizeCapabilityValues ───────────────────────────────────────────────

func TestNormalizeCapabilityValues(t *testing.T) {
	// Empty input
	if got := NormalizeCapabilityValues(nil); got != nil {
		t.Errorf("nil: %v", got)
	}

	// Dedup and trim
	got := NormalizeCapabilityValues([]string{"  Foo ", "foo", "BAR", "bar ", ""})
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d: %v", len(got), got)
	}
}

// ─── CapabilityRegistry.All ─────────────────────────────────────────────────

func TestCapabilityRegistry_All(t *testing.T) {
	reg := NewCapabilityRegistry()
	reg.Set(CapabilityAnnouncement{PubKey: "pk1", Runtime: "metiq"})
	reg.Set(CapabilityAnnouncement{PubKey: "pk2", Runtime: "metiq"})

	all := reg.All()
	if len(all) != 2 {
		t.Errorf("expected 2, got %d", len(all))
	}
	if _, ok := all["pk1"]; !ok {
		t.Error("missing pk1")
	}
}

func TestCapabilityRegistry_All_Empty(t *testing.T) {
	reg := NewCapabilityRegistry()
	all := reg.All()
	if len(all) != 0 {
		t.Errorf("expected empty, got %d", len(all))
	}
}

// ─── PublicKeyHex / MustPublicKeyHex ─────────────────────────────────────────

func TestPublicKeyHex(t *testing.T) {
	sk := nostr.Generate()
	kr := keyer.NewPlainKeySigner(sk)
	pk, _ := kr.GetPublicKey(nil)
	hexSK := hex.EncodeToString(sk[:])

	got, err := PublicKeyHex(hexSK)
	if err != nil {
		t.Fatal(err)
	}
	if got != pk.Hex() {
		t.Errorf("expected %q, got %q", pk.Hex(), got)
	}
}

func TestPublicKeyHex_Invalid(t *testing.T) {
	_, err := PublicKeyHex("not-a-key")
	if err == nil {
		t.Error("expected error")
	}
}

func TestMustPublicKeyHex(t *testing.T) {
	sk := nostr.Generate()
	hexSK := hex.EncodeToString(sk[:])
	got := MustPublicKeyHex(hexSK)
	if got == "" {
		t.Error("expected non-empty pubkey")
	}
}

func TestMustPublicKeyHex_EmptyPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for empty key")
		}
	}()
	MustPublicKeyHex("")
}

// ─── sanitizeDMText ──────────────────────────────────────────────────────────

func TestSanitizeDMText(t *testing.T) {
	// Valid
	got, err := sanitizeDMText("hello")
	if err != nil || got != "hello" {
		t.Errorf("valid: %q, %v", got, err)
	}

	// Empty
	_, err = sanitizeDMText("")
	if err == nil {
		t.Error("expected error for empty")
	}

	// Whitespace only
	_, err = sanitizeDMText("   ")
	if err == nil {
		t.Error("expected error for whitespace")
	}

	// Too long
	long := strings.Repeat("x", maxDMPlaintextRunes+1)
	_, err = sanitizeDMText(long)
	if err == nil {
		t.Error("expected error for too long")
	}
}

// ─── sanitizeNIP17Text ──────────────────────────────────────────────────────

func TestSanitizeNIP17Text(t *testing.T) {
	got, err := sanitizeNIP17Text("hello")
	if err != nil || got != "hello" {
		t.Errorf("valid: %q, %v", got, err)
	}

	long := strings.Repeat("x", maxDMPlaintextRunes+1)
	_, err = sanitizeNIP17Text(long)
	if err == nil {
		t.Error("expected error for too long")
	}
}

// ─── NIP65RelayList.AllRelays ────────────────────────────────────────────────

func TestNIP65RelayList_AllRelays(t *testing.T) {
	list := &NIP65RelayList{
		Entries: []NIP65RelayEntry{
			{URL: "wss://relay1.example.com", Read: true, Write: true},
			{URL: "wss://relay2.example.com", Read: true},
			{URL: "wss://relay1.example.com", Write: true}, // duplicate
		},
	}
	all := list.AllRelays()
	if len(all) != 2 {
		t.Errorf("expected 2 unique relays, got %d: %v", len(all), all)
	}
}

func TestNIP65RelayList_AllRelays_Empty(t *testing.T) {
	list := &NIP65RelayList{}
	all := list.AllRelays()
	if len(all) != 0 {
		t.Errorf("expected empty, got %d", len(all))
	}
}

// ─── categoriseRelays ────────────────────────────────────────────────────────

func TestCategoriseRelays(t *testing.T) {
	both, readOnly, writeOnly := categoriseRelays(
		[]string{"wss://both.example", "wss://read-only.example"},
		[]string{"wss://both.example", "wss://write-only.example"},
		[]string{"wss://explicit-both.example"},
	)
	if len(both) != 2 { // both.example + explicit-both.example
		t.Errorf("both: %v", both)
	}
	if len(readOnly) != 1 {
		t.Errorf("readOnly: %v", readOnly)
	}
	if len(writeOnly) != 1 {
		t.Errorf("writeOnly: %v", writeOnly)
	}
}

func TestCategoriseRelays_Empty(t *testing.T) {
	both, readOnly, writeOnly := categoriseRelays(nil, nil, nil)
	if len(both)+len(readOnly)+len(writeOnly) != 0 {
		t.Error("expected all empty")
	}
}

// ─── RelayHealthTracker.NextAllowedIn ────────────────────────────────────────

func TestRelayHealthTracker_NextAllowedIn(t *testing.T) {
	tracker := NewRelayHealthTracker()

	// Unknown relay → 0
	if d := tracker.NextAllowedIn("wss://unknown.example", time.Now()); d != 0 {
		t.Errorf("unknown: %v", d)
	}

	// Empty relay → 0
	if d := tracker.NextAllowedIn("", time.Now()); d != 0 {
		t.Errorf("empty: %v", d)
	}
}

// ─── RelayHealthMonitor.Trigger ──────────────────────────────────────────────

func TestRelayHealthMonitor_Trigger(t *testing.T) {
	mon := &RelayHealthMonitor{
		triggerCh: make(chan struct{}, 1),
	}
	mon.Trigger()
	// Should not block
	select {
	case <-mon.triggerCh:
		// OK
	default:
		t.Error("trigger should have sent on channel")
	}
	// Double trigger should not block
	mon.Trigger()
	mon.Trigger()
}

// ─── unixMillisOrZero ────────────────────────────────────────────────────────

func TestUnixMillisOrZero(t *testing.T) {
	if got := unixMillisOrZero(time.Time{}); got != 0 {
		t.Errorf("zero time: %d", got)
	}
	now := time.Now()
	if got := unixMillisOrZero(now); got <= 0 {
		t.Errorf("now: %d", got)
	}
}

// ─── CapabilityMonitor setters (no Start needed) ─────────────────────────────

func TestCapabilityMonitor_UpdateMethods(t *testing.T) {
	reg := NewCapabilityRegistry()
	mon := NewCapabilityMonitor(CapabilityMonitorOptions{
		Registry: reg,
	})

	mon.UpdatePublishRelays([]string{"wss://pub.example"})
	mon.UpdateSubscribeRelays([]string{"wss://sub.example"})
	mon.UpdatePeers([]string{"hex-peer"})
	mon.UpdateLocal(CapabilityAnnouncement{Runtime: "metiq"})

	// Verify internal state via snapshot (requires Start, but we can check the monitor was set)
	mon.mu.Lock()
	if len(mon.publishRelays) != 1 {
		t.Errorf("publishRelays: %v", mon.publishRelays)
	}
	if len(mon.subscribeRelays) != 1 {
		t.Errorf("subscribeRelays: %v", mon.subscribeRelays)
	}
	if len(mon.peers) != 1 {
		t.Errorf("peers: %v", mon.peers)
	}
	if mon.local.Runtime != "metiq" {
		t.Errorf("local.Runtime: %q", mon.local.Runtime)
	}
	mon.mu.Unlock()
}
