// Package runtime — peer_index.go maintains a per-account view of which fleet
// members are automated agents (bots) versus human operators, so the channel
// layer can damp agent-to-agent chatter (echo loops, circular conversations)
// WITHOUT touching authorization.
//
// Identity signal precedence:
//  1. An authoritative directory override (e.g. a NIP-51 fleet tier) when the
//     caller supplies one — resolves synchronously, no fetch required.
//  2. The member's self-attested NIP-24 kind:0 `bot` field, fetched lazily and
//     cached with a TTL, kept fresh because kind:0 is replaceable.
//
// TRUST BOUNDARY: the kind:0 `bot` flag is self-attested and spoofable. This
// index is for LOOP-DAMPING ONLY. It MUST NOT gate allow_from / authorization.
// A cold, stale, or negative lookup returns Unknown (fail-open); only a fresh
// verdict of Bot is definitive. Callers decide how conservatively to treat
// Unknown.
package runtime

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
)

// PeerAgentVerdict is the index's answer for a pubkey.
type PeerAgentVerdict uint8

const (
	// VerdictUnknown means not yet resolved / stale / no profile. Fail-open:
	// callers must never treat this as a definitive bot.
	VerdictUnknown PeerAgentVerdict = iota
	// VerdictHuman means a fresh kind:0 that does not self-attest as a bot.
	VerdictHuman
	// VerdictBot means a fresh kind:0 that self-attests `bot: true`, or an
	// authoritative directory override.
	VerdictBot
)

func (v PeerAgentVerdict) String() string {
	switch v {
	case VerdictBot:
		return "bot"
	case VerdictHuman:
		return "human"
	default:
		return "unknown"
	}
}

// IsKnownBot reports the hard-gate signal: a definitive fresh bot verdict only.
func (v PeerAgentVerdict) IsKnownBot() bool { return v == VerdictBot }

func boolVerdict(isBot bool) PeerAgentVerdict {
	if isBot {
		return VerdictBot
	}
	return VerdictHuman
}

// PeerProfileFacts are the profile facts the index needs; a nil return from the
// fetcher means no kind:0 was found (negative entry).
type PeerProfileFacts struct {
	IsBot       bool
	DisplayName string
}

// PeerAgentIndexOptions configure a PeerAgentIndex.
type PeerAgentIndexOptions struct {
	// FetchProfile fetches a member's kind:0 facts, or (nil, nil) if none is
	// published. Injected so the index is transport-independent and testable;
	// production wires this to NewRelayProfileFetcher. Required.
	FetchProfile func(pubkey string) (*PeerProfileFacts, error)
	// DirectoryOverride is consulted first, synchronously. Return (v, true) to
	// force a verdict (e.g. a NIP-51 fleet tier), or (_, false) to defer to the
	// self-attested kind:0 signal. Optional.
	DirectoryOverride func(pubkey string) (bool, bool)
	// TTL is the entry freshness window. Default 30 min.
	TTL time.Duration
	// NegativeTTL is the freshness window for a member with no kind:0. Default 5 min.
	NegativeTTL time.Duration
	// MaxEntries bounds cached entries (oldest evicted). Default 500.
	MaxEntries int
	// MaxConcurrentFetches caps concurrent background fetches; excess lookups
	// return Unknown until a slot frees. Default 4.
	MaxConcurrentFetches int
	// Now is an injectable clock for tests. Default time.Now.
	Now func() time.Time
	// OnError reports non-fatal fetch failures; never thrown to callers. Optional.
	OnError func(err error, pubkey string)
}

const (
	defaultPeerTTL             = 30 * time.Minute
	defaultPeerNegativeTTL     = 5 * time.Minute
	defaultPeerMaxEntries      = 500
	defaultPeerMaxConcurrent   = 4
	defaultRelayProfileTimeout = 5 * time.Second
)

type peerEntry struct {
	isBot       bool
	displayName string
	fetchedAt   time.Time
	// negative is true when the fetch found no kind:0 for this member.
	negative bool
}

type lruItem struct {
	key   string
	entry *peerEntry
}

// PeerAgentIndex is a bounded, TTL'd, fail-open index of peer bot verdicts.
type PeerAgentIndex struct {
	mu       sync.Mutex
	entries  map[string]*list.Element // key -> element in lru
	lru      *list.List               // front = oldest, back = newest
	inFlight map[string]chan struct{}
	// gen is bumped on Clear()/Close() so a fetch started earlier cannot
	// repopulate state or delete a newer in-flight marker.
	gen    uint64
	closed bool

	fetch         func(string) (*PeerProfileFacts, error)
	override      func(string) (bool, bool)
	ttl           time.Duration
	negTTL        time.Duration
	maxEntries    int
	maxConcurrent int
	now           func() time.Time
	onError       func(error, string)

	wg sync.WaitGroup
}

