package runtime

import (
	"context"
	"strings"
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
	return receiveBeforeTestDeadline(t, ch, "relay update")
}

func assertNoRelayUpdate(t *testing.T, ch <-chan relayUpdate) {
	t.Helper()
	select {
	case upd := <-ch:
		t.Fatalf("unexpected relay update: %#v", upd)
	default:
	}
}

func newTestNIP65SelfSyncState(pk nostr.PubKey, updates chan<- relayUpdate) *nip65SelfSyncState {
	return newNIP65SelfSyncState(pk, func(read, write []string) {
		updates <- relayUpdate{read: append([]string{}, read...), write: append([]string{}, write...)}
	})
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

func TestFetchNIP65RequiresPool(t *testing.T) {
	_, err := FetchNIP65(context.Background(), nil, []string{"wss://relay.example"}, strings.Repeat("1", 64))
	if err == nil || err.Error() != "nip65: pool is required" {
		t.Fatalf("expected pool validation error, got %v", err)
	}
}

func TestPublishNIP65RequiresPool(t *testing.T) {
	keyer := newNIP04KeyerAdapter(nostr.Generate())
	_, err := PublishNIP65(context.Background(), nil, keyer, []string{"wss://relay.example"}, nil, nil, []string{"wss://relay.example"})
	if err == nil || err.Error() != "nip65: pool is required" {
		t.Fatalf("expected pool validation error, got %v", err)
	}
}

func TestPublishNIP65RequiresKeyer(t *testing.T) {
	pool := NewPoolNIP42(newNIP04KeyerAdapter(nostr.Generate()))
	defer pool.Close("test")
	_, err := PublishNIP65(context.Background(), pool, nil, []string{"wss://relay.example"}, nil, nil, []string{"wss://relay.example"})
	if err == nil || err.Error() != "nip65: keyer is required" {
		t.Fatalf("expected keyer validation error, got %v", err)
	}
}

func TestFetchNIP02ContactsRequiresPool(t *testing.T) {
	_, _, err := FetchNIP02Contacts(context.Background(), nil, []string{"wss://relay.example"}, strings.Repeat("1", 64))
	if err == nil || err.Error() != "nip02: pool is required" {
		t.Fatalf("expected pool validation error, got %v", err)
	}
}

func TestPublishNIP02ContactListRequiresPool(t *testing.T) {
	keyer := newNIP04KeyerAdapter(nostr.Generate())
	_, err := PublishNIP02ContactList(context.Background(), nil, keyer, []string{"wss://relay.example"}, nil)
	if err == nil || err.Error() != "nip02: pool is required" {
		t.Fatalf("expected pool validation error, got %v", err)
	}
}

func TestFetchKind10050RequiresPool(t *testing.T) {
	_, err := fetchKind10050(context.Background(), nil, []string{"wss://relay.example"}, nostr.Generate().Public())
	if err == nil || err.Error() != "nip17: pool is required" {
		t.Fatalf("expected pool validation error, got %v", err)
	}
}

func TestPublishKind10050RequiresPool(t *testing.T) {
	keyer := newNIP04KeyerAdapter(nostr.Generate())
	_, err := publishKind10050(context.Background(), nil, keyer, []string{"wss://relay.example"}, []string{"wss://relay.example"})
	if err == nil || err.Error() != "nip17: pool is required" {
		t.Fatalf("expected pool validation error, got %v", err)
	}
}

func TestPublishKind10050RequiresKeyer(t *testing.T) {
	pool := NewPoolNIP42(newNIP04KeyerAdapter(nostr.Generate()))
	defer pool.Close("test")
	_, err := publishKind10050(context.Background(), pool, nil, []string{"wss://relay.example"}, []string{"wss://relay.example"})
	if err == nil || err.Error() != "nip17: keyer is required" {
		t.Fatalf("expected keyer validation error, got %v", err)
	}
}

func TestPublishNIP02ContactListRequiresKeyer(t *testing.T) {
	pool := NewPoolNIP42(newNIP04KeyerAdapter(nostr.Generate()))
	defer pool.Close("test")
	_, err := PublishNIP02ContactList(context.Background(), pool, nil, []string{"wss://relay.example"}, nil)
	if err == nil || err.Error() != "nip02: keyer is required" {
		t.Fatalf("expected keyer validation error, got %v", err)
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

func TestRelaySelectorPutDoesNotReplaceNewerMetadataWithStaleList(t *testing.T) {
	sel := NewRelaySelector(nil, nil)
	sel.Put(&NIP65RelayList{
		PubKey:    "abc123",
		CreatedAt: 20,
		EventID:   "bbbb",
		Entries:   []NIP65RelayEntry{{URL: "wss://new.example", Write: true}},
	})
	sel.Put(&NIP65RelayList{
		PubKey:    "abc123",
		CreatedAt: 10,
		EventID:   "ffff",
		Entries:   []NIP65RelayEntry{{URL: "wss://stale.example", Write: true}},
	})

	got := sel.Get("abc123")
	if got == nil {
		t.Fatal("expected cached list")
	}
	if relays := got.WriteRelays(); len(relays) != 1 || relays[0] != "wss://new.example" {
		t.Fatalf("stale metadata replaced newer list: %v", relays)
	}
}

func TestRelaySelectorPutUsesEventIDTieBreakerAtSameTimestamp(t *testing.T) {
	sel := NewRelaySelector(nil, nil)
	sel.Put(&NIP65RelayList{
		PubKey:    "abc123",
		CreatedAt: 20,
		EventID:   "1000",
		Entries:   []NIP65RelayEntry{{URL: "wss://low.example", Write: true}},
	})
	sel.Put(&NIP65RelayList{
		PubKey:    "abc123",
		CreatedAt: 20,
		EventID:   "0fff",
		Entries:   []NIP65RelayEntry{{URL: "wss://stale-tie.example", Write: true}},
	})
	if got := sel.Get("abc123"); got == nil || got.WriteRelays()[0] != "wss://low.example" {
		t.Fatalf("lower event-id tie-break should not replace current list: %#v", got)
	}

	sel.Put(&NIP65RelayList{
		PubKey:    "abc123",
		CreatedAt: 20,
		EventID:   "1001",
		Entries:   []NIP65RelayEntry{{URL: "wss://high.example", Write: true}},
	})
	if got := sel.Get("abc123"); got == nil || got.WriteRelays()[0] != "wss://high.example" {
		t.Fatalf("higher event-id tie-break should replace current list: %#v", got)
	}
}

func TestRelaySelectorPutClonesInput(t *testing.T) {
	sel := NewRelaySelector(nil, nil)
	list := &NIP65RelayList{
		PubKey:  "abc123",
		Entries: []NIP65RelayEntry{{URL: "wss://r1.example.com", Read: true}},
	}
	sselBefore := list.Entries[0].URL
	sel.Put(list)

	list.Entries[0].URL = "wss://mutated.example.com"
	got := sel.Get("abc123")
	if got == nil {
		t.Fatal("expected cached list")
	}
	if got.Entries[0].URL != sselBefore {
		t.Fatalf("cached relay URL = %q, want %q", got.Entries[0].URL, sselBefore)
	}
}

func TestRelaySelectorGetReturnsCopy(t *testing.T) {
	sel := NewRelaySelector(nil, nil)
	sel.Put(&NIP65RelayList{
		PubKey:  "abc123",
		Entries: []NIP65RelayEntry{{URL: "wss://r1.example.com", Read: true}},
	})

	first := sel.Get("abc123")
	if first == nil {
		t.Fatal("expected cached list")
	}
	first.Entries[0].URL = "wss://mutated.example.com"

	second := sel.Get("abc123")
	if second == nil {
		t.Fatal("expected cached list on second get")
	}
	if second.Entries[0].URL != "wss://r1.example.com" {
		t.Fatalf("cached relay URL = %q, want original", second.Entries[0].URL)
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
	sel.cacheTTL = time.Millisecond
	sel.Put(&NIP65RelayList{PubKey: "abc123"})

	sel.mu.Lock()
	entry := sel.cache["abc123"]
	entry.fetchedAt = time.Now().Add(-2 * time.Millisecond)
	sel.mu.Unlock()

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

func TestNIP65SelfSyncRequiresPool(t *testing.T) {
	keyer := newNIP04KeyerAdapter(nostr.Generate())
	err := NIP65SelfSync(context.Background(), NIP65SyncOptions{Keyer: keyer})
	if err == nil || err.Error() != "nip65: pool is required" {
		t.Fatalf("expected pool validation error, got %v", err)
	}
}

func TestPublishStartupListsRequiresPool(t *testing.T) {
	keyer := newNIP04KeyerAdapter(nostr.Generate())
	err := PublishStartupLists(context.Background(), StartupListPublishOptions{
		Keyer:         keyer,
		PublishRelays: []string{"wss://relay.example"},
	})
	if err == nil || err.Error() != "startup lists: pool is required" {
		t.Fatalf("expected pool validation error, got %v", err)
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

func TestSelectLatestVerifiedMetadataEventRejectsInvalidSameTimestampTieBreak(t *testing.T) {
	valid := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://valid.example", "write"}},
	)
	invalidID := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://invalid.example", "write"}},
	)
	for i := range invalidID.ID {
		invalidID.ID[i] = 0xff
	}

	got := selectLatestVerifiedMetadataEvent(
		relayEventStream(valid, invalidID),
		valid.PubKey,
		10002,
	)
	if got == nil {
		t.Fatal("expected verified event to be selected")
	}
	if got.ID != valid.ID {
		t.Fatalf("invalid same-timestamp tie-break won: selected %s, want %s", got.ID.Hex(), valid.ID.Hex())
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
	state := newTestNIP65SelfSyncState(newer.PubKey, updates)

	events <- nostr.RelayEvent{Event: older}
	events <- nostr.RelayEvent{Event: newer}
	assertNoRelayUpdate(t, updates)
	state.handleEOSE(events)

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
	state := newTestNIP65SelfSyncState(startup.PubKey, updates)

	state.handleEvent(nostr.RelayEvent{Event: startup})
	state.handleEOSE(nil)
	first := waitRelayUpdate(t, updates)
	if !relaySliceEqual(first.read, []string{"wss://startup-read.example"}) || !relaySliceEqual(first.write, []string{"wss://startup-write.example"}) {
		t.Fatalf("unexpected startup update: %#v", first)
	}

	state.handleEvent(nostr.RelayEvent{Event: stale})
	assertNoRelayUpdate(t, updates)

	state.handleEvent(nostr.RelayEvent{Event: newer})
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
	state := newTestNIP65SelfSyncState(expected.PubKey, updates)

	events <- nostr.RelayEvent{Event: other}
	events <- nostr.RelayEvent{Event: expected}
	assertNoRelayUpdate(t, updates)
	state.handleEOSE(events)
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
	state := newTestNIP65SelfSyncState(newer.PubKey, updates)

	events <- nostr.RelayEvent{Event: older}
	events <- nostr.RelayEvent{Event: newer}
	state.handleEOSE(events)
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
	state := newTestNIP65SelfSyncState(startup.PubKey, updates)

	state.handleEvent(nostr.RelayEvent{Event: startup})
	state.handleEOSE(nil)
	first := waitRelayUpdate(t, updates)
	if !relaySliceEqual(first.read, []string{"wss://same-read.example"}) || !relaySliceEqual(first.write, []string{"wss://same-write.example"}) {
		t.Fatalf("unexpected startup update: %#v", first)
	}

	state.handleEvent(nostr.RelayEvent{Event: republish})
	assertNoRelayUpdate(t, updates)
}

func TestMetadataValidationFailureFutureThresholdBoundary(t *testing.T) {
	skHex := "1111111111111111111111111111111111111111111111111111111111111111"
	base := time.Now()
	atThreshold := mustSignedMetadataEvent(t,
		skHex,
		10002,
		nostr.Timestamp(base.Add(inboundEventMaxFutureSkew).Unix()),
		nostr.Tags{{"r", "wss://ok.example", "read"}},
	)
	if reason := metadataValidationFailure(atThreshold, atThreshold.PubKey, 10002); reason != "" {
		t.Fatalf("expected exact future threshold to be accepted, got %q", reason)
	}

	overThreshold := mustSignedMetadataEvent(t,
		skHex,
		10002,
		nostr.Timestamp(base.Add(inboundEventMaxFutureSkew+time.Second).Unix()),
		nostr.Tags{{"r", "wss://future.example", "read"}},
	)
	if reason := metadataValidationFailure(overThreshold, overThreshold.PubKey, 10002); reason != "created_at_future" {
		t.Fatalf("expected created_at_future, got %q", reason)
	}
}

func TestMetadataValidationFailure(t *testing.T) {
	valid := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(20),
		nostr.Tags{{"r", "wss://valid.example"}},
	)
	if reason := metadataValidationFailure(valid, valid.PubKey, 10002); reason != "" {
		t.Fatalf("expected valid metadata event, got reason %q", reason)
	}

	wrongKind := valid
	wrongKind.Kind = 1
	if reason := metadataValidationFailure(wrongKind, valid.PubKey, 10002); reason != "unexpected_kind:1" {
		t.Fatalf("wrong kind reason = %q", reason)
	}

	wrongAuthor := valid
	wrongAuthor.PubKey = nostr.Generate().Public()
	if reason := metadataValidationFailure(wrongAuthor, valid.PubKey, 10002); reason != "unexpected_author" {
		t.Fatalf("wrong author reason = %q", reason)
	}

	invalidID := valid
	invalidID.ID[0] ^= 0x01
	if reason := metadataValidationFailure(invalidID, valid.PubKey, 10002); reason != "invalid_id" {
		t.Fatalf("invalid id reason = %q", reason)
	}

	invalidSig := valid
	invalidSig.Sig[0] ^= 0x01
	if reason := metadataValidationFailure(invalidSig, valid.PubKey, 10002); reason != "invalid_signature" {
		t.Fatalf("invalid signature reason = %q", reason)
	}

	future := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		10002,
		nostr.Timestamp(time.Now().Unix()+31),
		nostr.Tags{{"r", "wss://future.example"}},
	)
	if reason := metadataValidationFailure(future, valid.PubKey, 10002); reason != "created_at_future" {
		t.Fatalf("future created_at reason = %q", reason)
	}
}

// ─── Additional NIP-65 tests (Phase 6) ──────────────────────────────────────

func TestDecodeNIP65Event_UnknownMarker(t *testing.T) {
	ev := nostr.Event{
		Kind: 10002,
		Tags: nostr.Tags{{"r", "wss://relay1.example", "unknown"}},
	}
	list := DecodeNIP65Event(ev)
	if !list.Entries[0].Read || !list.Entries[0].Write {
		t.Error("unknown marker should default to both")
	}
}

func TestDecodeNIP65Event_SkipsNonRTags(t *testing.T) {
	ev := nostr.Event{
		Kind: 10002,
		Tags: nostr.Tags{{"d", "something"}, {"p", "pubkey"}, {"r", "wss://relay1.example"}},
	}
	list := DecodeNIP65Event(ev)
	if len(list.Entries) != 1 {
		t.Errorf("expected 1, got %d", len(list.Entries))
	}
}

func TestDecodeNIP65Event_SkipsShortTags(t *testing.T) {
	ev := nostr.Event{Kind: 10002, Tags: nostr.Tags{{"r"}}}
	list := DecodeNIP65Event(ev)
	if len(list.Entries) != 0 {
		t.Errorf("expected 0, got %d", len(list.Entries))
	}
}

func TestDecodeNIP65Event_Empty(t *testing.T) {
	list := DecodeNIP65Event(nostr.Event{Kind: 10002})
	if len(list.Entries) != 0 {
		t.Errorf("expected 0, got %d", len(list.Entries))
	}
}

func TestIsNewerReplaceableMetadataEvent_NilCurrent(t *testing.T) {
	if !isNewerReplaceableMetadataEvent(nil, nostr.Event{CreatedAt: 100}) {
		t.Error("nil current should always accept candidate")
	}
}

func TestIsNewerReplaceableMetadataEvent_Newer(t *testing.T) {
	current := nostr.Event{CreatedAt: 100}
	if !isNewerReplaceableMetadataEvent(&current, nostr.Event{CreatedAt: 200}) {
		t.Error("newer candidate should be accepted")
	}
}

func TestIsNewerReplaceableMetadataEvent_Older(t *testing.T) {
	current := nostr.Event{CreatedAt: 200}
	if isNewerReplaceableMetadataEvent(&current, nostr.Event{CreatedAt: 100}) {
		t.Error("older candidate should be rejected")
	}
}

func TestIsVerifiedMetadataEvent_Valid(t *testing.T) {
	sk, _ := ParseSecretKey("1111111111111111111111111111111111111111111111111111111111111111")
	keyer := newNIP04KeyerAdapter(sk)
	ev := nostr.Event{Kind: 10002, CreatedAt: nostr.Now()}
	keyer.SignEvent(context.Background(), &ev)
	if !isVerifiedMetadataEvent(ev, ev.PubKey, 10002) {
		t.Error("should be valid")
	}
}

func TestIsVerifiedMetadataEvent_WrongAuthor(t *testing.T) {
	sk, _ := ParseSecretKey("1111111111111111111111111111111111111111111111111111111111111111")
	keyer := newNIP04KeyerAdapter(sk)
	ev := nostr.Event{Kind: 10002, CreatedAt: nostr.Now()}
	keyer.SignEvent(context.Background(), &ev)
	wrongPk := nostr.Generate().Public()
	if isVerifiedMetadataEvent(ev, wrongPk, 10002) {
		t.Error("wrong author should be invalid")
	}
}

func TestSelectLatestVerifiedMetadataEventKind3RejectsInvalidSignature(t *testing.T) {
	valid := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		3,
		nostr.Timestamp(10),
		nostr.Tags{{"p", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
	)
	invalidSig := mustSignedMetadataEvent(t,
		"1111111111111111111111111111111111111111111111111111111111111111",
		3,
		nostr.Timestamp(20),
		nostr.Tags{{"p", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
	)
	invalidSig.Sig[0] ^= 0x01

	got := selectLatestVerifiedMetadataEvent(
		relayEventStream(invalidSig, valid),
		valid.PubKey,
		3,
	)
	if got == nil {
		t.Fatal("expected verified kind:3 event to be selected")
	}
	if got.ID != valid.ID {
		t.Fatalf("selected event = %s, want %s", got.ID.Hex(), valid.ID.Hex())
	}
}

func TestDecodeNIP02ContactsSkipsInvalidPubkeys(t *testing.T) {
	ev := nostr.Event{
		Tags: nostr.Tags{
			{"p", "not-a-pubkey"},
			{"p", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "wss://relay.example", "alice"},
		},
	}
	contacts := decodeNIP02Contacts(ev)
	if len(contacts) != 1 {
		t.Fatalf("expected 1 valid contact, got %d", len(contacts))
	}
	if contacts[0].PubKey != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected pubkey: %s", contacts[0].PubKey)
	}
}

func TestShortPubKey(t *testing.T) {
	sk := nostr.Generate()
	pk := nostr.GetPublicKey(sk)
	short := shortPubKey(pk)
	if len(short) != 12 {
		t.Errorf("expected 12 chars, got %d: %q", len(short), short)
	}
}

func TestShortEventID(t *testing.T) {
	sk := nostr.Generate()
	ev := nostr.Event{Kind: 1, CreatedAt: nostr.Now()}
	ev.Sign(sk)
	short := shortEventID(ev.ID)
	if len(short) != 12 {
		t.Errorf("expected 12 chars, got %d: %q", len(short), short)
	}
}

func TestMinInt(t *testing.T) {
	if MinInt(5, 10) != 5 {
		t.Error("5,10")
	}
	if MinInt(10, 5) != 5 {
		t.Error("10,5")
	}
	if MinInt(7, 7) != 7 {
		t.Error("7,7")
	}
}

func TestMetadataRelayURL_NilRelay(t *testing.T) {
	if got := metadataRelayURL(nostr.RelayEvent{}); got != "" {
		t.Errorf("nil relay: %q", got)
	}
}

func TestMetadataRelayURL_WithRelay(t *testing.T) {
	re := nostr.RelayEvent{Relay: &nostr.Relay{URL: "  wss://relay.example  "}}
	if got := metadataRelayURL(re); got != "wss://relay.example" {
		t.Errorf("got %q", got)
	}
}

func TestNIP65RelayList_ReadRelays(t *testing.T) {
	list := &NIP65RelayList{
		Entries: []NIP65RelayEntry{
			{URL: "wss://both.example", Read: true, Write: true},
			{URL: "wss://read-only.example", Read: true},
			{URL: "wss://write-only.example", Write: true},
		},
	}
	if len(list.ReadRelays()) != 2 {
		t.Errorf("expected 2 read relays, got %d", len(list.ReadRelays()))
	}
}

func TestNIP65RelayList_WriteRelays(t *testing.T) {
	list := &NIP65RelayList{
		Entries: []NIP65RelayEntry{
			{URL: "wss://both.example", Read: true, Write: true},
			{URL: "wss://read-only.example", Read: true},
			{URL: "wss://write-only.example", Write: true},
		},
	}
	if len(list.WriteRelays()) != 2 {
		t.Errorf("expected 2 write relays, got %d", len(list.WriteRelays()))
	}
}
