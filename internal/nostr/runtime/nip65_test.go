package runtime

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

func mustSignedMetadataEvent(t *testing.T, skHex string, kind nostr.Kind, createdAt nostr.Timestamp, tags nostr.Tags) nostr.Event {
	t.Helper()
	sk, err := ParseSecretKey(skHex)
	if err != nil {
		t.Fatalf("ParseSecretKey: %v", err)
	}
	keyer := newNIP04KeyerAdapter(sk)
	evt := nostr.Event{
		Kind:      kind,
		CreatedAt: createdAt,
		Tags:      tags,
		Content:   "",
	}
	if err := keyer.SignEvent(context.Background(), &evt); err != nil {
		t.Fatalf("SignEvent: %v", err)
	}
	return evt
}

func relayEventStream(events ...nostr.Event) <-chan nostr.RelayEvent {
	ch := make(chan nostr.RelayEvent, len(events))
	for _, evt := range events {
		ch <- nostr.RelayEvent{Event: evt}
	}
	close(ch)
	return ch
}

type relayUpdate struct {
	read  []string
	write []string
}

func waitRelayUpdate(t *testing.T, ch <-chan relayUpdate) relayUpdate {
	t.Helper()
	select {
	case upd := <-ch:
		return upd
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for relay update")
		return relayUpdate{}
	}
}

func assertNoRelayUpdate(t *testing.T, ch <-chan relayUpdate) {
	t.Helper()
	select {
	case upd := <-ch:
		t.Fatalf("unexpected relay update: %#v", upd)
	case <-time.After(50 * time.Millisecond):
	}
}

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

func TestSelectLatestVerifiedMetadataEventRejectsWrongAuthor(t *testing.T) {
	valid := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(10),
		nostr.Tags{{"r", "wss://valid.example"}},
	)
	wrongAuthor := mustSignedMetadataEvent(t,
		"2222222222222222222222222222222222222222222222222222222222222222",
		10002,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://wrong.example"}},
	)

	got := selectLatestVerifiedMetadataEvent(
		relayEventStream(wrongAuthor, valid),
		valid.PubKey,
		10002,
	)
	if got == nil {
		t.Fatal("expected verified event to be selected")
	}
	if got.ID != valid.ID {
		t.Fatalf("selected event = %s, want %s", got.ID.Hex(), valid.ID.Hex())
	}
}

func TestSelectLatestVerifiedMetadataEventRejectsInvalidSignature(t *testing.T) {
	valid := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(10),
		nostr.Tags{{"r", "wss://valid.example"}},
	)
	invalidSig := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://tampered.example"}},
	)
	invalidSig.Sig[0] ^= 0x01

	got := selectLatestVerifiedMetadataEvent(
		relayEventStream(invalidSig, valid),
		valid.PubKey,
		10002,
	)
	if got == nil {
		t.Fatal("expected verified event to be selected")
	}
	if got.ID != valid.ID {
		t.Fatalf("selected event = %s, want %s", got.ID.Hex(), valid.ID.Hex())
	}
}

func TestSelectLatestVerifiedMetadataEventRejectsInvalidID(t *testing.T) {
	valid := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10050,
		nostr.Timestamp(10),
		nostr.Tags{{"relay", "wss://valid.example"}},
	)
	invalidID := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10050,
		nostr.Timestamp(20),
		nostr.Tags{{"relay", "wss://tampered.example"}},
	)
	invalidID.ID[0] ^= 0x01

	got := selectLatestVerifiedMetadataEvent(
		relayEventStream(invalidID, valid),
		valid.PubKey,
		10050,
	)
	if got == nil {
		t.Fatal("expected verified event to be selected")
	}
	if got.ID != valid.ID {
		t.Fatalf("selected event = %s, want %s", got.ID.Hex(), valid.ID.Hex())
	}
}

func TestSelectLatestVerifiedMetadataEventRejectsWrongKind(t *testing.T) {
	valid := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(10),
		nostr.Tags{{"r", "wss://valid.example"}},
	)
	wrongKind := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		1,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://wrong-kind.example"}},
	)

	got := selectLatestVerifiedMetadataEvent(
		relayEventStream(wrongKind, valid),
		valid.PubKey,
		10002,
	)
	if got == nil {
		t.Fatal("expected verified event to be selected")
	}
	if got.ID != valid.ID {
		t.Fatalf("selected event = %s, want %s", got.ID.Hex(), valid.ID.Hex())
	}
}

func TestRunNIP65SelfSyncLoopAppliesBestPreEOSEEvent(t *testing.T) {
	older := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(10),
		nostr.Tags{{"r", "wss://old-read.example", "read"}, {"r", "wss://old-write.example", "write"}},
	)
	newer := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://new-read.example", "read"}, {"r", "wss://new-write.example", "write"}},
	)
	updates := make(chan relayUpdate, 4)
	events := make(chan nostr.RelayEvent, 4)
	eoseCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runNIP65SelfSyncLoop(ctx, newer.PubKey, events, eoseCh, func(read, write []string) {
		updates <- relayUpdate{read: append([]string{}, read...), write: append([]string{}, write...)}
	})

	events <- nostr.RelayEvent{Event: older}
	events <- nostr.RelayEvent{Event: newer}
	assertNoRelayUpdate(t, updates)
	close(eoseCh)

	upd := waitRelayUpdate(t, updates)
	if !relaySliceEqual(upd.read, []string{"wss://new-read.example"}) {
		t.Fatalf("read = %v", upd.read)
	}
	if !relaySliceEqual(upd.write, []string{"wss://new-write.example"}) {
		t.Fatalf("write = %v", upd.write)
	}
}

