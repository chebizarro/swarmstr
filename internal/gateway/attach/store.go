// Package attach implements the process-local grant store behind the
// attach.grant / attach.revoke gateway methods (OpenClaw parity, WS-A/A7).
// A grant is an opaque bearer token bound to one session key with an absolute
// expiry; external tool clients present it to the MCP loopback surface as
// `Authorization: Bearer <token>` so their tool calls are scoped to that
// session. Grants never persist: a gateway restart invalidates them all.
package attach

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTTL applies when the caller does not request a lifetime.
	DefaultTTL = time.Hour
	// MaxTTL caps requested lifetimes (mirrors the OpenClaw grant store).
	MaxTTL = 12 * time.Hour
)

// Grant is one minted attach grant.
type Grant struct {
	// Token is the opaque bearer presented as `Authorization: Bearer <token>`.
	Token string
	// SessionKey is the session this grant is bound to.
	SessionKey string
	// IssuedAt is the absolute mint time.
	IssuedAt time.Time
	// ExpiresAt is the absolute expiry.
	ExpiresAt time.Time
}

// Options configures a Store.
type Options struct {
	// Now overrides the clock (tests).
	Now func() time.Time
	// NewToken overrides token generation (tests). Nil uses 32 random bytes
	// hex-encoded.
	NewToken func() (string, error)
}

// Store owns every live attach grant for one gateway process.
type Store struct {
	mu     sync.Mutex
	opts   Options
	grants map[string]Grant
}

// NewStore returns an empty grant store.
func NewStore(opts ...Options) *Store {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NewToken == nil {
		o.NewToken = randomToken
	}
	return &Store{opts: o, grants: map[string]Grant{}}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// clampTTL applies the default for non-positive requests and the cap for
// oversized ones (non-positive mirrors OpenClaw, which silently defaults).
func clampTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return DefaultTTL
	}
	if ttl > MaxTTL {
		return MaxTTL
	}
	return ttl
}

// Mint issues one grant bound to sessionKey. Mint sweeps stale entries so
// abandoned grants do not accumulate.
func (s *Store) Mint(sessionKey string, ttl time.Duration) (Grant, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return Grant{}, errors.New("attach grant requires a session key")
	}
	token, err := s.opts.NewToken()
	if err != nil || strings.TrimSpace(token) == "" {
		return Grant{}, errors.New("attach grant token generation failed")
	}
	now := s.opts.Now()
	grant := Grant{
		Token:      token,
		SessionKey: sessionKey,
		IssuedAt:   now,
		ExpiresAt:  now.Add(clampTTL(ttl)),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	if _, exists := s.grants[token]; exists {
		return Grant{}, errors.New("attach grant token collision")
	}
	s.grants[token] = grant
	return grant, nil
}

// Resolve returns the live grant for token; expired grants are dropped.
// The presented token is compared against every stored grant with a
// constant-time comparison so lookup timing cannot leak token contents
// (the /mcp loopback surface authenticates on this path).
func (s *Store) Resolve(token string) (Grant, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Grant{}, false
	}
	now := s.opts.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var match Grant
	found := false
	for stored, grant := range s.grants {
		if subtle.ConstantTimeCompare([]byte(stored), []byte(token)) == 1 {
			match = grant
			found = true
		}
	}
	if !found {
		return Grant{}, false
	}
	if !match.ExpiresAt.After(now) {
		delete(s.grants, match.Token)
		return Grant{}, false
	}
	return match, true
}

// Revoke invalidates one token and reports whether a live grant was removed.
func (s *Store) Revoke(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	now := s.opts.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.grants[token]
	if !ok {
		return false
	}
	delete(s.grants, token)
	return grant.ExpiresAt.After(now)
}

// Count reports the number of stored (possibly stale) grants.
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.grants)
}

func (s *Store) sweepLocked(now time.Time) {
	for token, grant := range s.grants {
		if !grant.ExpiresAt.After(now) {
			delete(s.grants, token)
		}
	}
}
