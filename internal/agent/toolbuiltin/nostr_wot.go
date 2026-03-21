// Package toolbuiltin nostr_wot.go — Web of Trust tools (follows, followers, distance).
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
)

// ─── nostr_follows ────────────────────────────────────────────────────────────

// NostrFollowsTool returns an agent tool that fetches a pubkey's follow list (kind:3).
//
// Parameters:
//   - pubkey  string   — hex or npub (required)
//   - relays  []string — optional relay override
//   - limit   int      — max follows to return (default 100)
func NostrFollowsTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		pubkeyHex, err := requirePubkey(args)
		if err != nil {
			return "", fmt.Errorf("nostr_follows: %w", err)
		}
		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", fmt.Errorf("nostr_follows: no relays configured")
		}
		limit := 100
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}

		ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		pk, err := nostr.PubKeyFromHex(pubkeyHex)
		if err != nil {
			return "", fmt.Errorf("nostr_follows: invalid pubkey: %w", err)
		}

		pool, releasePool := opts.AcquirePool("follows done")
		defer releasePool()

		f := nostr.Filter{
			Kinds:   []nostr.Kind{3},
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
			out, _ := json.Marshal([]any{})
			return string(out), nil
		}

		type followEntry struct {
			Pubkey  string `json:"pubkey"`
			Relay   string `json:"relay,omitempty"`
			Petname string `json:"petname,omitempty"`
		}
		var follows []followEntry
		for _, tag := range best.Tags {
			if len(tag) < 2 || tag[0] != "p" {
				continue
			}
			entry := followEntry{Pubkey: tag[1]}
			if len(tag) >= 3 {
				entry.Relay = tag[2]
			}
			if len(tag) >= 4 {
				entry.Petname = tag[3]
			}
			follows = append(follows, entry)
			if len(follows) >= limit {
				break
			}
		}

		out, _ := json.Marshal(follows)
		return string(out), nil
	}
}

// ─── nostr_followers ──────────────────────────────────────────────────────────

// NostrFollowersTool returns an agent tool that finds who follows a pubkey
// by searching for kind:3 events that have #p tag matching the target.
//
// Parameters:
//   - pubkey  string   — hex or npub (required)
//   - relays  []string — optional relay override
//   - limit   int      — max followers to return (default 50)
func NostrFollowersTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		pubkeyHex, err := requirePubkey(args)
		if err != nil {
			return "", fmt.Errorf("nostr_followers: %w", err)
		}
		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", fmt.Errorf("nostr_followers: no relays configured")
		}
		limit := 50
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}

		ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		pool, releasePool := opts.AcquirePool("followers done")
		defer releasePool()

		f := nostr.Filter{
			Kinds: []nostr.Kind{3},
			Tags:  nostr.TagMap{"p": []string{pubkeyHex}},
			Limit: limit,
		}
		sub := pool.SubscribeMany(ctx2, relays, f, nostr.SubscriptionOptions{})

		type followerEntry struct {
			Pubkey string `json:"pubkey"`
		}
		seen := map[string]bool{}
		var followers []followerEntry
		for re := range sub {
			pk := re.Event.PubKey.Hex()
			if seen[pk] {
				continue
			}
			seen[pk] = true
			followers = append(followers, followerEntry{Pubkey: pk})
			if len(followers) >= limit {
				break
			}
		}

		out, _ := json.Marshal(followers)
		return string(out), nil
	}
}

// ─── nostr_wot_distance ───────────────────────────────────────────────────────

// NostrWotDistanceTool returns an agent tool that computes the Web of Trust
// hop distance between two pubkeys via BFS over kind:3 contact lists.
//
// Parameters:
//   - from_pubkey string   — starting pubkey (hex or npub, required)
//   - to_pubkey   string   — target pubkey (hex or npub, required)
//   - relays      []string — optional relay override
//   - max_hops    int      — BFS depth limit (default 3)
func NostrWotDistanceTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		fromHex, err := resolveNostrPubkey(stringArg(args, "from_pubkey"))
		if err != nil || fromHex == "" {
			return "", fmt.Errorf("nostr_wot_distance: from_pubkey is required")
		}
		toHex, err := resolveNostrPubkey(stringArg(args, "to_pubkey"))
		if err != nil || toHex == "" {
			return "", fmt.Errorf("nostr_wot_distance: to_pubkey is required")
		}

		maxHops := 3
		if v, ok := args["max_hops"].(float64); ok && v > 0 {
			maxHops = int(v)
		}

		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", fmt.Errorf("nostr_wot_distance: no relays configured")
		}

		type qItem struct {
			pubkey string
			hops   int
			path   []string
		}

		queue := []qItem{{pubkey: fromHex, hops: 0, path: []string{fromHex}}}
		visited := map[string]bool{fromHex: true}

		for len(queue) > 0 {
			item := queue[0]
			queue = queue[1:]

			if item.pubkey == toHex {
				out, _ := json.Marshal(map[string]any{
					"distance": item.hops,
					"path":     item.path,
				})
				return string(out), nil
			}
			if item.hops >= maxHops {
				continue
			}

			follows, fetchErr := fetchFollows(ctx, item.pubkey, relays)
			if fetchErr != nil {
				continue
			}
			for _, f := range follows {
				if visited[f] {
					continue
				}
				visited[f] = true
				newPath := make([]string, len(item.path)+1)
				copy(newPath, item.path)
				newPath[len(item.path)] = f
				queue = append(queue, qItem{pubkey: f, hops: item.hops + 1, path: newPath})
			}
		}

		out, _ := json.Marshal(map[string]any{"distance": -1, "path": nil})
		return string(out), nil
	}
}

// fetchFollows retrieves the p-tag pubkeys from the latest kind:3 event for a pubkey.
func fetchFollows(ctx context.Context, pubkeyHex string, relays []string) ([]string, error) {
	ctx2, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, err
	}

	pool := nostr.NewPool(nostr.PoolOptions{})
	defer pool.Close("fetchFollows done")

	f := nostr.Filter{
		Kinds:   []nostr.Kind{3},
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
		return nil, nil
	}

	var out []string
	for _, tag := range best.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			out = append(out, tag[1])
		}
	}
	return out, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func requirePubkey(args map[string]any) (string, error) {
	raw, _ := args["pubkey"].(string)
	if raw == "" {
		return "", fmt.Errorf("pubkey is required")
	}
	return resolveNostrPubkey(raw)
}

func stringArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}
