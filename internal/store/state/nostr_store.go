package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/nostr/events"
	nostruntime "metiq/internal/nostr/runtime"
)

type NostrStore struct {
	pool   *nostr.Pool
	relays []string
	keyer  nostr.Keyer
	pub    nostr.PubKey
}

var _ NostrStateStore = (*NostrStore)(nil)

func NewNostrStore(keyer nostr.Keyer, relays []string) (*NostrStore, error) {
	if len(relays) == 0 {
		return nil, fmt.Errorf("at least one relay is required")
	}
	if keyer == nil {
		return nil, fmt.Errorf("signing keyer is required")
	}
	pk, err := keyer.GetPublicKey(context.Background())
	if err != nil {
		return nil, fmt.Errorf("resolve signer pubkey: %w", err)
	}
	return &NostrStore{
		pool:   nostruntime.NewPoolNIP42(keyer),
		relays: relays,
		keyer:  keyer,
		pub:    pk,
	}, nil
}

func (s *NostrStore) Close() {
	s.pool.Close("state store closed")
}

func (s *NostrStore) GetLatestReplaceable(ctx context.Context, addr Address) (Event, error) {
	author, err := s.addressAuthor(addr)
	if err != nil {
		return Event{}, err
	}
	if addr.DTag == "" {
		return Event{}, fmt.Errorf("address d tag is required")
	}

	filter := nostr.Filter{
		Kinds:   []nostr.Kind{toNostrKind(addr.Kind)},
		Authors: []nostr.PubKey{author},
		Tags:    nostr.TagMap{"d": {addr.DTag}},
		Limit:   10,
	}

	var latest nostr.Event
	var latestState Event
	found := false
	for relayEvent := range s.pool.FetchMany(ctx, s.relays, filter, nostr.SubscriptionOptions{}) {
		evt := relayEvent.Event
		if !evt.CheckID() || !evt.VerifySignature() {
			continue
		}
		if evt.Kind != toNostrKind(addr.Kind) || evt.PubKey != author {
			continue
		}
		if !tagContainsValue(evt.Tags, "d", addr.DTag) {
			continue
		}
		candidate := fromNostrEvent(evt)
		if !found || isNewerEvent(candidate, latestState) {
			latest = evt
			latestState = candidate
			found = true
		}
	}
	if !found {
		return Event{}, ErrNotFound
	}
	return fromNostrEvent(latest), nil
}

func (s *NostrStore) PutReplaceable(ctx context.Context, addr Address, content string, extraTags [][]string) (Event, error) {
	author, err := s.addressAuthor(addr)
	if err != nil {
		return Event{}, err
	}
	if author != s.pub {
		return Event{}, fmt.Errorf("cannot write replaceable event for foreign author %s", author.Hex())
	}
	if addr.DTag == "" {
		return Event{}, fmt.Errorf("address d tag is required")
	}

	tags := appendTags(extraTags, []string{"d", addr.DTag})
	evt := nostr.Event{
		Kind:      toNostrKind(addr.Kind),
		CreatedAt: nostr.Now(),
		Tags:      toNostrTags(tags),
		Content:   content,
	}
	if err := s.keyer.SignEvent(ctx, &evt); err != nil {
		return Event{}, fmt.Errorf("sign replaceable event: %w", err)
	}
	if err := publishAtLeastOnce(ctx, s.pool, s.relays, evt); err != nil {
		return Event{}, err
	}
	return fromNostrEvent(evt), nil
}

func (s *NostrStore) PutAppend(ctx context.Context, addr Address, content string, extraTags [][]string) (Event, error) {
	author, err := s.addressAuthor(addr)
	if err != nil {
		return Event{}, err
	}
	if author != s.pub {
		return Event{}, fmt.Errorf("cannot append event for foreign author %s", author.Hex())
	}

	tags := append([][]string{}, extraTags...)
	if addr.DTag != "" {
		tags = appendTags(tags, []string{"ref", addr.DTag})
	}
	tags = appendTags(tags, []string{"swarmstr_nonce", fmt.Sprintf("%d", time.Now().UnixNano())})

	evt := nostr.Event{
		Kind:      toNostrKind(addr.Kind),
		CreatedAt: nostr.Now(),
		Tags:      toNostrTags(tags),
		Content:   content,
	}
	if err := s.keyer.SignEvent(ctx, &evt); err != nil {
		return Event{}, fmt.Errorf("sign append event: %w", err)
	}
	if err := publishAtLeastOnce(ctx, s.pool, s.relays, evt); err != nil {
		return Event{}, err
	}
	return fromNostrEvent(evt), nil
}

