// Package nip51 implements NIP-51 lists.
//
// NIP-51 defines several list kinds for grouping Nostr entities:
//
//	Kind 10000 – Mute list          (replaces follows who are muted)
//	Kind 10001 – Pin list           (pinned notes)
//	Kind 10002 – Relay list metadata (see also NIP-65)
//	Kind 30000 – Follow sets (replaceable, d-tag = list name)
//	Kind 30002 – Relay sets
//	Kind 30003 – Bookmark sets (private entries via NIP-44 encrypted content)
//	Kind 30001 – Deprecated legacy sets
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

// Kind constants for current NIP-51 standard lists and sets.
const (
	KindFollowList       = 3
	KindMuteList         = 10000
	KindPinList          = 10001
	KindRelayList        = 10002
	KindBookmarks        = 10003
	KindCommunities      = 10004
	KindPublicChats      = 10005
	KindBlockedRelays    = 10006
	KindSearchRelays     = 10007
	KindProfileBadges    = 10008
	KindSimpleGroups     = 10009
	KindRelayFeeds       = 10012
	KindPrivateRelays    = 10013
	KindInterests        = 10015
	KindGitAuthors       = 10017
	KindGitRepositories  = 10018
	KindMediaFollows     = 10020
	KindEmojis           = 10030
	KindDMRelays         = 10050
	KindFavoritePodcasts = 10054
	KindBlossomServers   = 10063
	KindAuthoredPodcasts = 10064
	KindGoodWikiAuthors  = 10101
	KindGoodWikiRelays   = 10102

	KindPeopleList       = 30000 // Alias for follow sets / categorized people lists.
	KindFollowSet        = 30000
	KindDeprecatedSet    = 30001
	KindRelaySet         = 30002
	KindBookmarkSet      = 30003
	KindCurationSet      = 30004 // Article curation.
	KindVideoCuration    = 30005
	KindPictureCuration  = 30006
	KindKindMuteSet      = 30007
	KindBadgeSet         = 30008
	KindInterestSet      = 30015
	KindEmojiSet         = 30030
	KindReleaseSet       = 30063
	KindAppCurationSet   = 30267
	KindCalendarSet      = 31924
	KindStarterPack      = 39089
	KindMediaStarterPack = 39092

	KindBlockList = 30000 // Alias: use d-tag "blocklist" for blocking.
	KindAllowList = 30000 // Alias: use d-tag "allowlist" for allowing.
)

// Well-known d-tag identifiers for relay sets (kind 30002).
// Each identifies a purpose-specific relay list published by the agent.
const (
	RelaySetDMInbox = "dm-inbox"      // NIP-17 DM inbox relays (mirrors kind:10050)
	RelaySetNIP29   = "nip29-relays"  // NIP-29 relay-managed group relays
	RelaySetChat    = "chat-relays"   // NIP-C7 kind:9 chat relays
	RelaySetNIP28   = "nip28-relays"  // NIP-28 public channel relays
	RelaySetSearch  = "search-relays" // NIP-50 search relays
	RelaySetDVM     = "dvm-relays"    // NIP-90 DVM relays
	RelaySetGrasp   = "grasp-servers" // Grasp protocol server endpoints
)

// ListEntry is a single entry in a NIP-51 list.
type ListEntry struct {
	Tag     string   // tag type, such as p, e, t, a, relay, word, group, emoji, server, or url
	Value   string   // the main value
	Relay   string   // optional relay hint (for p/e compatibility)
	Petname string   // optional petname (for p compatibility)
	Extra   []string // remaining wire values, preserved for generic list items
}

// List represents a decoded NIP-51 list event.
type List struct {
	Kind           int
	DTag           string // for replaceable kinds (30000, 30001)
	PubKey         string
	Title          string
	Image          string
	Description    string
	Entries        []ListEntry // public tag items
	PrivateEntries []ListEntry // NIP-44 items from encrypted content
	CreatedAt      int64
	EventID        string
}

// RelaysFromList extracts current relay tags and legacy kind-30002 r tags.
func RelaysFromList(list *List) []string {
	var out []string
	for _, e := range list.AllEntries() {
		if (e.Tag == "relay" || e.Tag == "r") && e.Value != "" {
			out = append(out, e.Value)
		}
	}
	return out
}

