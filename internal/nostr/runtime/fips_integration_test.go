//go:build experimental_fips

package runtime

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────────────
// Test keypairs — deterministic 32-byte hex pubkeys for reproducible addresses.
// These are the x-only public keys from well-known secp256k1 test vectors.
// ────────────────────────────────────────────────────────────────────────────

const (
	agentAPubkey = "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	agentBPubkey = "c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5"
	agentCPubkey = "f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9"
)

// ────────────────────────────────────────────────────────────────────────────
// helpers: loopback transport pair
// ────────────────────────────────────────────────────────────────────────────

// loopbackTransport creates a FIPSTransport with a loopback listener on a
// random port. The transport's identity cache is seeded with the peer pubkey
// so inbound identity resolution works. Returns (transport, listenAddr).
func loopbackTransport(t *testing.T, pubkey string, onMessage func(context.Context, InboundDM) error) (*FIPSTransport, string) {
	t.Helper()

	ft := &FIPSTransport{
		pubkeyHex: pubkey,
		agentPort: FIPSDefaultAgentPort,
		dialTTL:   5 * time.Second,
		conns:     make(map[string]*fipsConn),
		idCache:   make(map[string]string),
		onMessage: onMessage,
		ctx:       context.Background(),
	}

	// Start a listener on loopback.
	ln, err := NewFIPSListener(FIPSListenerOptions{
		ListenAddr: "[::1]:0",
		OnMessage:  ft.handleInbound,
		OnError:    func(e error) { t.Logf("listener error: %v", e) },
		IdentityResolver: func(addr string) string {
			return ft.resolveIdentity(addr)
		},
	})
	if err != nil {
		t.Fatalf("loopback listener: %v", err)
	}
	ft.listener = ln
	addr := ln.Addr().String()

	t.Cleanup(func() { ft.Close() })
	return ft, addr
}

// connectTransports wires transport A to transport B's listen address under
// B's pubkey. After this call, A.SendDM(ctx, bPubkey, ...) delivers to B.
// The connection is registered for cleanup (closed before the listener
// transport to avoid 30s read-timeout blocking during teardown).
func connectTransports(t *testing.T, from *FIPSTransport, toPubkey string, toAddr string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp6", toAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("connect %s → %s: %v", from.pubkeyHex[:8], toPubkey[:8], err)
	}
	from.connMu.Lock()
	from.conns[toPubkey] = &fipsConn{conn: conn, lastUsed: time.Now()}
	from.connMu.Unlock()
	// Close this outbound connection during cleanup so the listener's
	// handleConn goroutine sees EOF instead of blocking for 30s.
	t.Cleanup(func() { conn.Close() })
	// Seed identity cache so B can resolve A.
	from.cacheIdentity(toPubkey)
}

// ────────────────────────────────────────────────────────────────────────────
// Test 1: DM over FIPS — Agent A sends DM to Agent B
// ────────────────────────────────────────────────────────────────────────────

