// Package runtime — profile.go provides typed NIP-01 kind:0 profile content
// (including the NIP-24 `bot` flag) and an identity-bound profile fetch used by
// the peer-agent index for bot-to-bot loop control.
//
// TRUST BOUNDARY: the `bot` flag and any verdict derived from it are
// LOOP-CONTROL ONLY. They must never influence allow_from or command
// authorization.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	nostr "fiatjaf.com/nostr"
)

// ProfileContent mirrors NIP-01 kind:0 metadata content plus the NIP-24 `bot`
// flag. Bot is a pointer so an absent flag ("not declared") is distinguishable
// from an explicit false; both are treated as not-a-bot by IsBot.
type ProfileContent struct {
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	About       string `json:"about,omitempty"`
	Picture     string `json:"picture,omitempty"`
	Banner      string `json:"banner,omitempty"`
	Website     string `json:"website,omitempty"`
	NIP05       string `json:"nip05,omitempty"`
	LUD16       string `json:"lud16,omitempty"`
	// Bot is the NIP-24 bot flag. Only an explicit `bot: true` marks an
	// automated agent; nil or false is not-a-bot.
	Bot *bool `json:"bot,omitempty"`
}

// IsBot reports the NIP-24 bot flag. An absent flag reports false: only an
// explicit `bot: true` marks an automated agent.
func (p ProfileContent) IsBot() bool {
	return p.Bot != nil && *p.Bot
}

// ParseProfileContent parses the JSON content of a kind:0 event into typed
// profile fields.
func ParseProfileContent(content string) (ProfileContent, error) {
	var pc ProfileContent
	if err := json.Unmarshal([]byte(content), &pc); err != nil {
		return ProfileContent{}, fmt.Errorf("parse profile content: %w", err)
	}
	return pc, nil
}

// BoundProfile is the winning identity-bound kind:0 profile for a pubkey.
type BoundProfile struct {
	PubKey    nostr.PubKey
	EventID   string
	CreatedAt nostr.Timestamp
	Content   ProfileContent
}

// selectIdentityBoundProfile picks the newest signature-valid kind:0 event whose
// author is exactly pubkey from a set of candidate events.
//
// SECURITY-LOAD-BEARING: a malicious or nonconforming relay can return any event
// for a subscription, so never rely on the relay/library to enforce the
// kind/author filter. Every candidate is re-bound to the requested identity here
// (kind, author, id, signature, future-skew) via profileMetadataValidationFailure.
// Otherwise a spoofed kind:0 carrying `bot: true` for a human's key could poison
// the peer index and cause the harness to damp a human as if they were a bot.
func selectIdentityBoundProfile(events []nostr.Event, pubkey nostr.PubKey) (nostr.Event, bool) {
	var best *nostr.Event
	for i := range events {
		ev := events[i]
		if profileMetadataValidationFailure(ev, pubkey) != "" {
			continue
		}
		// NIP-01 replaceable-event semantics: newest created_at wins; on an
		// equal timestamp the lexicographically lower event id is the
		// tie-breaker, so the verdict is deterministic regardless of relay
		// arrival order.
		if best == nil ||
			ev.CreatedAt > best.CreatedAt ||
			(ev.CreatedAt == best.CreatedAt && ev.ID.Hex() < best.ID.Hex()) {
			cp := ev
			best = &cp
		}
	}
	if best == nil {
		return nostr.Event{}, false
	}
	return *best, true
}

// FetchIdentityBoundProfile fetches the newest signature-valid kind:0 profile
// bound to pubkey from the given relays.
//
// Returns (profile, true, nil) when a valid matching event was found;
// (_, false, nil) when no valid matching event was returned by any relay —
// callers treat this as an "unknown" verdict (fail-open, never a bot verdict);
// (_, false, err) only on a setup error (nil pool / no relays).
func FetchIdentityBoundProfile(ctx context.Context, pool *nostr.Pool, relays []string, pubkey nostr.PubKey) (BoundProfile, bool, error) {
	if pool == nil {
		return BoundProfile{}, false, fmt.Errorf("fetch profile: pool is required")
	}
	if len(relays) == 0 {
		return BoundProfile{}, false, fmt.Errorf("fetch profile: at least one relay is required")
	}

	f := nostr.Filter{
		Kinds:   []nostr.Kind{0},
		Authors: []nostr.PubKey{pubkey},
		Limit:   1,
	}

	var candidates []nostr.Event
	for re := range pool.FetchMany(ctx, relays, f, nostr.SubscriptionOptions{}) {
		candidates = append(candidates, re.Event)
	}

	best, ok := selectIdentityBoundProfile(candidates, pubkey)
	if !ok {
		return BoundProfile{}, false, nil
	}

	// Content parse failures do not undo identity binding: a signature-valid
	// kind:0 with unreadable content is still a real profile — it simply
	// declares no bot flag (treated as not-a-bot, fail-open toward human).
	content, _ := ParseProfileContent(best.Content)

	return BoundProfile{
		PubKey:    best.PubKey,
		EventID:   best.ID.Hex(),
		CreatedAt: best.CreatedAt,
		Content:   content,
	}, true, nil
}

// EnsureProfileBotFlag returns a copy of the profile map with the NIP-24 `bot`
// flag set to the given value. Nostr-first agents should publish `bot: true`
// so peers can damp bot-to-bot loops. The flag is loop-control only and never
// affects authorization.
func EnsureProfileBotFlag(profile map[string]any, bot bool) map[string]any {
	out := cloneProfileMap(profile)
	if out == nil {
		out = map[string]any{}
	}
	out["bot"] = bot
	return out
}
