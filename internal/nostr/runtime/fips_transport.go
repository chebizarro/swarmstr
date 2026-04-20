//go:build experimental_fips

// Package runtime – FIPSTransport: DMTransport over a FIPS mesh.
//
// FIPSTransport sends and receives agent messages through a local FIPS daemon's
// IPv6 adapter (fips0 TUN interface). Messages are delivered as length-prefixed
// JSON frames over TCP to each peer's fd00::/8 address on a well-known port.
//
// The FIPS daemon handles mesh routing, Noise IK hop-by-hop encryption, and
// Noise XK end-to-end session encryption. FIPSTransport is the integration
// seam: it implements DMTransport so ACP, fleet RPC, and control bus code gain
// FIPS connectivity with zero changes.
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

// fipsFrameType discriminates agent message frame types.
type fipsFrameType byte

const (
	fipsFrameDM         fipsFrameType = 0x01
	fipsFrameControlReq fipsFrameType = 0x02
	fipsFrameControlRes fipsFrameType = 0x03
	fipsFramePing       fipsFrameType = 0x04
	fipsFramePong       fipsFrameType = 0x05
)

const (
	fipsMaxPayloadBytes = 256 * 1024 // 256 KiB
	fipsDialTimeout     = 5 * time.Second
	fipsWriteTimeout    = 10 * time.Second
	fipsConnIdleTimeout = 90 * time.Second
	fipsConnPoolCap     = 64
)

// fipsDMEnvelope is the JSON envelope for DM payloads over FIPS.
type fipsDMEnvelope struct {
	From string `json:"from"` // sender hex pubkey
	Text string `json:"text"` // plaintext message
	TS   int64  `json:"ts"`   // unix timestamp
}

// FIPSTransportOptions configures a FIPSTransport.
type FIPSTransportOptions struct {
	// PubkeyHex is the agent's own hex pubkey.
	PubkeyHex string
	// AgentPort is the FSP port for agent messages. Default: 1337.
	AgentPort int
	// DialTimeout overrides the default TCP dial timeout.
	DialTimeout time.Duration
	// OnMessage is called for each inbound DM received via the FIPS listener.
	OnMessage func(context.Context, InboundDM) error
	// OnError is called for transport-level errors.
	OnError func(error)
}

// FIPSTransport implements DMTransport over a FIPS mesh connection.
type FIPSTransport struct {
	pubkeyHex string
	agentPort int
	dialTTL   time.Duration
	onMessage func(context.Context, InboundDM) error
	onError   func(error)

	// Connection pool: hex pubkey → *fipsConn
	connMu sync.Mutex
	conns  map[string]*fipsConn

	// Identity cache: IPv6 string → hex pubkey (for inbound sender resolution)
	idCacheMu sync.RWMutex
	idCache   map[string]string

	listener *FIPSListener

	ctx    context.Context
	cancel context.CancelFunc
}

type fipsConn struct {
	conn     net.Conn
	lastUsed time.Time
}

// NewFIPSTransport creates a FIPSTransport. Call Start() to begin listening.
func NewFIPSTransport(opts FIPSTransportOptions) (*FIPSTransport, error) {
	if opts.PubkeyHex == "" {
		return nil, fmt.Errorf("fips transport: pubkey is required")
	}

	port := opts.AgentPort
	if port <= 0 {
		port = FIPSDefaultAgentPort
	}
	dialTTL := opts.DialTimeout
	if dialTTL <= 0 {
		dialTTL = fipsDialTimeout
	}

	ctx, cancel := context.WithCancel(context.Background())

	ft := &FIPSTransport{
		pubkeyHex: opts.PubkeyHex,
		agentPort: port,
		dialTTL:   dialTTL,
		onMessage: opts.OnMessage,
		onError:   opts.OnError,
		conns:     make(map[string]*fipsConn),
		idCache:   make(map[string]string),
		ctx:       ctx,
		cancel:    cancel,
	}

	return ft, nil
}

