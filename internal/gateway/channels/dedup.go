// Subscription deduplication and jitter helpers.
//
// When a Nostr subscription connects (or the underlying pool reconnects), a
// small amount of "jitter" is subtracted from the Since timestamp so that
// events arriving during the connection gap are not lost.  Because this overlap
// window can re-deliver events the subscriber already processed, we keep a
// lightweight seen-event cache keyed on the event's unique ID (which is a hash
// of the event content, making deduplication trivial).
package channels

import (
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
)

// DefaultSinceJitter is the default duration subtracted from the Since
// timestamp when creating or reconnecting a subscription.  30 seconds is long
// enough to cover brief connectivity hiccups without pulling excessive
// historical data.
const DefaultSinceJitter = 30 * time.Second

// seenCacheDefaultTTL is how long an event ID stays in the cache.  Events
// older than this are assumed to have been fully processed and can be safely
// evicted.
const seenCacheDefaultTTL = 5 * time.Minute

// seenCacheMaxSize is the hard cap on cache entries to bound memory.
const seenCacheMaxSize = 10_000

// applyJitter backdates a nostr.Timestamp by the given duration.  If the
// resulting timestamp would be negative, zero is returned.
func applyJitter(since nostr.Timestamp, jitter time.Duration) nostr.Timestamp {
	backdated := since - nostr.Timestamp(jitter.Seconds())
	if backdated < 0 {
		return 0
	}
	return backdated
}

// SeenCache is a lightweight, concurrent-safe cache of event IDs used to
// deduplicate events received across overlapping subscription windows.
type SeenCache struct {
	mu    sync.Mutex
	items map[string]time.Time // event ID hex → time first seen
	ttl   time.Duration
}

// NewSeenCache creates a SeenCache with the default TTL.
func NewSeenCache() *SeenCache {
	return &SeenCache{
		items: make(map[string]time.Time, 256),
		ttl:   seenCacheDefaultTTL,
	}
}

// Add records an event ID.  Returns true if the event was already present
// (i.e. is a duplicate), false if it was newly added.
func (c *SeenCache) Add(eventID string) (duplicate bool) {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if seenAt, ok := c.items[eventID]; ok {
		if now.Sub(seenAt) <= c.ttl {
			return true
		}
		delete(c.items, eventID)
	}

	cutoff := now.Add(-c.ttl)
	for k, t := range c.items {
		if t.Before(cutoff) {
			delete(c.items, k)
		}
	}

	for len(c.items) >= seenCacheMaxSize {
		var oldestKey string
		var oldestAt time.Time
		for k, t := range c.items {
			if oldestKey == "" || t.Before(oldestAt) {
				oldestKey = k
				oldestAt = t
			}
		}
		if oldestKey == "" {
			break
		}
		delete(c.items, oldestKey)
	}

	c.items[eventID] = now
	return false
}

// Len returns the current number of cached entries.
func (c *SeenCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}
