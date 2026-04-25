// Package nip51 implements NIP-51 lists.
//
// NIP-51 defines several list kinds for grouping Nostr entities:
//
//   Kind 10000 – Mute list          (replaces follows who are muted)
//   Kind 10001 – Pin list           (pinned notes)
//   Kind 10002 – Relay list metadata (see also NIP-65)
//   Kind 30000 – Categorized people list (replaceable, d-tag = list name)
//   Kind 30001 – Categorized bookmark set
//   Kind 30003 – Bookmark set (private bookmarks via NIP-04 encrypted content)
//
// This package focuses on the kinds most relevant to agent allow/block flows.
package nip51

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
)

// Kind constants for NIP-51 list events.
const (
	KindMuteList       = 10000 // Muted pubkeys
	KindPinList        = 10001 // Pinned note IDs
	KindPeopleList     = 30000 // Categorized people list (replaceable, d-tag)
	KindBookmarkSet    = 30001 // Categorized bookmark set
	KindRelaySet       = 30002 // Categorized relay set (replaceable, d-tag)
	KindBlockList      = 30000 // Alias: use d-tag "blocklist" for blocking
	KindAllowList      = 30000 // Alias: use d-tag "allowlist" for allowing
)

// Well-known d-tag identifiers for relay sets (kind 30002).
// Each identifies a purpose-specific relay list published by the agent.
const (
	RelaySetDMInbox   = "dm-inbox"       // NIP-17 DM inbox relays (mirrors kind:10050)
	RelaySetNIP29     = "nip29-relays"   // NIP-29 relay-managed group relays
	RelaySetChat      = "chat-relays"    // NIP-C7 kind:9 chat relays
	RelaySetNIP28     = "nip28-relays"   // NIP-28 public channel relays
	RelaySetSearch    = "search-relays"  // NIP-50 search relays
	RelaySetDVM       = "dvm-relays"     // NIP-90 DVM relays
	RelaySetGrasp     = "grasp-servers"  // Grasp protocol server endpoints
)

// ListEntry is a single entry in a NIP-51 list.
type ListEntry struct {
	Tag      string // tag type: "p" (pubkey), "e" (event id), "t" (hashtag), "a" (naddr), "r" (relay)
	Value    string // the main value
	Relay    string // optional relay hint (for "p" and "e" tags)
	Petname  string // optional petname (for "p" tags)
}

// List represents a decoded NIP-51 list event.
type List struct {
	Kind      int
	DTag      string // for replaceable kinds (30000, 30001)
	PubKey    string
	Title     string
	Entries   []ListEntry
	CreatedAt int64
	EventID   string
}

// RelaysFromList extracts relay URLs from "r" tag entries in a list.
// This is the standard way to read relay sets (kind 30002).
func RelaysFromList(list *List) []string {
	var out []string
	for _, e := range list.Entries {
		if e.Tag == "r" && e.Value != "" {
			out = append(out, e.Value)
		}
	}
	return out
}

// NewRelaySetList creates a kind:30002 relay set list with the given d-tag and relay URLs.
func NewRelaySetList(pubkey, dtag string, relays []string) *List {
	entries := make([]ListEntry, 0, len(relays))
	for _, r := range relays {
		if r != "" {
			entries = append(entries, ListEntry{Tag: "r", Value: r})
		}
	}
	return &List{
		Kind:    KindRelaySet,
		DTag:    dtag,
		PubKey:  pubkey,
		Entries: entries,
	}
}

// ListStore is an in-process cache of fetched NIP-51 lists.
type ListStore struct {
	mu    sync.RWMutex
	lists map[string]*List // key: "<pubkey>:<kind>:<dtag>"
}

// NewListStore creates a new empty ListStore.
func NewListStore() *ListStore {
	return &ListStore{lists: make(map[string]*List)}
}

// Set upserts a list into the store.
func (s *ListStore) Set(l *List) {
	key := listKey(l.PubKey, l.Kind, l.DTag)
	s.mu.Lock()
	s.lists[key] = l
	s.mu.Unlock()
}

// Get retrieves a list from the store.
func (s *ListStore) Get(pubkey string, kind int, dtag string) (*List, bool) {
	key := listKey(pubkey, kind, dtag)
	s.mu.RLock()
	l, ok := s.lists[key]
	s.mu.RUnlock()
	return l, ok
}

