//go:build experimental_fips

package holepunch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// SignalSender sends an encrypted, gift-wrapped signaling message to a peer.
type SignalSender func(ctx context.Context, toPubKey string, signal Signal, relays []string) error

// SignalReceiver returns a channel that yields incoming signals for our pubkey.
// The caller must cancel the context to stop receiving.
type SignalReceiver func(ctx context.Context, relays []string) (<-chan Signal, error)

// OnPunchSuccess is called when a hole punch succeeds, handing over the
// established UDP socket and confirmed peer address.
type OnPunchSuccess func(peerPubKey string, conn net.PacketConn, peerAddr *net.UDPAddr, sessionID string)

// ManagerOptions configures the hole punch Manager.
type ManagerOptions struct {
	// PubKeyHex is this agent's Nostr pubkey.
	PubKeyHex string
	// STUNServers is the list of STUN servers to use.
	STUNServers []string
	// SignalRelays is where signaling messages are sent/received.
	SignalRelays []string
	// SendSignal sends a signaling message to a peer.
	SendSignal SignalSender
	// ReceiveSignal listens for incoming signaling messages.
	ReceiveSignal SignalReceiver
	// OnSuccess is called when a hole punch completes successfully.
	OnSuccess OnPunchSuccess
	// OnError is called for non-fatal errors.
	OnError func(error)
	// SignalMaxAge is the maximum age for incoming signals. Default: 60s.
	SignalMaxAge time.Duration
	// SessionReplayWindow tracks recent session IDs for replay protection.
	// Default: 1000 entries.
	SessionReplayWindow int
}

// Manager orchestrates the Nostr-signaled UDP hole punch protocol.
//
// It can act as both initiator (calling Initiate) and responder (started via
// ListenForOffers). The manager handles the full 7-phase protocol:
// STUN → Signal → Punch → Handoff.
type Manager struct {
	opts ManagerOptions

	// Replay protection.
	replayMu   sync.Mutex
	replaySeen map[string]time.Time
	replayList []string
	replayCap  int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewManager creates a hole punch Manager. Call ListenForOffers to start
// responding to incoming punch requests.
func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.PubKeyHex == "" {
		return nil, fmt.Errorf("holepunch: pubkey is required")
	}
	if len(opts.STUNServers) == 0 {
		return nil, fmt.Errorf("holepunch: at least one STUN server is required")
	}
	if opts.SendSignal == nil {
		return nil, fmt.Errorf("holepunch: SendSignal is required")
	}
	if opts.ReceiveSignal == nil {
		return nil, fmt.Errorf("holepunch: ReceiveSignal is required")
	}
	if opts.SignalMaxAge <= 0 {
		opts.SignalMaxAge = 60 * time.Second
	}
	replayCap := opts.SessionReplayWindow
	if replayCap <= 0 {
		replayCap = 1000
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		opts:       opts,
		replaySeen: make(map[string]time.Time),
		replayCap:  replayCap,
		ctx:        ctx,
		cancel:     cancel,
	}, nil
}

// Close stops the manager and all background goroutines.
func (m *Manager) Close() {
	m.cancel()
	m.wg.Wait()
}

