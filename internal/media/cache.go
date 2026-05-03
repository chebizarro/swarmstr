package media

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	output  MediaOutput
	expires time.Time
	touched time.Time
}

type AttachmentCache struct {
	ttl        time.Duration
	maxEntries int
	mu         sync.RWMutex
	entries    map[string]cacheEntry
}

func NewAttachmentCache(ttl time.Duration, maxEntries int) *AttachmentCache {
	if ttl <= 0 {
		ttl = time.Hour
	}
	if maxEntries <= 0 {
		maxEntries = 256
	}
	return &AttachmentCache{ttl: ttl, maxEntries: maxEntries, entries: map[string]cacheEntry{}}
}
func (c *AttachmentCache) Get(key string) (MediaOutput, bool) {
	if c == nil || key == "" {
		return MediaOutput{}, false
	}
	now := time.Now()
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return MediaOutput{}, false
	}
	if !e.expires.IsZero() && now.After(e.expires) {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return MediaOutput{}, false
	}
	c.mu.Lock()
	if cur, ok := c.entries[key]; ok {
		cur.touched = now
		c.entries[key] = cur
	}
	c.mu.Unlock()
	return e.output, true
}
func (c *AttachmentCache) Set(key string, out MediaOutput) {
	if c == nil || key == "" {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cacheEntry{}
	}
	c.entries[key] = cacheEntry{output: out, expires: now.Add(c.ttl), touched: now}
	if len(c.entries) > c.maxEntries {
		c.evictLocked(len(c.entries) - c.maxEntries)
	}
}
func (c *AttachmentCache) evictLocked(n int) {
	for ; n > 0; n-- {
		var oldest string
		var t time.Time
		for k, e := range c.entries {
			if oldest == "" || e.touched.Before(t) {
				oldest = k
				t = e.touched
			}
		}
		if oldest == "" {
			return
		}
		delete(c.entries, oldest)
	}
}
func BuildCacheKey(att MediaAttachment, prompt, mode string) string {
	h := sha256.New()
	h.Write([]byte(att.IdentityKey()))
	h.Write([]byte("\x00"))
	h.Write([]byte(strings.ToLower(strings.TrimSpace(mode))))
	h.Write([]byte("\x00"))
	h.Write([]byte(strings.TrimSpace(prompt)))
	return hex.EncodeToString(h.Sum(nil))
}