func (s *NostrStore) ListByTag(ctx context.Context, kind events.Kind, tagName, tagValue string, limit int) ([]Event, error) {
	return s.listByTag(ctx, kind, "", tagName, tagValue, limit)
}

func (s *NostrStore) ListByTagForAuthor(ctx context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]Event, error) {
	return s.listByTag(ctx, kind, authorPubKey, tagName, tagValue, limit)
}

func (s *NostrStore) listByTag(ctx context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]Event, error) {
	if tagName == "" || tagValue == "" {
		return nil, fmt.Errorf("tag name and value are required")
	}
	if limit <= 0 {
		limit = 100
	}

	filter := nostr.Filter{
		Kinds: []nostr.Kind{toNostrKind(kind)},
		Tags:  nostr.TagMap{tagName: {tagValue}},
		Limit: limit,
	}
	var author nostr.PubKey
	var hasAuthor bool
	if authorPubKey != "" {
		parsed, err := nostruntime.ParsePubKey(authorPubKey)
		if err != nil {
			return nil, err
		}
		author = parsed
		hasAuthor = true
		filter.Authors = []nostr.PubKey{author}
	}

	seen := map[string]struct{}{}
	res := make([]Event, 0, limit)
	for relayEvent := range s.pool.FetchMany(ctx, s.relays, filter, nostr.SubscriptionOptions{}) {
		evt := relayEvent.Event
		if !evt.CheckID() || !evt.VerifySignature() {
			continue
		}
		if evt.Kind != toNostrKind(kind) || !tagContainsValue(evt.Tags, tagName, tagValue) {
			continue
		}
		if hasAuthor && evt.PubKey != author {
			continue
		}
		id := evt.ID.Hex()
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		res = append(res, fromNostrEvent(evt))
	}

	sort.Slice(res, func(i, j int) bool {
		return isNewerEvent(res[i], res[j])
	})
	if len(res) > limit {
		res = res[:limit]
	}
	return res, nil
}

func (s *NostrStore) addressAuthor(addr Address) (nostr.PubKey, error) {
	if addr.PubKey == "" {
		return s.pub, nil
	}
	return nostruntime.ParsePubKey(addr.PubKey)
}

func publishAtLeastOnce(ctx context.Context, pool *nostr.Pool, relays []string, evt nostr.Event) error {
	published := false
	var lastErr error
	for result := range pool.PublishMany(ctx, relays, evt) {
		if result.Error == nil {
			published = true
			continue
		}
		lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
	}
	if published {
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no relay accepted publish")
	}
	return lastErr
}

func fromNostrEvent(evt nostr.Event) Event {
	tags := make([][]string, 0, len(evt.Tags))
	for _, tag := range evt.Tags {
		cloned := make([]string, len(tag))
		copy(cloned, tag)
		tags = append(tags, cloned)
	}

	return Event{
		ID:        evt.ID.Hex(),
		PubKey:    evt.PubKey.Hex(),
		Kind:      toSwarmKind(evt.Kind),
		CreatedAt: int64(evt.CreatedAt),
		Tags:      tags,
		Content:   evt.Content,
		Sig:       nostr.HexEncodeToString(evt.Sig[:]),
	}
}

func toNostrTags(tags [][]string) nostr.Tags {
	res := make(nostr.Tags, 0, len(tags))
	for _, t := range tags {
		if len(t) == 0 {
			continue
		}
		cloned := make([]string, len(t))
		copy(cloned, t)
		res = append(res, cloned)
	}
	return res
}

func appendTags(tags [][]string, tag []string) [][]string {
	res := append([][]string{}, tags...)
	res = append(res, append([]string{}, tag...))
	return res
}

func tagContainsValue(tags nostr.Tags, name string, value string) bool {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name && tag[1] == value {
			return true
		}
	}
	return false
}

func toNostrKind(kind events.Kind) nostr.Kind {
	return nostr.Kind(kind)
}

func toSwarmKind(kind nostr.Kind) events.Kind {
	return events.Kind(kind)
}