// Initiate starts a hole punch to peerPubKey. This is the initiator flow:
// STUN bind → send offer → wait for answer → punch.
//
// This method blocks until the punch succeeds, times out, or the context
// is cancelled.
func (m *Manager) Initiate(ctx context.Context, peerPubKey string, peerRelays []string) (*PunchResult, error) {
	if len(peerRelays) == 0 {
		peerRelays = m.opts.SignalRelays
	}

	// Phase 2: STUN binding.
	stunServer := m.opts.STUNServers[0]
	stunResult, conn, err := STUNBindNew(stunServer)
	if err != nil {
		return nil, fmt.Errorf("holepunch initiate: stun: %w", err)
	}
	defer func() {
		// Only close conn if punch didn't succeed (caller takes ownership on success).
		// This is handled by OnSuccess callback.
	}()

	sessionID := generateSessionID()

	// Phase 3: Send offer.
	offer := Signal{
		Type:          SignalOffer,
		SessionID:     sessionID,
		ReflexiveAddr: stunResult.ReflexiveAddr.String(),
		LocalAddr:     stunResult.LocalAddr.String(),
		STUNServer:    stunServer,
		Timestamp:     time.Now().Unix(),
	}
	if err := m.opts.SendSignal(ctx, peerPubKey, offer, peerRelays); err != nil {
		conn.Close()
		return nil, fmt.Errorf("holepunch initiate: send offer: %w", err)
	}
	log.Printf("holepunch: sent offer to %s session=%s reflexive=%s",
		peerPubKey[:12], sessionID[:8], stunResult.ReflexiveAddr)

	// Listen for answer.
	sigCh, err := m.opts.ReceiveSignal(ctx, peerRelays)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("holepunch initiate: listen for answer: %w", err)
	}

	// Phase 4: Wait for answer.
	var answer Signal
	answerTimeout := time.After(30 * time.Second)
	select {
	case <-ctx.Done():
		conn.Close()
		return nil, ctx.Err()
	case <-answerTimeout:
		conn.Close()
		return nil, fmt.Errorf("holepunch initiate: no answer from %s within 30s", peerPubKey[:12])
	case sig, ok := <-sigCh:
		if !ok {
			conn.Close()
			return nil, fmt.Errorf("holepunch initiate: signal channel closed")
		}
		if sig.Type != SignalAnswer || sig.SessionID != sessionID {
			conn.Close()
			return nil, fmt.Errorf("holepunch initiate: unexpected signal type=%s session=%s", sig.Type, sig.SessionID)
		}
		answer = sig
	}

	// Phase 5: Punch.
	peerAddr, err := net.ResolveUDPAddr("udp", answer.ReflexiveAddr)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("holepunch initiate: resolve peer addr: %w", err)
	}

	var peerLocalAddr *net.UDPAddr
	if answer.LocalAddr != "" {
		peerLocalAddr, _ = net.ResolveUDPAddr("udp", answer.LocalAddr)
	}

	log.Printf("holepunch: punching %s reflexive=%s session=%s",
		peerPubKey[:12], peerAddr, sessionID[:8])

	result := Punch(PunchOptions{
		Conn:          conn,
		PeerAddr:      peerAddr,
		PeerLocalAddr: peerLocalAddr,
		SessionID:     sessionID,
	})

	if result.Success && m.opts.OnSuccess != nil {
		m.opts.OnSuccess(peerPubKey, conn, result.PeerAddr, sessionID)
	} else if !result.Success {
		conn.Close()
	}

	return &result, nil
}

// ListenForOffers starts a background goroutine that responds to incoming
// hole punch offers. For each valid offer, it performs STUN binding, sends
// an answer, and initiates the punch phase.
func (m *Manager) ListenForOffers() error {
	sigCh, err := m.opts.ReceiveSignal(m.ctx, m.opts.SignalRelays)
	if err != nil {
		return fmt.Errorf("holepunch: listen for offers: %w", err)
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			select {
			case <-m.ctx.Done():
				return
			case sig, ok := <-sigCh:
				if !ok {
					return
				}
				if sig.Type != SignalOffer {
					continue
				}
				m.wg.Add(1)
				go func(offer Signal) {
					defer m.wg.Done()
					m.handleOffer(offer)
				}(sig)
			}
		}
	}()

	return nil
}

