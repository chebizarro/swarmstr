package runtime

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ── Mock DMTransport ──────────────────────────────────────────────────────────

type mockTransport struct {
	name     string
	pubkey   string
	relays   []string
	sendErr  error
	closed   bool
	mu       sync.Mutex
	sendLog  []string // records toPubKey of each SendDM call
}

func (m *mockTransport) SendDM(_ context.Context, toPubKey string, _ string) error {
	m.mu.Lock()
	m.sendLog = append(m.sendLog, toPubKey)
	m.mu.Unlock()
	return m.sendErr
}
func (m *mockTransport) PublicKey() string        { return m.pubkey }
func (m *mockTransport) Relays() []string         { return m.relays }
func (m *mockTransport) SetRelays(r []string) error { m.relays = r; return nil }
func (m *mockTransport) Close()                   { m.closed = true }

func (m *mockTransport) sendCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sendLog)
}

// ── Constructor tests ─────────────────────────────────────────────────────────

func TestNewTransportSelector_defaults(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "aaa"}
	relay := &mockTransport{name: "relay", pubkey: "aaa"}

	ts, err := NewTransportSelector(TransportSelectorOptions{
		FIPS:  fips,
		Relay: relay,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.Pref() != TransportPrefFIPSFirst {
		t.Fatalf("expected default pref fips-first, got %q", ts.Pref())
	}
	if !ts.HasFIPS() {
		t.Fatal("expected HasFIPS=true")
	}
	if !ts.HasRelay() {
		t.Fatal("expected HasRelay=true")
	}
}

func TestNewTransportSelector_fips_only_requires_fips(t *testing.T) {
	relay := &mockTransport{name: "relay"}

	_, err := NewTransportSelector(TransportSelectorOptions{
		Relay: relay,
		Pref:  TransportPrefFIPSOnly,
	})
	if err == nil {
		t.Fatal("expected error for fips-only without FIPS transport")
	}
}

func TestNewTransportSelector_relay_first_requires_relay(t *testing.T) {
	fips := &mockTransport{name: "fips"}

	_, err := NewTransportSelector(TransportSelectorOptions{
		FIPS: fips,
		Pref: TransportPrefRelayFirst,
	})
	if err == nil {
		t.Fatal("expected error for relay-first without relay transport")
	}
}

func TestNewTransportSelector_fips_first_needs_at_least_one(t *testing.T) {
	_, err := NewTransportSelector(TransportSelectorOptions{
		Pref: TransportPrefFIPSFirst,
	})
	if err == nil {
		t.Fatal("expected error when no transports provided")
	}
}

func TestNewTransportSelector_unknown_pref(t *testing.T) {
	_, err := NewTransportSelector(TransportSelectorOptions{
		FIPS: &mockTransport{},
		Pref: "mesh-only",
	})
	if err == nil {
		t.Fatal("expected error for unknown preference")
	}
}

// ── fips-first routing ────────────────────────────────────────────────────────

func TestFIPSFirst_sends_via_fips_when_reachable(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:      fips,
		Relay:     relay,
		Pref:      TransportPrefFIPSFirst,
		Reachable: func(_ string) bool { return true },
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if err != nil {
		t.Fatalf("SendDM: %v", err)
	}
	if fips.sendCount() != 1 {
		t.Fatalf("expected 1 FIPS send, got %d", fips.sendCount())
	}
	if relay.sendCount() != 0 {
		t.Fatalf("expected 0 relay sends, got %d", relay.sendCount())
	}
}

func TestFIPSFirst_falls_back_to_relay_on_fips_error(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fmt.Errorf("mesh unreachable")}
	relay := &mockTransport{name: "relay", pubkey: "abc"}
	var fallbackCalled bool

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:      fips,
		Relay:     relay,
		Pref:      TransportPrefFIPSFirst,
		Reachable: func(_ string) bool { return true },
		OnFallback: func(toPubKey, preferred string, err error) {
			fallbackCalled = true
			if preferred != "fips" {
				t.Errorf("expected preferred=fips, got %q", preferred)
			}
		},
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if err != nil {
		t.Fatalf("SendDM should succeed via relay fallback: %v", err)
	}
	if fips.sendCount() != 1 {
		t.Fatalf("expected 1 FIPS attempt, got %d", fips.sendCount())
	}
	if relay.sendCount() != 1 {
		t.Fatalf("expected 1 relay send, got %d", relay.sendCount())
	}
	if !fallbackCalled {
		t.Fatal("expected fallback callback")
	}
}

