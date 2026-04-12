package nip51

import (
	"encoding/json"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
)

// ─── ListEntry / List ─────────────────────────────────────────────────────────

func TestRelaysFromList(t *testing.T) {
	l := &List{
		Entries: []ListEntry{
			{Tag: "r", Value: "wss://relay1.example"},
			{Tag: "p", Value: "pubkey1"},
			{Tag: "r", Value: "wss://relay2.example"},
			{Tag: "r", Value: ""}, // empty relay should be skipped
		},
	}
	got := RelaysFromList(l)
	if len(got) != 2 {
		t.Fatalf("expected 2 relays, got %d: %v", len(got), got)
	}
	if got[0] != "wss://relay1.example" || got[1] != "wss://relay2.example" {
		t.Errorf("unexpected relays: %v", got)
	}
}

func TestRelaysFromList_Empty(t *testing.T) {
	l := &List{Entries: nil}
	got := RelaysFromList(l)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestNewRelaySetList(t *testing.T) {
	l := NewRelaySetList("pk1", "dm-inbox", []string{"wss://r1", "", "wss://r2"})
	if l.Kind != KindRelaySet {
		t.Errorf("kind: got %d, want %d", l.Kind, KindRelaySet)
	}
	if l.DTag != "dm-inbox" {
		t.Errorf("dtag: got %q", l.DTag)
	}
	if l.PubKey != "pk1" {
		t.Errorf("pubkey: got %q", l.PubKey)
	}
	// Empty relay should be filtered out
	if len(l.Entries) != 2 {
		t.Fatalf("expected 2 entries (empty filtered), got %d", len(l.Entries))
	}
	for _, e := range l.Entries {
		if e.Tag != "r" {
			t.Errorf("entry tag: got %q, want r", e.Tag)
		}
	}
}

// ─── ListStore ────────────────────────────────────────────────────────────────

func TestListStore_SetGet(t *testing.T) {
	s := NewListStore()
	l := &List{Kind: KindPeopleList, DTag: "friends", PubKey: "pk1", Title: "Friends"}
	s.Set(l)

	got, ok := s.Get("pk1", KindPeopleList, "friends")
	if !ok {
		t.Fatal("expected to find list")
	}
	if got.Title != "Friends" {
		t.Errorf("title: got %q", got.Title)
	}
}

func TestListStore_GetNotFound(t *testing.T) {
	s := NewListStore()
	_, ok := s.Get("pk1", KindMuteList, "")
	if ok {
		t.Error("expected not found")
	}
}

func TestListStore_SetOverwrite(t *testing.T) {
	s := NewListStore()
	s.Set(&List{Kind: KindPeopleList, DTag: "x", PubKey: "pk1", Title: "V1"})
	s.Set(&List{Kind: KindPeopleList, DTag: "x", PubKey: "pk1", Title: "V2"})
	got, _ := s.Get("pk1", KindPeopleList, "x")
	if got.Title != "V2" {
		t.Errorf("expected V2, got %q", got.Title)
	}
}

func TestListStore_IsMuted(t *testing.T) {
	s := NewListStore()
	s.Set(&List{
		Kind:   KindMuteList,
		PubKey: "owner",
		Entries: []ListEntry{
			{Tag: "p", Value: "muted1"},
			{Tag: "p", Value: "muted2"},
		},
	})

	if !s.IsMuted("owner", "muted1") {
		t.Error("muted1 should be muted")
	}
	if s.IsMuted("owner", "other") {
		t.Error("other should not be muted")
	}
	// No mute list for another owner
	if s.IsMuted("nobody", "muted1") {
		t.Error("should not be muted when owner has no list")
	}
}

func TestListStore_IsBlocked(t *testing.T) {
	s := NewListStore()
	s.Set(&List{
		Kind:   KindPeopleList,
		DTag:   "blocklist",
		PubKey: "owner",
		Entries: []ListEntry{
			{Tag: "p", Value: "blocked1"},
		},
	})

	if !s.IsBlocked("owner", "blocked1") {
		t.Error("blocked1 should be blocked")
	}
	if s.IsBlocked("owner", "other") {
		t.Error("other should not be blocked")
	}
}

func TestListStore_IsAllowed(t *testing.T) {
	s := NewListStore()

	// No allowlist = allow all
	if !s.IsAllowed("owner", "anyone") {
		t.Error("should allow when no allowlist exists")
	}

	// Set allowlist with specific pubkeys
	s.Set(&List{
		Kind:   KindPeopleList,
		DTag:   "allowlist",
		PubKey: "owner",
		Entries: []ListEntry{
			{Tag: "p", Value: "allowed1"},
		},
	})

	if !s.IsAllowed("owner", "allowed1") {
		t.Error("allowed1 should be allowed")
	}
	if s.IsAllowed("owner", "other") {
		t.Error("other should not be allowed when allowlist exists")
	}
}

// ─── DecodeEvent ──────────────────────────────────────────────────────────────

func makeTestEvent(kind int, tags [][]string, content string) nostr.Event {
	ev := nostr.Event{
		Kind:      nostr.Kind(kind),
		Content:   content,
		CreatedAt: nostr.Timestamp(1700000000),
	}
	for _, tag := range tags {
		ev.Tags = append(ev.Tags, nostr.Tag(tag))
	}
	return ev
}

func TestDecodeEvent_MuteList(t *testing.T) {
	ev := makeTestEvent(KindMuteList, [][]string{
		{"p", "pk1", "wss://relay1"},
		{"p", "pk2"},
	}, "")

	l := DecodeEvent(ev)
	if l.Kind != KindMuteList {
		t.Errorf("kind: %d", l.Kind)
	}
	if len(l.Entries) != 2 {
		t.Fatalf("entries: %d", len(l.Entries))
	}
	if l.Entries[0].Value != "pk1" || l.Entries[0].Relay != "wss://relay1" {
		t.Errorf("entry[0]: %+v", l.Entries[0])
	}
	if l.Entries[1].Value != "pk2" || l.Entries[1].Relay != "" {
		t.Errorf("entry[1]: %+v", l.Entries[1])
	}
}

func TestDecodeEvent_PeopleListWithDTag(t *testing.T) {
	ev := makeTestEvent(KindPeopleList, [][]string{
		{"d", "friends"},
		{"title", "My Friends"},
		{"p", "pk1", "wss://r", "Alice"},
	}, "")

	l := DecodeEvent(ev)
	if l.DTag != "friends" {
		t.Errorf("dtag: %q", l.DTag)
	}
	if l.Title != "My Friends" {
		t.Errorf("title: %q", l.Title)
	}
	if len(l.Entries) != 1 {
		t.Fatalf("entries: %d", len(l.Entries))
	}
	e := l.Entries[0]
	if e.Tag != "p" || e.Value != "pk1" || e.Relay != "wss://r" || e.Petname != "Alice" {
		t.Errorf("entry: %+v", e)
	}
}

func TestDecodeEvent_AllEntryTypes(t *testing.T) {
	ev := makeTestEvent(KindBookmarkSet, [][]string{
		{"d", "bookmarks"},
		{"p", "pk1"},
		{"e", "evid1", "wss://r"},
		{"t", "nostr"},
		{"a", "30023:pk1:article1"},
		{"r", "wss://relay.example"},
	}, "")

	l := DecodeEvent(ev)
	if len(l.Entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(l.Entries))
	}
	expectedTags := []string{"p", "e", "t", "a", "r"}
	for i, tag := range expectedTags {
		if l.Entries[i].Tag != tag {
			t.Errorf("entry[%d] tag: got %q, want %q", i, l.Entries[i].Tag, tag)
		}
	}
}

