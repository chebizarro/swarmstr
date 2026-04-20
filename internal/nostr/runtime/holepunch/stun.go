//go:build experimental_fips

// Package holepunch implements Nostr-signaled UDP hole punching for NAT traversal.
//
// This implements a minimal STUN client (RFC 8489 Binding Request/Response only)
// for reflexive address discovery. No full ICE or TURN support.
package holepunch

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// STUN message constants (RFC 8489).
const (
	stunMagicCookie     = 0x2112A442
	stunHeaderSize      = 20
	stunBindingRequest  = 0x0001
	stunBindingResponse = 0x0101

	// Attribute types.
	stunAttrMappedAddress    = 0x0001
	stunAttrXORMappedAddress = 0x0020

	// Timeouts.
	stunTimeout     = 3 * time.Second
	stunMaxRetries  = 2
	stunMaxRespSize = 576
)

// STUNResult holds the outcome of a STUN binding request.
type STUNResult struct {
	// ReflexiveAddr is the public IP:port as seen by the STUN server.
	ReflexiveAddr *net.UDPAddr
	// LocalAddr is the local socket address used for the query.
	LocalAddr *net.UDPAddr
	// ServerUsed is the STUN server that was queried.
	ServerUsed string
	// RTT is the round-trip time of the binding request.
	RTT time.Duration
}

// STUNBind performs a STUN Binding Request from the given UDP connection and
// returns the reflexive address. The conn must already be bound (e.g. via
// net.ListenPacket). The same conn should be reused for hole punching.
func STUNBind(conn net.PacketConn, stunServer string) (*STUNResult, error) {
	serverAddr, err := net.ResolveUDPAddr("udp", stunServer)
	if err != nil {
		return nil, fmt.Errorf("stun: resolve %s: %w", stunServer, err)
	}

	// Build STUN Binding Request.
	txID := make([]byte, 12)
	if _, err := rand.Read(txID); err != nil {
		return nil, fmt.Errorf("stun: random txid: %w", err)
	}

	req := buildSTUNBindingRequest(txID)

	var result *STUNResult
	for attempt := 0; attempt <= stunMaxRetries; attempt++ {
		conn.SetWriteDeadline(time.Now().Add(stunTimeout))
		if _, err := conn.WriteTo(req, serverAddr); err != nil {
			continue
		}

		start := time.Now()
		conn.SetReadDeadline(time.Now().Add(stunTimeout))
		buf := make([]byte, stunMaxRespSize)
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			continue
		}
		rtt := time.Since(start)

		reflexive, err := parseSTUNBindingResponse(buf[:n], txID)
		if err != nil {
			continue
		}

		localAddr, _ := conn.LocalAddr().(*net.UDPAddr)
		result = &STUNResult{
			ReflexiveAddr: reflexive,
			LocalAddr:     localAddr,
			ServerUsed:    stunServer,
			RTT:           rtt,
		}
		break
	}

	if result == nil {
		return nil, fmt.Errorf("stun: no response from %s after %d attempts", stunServer, stunMaxRetries+1)
	}
	return result, nil
}

// STUNBindNew creates a new UDP socket, performs STUN binding, and returns
// both the result and the socket. The caller owns the socket and must close it.
func STUNBindNew(stunServer string) (*STUNResult, net.PacketConn, error) {
	conn, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		return nil, nil, fmt.Errorf("stun: listen: %w", err)
	}

	result, err := STUNBind(conn, stunServer)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return result, conn, nil
}

// buildSTUNBindingRequest constructs a minimal STUN Binding Request.
func buildSTUNBindingRequest(txID []byte) []byte {
	buf := make([]byte, stunHeaderSize)
	// Message Type: Binding Request (0x0001)
	binary.BigEndian.PutUint16(buf[0:2], stunBindingRequest)
	// Message Length: 0 (no attributes)
	binary.BigEndian.PutUint16(buf[2:4], 0)
	// Magic Cookie
	binary.BigEndian.PutUint32(buf[4:8], stunMagicCookie)
	// Transaction ID (12 bytes)
	copy(buf[8:20], txID)
	return buf
}