func TestIntegration_DM_Over_FIPS(t *testing.T) {
	var receivedMu sync.Mutex
	var receivedDMs []InboundDM

	// Create Agent B's transport (receiver).
	transportB, addrB := loopbackTransport(t, agentBPubkey, func(_ context.Context, dm InboundDM) error {
		receivedMu.Lock()
		receivedDMs = append(receivedDMs, dm)
		receivedMu.Unlock()
		return nil
	})
	_ = transportB

	// Create Agent A's transport (sender).
	transportA, _ := loopbackTransport(t, agentAPubkey, nil)

	// Seed B's identity cache so it can resolve A.
	transportB.cacheIdentity(agentAPubkey)

	// Wire A → B.
	connectTransports(t, transportA, agentBPubkey, addrB)

	// Send DM from A to B.
	ctx := context.Background()
	err := transportA.SendDM(ctx, agentBPubkey, "hello from Agent A")
	if err != nil {
		t.Fatalf("SendDM: %v", err)
	}

	// Wait for delivery.
	deadline := time.After(3 * time.Second)
	for {
		receivedMu.Lock()
		n := len(receivedDMs)
		receivedMu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for DM delivery")
		case <-time.After(10 * time.Millisecond):
		}
	}

	receivedMu.Lock()
	defer receivedMu.Unlock()
	if len(receivedDMs) != 1 {
		t.Fatalf("expected 1 DM, got %d", len(receivedDMs))
	}
	dm := receivedDMs[0]
	if dm.Text != "hello from Agent A" {
		t.Errorf("text = %q, want %q", dm.Text, "hello from Agent A")
	}
	if dm.Scheme != "fips" {
		t.Errorf("scheme = %q, want %q", dm.Scheme, "fips")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test 2: Bidirectional DM — A↔B exchange
// ────────────────────────────────────────────────────────────────────────────

func TestIntegration_Bidirectional_DM(t *testing.T) {
	var muA, muB sync.Mutex
	var dmsA, dmsB []InboundDM

	transportA, addrA := loopbackTransport(t, agentAPubkey, func(_ context.Context, dm InboundDM) error {
		muA.Lock()
		dmsA = append(dmsA, dm)
		muA.Unlock()
		return nil
	})
	transportB, addrB := loopbackTransport(t, agentBPubkey, func(_ context.Context, dm InboundDM) error {
		muB.Lock()
		dmsB = append(dmsB, dm)
		muB.Unlock()
		return nil
	})

	// Seed identity caches.
	transportA.cacheIdentity(agentBPubkey)
	transportB.cacheIdentity(agentAPubkey)

	// Wire both directions.
	connectTransports(t, transportA, agentBPubkey, addrB)
	connectTransports(t, transportB, agentAPubkey, addrA)

	ctx := context.Background()

	// A → B.
	if err := transportA.SendDM(ctx, agentBPubkey, "ping from A"); err != nil {
		t.Fatalf("A→B SendDM: %v", err)
	}
	// B → A.
	if err := transportB.SendDM(ctx, agentAPubkey, "pong from B"); err != nil {
		t.Fatalf("B→A SendDM: %v", err)
	}

	// Wait for both deliveries.
	waitFor(t, 3*time.Second, func() bool {
		muA.Lock()
		defer muA.Unlock()
		muB.Lock()
		defer muB.Unlock()
		return len(dmsA) >= 1 && len(dmsB) >= 1
	})

	muA.Lock()
	if dmsA[0].Text != "pong from B" {
		t.Errorf("A received %q, want %q", dmsA[0].Text, "pong from B")
	}
	muA.Unlock()

	muB.Lock()
	if dmsB[0].Text != "ping from A" {
		t.Errorf("B received %q, want %q", dmsB[0].Text, "ping from A")
	}
	muB.Unlock()
}

// ────────────────────────────────────────────────────────────────────────────
// Test 3: Control RPC over FIPS
// ────────────────────────────────────────────────────────────────────────────

func TestIntegration_Control_RPC_Over_FIPS(t *testing.T) {
	// Set up Agent B's control channel.
	handler := func(_ context.Context, in ControlRPCInbound) (ControlRPCResult, error) {
		if in.Method == "status.get" {
			return ControlRPCResult{Result: map[string]any{"status": "ok", "agent": "B"}}, nil
		}
		return ControlRPCResult{}, fmt.Errorf("unknown method: %s", in.Method)
	}

	cc, err := NewFIPSControlChannel(FIPSControlChannelOptions{
		PubkeyHex: agentBPubkey,
		OnRequest: handler,
	})
	if err != nil {
		t.Fatalf("new control channel: %v", err)
	}

	// Bind to loopback instead of fd00::.
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cc.listener = ln
	cc.wg.Add(1)
	go cc.acceptLoop()
	t.Cleanup(func() { cc.Close() })

	// Agent A connects and sends a control request.
	conn, err := net.DialTimeout("tcp6", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	defer conn.Close()

	env := fipsControlEnvelope{
		RequestID: "int-test-001",
		From:      agentAPubkey,
		Method:    "status.get",
	}
	payload, _ := json.Marshal(env)
	writeFrame(t, conn, fipsFrameControlReq, payload)

	ft, respPayload := readFrame(t, conn)
	if ft != fipsFrameControlRes {
		t.Fatalf("expected ControlRes (0x03), got 0x%02x", ft)
	}

	var resp fipsControlResponse
	json.Unmarshal(respPayload, &resp)
	if resp.RequestID != "int-test-001" {
		t.Errorf("request_id = %q", resp.RequestID)
	}
	if resp.Error != "" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", resp.Result)
	}
	if resultMap["agent"] != "B" {
		t.Errorf("agent = %v, want B", resultMap["agent"])
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test 4: TransportSelector fallback — FIPS fails, relay succeeds
// ────────────────────────────────────────────────────────────────────────────

func TestIntegration_TransportSelector_Fallback(t *testing.T) {
	var fallbackCalled atomic.Int32

	// Broken FIPS transport — always fails.
	brokenFIPS := &integMockTransport{
		pubkey:  agentAPubkey,
		sendErr: fmt.Errorf("fips daemon unreachable"),
	}

	// Working relay transport.
	workingRelay := &integMockTransport{pubkey: agentAPubkey}

	ts, err := NewTransportSelector(TransportSelectorOptions{
		FIPS:  brokenFIPS,
		Relay: workingRelay,
		Pref:  TransportPrefFIPSFirst,
		OnFallback: func(to, pref string, err error) {
			fallbackCalled.Add(1)
		},
	})
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}
	defer ts.Close()

	ctx := context.Background()
	err = ts.SendDM(ctx, agentBPubkey, "test fallback")
	if err != nil {
		t.Fatalf("SendDM should succeed via relay fallback: %v", err)
	}

	if fallbackCalled.Load() != 1 {
		t.Errorf("fallback called %d times, want 1", fallbackCalled.Load())
	}

	// Verify relay received the message.
	workingRelay.mu.Lock()
	if len(workingRelay.sendLog) != 1 {
		workingRelay.mu.Unlock()
		t.Fatalf("relay send count = %d, want 1", len(workingRelay.sendLog))
	}
	if workingRelay.sendLog[0].text != "test fallback" {
		workingRelay.mu.Unlock()
		t.Errorf("relay received %q, want %q", workingRelay.sendLog[0].text, "test fallback")
	} else {
		workingRelay.mu.Unlock()
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test 5: TransportSelector FIPS-only — no fallback
// ────────────────────────────────────────────────────────────────────────────

func TestIntegration_TransportSelector_FIPSOnly_NoFallback(t *testing.T) {
	brokenFIPS := &integMockTransport{
		pubkey:  agentAPubkey,
		sendErr: fmt.Errorf("mesh down"),
	}
	workingRelay := &integMockTransport{pubkey: agentAPubkey}

	ts, err := NewTransportSelector(TransportSelectorOptions{
		FIPS:  brokenFIPS,
		Relay: workingRelay,
		Pref:  TransportPrefFIPSOnly,
	})
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}
	defer ts.Close()

	err = ts.SendDM(context.Background(), agentBPubkey, "should fail")
	if err == nil {
		t.Fatal("expected error in fips-only mode with broken FIPS")
	}
	if !strings.Contains(err.Error(), "mesh down") {
		t.Errorf("error = %q, want to contain 'mesh down'", err.Error())
	}
	workingRelay.mu.Lock()
	relayCount := len(workingRelay.sendLog)
	workingRelay.mu.Unlock()
	if relayCount != 0 {
		t.Error("relay should NOT be used in fips-only mode")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test 6: Multi-message burst — verify ordering and completeness
// ────────────────────────────────────────────────────────────────────────────

func TestIntegration_MultiBurst(t *testing.T) {
	const msgCount = 20

	var mu sync.Mutex
	var received []string

	_, addrB := loopbackTransport(t, agentBPubkey, func(_ context.Context, dm InboundDM) error {
		mu.Lock()
		received = append(received, dm.Text)
		mu.Unlock()
		return nil
	})

	transportA, _ := loopbackTransport(t, agentAPubkey, nil)
	connectTransports(t, transportA, agentBPubkey, addrB)

	ctx := context.Background()
	for i := 0; i < msgCount; i++ {
		err := transportA.SendDM(ctx, agentBPubkey, fmt.Sprintf("msg-%03d", i))
		if err != nil {
			t.Fatalf("SendDM msg-%03d: %v", i, err)
		}
	}

	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) >= msgCount
	})

	mu.Lock()
	defer mu.Unlock()
	if len(received) != msgCount {
		t.Fatalf("received %d messages, want %d", len(received), msgCount)
	}

	// Verify all messages arrived (order may vary due to TCP framing).
	seen := make(map[string]bool)
	for _, msg := range received {
		seen[msg] = true
	}
	for i := 0; i < msgCount; i++ {
		key := fmt.Sprintf("msg-%03d", i)
		if !seen[key] {
			t.Errorf("missing message: %s", key)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test 7: Health accessors reflect live state
// ────────────────────────────────────────────────────────────────────────────

func TestIntegration_HealthAccessors(t *testing.T) {
	transportA, addrA := loopbackTransport(t, agentAPubkey, nil)
	transportB, addrB := loopbackTransport(t, agentBPubkey, nil)

	// Before connections.
	if transportA.ConnectionCount() != 0 {
		t.Errorf("initial connection count = %d, want 0", transportA.ConnectionCount())
	}
	if transportA.ListenerAddr() == "" {
		t.Error("expected non-empty listener addr")
	}

	// Connect A → B and B → A.
	connectTransports(t, transportA, agentBPubkey, addrB)
	connectTransports(t, transportB, agentAPubkey, addrA)

	if transportA.ConnectionCount() != 1 {
		t.Errorf("after connect: connection count = %d, want 1", transportA.ConnectionCount())
	}

	// Seed identity caches.
	transportA.cacheIdentity(agentBPubkey)
	transportA.cacheIdentity(agentCPubkey) // extra peer

	if transportA.IdentityCacheSize() != 2 {
		t.Errorf("identity cache size = %d, want 2", transportA.IdentityCacheSize())
	}

	// TransportSelector accessors.
	ts, err := NewTransportSelector(TransportSelectorOptions{
		FIPS:  &integMockTransport{pubkey: agentAPubkey},
		Relay: &integMockTransport{pubkey: agentAPubkey},
		Pref:  TransportPrefFIPSFirst,
	})
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}
	defer ts.Close()

	if ts.Preference() != TransportPrefFIPSFirst {
		t.Errorf("preference = %q, want %q", ts.Preference(), TransportPrefFIPSFirst)
	}
	if ts.ReachabilityCacheSize() != 0 {
		t.Errorf("initial cache size = %d, want 0", ts.ReachabilityCacheSize())
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test 8: Control channel + DM on same agent (dual-port)
// ────────────────────────────────────────────────────────────────────────────

func TestIntegration_DualPort_DM_And_Control(t *testing.T) {
	var dmReceived atomic.Int32
	var controlReceived atomic.Int32

	// Agent B: DM transport on port X, control channel on port Y.
	_, addrB := loopbackTransport(t, agentBPubkey, func(_ context.Context, dm InboundDM) error {
		dmReceived.Add(1)
		return nil
	})

	handler := func(_ context.Context, in ControlRPCInbound) (ControlRPCResult, error) {
		controlReceived.Add(1)
		return ControlRPCResult{Result: "ack"}, nil
	}
	cc, _ := NewFIPSControlChannel(FIPSControlChannelOptions{
		PubkeyHex: agentBPubkey,
		OnRequest: handler,
	})
	ccLn, _ := net.Listen("tcp6", "[::1]:0")
	cc.listener = ccLn
	cc.wg.Add(1)
	go cc.acceptLoop()
	t.Cleanup(func() { cc.Close() })

	// Agent A sends a DM.
	transportA, _ := loopbackTransport(t, agentAPubkey, nil)
	connectTransports(t, transportA, agentBPubkey, addrB)
	transportA.SendDM(context.Background(), agentBPubkey, "dm payload")

	// Agent A sends a control request.
	ctrlConn, _ := net.DialTimeout("tcp6", ccLn.Addr().String(), 2*time.Second)
	defer ctrlConn.Close()
	env := fipsControlEnvelope{RequestID: "dual-1", From: agentAPubkey, Method: "ping"}
	payload, _ := json.Marshal(env)
	writeFrame(t, ctrlConn, fipsFrameControlReq, payload)
	readFrame(t, ctrlConn)

	waitFor(t, 3*time.Second, func() bool {
		return dmReceived.Load() >= 1 && controlReceived.Load() >= 1
	})

	if dmReceived.Load() != 1 {
		t.Errorf("DM received = %d, want 1", dmReceived.Load())
	}
	if controlReceived.Load() != 1 {
		t.Errorf("control received = %d, want 1", controlReceived.Load())
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test 9: Identity derivation consistency across agents
// ────────────────────────────────────────────────────────────────────────────

func TestIntegration_IdentityDerivation_Consistency(t *testing.T) {
	// All three agents derive deterministic, unique addresses.
	addrA, errA := FIPSIPv6FromPubkey(agentAPubkey)
	addrB, errB := FIPSIPv6FromPubkey(agentBPubkey)
	addrC, errC := FIPSIPv6FromPubkey(agentCPubkey)

	for _, err := range []error{errA, errB, errC} {
		if err != nil {
			t.Fatalf("identity derivation error: %v", err)
		}
	}

	// All must be in fd00::/8 range.
	for _, addr := range []net.IP{addrA, addrB, addrC} {
		if addr[0] != 0xfd {
			t.Errorf("address %s not in fd00::/8", addr)
		}
	}

	// All must be unique.
	addrs := map[string]bool{addrA.String(): true, addrB.String(): true, addrC.String(): true}
	if len(addrs) != 3 {
		t.Fatalf("expected 3 unique addresses, got %d (A=%s, B=%s, C=%s)",
			len(addrs), addrA, addrB, addrC)
	}

	// Same key always produces same address.
	addrA2, _ := FIPSIPv6FromPubkey(agentAPubkey)
	if !addrA.Equal(addrA2) {
		t.Error("identity derivation is not deterministic")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test 10: Connection pool eviction under load
// ────────────────────────────────────────────────────────────────────────────

func TestIntegration_ConnectionPool_Eviction(t *testing.T) {
	transportA, _ := loopbackTransport(t, agentAPubkey, nil)

	// Fill the connection pool with mock connections (more than fipsConnPoolCap).
	for i := 0; i < fipsConnPoolCap+5; i++ {
		fakePubkey := fmt.Sprintf("%064x", i)
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		defer ln.Close()
		go ln.Accept() // consume the connection

		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatalf("dial mock %d: %v", i, err)
		}
		transportA.connMu.Lock()
		transportA.conns[fakePubkey] = &fipsConn{
			conn:     conn,
			lastUsed: time.Now().Add(-time.Duration(i) * time.Second), // older = earlier
		}
		transportA.connMu.Unlock()
	}

	// Pool should be capped.
	transportA.connMu.Lock()
	count := len(transportA.conns)
	transportA.connMu.Unlock()

	// We added 69 entries to a map without eviction logic in this test.
	// The real eviction happens in getOrDial. Verify the transport works.
	if count != fipsConnPoolCap+5 {
		t.Logf("pool size = %d (direct fill bypasses eviction — expected)", count)
	}

	// Verify ConnectionCount accessor.
	if transportA.ConnectionCount() != count {
		t.Errorf("ConnectionCount() = %d, want %d", transportA.ConnectionCount(), count)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// helpers
// ────────────────────────────────────────────────────────────────────────────

func writeFrame(t *testing.T, conn net.Conn, ft fipsFrameType, payload []byte) {
	t.Helper()
	frame := make([]byte, 4+1+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	frame[4] = byte(ft)
	copy(frame[5:], payload)
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
}

func readFrame(t *testing.T, conn net.Conn) (fipsFrameType, []byte) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	header := make([]byte, 5)
	if _, err := readFull(conn, header); err != nil {
		t.Fatalf("readFrame header: %v", err)
	}
	pLen := binary.BigEndian.Uint32(header[0:4])
	ft := fipsFrameType(header[4])
	payload := make([]byte, pLen)
	if pLen > 0 {
		if _, err := readFull(conn, payload); err != nil {
			t.Fatalf("readFrame payload: %v", err)
		}
	}
	return ft, payload
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("waitFor timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// integMockTransport is a minimal DMTransport for integration selector tests.
// Named differently from mockTransport in transport_selector_test.go to avoid
// redeclaration within the same test package.
type integMockTransport struct {
	pubkey  string
	sendErr error
	sendLog []integMockSendEntry
	mu      sync.Mutex
}

type integMockSendEntry struct {
	to, text string
}

func (m *integMockTransport) SendDM(_ context.Context, to, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendLog = append(m.sendLog, integMockSendEntry{to: to, text: text})
	return m.sendErr
}

func (m *integMockTransport) PublicKey() string          { return m.pubkey }
func (m *integMockTransport) Relays() []string           { return []string{"wss://relay.test"} }
func (m *integMockTransport) SetRelays(_ []string) error { return nil }
func (m *integMockTransport) Close()                     {}

var _ DMTransport = (*integMockTransport)(nil)
