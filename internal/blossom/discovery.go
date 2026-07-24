package blossom

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	nostruntime "metiq/internal/nostr/runtime"
)

// KindServerList is the NIP-B7/BUD-03 Blossom server-list event kind.
const KindServerList = 10063

// ServerList is a verified kind-10063 replaceable event.
type ServerList struct {
	Event   nostr.Event
	Servers []string
}

// ServerListSubscription describes an event-driven server discovery subscription.
type ServerListSubscription struct {
	Filter  nostr.Filter
	Relays  []string
	OnEvent func(nostr.Event)
}

// ServerListTransport opens persistent Nostr subscriptions for discovery.
type ServerListTransport interface {
	SubscribeServerList(context.Context, ServerListSubscription) (func(), error)
}

// HubServerListTransport adapts the daemon's managed Nostr hub.
type HubServerListTransport struct {
	Hub func() *nostruntime.NostrHub
}

func (t HubServerListTransport) SubscribeServerList(ctx context.Context, subscription ServerListSubscription) (func(), error) {
	if t.Hub == nil || t.Hub() == nil {
		return nil, errors.New("blossom server list: Nostr hub is unavailable")
	}
	hub := t.Hub()
	sub, err := hub.Subscribe(ctx, nostruntime.SubOpts{
		Filter: subscription.Filter,
		Relays: subscription.Relays,
		OnEvent: func(event nostr.RelayEvent) {
			subscription.OnEvent(event.Event)
		},
	})
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { hub.Unsubscribe(sub.ID) }) }, nil
}

// BuildServerListEvent creates the current NIP-B7 wire format: empty content
// and one ["server", URL] tag for each server.
func BuildServerListEvent(ctx context.Context, signer nostr.Keyer, servers []string) (nostr.Event, error) {
	if signer == nil {
		return nostr.Event{}, errors.New("blossom server list: signer is required")
	}
	normalized := normalizeServers(servers)
	if len(normalized) == 0 {
		return nostr.Event{}, errors.New("blossom server list: at least one valid server is required")
	}
	tags := make(nostr.Tags, 0, len(normalized))
	for _, server := range normalized {
		tags = append(tags, nostr.Tag{"server", server})
	}
	event := nostr.Event{
		Kind:      KindServerList,
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   "",
	}
	if err := signer.SignEvent(ctx, &event); err != nil {
		return nostr.Event{}, fmt.Errorf("blossom server list: sign: %w", err)
	}
	return event, nil
}

// ParseServerListEvent validates a current kind-10063 event and extracts all
// standard ["server", URL] tags in publisher order.
func ParseServerListEvent(event nostr.Event, author nostr.PubKey) (ServerList, error) {
	if event.Kind != KindServerList || event.PubKey != author {
		return ServerList{}, errors.New("blossom server list: wrong kind or author")
	}
	if !event.CheckID() || !event.VerifySignature() {
		return ServerList{}, errors.New("blossom server list: invalid event signature")
	}
	now := nostr.Now()
	if event.CreatedAt > now+600 || event.CreatedAt < now-nostr.Timestamp((365*24*time.Hour)/time.Second) {
		return ServerList{}, errors.New("blossom server list: unreasonable timestamp")
	}
	raw := make([]string, 0, len(event.Tags))
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "server" {
			raw = append(raw, tag[1])
		}
	}
	servers := normalizeServers(raw)
	if len(servers) == 0 {
		return ServerList{}, errors.New("blossom server list: no valid server tags")
	}
	return ServerList{Event: event, Servers: servers}, nil
}

// DiscoverServerList subscribes by kind and author, validates inbound events,
// and emits only the newest replaceable event observed.
func DiscoverServerList(
	ctx context.Context,
	transport ServerListTransport,
	relays []string,
	author nostr.PubKey,
	onList func(ServerList),
) (func(), error) {
	if transport == nil {
		return nil, errors.New("blossom server list: transport is required")
	}
	var mu sync.Mutex
	var latest nostr.Timestamp
	var latestID string
	return transport.SubscribeServerList(ctx, ServerListSubscription{
		Filter: nostr.Filter{
			Kinds:   []nostr.Kind{KindServerList},
			Authors: []nostr.PubKey{author},
		},
		Relays: append([]string(nil), relays...),
		OnEvent: func(event nostr.Event) {
			list, err := ParseServerListEvent(event, author)
			if err != nil {
				return
			}
			mu.Lock()
			if list.Event.CreatedAt < latest ||
				(list.Event.CreatedAt == latest && latestID != "" && list.Event.ID.Hex() > latestID) {
				mu.Unlock()
				return
			}
			latest = list.Event.CreatedAt
			latestID = list.Event.ID.Hex()
			mu.Unlock()
			if onList != nil {
				onList(list)
			}
		},
	})
}

// FallbackURLs maps a failed hash-addressed media URL onto discovered Blossom
// servers while retaining the optional file extension.
func FallbackURLs(original string, servers []string) ([]string, error) {
	parsed, err := url.Parse(strings.TrimSpace(original))
	if err != nil {
		return nil, fmt.Errorf("blossom fallback: parse URL: %w", err)
	}
	name := path.Base(parsed.Path)
	hash, suffix, ok := splitHashFilename(name)
	if !ok {
		return nil, errors.New("blossom fallback: URL does not end in a SHA-256 hex filename")
	}
	normalized := normalizeServers(servers)
	out := make([]string, 0, len(normalized))
	for _, server := range normalized {
		out = append(out, strings.TrimRight(server, "/")+"/"+hash+suffix)
	}
	return out, nil
}

func normalizeServers(servers []string) []string {
	out := make([]string, 0, len(servers))
	seen := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		server = strings.TrimRight(strings.TrimSpace(server), "/")
		parsed, err := url.Parse(server)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			continue
		}
		if _, duplicate := seen[server]; duplicate {
			continue
		}
		seen[server] = struct{}{}
		out = append(out, server)
	}
	return out
}

func splitHashFilename(name string) (hash, suffix string, ok bool) {
	if len(name) < 64 {
		return "", "", false
	}
	hash = name[:64]
	for _, char := range hash {
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return "", "", false
		}
	}
	suffix = name[64:]
	if suffix != "" && !strings.HasPrefix(suffix, ".") {
		return "", "", false
	}
	return strings.ToLower(hash), suffix, true
}
