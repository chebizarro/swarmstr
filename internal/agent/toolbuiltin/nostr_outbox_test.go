package toolbuiltin

import (
	"context"
	"testing"
	"time"
)

func TestNostrRelayHintsTool_MissingPubkey(t *testing.T) {
	tool := NostrRelayHintsTool(NostrToolOpts{Relays: []string{"wss://example.com"}})
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error with missing pubkey")
	}
}

func TestNostrRelayHintsTool_NoRelays(t *testing.T) {
	tool := NostrRelayHintsTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"pubkey": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	})
	if err == nil {
		t.Fatal("expected error with no relays")
	}
}

func TestOutboxCacheRoundtrip(t *testing.T) {
	outboxCacheMu.Lock()
	outboxCache = map[string]outboxCacheEntry{} // reset
	outboxCacheMu.Unlock()

	pk := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	outboxCacheMu.Lock()
	outboxCache[pk] = outboxCacheEntry{
		read:      []string{"wss://r.example.com"},
		write:     []string{"wss://w.example.com"},
		fetchedAt: time.Now(),
	}
	outboxCacheMu.Unlock()

	outboxCacheMu.Lock()
	e, ok := outboxCache[pk]
	outboxCacheMu.Unlock()

	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(e.read) != 1 || e.read[0] != "wss://r.example.com" {
		t.Fatalf("unexpected read relays: %v", e.read)
	}
}
