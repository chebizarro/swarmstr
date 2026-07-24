//go:build experimental_fips

package runtime

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestIntegration_DelayedDiscovery_EventualFIPSSuccessClearsNegativeCache(t *testing.T) {
	peer := agentBPubkey
	fips := &scriptedTransport{pubkey: agentAPubkey, results: []error{fipsTransportErr(peer), nil}}
	relay := &scriptedTransport{pubkey: agentAPubkey}

	ts, err := NewTransportSelector(TransportSelectorOptions{
		FIPS:          fips,
		Relay:         relay,
		Pref:          TransportPrefFIPSFirst,
		ReachCacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}
	defer ts.Close()

	if err := ts.SendDM(context.Background(), peer, "discovery not ready"); err != nil {
		t.Fatalf("initial SendDM should fall back to relay: %v", err)
	}
	if fips.sendCount() != 1 || relay.sendCount() != 1 {
		t.Fatalf("initial send counts fips=%d relay=%d, want 1/1", fips.sendCount(), relay.sendCount())
	}
	if ts.ReachabilityCacheSize() != 1 {
		t.Fatalf("negative cache size = %d, want 1", ts.ReachabilityCacheSize())
	}

	// Discovery eventually completes after the cached failure cooldown expires.
	expireFIPSFailure(t, ts, peer)
	if err := ts.SendDM(context.Background(), peer, "discovery ready"); err != nil {
		t.Fatalf("second SendDM should succeed over FIPS: %v", err)
	}
	if fips.sendCount() != 2 {
		t.Fatalf("expected FIPS retry after discovery readiness, got %d", fips.sendCount())
	}
	if relay.sendCount() != 1 {
		t.Fatalf("relay should not receive recovered send, got %d", relay.sendCount())
	}
	if ts.ReachabilityCacheSize() != 0 {
		t.Fatalf("negative cache should clear on FIPS success, got %d", ts.ReachabilityCacheSize())
	}
}

func TestIntegration_SustainedDMExchange_DuringDaemonRekey(t *testing.T) {
	const perDirection = 25

	var muA, muB sync.Mutex
	receivedA := make(map[string]bool)
	receivedB := make(map[string]bool)
	rekeyTriggered := make(chan struct{})
	var rekeyOnce sync.Once

	transportA, addrA := loopbackTransport(t, agentAPubkey, func(_ context.Context, dm InboundDM) error {
		muA.Lock()
		receivedA[dm.Text] = true
		muA.Unlock()
		return nil
	})
	transportB, addrB := loopbackTransport(t, agentBPubkey, func(_ context.Context, dm InboundDM) error {
		muB.Lock()
		receivedB[dm.Text] = true
		if len(receivedB) == perDirection/2 {
			rekeyOnce.Do(func() { close(rekeyTriggered) })
		}
		muB.Unlock()
		return nil
	})
	transportA.cacheIdentity(agentBPubkey)
	transportB.cacheIdentity(agentAPubkey)
	connectTransports(t, transportA, transportB, addrB)
	connectTransports(t, transportB, transportA, addrA)

	ctx := context.Background()
	for i := 0; i < perDirection; i++ {
		if i == perDirection/2 {
			// FIPS v0.3.0+ daemon rekey is hitless from metiq's TCP/framing boundary.
			// This marker verifies sends before, during, and after the rekey window.
			rekeyOnce.Do(func() { close(rekeyTriggered) })
		}
		if err := transportA.SendDM(ctx, agentBPubkey, fmt.Sprintf("a-to-b-%02d", i)); err != nil {
			t.Fatalf("A→B SendDM %d: %v", i, err)
		}
		if err := transportB.SendDM(ctx, agentAPubkey, fmt.Sprintf("b-to-a-%02d", i)); err != nil {
			t.Fatalf("B→A SendDM %d: %v", i, err)
		}
	}

	<-rekeyTriggered
	waitFor(t, 5*time.Second, func() bool {
		muA.Lock()
		defer muA.Unlock()
		muB.Lock()
		defer muB.Unlock()
		return len(receivedA) == perDirection && len(receivedB) == perDirection
	})

	muA.Lock()
	defer muA.Unlock()
	muB.Lock()
	defer muB.Unlock()
	for i := 0; i < perDirection; i++ {
		if !receivedB[fmt.Sprintf("a-to-b-%02d", i)] {
			t.Fatalf("missing A→B message %02d", i)
		}
		if !receivedA[fmt.Sprintf("b-to-a-%02d", i)] {
			t.Fatalf("missing B→A message %02d", i)
		}
	}
}

func TestIntegration_NegativeCacheTTLExpiry_RetriesFIPS(t *testing.T) {
	peer := agentBPubkey
	fips := &scriptedTransport{pubkey: agentAPubkey, results: []error{fipsTransportErr(peer), fipsTransportErr(peer)}}
	relay := &scriptedTransport{pubkey: agentAPubkey}

	ts, err := NewTransportSelector(TransportSelectorOptions{
		FIPS:          fips,
		Relay:         relay,
		Pref:          TransportPrefFIPSFirst,
		ReachCacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}
	defer ts.Close()

	if err := ts.SendDM(context.Background(), peer, "first failure"); err != nil {
		t.Fatalf("first SendDM should fall back to relay: %v", err)
	}
	if err := ts.SendDM(context.Background(), peer, "cached failure"); err != nil {
		t.Fatalf("cached SendDM should use relay: %v", err)
	}
	if fips.sendCount() != 1 {
		t.Fatalf("FIPS should be skipped while negative cache is active, got %d attempts", fips.sendCount())
	}

	expireFIPSFailure(t, ts, peer)
	if err := ts.SendDM(context.Background(), peer, "retry after ttl"); err != nil {
		t.Fatalf("post-TTL SendDM should retry FIPS then fall back: %v", err)
	}
	if fips.sendCount() != 2 {
		t.Fatalf("expected FIPS retry after TTL expiry, got %d attempts", fips.sendCount())
	}
	if relay.sendCount() != 3 {
		t.Fatalf("expected all three sends to complete via relay fallback, got %d", relay.sendCount())
	}
}

type scriptedTransport struct {
	pubkey  string
	results []error

	mu      sync.Mutex
	sendLog []integMockSendEntry
}

func (s *scriptedTransport) SendDM(_ context.Context, to, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendLog = append(s.sendLog, integMockSendEntry{to: to, text: text})
	if len(s.results) == 0 {
		return nil
	}
	err := s.results[0]
	s.results = s.results[1:]
	return err
}

func (s *scriptedTransport) PublicKey() string          { return s.pubkey }
func (s *scriptedTransport) Relays() []string           { return []string{"wss://relay.test"} }
func (s *scriptedTransport) SetRelays(_ []string) error { return nil }
func (s *scriptedTransport) Close()                     {}

func (s *scriptedTransport) sendCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sendLog)
}

var _ DMTransport = (*scriptedTransport)(nil)
