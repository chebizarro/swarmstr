package runtime

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/testutil"
)

// ─── PublishNIP65 + FetchNIP65 round-trip ────────────────────────────────────

func TestPublishAndFetchNIP65_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	eventID, err := PublishNIP65(ctx, pool, kp.Keyer, []string{url},
		[]string{"wss://read.example"},
		[]string{"wss://write.example"},
		[]string{"wss://both.example"},
	)
	if err != nil {
		t.Fatalf("PublishNIP65: %v", err)
	}
	if eventID == "" {
		t.Fatal("expected non-empty event ID")
	}

	list, err := FetchNIP65(ctx, pool, []string{url}, kp.PubKeyHex())
	if err != nil {
		t.Fatalf("FetchNIP65: %v", err)
	}

	if len(list.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d: %v", len(list.Entries), list.Entries)
	}

	// Check we got the right relays
	readCount, writeCount, bothCount := 0, 0, 0
	for _, e := range list.Entries {
		if e.Read && e.Write {
			bothCount++
		} else if e.Read {
			readCount++
		} else if e.Write {
			writeCount++
		}
	}
	if bothCount != 1 {
		t.Errorf("expected 1 both, got %d", bothCount)
	}
	if readCount != 1 {
		t.Errorf("expected 1 read-only, got %d", readCount)
	}
	if writeCount != 1 {
		t.Errorf("expected 1 write-only, got %d", writeCount)
	}
}

func TestFetchNIP65_NotFound(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := FetchNIP65(ctx, pool, []string{url}, kp.PubKeyHex())
	if err == nil {
		t.Error("expected error for non-existent NIP-65 list")
	}
}

func TestFetchNIP65_InvalidPubKey(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := FetchNIP65(ctx, pool, []string{url}, "not-hex")
	if err == nil {
		t.Error("expected error for invalid pubkey")
	}
}

// ─── PublishNIP02ContactList + FetchNIP02Contacts ────────────────────────────

func TestPublishAndFetchNIP02Contacts_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	contacts := []NIP02Contact{
		{PubKey: "aaaa01234567890abcdef01234567890abcdef01234567890abcdef01234567", Relay: "wss://relay1.example", Petname: "alice"},
		{PubKey: "bbbb01234567890abcdef01234567890abcdef01234567890abcdef01234567", Relay: "", Petname: "bob"},
	}

	eventID, err := PublishNIP02ContactList(ctx, pool, kp.Keyer, []string{url}, contacts)
	if err != nil {
		t.Fatalf("PublishNIP02ContactList: %v", err)
	}
	if eventID == "" {
		t.Fatal("expected non-empty event ID")
	}

	got, _, err := FetchNIP02Contacts(ctx, pool, []string{url}, kp.PubKeyHex())
	if err != nil {
		t.Fatalf("FetchNIP02Contacts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 contacts, got %d", len(got))
	}

	contactMap := map[string]NIP02Contact{}
	for _, c := range got {
		contactMap[c.PubKey] = c
	}
	if c, ok := contactMap["aaaa01234567890abcdef01234567890abcdef01234567890abcdef01234567"]; !ok {
		t.Error("missing alice")
	} else {
		if c.Petname != "alice" {
			t.Errorf("alice petname: %q", c.Petname)
		}
		if c.Relay != "wss://relay1.example" {
			t.Errorf("alice relay: %q", c.Relay)
		}
	}
}

func TestFetchNIP02Contacts_NotFound(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, _, err := FetchNIP02Contacts(ctx, pool, []string{url}, kp.PubKeyHex())
	if err == nil {
		t.Error("expected error for non-existent contacts")
	}
}

// ─── RelaySelector.FetchAndCache ─────────────────────────────────────────────

func TestRelaySelector_FetchAndCache_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Publish a NIP-65 list first
	_, err := PublishNIP65(ctx, pool, kp.Keyer, []string{url},
		[]string{"wss://read.example"},
		nil,
		[]string{"wss://both.example"},
	)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	sel := NewRelaySelector(nil, nil)

	// FetchAndCache should retrieve and cache it
	list, err := sel.FetchAndCache(ctx, pool, []string{url}, kp.PubKeyHex())
	if err != nil {
		t.Fatalf("FetchAndCache: %v", err)
	}
	if len(list.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(list.Entries))
	}

	// Should now be in cache
	cached := sel.Get(kp.PubKeyHex())
	if cached == nil {
		t.Error("expected cached list after FetchAndCache")
	}
}

func TestRelaySelector_FetchAndCache_NotFound(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	sel := NewRelaySelector(nil, nil)
	_, err := sel.FetchAndCache(ctx, pool, []string{url}, kp.PubKeyHex())
	if err == nil {
		t.Error("expected error for non-existent list")
	}
}
