package acp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// PeerEntry describes a known remote agent peer.
type PeerEntry struct {
	// PubKey is the Nostr public key of the remote agent (hex, no-prefix).
	PubKey string
	// Alias is a human-readable name for the peer.
	Alias string
	// Tags holds arbitrary key/value metadata (e.g. capabilities, region).
	Tags map[string]string
}

// PeerRegistry tracks known ACP peer agents by pubkey.
// All methods are goroutine-safe.
type PeerRegistry struct {
	mu    sync.RWMutex
	peers map[string]PeerEntry // key: pubkey
}

// NewPeerRegistry creates an empty PeerRegistry.
func NewPeerRegistry() *PeerRegistry {
	return &PeerRegistry{peers: make(map[string]PeerEntry)}
}

// Register adds or updates a peer.
func (r *PeerRegistry) Register(e PeerEntry) error {
	pk := strings.TrimSpace(e.PubKey)
	if pk == "" {
		return fmt.Errorf("peer pubkey required")
	}
	e.PubKey = pk
	r.mu.Lock()
	r.peers[pk] = e
	r.mu.Unlock()
	return nil
}

// Remove unregisters a peer by pubkey.
func (r *PeerRegistry) Remove(pubkey string) {
	r.mu.Lock()
	delete(r.peers, strings.TrimSpace(pubkey))
	r.mu.Unlock()
}

// Get returns the PeerEntry for the given pubkey and whether it exists.
func (r *PeerRegistry) Get(pubkey string) (PeerEntry, bool) {
	r.mu.RLock()
	e, ok := r.peers[strings.TrimSpace(pubkey)]
	r.mu.RUnlock()
	return e, ok
}

// IsPeer reports whether the pubkey is a registered ACP peer.
func (r *PeerRegistry) IsPeer(pubkey string) bool {
	_, ok := r.Get(pubkey)
	return ok
}

// List returns all registered peers.
func (r *PeerRegistry) List() []PeerEntry {
	r.mu.RLock()
	out := make([]PeerEntry, 0, len(r.peers))
	for _, e := range r.peers {
		out = append(out, e)
	}
	r.mu.RUnlock()
	return out
}

// Parse attempts to decode raw bytes as an ACP Message.
// Returns (msg, nil) on success, or (zero, err) on failure.
func Parse(raw []byte) (Message, error) {
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return Message{}, err
	}
	if strings.TrimSpace(msg.ACPType) == "" {
		return Message{}, fmt.Errorf("not an ACP message: missing acp_type")
	}
	return msg, nil
}
