// Package toolbuiltin nostr_outbox.go — NIP-65 outbox model relay hints tools.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	nostruntime "metiq/internal/nostr/runtime"
)

// ─── NIP-65 outbox cache ─────────────────────────────────────────────────────

type outboxCacheEntry struct {
	read      []string
	write     []string
	fetchedAt time.Time
}

var (
	outboxCacheMu  sync.Mutex
	outboxCache    = map[string]outboxCacheEntry{}
	outboxCacheTTL = 30 * time.Minute
)

// NostrRelayHintsTool fetches a pubkey's NIP-65 relay hints (kind:10002).
// It checks both the local outbox cache and the global NIP-65 relay selector.
func NostrRelayHintsTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		pubkeyHex, err := requirePubkey(args)
		if err != nil {
			return "", fmt.Errorf("nostr_relay_hints: %w", err)
		}

		// Check the NIP-65 relay selector cache first (if available).
		if sel := GetRelaySelector(); sel != nil {
			if list := sel.Get(pubkeyHex); list != nil {
				out, _ := json.Marshal(map[string]any{
					"pubkey": pubkeyHex,
					"read":   list.ReadRelays(),
					"write":  list.WriteRelays(),
					"source": "nip65_selector",
				})
				return string(out), nil
			}
		}

		outboxCacheMu.Lock()
		if e, ok := outboxCache[pubkeyHex]; ok && time.Since(e.fetchedAt) < outboxCacheTTL {
			outboxCacheMu.Unlock()
			out, _ := json.Marshal(map[string]any{"pubkey": pubkeyHex, "read": e.read, "write": e.write})
			return string(out), nil
		}
		outboxCacheMu.Unlock()

		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", fmt.Errorf("nostr_relay_hints: no relays configured")
		}

		ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		pk, err := nostr.PubKeyFromHex(pubkeyHex)
		if err != nil {
			return "", fmt.Errorf("nostr_relay_hints: invalid pubkey: %w", err)
		}

		pool, releasePool := opts.AcquirePool("relay_hints done")
		defer releasePool()

		f := nostr.Filter{Kinds: []nostr.Kind{10002}, Authors: []nostr.PubKey{pk}, Limit: 1}
		var best *nostr.Event
		for re := range pool.FetchMany(ctx2, relays, f, nostr.SubscriptionOptions{}) {
			ev := re.Event
			if best == nil || ev.CreatedAt > best.CreatedAt {
				cp := ev
				best = &cp
			}
		}
		if best == nil {
			out, _ := json.Marshal(map[string]any{"pubkey": pubkeyHex, "read": []string{}, "write": []string{}})
			return string(out), nil
		}

		var readRelays, writeRelays []string
		for _, tag := range best.Tags {
			if len(tag) < 2 || tag[0] != "r" {
				continue
			}
			relayURL := tag[1]
			if len(tag) == 2 {
				readRelays = append(readRelays, relayURL)
				writeRelays = append(writeRelays, relayURL)
			} else {
				switch tag[2] {
				case "read":
					readRelays = append(readRelays, relayURL)
				case "write":
					writeRelays = append(writeRelays, relayURL)
				}
			}
		}

		outboxCacheMu.Lock()
		outboxCache[pubkeyHex] = outboxCacheEntry{read: readRelays, write: writeRelays, fetchedAt: time.Now()}
		outboxCacheMu.Unlock()

		// Also populate the global NIP-65 relay selector cache.
		if sel := GetRelaySelector(); sel != nil {
			list := &nostruntime.NIP65RelayList{PubKey: pubkeyHex, EventID: best.ID.Hex(), CreatedAt: int64(best.CreatedAt)}
			for _, tag := range best.Tags {
				if len(tag) < 2 || tag[0] != "r" {
					continue
				}
				entry := nostruntime.NIP65RelayEntry{URL: tag[1]}
				if len(tag) == 2 {
					entry.Read = true
					entry.Write = true
				} else if tag[2] == "read" {
					entry.Read = true
				} else if tag[2] == "write" {
					entry.Write = true
				}
				list.Entries = append(list.Entries, entry)
			}
			sel.Put(list)
		}

		out, _ := json.Marshal(map[string]any{"pubkey": pubkeyHex, "read": readRelays, "write": writeRelays})
		return string(out), nil
	}
}

