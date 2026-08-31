package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Mock DMTransport ──────────────────────────────────────────────────────────

type mockTransport struct {
	name   string
	pubkey string
	relays []string

	mu       sync.Mutex
	sendErr  error
	sendFunc func(context.Context, string, string) error
	closed   bool
	sendLog  []string // records toPubKey of each SendDM call
}

func (m *mockTransport) SendDM(ctx context.Context, toPubKey string, text string) error {
	m.mu.Lock()
	m.sendLog = append(m.sendLog, toPubKey)
	fn := m.sendFunc
	err := m.sendErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, toPubKey, text)
	}
	return err
}
func (m *mockTransport) PublicKey() string { return m.pubkey }
func (m *mockTransport) Relays() []string  { return m.relays }
func (m *mockTransport) SetRelays(r []string) error {
	m.relays = r
	return nil
}
func (m *mockTransport) Close() { m.closed = true }

func (m *mockTransport) setSendErr(err error) {
	m.mu.Lock()
	m.sendErr = err
	m.mu.Unlock()
}

func (m *mockTransport) sendCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sendLog)
}

func fipsTransportErr(peer string) error {
	return newFIPSTransportError(peer, "dial", errors.New("mesh unreachable"))
}

func fipsPermanentErr(peer string) error {
	return newFIPSPermanentError(peer, "parse pubkey", errors.New("invalid pubkey"))
}