// AllEntries returns public items followed by private items in chronological order.
func (list *List) AllEntries() []ListEntry {
	if list == nil {
		return nil
	}
	out := make([]ListEntry, 0, len(list.Entries)+len(list.PrivateEntries))
	out = append(out, list.Entries...)
	out = append(out, list.PrivateEntries...)
	return out
}

// NewRelaySetList creates a kind:30002 relay set list with the given d-tag and relay URLs.
func NewRelaySetList(pubkey, dtag string, relays []string) *List {
	entries := make([]ListEntry, 0, len(relays))
	for _, r := range relays {
		if r != "" {
			entries = append(entries, ListEntry{Tag: "relay", Value: r})
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
	for _, e := range l.AllEntries() {
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
	for _, e := range l.AllEntries() {
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
	for _, e := range l.AllEntries() {
		if e.Tag == "p" && e.Value == targetPubkey {
			return true
		}
	}
	return false
}

func validationFailure(ev nostr.Event, expectedAuthor nostr.PubKey, expectedKind int) string {
	if ev.Kind != nostr.Kind(expectedKind) {
		return fmt.Sprintf("unexpected_kind:%d", ev.Kind)
	}
	if ev.PubKey != expectedAuthor {
		return "unexpected_author"
	}
	if !ev.CheckID() {
		return "invalid_id"
	}
	if !ev.VerifySignature() {
		return "invalid_signature"
	}
	if ev.CreatedAt > nostr.Now()+nostr.Timestamp(600) {
		return "created_at_future"
	}
	return ""
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
		if reason := validationFailure(re.Event, pk, kind); reason != "" {
			log.Printf("nip51.Fetch: dropped invalid event reason=%s", reason)
			continue
		}
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
		case "image":
			if len(tag) >= 2 {
				l.Image = tag[1]
			}
		case "description":
			if len(tag) >= 2 {
				l.Description = tag[1]
			}
		default:
			if !isNIP51ItemTag(tag[0]) || len(tag) < 2 {
				continue
			}
			entry := listEntryFromTag(tag)
			// Pre-parity swarmstr emitted r for relay sets. Normalize it on
			// read while preserving r for kind-10009 simple-group metadata.
			if ev.Kind == nostr.Kind(KindRelaySet) && entry.Tag == "r" {
				entry.Tag = "relay"
			}
			l.Entries = append(l.Entries, entry)
		}
	}
	return l
}

// DecodeEventWithPrivate decrypts self-encrypted NIP-44 private list items.
func DecodeEventWithPrivate(ctx context.Context, keyer nostr.Keyer, ev nostr.Event) (*List, error) {
	list := DecodeEvent(ev)
	if strings.TrimSpace(ev.Content) == "" {
		return list, nil
	}
	if keyer == nil {
		return nil, fmt.Errorf("nip51: keyer required for private items")
	}
	plaintext, err := keyer.Decrypt(ctx, ev.Content, ev.PubKey)
	if err != nil {
		return nil, fmt.Errorf("nip51: decrypt private items: %w", err)
	}
	var tags nostr.Tags
	if err := json.Unmarshal([]byte(plaintext), &tags); err != nil {
		return nil, fmt.Errorf("nip51: decode private items: %w", err)
	}
	for _, tag := range tags {
		if len(tag) < 2 || !isNIP51ItemTag(tag[0]) {
			return nil, fmt.Errorf("nip51: invalid private item %v", tag)
		}
		entry := listEntryFromTag(tag)
		if ev.Kind == nostr.Kind(KindRelaySet) && entry.Tag == "r" {
			entry.Tag = "relay"
		}
		list.PrivateEntries = append(list.PrivateEntries, entry)
	}
	return list, nil
}

func isNIP51ItemTag(name string) bool {
	switch name {
	case "p", "e", "t", "a", "r", "relay", "word", "group", "emoji", "server", "url":
		return true
	default:
		return false
	}
}

func listEntryFromTag(tag nostr.Tag) ListEntry {
	entry := ListEntry{Tag: tag[0], Value: tag[1]}
	if len(tag) > 2 {
		entry.Extra = append([]string(nil), tag[2:]...)
		entry.Relay = tag[2]
	}
	if len(tag) > 3 && tag[0] == "p" {
		entry.Petname = tag[3]
	}
	return entry
}

func listEntryTag(entry ListEntry, kind int) (nostr.Tag, error) {
	name := strings.TrimSpace(entry.Tag)
	if kind == KindRelaySet && name == "r" {
		name = "relay"
	}
	if !isNIP51ItemTag(name) || strings.TrimSpace(entry.Value) == "" {
		return nil, fmt.Errorf("invalid list entry %q=%q", name, entry.Value)
	}
	tag := nostr.Tag{name, entry.Value}
	if len(entry.Extra) > 0 {
		return append(tag, entry.Extra...), nil
	}
	if entry.Relay != "" {
		tag = append(tag, entry.Relay)
	}
	if name == "p" && entry.Petname != "" {
		if entry.Relay == "" {
			tag = append(tag, "")
		}
		tag = append(tag, entry.Petname)
	}
	return tag, nil
}

// BuildListEvent constructs an unsigned NIP-51 event. PrivateEntries are
// serialized as a JSON tag array and NIP-44 encrypted to the author's own key.
func BuildListEvent(ctx context.Context, keyer nostr.Keyer, list *List) (nostr.Event, error) {
	if list == nil {
		return nostr.Event{}, fmt.Errorf("nip51: list is required")
	}
	if keyer == nil {
		return nostr.Event{}, fmt.Errorf("nip51: keyer is required")
	}
	if list.Kind >= 30000 && list.Kind < 40000 && strings.TrimSpace(list.DTag) == "" {
		return nostr.Event{}, fmt.Errorf("nip51: d tag is required for set kind %d", list.Kind)
	}
	pubkey, err := keyer.GetPublicKey(ctx)
	if err != nil {
		return nostr.Event{}, fmt.Errorf("nip51: get public key: %w", err)
	}
	tags := nostr.Tags{}
	if list.DTag != "" {
		tags = append(tags, nostr.Tag{"d", list.DTag})
	}
	for _, metadata := range []struct{ name, value string }{
		{"title", list.Title}, {"image", list.Image}, {"description", list.Description},
	} {
		if metadata.value != "" {
			tags = append(tags, nostr.Tag{metadata.name, metadata.value})
		}
	}
	for _, entry := range list.Entries {
		tag, err := listEntryTag(entry, list.Kind)
		if err != nil {
			return nostr.Event{}, fmt.Errorf("nip51: public item: %w", err)
		}
		tags = append(tags, tag)
	}

	content := ""
	if len(list.PrivateEntries) > 0 {
		privateTags := make(nostr.Tags, 0, len(list.PrivateEntries))
		for _, entry := range list.PrivateEntries {
			tag, err := listEntryTag(entry, list.Kind)
			if err != nil {
				return nostr.Event{}, fmt.Errorf("nip51: private item: %w", err)
			}
			privateTags = append(privateTags, tag)
		}
		plaintext, err := json.Marshal(privateTags)
		if err != nil {
			return nostr.Event{}, fmt.Errorf("nip51: encode private items: %w", err)
		}
		content, err = keyer.Encrypt(ctx, string(plaintext), pubkey)
		if err != nil {
			return nostr.Event{}, fmt.Errorf("nip51: encrypt private items: %w", err)
		}
	}
	return nostr.Event{
		Kind: nostr.Kind(list.Kind), PubKey: pubkey, CreatedAt: nostr.Now(), Tags: tags, Content: content,
	}, nil
}

// Publish creates or replaces a NIP-51 list event on the given relays.
func Publish(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, list *List) (string, error) {
	evt, err := BuildListEvent(ctx, keyer, list)
	if err != nil {
		return "", err
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
		allowedAuthors := make(map[nostr.PubKey]struct{}, len(authors))
		for _, author := range authors {
			allowedAuthors[author] = struct{}{}
		}
		allowedKinds := make(map[nostr.Kind]struct{}, len(nostrKinds))
		for _, kind := range nostrKinds {
			allowedKinds[kind] = struct{}{}
		}
		for re := range pool.SubscribeMany(ctx, relays, filter, nostr.SubscriptionOptions{}) {
			if _, ok := allowedAuthors[re.Event.PubKey]; !ok {
				continue
			}
			if _, ok := allowedKinds[re.Event.Kind]; !ok {
				continue
			}
			if reason := validationFailure(re.Event, re.Event.PubKey, int(re.Event.Kind)); reason != "" {
				continue
			}
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