// NostrRelayListSetTool publishes the caller's relay list metadata (kind:10002).
func NostrRelayListSetTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		signFn, err := opts.signerFunc()
		if err != nil {
			return "", nostrToolErr("nostr_relay_list_set", "no_keyer", err.Error(), nil)
		}
		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", nostrToolErr("nostr_relay_list_set", "no_relays", "no relays configured", nil)
		}

		readRelays := uniqueNonEmpty(toStringSlice(args["read_relays"]))
		writeRelays := uniqueNonEmpty(toStringSlice(args["write_relays"]))
		bothRelays := uniqueNonEmpty(toStringSlice(args["both_relays"]))
		if len(readRelays)+len(writeRelays)+len(bothRelays) == 0 {
			bothRelays = uniqueNonEmpty(relays)
		}

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

		evt := nostr.Event{Kind: 10002, CreatedAt: nostr.Now(), Tags: tags, Content: ""}
		if err := opts.checkOutboundEvent(&evt); err != nil {
			return "", nostrToolErr("nostr_relay_list_set", "content_blocked", err.Error(), map[string]any{"kind": 10002})
		}
		if err := signFn(ctx, &evt); err != nil {
			return "", nostrToolErr("nostr_relay_list_set", "sign_failed", err.Error(), map[string]any{"kind": 10002})
		}

		ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		pool, releasePool := opts.AcquirePool("relay_list_set done")
		defer releasePool()

		published := 0
		var lastErr error
		for _, relayURL := range relays {
			r, rErr := pool.EnsureRelay(relayURL)
			if rErr != nil {
				lastErr = rErr
				continue
			}
			if pErr := r.Publish(ctx2, evt); pErr != nil {
				lastErr = pErr
				continue
			}
			published++
		}
		if published == 0 && lastErr != nil {
			return "", nostrToolErr("nostr_relay_list_set", "publish_failed", lastErr.Error(), map[string]any{"kind": 10002, "publish_relays": relays})
		}

		// Invalidate caches for this pubkey so subsequent relay_hints calls get fresh data
		outboxCacheMu.Lock()
		delete(outboxCache, evt.PubKey.Hex())
		outboxCacheMu.Unlock()

		// Invalidate the global NIP-65 relay selector cache if one is registered.
		if sel := GetRelaySelector(); sel != nil {
			sel.Invalidate(evt.PubKey.Hex())
		}

		return nostrWriteSuccessEnvelope("nostr_relay_list_set", evt.ID.Hex(), 10002, map[string]any{
			"read_relays":  readRelays,
			"write_relays": writeRelays,
			"both_relays":  bothRelays,
		}, map[string]any{
			"published":      published,
			"publish_relays": relays,
		}, map[string]any{
			"published": published,
		}), nil
	}
}

// OutboxRelaysFor returns cached NIP-65 relays for a pubkey (union of read
// and write).  Returns nil if no cached data is available.  Does NOT trigger
// a network fetch — callers should use nostr_relay_hints or the relay selector
// for that.
func OutboxRelaysFor(pubkeyHex string) []string {
	// Check global relay selector first.
	if sel := GetRelaySelector(); sel != nil {
		if list := sel.Get(pubkeyHex); list != nil {
			// Union of read + write relays, deduplicated.
			seen := make(map[string]bool)
			var out []string
			for _, r := range list.WriteRelays() {
				if !seen[r] {
					seen[r] = true
					out = append(out, r)
				}
			}
			for _, r := range list.ReadRelays() {
				if !seen[r] {
					seen[r] = true
					out = append(out, r)
				}
			}
			return out
		}
	}
	// Fall back to local outbox cache (union of read + write).
	outboxCacheMu.Lock()
	defer outboxCacheMu.Unlock()
	if e, ok := outboxCache[pubkeyHex]; ok && time.Since(e.fetchedAt) < outboxCacheTTL {
		seen := make(map[string]bool)
		var out []string
		for _, r := range e.write {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
		for _, r := range e.read {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
		return out
	}
	return nil
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
