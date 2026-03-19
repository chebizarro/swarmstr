// Package runtime – NIP-65 relay selection protocol.
//
// NIP-65 defines kind:10002 relay list metadata events. Each relay URL is tagged
// with "read", "write", or both (no marker = both). This file implements:
//
//   - RelaySelector: fetches and caches NIP-65 relay lists for pubkeys
//   - Outbox model relay selection: when writing TO someone, use their read relays;
//     when reading FROM someone, use their write relays
//   - Self relay list publication and subscription (bidirectional sync)
//   - Startup publishing of the agent's own NIP-65 relay list
package runtime

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
)

// NIP65RelayEntry represents a single relay in a kind:10002 event.
type NIP65RelayEntry struct {
	URL   string
	Read  bool
	Write bool
}

// NIP65RelayList is a decoded kind:10002 relay list metadata event.
type NIP65RelayList struct {
	PubKey    string
	Entries   []NIP65RelayEntry
	CreatedAt int64
	EventID   string
}

// ReadRelays returns only relays marked for reading.
func (l *NIP65RelayList) ReadRelays() []string {
	out := make([]string, 0, len(l.Entries))
	for _, e := range l.Entries {
		if e.Read {
			out = append(out, e.URL)
		}
	}
	return out
}

// WriteRelays returns only relays marked for writing.
func (l *NIP65RelayList) WriteRelays() []string {
	out := make([]string, 0, len(l.Entries))
	for _, e := range l.Entries {
		if e.Write {
			out = append(out, e.URL)
		}
	}
	return out
}

// AllRelays returns all unique relay URLs regardless of read/write designation.
func (l *NIP65RelayList) AllRelays() []string {
	seen := make(map[string]struct{}, len(l.Entries))
	out := make([]string, 0, len(l.Entries))
	for _, e := range l.Entries {
		if _, ok := seen[e.URL]; !ok {
			seen[e.URL] = struct{}{}
			out = append(out, e.URL)
		}
	}
	return out
}

// ─── Relay Selector ──────────────────────────────────────────────────────────

type relaySelectorEntry struct {
	list      *NIP65RelayList
	fetchedAt time.Time
}

// RelaySelector fetches, caches, and applies NIP-65 relay lists for outbox model
// relay selection. It is the central component for Nostr-native relay routing.
type RelaySelector struct {
	mu       sync.RWMutex
	cache    map[string]*relaySelectorEntry // keyed by hex pubkey
	cacheTTL time.Duration

	// fallbackRead and fallbackWrite are used when a pubkey has no NIP-65 list.
	fallbackRead  []string
	fallbackWrite []string
	fallbackMu    sync.RWMutex
}

// NewRelaySelector creates a new RelaySelector with the given fallback relays.
func NewRelaySelector(fallbackRead, fallbackWrite []string) *RelaySelector {
	return &RelaySelector{
		cache:         make(map[string]*relaySelectorEntry),
		cacheTTL:      30 * time.Minute,
		fallbackRead:  append([]string{}, fallbackRead...),
		fallbackWrite: append([]string{}, fallbackWrite...),
	}
}

// SetFallbacks updates the fallback relay lists (called when local config changes).
func (s *RelaySelector) SetFallbacks(read, write []string) {
	s.fallbackMu.Lock()
	s.fallbackRead = append([]string{}, read...)
	s.fallbackWrite = append([]string{}, write...)
	s.fallbackMu.Unlock()
}

// FallbackRead returns a copy of the current fallback read relays.
func (s *RelaySelector) FallbackRead() []string {
	s.fallbackMu.RLock()
	defer s.fallbackMu.RUnlock()
	out := make([]string, len(s.fallbackRead))
	copy(out, s.fallbackRead)
	return out
}

// FallbackWrite returns a copy of the current fallback write relays.
func (s *RelaySelector) FallbackWrite() []string {
	s.fallbackMu.RLock()
	defer s.fallbackMu.RUnlock()
	out := make([]string, len(s.fallbackWrite))
	copy(out, s.fallbackWrite)
	return out
}

