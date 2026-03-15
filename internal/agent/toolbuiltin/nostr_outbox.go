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

	"swarmstr/internal/agent"
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
func NostrRelayHintsTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		pubkeyHex, err := requirePubkey(args)
		if err != nil {
			return "", fmt.Errorf("nostr_relay_hints: %w", err)
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

		pool := nostr.NewPool(nostr.PoolOptions{})
		defer pool.Close("relay_hints done")

		f := nostr.Filter{Kinds: []nostr.Kind{10002}, Authors: []nostr.PubKey{pk}, Limit: 1}
		sub := pool.SubscribeMany(ctx2, relays, f, nostr.SubscriptionOptions{})

		var best *nostr.Event
		for re := range sub {
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
		if err := signFn(ctx, &evt); err != nil {
			return "", nostrToolErr("nostr_relay_list_set", "sign_failed", err.Error(), map[string]any{"kind": 10002})
		}

		ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		pool := nostr.NewPool(nostr.PoolOptions{})
		defer pool.Close("relay_list_set done")

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
	
		// Invalidate cache for this pubkey so subsequent relay_hints calls get fresh data
		outboxCacheMu.Lock()
		delete(outboxCache, evt.PubKey.Hex())
		outboxCacheMu.Unlock()
	
		return nostrWriteSuccessEnvelope("nostr_relay_list_set", evt.ID.Hex(), 10002, map[string]any{
			"read_relays":  readRelays,
			"write_relays": writeRelays,
			"both_relays":  bothRelays,
		}, map[string]any{
			"published":     published,
			"publish_relays": relays,
		}, map[string]any{
			"published": published,
		}), nil
	}
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