// IsMuted returns true if the given pubkey appears in the mute list.
func (s *ListStore) IsMuted(ownerPubkey, targetPubkey string) bool {
	l, ok := s.Get(ownerPubkey, KindMuteList, "")
	if !ok {
		return false
	}
	for _, e := range l.Entries {
		if e.Tag == "p" && e.Value == targetPubkey {
			return true
		}
	}
	return false
}

// IsBlocked returns true if targetPubkey is in owner's blocklist (d-tag "blocklist").
func (s *ListStore) IsBlocked(ownerPubkey, targetPubkey string) bool {
	l, ok := s.Get(ownerPubkey, KindPeopleList, "blocklist")
	if !ok {
		return false
	}
	for _, e := range l.Entries {
		if e.Tag == "p" && e.Value == targetPubkey {
			return true
		}
	}
	return false
}

// IsAllowed returns true if targetPubkey is in owner's allowlist (d-tag "allowlist").
// Returns true if no allowlist exists (open by default).
func (s *ListStore) IsAllowed(ownerPubkey, targetPubkey string) bool {
	l, ok := s.Get(ownerPubkey, KindPeopleList, "allowlist")
	if !ok {
		return true // no allowlist = allow all
	}
	for _, e := range l.Entries {
		if e.Tag == "p" && e.Value == targetPubkey {
			return true
		}
	}
	return false
}

// Fetch retrieves a NIP-51 list from relays and stores it.
// For kind 10000 (mute list) set dtag = "".
func Fetch(ctx context.Context, pool *nostr.Pool, relays []string, pubkey string, kind int, dtag string) (*List, error) {
	filter := nostr.Filter{
		Kinds:   []nostr.Kind{nostr.Kind(kind)},
		Authors: []nostr.PubKey{},
		Limit:   1,
	}
	pk, err := nostr.PubKeyFromHex(pubkey)
	if err != nil {
		return nil, fmt.Errorf("nip51: invalid pubkey: %w", err)
	}
	filter.Authors = append(filter.Authors, pk)
	if dtag != "" {
		filter.Tags = nostr.TagMap{"d": []string{dtag}}
	}

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var best *nostr.Event
	for re := range pool.FetchMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
		if best == nil || re.Event.CreatedAt > best.CreatedAt {
			ev := re.Event
			best = &ev
		}
	}
	if best == nil {
		return nil, fmt.Errorf("nip51: list not found (kind=%d dtag=%q pubkey=%s)", kind, dtag, pubkey)
	}

	return DecodeEvent(*best), nil
}

// DecodeEvent parses a NIP-51 event into a List struct.
func DecodeEvent(ev nostr.Event) *List {
	l := &List{
		Kind:      int(ev.Kind),
		PubKey:    ev.PubKey.Hex(),
		CreatedAt: int64(ev.CreatedAt),
		EventID:   ev.ID.Hex(),
	}
	for _, tag := range ev.Tags {
		if len(tag) == 0 {
			continue
		}
		switch tag[0] {
		case "d":
			if len(tag) >= 2 {
				l.DTag = tag[1]
			}
		case "title":
			if len(tag) >= 2 {
				l.Title = tag[1]
			}
		case "p", "e", "t", "a", "r":
			entry := ListEntry{Tag: tag[0]}
			if len(tag) >= 2 {
				entry.Value = tag[1]
			}
			if len(tag) >= 3 {
				entry.Relay = tag[2]
			}
			if len(tag) >= 4 && tag[0] == "p" {
				entry.Petname = tag[3]
			}
			l.Entries = append(l.Entries, entry)
		}
	}
	return l
}

// Publish creates or replaces a NIP-51 list event on the given relays.
func Publish(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, list *List) (string, error) {
	tags := nostr.Tags{}
	if list.DTag != "" {
		tags = append(tags, nostr.Tag{"d", list.DTag})
	}
	if list.Title != "" {
		tags = append(tags, nostr.Tag{"title", list.Title})
	}
	for _, e := range list.Entries {
		tag := nostr.Tag{e.Tag, e.Value}
		if e.Relay != "" {
			tag = append(tag, e.Relay)
		}
		if e.Tag == "p" && e.Petname != "" {
			if e.Relay == "" {
				tag = append(tag, "")
			}
			tag = append(tag, e.Petname)
		}
		tags = append(tags, tag)
	}

	evt := nostr.Event{
		Kind:      nostr.Kind(list.Kind),
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   "",
	}

	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("nip51: sign event: %w", err)
	}

	// Use explicit timeout to properly wait for OK responses.
	// The nostr library defaults to 7s if no deadline is set.
	pubCtx, pubCancel := context.WithTimeout(ctx, 30*time.Second)
	defer pubCancel()

	published := false
	var lastErr error
	for result := range pool.PublishMany(pubCtx, relays, evt) {
		if result.Error == nil {
			published = true
		} else {
			lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
		}
	}
	if !published {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relay accepted publish")
		}
		return "", lastErr
	}
	return evt.ID.Hex(), nil
}

