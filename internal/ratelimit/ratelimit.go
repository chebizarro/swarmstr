// Package ratelimit provides per-user and per-channel rate limiting for
// metiq message processing.  It uses a token-bucket algorithm:
// each bucket refills at a configured rate (tokens per second) and has a
// maximum burst capacity.  A message is allowed when at least one token can
// be consumed from the bucket.
//
// Thread-safe for concurrent access.
package ratelimit

import (
	"sync"
	"time"
)

// Bucket is a single token-bucket rate limiter for one key.
type Bucket struct {
	mu       sync.Mutex
	tokens   float64
	max      float64   // burst capacity
	rate     float64   // tokens per second
	lastTick time.Time // time of last refill
}

// newBucket creates a Bucket with the given burst capacity and refill rate.
func newBucket(burst, rate float64) *Bucket {
	return &Bucket{
		tokens:   burst,
		max:      burst,
		rate:     rate,
		lastTick: time.Now(),
	}
}

// Allow returns true and consumes one token if a token is available.
// It refills the bucket based on elapsed time first.
func (b *Bucket) Allow() bool {
	return b.AllowN(1)
}

// AllowN returns true if n tokens are available and consumes them.
func (b *Bucket) AllowN(n float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastTick).Seconds()
	b.lastTick = now

	b.tokens += elapsed * b.rate
	if b.tokens > b.max {
		b.tokens = b.max
	}
	if b.tokens < n {
		return false
	}
	b.tokens -= n
	return true
}

// Tokens returns the current token count after applying elapsed refill time.
func (b *Bucket) Tokens() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Apply elapsed refill without consuming.
	now := time.Now()
	elapsed := now.Sub(b.lastTick).Seconds()
	tokens := b.tokens + elapsed*b.rate
	if tokens > b.max {
		tokens = b.max
	}
	return tokens
}

// ─── Limiter ─────────────────────────────────────────────────────────────────

// Config holds rate limiter settings.
type Config struct {
	// Burst is the maximum burst (token capacity).  Default 5.
	Burst float64
	// Rate is the refill speed in tokens per second.  Default 1.
	Rate float64
	// Enabled controls whether the limiter is active.  When false, Allow
	// always returns true.
	Enabled bool
}

// DefaultConfig returns a sensible default: burst=5, rate=1 msg/s.
func DefaultConfig() Config {
	return Config{Burst: 5, Rate: 1, Enabled: true}
}

// Limiter manages per-key token buckets.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*Bucket
	cfg     Config
}

// NewLimiter creates a Limiter with the given configuration.
func NewLimiter(cfg Config) *Limiter {
	if cfg.Burst <= 0 {
		cfg.Burst = 5
	}
	if cfg.Rate <= 0 {
		cfg.Rate = 1
	}
	return &Limiter{
		buckets: map[string]*Bucket{},
		cfg:     cfg,
	}
}

// Allow returns true if the key is allowed to proceed.
// When the limiter is disabled it always returns true.
func (l *Limiter) Allow(key string) bool {
	if !l.cfg.Enabled {
		return true
	}
	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		b = newBucket(l.cfg.Burst, l.cfg.Rate)
		l.buckets[key] = b
	}
	l.mu.Unlock()
	return b.Allow()
}

// Reset removes the bucket for key, effectively resetting its token count.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

// SetConfig replaces the configuration.  Existing buckets are unaffected;
// new buckets created after this call use the new config.
func (l *Limiter) SetConfig(cfg Config) {
	if cfg.Burst <= 0 {
		cfg.Burst = 5
	}
	if cfg.Rate <= 0 {
		cfg.Rate = 1
	}
	l.mu.Lock()
	l.cfg = cfg
	l.mu.Unlock()
}

// Prune removes buckets whose token count has fully refilled (idle keys).
// Call periodically to reclaim memory.
func (l *Limiter) Prune() {
	l.mu.Lock()
	for key, b := range l.buckets {
		if b.Tokens() >= l.cfg.Burst {
			delete(l.buckets, key)
		}
	}
	l.mu.Unlock()
}

// Size returns the number of active buckets.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// ─── Multi-limiter (per-user + per-channel) ───────────────────────────────────

// MultiLimiter composes a per-user and a per-channel Limiter.
// A request is allowed only when both limiters permit it.
type MultiLimiter struct {
	User    *Limiter
	Channel *Limiter
}

// NewMultiLimiter creates a MultiLimiter with the provided configurations.
func NewMultiLimiter(userCfg, channelCfg Config) *MultiLimiter {
	return &MultiLimiter{
		User:    NewLimiter(userCfg),
		Channel: NewLimiter(channelCfg),
	}
}

// Allow returns true only when both the user bucket and channel bucket allow the request.
func (m *MultiLimiter) Allow(userKey, channelKey string) bool {
	return m.User.Allow(userKey) && m.Channel.Allow(channelKey)
}

// Prune prunes both limiters.
func (m *MultiLimiter) Prune() {
	m.User.Prune()
	m.Channel.Prune()
}