func expireFIPSFailure(t *testing.T, ts *TransportSelector, peer string) {
	t.Helper()
	ts.failureMu.Lock()
	state, ok := ts.failures[peer]
	if !ok {
		ts.failureMu.Unlock()
		t.Fatalf("expected cached FIPS failure for %s", peer)
	}
	state.Until = time.Now().Add(-time.Second)
	ts.failures[peer] = state
	ts.failureMu.Unlock()
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

func TestTransportSelectorDaemonLifecycleSkipsFIPS(t *testing.T) {
	for _, state := range []string{"Degraded", "Failed", "Draining"} {
		t.Run(state, func(t *testing.T) {
			fips := &mockTransport{name: "fips"}
			relay := &mockTransport{name: "relay"}
			ts, err := NewTransportSelector(TransportSelectorOptions{
				FIPS: fips, Relay: relay, Pref: TransportPrefFIPSFirst,
				DaemonState: func(context.Context) (string, error) { return state, nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := ts.SendDM(context.Background(), "peer", "hello"); err != nil {
				t.Fatal(err)
			}
			if fips.sendCount() != 0 || relay.sendCount() != 1 {
				t.Fatalf("fips sends=%d relay sends=%d", fips.sendCount(), relay.sendCount())
			}
		})
	}
}

func TestTransportSelectorHealthyDaemonUsesFIPS(t *testing.T) {
	fips := &mockTransport{name: "fips"}
	relay := &mockTransport{name: "relay"}
	ts, err := NewTransportSelector(TransportSelectorOptions{
		FIPS: fips, Relay: relay, Pref: TransportPrefFIPSFirst,
		DaemonState: func(context.Context) (string, error) { return "Running", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.SendDM(context.Background(), "peer", "hello"); err != nil {
		t.Fatal(err)
	}
	if fips.sendCount() != 1 || relay.sendCount() != 0 {
		t.Fatalf("fips sends=%d relay sends=%d", fips.sendCount(), relay.sendCount())
	}
}

// ── fips-first routing ────────────────────────────────────────────────────────

func TestFIPSFirst_optimistically_sends_via_fips(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  fips,
		Relay: relay,
		Pref:  TransportPrefFIPSFirst,
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

func TestFIPSFirst_reachability_checker_is_advisory_only(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc"}
	checkCount := 0

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  fips,
		Relay: relay,
		Pref:  TransportPrefFIPSFirst,
		Reachable: func(_ string) bool {
			checkCount++
			return false
		},
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if err != nil {
		t.Fatalf("SendDM: %v", err)
	}
	if checkCount != 0 {
		t.Fatalf("reachability checker should not be called on send path, got %d calls", checkCount)
	}
	if fips.sendCount() != 1 {
		t.Fatalf("expected optimistic FIPS send despite checker=false, got %d", fips.sendCount())
	}
	if relay.sendCount() != 0 {
		t.Fatalf("expected 0 relay sends, got %d", relay.sendCount())
	}
}

func TestFIPSFirst_transport_failure_falls_back_and_negative_caches(t *testing.T) {
	peer := "dest1"
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fipsTransportErr(peer)}
	relay := &mockTransport{name: "relay", pubkey: "abc"}
	fallbackCount := 0

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:          fips,
		Relay:         relay,
		Pref:          TransportPrefFIPSFirst,
		ReachCacheTTL: time.Minute,
		OnFallback: func(toPubKey, preferred string, err error) {
			fallbackCount++
			if toPubKey != peer {
				t.Errorf("fallback peer = %q, want %q", toPubKey, peer)
			}
			if preferred != "fips" {
				t.Errorf("preferred = %q, want fips", preferred)
			}
		},
	})

	if err := ts.SendDM(context.Background(), peer, "hello"); err != nil {
		t.Fatalf("SendDM should succeed via relay fallback: %v", err)
	}
	if fips.sendCount() != 1 || relay.sendCount() != 1 {
		t.Fatalf("first send counts fips=%d relay=%d, want 1/1", fips.sendCount(), relay.sendCount())
	}
	if fallbackCount != 1 {
		t.Fatalf("fallback count = %d, want 1", fallbackCount)
	}
	if ts.ReachabilityCacheSize() != 1 {
		t.Fatalf("failure cache size = %d, want 1", ts.ReachabilityCacheSize())
	}

	if err := ts.SendDM(context.Background(), peer, "again"); err != nil {
		t.Fatalf("second SendDM should use relay while FIPS failure cached: %v", err)
	}
	if fips.sendCount() != 1 {
		t.Fatalf("expected cached failure to skip FIPS, got %d attempts", fips.sendCount())
	}
	if relay.sendCount() != 2 {
		t.Fatalf("expected 2 relay sends, got %d", relay.sendCount())
	}
	if fallbackCount != 2 {
		t.Fatalf("expected cached bypass to emit fallback too, got %d fallback calls", fallbackCount)
	}
}

func TestFIPSFirst_success_after_ttl_expiry_clears_negative_cache(t *testing.T) {
	peer := "dest1"
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fipsTransportErr(peer)}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:          fips,
		Relay:         relay,
		Pref:          TransportPrefFIPSFirst,
		ReachCacheTTL: time.Minute,
	})

	if err := ts.SendDM(context.Background(), peer, "fail then relay"); err != nil {
		t.Fatalf("initial SendDM: %v", err)
	}
	if ts.ReachabilityCacheSize() != 1 {
		t.Fatalf("failure cache size = %d, want 1", ts.ReachabilityCacheSize())
	}

	fips.setSendErr(nil)
	expireFIPSFailure(t, ts, peer)

	if err := ts.SendDM(context.Background(), peer, "fips recovers"); err != nil {
		t.Fatalf("SendDM after expiry should succeed via FIPS: %v", err)
	}
	if fips.sendCount() != 2 {
		t.Fatalf("expected second FIPS attempt after TTL expiry, got %d", fips.sendCount())
	}
	if relay.sendCount() != 1 {
		t.Fatalf("expected no relay send after FIPS recovery, got %d", relay.sendCount())
	}
	if ts.ReachabilityCacheSize() != 0 {
		t.Fatalf("failure cache size after success = %d, want 0", ts.ReachabilityCacheSize())
	}
}

func TestFIPSFirst_ttl_expiry_retries_fips(t *testing.T) {
	peer := "dest1"
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fipsTransportErr(peer)}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:          fips,
		Relay:         relay,
		Pref:          TransportPrefFIPSFirst,
		ReachCacheTTL: time.Minute,
	})

	if err := ts.SendDM(context.Background(), peer, "first"); err != nil {
		t.Fatalf("first SendDM: %v", err)
	}
	expireFIPSFailure(t, ts, peer)
	if err := ts.SendDM(context.Background(), peer, "second"); err != nil {
		t.Fatalf("second SendDM: %v", err)
	}
	if fips.sendCount() != 2 {
		t.Fatalf("expected FIPS retry after TTL expiry, got %d attempts", fips.sendCount())
	}
	if relay.sendCount() != 2 {
		t.Fatalf("expected relay fallback after both failures, got %d sends", relay.sendCount())
	}
}

func TestFIPSFirst_permanent_error_does_not_fallback_or_cache(t *testing.T) {
	peer := "dest1"
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fipsPermanentErr(peer)}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  fips,
		Relay: relay,
		Pref:  TransportPrefFIPSFirst,
	})

	err := ts.SendDM(context.Background(), peer, "hello")
	if err == nil {
		t.Fatal("expected permanent FIPS error")
	}
	var fipsErr *FIPSError
	if !errors.As(err, &fipsErr) || fipsErr.Kind != FIPSErrorKindPermanent {
		t.Fatalf("expected permanent FIPSError, got %v", err)
	}
	if relay.sendCount() != 0 {
		t.Fatalf("permanent FIPS error must not fall back to relay, got %d relay sends", relay.sendCount())
	}
	if ts.ReachabilityCacheSize() != 0 {
		t.Fatalf("permanent FIPS error must not be cached, cache size %d", ts.ReachabilityCacheSize())
	}
}

