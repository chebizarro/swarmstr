package l402

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/paymentstate"
)

// TokenCache is the exact-resource credential cache used by Client.
type TokenCache interface {
	Get(context.Context, string, string) (paymentstate.L402TokenRecord, bool, error)
	Put(context.Context, string, string, Token) (paymentstate.L402TokenRecord, error)
	Delete(context.Context, string, string) error
}

// Token contains the payment proof and metadata needed to create a cache record.
type Token struct {
	Challenge      Challenge
	PreimageHex    string
	PaymentHashHex string
	PayerID        string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// CacheOptions controls TTL, size, and deterministic time in tests.
type CacheOptions struct {
	TTL        time.Duration
	MaxEntries int
	Now        func() time.Time
}

// Cache adapts paymentstate memory or protected-secret repositories into an
// in-process TTL/LRU cache. Memory is updated before persistence so a backend
// failure after payment cannot cause repayment during the same process.
type Cache struct {
	mu         sync.Mutex
	repository paymentstate.L402TokenRepository
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
	records    map[string]paymentstate.L402TokenRecord
}

// NewCache eagerly loads persisted records so protected-store failures disable
// L402 initialization rather than silently degrading persistence.
func NewCache(ctx context.Context, repository paymentstate.L402TokenRepository, opts CacheOptions) (*Cache, error) {
	if repository == nil {
		return nil, fmt.Errorf("L402 token repository is required")
	}
	if opts.TTL <= 0 {
		opts.TTL = 24 * time.Hour
	}
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = 128
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	loaded, err := repository.Load(ctx)
	if err != nil {
		return nil, err
	}
	cache := &Cache{
		repository: repository,
		ttl:        opts.TTL,
		maxEntries: opts.MaxEntries,
		now:        opts.Now,
		records:    make(map[string]paymentstate.L402TokenRecord, len(loaded)),
	}
	now := cache.now()
	for _, record := range loaded {
		if err := record.Validate(); err != nil {
			return nil, fmt.Errorf("invalid persisted L402 token: %w", err)
		}
		if !record.ExpiresAt.After(now) {
			if err := repository.Delete(ctx, record.ResourceKey); err != nil {
				return nil, err
			}
			continue
		}
		cache.records[record.ResourceKey] = record
	}
	if err := cache.evictLocked(ctx); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *Cache) Get(ctx context.Context, method, rawURL string) (paymentstate.L402TokenRecord, bool, error) {
	key, _, err := ResourceKey(method, rawURL)
	if err != nil {
		return paymentstate.L402TokenRecord{}, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.records[key]
	if !ok {
		return paymentstate.L402TokenRecord{}, false, nil
	}
	now := c.now()
	if !record.ExpiresAt.After(now) {
		delete(c.records, key)
		return paymentstate.L402TokenRecord{}, false, c.repository.Delete(ctx, key)
	}
	record.LastUsedAt = now
	c.records[key] = record
	if err := c.repository.Put(ctx, record); err != nil {
		return record, true, err
	}
	return record, true, nil
}

func (c *Cache) Put(ctx context.Context, method, rawURL string, token Token) (paymentstate.L402TokenRecord, error) {
	if !isL402Scheme(token.Challenge.Scheme) || strings.TrimSpace(token.Challenge.Macaroon) == "" || strings.TrimSpace(token.Challenge.Invoice) == "" {
		return paymentstate.L402TokenRecord{}, fmt.Errorf("%w: unusable cache token challenge", ErrInvalidChallenge)
	}
	key, canonicalURL, err := ResourceKey(method, rawURL)
	if err != nil {
		return paymentstate.L402TokenRecord{}, err
	}
	origin, err := CanonicalOrigin(canonicalURL)
	if err != nil {
		return paymentstate.L402TokenRecord{}, err
	}
	now := c.now()
	created := token.CreatedAt
	if created.IsZero() {
		created = now
	}
	expires := token.ExpiresAt
	if expires.IsZero() || expires.After(created.Add(c.ttl)) {
		expires = created.Add(c.ttl)
	}
	record := paymentstate.L402TokenRecord{
		ResourceKey:    key,
		Origin:         origin,
		Scheme:         canonicalScheme(token.Challenge.Scheme),
		Macaroon:       token.Challenge.Macaroon,
		MacaroonSHA256: token.Challenge.MacaroonFingerprint(),
		PreimageHex:    strings.ToLower(token.PreimageHex),
		PaymentHashHex: strings.ToLower(token.PaymentHashHex),
		PayerID:        strings.TrimSpace(token.PayerID),
		CreatedAt:      created,
		ExpiresAt:      expires,
		LastUsedAt:     now,
	}
	if err := record.Validate(); err != nil {
		return paymentstate.L402TokenRecord{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.records[key] = record
	var persistenceErr error
	if err := c.repository.Put(ctx, record); err != nil {
		persistenceErr = err
	}
	if err := c.evictLocked(ctx); err != nil && persistenceErr == nil {
		persistenceErr = err
	}
	return record, persistenceErr
}

// ChallengeOrigin reports whether this opaque macaroon is already bound to an
// origin. It prevents a copied challenge from causing credential reuse across
// origins, including after loading a persistent cache.
func (c *Cache) ChallengeOrigin(challenge Challenge) (string, bool) {
	fingerprint := challenge.MacaroonFingerprint()
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for _, record := range c.records {
		if record.ExpiresAt.After(now) && record.Scheme == canonicalScheme(challenge.Scheme) && record.MacaroonSHA256 == fingerprint {
			return record.Origin, true
		}
	}
	return "", false
}

func (c *Cache) Delete(ctx context.Context, method, rawURL string) error {
	key, _, err := ResourceKey(method, rawURL)
	if err != nil {
		return err
	}
	return c.DeleteKey(ctx, key)
}

func (c *Cache) DeleteKey(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.records, key)
	return c.repository.Delete(ctx, key)
}

func (c *Cache) evictLocked(ctx context.Context) error {
	if len(c.records) <= c.maxEntries {
		return nil
	}
	records := make([]paymentstate.L402TokenRecord, 0, len(c.records))
	for _, record := range c.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].LastUsedAt.Equal(records[j].LastUsedAt) {
			return records[i].ResourceKey < records[j].ResourceKey
		}
		return records[i].LastUsedAt.Before(records[j].LastUsedAt)
	})
	var firstErr error
	for len(c.records) > c.maxEntries {
		record := records[0]
		records = records[1:]
		delete(c.records, record.ResourceKey)
		if err := c.repository.Delete(ctx, record.ResourceKey); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ResourceKey returns the opaque SHA-256 key and canonical URL used for exact
// resource scoping. The URL (including its query) is never persisted.
func ResourceKey(method, rawURL string) (string, string, error) {
	canonical, err := canonicalResourceURL(rawURL)
	if err != nil {
		return "", "", err
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = "GET"
	}
	sum := sha256.Sum256([]byte(method + "\n" + canonical))
	return hex.EncodeToString(sum[:]), canonical, nil
}

func canonicalAllowedOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawFragment != "" {
		return "", fmt.Errorf("allowed L402 origin must not contain a path, query, or fragment")
	}
	return CanonicalOrigin(raw)
}

// CanonicalOrigin returns the canonical origin for an HTTPS resource URL.
func CanonicalOrigin(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil {
		return "", fmt.Errorf("L402 URL must use an absolute HTTPS origin")
	}
	host := canonicalHost(parsed)
	return "https://" + host, nil
}

func canonicalResourceURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" {
		return "", fmt.Errorf("invalid absolute URL")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", fmt.Errorf("L402 URL must use HTTPS")
	}
	parsed.Scheme = "https"
	parsed.Host = canonicalHost(parsed)
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), nil
}

func canonicalHost(parsed *url.URL) string {
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port == "" || port == "443" {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, port)
}