// Get returns the cached NIP-65 list for a pubkey, or nil if not cached/expired.
func (s *RelaySelector) Get(pubkey string) *NIP65RelayList {
	key := strings.ToLower(pubkey)

	s.mu.RLock()
	e, ok := s.cache[key]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	if time.Since(e.fetchedAt) > s.cacheTTL {
		// Evict expired entries to avoid unbounded growth.
		s.mu.Lock()
		delete(s.cache, key)
		s.mu.Unlock()
		return nil
	}
	return e.list
}

// Put stores a NIP-65 list in the cache.
func (s *RelaySelector) Put(list *NIP65RelayList) {
	if list == nil {
		return
	}
	s.mu.Lock()
	s.cache[strings.ToLower(list.PubKey)] = &relaySelectorEntry{
		list:      list,
		fetchedAt: time.Now(),
	}
	s.mu.Unlock()
}

// Invalidate removes a pubkey from the cache.
func (s *RelaySelector) Invalidate(pubkey string) {
	s.mu.Lock()
	delete(s.cache, strings.ToLower(pubkey))
	s.mu.Unlock()
}

// FetchAndCache queries relays for a pubkey's NIP-65 list and caches the result.
func (s *RelaySelector) FetchAndCache(ctx context.Context, pool *nostr.Pool, queryRelays []string, pubkey string) (*NIP65RelayList, error) {
	list, err := FetchNIP65(ctx, pool, queryRelays, pubkey)
	if err != nil {
		return nil, err
	}
	s.Put(list)
	return list, nil
}

// ── NIP-65 relay selection semantics ─────────────────────────────────────────
//
// Spec summary:
//   - When downloading events **from** a user, clients SHOULD use the user's **write** relays.
//   - When downloading events **about** a user (mentions/tags), clients SHOULD use the user's **read** relays.
//   - When publishing an event, clients SHOULD:
//       * Send to the author's **write** relays
//       * Send to all **read** relays of each tagged user

// RelaysForPublishingAsAuthor returns relays to publish an event authored by pubkey.
// Per NIP-65: publish to the author's WRITE relays.
func (s *RelaySelector) RelaysForPublishingAsAuthor(ctx context.Context, pool *nostr.Pool, queryRelays []string, authorPubkey string) []string {
	list := s.Get(authorPubkey)
	if list == nil {
		var err error
		list, err = s.FetchAndCache(ctx, pool, queryRelays, authorPubkey)
		if err != nil || list == nil {
			return MergeRelayLists(s.FallbackRead(), s.FallbackWrite())
		}
	}
	writeRelays := list.WriteRelays()
	if len(writeRelays) == 0 {
		return MergeRelayLists(s.FallbackRead(), s.FallbackWrite())
	}
	return writeRelays
}

// RelaysForDownloadingFrom returns relays to fetch events FROM a user.
// Per NIP-65: use the user's WRITE relays.
func (s *RelaySelector) RelaysForDownloadingFrom(ctx context.Context, pool *nostr.Pool, queryRelays []string, sourcePubkey string) []string {
	list := s.Get(sourcePubkey)
	if list == nil {
		var err error
		list, err = s.FetchAndCache(ctx, pool, queryRelays, sourcePubkey)
		if err != nil || list == nil {
			return MergeRelayLists(s.FallbackRead(), s.FallbackWrite())
		}
	}
	writeRelays := list.WriteRelays()
	if len(writeRelays) == 0 {
		return MergeRelayLists(s.FallbackRead(), s.FallbackWrite())
	}
	return writeRelays
}

// RelaysForDownloadingAbout returns relays to fetch events ABOUT a user (mentions/tags).
// Per NIP-65: use the user's READ relays.
func (s *RelaySelector) RelaysForDownloadingAbout(ctx context.Context, pool *nostr.Pool, queryRelays []string, targetPubkey string) []string {
	list := s.Get(targetPubkey)
	if list == nil {
		var err error
		list, err = s.FetchAndCache(ctx, pool, queryRelays, targetPubkey)
		if err != nil || list == nil {
			return MergeRelayLists(s.FallbackRead(), s.FallbackWrite())
		}
	}
	readRelays := list.ReadRelays()
	if len(readRelays) == 0 {
		return MergeRelayLists(s.FallbackRead(), s.FallbackWrite())
	}
	return readRelays
}