func TestFIPSFirst_context_error_does_not_fallback_or_cache(t *testing.T) {
	peer := "dest1"
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: context.Canceled}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  fips,
		Relay: relay,
		Pref:  TransportPrefFIPSFirst,
	})

	err := ts.SendDM(context.Background(), peer, "hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if relay.sendCount() != 0 {
		t.Fatalf("context error must not fall back to relay, got %d relay sends", relay.sendCount())
	}
	if ts.ReachabilityCacheSize() != 0 {
		t.Fatalf("context error must not be cached, cache size %d", ts.ReachabilityCacheSize())
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

func TestRelayFirst_sends_via_relay_when_relay_succeeds(t *testing.T) {
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

func TestRelayFirst_falls_back_to_fips_on_relay_failure(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc", sendErr: errors.New("relay rejected")}
	fallbackCount := 0

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  fips,
		Relay: relay,
		Pref:  TransportPrefRelayFirst,
		Reachable: func(_ string) bool {
			t.Fatal("reachability checker must not gate relay-first fallback")
			return false
		},
		OnFallback: func(toPubKey, preferred string, err error) {
			fallbackCount++
			if preferred != "relay" {
				t.Errorf("preferred = %q, want relay", preferred)
			}
		},
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if err != nil {
		t.Fatalf("SendDM should succeed via FIPS fallback: %v", err)
	}
	if relay.sendCount() != 1 {
		t.Fatalf("expected 1 relay send, got %d", relay.sendCount())
	}
	if fips.sendCount() != 1 {
		t.Fatalf("expected 1 FIPS fallback, got %d", fips.sendCount())
	}
	if fallbackCount != 1 {
		t.Fatalf("fallback count = %d, want 1", fallbackCount)
	}
}

func TestRelayFirst_context_error_does_not_fallback_to_fips_or_cache(t *testing.T) {
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc", sendErr: context.DeadlineExceeded}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  fips,
		Relay: relay,
		Pref:  TransportPrefRelayFirst,
	})

	err := ts.SendDM(context.Background(), "dest1", "hello")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline error, got %v", err)
	}
	if fips.sendCount() != 0 {
		t.Fatalf("context relay error must not attempt FIPS fallback, got %d attempts", fips.sendCount())
	}
	if ts.ReachabilityCacheSize() != 0 {
		t.Fatalf("context relay error must not update FIPS cache, got size %d", ts.ReachabilityCacheSize())
	}
}

func TestRelayFirst_negative_cache_skips_fips_fallback(t *testing.T) {
	peer := "dest1"
	fips := &mockTransport{name: "fips", pubkey: "abc"}
	relay := &mockTransport{name: "relay", pubkey: "abc", sendErr: errors.New("relay down")}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:          fips,
		Relay:         relay,
		Pref:          TransportPrefRelayFirst,
		ReachCacheTTL: time.Minute,
	})
	ts.cacheFIPSFailure(peer, fipsTransportErr(peer))

	err := ts.SendDM(context.Background(), peer, "hello")
	if err == nil || !strings.Contains(err.Error(), "relay down") {
		t.Fatalf("expected original relay error while FIPS failure cached, got %v", err)
	}
	if fips.sendCount() != 0 {
		t.Fatalf("expected cached failure to skip FIPS fallback, got %d attempts", fips.sendCount())
	}
}

func TestRelayFirst_fips_fallback_failure_updates_negative_cache(t *testing.T) {
	peer := "dest1"
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fipsTransportErr(peer)}
	relay := &mockTransport{name: "relay", pubkey: "abc", sendErr: errors.New("relay down")}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:          fips,
		Relay:         relay,
		Pref:          TransportPrefRelayFirst,
		ReachCacheTTL: time.Minute,
	})

	err := ts.SendDM(context.Background(), peer, "hello")
	if err == nil {
		t.Fatal("expected relay+FIPS failure")
	}
	if fips.sendCount() != 1 {
		t.Fatalf("expected 1 FIPS fallback attempt, got %d", fips.sendCount())
	}
	if ts.ReachabilityCacheSize() != 1 {
		t.Fatalf("failure cache size = %d, want 1", ts.ReachabilityCacheSize())
	}

	err = ts.SendDM(context.Background(), peer, "again")
	if err == nil || !strings.Contains(err.Error(), "relay down") {
		t.Fatalf("expected relay error on cached second send, got %v", err)
	}
	if fips.sendCount() != 1 {
		t.Fatalf("expected second send to skip FIPS while cached, got %d attempts", fips.sendCount())
	}
}

