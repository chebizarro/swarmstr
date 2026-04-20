//go:build experimental_fips

package runtime

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// dialControl connects to a FIPSControlChannel's listener on loopback.
func dialControl(t *testing.T, cc *FIPSControlChannel) net.Conn {
	t.Helper()
	addr := cc.listener.Addr().String()
	conn, err := net.DialTimeout("tcp6", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial control channel: %v", err)
	}
	return conn
}

// sendControlFrame writes a framed message (type + payload) to conn.
func sendControlFrame(t *testing.T, conn net.Conn, frameType fipsFrameType, payload []byte) {
	t.Helper()
	frame := make([]byte, 4+1+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	frame[4] = byte(frameType)
	copy(frame[5:], payload)
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// readControlFrame reads one frame from conn, returning frame type and payload.
func readControlFrame(t *testing.T, conn net.Conn) (fipsFrameType, []byte) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	header := make([]byte, 5)
	if _, err := readFull(conn, header); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	payloadLen := binary.BigEndian.Uint32(header[0:4])
	ft := fipsFrameType(header[4])
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := readFull(conn, payload); err != nil {
			t.Fatalf("read frame payload: %v", err)
		}
	}
	return ft, payload
}

// startLoopbackControlChannel creates a FIPSControlChannel listening on
// [::1]:0 (loopback, random port) to avoid needing a real fd00::/8 address.
func startLoopbackControlChannel(t *testing.T, handler func(context.Context, ControlRPCInbound) (ControlRPCResult, error)) *FIPSControlChannel {
	t.Helper()

	cc, err := NewFIPSControlChannel(FIPSControlChannelOptions{
		PubkeyHex: "aabbccdd00112233aabbccdd00112233aabbccdd00112233aabbccdd00112233",
		OnRequest: handler,
	})
	if err != nil {
		t.Fatalf("new control channel: %v", err)
	}

	// Override: bind to loopback instead of fd00:: address.
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatalf("listen loopback: %v", err)
	}
	cc.listener = ln
	cc.wg.Add(1)
	go cc.acceptLoop()

	t.Cleanup(func() { cc.Close() })
	return cc
}

func TestFIPSControlChannel_RoundTrip(t *testing.T) {
	handler := func(_ context.Context, in ControlRPCInbound) (ControlRPCResult, error) {
		if in.Method == "echo" {
			return ControlRPCResult{Result: map[string]any{"echo": string(in.Params)}}, nil
		}
		return ControlRPCResult{}, fmt.Errorf("unknown method")
	}
	cc := startLoopbackControlChannel(t, handler)
	conn := dialControl(t, cc)
	defer conn.Close()

	env := fipsControlEnvelope{
		RequestID: "req-001",
		From:      "sender-pubkey-hex",
		Method:    "echo",
		Params:    json.RawMessage(`"hello"`),
	}
	payload, _ := json.Marshal(env)
	sendControlFrame(t, conn, fipsFrameControlReq, payload)

	ft, respPayload := readControlFrame(t, conn)
	if ft != fipsFrameControlRes {
		t.Fatalf("expected ControlRes frame (0x03), got 0x%02x", ft)
	}

	var resp fipsControlResponse
	if err := json.Unmarshal(respPayload, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.RequestID != "req-001" {
		t.Errorf("request_id = %q, want %q", resp.RequestID, "req-001")
	}
	if resp.Error != "" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map", resp.Result)
	}
	if resultMap["echo"] != `"hello"` {
		t.Errorf("echo = %v, want %q", resultMap["echo"], `"hello"`)
	}
}

func TestFIPSControlChannel_HandlerError(t *testing.T) {
	handler := func(_ context.Context, in ControlRPCInbound) (ControlRPCResult, error) {
		return ControlRPCResult{}, fmt.Errorf("something broke")
	}
	cc := startLoopbackControlChannel(t, handler)
	conn := dialControl(t, cc)
	defer conn.Close()

	env := fipsControlEnvelope{
		RequestID: "req-err",
		From:      "sender",
		Method:    "fail",
	}
	payload, _ := json.Marshal(env)
	sendControlFrame(t, conn, fipsFrameControlReq, payload)

	ft, respPayload := readControlFrame(t, conn)
	if ft != fipsFrameControlRes {
		t.Fatalf("expected ControlRes, got 0x%02x", ft)
	}

	var resp fipsControlResponse
	json.Unmarshal(respPayload, &resp)
	if resp.RequestID != "req-err" {
		t.Errorf("request_id = %q, want %q", resp.RequestID, "req-err")
	}
	if resp.Error != "something broke" {
		t.Errorf("error = %q, want %q", resp.Error, "something broke")
	}
}