func TestDecodeEvent_EmptyTags(t *testing.T) {
	ev := makeTestEvent(KindMuteList, [][]string{
		{}, // empty tag, should be skipped
		{"p", "pk1"},
	}, "")
	l := DecodeEvent(ev)
	if len(l.Entries) != 1 {
		t.Errorf("expected 1 entry (empty skipped), got %d", len(l.Entries))
	}
}

func TestDecodeEvent_UnknownTagIgnored(t *testing.T) {
	ev := makeTestEvent(KindMuteList, [][]string{
		{"x", "unknown"},
		{"p", "pk1"},
	}, "")
	l := DecodeEvent(ev)
	if len(l.Entries) != 1 {
		t.Errorf("expected 1 entry (unknown tag ignored), got %d", len(l.Entries))
	}
}

// ─── MarshalList ──────────────────────────────────────────────────────────────

func TestMarshalList_RoundTrip(t *testing.T) {
	l := &List{
		Kind:      KindPeopleList,
		DTag:      "friends",
		PubKey:    "pk1",
		Title:     "Friends",
		CreatedAt: 1700000000,
		EventID:   "eid1",
		Entries: []ListEntry{
			{Tag: "p", Value: "pk2", Relay: "wss://r", Petname: "Bob"},
			{Tag: "r", Value: "wss://relay.example"},
		},
	}
	s, err := MarshalList(l)
	if err != nil {
		t.Fatal(err)
	}
	// Verify it's valid JSON
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, s)
	}
	if raw["kind"].(float64) != float64(KindPeopleList) {
		t.Errorf("kind mismatch in JSON")
	}
	entries := raw["entries"].([]interface{})
	if len(entries) != 2 {
		t.Errorf("entries length: %d", len(entries))
	}
}

func TestMarshalList_NilEntries(t *testing.T) {
	l := &List{Kind: KindMuteList, PubKey: "pk1"}
	s, err := MarshalList(l)
	if err != nil {
		t.Fatal(err)
	}
	// Should still be valid JSON with null or empty entries
	if !strings.Contains(s, `"entries"`) {
		t.Error("expected entries field in JSON")
	}
}

// ─── listKey ──────────────────────────────────────────────────────────────────

func TestListKey_CaseInsensitive(t *testing.T) {
	k1 := listKey("AABB", 10000, "")
	k2 := listKey("aabb", 10000, "")
	if k1 != k2 {
		t.Errorf("keys should be case-insensitive: %q vs %q", k1, k2)
	}
}

func TestListKey_DifferentParams(t *testing.T) {
	k1 := listKey("pk1", 30000, "friends")
	k2 := listKey("pk1", 30000, "enemies")
	if k1 == k2 {
		t.Error("different dtag should produce different keys")
	}
	k3 := listKey("pk1", 10000, "")
	if k1 == k3 {
		t.Error("different kind should produce different keys")
	}
}

// ─── Constants ────────────────────────────────────────────────────────────────

func TestKindConstants(t *testing.T) {
	if KindMuteList != 10000 {
		t.Errorf("KindMuteList: %d", KindMuteList)
	}
	if KindPinList != 10001 {
		t.Errorf("KindPinList: %d", KindPinList)
	}
	if KindRelaySet != 30002 {
		t.Errorf("KindRelaySet: %d", KindRelaySet)
	}
}

func TestRelaySetDTagConstants(t *testing.T) {
	dtags := []string{
		RelaySetDMInbox, RelaySetNIP29, RelaySetChat,
		RelaySetNIP28, RelaySetSearch, RelaySetDVM, RelaySetGrasp,
	}
	seen := make(map[string]bool)
	for _, d := range dtags {
		if d == "" {
			t.Error("relay set d-tag is empty")
		}
		if seen[d] {
			t.Errorf("duplicate d-tag: %q", d)
		}
		seen[d] = true
	}
}
