// Package toolbuiltin nostr_outbox.go — NIP-65 outbox model relay hints tool.
//
// nostr_relay_hints fetches a pubkey's kind:10002 relay list and returns
// the read/write relay sets per the NIP-65 outbox model.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
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

// ─── nostr_relay_hints ────────────────────────────────────────────────────────

// NostrRelayHintsTool returns an agent tool that fetches a pubkey's NIP-65
// relay hints (kind:10002 relay list event).
//
// Parameters:
//   - pubkey  string   — hex pubkey or npub (required)
//   - relays  []string — optional relay override for fetching
func NostrRelayHintsTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		pubkeyHex, err := requirePubkey(args)
		if err != nil {
			return "", fmt.Errorf("nostr_relay_hints: %w", err)
		}

		outboxCacheMu.Lock()
		if e, ok := outboxCache[pubkeyHex]; ok && time.Since(e.fetchedAt) < outboxCacheTTL {
			outboxCacheMu.Unlock()
			out, _ := json.Marshal(map[string]any{
				"pubkey": pubkeyHex,
				"read":   e.read,
				"write":  e.write,
			})
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

		f := nostr.Filter{
			Kinds:   []nostr.Kind{10002},
			Authors: []nostr.PubKey{pk},
			Limit:   1,
		}
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
			out, _ := json.Marshal(map[string]any{
				"pubkey": pubkeyHex,
				"read":   []string{},
				"write":  []string{},
			})
			return string(out), nil
		}

		// Parse "r" tags: ["r", relayURL] or ["r", relayURL, "read"/"write"].
		var readRelays, writeRelays []string
		for _, tag := range best.Tags {
			if len(tag) < 2 || tag[0] != "r" {
				continue
			}
			relayURL := tag[1]
			if len(tag) == 2 {
				// No marker = both read and write.
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
		outboxCache[pubkeyHex] = outboxCacheEntry{
			read:      readRelays,
			write:     writeRelays,
			fetchedAt: time.Now(),
		}
		outboxCacheMu.Unlock()

		out, _ := json.Marshal(map[string]any{
			"pubkey": pubkeyHex,
			"read":   readRelays,
			"write":  writeRelays,
		})
		return string(out), nil
	}
}
