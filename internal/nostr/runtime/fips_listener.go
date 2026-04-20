//go:build experimental_fips

// Package runtime – FIPSListener: inbound message receiver for FIPS mesh.
//
// Listens on the agent's fd00::/8 address for incoming TCP connections from
// other FIPS mesh agents. Reads length-prefixed frames and dispatches them
// to the registered handler.
package runtime

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	fipsListenerAcceptTimeout = 1 * time.Second
	fipsReadTimeout           = 30 * time.Second
)

// FIPSListenerOptions configures a FIPSListener.
type FIPSListenerOptions struct {
	// ListenAddr is the "[fd00:...]:port" address to bind.
	ListenAddr string
	// OnMessage is called for each received frame.
	OnMessage func(frameType fipsFrameType, payload []byte, senderPubkey string)
	// OnError is called for listener-level errors.
	OnError func(error)
	// IdentityResolver maps a remote address string to a hex pubkey.
	IdentityResolver func(remoteAddr string) string
}

// FIPSListener accepts inbound FIPS agent connections.
type FIPSListener struct {
	listener  net.Listener
	onMessage func(fipsFrameType, []byte, string)
	onError   func(error)
	resolveID func(string) string

	wg     sync.WaitGroup
	closed chan struct{}
}

// NewFIPSListener creates and starts a FIPSListener.
func NewFIPSListener(opts FIPSListenerOptions) (*FIPSListener, error) {
	if opts.ListenAddr == "" {
		return nil, fmt.Errorf("fips listener: listen address required")
	}
	if opts.OnMessage == nil {
		return nil, fmt.Errorf("fips listener: OnMessage handler required")
	}

	ln, err := net.Listen("tcp6", opts.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("fips listener: bind %s: %w", opts.ListenAddr, err)
	}

	fl := &FIPSListener{
		listener:  ln,
		onMessage: opts.OnMessage,
		onError:   opts.OnError,
		resolveID: opts.IdentityResolver,
		closed:    make(chan struct{}),
	}

	fl.wg.Add(1)
	go fl.acceptLoop()

	log.Printf("fips listener: listening on %s", opts.ListenAddr)
	return fl, nil
}

// Close stops accepting connections and waits for in-flight handlers.
func (fl *FIPSListener) Close() {
	close(fl.closed)
	fl.listener.Close()
	fl.wg.Wait()
}

// Addr returns the listener's network address.
func (fl *FIPSListener) Addr() net.Addr {
	return fl.listener.Addr()
}

func (fl *FIPSListener) acceptLoop() {
	defer fl.wg.Done()
	for {
		conn, err := fl.listener.Accept()
		if err != nil {
			select {
			case <-fl.closed:
				return
			default:
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			fl.emitError(fmt.Errorf("fips listener: accept: %w", err))
			return
		}

		fl.wg.Add(1)
		go fl.handleConn(conn)
	}
}

func (fl *FIPSListener) handleConn(conn net.Conn) {
	defer fl.wg.Done()
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	senderPubkey := ""
	if fl.resolveID != nil {
		senderPubkey = fl.resolveID(remoteAddr)
	}

	for {
		select {
		case <-fl.closed:
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(fipsReadTimeout))

		// Read frame header: 4-byte length + 1-byte type.
		var header [5]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			if err != io.EOF && !isClosedErr(err) {
				fl.emitError(fmt.Errorf("fips listener: read header from %s: %w", remoteAddr, err))
			}
			return
		}

		payloadLen := binary.BigEndian.Uint32(header[0:4])
		frameType := fipsFrameType(header[4])

		if payloadLen > fipsMaxPayloadBytes {
			fl.emitError(fmt.Errorf("fips listener: payload too large from %s (%d bytes)", remoteAddr, payloadLen))
			return
		}

		// Handle ping/pong without reading payload.
		if frameType == fipsFramePing {
			pongFrame := [5]byte{}
			binary.BigEndian.PutUint32(pongFrame[0:4], 0)
			pongFrame[4] = byte(fipsFramePong)
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			conn.Write(pongFrame[:])
			continue
		}
		if frameType == fipsFramePong {
			continue
		}

		// Read payload.
		payload := make([]byte, payloadLen)
		if payloadLen > 0 {
			if _, err := io.ReadFull(conn, payload); err != nil {
				fl.emitError(fmt.Errorf("fips listener: read payload from %s: %w", remoteAddr, err))
				return
			}
		}

		// Re-resolve identity on each message — the cache may have been
		// populated since the connection was accepted.
		if senderPubkey == "" && fl.resolveID != nil {
			senderPubkey = fl.resolveID(remoteAddr)
		}

		fl.onMessage(frameType, payload, senderPubkey)
	}
}

func (fl *FIPSListener) emitError(err error) {
	if err != nil && fl.onError != nil {
		fl.onError(err)
	}
}

func isClosedErr(err error) bool {
	if err == nil {
		return false
	}
	// net.ErrClosed or "use of closed network connection"
	if opErr, ok := err.(*net.OpError); ok {
		return opErr.Err.Error() == "use of closed network connection"
	}
	return false
}