func TestRunNIP65SelfSyncLoopIgnoresStaleLiveUpdatesAfterEOSE(t *testing.T) {
	startup := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://startup-read.example", "read"}, {"r", "wss://startup-write.example", "write"}},
	)
	stale := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(10),
		nostr.Tags{{"r", "wss://stale-read.example", "read"}, {"r", "wss://stale-write.example", "write"}},
	)
	newer := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(30),
		nostr.Tags{{"r", "wss://live-read.example", "read"}, {"r", "wss://live-write.example", "write"}},
	)
	updates := make(chan relayUpdate, 4)
	events := make(chan nostr.RelayEvent, 4)
	eoseCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runNIP65SelfSyncLoop(ctx, startup.PubKey, events, eoseCh, func(read, write []string) {
		updates <- relayUpdate{read: append([]string{}, read...), write: append([]string{}, write...)}
	})

	events <- nostr.RelayEvent{Event: startup}
	close(eoseCh)
	first := waitRelayUpdate(t, updates)
	if !relaySliceEqual(first.read, []string{"wss://startup-read.example"}) || !relaySliceEqual(first.write, []string{"wss://startup-write.example"}) {
		t.Fatalf("unexpected startup update: %#v", first)
	}

	events <- nostr.RelayEvent{Event: stale}
	assertNoRelayUpdate(t, updates)

	events <- nostr.RelayEvent{Event: newer}
	second := waitRelayUpdate(t, updates)
	if !relaySliceEqual(second.read, []string{"wss://live-read.example"}) || !relaySliceEqual(second.write, []string{"wss://live-write.example"}) {
		t.Fatalf("unexpected live update: %#v", second)
	}
}

func TestRunNIP65SelfSyncLoopUsesEventIDTieBreakerAtSameTimestamp(t *testing.T) {
	a := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://a-read.example", "read"}, {"r", "wss://a-write.example", "write"}},
	)
	b := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://b-read.example", "read"}, {"r", "wss://b-write.example", "write"}},
	)
	expected := a
	expectedRead := []string{"wss://a-read.example"}
	expectedWrite := []string{"wss://a-write.example"}
	other := b
	if b.ID.Hex() > a.ID.Hex() {
		expected = b
		expectedRead = []string{"wss://b-read.example"}
		expectedWrite = []string{"wss://b-write.example"}
		other = a
	}
	updates := make(chan relayUpdate, 2)
	events := make(chan nostr.RelayEvent, 2)
	eoseCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runNIP65SelfSyncLoop(ctx, expected.PubKey, events, eoseCh, func(read, write []string) {
		updates <- relayUpdate{read: append([]string{}, read...), write: append([]string{}, write...)}
	})

	events <- nostr.RelayEvent{Event: other}
	events <- nostr.RelayEvent{Event: expected}
	assertNoRelayUpdate(t, updates)
	close(eoseCh)
	upd := waitRelayUpdate(t, updates)
	if !relaySliceEqual(upd.read, expectedRead) || !relaySliceEqual(upd.write, expectedWrite) {
		t.Fatalf("unexpected tie-break update: %#v expected event=%s", upd, expected.ID.Hex())
	}
}

func TestRunNIP65SelfSyncLoopDrainsBufferedPreEOSEEventsBeforeStartupApply(t *testing.T) {
	older := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(10),
		nostr.Tags{{"r", "wss://older-read.example", "read"}, {"r", "wss://older-write.example", "write"}},
	)
	newer := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://buffered-read.example", "read"}, {"r", "wss://buffered-write.example", "write"}},
	)
	updates := make(chan relayUpdate, 4)
	events := make(chan nostr.RelayEvent, 4)
	eoseCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runNIP65SelfSyncLoop(ctx, newer.PubKey, events, eoseCh, func(read, write []string) {
		updates <- relayUpdate{read: append([]string{}, read...), write: append([]string{}, write...)}
	})

	events <- nostr.RelayEvent{Event: older}
	events <- nostr.RelayEvent{Event: newer}
	close(eoseCh)
	upd := waitRelayUpdate(t, updates)
	if !relaySliceEqual(upd.read, []string{"wss://buffered-read.example"}) || !relaySliceEqual(upd.write, []string{"wss://buffered-write.example"}) {
		t.Fatalf("unexpected buffered startup update: %#v", upd)
	}
	assertNoRelayUpdate(t, updates)
}

func TestRunNIP65SelfSyncLoopSuppressesSemanticallyIdenticalRepublish(t *testing.T) {
	startup := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://same-read.example", "read"}, {"r", "wss://same-write.example", "write"}},
	)
	republish := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(30),
		nostr.Tags{{"r", "wss://same-read.example", "read"}, {"r", "wss://same-write.example", "write"}},
	)
	updates := make(chan relayUpdate, 4)
	events := make(chan nostr.RelayEvent, 4)
	eoseCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runNIP65SelfSyncLoop(ctx, startup.PubKey, events, eoseCh, func(read, write []string) {
		updates <- relayUpdate{read: append([]string{}, read...), write: append([]string{}, write...)}
	})

	events <- nostr.RelayEvent{Event: startup}
	close(eoseCh)
	first := waitRelayUpdate(t, updates)
	if !relaySliceEqual(first.read, []string{"wss://same-read.example"}) || !relaySliceEqual(first.write, []string{"wss://same-write.example"}) {
		t.Fatalf("unexpected startup update: %#v", first)
	}

	events <- nostr.RelayEvent{Event: republish}
	assertNoRelayUpdate(t, updates)
}