// ── NIP-65 event fetch/decode/publish ────────────────────────────────────────

// FetchNIP65 fetches the latest kind:10002 relay list for a pubkey.
func FetchNIP65(ctx context.Context, pool *nostr.Pool, relays []string, pubkey string) (*NIP65RelayList, error) {
	pk, err := ParsePubKey(pubkey)
	if err != nil {
		return nil, fmt.Errorf("nip65: invalid pubkey: %w", err)
	}

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	filter := nostr.Filter{
		Kinds:   []nostr.Kind{10002},
		Authors: []nostr.PubKey{pk},
		Limit:   1,
	}

	var best *nostr.Event
	for re := range pool.SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
		ev := re.Event
		if best == nil || ev.CreatedAt > best.CreatedAt {
			cp := ev
			best = &cp
		}
	}
	if best == nil {
		return nil, fmt.Errorf("nip65: no relay list found for %s", pubkey)
	}

	return DecodeNIP65Event(*best), nil
}

// DecodeNIP65Event parses a kind:10002 event into a NIP65RelayList.
func DecodeNIP65Event(ev nostr.Event) *NIP65RelayList {
	list := &NIP65RelayList{
		PubKey:    ev.PubKey.Hex(),
		CreatedAt: int64(ev.CreatedAt),
		EventID:   ev.ID.Hex(),
	}
	for _, tag := range ev.Tags {
		if len(tag) < 2 || tag[0] != "r" {
			continue
		}
		entry := NIP65RelayEntry{URL: tag[1]}
		if len(tag) == 2 {
			// No marker = both read and write
			entry.Read = true
			entry.Write = true
		} else {
			switch tag[2] {
			case "read":
				entry.Read = true
			case "write":
				entry.Write = true
			default:
				// Unknown marker: treat as both
				entry.Read = true
				entry.Write = true
			}
		}
		list.Entries = append(list.Entries, entry)
	}
	return list
}

// PublishNIP65 publishes a kind:10002 relay list metadata event.
func PublishNIP65(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, publishRelays []string, readRelays, writeRelays, bothRelays []string) (string, error) {
	tags := nostr.Tags{}
	for _, r := range bothRelays {
		tags = append(tags, nostr.Tag{"r", r})
	}
	for _, r := range readRelays {
		tags = append(tags, nostr.Tag{"r", r, "read"})
	}
	for _, r := range writeRelays {
		tags = append(tags, nostr.Tag{"r", r, "write"})
	}

	evt := nostr.Event{
		Kind:      10002,
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   "",
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("nip65: sign event: %w", err)
	}

	published := 0
	var lastErr error
	for result := range pool.PublishMany(ctx, publishRelays, evt) {
		if result.Error == nil {
			published++
		} else {
			lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
		}
	}
	if published == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relay accepted publish")
		}
		return "", lastErr
	}
	return evt.ID.Hex(), nil
}

// PublishNIP02ContactList publishes a kind:3 contacts/follows event.
func PublishNIP02ContactList(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, publishRelays []string, contacts []NIP02Contact) (string, error) {
	tags := nostr.Tags{}
	for _, c := range contacts {
		tag := nostr.Tag{"p", c.PubKey}
		if c.Relay != "" {
			tag = append(tag, c.Relay)
		} else {
			tag = append(tag, "")
		}
		if c.Petname != "" {
			tag = append(tag, c.Petname)
		}
		tags = append(tags, tag)
	}

	evt := nostr.Event{
		Kind:      3,
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   "",
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("nip02: sign event: %w", err)
	}

	published := 0
	var lastErr error
	for result := range pool.PublishMany(ctx, publishRelays, evt) {
		if result.Error == nil {
			published++
		} else {
			lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
		}
	}
	if published == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relay accepted publish")
		}
		return "", lastErr
	}
	return evt.ID.Hex(), nil
}

// NIP02Contact represents a single contact in a kind:3 event.
type NIP02Contact struct {
	PubKey  string
	Relay   string
	Petname string
}

