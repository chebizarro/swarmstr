//go:build experimental_fips

// Package runtime – FIPSControlChannel: JSON-RPC control messages over FIPS mesh.
//
// FIPSControlChannel provides a dedicated listener on the agent's FIPS control
// port (default 1338) for receiving control RPC requests directly from mesh
// peers. Responses are sent back over the same TCP connection rather than
// publishing Nostr events, bypassing relay round-trips entirely.
//
// The channel reuses the existing ControlRPCInbound/ControlRPCResult types and
// routes inbound requests to the same OnRequest handler as the Nostr-based
// ControlRPCBus, so all control methods work identically over both transports.
package runtime

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// fipsControlEnvelope is the JSON envelope for control RPC over FIPS.
type fipsControlEnvelope struct {
	RequestID string          `json:"req_id"`
	From      string          `json:"from"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// fipsControlResponse is the JSON envelope for control RPC responses.
type fipsControlResponse struct {
	RequestID string `json:"req_id"`
	Result    any    `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorCode int    `json:"error_code,omitempty"`
}

// FIPSControlChannelOptions configures a FIPSControlChannel.
type FIPSControlChannelOptions struct {
	// PubkeyHex is the agent's own hex pubkey.
	PubkeyHex string
	// ControlPort is the FSP port for control messages. Default: 1338.
	ControlPort int
	// OnRequest is the shared handler for control RPC requests.
	OnRequest func(context.Context, ControlRPCInbound) (ControlRPCResult, error)
	// OnError is called for transport-level errors.
	OnError func(error)
}

