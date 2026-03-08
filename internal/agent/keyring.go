// Package agent — keyring.go provides multi-key round-robin rotation with
// cooldown tracking for rate-limited API keys.
package agent

import (
	"sync"
	"time"
)

const (
	// keyCooldown is the default backoff period after a 429 or auth error.
	keyCooldown = 60 * time.Second
)

// KeyRing manages a pool of API keys for a single provider, providing
// round-robin selection with per-key cooldown after errors.
type KeyRing struct {
	mu       sync.Mutex
	keys     []string
	cooldown map[string]time.Time // key → earliest retry time
	next     int                  // next index for round-robin
}

// NewKeyRing constructs a KeyRing from the given key list.
// Duplicates and empty strings are removed.
func NewKeyRing(keys []string) *KeyRing {
	seen := map[string]bool{}
	deduped := make([]string, 0, len(keys))
	for _, k := range keys {
		if k != "" && !seen[k] {
			seen[k] = true
			deduped = append(deduped, k)
		}
	}
	return &KeyRing{
		keys:     deduped,
		cooldown: map[string]time.Time{},
	}
}

// Pick returns the next available key, skipping keys that are in cooldown.
// Returns ("", false) if all keys are in cooldown or the ring is empty.
func (r *KeyRing) Pick() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.keys) == 0 {
		return "", false
	}
	now := time.Now()
	// Try each key starting at r.next, wrapping around.
	for i := 0; i < len(r.keys); i++ {
		idx := (r.next + i) % len(r.keys)
		key := r.keys[idx]
		if until, ok := r.cooldown[key]; !ok || now.After(until) {
			r.next = (idx + 1) % len(r.keys) // advance for next call
			return key, true
		}
	}
	// All keys in cooldown — return the one with the shortest remaining wait.
	earliest := r.keys[0]
	for _, k := range r.keys {
		if r.cooldown[k].Before(r.cooldown[earliest]) {
			earliest = k
		}
	}
	return earliest, true // still return it; caller may choose to wait
}

// MarkFailed puts key into cooldown for the configured duration.
func (r *KeyRing) MarkFailed(key string) {
	if key == "" {
		return
	}
	r.mu.Lock()
	r.cooldown[key] = time.Now().Add(keyCooldown)
	r.mu.Unlock()
}

// Len returns the number of keys in the ring.
func (r *KeyRing) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.keys)
}

// ProviderKeyRingRegistry maps provider IDs to their KeyRings.
// It is safe for concurrent use.
type ProviderKeyRingRegistry struct {
	mu   sync.RWMutex
	rings map[string]*KeyRing
}

// NewProviderKeyRingRegistry creates an empty registry.
func NewProviderKeyRingRegistry() *ProviderKeyRingRegistry {
	return &ProviderKeyRingRegistry{rings: map[string]*KeyRing{}}
}

// Set registers a KeyRing for the given provider ID.
func (r *ProviderKeyRingRegistry) Set(providerID string, ring *KeyRing) {
	r.mu.Lock()
	r.rings[providerID] = ring
	r.mu.Unlock()
}

// Get returns the KeyRing for providerID, or nil if not registered.
func (r *ProviderKeyRingRegistry) Get(providerID string) *KeyRing {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rings[providerID]
}
