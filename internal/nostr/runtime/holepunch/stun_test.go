//go:build experimental_fips

package holepunch

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestBuildSTUNBindingRequest(t *testing.T) {
	txID := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c}
	req := buildSTUNBindingRequest(txID)

	if len(req) != stunHeaderSize {
		t.Fatalf("request size = %d, want %d", len(req), stunHeaderSize)
	}

	// Message type: Binding Request (0x0001).
	msgType := binary.BigEndian.Uint16(req[0:2])
	if msgType != stunBindingRequest {
		t.Errorf("message type = 0x%04x, want 0x%04x", msgType, stunBindingRequest)
	}

	// Message length: 0 (no attributes).
	msgLen := binary.BigEndian.Uint16(req[2:4])
	if msgLen != 0 {
		t.Errorf("message length = %d, want 0", msgLen)
	}

	// Magic cookie.
	cookie := binary.BigEndian.Uint32(req[4:8])
	if cookie != stunMagicCookie {
		t.Errorf("magic cookie = 0x%08x, want 0x%08x", cookie, stunMagicCookie)
	}

	// Transaction ID.
	for i := 0; i < 12; i++ {
		if req[8+i] != txID[i] {
			t.Errorf("txID[%d] = 0x%02x, want 0x%02x", i, req[8+i], txID[i])
		}
	}
}

func TestParseSTUNBindingResponse_XORMapped_IPv4(t *testing.T) {
	txID := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c}

	// Build a synthetic STUN Binding Response with XOR-MAPPED-ADDRESS.
	// Target address: 198.51.100.1:12345
	targetIP := net.ParseIP("198.51.100.1").To4()
	targetPort := uint16(12345)

	// XOR the port with the first 2 bytes of the magic cookie.
	magicBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(magicBytes, stunMagicCookie)
	xPort := targetPort ^ binary.BigEndian.Uint16(magicBytes[0:2])

	// XOR the IP with the magic cookie.
	xIP := make([]byte, 4)
	for i := 0; i < 4; i++ {
		xIP[i] = targetIP[i] ^ magicBytes[i]
	}

	// Build attribute: XOR-MAPPED-ADDRESS
	// Family: 0x01 (IPv4), Port, IP
	attr := []byte{
		0x00, 0x01, // reserved + family (IPv4)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, xPort)
	attr = append(attr, portBytes...)
	attr = append(attr, xIP...)

	// Build full response.
	resp := make([]byte, stunHeaderSize)
	binary.BigEndian.PutUint16(resp[0:2], stunBindingResponse)
	// Attribute: type(2) + length(2) + value(8) = 12 bytes
	attrHeader := make([]byte, 4)
	binary.BigEndian.PutUint16(attrHeader[0:2], stunAttrXORMappedAddress)
	binary.BigEndian.PutUint16(attrHeader[2:4], uint16(len(attr)))
	fullAttr := append(attrHeader, attr...)
	binary.BigEndian.PutUint16(resp[2:4], uint16(len(fullAttr)))
	binary.BigEndian.PutUint32(resp[4:8], stunMagicCookie)
	copy(resp[8:20], txID)
	resp = append(resp, fullAttr...)

	addr, err := parseSTUNBindingResponse(resp, txID)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if !addr.IP.Equal(targetIP) {
		t.Errorf("IP = %s, want %s", addr.IP, targetIP)
	}
	if addr.Port != int(targetPort) {
		t.Errorf("port = %d, want %d", addr.Port, targetPort)
	}
}

func TestParseSTUNBindingResponse_BadMagic(t *testing.T) {
	resp := make([]byte, stunHeaderSize)
	binary.BigEndian.PutUint16(resp[0:2], stunBindingResponse)
	binary.BigEndian.PutUint32(resp[4:8], 0xDEADBEEF) // wrong magic

	_, err := parseSTUNBindingResponse(resp, nil)
	if err == nil {
		t.Fatal("expected error for bad magic cookie")
	}
}

func TestParseSTUNBindingResponse_WrongType(t *testing.T) {
	resp := make([]byte, stunHeaderSize)
	binary.BigEndian.PutUint16(resp[0:2], 0x0111) // not a binding response
	binary.BigEndian.PutUint32(resp[4:8], stunMagicCookie)

	_, err := parseSTUNBindingResponse(resp, nil)
	if err == nil {
		t.Fatal("expected error for wrong message type")
	}
}

