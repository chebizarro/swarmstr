// Package toolbuiltin nostr_outbox.go — NIP-65 outbox model relay hints tools.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	"metiq/internal/nostr/nip51"
	nostruntime "metiq/internal/nostr/runtime"
)

// NostrRelayHintsTool fetches a pubkey's NIP-65 relay hints (kind:10002),
// using the global NIP-65 relay selector as its cache.
func NostrRelayHintsTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		pubkeyHex, err := requirePubkey(args)
		if err != nil {
			return "", fmt.Errorf("nostr_relay_hints: %w", err)
		}

		sel := GetRelaySelector()
		if sel != nil {
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

		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", fmt.Errorf("nostr_relay_hints: no relays configured")
		}
		if _, err := nostr.PubKeyFromHex(pubkeyHex); err != nil {
			return "", fmt.Errorf("nostr_relay_hints: invalid pubkey: %w", err)
		}

		pool, releasePool := opts.AcquirePool("relay_hints done")
		defer releasePool()

		// With a valid pubkey and pool, FetchNIP65 fails only when no verified
		// relay list was found.
		list, err := nostruntime.FetchNIP65(ctx, pool, relays, pubkeyHex)
		if err != nil {
			out, _ := json.Marshal(map[string]any{"pubkey": pubkeyHex, "read": []string{}, "write": []string{}})
			return string(out), nil
		}
		if sel != nil {
			sel.Put(list)
		}
		out, _ := json.Marshal(map[string]any{"pubkey": pubkeyHex, "read": list.ReadRelays(), "write": list.WriteRelays()})
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

		evt := nostr.Event{Kind: nip51.KindRelayList, CreatedAt: nostr.Now(), Tags: tags, Content: ""}
		if err := opts.checkOutboundEvent(&evt); err != nil {
			return "", nostrToolErr("nostr_relay_list_set", "content_blocked", err.Error(), map[string]any{"kind": nip51.KindRelayList})
		}
		if err := signFn(ctx, &evt); err != nil {
			return "", nostrToolErr("nostr_relay_list_set", "sign_failed", err.Error(), map[string]any{"kind": nip51.KindRelayList})
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
			return "", nostrToolErr("nostr_relay_list_set", "publish_failed", lastErr.Error(), map[string]any{"kind": nip51.KindRelayList, "publish_relays": relays})
		}

		// Invalidate the NIP-65 relay selector cache so subsequent relay_hints calls get fresh data.
		if sel := GetRelaySelector(); sel != nil {
			sel.Invalidate(evt.PubKey.Hex())
		}

		return nostrWriteSuccessEnvelope("nostr_relay_list_set", evt.ID.Hex(), nip51.KindRelayList, map[string]any{
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

// OutboxRelaysFor returns the relay selector's cached NIP-65 relays for a
// pubkey (union of read and write), or nil if none are cached.  Does NOT
// trigger a network fetch — callers should use nostr_relay_hints or the relay
// selector for that.
func OutboxRelaysFor(pubkeyHex string) []string {
	sel := GetRelaySelector()
	if sel == nil {
		return nil
	}
	list := sel.Get(pubkeyHex)
	if list == nil {
		return nil
	}
	return list.AllRelays()
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
