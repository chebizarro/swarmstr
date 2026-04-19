package nip51

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/testutil"
)

// ─── Publish + Fetch round-trip ─────────────────────────────────────────────

func TestPublishAndFetch_RoundTrip(t *testing.T) {
	url := testutil.NewTestRelay(t)
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	pool := nostr.NewPool(nostr.PoolOptions{})

	list := &List{
		Kind:  10000, // mute list
		DTag:  "",
		Title: "My Mute List",
		Entries: []ListEntry{
			{Tag: "p", Value: "deadbeef01234567deadbeef01234567deadbeef01234567deadbeef01234567"},
			{Tag: "t", Value: "spam"},
		},
	}

	eventID, err := Publish(ctx, pool, kp.Keyer, []string{url}, list)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if eventID == "" {
		t.Fatal("expected non-empty event ID")
	}

	// Fetch it back
	got, err := Fetch(ctx, pool, []string{url}, kp.PubKeyHex(), 10000, "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Kind != 10000 {
		t.Errorf("kind: %d", got.Kind)
	}
	if got.Title != "My Mute List" {
		t.Errorf("title: %q", got.Title)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got.Entries))
	}

	// Check entries
	foundP, foundT := false, false
	for _, e := range got.Entries {
		if e.Tag == "p" && e.Value == "deadbeef01234567deadbeef01234567deadbeef01234567deadbeef01234567" {
			foundP = true
		}
		if e.Tag == "t" && e.Value == "spam" {
			foundT = true
		}
	}
	if !foundP {
		t.Error("missing p entry")
	}
	if !foundT {
		t.Error("missing t entry")
	}
}

// ─── Publish + Fetch with d-tag ─────────────────────────────────────────────

func TestPublishAndFetch_WithDTag(t *testing.T) {
	url := testutil.NewTestRelay(t)
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool := nostr.NewPool(nostr.PoolOptions{})

	list := &List{
		Kind: 30000, // categorized people list
		DTag: "friends",
		Entries: []ListEntry{
			{Tag: "p", Value: "aaaa01234567890abcdef01234567890abcdef01234567890abcdef01234567"},
		},
	}

	_, err := Publish(ctx, pool, kp.Keyer, []string{url}, list)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	got, err := Fetch(ctx, pool, []string{url}, kp.PubKeyHex(), 30000, "friends")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.DTag != "friends" {
		t.Errorf("dtag: %q", got.DTag)
	}
	if len(got.Entries) != 1 {
		t.Errorf("entries: %d", len(got.Entries))
	}
}

// ─── Fetch not found ─────────────────────────────────────────────────────────

func TestFetch_NotFound(t *testing.T) {
	url := testutil.NewTestRelay(t)
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	pool := nostr.NewPool(nostr.PoolOptions{})

	_, err := Fetch(ctx, pool, []string{url}, kp.PubKeyHex(), 10000, "")
	if err == nil {
		t.Error("expected error for non-existent list")
	}
}

// ─── Fetch invalid pubkey ────────────────────────────────────────────────────

