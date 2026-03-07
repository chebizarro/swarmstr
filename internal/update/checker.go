// Package update provides version checking against the GitHub releases API.
// Results are cached for 1 hour to avoid hammering the API.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultCheckURL is the GitHub releases API endpoint used when no override is configured.
	DefaultCheckURL = "https://api.github.com/repos/swarmstr-org/swarmstr/releases/latest"

	// CacheTTL is how long a successful version check result is cached.
	CacheTTL = time.Hour
)

// Result is the outcome of a version check.
type Result struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"` // true when latest > current
	CheckedAt int64  `json:"checked_at_ms"`
	Error     string `json:"error,omitempty"`
}

// Checker fetches the latest release version and compares it to the running version.
type Checker struct {
	mu         sync.Mutex
	current    string
	checkURL   string
	cached     *Result
	cachedAt   time.Time
	httpClient *http.Client
}

// NewChecker constructs a Checker for the given running version and optional check URL.
// Pass an empty checkURL to use DefaultCheckURL.
func NewChecker(currentVersion, checkURL string) *Checker {
	if checkURL == "" {
		checkURL = DefaultCheckURL
	}
	return &Checker{
		current:    currentVersion,
		checkURL:   checkURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Check returns the latest known version, using the cache if it is still fresh.
// force=true bypasses the cache.
func (c *Checker) Check(ctx context.Context, force bool) Result {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !force && c.cached != nil && time.Since(c.cachedAt) < CacheTTL {
		return *c.cached
	}

	latest, err := c.fetchLatest(ctx)
	now := time.Now().UnixMilli()
	if err != nil {
		r := Result{
			Current:   c.current,
			Latest:    "",
			Available: false,
			CheckedAt: now,
			Error:     err.Error(),
		}
		// Don't cache errors so transient network failures retry on next call.
		return r
	}

	available := isNewer(latest, c.current)
	r := Result{
		Current:   c.current,
		Latest:    latest,
		Available: available,
		CheckedAt: now,
	}
	c.cached = &r
	c.cachedAt = time.Now()
	return r
}

// fetchLatest retrieves the tag_name of the latest GitHub release.
func (c *Checker) fetchLatest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.checkURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "swarmstrd/"+c.current)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("releases API returned %s", resp.Status)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return "", fmt.Errorf("releases API returned empty tag_name")
	}
	return tag, nil
}

// isNewer reports whether candidate is a strictly newer semver than base.
// It strips a leading "v" prefix and compares [major, minor, patch] lexicographically
// as integers. Non-numeric components are compared as strings (pre-release suffixes
// make a version older than the same version without one, matching semver semantics).
func isNewer(candidate, base string) bool {
	cv := parseVer(candidate)
	bv := parseVer(base)
	for i := 0; i < 3; i++ {
		if cv[i].n > bv[i].n {
			return true
		}
		if cv[i].n < bv[i].n {
			return false
		}
	}
	return false
}

// verPart holds a single semver component as both int and string.
type verPart struct {
	n   int
	raw string
}

func parseVer(v string) [3]verPart {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Strip pre-release suffix (e.g. "1.2.3-beta" → "1.2.3").
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}
	parts := strings.SplitN(v, ".", 3)
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	var out [3]verPart
	for i := 0; i < 3; i++ {
		p := strings.TrimSpace(parts[i])
		n := 0
		for _, c := range p {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			} else {
				break
			}
		}
		out[i] = verPart{n: n, raw: p}
	}
	return out
}