func TestRelayFirst_fips_permanent_error_returned_and_not_cached(t *testing.T) {
	peer := "dest1"
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fipsPermanentErr(peer)}
	relay := &mockTransport{name: "relay", pubkey: "abc", sendErr: errors.New("relay down")}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  fips,
		Relay: relay,
		Pref:  TransportPrefRelayFirst,
	})

	err := ts.SendDM(context.Background(), peer, "hello")
	var fipsErr *FIPSError
	if !errors.As(err, &fipsErr) || fipsErr.Kind != FIPSErrorKindPermanent {
		t.Fatalf("expected permanent FIPSError, got %v", err)
	}
	if ts.ReachabilityCacheSize() != 0 {
		t.Fatalf("permanent FIPS error must not be cached, cache size %d", ts.ReachabilityCacheSize())
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

func TestFIPSOnly_transport_failure_caches_without_relay_fallback(t *testing.T) {
	peer := "dest1"
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fipsTransportErr(peer)}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:          fips,
		Relay:         relay,
		Pref:          TransportPrefFIPSOnly,
		ReachCacheTTL: time.Minute,
	})

	err := ts.SendDM(context.Background(), peer, "hello")
	if err == nil {
		t.Fatal("expected FIPS transport error")
	}
	if relay.sendCount() != 0 {
		t.Fatalf("fips-only must not use relay, got %d relay sends", relay.sendCount())
	}
	if ts.ReachabilityCacheSize() != 1 {
		t.Fatalf("failure cache size = %d, want 1", ts.ReachabilityCacheSize())
	}

	err = ts.SendDM(context.Background(), peer, "again")
	if err == nil || !strings.Contains(err.Error(), "cached") {
		t.Fatalf("expected cached FIPS failure, got %v", err)
	}
	if fips.sendCount() != 1 {
		t.Fatalf("expected cached failure to skip second FIPS attempt, got %d", fips.sendCount())
	}
}

func TestFIPSOnly_permanent_error_not_cached(t *testing.T) {
	peer := "dest1"
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fipsPermanentErr(peer)}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS: fips,
		Pref: TransportPrefFIPSOnly,
	})

	err := ts.SendDM(context.Background(), peer, "hello")
	var fipsErr *FIPSError
	if !errors.As(err, &fipsErr) || fipsErr.Kind != FIPSErrorKindPermanent {
		t.Fatalf("expected permanent FIPSError, got %v", err)
	}
	if ts.ReachabilityCacheSize() != 0 {
		t.Fatalf("permanent FIPS error must not be cached, cache size %d", ts.ReachabilityCacheSize())
	}
}

// ── Failure cache utilities ───────────────────────────────────────────────────

func TestFailureCache_clear(t *testing.T) {
	peer := "peer1"
	fips := &mockTransport{name: "fips", pubkey: "abc", sendErr: fipsTransportErr(peer)}
	relay := &mockTransport{name: "relay", pubkey: "abc"}

	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:          fips,
		Relay:         relay,
		Pref:          TransportPrefFIPSFirst,
		ReachCacheTTL: time.Minute,
	})

	if err := ts.SendDM(context.Background(), peer, "a"); err != nil {
		t.Fatalf("SendDM: %v", err)
	}
	if ts.ReachabilityCacheSize() != 1 {
		t.Fatalf("expected cache size 1, got %d", ts.ReachabilityCacheSize())
	}

	ts.ClearReachabilityCache()
	if ts.ReachabilityCacheSize() != 0 {
		t.Fatalf("expected cache cleared, got %d", ts.ReachabilityCacheSize())
	}

	if err := ts.SendDM(context.Background(), peer, "b"); err != nil {
		t.Fatalf("SendDM after clear: %v", err)
	}
	if fips.sendCount() != 2 {
		t.Fatalf("expected FIPS retry after cache clear, got %d", fips.sendCount())
	}
}

// ── Interface delegation ──────────────────────────────────────────────────────

func TestTransportSelector_PublicKey(t *testing.T) {
	ts, _ := NewTransportSelector(TransportSelectorOptions{
		FIPS:  &mockTransport{pubkey: "fips-key"},
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