func TestFIPSControlChannel_MissingMethod(t *testing.T) {
	handler := func(_ context.Context, in ControlRPCInbound) (ControlRPCResult, error) {
		t.Error("handler should not be called for missing method")
		return ControlRPCResult{}, nil
	}
	cc := startLoopbackControlChannel(t, handler)
	conn := dialControl(t, cc)
	defer conn.Close()

	env := fipsControlEnvelope{
		RequestID: "req-no-method",
		From:      "sender",
		Method:    "", // empty
	}
	payload, _ := json.Marshal(env)
	sendControlFrame(t, conn, fipsFrameControlReq, payload)

	ft, respPayload := readControlFrame(t, conn)
	if ft != fipsFrameControlRes {
		t.Fatalf("expected ControlRes, got 0x%02x", ft)
	}

	var resp fipsControlResponse
	json.Unmarshal(respPayload, &resp)
	if resp.Error != "missing method" {
		t.Errorf("error = %q, want %q", resp.Error, "missing method")
	}
}

func TestFIPSControlChannel_InvalidJSON(t *testing.T) {
	var sawError bool
	handler := func(_ context.Context, in ControlRPCInbound) (ControlRPCResult, error) {
		t.Error("handler should not be called for invalid JSON")
		return ControlRPCResult{}, nil
	}
	cc := startLoopbackControlChannel(t, handler)
	cc.onError = func(err error) { sawError = true }
	conn := dialControl(t, cc)
	defer conn.Close()

	sendControlFrame(t, conn, fipsFrameControlReq, []byte(`{not valid json`))

	ft, respPayload := readControlFrame(t, conn)
	if ft != fipsFrameControlRes {
		t.Fatalf("expected ControlRes, got 0x%02x", ft)
	}

	var resp fipsControlResponse
	json.Unmarshal(respPayload, &resp)
	if resp.Error != "invalid request envelope" {
		t.Errorf("error = %q, want %q", resp.Error, "invalid request envelope")
	}
	if !sawError {
		t.Error("expected onError callback for invalid JSON")
	}
}

func TestFIPSControlChannel_PingPong(t *testing.T) {
	cc := startLoopbackControlChannel(t, func(_ context.Context, _ ControlRPCInbound) (ControlRPCResult, error) {
		return ControlRPCResult{}, nil
	})
	conn := dialControl(t, cc)
	defer conn.Close()

	sendControlFrame(t, conn, fipsFramePing, nil)

	ft, payload := readControlFrame(t, conn)
	if ft != fipsFramePong {
		t.Fatalf("expected Pong (0x05), got 0x%02x", ft)
	}
	if len(payload) != 0 {
		t.Errorf("pong payload length = %d, want 0", len(payload))
	}
}

