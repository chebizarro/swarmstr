//go:build experimental_fips

package holepunch

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"
)

// Punch packet constants from the protocol spec.
const (
	// PunchMagic is the 4-byte header for punch packets ("NPTC").
	PunchMagic uint32 = 0x4E505443
	// AckMagic is the 4-byte header for acknowledgment packets ("NPTA").
	AckMagic uint32 = 0x4E505441

	PunchPacketSize = 24 // 4 magic + 4 seq + 16 session hash
	PunchInterval   = 200 * time.Millisecond
	PunchTimeout    = 10 * time.Second
)

// PunchResult describes the outcome of a hole punch attempt.
type PunchResult struct {
	// Success indicates whether the hole was punched.
	Success bool
	// PeerAddr is the confirmed peer address (may differ from the reflexive
	// address if the NAT rebinds).
	PeerAddr *net.UDPAddr
	// RTT is the round-trip time of the first successful punch/ack exchange.
	RTT time.Duration
	// PacketsSent is the total number of punch packets sent.
	PacketsSent int
	// Error, if non-nil, describes why the punch failed.
	Error error
}

// PunchOptions configures a hole punch attempt.
type PunchOptions struct {
	// Conn is the UDP socket to use (must be the same socket used for STUN).
	Conn net.PacketConn
	// PeerAddr is the peer's reflexive address to punch towards.
	PeerAddr *net.UDPAddr
	// PeerLocalAddr is the peer's local address (for LAN optimization).
	// Optional; if set and in the same subnet, punching is attempted on both.
	PeerLocalAddr *net.UDPAddr
	// SessionID is the 32-hex-char session identifier.
	SessionID string
	// Interval overrides the default punch interval (200ms).
	Interval time.Duration
	// Timeout overrides the default punch timeout (10s).
	Timeout time.Duration
}