func TestFIPSFirst_skips_fips_when_peer_unreachable(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:      fips,
		Relay:     relay,
		Pref:      TransportPrefFIPSFirst,
		Reachable: func(_ string) bool { return false },
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if err != nil {
		t.Fatalf("SendDM: %v", err)
	}
	if fips.sendCount() != 0 {
		t.Fatalf("expected 0 FIPS sends (peer unreachable), got %d", fips.sendCount())
	}
	if relay.sendCount() != 1 {
		t.Fatalf("expected 1 relay send, got %d", relay.sendCount())
	}
}

func TestFIPSFirst_optimistic_when_no_reachability_checker(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  fips,
		Relay: relay,
		Pref:  TransportPrefFIPSFirst,
		// No Reachable function — should optimistically try FIPS.
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if err != nil {
		t.Fatalf("SendDM: %v", err)
	}
	if fips.sendCount() != 1 {
		t.Fatalf("expected 1 FIPS send (optimistic), got %d", fips.sendCount())
	}
}

func TestFIPSFirst_relay_only_when_no_fips(t *testing.T) {
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		Relay: relay,
		Pref:  TransportPrefFIPSFirst,
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if err != nil {
		t.Fatalf("SendDM: %v", err)
	}
	if relay.sendCount() != 1 {
		t.Fatalf("expected 1 relay send, got %d", relay.sendCount())
	}
}

// ── relay-first routing ───────────────────────────────────────────────────────

func TestRelayFirst_sends_via_relay(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  fips,
		Relay: relay,
		Pref:  TransportPrefRelayFirst,
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if err != nil {
		t.Fatalf("SendDM: %v", err)
	}
	if relay.sendCount() != 1 {
		t.Fatalf("expected 1 relay send, got %d", relay.sendCount())
	}
	if fips.sendCount() != 0 {
		t.Fatalf("expected 0 FIPS sends, got %d", fips.sendCount())
	}
}

func TestRelayFirst_uses_fips_for_explicitly_reachable_peer(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:      fips,
		Relay:     relay,
		Pref:      TransportPrefRelayFirst,
		Reachable: func(pk string) bool { return pk == "fips-peer" },
	})

	// Non-FIPS peer → relay.
	ts.SendDM(context.Background(), "relay-peer", "hi")
	if relay.sendCount() != 1 {
		t.Fatalf("expected 1 relay send, got %d", relay.sendCount())
	}
	if fips.sendCount() != 0 {
		t.Fatalf("expected 0 FIPS sends, got %d", fips.sendCount())
	}

	// FIPS peer → FIPS.
	ts.SendDM(context.Background(), "fips-peer", "hi")
	if fips.sendCount() != 1 {
		t.Fatalf("expected 1 FIPS send, got %d", fips.sendCount())
	}
}

func TestRelayFirst_falls_back_when_fips_fails_for_reachable_peer(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fmt.Errorf("timeout")}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:      fips,
		Relay:     relay,
		Pref:      TransportPrefRelayFirst,
		Reachable: func(_ string) bool { return true },
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if err != nil {
		t.Fatalf("SendDM should succeed via relay fallback: %v", err)
	}
	if fips.sendCount() != 1 {
		t.Fatalf("expected 1 FIPS attempt, got %d", fips.sendCount())
	}
	if relay.sendCount() != 1 {
		t.Fatalf("expected 1 relay send, got %d", relay.sendCount())
	}
}

// ── fips-only routing ─────────────────────────────────────────────────────────

func TestFIPSOnly_sends_via_fips(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS: fips,
		Pref: TransportPrefFIPSOnly,
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if err != nil {
		t.Fatalf("SendDM: %v", err)
	}
	if fips.sendCount() != 1 {
		t.Fatalf("expected 1 FIPS send, got %d", fips.sendCount())
	}
}

func TestFIPSOnly_returns_error_on_fips_failure(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fmt.Errorf("dead")}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS: fips,
		Pref: TransportPrefFIPSOnly,
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if err == nil {
		t.Fatal("expected error in fips-only mode")
	}
}

