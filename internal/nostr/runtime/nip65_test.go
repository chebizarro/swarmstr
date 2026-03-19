package runtime

import (
	"time"
	"testing"

	nostr "fiatjaf.com/nostr"
)

func TestDecodeNIP65Event(t *testing.T) {
	ev := nostr.Event{
		Kind:      10002,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"r", "wss://relay1.example.com"},
			{"r", "wss://relay2.example.com", "read"},
			{"r", "wss://relay3.example.com", "write"},
		},
	}

	list := DecodeNIP65Event(ev)

	if len(list.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(list.Entries))
	}

	// Entry 0: no marker = both
	if !list.Entries[0].Read || !list.Entries[0].Write {
		t.Error("entry 0 should be both read+write")
	}
	// Entry 1: read only
	if !list.Entries[1].Read || list.Entries[1].Write {
		t.Error("entry 1 should be read only")
	}
	// Entry 2: write only
	if list.Entries[2].Read || !list.Entries[2].Write {
		t.Error("entry 2 should be write only")
	}

	readRelays := list.ReadRelays()
	if len(readRelays) != 2 {
		t.Fatalf("expected 2 read relays, got %d: %v", len(readRelays), readRelays)
	}
	writeRelays := list.WriteRelays()
	if len(writeRelays) != 2 {
		t.Fatalf("expected 2 write relays, got %d: %v", len(writeRelays), writeRelays)
	}
}

func TestRelaySelectorFallback(t *testing.T) {
	sel := NewRelaySelector(
		[]string{"wss://read-fallback.example.com"},
		[]string{"wss://write-fallback.example.com"},
	)

	// No cached list, should return fallbacks
	got := sel.Get("abc123")
	if got != nil {
		t.Error("expected nil for uncached pubkey")
	}

	fb := sel.FallbackRead()
	if len(fb) != 1 || fb[0] != "wss://read-fallback.example.com" {
		t.Errorf("unexpected fallback read: %v", fb)
	}
	fb = sel.FallbackWrite()
	if len(fb) != 1 || fb[0] != "wss://write-fallback.example.com" {
		t.Errorf("unexpected fallback write: %v", fb)
	}
}

func TestRelaySelectorPutGet(t *testing.T) {
	sel := NewRelaySelector(nil, nil)

	list := &NIP65RelayList{
		PubKey: "abc123",
		Entries: []NIP65RelayEntry{
			{URL: "wss://r1.example.com", Read: true, Write: false},
			{URL: "wss://r2.example.com", Read: false, Write: true},
			{URL: "wss://r3.example.com", Read: true, Write: true},
		},
	}
	sel.Put(list)

	got := sel.Get("abc123")
	if got == nil {
		t.Fatal("expected cached list")
	}
	if len(got.ReadRelays()) != 2 {
		t.Errorf("expected 2 read relays, got %d", len(got.ReadRelays()))
	}
	if len(got.WriteRelays()) != 2 {
		t.Errorf("expected 2 write relays, got %d", len(got.WriteRelays()))
	}
}

func TestRelaySelectorInvalidate(t *testing.T) {
	sel := NewRelaySelector(nil, nil)
	sel.Put(&NIP65RelayList{PubKey: "abc123"})
	sel.Invalidate("abc123")
	if sel.Get("abc123") != nil {
		t.Error("expected nil after invalidate")
	}
}

func TestRelaySelectorSetFallbacks(t *testing.T) {
	sel := NewRelaySelector([]string{"old"}, []string{"old"})
	sel.SetFallbacks([]string{"new-read"}, []string{"new-write"})
	if fb := sel.FallbackRead(); len(fb) != 1 || fb[0] != "new-read" {
		t.Errorf("unexpected: %v", fb)
	}
	if fb := sel.FallbackWrite(); len(fb) != 1 || fb[0] != "new-write" {
		t.Errorf("unexpected: %v", fb)
	}
}

func TestDedupeRelays(t *testing.T) {
	got := dedupeRelays([]string{"wss://a.com", "WSS://A.COM", "wss://b.com", " ", "wss://b.com"})
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d: %v", len(got), got)
	}
}

func TestMergeRelayListsNormalizesCaseAndSorts(t *testing.T) {
	got := MergeRelayLists(
		[]string{"wss://b.com", "WSS://A.COM"},
		[]string{"wss://a.com", "wss://c.com"},
	)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d: %v", len(got), got)
	}
	if got[0] != "WSS://A.COM" || got[1] != "wss://b.com" || got[2] != "wss://c.com" {
		t.Fatalf("unexpected order/values: %v", got)
	}
}

func TestRelaySelectorEvictsExpiredEntries(t *testing.T) {
	sel := NewRelaySelector(nil, nil)
	sel.cacheTTL = 1 * time.Millisecond
	sel.Put(&NIP65RelayList{PubKey: "abc123"})
	time.Sleep(5 * time.Millisecond)
	if sel.Get("abc123") != nil {
		t.Fatal("expected nil after TTL expiry")
	}
	sel.mu.RLock()
	_, ok := sel.cache["abc123"]
	sel.mu.RUnlock()
	if ok {
		t.Fatal("expected expired entry to be evicted")
	}
}
