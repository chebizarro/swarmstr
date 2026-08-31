// Package agent — keyring.go provides multi-key round-robin rotation with
// cooldown tracking for rate-limited API keys.
package agent

import (
	"crypto/sha256"
	"encoding/hex"
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
	now      func() time.Time
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
		now:      time.Now,
	}
}

// Pick returns the next available key, skipping keys that are in cooldown.
// Legacy callers receive the earliest cooling key when none are ready; production
// request paths must use Acquire, which never bypasses cooldown.
func (r *KeyRing) Pick() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.keys) == 0 {
		return "", false
	}
	now := r.now()
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

// MarkFailed is the legacy explicit cooldown hook. Production request paths call
// MarkRateLimited only after strict rate-limit/quota classification.
func (r *KeyRing) MarkFailed(key string) {
	if key == "" {
		return
	}
	r.mu.Lock()
	r.cooldown[key] = r.now().Add(keyCooldown)
	r.mu.Unlock()
}

// CredentialLease is a request-scoped key selection. Fingerprint is safe for
// attempted-key bookkeeping and diagnostics; Key must never be logged.
type CredentialLease struct {
	Key         string
	Fingerprint string
}

// Acquire selects an available key not present in excludedFingerprints. Unlike
// legacy Pick, it fails when every key is cooling down and never bypasses the
// cooldown. Production request paths should use Acquire.
func (r *KeyRing) Acquire(excludedFingerprints map[string]struct{}) (CredentialLease, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.keys) == 0 {
		return CredentialLease{}, false
	}
	now := r.now()
	for i := 0; i < len(r.keys); i++ {
		idx := (r.next + i) % len(r.keys)
		key := r.keys[idx]
		fingerprint := credentialFingerprint(key)
		if _, excluded := excludedFingerprints[fingerprint]; excluded {
			continue
		}
		if until := r.cooldown[key]; !until.IsZero() && now.Before(until) {
			continue
		}
		r.next = (idx + 1) % len(r.keys)
		return CredentialLease{Key: key, Fingerprint: fingerprint}, true
	}
	return CredentialLease{}, false
}

// MarkRateLimited cools only a classified rate-limit/quota credential.
func (r *KeyRing) MarkRateLimited(lease CredentialLease, retryAt time.Time) {
	if lease.Key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if retryAt.IsZero() || !retryAt.After(r.now()) {
		retryAt = r.now().Add(keyCooldown)
	}
	r.cooldown[lease.Key] = retryAt
}

func (r *KeyRing) EarliestRetry() (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var earliest time.Time
	for _, key := range r.keys {
		until := r.cooldown[key]
		if until.IsZero() || !until.After(r.now()) {
			return r.now(), true
		}
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
		}
	}
	return earliest, !earliest.IsZero()
}

func credentialFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
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
	mu    sync.RWMutex
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

// Replace swaps the registry contents with the provided provider→ring map.
func (r *ProviderKeyRingRegistry) Replace(rings map[string]*KeyRing) {
	r.mu.Lock()
	r.rings = make(map[string]*KeyRing, len(rings))
	for providerID, ring := range rings {
		r.rings[providerID] = ring
	}
	r.mu.Unlock()
}