// NewPeerAgentIndex constructs a PeerAgentIndex.
func NewPeerAgentIndex(opts PeerAgentIndexOptions) (*PeerAgentIndex, error) {
	if opts.FetchProfile == nil {
		return nil, fmt.Errorf("peer-agent index: FetchProfile is required")
	}
	if opts.TTL <= 0 {
		opts.TTL = defaultPeerTTL
	}
	if opts.NegativeTTL <= 0 {
		opts.NegativeTTL = defaultPeerNegativeTTL
	}
	if opts.MaxEntries == 0 {
		opts.MaxEntries = defaultPeerMaxEntries
	}
	if opts.MaxEntries < 1 {
		return nil, fmt.Errorf("peer-agent index: MaxEntries must be a positive integer")
	}
	if opts.MaxConcurrentFetches == 0 {
		opts.MaxConcurrentFetches = defaultPeerMaxConcurrent
	}
	if opts.MaxConcurrentFetches < 1 {
		return nil, fmt.Errorf("peer-agent index: MaxConcurrentFetches must be a positive integer")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	return &PeerAgentIndex{
		entries:       make(map[string]*list.Element),
		lru:           list.New(),
		inFlight:      make(map[string]chan struct{}),
		fetch:         opts.FetchProfile,
		override:      opts.DirectoryOverride,
		ttl:           opts.TTL,
		negTTL:        opts.NegativeTTL,
		maxEntries:    opts.MaxEntries,
		maxConcurrent: opts.MaxConcurrentFetches,
		now:           opts.Now,
		onError:       opts.OnError,
	}, nil
}

func (idx *PeerAgentIndex) normalize(pubkey string) string {
	pk, err := ParsePubKey(pubkey)
	if err != nil {
		return ""
	}
	return pk.Hex()
}

func (idx *PeerAgentIndex) isFresh(e *peerEntry) bool {
	ttl := idx.ttl
	if e.negative {
		ttl = idx.negTTL
	}
	return idx.now().Sub(e.fetchedAt) <= ttl
}

func (idx *PeerAgentIndex) overrideLocked(key string) (bool, bool) {
	if idx.override == nil {
		return false, false
	}
	return idx.override(key)
}

// putEntryLocked inserts/refreshes an entry, capping total size with oldest-first
// eviction; re-inserting moves a key to the newest slot so hot keys survive.
func (idx *PeerAgentIndex) putEntryLocked(key string, entry *peerEntry) {
	if el, ok := idx.entries[key]; ok {
		idx.lru.Remove(el)
		delete(idx.entries, key)
	}
	el := idx.lru.PushBack(&lruItem{key: key, entry: entry})
	idx.entries[key] = el
	for len(idx.entries) > idx.maxEntries {
		front := idx.lru.Front()
		if front == nil {
			break
		}
		item := front.Value.(*lruItem)
		idx.lru.Remove(front)
		delete(idx.entries, item.key)
	}
}

func (idx *PeerAgentIndex) entryLocked(key string) (*peerEntry, bool) {
	el, ok := idx.entries[key]
	if !ok {
		return nil, false
	}
	return el.Value.(*lruItem).entry, true
}

func (idx *PeerAgentIndex) verdictForLocked(key string) PeerAgentVerdict {
	if v, ok := idx.overrideLocked(key); ok {
		return boolVerdict(v)
	}
	e, ok := idx.entryLocked(key)
	// A stale or negative entry is authoritative for NOTHING: it degrades to
	// Unknown so a former-agent key now run by a human, or an entry whose
	// refreshes keep failing, is never left classified as a bot.
	if !ok || e.negative || !idx.isFresh(e) {
		return VerdictUnknown
	}
	if e.isBot {
		return VerdictBot
	}
	return VerdictHuman
}

// refreshLocked kicks off a background fetch for key if one is warranted. It
// returns a channel closed when the fetch completes, or nil when the fetch was
// deduplicated-to-existing / shed for concurrency / suppressed after Close.
// Must be called with idx.mu held.
func (idx *PeerAgentIndex) refreshLocked(key string) <-chan struct{} {
	if idx.closed {
		return nil
	}
	if ch, ok := idx.inFlight[key]; ok {
		return ch
	}
	// Cap concurrent relay work on the hardened admission path; excess lookups
	// simply stay Unknown (fail open) until a slot frees.
	if len(idx.inFlight) >= idx.maxConcurrent {
		return nil
	}
	startedGen := idx.gen
	done := make(chan struct{})
	idx.inFlight[key] = done
	idx.wg.Add(1)
	go func() {
		defer idx.wg.Done()
		defer close(done)

		facts, err := idx.fetch(key)

		idx.mu.Lock()
		superseded := startedGen != idx.gen
		if !superseded {
			if err == nil {
				idx.putEntryLocked(key, &peerEntry{
					isBot:       facts != nil && facts.IsBot,
					displayName: displayNameOf(facts),
					fetchedAt:   idx.now(),
					negative:    facts == nil,
				})
			}
			if idx.inFlight[key] == done {
				delete(idx.inFlight, key)
			}
		}
		idx.mu.Unlock()

		// Report fetch errors outside the lock so a callback can't deadlock.
		if err != nil && !superseded && idx.onError != nil {
			idx.onError(err, key)
		}
	}()
	return done
}

func displayNameOf(facts *PeerProfileFacts) string {
	if facts == nil {
		return ""
	}
	return facts.DisplayName
}

// IsPeerAgent returns the best verdict available now. On a cold or stale entry
// it kicks off a background refresh and returns the current value (Unknown when
// nothing is cached). Use on the synchronous admission-gate path.
func (idx *PeerAgentIndex) IsPeerAgent(pubkey string) PeerAgentVerdict {
	key := idx.normalize(pubkey)
	if key == "" {
		return VerdictUnknown
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if v, ok := idx.overrideLocked(key); ok {
		return boolVerdict(v)
	}
	e, ok := idx.entryLocked(key)
	if !ok || !idx.isFresh(e) {
		idx.refreshLocked(key)
	}
	return idx.verdictForLocked(key)
}

// Resolve awaits a resolved verdict, fetching if the entry is cold or stale.
func (idx *PeerAgentIndex) Resolve(pubkey string) PeerAgentVerdict {
	key := idx.normalize(pubkey)
	if key == "" {
		return VerdictUnknown
	}
	idx.mu.Lock()
	if v, ok := idx.overrideLocked(key); ok {
		idx.mu.Unlock()
		return boolVerdict(v)
	}
	e, ok := idx.entryLocked(key)
	var done <-chan struct{}
	if !ok || !idx.isFresh(e) {
		done = idx.refreshLocked(key)
	}
	idx.mu.Unlock()

	if done != nil {
		<-done
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.verdictForLocked(key)
}

// Set seeds or overrides an entry directly (e.g. the agent's own pubkey as a bot).
func (idx *PeerAgentIndex) Set(pubkey string, facts PeerProfileFacts) {
	key := idx.normalize(pubkey)
	if key == "" {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.putEntryLocked(key, &peerEntry{
		isBot:       facts.IsBot,
		displayName: facts.DisplayName,
		fetchedAt:   idx.now(),
		negative:    false,
	})
}

// Size returns the number of cached entries.
func (idx *PeerAgentIndex) Size() int {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return len(idx.entries)
}

// Clear drops all cached entries. In-flight fetches started before the clear
// cannot repopulate state (generation guard).
func (idx *PeerAgentIndex) Clear() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.gen++
	idx.entries = make(map[string]*list.Element)
	idx.lru.Init()
	idx.inFlight = make(map[string]chan struct{})
}

// Close stops accepting new fetches and waits for outstanding ones to finish.
func (idx *PeerAgentIndex) Close() {
	idx.mu.Lock()
	idx.gen++
	idx.closed = true
	idx.mu.Unlock()
	idx.wg.Wait()
}

// NewRelayProfileFetcher returns a FetchProfile adapter backed by
// FetchIdentityBoundProfile. It reads a member's identity-bound kind:0 and
// reports whether they self-attest as a bot. Returns (nil, nil) when no valid
// profile is published (member stays Unknown / negative in the index).
func NewRelayProfileFetcher(pool *nostr.Pool, relays []string, timeout time.Duration) func(string) (*PeerProfileFacts, error) {
	if timeout <= 0 {
		timeout = defaultRelayProfileTimeout
	}
	return func(pubkey string) (*PeerProfileFacts, error) {
		pk, err := ParsePubKey(pubkey)
		if err != nil {
			return nil, fmt.Errorf("peer-agent fetch: parse pubkey: %w", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		bp, found, err := FetchIdentityBoundProfile(ctx, pool, relays, pk)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, nil
		}
		display := bp.Content.DisplayName
		if display == "" {
			display = bp.Content.Name
		}
		return &PeerProfileFacts{IsBot: bp.Content.IsBot(), DisplayName: display}, nil
	}
}
