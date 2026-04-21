package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// ─── Gemini CachedContent ────────────────────────────────────────────────────
//
// The Gemini API supports explicit prompt caching via CachedContent resources.
// We cache the system instruction (and optionally tool definitions) across turns
// so repeated calls reuse the same cached content, saving input token costs.
//
// Docs: https://ai.google.dev/gemini-api/docs/caching

// geminiCachedContentEntry tracks a single active CachedContent resource.
type geminiCachedContentEntry struct {
	Name      string    // e.g. "cachedContents/abc123"
	ExpiresAt time.Time // when the server-side resource expires
}

// geminiContextCache holds an LRU of cached content resources keyed by
// a hash of (model + system instruction + tools).
type geminiContextCache struct {
	mu      sync.RWMutex
	entries map[string]geminiCachedContentEntry
}

var globalGeminiContextCache = &geminiContextCache{
	entries: make(map[string]geminiCachedContentEntry),
}

const (
	// geminiCacheTTL is the default time-to-live for Gemini cached content.
	// This should be long enough to survive multi-turn conversations but short
	// enough to not waste storage on stale caches.
	geminiCacheTTL = 1 * time.Hour

	// geminiCacheMinSystemTokens is a rough minimum system prompt size (in chars)
	// before we attempt caching. Gemini requires sufficient content to make
	// caching worthwhile. Below this threshold, we skip caching entirely.
	geminiCacheMinSystemChars = 2000
)

// geminiCacheKey computes a deterministic key from the system instruction and tools.
func geminiCacheKey(model, systemText string, tools []ToolDefinition) string {
	h := sha256.New()
	h.Write([]byte(model))
	h.Write([]byte{0})
	h.Write([]byte(systemText))
	h.Write([]byte{0})
	for _, t := range tools {
		h.Write([]byte(t.Name))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}

// lookup returns a cached content name if one exists and hasn't expired.
func (c *geminiContextCache) lookup(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.ExpiresAt) {
		return "", false
	}
	return entry.Name, true
}

// store saves a cached content resource reference.
func (c *geminiContextCache) store(key string, name string, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Evict expired entries opportunistically (cap at 32 entries).
	now := time.Now()
	if len(c.entries) > 32 {
		for k, e := range c.entries {
			if now.After(e.ExpiresAt) {
				delete(c.entries, k)
			}
		}
	}
	c.entries[key] = geminiCachedContentEntry{Name: name, ExpiresAt: expiresAt}
}

// clear removes all entries (e.g. on config change).
func (c *geminiContextCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]geminiCachedContentEntry)
}

// ── Gemini CachedContent API types ──────────────────────────────────────────

type geminiCachedContentRequest struct {
	Model             string              `json:"model"`
	SystemInstruction *geminiContent      `json:"systemInstruction,omitempty"`
	Tools             []geminiToolBundle  `json:"tools,omitempty"`
	TTL               string              `json:"ttl"` // e.g. "3600s"
	DisplayName       string              `json:"displayName,omitempty"`
}

type geminiCachedContentResponse struct {
	Name       string `json:"name"`       // e.g. "cachedContents/abc123"
	ExpireTime string `json:"expireTime"` // RFC3339
	Error      *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// createGeminiCachedContent calls the CachedContent API to create a new cache.
func createGeminiCachedContent(ctx context.Context, apiKey, model string, systemInstruction *geminiContent, tools []geminiToolBundle, httpClient *http.Client, ttl time.Duration) (name string, expiresAt time.Time, err error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	// Gemini expects the model in "models/..." format.
	modelRef := model
	if !contains(modelRef, "/") {
		modelRef = "models/" + modelRef
	}

	req := geminiCachedContentRequest{
		Model:             modelRef,
		SystemInstruction: systemInstruction,
		Tools:             tools,
		TTL:               fmt.Sprintf("%ds", int(ttl.Seconds())),
		DisplayName:       "swarmstr-system-cache",
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("gemini cache: marshal: %w", err)
	}

	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/cachedContents?key=%s", apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("gemini cache: request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("gemini cache: http: %w", err)
	}
	defer resp.Body.Close()

	var result geminiCachedContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", time.Time{}, fmt.Errorf("gemini cache: decode: %w", err)
	}
	if result.Error != nil {
		return "", time.Time{}, fmt.Errorf("gemini cache API error %d: %s", result.Error.Code, result.Error.Message)
	}
	if result.Name == "" {
		return "", time.Time{}, fmt.Errorf("gemini cache: empty name in response")
	}

	expiry := time.Now().Add(ttl)
	if result.ExpireTime != "" {
		if t, err := time.Parse(time.RFC3339, result.ExpireTime); err == nil {
			expiry = t
		}
	}

	return result.Name, expiry, nil
}

// contains checks if a string contains a substring (avoids importing strings again).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (substr == "" || findSubstring(s, substr))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// resolveGeminiCache attempts to find or create a CachedContent resource for
// the given system instruction and tools. Returns the cached content name
// (empty string if caching is skipped or fails).
func resolveGeminiCache(ctx context.Context, apiKey, model string, systemInstruction *geminiContent, tools []geminiToolBundle, httpClient *http.Client) string {
	// Skip caching if system instruction is too small.
	if systemInstruction == nil || len(systemInstruction.Parts) == 0 {
		return ""
	}
	totalChars := 0
	for _, p := range systemInstruction.Parts {
		totalChars += len(p.Text)
	}
	if totalChars < geminiCacheMinSystemChars {
		return ""
	}

	// Compute cache key from system text + model.
	var systemText string
	for _, p := range systemInstruction.Parts {
		systemText += p.Text
	}
	// We don't include tools in the cache key for simplicity; tools tend to be
	// stable and system instruction is the dominant cacheable content.
	key := geminiCacheKey(model, systemText, nil)

	// Check local cache.
	if name, ok := globalGeminiContextCache.lookup(key); ok {
		return name
	}

	// Create a new CachedContent resource.
	name, expiresAt, err := createGeminiCachedContent(ctx, apiKey, model, systemInstruction, tools, httpClient, geminiCacheTTL)
	if err != nil {
		log.Printf("gemini: cache creation failed (proceeding without cache): %v", err)
		return ""
	}

	globalGeminiContextCache.store(key, name, expiresAt)
	log.Printf("gemini: created cached content %s (expires %s)", name, expiresAt.Format(time.RFC3339))
	return name
}