func TestFIPSControlChannel_MultipleRequests(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	handler := func(_ context.Context, in ControlRPCInbound) (ControlRPCResult, error) {
		mu.Lock()
		methods = append(methods, in.Method)
		mu.Unlock()
		return ControlRPCResult{Result: in.Method}, nil
	}
	cc := startLoopbackControlChannel(t, handler)
	conn := dialControl(t, cc)
	defer conn.Close()

	for i := 0; i < 5; i++ {
		env := fipsControlEnvelope{
			RequestID: fmt.Sprintf("req-%d", i),
			From:      "sender",
			Method:    fmt.Sprintf("method_%d", i),
		}
		payload, _ := json.Marshal(env)
		sendControlFrame(t, conn, fipsFrameControlReq, payload)

		ft, respPayload := readControlFrame(t, conn)
		if ft != fipsFrameControlRes {
			t.Fatalf("req %d: expected ControlRes, got 0x%02x", i, ft)
		}
		var resp fipsControlResponse
		json.Unmarshal(respPayload, &resp)
		if resp.RequestID != fmt.Sprintf("req-%d", i) {
			t.Errorf("req %d: request_id = %q", i, resp.RequestID)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 5 {
		t.Errorf("handler called %d times, want 5", len(methods))
	}
}

func TestFIPSControlChannel_ResultWithError(t *testing.T) {
	// Handler returns a ControlRPCResult that has Error set (not a Go error).
	handler := func(_ context.Context, _ ControlRPCInbound) (ControlRPCResult, error) {
		return ControlRPCResult{
			Error:     "quota exceeded",
			ErrorCode: -32050,
		}, nil
	}
	cc := startLoopbackControlChannel(t, handler)
	conn := dialControl(t, cc)
	defer conn.Close()

	env := fipsControlEnvelope{RequestID: "req-re", From: "s", Method: "check"}
	payload, _ := json.Marshal(env)
	sendControlFrame(t, conn, fipsFrameControlReq, payload)

	ft, respPayload := readControlFrame(t, conn)
	if ft != fipsFrameControlRes {
		t.Fatalf("expected ControlRes, got 0x%02x", ft)
	}
	var resp fipsControlResponse
	json.Unmarshal(respPayload, &resp)
	if resp.Error != "quota exceeded" {
		t.Errorf("error = %q, want %q", resp.Error, "quota exceeded")
	}
	if resp.ErrorCode != -32050 {
		t.Errorf("error_code = %d, want %d", resp.ErrorCode, -32050)
	}
	if resp.Result != nil {
		t.Errorf("result should be nil when error is set, got %v", resp.Result)
	}
}

func TestFIPSControlChannel_InboundFields(t *testing.T) {
	// Verify that the ControlRPCInbound passed to the handler has correct fields.
	var got ControlRPCInbound
	handler := func(_ context.Context, in ControlRPCInbound) (ControlRPCResult, error) {
		got = in
		return ControlRPCResult{Result: "ok"}, nil
	}
	cc := startLoopbackControlChannel(t, handler)
	conn := dialControl(t, cc)
	defer conn.Close()

	env := fipsControlEnvelope{
		RequestID: "req-fields",
		From:      "abcd1234",
		Method:    "status.get",
		Params:    json.RawMessage(`{"key":"val"}`),
	}
	payload, _ := json.Marshal(env)
	sendControlFrame(t, conn, fipsFrameControlReq, payload)
	readControlFrame(t, conn) // consume response

	if got.RequestID != "req-fields" {
		t.Errorf("RequestID = %q, want %q", got.RequestID, "req-fields")
	}
	if got.FromPubKey != "abcd1234" {
		t.Errorf("FromPubKey = %q, want %q", got.FromPubKey, "abcd1234")
	}
	if got.Method != "status.get" {
		t.Errorf("Method = %q, want %q", got.Method, "status.get")
	}
	if got.RelayURL != "" {
		t.Errorf("RelayURL = %q, want empty (FIPS has no relay)", got.RelayURL)
	}
	if string(got.Params) != `{"key":"val"}` {
		t.Errorf("Params = %s, want %s", got.Params, `{"key":"val"}`)
	}
	if got.EventID == "" {
		t.Error("EventID should be non-empty")
	}
	if got.CreatedAt == 0 {
		t.Error("CreatedAt should be non-zero")
	}
}

func TestNewFIPSControlChannel_Validation(t *testing.T) {
	handler := func(_ context.Context, _ ControlRPCInbound) (ControlRPCResult, error) {
		return ControlRPCResult{}, nil
	}

	// Missing pubkey.
	_, err := NewFIPSControlChannel(FIPSControlChannelOptions{
		OnRequest: handler,
	})
	if err == nil {
		t.Error("expected error for missing pubkey")
	}

	// Missing handler.
	_, err = NewFIPSControlChannel(FIPSControlChannelOptions{
		PubkeyHex: "aabbccdd00112233aabbccdd00112233aabbccdd00112233aabbccdd00112233",
	})
	if err == nil {
		t.Error("expected error for missing OnRequest")
	}

	// Default port.
	cc, err := NewFIPSControlChannel(FIPSControlChannelOptions{
		PubkeyHex: "aabbccdd00112233aabbccdd00112233aabbccdd00112233aabbccdd00112233",
		OnRequest: handler,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cc.controlPort != 1338 {
		t.Errorf("default port = %d, want 1338", cc.controlPort)
	}
	cc.Close()

	// Custom port.
	cc2, err := NewFIPSControlChannel(FIPSControlChannelOptions{
		PubkeyHex:   "aabbccdd00112233aabbccdd00112233aabbccdd00112233aabbccdd00112233",
		ControlPort: 9999,
		OnRequest:   handler,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cc2.controlPort != 9999 {
		t.Errorf("custom port = %d, want 9999", cc2.controlPort)
	}
	cc2.Close()
}

func TestFIPSControlChannel_Close(t *testing.T) {
	cc := startLoopbackControlChannel(t, func(_ context.Context, _ ControlRPCInbound) (ControlRPCResult, error) {
		return ControlRPCResult{}, nil
	})

	// Dial before close.
	conn := dialControl(t, cc)
	defer conn.Close()

	// Close the channel.
	cc.Close()

	// Subsequent dial should fail.
	_, err := net.DialTimeout("tcp6", cc.listener.Addr().String(), 500*time.Millisecond)
	if err == nil {
		t.Error("expected dial to fail after Close()")
	}
}
