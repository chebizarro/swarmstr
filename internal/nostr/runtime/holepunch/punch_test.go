//go:build experimental_fips

package holepunch

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestBuildPunchPacket(t *testing.T) {
	sessionHash := sessionHash16("test-session-id-1234567890abcdef")
	pkt := buildPunchPacket(PunchMagic, 42, sessionHash)

	if len(pkt) != PunchPacketSize {
		t.Fatalf("packet size = %d, want %d", len(pkt), PunchPacketSize)
	}

	magic := binary.BigEndian.Uint32(pkt[0:4])
	if magic != PunchMagic {
		t.Errorf("magic = 0x%08x, want 0x%08x", magic, PunchMagic)
	}

	seq := binary.BigEndian.Uint32(pkt[4:8])
	if seq != 42 {
		t.Errorf("seq = %d, want 42", seq)
	}

	if !matchSessionHash(pkt[8:24], sessionHash) {
		t.Error("session hash mismatch")
	}
}

func TestBuildPunchPacket_Ack(t *testing.T) {
	sessionHash := sessionHash16("ack-session")
	pkt := buildPunchPacket(AckMagic, 7, sessionHash)

	magic := binary.BigEndian.Uint32(pkt[0:4])
	if magic != AckMagic {
		t.Errorf("magic = 0x%08x, want 0x%08x (AckMagic)", magic, AckMagic)
	}
}

func TestValidatePunchPacket(t *testing.T) {
	sessionID := "validate-session-abc123"
	sessionHash := sessionHash16(sessionID)
	pkt := buildPunchPacket(PunchMagic, 99, sessionHash)

	magic, seq, ok := ValidatePunchPacket(pkt, sessionID)
	if !ok {
		t.Fatal("expected valid packet")
	}
	if magic != PunchMagic {
		t.Errorf("magic = 0x%08x", magic)
	}
	if seq != 99 {
		t.Errorf("seq = %d, want 99", seq)
	}
}

func TestValidatePunchPacket_WrongSession(t *testing.T) {
	sessionHash := sessionHash16("session-a")
	pkt := buildPunchPacket(PunchMagic, 0, sessionHash)

	_, _, ok := ValidatePunchPacket(pkt, "session-b")
	if ok {
		t.Fatal("should reject packet with wrong session")
	}
}

func TestValidatePunchPacket_TooShort(t *testing.T) {
	_, _, ok := ValidatePunchPacket([]byte{0x01, 0x02}, "any")
	if ok {
		t.Fatal("should reject short packet")
	}
}

func TestValidatePunchPacket_BadMagic(t *testing.T) {
	pkt := make([]byte, PunchPacketSize)
	binary.BigEndian.PutUint32(pkt[0:4], 0xDEADBEEF)

	_, _, ok := ValidatePunchPacket(pkt, "any")
	if ok {
		t.Fatal("should reject bad magic")
	}
}

func TestSessionHash16_Deterministic(t *testing.T) {
	h1 := sessionHash16("same-id")
	h2 := sessionHash16("same-id")

	if len(h1) != 16 {
		t.Fatalf("hash length = %d, want 16", len(h1))
	}
	for i := 0; i < 16; i++ {
		if h1[i] != h2[i] {
			t.Fatalf("hash not deterministic at byte %d", i)
		}
	}
}

func TestSessionHash16_Different(t *testing.T) {
	h1 := sessionHash16("id-a")
	h2 := sessionHash16("id-b")

	match := true
	for i := 0; i < 16; i++ {
		if h1[i] != h2[i] {
			match = false
			break
		}
	}
	if match {
		t.Fatal("different session IDs should produce different hashes")
	}
}

// TestPunch_Loopback tests the punch state machine with two loopback sockets.
func TestPunch_Loopback(t *testing.T) {
	connA, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen A: %v", err)
	}
	defer connA.Close()

	connB, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen B: %v", err)
	}
	defer connB.Close()

	addrA := connA.LocalAddr().(*net.UDPAddr)
	addrB := connB.LocalAddr().(*net.UDPAddr)

	sessionID := "loopback-test-session"

	// Run both sides concurrently.
	resultCh := make(chan PunchResult, 2)

	go func() {
		r := Punch(PunchOptions{
			Conn:      connA,
			PeerAddr:  addrB,
			SessionID: sessionID,
			Interval:  50 * time.Millisecond,
			Timeout:   5 * time.Second,
		})
		resultCh <- r
	}()

	go func() {
		r := Punch(PunchOptions{
			Conn:      connB,
			PeerAddr:  addrA,
			SessionID: sessionID,
			Interval:  50 * time.Millisecond,
			Timeout:   5 * time.Second,
		})
		resultCh <- r
	}()

	// Both should succeed.
	for i := 0; i < 2; i++ {
		select {
		case r := <-resultCh:
			if !r.Success {
				t.Errorf("peer %d: punch failed: %v", i, r.Error)
			}
			if r.PeerAddr == nil {
				t.Errorf("peer %d: nil peer address", i)
			}
			if r.PacketsSent == 0 {
				t.Errorf("peer %d: no packets sent", i)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("peer %d: timeout waiting for punch result", i)
		}
	}
}

func TestPunch_Timeout(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()

	// Punch to an address nobody is listening on.
	result := Punch(PunchOptions{
		Conn:      conn,
		PeerAddr:  &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1},
		SessionID: "timeout-session",
		Interval:  50 * time.Millisecond,
		Timeout:   500 * time.Millisecond,
	})

	if result.Success {
		t.Fatal("expected failure for unreachable peer")
	}
	if result.Error == nil {
		t.Fatal("expected non-nil error")
	}
	if result.PacketsSent == 0 {
		t.Error("expected some packets sent before timeout")
	}
}