func (m *Manager) handleOffer(offer Signal) {
	// Validate.
	if offer.IsExpired(m.opts.SignalMaxAge) {
		m.emitError(fmt.Errorf("holepunch: expired offer session=%s", offer.SessionID))
		return
	}
	if m.isReplay(offer.SessionID) {
		m.emitError(fmt.Errorf("holepunch: replay offer session=%s", offer.SessionID))
		return
	}
	m.markSeen(offer.SessionID)

	// Phase 2: STUN binding.
	stunServer := offer.STUNServer
	if stunServer == "" && len(m.opts.STUNServers) > 0 {
		stunServer = m.opts.STUNServers[0]
	}

	stunResult, conn, err := STUNBindNew(stunServer)
	if err != nil {
		m.emitError(fmt.Errorf("holepunch responder: stun: %w", err))
		return
	}

	// Phase 4: Send answer.
	answer := Signal{
		Type:          SignalAnswer,
		SessionID:     offer.SessionID,
		ReflexiveAddr: stunResult.ReflexiveAddr.String(),
		LocalAddr:     stunResult.LocalAddr.String(),
		STUNServer:    stunServer,
		Timestamp:     time.Now().Unix(),
	}

	// We need to know the offer sender's pubkey to send the answer.
	// This is passed through the signal's AppParams or inferred from the
	// gift-wrap sender. For now, we extract it from AppParams if available.
	// In production, the SignalReceiver should include the sender pubkey.
	senderPubKey := ""
	if offer.AppParams != nil {
		var params struct {
			From string `json:"from"`
		}
		if err := json.Unmarshal(offer.AppParams, &params); err == nil {
			senderPubKey = params.From
		}
	}
	if senderPubKey == "" {
		m.emitError(fmt.Errorf("holepunch responder: cannot determine offer sender pubkey"))
		conn.Close()
		return
	}

	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()

	if err := m.opts.SendSignal(ctx, senderPubKey, answer, m.opts.SignalRelays); err != nil {
		m.emitError(fmt.Errorf("holepunch responder: send answer: %w", err))
		conn.Close()
		return
	}

	log.Printf("holepunch: responding to %s session=%s reflexive=%s",
		senderPubKey[:min(12, len(senderPubKey))], offer.SessionID[:min(8, len(offer.SessionID))], stunResult.ReflexiveAddr)

	// Phase 5: Punch immediately after sending answer.
	peerAddr, err := net.ResolveUDPAddr("udp", offer.ReflexiveAddr)
	if err != nil {
		m.emitError(fmt.Errorf("holepunch responder: resolve peer: %w", err))
		conn.Close()
		return
	}

	var peerLocalAddr *net.UDPAddr
	if offer.LocalAddr != "" {
		peerLocalAddr, _ = net.ResolveUDPAddr("udp", offer.LocalAddr)
	}

	result := Punch(PunchOptions{
		Conn:          conn,
		PeerAddr:      peerAddr,
		PeerLocalAddr: peerLocalAddr,
		SessionID:     offer.SessionID,
	})

	if result.Success && m.opts.OnSuccess != nil {
		m.opts.OnSuccess(senderPubKey, conn, result.PeerAddr, offer.SessionID)
	} else {
		if !result.Success {
			m.emitError(fmt.Errorf("holepunch responder: punch failed: %v", result.Error))
		}
		conn.Close()
	}
}

// ── Replay protection ─────────────────────────────────────────────────────

func (m *Manager) isReplay(sessionID string) bool {
	m.replayMu.Lock()
	defer m.replayMu.Unlock()
	_, seen := m.replaySeen[sessionID]
	return seen
}

func (m *Manager) markSeen(sessionID string) {
	m.replayMu.Lock()
	defer m.replayMu.Unlock()
	if _, exists := m.replaySeen[sessionID]; !exists {
		m.replaySeen[sessionID] = time.Now()
		m.replayList = append(m.replayList, sessionID)
		if len(m.replayList) > m.replayCap {
			victim := m.replayList[0]
			m.replayList = m.replayList[1:]
			delete(m.replaySeen, victim)
		}
	}
}

func (m *Manager) emitError(err error) {
	if err == nil {
		return
	}
	if m.opts.OnError != nil {
		m.opts.OnError(err)
	}
	log.Printf("holepunch: %v", err)
}

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
