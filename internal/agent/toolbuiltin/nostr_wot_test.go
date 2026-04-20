package toolbuiltin

import (
	"context"
	"testing"
	"time"
)

// ─── Cache helper tests ──────────────────────────────────────────────────────

func TestCachedFollows_Miss(t *testing.T) {
	// Clear cache state.
	followsCacheMu.Lock()
	delete(followsCache, "test_miss_key")
	followsCacheMu.Unlock()

	_, ok := cachedFollows("test_miss_key")
	if ok {
		t.Error("expected cache miss for unknown key")
	}
}

func TestCachedFollows_Hit(t *testing.T) {
	storeFollows("test_wot_hit", []string{"pub1", "pub2"})
	got, ok := cachedFollows("test_wot_hit")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 2 {
		t.Errorf("expected 2 follows, got %d", len(got))
	}
}

func TestCachedFollows_Expired(t *testing.T) {
	// Store with a past fetchedAt to simulate expiry.
	followsCacheMu.Lock()
	followsCache["test_expired"] = followsCacheEntry{
		follows:   []string{"old"},
		fetchedAt: time.Now().Add(-followsCacheTTL - time.Minute),
	}
	followsCacheMu.Unlock()

	_, ok := cachedFollows("test_expired")
	if ok {
		t.Error("expected cache miss for expired entry")
	}
}

func TestWotDistanceCacheKey(t *testing.T) {
	key := wotDistanceCacheKey("from123", "to456")
	if key != "from123→to456" {
		t.Errorf("key = %q", key)
	}
}

func TestCachedWotDistance_Miss(t *testing.T) {
	_, _, ok := cachedWotDistance("no_from", "no_to")
	if ok {
		t.Error("expected cache miss")
	}
}

func TestCachedWotDistance_Hit(t *testing.T) {
	storeWotDistance("wot_from", "wot_to", 2, []string{"wot_from", "mid", "wot_to"})
	dist, path, ok := cachedWotDistance("wot_from", "wot_to")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if dist != 2 {
		t.Errorf("distance = %d, want 2", dist)
	}
	if len(path) != 3 {
		t.Errorf("path len = %d, want 3", len(path))
	}
}

func TestCachedWotDistance_Expired(t *testing.T) {
	wotDistanceCacheMu.Lock()
	wotDistanceCache[wotDistanceCacheKey("exp_from", "exp_to")] = wotDistanceCacheEntry{
		distance:  1,
		path:      []string{"a", "b"},
		fetchedAt: time.Now().Add(-wotDistanceCacheTTL - time.Minute),
	}
	wotDistanceCacheMu.Unlock()

	_, _, ok := cachedWotDistance("exp_from", "exp_to")
	if ok {
		t.Error("expected cache miss for expired distance entry")
	}
}

// ─── Tool error paths ────────────────────────────────────────────────────────

func TestNostrFollowsTool_NoRelays(t *testing.T) {
	tool := NostrFollowsTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"pubkey": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	})
	if err == nil {
		t.Fatal("expected error with no relays")
	}
}

func TestNostrFollowsTool_NoPubkey(t *testing.T) {
	tool := NostrFollowsTool(NostrToolOpts{Relays: []string{"wss://r.test"}})
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error with missing pubkey")
	}
}

func TestNostrFollowersTool_NoRelays(t *testing.T) {
	tool := NostrFollowersTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"pubkey": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	})
	if err == nil {
		t.Fatal("expected error with no relays")
	}
}

func TestNostrWotDistanceTool_NoRelays(t *testing.T) {
	tool := NostrWotDistanceTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"from_pubkey": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"to_pubkey":   "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3",
	})
	if err == nil {
		t.Fatal("expected error with no relays")
	}
}
