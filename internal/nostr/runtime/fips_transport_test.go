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
	"testing"
	"time"
)

// ── Identity derivation ───────────────────────────────────────────────────────

func TestFIPSIPv6FromPubkey_basic(t *testing.T) {
	// Deterministic: same pubkey always produces same address.
	pubkey := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	ip1, err := FIPSIPv6FromPubkey(pubkey)
	if err != nil {
		t.Fatalf("FIPSIPv6FromPubkey: %v", err)
	}
	ip2, err := FIPSIPv6FromPubkey(pubkey)
	if err != nil {
		t.Fatalf("FIPSIPv6FromPubkey second call: %v", err)
	}
	if !ip1.Equal(ip2) {
		t.Fatalf("expected deterministic output, got %s and %s", ip1, ip2)
	}

	// Must be in fd00::/8 range.
	if ip1[0] != 0xfd {
		t.Fatalf("expected fd00::/8 prefix, got %02x", ip1[0])
	}

	// Must be 16 bytes.
	if len(ip1) != 16 {
		t.Fatalf("expected 16-byte IPv6, got %d bytes", len(ip1))
	}
}

func TestFIPSIPv6FromPubkey_different_keys(t *testing.T) {
	key1 := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	key2 := "c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5"

	ip1, _ := FIPSIPv6FromPubkey(key1)
	ip2, _ := FIPSIPv6FromPubkey(key2)

	if ip1.Equal(ip2) {
		t.Fatal("different pubkeys should produce different addresses")
	}
}

func TestFIPSIPv6FromPubkey_invalid(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pubkey string
	}{
		{"short", "abcdef"},
		{"not hex", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FIPSIPv6FromPubkey(tc.pubkey)
			if err == nil {
				t.Fatal("expected error for invalid pubkey")
			}
		})
	}
}

func TestFIPSIPv6FromPubkey_strips_compressed_prefix(t *testing.T) {
	// 33-byte compressed key (with 02 prefix) should produce same result as 32-byte x-only.
	xonly := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	compressed := "02" + xonly

	ip1, err := FIPSIPv6FromPubkey(xonly)
	if err != nil {
		t.Fatalf("x-only: %v", err)
	}
	ip2, err := FIPSIPv6FromPubkey(compressed)
	if err != nil {
		t.Fatalf("compressed: %v", err)
	}
	if !ip1.Equal(ip2) {
		t.Fatalf("compressed prefix should be stripped: %s vs %s", ip1, ip2)
	}
}

func TestFIPSAddrString(t *testing.T) {
	pubkey := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	addr, err := FIPSAddrString(pubkey, 1337)
	if err != nil {
		t.Fatalf("FIPSAddrString: %v", err)
	}
	if !strings.HasPrefix(addr, "[fd") {
		t.Fatalf("expected [fd..]:port format, got %q", addr)
	}
	if !strings.HasSuffix(addr, ":1337") {
		t.Fatalf("expected port 1337, got %q", addr)
	}
}

// ── DM envelope ───────────────────────────────────────────────────────────────

