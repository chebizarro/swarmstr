package refresolve

import (
	"context"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	nostrmeta "metiq/internal/nostr/metadata"
)

const (
	CacheTTL        = 2 * time.Minute
	CacheNegTTL     = 30 * time.Second
	CacheMaxEntries = 256
)

type Fetcher interface {
	Fetch(ctx context.Context, relays []string, filter nostr.Filter) <-chan nostr.RelayEvent
}

type cacheEntry struct {
	msg       *ReferencedMessage
	expiresAt time.Time
}

type RelayResolver struct {
	fetcher    Fetcher
	laneRelays []string

	mu    sync.Mutex
	cache map[string]cacheEntry
}

func NewRelayResolver(fetcher Fetcher, laneRelays []string) *RelayResolver {
	return &RelayResolver{
		fetcher:    fetcher,
		laneRelays: laneRelays,
		cache:      make(map[string]cacheEntry),
	}
}

func (r *RelayResolver) Resolve(ctx context.Context, refs []Ref) []ReferencedMessage {
	if r.fetcher == nil || len(refs) == 0 {
		return nil
	}

	now := time.Now()
	r.mu.Lock()
	needsFetch := make([]Ref, 0, len(refs))
	results := make([]*ReferencedMessage, len(refs))
	for i, ref := range refs {
		entry, ok := r.cache[ref.Event.ID]
		if ok && now.Before(entry.expiresAt) {
			results[i] = entry.msg
		} else {
			needsFetch = append(needsFetch, ref)
		}
	}
	r.mu.Unlock()

	if len(needsFetch) > 0 {
		fetched := r.doFetch(ctx, needsFetch)
		for i, ref := range refs {
			if results[i] == nil {
				if m, ok := fetched[ref.Event.ID]; ok {
					results[i] = &m
				}
			}
		}
	}

	out := make([]ReferencedMessage, 0, len(refs))
	for _, m := range results {
		if m != nil {
			out = append(out, *m)
		}
	}
	return out
}

func (r *RelayResolver) doFetch(ctx context.Context, refs []Ref) map[string]ReferencedMessage {
	ids := make([]nostr.ID, 0, len(refs))
	hexIDs := make(map[string]nostr.ID, len(refs))
	roleByID := make(map[string]string, len(refs))
	for _, ref := range refs {
		id, err := nostr.IDFromHex(ref.Event.ID)
		if err != nil {
			r.setCache(ref.Event.ID, nil, CacheNegTTL)
			continue
		}
		ids = append(ids, id)
		hexIDs[ref.Event.ID] = id
		roleByID[ref.Event.ID] = ref.Role
	}

	if len(ids) == 0 {
		return nil
	}

	relaySet := make(map[string]struct{})
	for _, ref := range refs {
		if ref.Event.Relay != "" {
			relaySet[ref.Event.Relay] = struct{}{}
		}
	}
	for _, rl := range r.laneRelays {
		relaySet[rl] = struct{}{}
	}

	relays := make([]string, 0, min(4, len(relaySet)))
	for rl := range relaySet {
		relays = append(relays, rl)
		if len(relays) >= 4 {
			break
		}
	}

	fetchCtx, cancel := context.WithTimeout(ctx, RelayTimeout)
	defer cancel()

	filter := nostr.Filter{
		IDs:   ids,
		Limit: len(ids),
	}

	eventCh := r.fetcher.Fetch(fetchCtx, relays, filter)

	wanted := make(map[string]bool, len(hexIDs))
	for he := range hexIDs {
		wanted[he] = true
	}

	fetched := make(map[string]ReferencedMessage)
	for re := range eventCh {
		if !re.CheckID() {
			continue
		}
		hexID := re.ID.Hex()
		if !wanted[hexID] {
			continue
		}
		if !re.VerifySignature() {
			r.setCache(hexID, nil, CacheNegTTL)
			continue
		}

		body, _ := nostrmeta.NormalizeText(re.Content, MaxBodyChars)
		msg := ReferencedMessage{
			ID:        hexID,
			Author:    re.PubKey.Hex(),
			Kind:      int(re.Kind),
			CreatedAt: int64(re.CreatedAt),
			Body:      body,
			Role:      roleByID[hexID],
			Via:       "relay",
		}
		fetched[hexID] = msg
		r.setCache(hexID, &msg, CacheTTL)
		delete(wanted, hexID)

		if len(wanted) == 0 {
			break
		}
	}

	for he := range wanted {
		r.setCache(he, nil, CacheNegTTL)
	}

	return fetched
}

func (r *RelayResolver) setCache(id string, msg *ReferencedMessage, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.cache) >= CacheMaxEntries {
		count := CacheMaxEntries / 4
		for k := range r.cache {
			delete(r.cache, k)
			count--
			if count == 0 {
				break
			}
		}
	}

	r.cache[id] = cacheEntry{
		msg:       msg,
		expiresAt: time.Now().Add(ttl),
	}
}