// ── Reachability cache ────────────────────────────────────────────────────────

func TestReachabilityCache_positive_avoids_recheck(t *testing.T) {
	checkCount := 0
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS: fips,
		Relay: relay,
		Pref: TransportPrefFIPSFirst,
		Reachable: func(_ string) bool {
			checkCount++
			return true
		},
		ReachCacheTTL: 1 * time.Minute,
	})

	// First send — checker called.
	ts.SendDM(context.Background(), "peer1", "a")
	if checkCount != 1 {
		t.Fatalf("expected 1 reachability check, got %d", checkCount)
	}

	// Second send — should use cached result.
	ts.SendDM(context.Background(), "peer1", "b")
	if checkCount != 1 {
		t.Fatalf("expected still 1 reachability check (cached), got %d", checkCount)
	}
}

func TestReachabilityCache_negative_cached_after_fips_failure(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fmt.Errorf("fail")}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:          fips,
		Relay:         relay,
		Pref:          TransportPrefFIPSFirst,
		Reachable:     func(_ string) bool { return true },
		ReachCacheTTL: 1 * time.Minute,
	})

	// First send — FIPS fails, falls back, caches negative.
	ts.SendDM(context.Background(), "peer1", "a")
	if fips.sendCount() != 1 {
		t.Fatalf("expected 1 FIPS attempt, got %d", fips.sendCount())
	}
	if relay.sendCount() != 1 {
		t.Fatalf("expected 1 relay send, got %d", relay.sendCount())
	}

	// Second send — should skip FIPS (negative cache).
	ts.SendDM(context.Background(), "peer1", "b")
	if fips.sendCount() != 1 {
		t.Fatalf("expected still 1 FIPS attempt (cached unreachable), got %d", fips.sendCount())
	}
	if relay.sendCount() != 2 {
		t.Fatalf("expected 2 relay sends, got %d", relay.sendCount())
	}
}

func TestReachabilityCache_clear(t *testing.T) {
	checkCount := 0
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS: fips,
		Relay: relay,
		Pref: TransportPrefFIPSFirst,
		Reachable: func(_ string) bool {
			checkCount++
			return true
		},
		ReachCacheTTL: 1 * time.Minute,
	})

	ts.SendDM(context.Background(), "peer1", "a")
	if checkCount != 1 {
		t.Fatalf("expected 1 check, got %d", checkCount)
	}

	ts.ClearReachabilityCache()

	ts.SendDM(context.Background(), "peer1", "b")
	if checkCount != 2 {
		t.Fatalf("expected 2 checks after cache clear, got %d", checkCount)
	}
}

// ── Interface delegation ──────────────────────────────────────────────────────

func TestTransportSelector_PublicKey(t *testing.T) {
	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS: &mockTransport{pubkey: "fips-key"},
		Relay: &mockTransport{pubkey: "relay-key"},
	})
	if pk := ts.PublicKey(); pk != "fips-key" {
		t.Fatalf("expected fips-key, got %q", pk)
	}

	// Relay-only.
	ts2, _ := NewTransportSelector(TransportSelectorOptions{
		Relay: &mockTransport{pubkey: "relay-key"},
	})
	if pk := ts2.PublicKey(); pk != "relay-key" {
		t.Fatalf("expected relay-key, got %q", pk)
	}
}

func TestTransportSelector_Relays_from_relay(t *testing.T) {
	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  &mockTransport{pubkey: "abc"},
		Relay: &mockTransport{pubkey: "abc", relays: []string{"wss://r1", "wss://r2"}},
	})
	relays := ts.Relays()
	if len(relays) != 2 {
		t.Fatalf("expected 2 relays, got %d", len(relays))
	}
}

func TestTransportSelector_Close_both(t *testing.T) {
	fips := &mockTransport{pubkey: "abc"}
	relay := &mockTransport{pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  fips,
		Relay: relay,
	})
	ts.Close()

	if !fips.closed {
		t.Fatal("expected FIPS transport closed")
	}
	if !relay.closed {
		t.Fatal("expected relay transport closed")
	}
}

// ── Compile-time check ────────────────────────────────────────────────────────

func TestTransportSelector_satisfies_DMTransport(t *testing.T) {
	var _ DMTransport = (*TransportSelector)(nil)
}