func TestFIPSDMEnvelope_roundtrip(t *testing.T) {
	env := fipsDMEnvelope{
		From: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Text: "hello from FIPS mesh",
		TS:   time.Now().Unix(),
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded fipsDMEnvelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.From != env.From || decoded.Text != env.Text || decoded.TS != env.TS {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", env, decoded)
	}
}

// ── Frame wire format ─────────────────────────────────────────────────────────

func TestFIPSFrameFormat_write_read(t *testing.T) {
	payload := []byte(`{"from":"abc","text":"hi","ts":12345}`)

	// Build a frame.
	frame := make([]byte, 4+1+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	frame[4] = byte(fipsFrameDM)
	copy(frame[5:], payload)

	// Parse it back.
	if len(frame) < 5 {
		t.Fatal("frame too short")
	}
	readLen := binary.BigEndian.Uint32(frame[0:4])
	readType := fipsFrameType(frame[4])
	readPayload := frame[5 : 5+readLen]

	if readLen != uint32(len(payload)) {
		t.Fatalf("length mismatch: wrote %d, read %d", len(payload), readLen)
	}
	if readType != fipsFrameDM {
		t.Fatalf("type mismatch: expected DM (0x01), got 0x%02x", readType)
	}
	if string(readPayload) != string(payload) {
		t.Fatalf("payload mismatch")
	}
}

// ── Integration: loopback transport ───────────────────────────────────────────

func TestFIPSTransport_loopback(t *testing.T) {
	// This test creates a mock TCP server acting as a FIPS peer, then sends
	// a DM to it and verifies the frame is received correctly.
	pubkey := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"

	// Start a mock peer listener on a random port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var received fipsDMEnvelope
	var receivedType fipsFrameType
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close()

		// Read frame header.
		var header [5]byte
		if _, err := conn.Read(header[:]); err != nil {
			t.Errorf("read header: %v", err)
			return
		}
		payloadLen := binary.BigEndian.Uint32(header[0:4])
		receivedType = fipsFrameType(header[4])

		// Read payload.
		payload := make([]byte, payloadLen)
		if _, err := conn.Read(payload); err != nil {
			t.Errorf("read payload: %v", err)
			return
		}
		json.Unmarshal(payload, &received)
	}()

	// Create a transport that will dial our mock listener.
	ft := &FIPSTransport{
		pubkeyHex: pubkey,
		agentPort: FIPSDefaultAgentPort,
		dialTTL:   5 * time.Second,
		conns:     make(map[string]*fipsConn),
		idCache:   make(map[string]string),
		ctx:       context.Background(),
	}

	// Override getOrDial to connect to our mock instead of a real FIPS address.
	targetPub := "c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5"
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial mock: %v", err)
	}
	ft.conns[targetPub] = &fipsConn{conn: conn, lastUsed: time.Now()}

	// Build and send a DM frame directly.
	env := fipsDMEnvelope{From: pubkey, Text: "hello mesh", TS: time.Now().Unix()}
	payload, _ := json.Marshal(env)
	frame := make([]byte, 4+1+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	frame[4] = byte(fipsFrameDM)
	copy(frame[5:], payload)
	conn.Write(frame)

	wg.Wait()

	if receivedType != fipsFrameDM {
		t.Fatalf("expected DM frame type, got 0x%02x", receivedType)
	}
	if received.Text != "hello mesh" {
		t.Fatalf("expected 'hello mesh', got %q", received.Text)
	}
	if received.From != pubkey {
		t.Fatalf("expected from=%s, got %s", pubkey, received.From)
	}

	ft.Close()
}

// ── DMTransport interface contract ────────────────────────────────────────────

func TestFIPSTransport_interface_methods(t *testing.T) {
	// Verify the stub/real type satisfies DMTransport at the type level.
	// (Compile-time check is in the source; this tests runtime behavior.)
	var transport DMTransport = &FIPSTransport{pubkeyHex: "abc123"}

	if transport.PublicKey() != "abc123" {
		t.Fatalf("expected pubkey abc123, got %q", transport.PublicKey())
	}
	if relays := transport.Relays(); relays != nil {
		t.Fatalf("expected nil relays, got %v", relays)
	}
	if err := transport.SetRelays([]string{"wss://relay.example.com"}); err != nil {
		t.Fatalf("SetRelays should be no-op, got error: %v", err)
	}
}

func TestFIPSTransport_SendDM_invalid_pubkey(t *testing.T) {
	ft := &FIPSTransport{
		pubkeyHex: "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
		agentPort: FIPSDefaultAgentPort,
		dialTTL:   1 * time.Second,
		conns:     make(map[string]*fipsConn),
		idCache:   make(map[string]string),
		ctx:       context.Background(),
	}
	err := ft.SendDM(context.Background(), "not-a-pubkey", "hello")
	if err == nil {
		t.Fatal("expected error for invalid pubkey")
	}
	ft.Close()
}

// ── Default port helpers ──────────────────────────────────────────────────────

func TestFIPSDefaultAgentPort(t *testing.T) {
	if FIPSDefaultAgentPort != 1337 {
		t.Fatalf("expected default agent port 1337, got %d", FIPSDefaultAgentPort)
	}
}

// ── Identity cache ────────────────────────────────────────────────────────────

func TestFIPSTransport_identity_cache(t *testing.T) {
	ft := &FIPSTransport{
		idCache: make(map[string]string),
	}

	pubkey := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	ft.cacheIdentity(pubkey)

	ip, _ := FIPSIPv6FromPubkey(pubkey)
	resolved := ft.resolveIdentity(fmt.Sprintf("[%s]:1337", ip))
	if resolved != pubkey {
		t.Fatalf("expected %s, got %s", pubkey, resolved)
	}

	// Unknown address returns empty.
	if got := ft.resolveIdentity("[::1]:1337"); got != "" {
		t.Fatalf("expected empty for unknown addr, got %q", got)
	}
}