// parseSTUNBindingResponse extracts the XOR-MAPPED-ADDRESS from a STUN
// Binding Response. Falls back to MAPPED-ADDRESS if XOR variant is absent.
func parseSTUNBindingResponse(data []byte, expectedTxID []byte) (*net.UDPAddr, error) {
	if len(data) < stunHeaderSize {
		return nil, fmt.Errorf("stun: response too short (%d bytes)", len(data))
	}

	msgType := binary.BigEndian.Uint16(data[0:2])
	if msgType != stunBindingResponse {
		return nil, fmt.Errorf("stun: unexpected message type 0x%04x", msgType)
	}

	cookie := binary.BigEndian.Uint32(data[4:8])
	if cookie != stunMagicCookie {
		return nil, fmt.Errorf("stun: bad magic cookie 0x%08x", cookie)
	}

	// Verify transaction ID.
	if len(expectedTxID) == 12 {
		for i := 0; i < 12; i++ {
			if data[8+i] != expectedTxID[i] {
				return nil, fmt.Errorf("stun: transaction ID mismatch")
			}
		}
	}

	msgLen := int(binary.BigEndian.Uint16(data[2:4]))
	if stunHeaderSize+msgLen > len(data) {
		return nil, fmt.Errorf("stun: truncated response")
	}

	// Parse attributes — prefer XOR-MAPPED-ADDRESS.
	var mapped, xorMapped *net.UDPAddr
	offset := stunHeaderSize
	end := stunHeaderSize + msgLen

	for offset+4 <= end {
		attrType := binary.BigEndian.Uint16(data[offset : offset+2])
		attrLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		attrStart := offset + 4

		if attrStart+attrLen > end {
			break
		}

		switch attrType {
		case stunAttrXORMappedAddress:
			addr, err := parseXORMappedAddress(data[attrStart:attrStart+attrLen], data[4:8], data[8:20])
			if err == nil {
				xorMapped = addr
			}
		case stunAttrMappedAddress:
			addr, err := parseMappedAddress(data[attrStart : attrStart+attrLen])
			if err == nil {
				mapped = addr
			}
		}

		// Attributes are padded to 4-byte boundaries.
		offset = attrStart + ((attrLen + 3) &^ 3)
	}

	if xorMapped != nil {
		return xorMapped, nil
	}
	if mapped != nil {
		return mapped, nil
	}
	return nil, fmt.Errorf("stun: no mapped address in response")
}

// parseXORMappedAddress decodes a STUN XOR-MAPPED-ADDRESS attribute.
func parseXORMappedAddress(data []byte, magicCookie []byte, txID []byte) (*net.UDPAddr, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("xor-mapped-address too short")
	}

	family := data[1]
	xport := binary.BigEndian.Uint16(data[2:4])
	port := xport ^ binary.BigEndian.Uint16(magicCookie[0:2])

	switch family {
	case 0x01: // IPv4
		if len(data) < 8 {
			return nil, fmt.Errorf("xor-mapped-address ipv4 too short")
		}
		ip := make(net.IP, 4)
		for i := 0; i < 4; i++ {
			ip[i] = data[4+i] ^ magicCookie[i]
		}
		return &net.UDPAddr{IP: ip, Port: int(port)}, nil

	case 0x02: // IPv6
		if len(data) < 20 {
			return nil, fmt.Errorf("xor-mapped-address ipv6 too short")
		}
		ip := make(net.IP, 16)
		xorKey := append(magicCookie, txID...)
		for i := 0; i < 16; i++ {
			ip[i] = data[4+i] ^ xorKey[i]
		}
		return &net.UDPAddr{IP: ip, Port: int(port)}, nil

	default:
		return nil, fmt.Errorf("unknown address family 0x%02x", family)
	}
}

// parseMappedAddress decodes a STUN MAPPED-ADDRESS attribute (fallback).
func parseMappedAddress(data []byte) (*net.UDPAddr, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("mapped-address too short")
	}

	family := data[1]
	port := binary.BigEndian.Uint16(data[2:4])

	switch family {
	case 0x01: // IPv4
		if len(data) < 8 {
			return nil, fmt.Errorf("mapped-address ipv4 too short")
		}
		ip := net.IP(data[4:8])
		return &net.UDPAddr{IP: ip, Port: int(port)}, nil

	case 0x02: // IPv6
		if len(data) < 20 {
			return nil, fmt.Errorf("mapped-address ipv6 too short")
		}
		ip := net.IP(data[4:20])
		return &net.UDPAddr{IP: ip, Port: int(port)}, nil

	default:
		return nil, fmt.Errorf("unknown address family 0x%02x", family)
	}
}
