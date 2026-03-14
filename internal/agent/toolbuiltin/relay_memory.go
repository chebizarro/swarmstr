// Package toolbuiltin – relay-as-indexed-memory tools.
//
// Uses Nostr relays as a persistent, searchable memory layer for agents.
// relay_remember: publish a kind:30078 or kind:1 event as a memory note
// relay_recall:   fetch matching events from relays and return as context
// relay_forget:   publish a NIP-09 deletion event for a remembered event
//
// These tools complement the in-process memory.Store with durable Nostr-native storage.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	"swarmstr/internal/agent"
)

// RelayMemoryToolOpts configures the relay memory tools.
type RelayMemoryToolOpts struct {
	Keyer      nostr.Keyer
	Relays     []string
}

// RegisterRelayMemoryTools registers relay memory tools into the registry.
func RegisterRelayMemoryTools(tools *agent.ToolRegistry, opts RelayMemoryToolOpts) {
	pool := nostr.NewPool(nostr.PoolOptions{PenaltyBox: true})

	signEvent := func(ctx context.Context, evt *nostr.Event) error {
		if opts.Keyer == nil {
			return fmt.Errorf("no signing keyer configured")
		}
		return opts.Keyer.SignEvent(ctx, evt)
	}

	ownPubkey := func(ctx context.Context) (string, error) {
		if opts.Keyer == nil {
			return "", fmt.Errorf("no signing keyer configured")
		}
		pk, err := opts.Keyer.GetPublicKey(ctx)
		if err != nil {
			return "", err
		}
		return pk.Hex(), nil
	}

	// relay_remember – store a memory note as a replaceable kind:30078 or ephemeral kind:1.
	tools.RegisterWithDef("relay_remember", func(ctx context.Context, args map[string]any) (string, error) {
		content, _ := args["content"].(string)
		if content == "" {
			return "", fmt.Errorf("relay_remember: content is required")
		}
		topic, _ := args["topic"].(string)
		hashtags := toStringSlice(args["tags"])

		// Use kind 30078 (replaceable app data) for structured memories,
		// kind 1 (short note) for free-form thoughts.
		kind := 30078
		if v, ok := args["kind"].(float64); ok && v > 0 {
			kind = int(v)
		}

		dTag := topic
		if dTag == "" {
			dTag = fmt.Sprintf("memory:%d", time.Now().Unix())
		}

		tags := nostr.Tags{{"d", dTag}}
		for _, ht := range hashtags {
			tags = append(tags, nostr.Tag{"t", ht})
		}
		if topic != "" {
			tags = append(tags, nostr.Tag{"topic", topic})
		}

		evt := nostr.Event{
			Kind:      nostr.Kind(kind),
			CreatedAt: nostr.Now(),
			Tags:      tags,
			Content:   content,
		}
		if err := signEvent(ctx, &evt); err != nil {
			return "", fmt.Errorf("relay_remember: sign: %w", err)
		}

		relays := opts.Relays
		if custom := toStringSlice(args["relays"]); len(custom) > 0 {
			relays = custom
		}

		published := false
		var lastErr error
		for result := range pool.PublishMany(ctx, relays, evt) {
			if result.Error == nil {
				published = true
			} else {
				lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
			}
		}
		if !published {
			if lastErr == nil {
				lastErr = fmt.Errorf("no relay accepted publish")
			}
			return "", lastErr
		}

		out, _ := json.Marshal(map[string]any{
			"ok":       true,
			"event_id": evt.ID.Hex(),
			"topic":    dTag,
			"kind":     kind,
		})
		return string(out), nil
	}, RelayRememberDef)

	// relay_recall – search relay event history for memories.
	tools.RegisterWithDef("relay_recall", func(ctx context.Context, args map[string]any) (string, error) {
		query, _ := args["query"].(string)
		topic, _ := args["topic"].(string)
		limit := 10
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = min(int(v), 50)
		}
		relays := opts.Relays
		if custom := toStringSlice(args["relays"]); len(custom) > 0 {
			relays = custom
		}

		pubkey, err := ownPubkey(ctx)
		if err != nil {
			return "", fmt.Errorf("relay_recall: %w", err)
		}
		pk, err := nostr.PubKeyFromHex(pubkey)
		if err != nil {
			return "", fmt.Errorf("relay_recall: invalid pubkey: %w", err)
		}

		filter := nostr.Filter{
			Kinds:   []nostr.Kind{30078, 1},
			Authors: []nostr.PubKey{pk},
			Limit:   limit,
		}

		// Apply time filters.
		if v, ok := args["since"].(float64); ok && v > 0 {
			ts := nostr.Timestamp(int64(v))
			filter.Since = ts
		}
		if v, ok := args["until"].(float64); ok && v > 0 {
			ts := nostr.Timestamp(int64(v))
			filter.Until = ts
		}

		// Topic filter via d-tag.
		if topic != "" {
			filter.Tags = nostr.TagMap{"d": []string{topic}}
		}

		// Use NIP-50 search if query provided and no topic.
		if query != "" && topic == "" {
			filter.Search = query
		}

		timeoutSec := 10
		if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
			timeoutSec = int(v)
		}
		ctx2, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()

		var memories []map[string]any
		seen := make(map[string]bool)
		for re := range pool.SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
			id := re.Event.ID.Hex()
			if seen[id] {
				continue
			}
			seen[id] = true

			m := map[string]any{
				"id":         id,
				"created_at": int64(re.Event.CreatedAt),
				"kind":       int(re.Event.Kind),
				"content":    re.Event.Content,
			}
			// Extract topic/d-tag.
			for _, tag := range re.Event.Tags {
				if len(tag) >= 2 && tag[0] == "d" {
					m["topic"] = tag[1]
				}
			}
			// Client-side content filter if NIP-50 not supported.
			if query != "" && filter.Search == "" {
				if !strings.Contains(strings.ToLower(re.Event.Content), strings.ToLower(query)) {
					continue
				}
			}
			memories = append(memories, m)
			if len(memories) >= limit {
				break
			}
		}

		out, _ := json.Marshal(map[string]any{
			"query":    query,
			"memories": memories,
			"count":    len(memories),
		})
		return string(out), nil
	}, RelayRecallDef)

	// relay_forget – delete a remembered event via NIP-09.
	tools.RegisterWithDef("relay_forget", func(ctx context.Context, args map[string]any) (string, error) {
		eventID, _ := args["event_id"].(string)
		if eventID == "" {
			return "", fmt.Errorf("relay_forget: event_id is required")
		}
		relays := opts.Relays
		if custom := toStringSlice(args["relays"]); len(custom) > 0 {
			relays = custom
		}

		delEvt := nostr.Event{
			Kind:      5,
			CreatedAt: nostr.Now(),
			Tags:      nostr.Tags{{"e", eventID}},
			Content:   "forgotten",
		}
		if err := signEvent(ctx, &delEvt); err != nil {
			return "", fmt.Errorf("relay_forget: sign: %w", err)
		}

		published := false
		for result := range pool.PublishMany(ctx, relays, delEvt) {
			if result.Error == nil {
				published = true
			}
		}
		if !published {
			return "", fmt.Errorf("relay_forget: no relay accepted deletion")
		}

		out, _ := json.Marshal(map[string]any{
			"ok":              true,
			"deleted_event":   eventID,
			"deletion_event":  delEvt.ID.Hex(),
		})
		return string(out), nil
	}, RelayForgetDef)
}
