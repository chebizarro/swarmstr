package toolbuiltin

import (
	"context"
	"strings"
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

func TestNostrRelayListSetTool_RequiresKeyer(t *testing.T) {
	tool := NostrRelayListSetTool(NostrToolOpts{Relays: []string{"wss://example.com"}})
	_, err := tool(context.Background(), map[string]any{})
	if err == nil || !strings.HasPrefix(err.Error(), "nostr_relay_list_set_error:") {
		t.Fatalf("expected keyer error, got: %v", err)
	}
}

func TestNostrRelayListSetTool_NoRelays(t *testing.T) {
	tool := NostrRelayListSetTool(NostrToolOpts{Keyer: testSigner(t)})
	_, err := tool(context.Background(), map[string]any{})
	if err == nil || !strings.HasPrefix(err.Error(), "nostr_relay_list_set_error:") {
		t.Fatalf("expected no-relays error, got: %v", err)
	}
}

func TestUniqueNonEmpty_DedupTrimAndSort(t *testing.T) {
	got := uniqueNonEmpty([]string{" wss://b.example ", "", "wss://a.example", "wss://b.example", "wss://c.example "})
	want := []string{"wss://a.example", "wss://b.example", "wss://c.example"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d mismatch: got %q want %q", i, got[i], want[i])
		}
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