// FetchNIP02Contacts fetches the latest kind:3 contact list for a pubkey.
func FetchNIP02Contacts(ctx context.Context, pool *nostr.Pool, relays []string, pubkey string) ([]NIP02Contact, string, error) {
	pk, err := ParsePubKey(pubkey)
	if err != nil {
		return nil, "", fmt.Errorf("nip02: invalid pubkey: %w", err)
	}

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	filter := nostr.Filter{
		Kinds:   []nostr.Kind{3},
		Authors: []nostr.PubKey{pk},
		Limit:   1,
	}

	var best *nostr.Event
	for re := range pool.SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
		ev := re.Event
		if best == nil || ev.CreatedAt > best.CreatedAt {
			cp := ev
			best = &cp
		}
	}
	if best == nil {
		return nil, "", fmt.Errorf("nip02: no contact list found for %s", pubkey)
	}

	var contacts []NIP02Contact
	for _, tag := range best.Tags {
		if len(tag) < 2 || tag[0] != "p" {
			continue
		}
		c := NIP02Contact{PubKey: tag[1]}
		if len(tag) >= 3 {
			c.Relay = tag[2]
		}
		if len(tag) >= 4 {
			c.Petname = tag[3]
		}
		contacts = append(contacts, c)
	}
	return contacts, best.ID.Hex(), nil
}

// ─── NIP-65 Self-Sync ────────────────────────────────────────────────────────

// NIP65SyncOptions configures the bidirectional NIP-65 relay list sync.
type NIP65SyncOptions struct {
	Keyer        nostr.Keyer
	Pool         *nostr.Pool
	Relays       []string // bootstrap relays for initial fetch + subscription
	OnRelayUpdate func(read, write []string) // called when remote NIP-65 changes are detected
}