// Start begins listening for inbound FIPS messages. Must be called after
// NewFIPSTransport. The listener binds to the agent's own fd00::/8 address.
func (ft *FIPSTransport) Start() error {
	listenAddr, err := FIPSAddrString(ft.pubkeyHex, ft.agentPort)
	if err != nil {
		return fmt.Errorf("fips transport: derive listen address: %w", err)
	}

	listener, err := NewFIPSListener(FIPSListenerOptions{
		ListenAddr: listenAddr,
		OnMessage:  ft.handleInbound,
		OnError:    ft.emitError,
		IdentityResolver: func(remoteAddr string) string {
			return ft.resolveIdentity(remoteAddr)
		},
	})
	if err != nil {
		return fmt.Errorf("fips transport: start listener: %w", err)
	}
	ft.listener = listener
	return nil
}

// ── DMTransport interface ─────────────────────────────────────────────────────

// SendDM sends a plain-text message to the given pubkey over the FIPS mesh.
func (ft *FIPSTransport) SendDM(ctx context.Context, toPubKey string, text string) error {
	pk, err := ParsePubKey(toPubKey)
	if err != nil {
		return fmt.Errorf("fips send: %w", err)
	}
	hexPub := pk.Hex()

	// Build the DM envelope.
	env := fipsDMEnvelope{
		From: ft.pubkeyHex,
		Text: text,
		TS:   time.Now().Unix(),
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("fips send: marshal envelope: %w", err)
	}
	if len(payload) > fipsMaxPayloadBytes {
		return fmt.Errorf("fips send: payload too large (%d bytes, max %d)", len(payload), fipsMaxPayloadBytes)
	}

	return ft.sendFrame(ctx, hexPub, fipsFrameDM, payload)
}

// PublicKey returns the agent's public key in hex.
func (ft *FIPSTransport) PublicKey() string {
	return ft.pubkeyHex
}

// Relays returns nil — FIPS is relay-independent.
func (ft *FIPSTransport) Relays() []string {
	return nil
}

// SetRelays is a no-op — FIPS does not use relays.
func (ft *FIPSTransport) SetRelays(_ []string) error {
	return nil
}

// Close shuts down the transport: closes listener, drains connections.
func (ft *FIPSTransport) Close() {
	if ft.cancel != nil {
		ft.cancel()
	}

	if ft.listener != nil {
		ft.listener.Close()
	}

	ft.connMu.Lock()
	for key, fc := range ft.conns {
		fc.conn.Close()
		delete(ft.conns, key)
	}
	ft.connMu.Unlock()
}

// ── Connection management ─────────────────────────────────────────────────────