func TestFetch_InvalidPubKey(t *testing.T) {
	url := testutil.NewTestRelay(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	pool := nostr.NewPool(nostr.PoolOptions{})

	_, err := Fetch(ctx, pool, []string{url}, "not-a-hex-pubkey", 10000, "")
	if err == nil {
		t.Error("expected error for invalid pubkey")
	}
}

// ─── Publish replaceable overwrites ──────────────────────────────────────────

func TestPublish_ReplaceableOverwrites(t *testing.T) {
	url := testutil.NewTestRelay(t)
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool := nostr.NewPool(nostr.PoolOptions{})

	// Publish first version
	list1 := &List{
		Kind:    10000,
		Entries: []ListEntry{{Tag: "p", Value: "aaaa01234567890abcdef01234567890abcdef01234567890abcdef01234567"}},
	}
	_, err := Publish(ctx, pool, kp.Keyer, []string{url}, list1)
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}

	// Nostr timestamps have second resolution (nostr.Now() returns seconds).
	// A replaceable event needs a strictly newer created_at to replace the
	// prior version on the relay.  We must advance the wall-clock by at
	// least one full second.  There is no protocol-level alternative to
	// this wait.
	time.Sleep(1100 * time.Millisecond)

	// Publish second version (should replace)
	list2 := &List{
		Kind:    10000,
		Entries: []ListEntry{{Tag: "t", Value: "updated"}},
	}
	_, err = Publish(ctx, pool, kp.Keyer, []string{url}, list2)
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}

	// Fetch should return the latest
	got, err := Fetch(ctx, pool, []string{url}, kp.PubKeyHex(), 10000, "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	// Should have the v2 entry, not v1
	if len(got.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got.Entries))
	}
	if got.Entries[0].Tag != "t" || got.Entries[0].Value != "updated" {
		t.Errorf("expected updated entry, got %v", got.Entries[0])
	}
}

// ─── Publish with petname and relay hints ────────────────────────────────────

func TestPublish_EntryWithRelayAndPetname(t *testing.T) {
	url := testutil.NewTestRelay(t)
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool := nostr.NewPool(nostr.PoolOptions{})

	list := &List{
		Kind: 30000,
		DTag: "buddies",
		Entries: []ListEntry{
			{
				Tag:     "p",
				Value:   "bbbb01234567890abcdef01234567890abcdef01234567890abcdef01234567",
				Relay:   "wss://relay.example",
				Petname: "alice",
			},
		},
	}

	_, err := Publish(ctx, pool, kp.Keyer, []string{url}, list)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	got, err := Fetch(ctx, pool, []string{url}, kp.PubKeyHex(), 30000, "buddies")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if len(got.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got.Entries))
	}
	e := got.Entries[0]
	if e.Relay != "wss://relay.example" {
		t.Errorf("relay: %q", e.Relay)
	}
	if e.Petname != "alice" {
		t.Errorf("petname: %q", e.Petname)
	}
}

// ─── Subscribe live updates ──────────────────────────────────────────────────

func TestSubscribe_LiveUpdates(t *testing.T) {
	url := testutil.NewTestRelay(t)
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool := nostr.NewPool(nostr.PoolOptions{})

	store := NewListStore()

	// Start subscription in background.  There is no explicit "ready" signal
	// from Subscribe, but the polling loop below tolerates subscription
	// setup delay without needing a sleep guard.
	go Subscribe(ctx, pool, store, []string{url}, []string{kp.PubKeyHex()}, []int{10000})

	// Publish a mute list
	list := &List{
		Kind:    10000,
		Entries: []ListEntry{{Tag: "p", Value: "cccc01234567890abcdef01234567890abcdef01234567890abcdef01234567"}},
	}
	_, err := Publish(ctx, pool, kp.Keyer, []string{url}, list)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Poll store until the subscription delivers the published list.
	// The poll interval (50ms) is a scheduling yield, not a protocol
	// wait — the test is bounded by the 10s context deadline.
	for {
		if got, ok := store.Get(kp.PubKeyHex(), 10000, ""); ok {
			if len(got.Entries) == 1 && got.Entries[0].Value == "cccc01234567890abcdef01234567890abcdef01234567890abcdef01234567" {
				return // success
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("timeout waiting for store to receive list update")
		case <-time.After(50 * time.Millisecond):
			// keep polling
		}
	}
}

// ─── Subscribe with no pubkeys/relays (no-op) ───────────────────────────────

func TestSubscribe_NoPubkeys(t *testing.T) {
	url := testutil.NewTestRelay(t)
	store := NewListStore()
	// Should return immediately without panic
	Subscribe(t.Context(), nil, store, []string{url}, nil, []int{10000})
}

func TestSubscribe_NoRelays(t *testing.T) {
	store := NewListStore()
	Subscribe(t.Context(), nil, store, nil, []string{"deadbeef"}, []int{10000})
}