// Subscribe watches for list updates and keeps the store current.
// It fetches existing lists and subscribes to replaceable updates.
func Subscribe(ctx context.Context, pool *nostr.Pool, store *ListStore, relays []string, pubkeys []string, kinds []int) {
	if len(pubkeys) == 0 || len(relays) == 0 {
		return
	}

	authors := make([]nostr.PubKey, 0, len(pubkeys))
	for _, pk := range pubkeys {
		p, err := nostr.PubKeyFromHex(pk)
		if err != nil {
			log.Printf("nip51.Subscribe: invalid pubkey %s: %v", pk, err)
			continue
		}
		authors = append(authors, p)
	}
	if len(authors) == 0 {
		return
	}

	nostrKinds := make([]nostr.Kind, len(kinds))
	for i, k := range kinds {
		nostrKinds[i] = nostr.Kind(k)
	}

	filter := nostr.Filter{
		Kinds:   nostrKinds,
		Authors: authors,
	}

	go func() {
		for re := range pool.SubscribeMany(ctx, relays, filter, nostr.SubscriptionOptions{}) {
			l := DecodeEvent(re.Event)
			store.Set(l)
		}
	}()
}

// AddEntry adds an entry to a list (loading from relays first if needed).
func AddEntry(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string,
	pubkey string, kind int, dtag string, entry ListEntry) (string, error) {

	existing, err := Fetch(ctx, pool, relays, pubkey, kind, dtag)
	if err != nil {
		// List doesn't exist yet — start fresh.
		existing = &List{Kind: kind, DTag: dtag, PubKey: pubkey}
	}

	// Deduplicate: don't add if already present.
	for _, e := range existing.Entries {
		if e.Tag == entry.Tag && e.Value == entry.Value {
			return existing.EventID, nil // already in list
		}
	}

	existing.Entries = append(existing.Entries, entry)
	return Publish(ctx, pool, keyer, relays, existing)
}

// RemoveEntry removes an entry from a list.
func RemoveEntry(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string,
	pubkey string, kind int, dtag string, entryTag, entryValue string) (string, error) {

	existing, err := Fetch(ctx, pool, relays, pubkey, kind, dtag)
	if err != nil {
		return "", fmt.Errorf("nip51: list not found: %w", err)
	}

	filtered := existing.Entries[:0]
	for _, e := range existing.Entries {
		if e.Tag == entryTag && e.Value == entryValue {
			continue
		}
		filtered = append(filtered, e)
	}
	existing.Entries = filtered
	return Publish(ctx, pool, keyer, relays, existing)
}

// MarshalList serializes a List to JSON.
func MarshalList(l *List) (string, error) {
	type jsonEntry struct {
		Tag     string `json:"tag"`
		Value   string `json:"value"`
		Relay   string `json:"relay,omitempty"`
		Petname string `json:"petname,omitempty"`
	}
	type jsonList struct {
		Kind      int         `json:"kind"`
		DTag      string      `json:"d_tag,omitempty"`
		PubKey    string      `json:"pubkey"`
		Title     string      `json:"title,omitempty"`
		Entries   []jsonEntry `json:"entries"`
		CreatedAt int64       `json:"created_at"`
		EventID   string      `json:"event_id"`
	}
	jl := jsonList{
		Kind:      l.Kind,
		DTag:      l.DTag,
		PubKey:    l.PubKey,
		Title:     l.Title,
		CreatedAt: l.CreatedAt,
		EventID:   l.EventID,
	}
	for _, e := range l.Entries {
		jl.Entries = append(jl.Entries, jsonEntry{Tag: e.Tag, Value: e.Value, Relay: e.Relay, Petname: e.Petname})
	}
	b, err := json.MarshalIndent(jl, "", "  ")
	return string(b), err
}

// listKey returns a unique cache key for a list.
func listKey(pubkey string, kind int, dtag string) string {
	return fmt.Sprintf("%s:%d:%s", strings.ToLower(pubkey), kind, dtag)
}
