// Package toolbuiltin nostr_zap.go — NIP-57 zap agent tools.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nostr "fiatjaf.com/nostr"

	"swarmstr/internal/agent"
	"swarmstr/internal/nostr/zap"
)

// ─── nostr_zap_send ───────────────────────────────────────────────────────────

// NostrZapSendTool returns an agent tool that sends a NIP-57 zap.
//
// Parameters:
//   - to_pubkey   string — hex pubkey of the recipient (required)
//   - lud16       string — lightning address e.g. alice@wallet.example.com (required)
//   - amount_sats int    — amount in satoshis (required, > 0)
//   - comment     string — optional zap comment
//   - note_id     string — optional hex event ID of the note being zapped
func NostrZapSendTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		if opts.Keyer == nil {
			return "", fmt.Errorf("nostr_zap_send: signing keyer not configured")
		}

		toPubkeyRaw, _ := args["to_pubkey"].(string)
		if toPubkeyRaw == "" {
			return "", fmt.Errorf("nostr_zap_send: to_pubkey is required")
		}
		toPubkey, err := resolveNostrPubkey(toPubkeyRaw)
		if err != nil {
			return "", fmt.Errorf("nostr_zap_send: %w", err)
		}

		lud16, _ := args["lud16"].(string)
		if lud16 == "" {
			return "", fmt.Errorf("nostr_zap_send: lud16 (lightning address) is required")
		}

		amountSats, ok := args["amount_sats"].(float64)
		if !ok || amountSats <= 0 {
			return "", fmt.Errorf("nostr_zap_send: amount_sats (positive integer) is required")
		}

		comment, _ := args["comment"].(string)
		noteID, _ := args["note_id"].(string)

		result, err := zap.Send(ctx, zap.SendOpts{
			Keyer:  opts.Keyer,
			Relays: opts.Relays,
		}, lud16, toPubkey, noteID, int64(amountSats), comment)
		if err != nil {
			return "", fmt.Errorf("nostr_zap_send: %w", err)
		}

		out, _ := json.Marshal(result)
		return string(out), nil
	}
}

// ─── nostr_zap_list ───────────────────────────────────────────────────────────

// NostrZapListTool returns an agent tool that fetches recent zap receipts (kind:9735).
//
// Parameters:
//   - pubkey  string   — hex pubkey to fetch zaps for (required)
//   - relays  []string — optional relay override
//   - limit   int      — max receipts (default 20)
func NostrZapListTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		pubkeyHex, err := requirePubkey(args)
		if err != nil {
			return "", fmt.Errorf("nostr_zap_list: %w", err)
		}
		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", fmt.Errorf("nostr_zap_list: no relays configured")
		}
		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}

		ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		pk, err := nostr.PubKeyFromHex(pubkeyHex)
		if err != nil {
			return "", fmt.Errorf("nostr_zap_list: invalid pubkey: %w", err)
		}

		pool, releasePool := opts.AcquirePool("zap_list done")
		defer releasePool()

		f := nostr.Filter{
			Kinds: []nostr.Kind{9735},
			Tags:  nostr.TagMap{"p": []string{pk.Hex()}},
			Limit: limit,
		}
		sub := pool.SubscribeMany(ctx2, relays, f, nostr.SubscriptionOptions{})

		var receipts []map[string]any
		for re := range sub {
			ev := re.Event
			receipt := map[string]any{
				"id":         ev.ID.Hex(),
				"pubkey":     ev.PubKey.Hex(),
				"created_at": int64(ev.CreatedAt),
				"tags":       eventToMap(ev)["tags"],
			}
			// Extract bolt11 and description from tags.
			for _, tag := range ev.Tags {
				if len(tag) >= 2 {
					switch tag[0] {
					case "bolt11":
						receipt["bolt11"] = tag[1]
					case "description":
						receipt["description"] = tag[1]
					case "amount":
						receipt["amount_msat"] = tag[1]
					}
				}
			}
			receipts = append(receipts, receipt)
			if len(receipts) >= limit {
				break
			}
		}
		if receipts == nil {
			receipts = []map[string]any{}
		}

		out, _ := json.Marshal(receipts)
		return string(out), nil
	}
}