// Punch executes the UDP hole punch state machine.
//
// Both peers must call this concurrently after exchanging reflexive addresses.
// The function sends punch packets at regular intervals while listening for
// incoming punches and acknowledgments. Returns when either:
//   - A full punch+ack exchange completes (with a grace period so both sides
//     can finish)
//   - The timeout expires (failure)
func Punch(opts PunchOptions) PunchResult {
	interval := opts.Interval
	if interval <= 0 {
		interval = PunchInterval
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = PunchTimeout
	}

	sessionHash := sessionHash16(opts.SessionID)

	var (
		mu          sync.Mutex
		peerAddr    *net.UDPAddr
		gotPunch    bool
		gotAck      bool
		firstAckRTT time.Duration
		packetsSent int
	)

	// ackCh signals the sender that the receiver got an AckMagic.
	// The receiver does NOT return immediately — it keeps responding to
	// PunchMagic packets during a grace period so the peer can also complete.
	ackCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})

	// Receiver goroutine.
	go func() {
		buf := make([]byte, PunchPacketSize+64) // extra room for future extensions
		for {
			// Check if we should stop.
			select {
			case <-stopCh:
				return
			default:
			}

			opts.Conn.SetReadDeadline(time.Now().Add(interval * 2))
			n, from, err := opts.Conn.ReadFrom(buf)
			if err != nil {
				// On read timeout, just retry (unless stopped).
				continue
			}
			if n < PunchPacketSize {
				continue
			}

			magic := binary.BigEndian.Uint32(buf[0:4])
			hash := buf[8:24]

			if !matchSessionHash(hash, sessionHash) {
				continue
			}

			fromUDP, ok := from.(*net.UDPAddr)
			if !ok {
				continue
			}

			switch magic {
			case PunchMagic:
				mu.Lock()
				if !gotPunch {
					gotPunch = true
					peerAddr = fromUDP
				}
				mu.Unlock()

				// Send acknowledgment.
				ack := buildPunchPacket(AckMagic, binary.BigEndian.Uint32(buf[4:8]), sessionHash)
				opts.Conn.SetWriteDeadline(time.Now().Add(time.Second))
				opts.Conn.WriteTo(ack, fromUDP)

			case AckMagic:
				mu.Lock()
				if !gotAck {
					gotAck = true
					if peerAddr == nil {
						peerAddr = fromUDP
					}
				}
				mu.Unlock()
				// Signal the sender but keep running to respond to any
				// remaining PunchMagic from the peer.
				select {
				case ackCh <- struct{}{}:
				default:
				}
			}
		}
	}()

	// Sender loop.
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	deadline := time.After(timeout)
	var seq uint32
	sendStart := time.Now()

	targets := []*net.UDPAddr{opts.PeerAddr}
	if opts.PeerLocalAddr != nil {
		targets = append(targets, opts.PeerLocalAddr)
	}

	sendPunch := func() {
		pkt := buildPunchPacket(PunchMagic, seq, sessionHash)
		for _, target := range targets {
			opts.Conn.SetWriteDeadline(time.Now().Add(time.Second))
			opts.Conn.WriteTo(pkt, target)
		}
		mu.Lock()
		packetsSent++
		if seq == 0 {
			sendStart = time.Now()
		}
		mu.Unlock()
		seq++
	}

	finish := func() PunchResult {
		close(stopCh)
		mu.Lock()
		defer mu.Unlock()
		return PunchResult{
			Success:     gotAck,
			PeerAddr:    peerAddr,
			RTT:         firstAckRTT,
			PacketsSent: packetsSent,
		}
	}

	for {
		select {
		case <-deadline:
			close(stopCh)
			mu.Lock()
			result := PunchResult{
				Success:     gotAck,
				PeerAddr:    peerAddr,
				PacketsSent: packetsSent,
				Error:       fmt.Errorf("punch timeout after %s", timeout),
			}
			mu.Unlock()
			return result

		case <-ackCh:
			mu.Lock()
			firstAckRTT = time.Since(sendStart)
			mu.Unlock()

			// Grace period: keep sending punches and let receiver respond
			// so the peer can also complete its ack exchange.
			graceDeadline := time.After(interval * 4)
			for {
				select {
				case <-graceDeadline:
					return finish()
				case <-deadline:
					return finish()
				case <-ticker.C:
					sendPunch()
				}
			}

		case <-ticker.C:
			sendPunch()

			// Check if we got an ack while sending.
			mu.Lock()
			if gotAck {
				firstAckRTT = time.Since(sendStart)
				mu.Unlock()
				// Still do grace period.
				graceDeadline := time.After(interval * 4)
				for {
					select {
					case <-graceDeadline:
						return finish()
					case <-deadline:
						return finish()
					case <-ticker.C:
						sendPunch()
					}
				}
			}
			mu.Unlock()
		}
	}
}

// sessionHash16 returns the first 16 bytes of SHA-256(sessionID).
func sessionHash16(sessionID string) []byte {
	h := sha256.Sum256([]byte(sessionID))
	return h[:16]
}

func matchSessionHash(got, want []byte) bool {
	if len(got) < 16 || len(want) < 16 {
		return false
	}
	for i := 0; i < 16; i++ {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func buildPunchPacket(magic uint32, seq uint32, sessionHash []byte) []byte {
	pkt := make([]byte, PunchPacketSize)
	binary.BigEndian.PutUint32(pkt[0:4], magic)
	binary.BigEndian.PutUint32(pkt[4:8], seq)
	copy(pkt[8:24], sessionHash[:16])
	return pkt
}

// ValidatePunchPacket checks if raw bytes are a valid punch or ack packet
// for the given session. Returns the magic value and sequence number.
func ValidatePunchPacket(data []byte, sessionID string) (magic uint32, seq uint32, ok bool) {
	if len(data) < PunchPacketSize {
		return 0, 0, false
	}
	magic = binary.BigEndian.Uint32(data[0:4])
	if magic != PunchMagic && magic != AckMagic {
		return 0, 0, false
	}
	seq = binary.BigEndian.Uint32(data[4:8])
	expected := sessionHash16(sessionID)
	if !matchSessionHash(data[8:24], expected) {
		return 0, 0, false
	}
	return magic, seq, true
}