// FIPSControlChannel listens for control RPC requests over FIPS and routes
// them to the shared OnRequest handler.
type FIPSControlChannel struct {
	pubkeyHex   string
	controlPort int
	onRequest   func(context.Context, ControlRPCInbound) (ControlRPCResult, error)
	onError     func(error)

	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewFIPSControlChannel creates a FIPSControlChannel. Call Start() to begin.
func NewFIPSControlChannel(opts FIPSControlChannelOptions) (*FIPSControlChannel, error) {
	if opts.PubkeyHex == "" {
		return nil, fmt.Errorf("fips control: pubkey is required")
	}
	if opts.OnRequest == nil {
		return nil, fmt.Errorf("fips control: OnRequest handler is required")
	}
	port := opts.ControlPort
	if port <= 0 {
		port = 1338
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &FIPSControlChannel{
		pubkeyHex:   opts.PubkeyHex,
		controlPort: port,
		onRequest:   opts.OnRequest,
		onError:     opts.OnError,
		ctx:         ctx,
		cancel:      cancel,
	}, nil
}

// Start binds the listener to the agent's fd00::/8 address on the control port.
func (cc *FIPSControlChannel) Start() error {
	listenAddr, err := FIPSAddrString(cc.pubkeyHex, cc.controlPort)
	if err != nil {
		return fmt.Errorf("fips control: derive listen address: %w", err)
	}

	ln, err := net.Listen("tcp6", listenAddr)
	if err != nil {
		return fmt.Errorf("fips control: listen %s: %w", listenAddr, err)
	}
	cc.listener = ln
	log.Printf("fips control: listening on %s", listenAddr)

	cc.wg.Add(1)
	go cc.acceptLoop()
	return nil
}

// ListenerAddr returns the control channel listener address, or empty if not listening.
func (cc *FIPSControlChannel) ListenerAddr() string {
	if cc.listener == nil {
		return ""
	}
	return cc.listener.Addr().String()
}

// Close shuts down the control channel.
func (cc *FIPSControlChannel) Close() {
	cc.cancel()
	if cc.listener != nil {
		cc.listener.Close()
	}
	cc.wg.Wait()
}

func (cc *FIPSControlChannel) acceptLoop() {
	defer cc.wg.Done()
	for {
		conn, err := cc.listener.Accept()
		if err != nil {
			select {
			case <-cc.ctx.Done():
				return
			default:
			}
			cc.emitError(fmt.Errorf("fips control: accept: %w", err))
			continue
		}
		cc.wg.Add(1)
		go cc.handleConn(conn)
	}
}

func (cc *FIPSControlChannel) handleConn(conn net.Conn) {
	defer cc.wg.Done()
	defer conn.Close()

	for {
		select {
		case <-cc.ctx.Done():
			return
		default:
		}

		// Read frame header: [4-byte length][1-byte type]
		conn.SetReadDeadline(time.Now().Add(fipsConnIdleTimeout))
		header := make([]byte, 5)
		if _, err := readFull(conn, header); err != nil {
			return // connection closed or timed out
		}
		payloadLen := binary.BigEndian.Uint32(header[0:4])
		frameType := fipsFrameType(header[4])

		if payloadLen > uint32(fipsMaxPayloadBytes) {
			cc.emitError(fmt.Errorf("fips control: frame too large (%d bytes)", payloadLen))
			return
		}

		payload := make([]byte, payloadLen)
		if payloadLen > 0 {
			if _, err := readFull(conn, payload); err != nil {
				return
			}
		}

		switch frameType {
		case fipsFrameControlReq:
			cc.handleControlRequest(conn, payload)
		case fipsFramePing:
			cc.sendPong(conn)
		default:
			// Ignore unknown frame types.
		}
	}
}

func (cc *FIPSControlChannel) handleControlRequest(conn net.Conn, payload []byte) {
	var env fipsControlEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		cc.emitError(fmt.Errorf("fips control: unmarshal request: %w", err))
		cc.sendControlResponse(conn, fipsControlResponse{
			Error: "invalid request envelope",
		})
		return
	}

	if env.Method == "" {
		cc.sendControlResponse(conn, fipsControlResponse{
			RequestID: env.RequestID,
			Error:     "missing method",
		})
		return
	}

	// Build the ControlRPCInbound and route to the shared handler.
	inbound := ControlRPCInbound{
		EventID:    fmt.Sprintf("fips-%s-%d", env.RequestID, time.Now().UnixNano()),
		RequestID:  env.RequestID,
		FromPubKey: env.From,
		RelayURL:   "", // FIPS — no relay
		Method:     env.Method,
		Params:     env.Params,
		CreatedAt:  time.Now().Unix(),
	}

	result, err := cc.onRequest(cc.ctx, inbound)
	if err != nil {
		resp := fipsControlResponse{
			RequestID: env.RequestID,
			Error:     err.Error(),
		}
		if coded, ok := err.(codedDataError); ok {
			resp.ErrorCode = coded.ErrorCode()
		}
		cc.sendControlResponse(conn, resp)
		return
	}

	resp := fipsControlResponse{
		RequestID: env.RequestID,
		Result:    result.Result,
	}
	if result.Error != "" {
		resp.Error = result.Error
		resp.ErrorCode = result.ErrorCode
		resp.Result = nil
	}
	cc.sendControlResponse(conn, resp)
}

func (cc *FIPSControlChannel) sendControlResponse(conn net.Conn, resp fipsControlResponse) {
	payload, err := json.Marshal(resp)
	if err != nil {
		cc.emitError(fmt.Errorf("fips control: marshal response: %w", err))
		return
	}

	frame := make([]byte, 4+1+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	frame[4] = byte(fipsFrameControlRes)
	copy(frame[5:], payload)

	conn.SetWriteDeadline(time.Now().Add(fipsWriteTimeout))
	if _, err := conn.Write(frame); err != nil {
		cc.emitError(fmt.Errorf("fips control: write response: %w", err))
	}
}

func (cc *FIPSControlChannel) sendPong(conn net.Conn) {
	frame := make([]byte, 5)
	binary.BigEndian.PutUint32(frame[0:4], 0)
	frame[4] = byte(fipsFramePong)
	conn.SetWriteDeadline(time.Now().Add(fipsWriteTimeout))
	conn.Write(frame)
}

func (cc *FIPSControlChannel) emitError(err error) {
	if err == nil {
		return
	}
	if cc.onError != nil {
		cc.onError(err)
	}
	log.Printf("fips control: %v", err)
}

// readFull reads exactly len(buf) bytes from the connection.
func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