// NIP65SelfSync subscribes to the agent's own NIP-65 relay list and calls
// OnRelayUpdate when changes are detected from the network. This enables
// external clients to update the agent's relay configuration by publishing
// a new kind:10002 event.
func NIP65SelfSync(ctx context.Context, opts NIP65SyncOptions) error {
	if opts.Keyer == nil {
		return fmt.Errorf("nip65: keyer is required")
	}

	pkCtx, pkCancel := context.WithTimeout(ctx, 10*time.Second)
	pk, err := opts.Keyer.GetPublicKey(pkCtx)
	pkCancel()
	if err != nil {
		return fmt.Errorf("nip65: get public key: %w", err)
	}

	filter := nostr.Filter{
		Kinds:   []nostr.Kind{10002},
		Authors: []nostr.PubKey{pk},
	}

	var lastEventID string

	go func() {
		events, eoseCh := opts.Pool.SubscribeManyNotifyEOSE(ctx, opts.Relays, filter, nostr.SubscriptionOptions{})
		eoseDone := false
		for {
			select {
			case re, ok := <-events:
				if !ok {
					return
				}
				eventID := re.Event.ID.Hex()
				if eventID == lastEventID {
					continue
				}
				lastEventID = eventID

				list := DecodeNIP65Event(re.Event)
				readRelays := list.ReadRelays()
				writeRelays := list.WriteRelays()

				if eoseDone && opts.OnRelayUpdate != nil {
					log.Printf("nip65: detected remote relay list update (event=%s, read=%d, write=%d)",
						eventID[:MinInt(12, len(eventID))], len(readRelays), len(writeRelays))
					opts.OnRelayUpdate(readRelays, writeRelays)
				}

			case <-eoseCh:
				if !eoseDone {
					eoseDone = true
					log.Printf("nip65: self-sync EOSE received, watching for remote relay list changes")
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

// ─── Startup Publisher ───────────────────────────────────────────────────────

// StartupListPublishOptions controls what lists are published at startup.
type StartupListPublishOptions struct {
	Keyer        nostr.Keyer
	Pool         *nostr.Pool
	PublishRelays []string
	ReadRelays    []string
	WriteRelays   []string
	BothRelays    []string
	Contacts      []NIP02Contact // NIP-02 contact list (kind:3)
	// ForcePublish forces republishing even if a list already exists.
	ForcePublish  bool
}

// PublishStartupLists publishes the agent's NIP-65 relay list (kind:10002)
// and NIP-02 contact list (kind:3) at startup if they don't already exist.
func PublishStartupLists(ctx context.Context, opts StartupListPublishOptions) error {
	if opts.Keyer == nil {
		return fmt.Errorf("startup lists: keyer is required")
	}
	if len(opts.PublishRelays) == 0 {
		return fmt.Errorf("startup lists: no publish relays")
	}

	pkCtx, pkCancel := context.WithTimeout(ctx, 10*time.Second)
	pk, err := opts.Keyer.GetPublicKey(pkCtx)
	pkCancel()
	if err != nil {
		return fmt.Errorf("startup lists: get public key: %w", err)
	}
	pubkey := pk.Hex()

	// ── NIP-65 relay list ──────────────────────────────────────────────────
	publishNIP65 := opts.ForcePublish
	if !publishNIP65 {
		fetchCtx, fetchCancel := context.WithTimeout(ctx, 10*time.Second)
		_, fetchErr := FetchNIP65(fetchCtx, opts.Pool, opts.PublishRelays, pubkey)
		fetchCancel()
		if fetchErr != nil {
			publishNIP65 = true // no existing list found
		}
	}
	if publishNIP65 {
		// Deduplicate relay lists
		both := dedupeRelays(opts.BothRelays)
		read := dedupeRelays(opts.ReadRelays)
		write := dedupeRelays(opts.WriteRelays)
		// If no explicit read/write/both are specified, treat all publish relays as both
		if len(both) == 0 && len(read) == 0 && len(write) == 0 {
			both = dedupeRelays(opts.PublishRelays)
		}

		pubCtx, pubCancel := context.WithTimeout(ctx, 15*time.Second)
		eventID, pubErr := PublishNIP65(pubCtx, opts.Pool, opts.Keyer, opts.PublishRelays, read, write, both)
		pubCancel()
		if pubErr != nil {
			log.Printf("nip65: startup publish relay list failed: %v", pubErr)
		} else {
			log.Printf("nip65: published relay list (event=%s, read=%d, write=%d, both=%d)",
				eventID[:MinInt(12, len(eventID))], len(read), len(write), len(both))
		}
	} else {
		log.Printf("nip65: relay list already exists for %s, skipping publish", pubkey[:MinInt(12, len(pubkey))])
	}

	// ── NIP-02 contact list ────────────────────────────────────────────────
	if len(opts.Contacts) > 0 {
		publishNIP02 := opts.ForcePublish
		if !publishNIP02 {
			fetchCtx, fetchCancel := context.WithTimeout(ctx, 10*time.Second)
			_, _, fetchErr := FetchNIP02Contacts(fetchCtx, opts.Pool, opts.PublishRelays, pubkey)
			fetchCancel()
			if fetchErr != nil {
				publishNIP02 = true
			}
		}
		if publishNIP02 {
			pubCtx, pubCancel := context.WithTimeout(ctx, 15*time.Second)
			eventID, pubErr := PublishNIP02ContactList(pubCtx, opts.Pool, opts.Keyer, opts.PublishRelays, opts.Contacts)
			pubCancel()
			if pubErr != nil {
				log.Printf("nip02: startup publish contact list failed: %v", pubErr)
			} else {
				log.Printf("nip02: published contact list (event=%s, contacts=%d)",
					eventID[:MinInt(12, len(eventID))], len(opts.Contacts))
			}
		} else {
			log.Printf("nip02: contact list already exists for %s, skipping publish", pubkey[:MinInt(12, len(pubkey))])
		}
	}

	return nil
}

// dedupeRelays deduplicates and trims a relay URL slice.
func dedupeRelays(relays []string) []string {
	seen := make(map[string]struct{}, len(relays))
	out := make([]string, 0, len(relays))
	for _, r := range relays {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		lower := strings.ToLower(r)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// MergeRelayLists merges read and write relay lists into a single deduplicated list.
func MergeRelayLists(read, write []string) []string {
	seen := make(map[string]struct{}, len(read)+len(write))
	out := make([]string, 0, len(read)+len(write))
	add := func(r string) {
		r = strings.TrimSpace(r)
		if r == "" {
			return
		}
		key := strings.ToLower(r)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	for _, r := range read {
		add(r)
	}
	for _, r := range write {
		add(r)
	}
	sort.Strings(out)
	return out
}

// MinInt returns the smaller of a and b.
func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