func TestParseSTUNBindingResponse_TxIDMismatch(t *testing.T) {
	resp := make([]byte, stunHeaderSize)
	binary.BigEndian.PutUint16(resp[0:2], stunBindingResponse)
	binary.BigEndian.PutUint32(resp[4:8], stunMagicCookie)
	copy(resp[8:20], []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})

	expectedTxID := []byte{99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99}
	_, err := parseSTUNBindingResponse(resp, expectedTxID)
	if err == nil {
		t.Fatal("expected error for txID mismatch")
	}
}

func TestParseSTUNBindingResponse_TooShort(t *testing.T) {
	_, err := parseSTUNBindingResponse([]byte{0x01, 0x01}, nil)
	if err == nil {
		t.Fatal("expected error for short response")
	}
}

func TestParseSTUNBindingResponse_NoMappedAddress(t *testing.T) {
	resp := make([]byte, stunHeaderSize)
	binary.BigEndian.PutUint16(resp[0:2], stunBindingResponse)
	binary.BigEndian.PutUint16(resp[2:4], 0) // no attributes
	binary.BigEndian.PutUint32(resp[4:8], stunMagicCookie)

	_, err := parseSTUNBindingResponse(resp, nil)
	if err == nil {
		t.Fatal("expected error for missing mapped address")
	}
}

// TestSTUNBind_LoopbackMock tests STUNBind against a mock STUN server.
func TestSTUNBind_LoopbackMock(t *testing.T) {
	// Create a mock STUN server on loopback.
	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mock server listen: %v", err)
	}
	defer serverConn.Close()

	go func() {
		buf := make([]byte, 512)
		n, clientAddr, err := serverConn.ReadFrom(buf)
		if err != nil || n < stunHeaderSize {
			return
		}
		// Extract txID from request.
		txID := buf[8:20]

		// Build response with XOR-MAPPED-ADDRESS for the client.
		udpAddr := clientAddr.(*net.UDPAddr)
		resp := buildMockSTUNResponse(txID, udpAddr)
		serverConn.WriteTo(resp, clientAddr)
	}()

	// Create client socket.
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("client listen: %v", err)
	}
	defer clientConn.Close()

	result, err := STUNBind(clientConn, serverConn.LocalAddr().String())
	if err != nil {
		t.Fatalf("STUNBind: %v", err)
	}

	if result.ReflexiveAddr == nil {
		t.Fatal("expected non-nil reflexive address")
	}
	if result.RTT <= 0 {
		t.Error("expected positive RTT")
	}
	if result.ServerUsed != serverConn.LocalAddr().String() {
		t.Errorf("server used = %q", result.ServerUsed)
	}
}

func TestSTUNBind_Timeout(t *testing.T) {
	// Use a random port that nobody is listening on.
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("client listen: %v", err)
	}
	defer clientConn.Close()

	start := time.Now()
	_, err = STUNBind(clientConn, "127.0.0.1:1") // nothing listening
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	// Should complete within a reasonable time (not hang forever).
	if elapsed > 15*time.Second {
		t.Errorf("took %s, expected < 15s", elapsed)
	}
}

// buildMockSTUNResponse creates a STUN Binding Response with XOR-MAPPED-ADDRESS.
func buildMockSTUNResponse(txID []byte, addr *net.UDPAddr) []byte {
	magicBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(magicBytes, stunMagicCookie)

	ip4 := addr.IP.To4()
	if ip4 == nil {
		ip4 = addr.IP
	}

	xPort := make([]byte, 2)
	binary.BigEndian.PutUint16(xPort, uint16(addr.Port)^binary.BigEndian.Uint16(magicBytes[0:2]))

	xIP := make([]byte, 4)
	for i := 0; i < 4; i++ {
		xIP[i] = ip4[i] ^ magicBytes[i]
	}

	// XOR-MAPPED-ADDRESS attribute value.
	attrValue := []byte{0x00, 0x01} // reserved + IPv4 family
	attrValue = append(attrValue, xPort...)
	attrValue = append(attrValue, xIP...)

	// Attribute header.
	attrHeader := make([]byte, 4)
	binary.BigEndian.PutUint16(attrHeader[0:2], stunAttrXORMappedAddress)
	binary.BigEndian.PutUint16(attrHeader[2:4], uint16(len(attrValue)))
	fullAttr := append(attrHeader, attrValue...)

	// Response header.
	resp := make([]byte, stunHeaderSize)
	binary.BigEndian.PutUint16(resp[0:2], stunBindingResponse)
	binary.BigEndian.PutUint16(resp[2:4], uint16(len(fullAttr)))
	binary.BigEndian.PutUint32(resp[4:8], stunMagicCookie)
	copy(resp[8:20], txID)

	return append(resp, fullAttr...)
}