func (ft *FIPSTransport) sendFrame(ctx context.Context, toPubkeyHex string, frameType fipsFrameType, payload []byte) error {
	conn, err := ft.getOrDial(ctx, toPubkeyHex)
	if err != nil {
		return fmt.Errorf("fips send: dial %s: %w", toPubkeyHex[:12], err)
	}

	// Populate identity cache for the return path.
	ft.cacheIdentity(toPubkeyHex)

	// Write frame: [4-byte length][1-byte type][payload]
	// Length counts payload bytes only (excludes type byte).
	frame := make([]byte, 4+1+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	frame[4] = byte(frameType)
	copy(frame[5:], payload)

	if deadline, ok := ctx.Deadline(); ok {
		conn.SetWriteDeadline(deadline)
	} else {
		conn.SetWriteDeadline(time.Now().Add(fipsWriteTimeout))
	}

	if _, err := conn.Write(frame); err != nil {
		// Connection broken — evict from pool and retry once.
		ft.evictConn(toPubkeyHex)
		conn2, err2 := ft.getOrDial(ctx, toPubkeyHex)
		if err2 != nil {
			return fmt.Errorf("fips send: reconnect to %s: %w", toPubkeyHex[:12], err2)
		}
		if deadline, ok := ctx.Deadline(); ok {
			conn2.SetWriteDeadline(deadline)
		} else {
			conn2.SetWriteDeadline(time.Now().Add(fipsWriteTimeout))
		}
		if _, err3 := conn2.Write(frame); err3 != nil {
			ft.evictConn(toPubkeyHex)
			return fmt.Errorf("fips send: write retry to %s: %w", toPubkeyHex[:12], err3)
		}
	}

	// Update last-used timestamp.
	ft.connMu.Lock()
	if fc, ok := ft.conns[toPubkeyHex]; ok {
		fc.lastUsed = time.Now()
	}
	ft.connMu.Unlock()

	return nil
}

func (ft *FIPSTransport) getOrDial(ctx context.Context, toPubkeyHex string) (net.Conn, error) {
	ft.connMu.Lock()
	fc, ok := ft.conns[toPubkeyHex]
	if ok {
		fc.lastUsed = time.Now()
		ft.connMu.Unlock()
		return fc.conn, nil
	}
	ft.connMu.Unlock()

	// Dial a new connection.
	addr, err := FIPSAddrString(toPubkeyHex, ft.agentPort)
	if err != nil {
		return nil, err
	}

	dialCtx, dialCancel := context.WithTimeout(ctx, ft.dialTTL)
	defer dialCancel()

	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp6", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	ft.connMu.Lock()
	// Check again — another goroutine may have dialed concurrently.
	if existing, ok := ft.conns[toPubkeyHex]; ok {
		ft.connMu.Unlock()
		conn.Close() // discard our new connection
		return existing.conn, nil
	}
	// Enforce pool cap.
	if len(ft.conns) >= fipsConnPoolCap {
		ft.evictOldestLocked()
	}
	ft.conns[toPubkeyHex] = &fipsConn{conn: conn, lastUsed: time.Now()}
	ft.connMu.Unlock()

	return conn, nil
}

func (ft *FIPSTransport) evictConn(pubkeyHex string) {
	ft.connMu.Lock()
	defer ft.connMu.Unlock()
	if fc, ok := ft.conns[pubkeyHex]; ok {
		fc.conn.Close()
		delete(ft.conns, pubkeyHex)
	}
}

func (ft *FIPSTransport) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time
	for k, fc := range ft.conns {
		if oldestKey == "" || fc.lastUsed.Before(oldestTime) {
			oldestKey = k
			oldestTime = fc.lastUsed
		}
	}
	if oldestKey != "" {
		ft.conns[oldestKey].conn.Close()
		delete(ft.conns, oldestKey)
	}
}

// ── Identity cache ────────────────────────────────────────────────────────────

func (ft *FIPSTransport) cacheIdentity(pubkeyHex string) {
	ip, err := FIPSIPv6FromPubkey(pubkeyHex)
	if err != nil {
		return
	}
	ipStr := ip.String()
	ft.idCacheMu.Lock()
	ft.idCache[ipStr] = pubkeyHex
	ft.idCacheMu.Unlock()
}

func (ft *FIPSTransport) resolveIdentity(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	ipStr := ip.String()
	ft.idCacheMu.RLock()
	pubkey := ft.idCache[ipStr]
	ft.idCacheMu.RUnlock()
	return pubkey
}

// RegisterIdentity adds a pubkey → IPv6 mapping to the identity cache.
// Called by fleet discovery when FIPS-enabled peers are found.
func (ft *FIPSTransport) RegisterIdentity(pubkeyHex string) {
	ft.cacheIdentity(pubkeyHex)
}

// ── Inbound handling ──────────────────────────────────────────────────────────

func (ft *FIPSTransport) handleInbound(frameType fipsFrameType, payload []byte, senderPubkey string) {
	if frameType != fipsFrameDM {
		return // only DM frames handled here for now
	}

	var env fipsDMEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		ft.emitError(fmt.Errorf("fips inbound: unmarshal DM envelope: %w", err))
		return
	}

	// Use envelope sender if identity cache miss.
	fromPubkey := senderPubkey
	if fromPubkey == "" {
		fromPubkey = env.From
	}

	if ft.onMessage != nil {
		dm := InboundDM{
			FromPubKey: fromPubkey,
			Text:       env.Text,
			CreatedAt:  env.TS,
			Scheme:     "fips",
		}
		if err := ft.onMessage(ft.ctx, dm); err != nil {
			ft.emitError(fmt.Errorf("fips inbound: handler error: %w", err))
		}
	}
}

func (ft *FIPSTransport) emitError(err error) {
	if err != nil && ft.onError != nil {
		ft.onError(err)
	}
	if err != nil {
		log.Printf("fips transport: %v", err)
	}
}

// ── Compile-time interface check ──────────────────────────────────────────────

var _ DMTransport = (*FIPSTransport)(nil)
