// Package toolbuiltin nostr_zap.go — NIP-57 zap agent tools.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	"metiq/internal/nostr/zap"
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
			return "", nostrToolErr("nostr_zap_send", "no_keyer", "signing keyer not configured", nil)
		}

		toPubkeyRaw, _ := args["to_pubkey"].(string)
		if toPubkeyRaw == "" {
			return "", nostrToolErr("nostr_zap_send", "invalid_input", "to_pubkey is required", nil)
		}
		toPubkey, err := resolveNostrPubkey(toPubkeyRaw)
		if err != nil {
			return "", nostrToolErr("nostr_zap_send", "invalid_input", err.Error(), nil)
		}

		lud16, _ := args["lud16"].(string)
		if lud16 == "" {
			return "", nostrToolErr("nostr_zap_send", "invalid_input", "lud16 (lightning address) is required", nil)
		}

		amountSats, ok := args["amount_sats"].(float64)
		if !ok || amountSats <= 0 {
			return "", nostrToolErr("nostr_zap_send", "invalid_input", "amount_sats (positive integer) is required", nil)
		}

		comment, _ := args["comment"].(string)
		noteID, _ := args["note_id"].(string)

		// Scan zap comment for secrets before publishing.
		if comment != "" {
			if err := opts.checkOutboundContent(comment); err != nil {
				return "", nostrToolErr("nostr_zap_send", "content_blocked", err.Error(), nil)
			}
		}

		result, err := zap.Send(ctx, zap.SendOpts{
			Keyer:  opts.Keyer,
			Relays: opts.Relays,
		}, lud16, toPubkey, noteID, int64(amountSats), comment)
		if err != nil {
			return "", nostrToolErr("nostr_zap_send", "operation_failed", err.Error(), nil)
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
			return "", nostrToolErr("nostr_zap_list", "invalid_input", err.Error(), nil)
		}
		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", nostrToolErr("nostr_zap_list", "no_relays", "no relays configured", nil)
		}
		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}

		ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		pk, err := nostr.PubKeyFromHex(pubkeyHex)
		if err != nil {
			return "", nostrToolErr("nostr_zap_list", "invalid_input", fmt.Sprintf("invalid pubkey: %v", err), nil)
		}

		pool, releasePool := opts.AcquirePool("zap_list done")
		defer releasePool()

		f := nostr.Filter{
			Kinds: []nostr.Kind{9735},
			Tags:  nostr.TagMap{"p": []string{pk.Hex()}},
			Limit: limit,
		}
		var receipts []map[string]any
		for re := range pool.FetchMany(ctx2, relays, f, nostr.SubscriptionOptions{}) {
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
