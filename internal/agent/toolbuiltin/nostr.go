// Package toolbuiltin nostr.go — agent tools for direct Nostr network access.
//
// These tools give the agent first-class ability to query, publish, and send
// messages on the Nostr network, addressing the core gap of being reactive-only.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"

	"swarmstr/internal/agent"
	nostruntime "swarmstr/internal/nostr/runtime"
)

// NostrToolOpts holds the shared credentials and default relay list for all
// Nostr agent tools.
type NostrToolOpts struct {
	// Keyer is the signing interface used for all event signing.
	// This is required in all modes (plain-key and bunker).
	Keyer nostr.Keyer
	// Relays is the default relay list used when the tool caller doesn't specify.
	Relays []string
	// DMTransport is used by NostrSendDMTool to deliver DMs. May be nil
	// (in which case the tool returns an error).
	DMTransport nostruntime.DMTransport
}

// resolveRelays returns the caller-supplied list or falls back to opts.Relays.
func (o NostrToolOpts) resolveRelays(override []string) []string {
	if len(override) > 0 {
		return override
	}
	return o.Relays
}

// PoolOptsNIP42 returns PoolOptions with full NIP-42 authentication support.
// Both AuthHandler (reactive AUTH challenge signing) and AuthRequiredHandler
// (retry after "auth-required:" CLOSED/OK responses) are wired to the keyer.
// If o.Keyer is nil, returns plain PenaltyBox-only options.
func (o NostrToolOpts) PoolOptsNIP42() nostr.PoolOptions {
	return nostruntime.PoolOptsNIP42(o.Keyer)
}

// NewPoolNIP42 is a convenience that creates a new Pool with full NIP-42 support.
func (o NostrToolOpts) NewPoolNIP42() *nostr.Pool {
	return nostruntime.NewPoolNIP42(o.Keyer)
}

// signerFunc returns a function that signs a nostr event using the configured Keyer.
func (o NostrToolOpts) signerFunc() (func(ctx context.Context, evt *nostr.Event) error, error) {
	if o.Keyer == nil {
		return nil, fmt.Errorf("signing keyer not configured")
	}
	return func(ctx context.Context, evt *nostr.Event) error {
		return o.Keyer.SignEvent(ctx, evt)
	}, nil
}

// ─── nostr_fetch ──────────────────────────────────────────────────────────────

// NostrFetchTool returns an agent tool that fetches events from relays matching
// a NIP-01 filter.
//
// Parameters (JSON object):
//   - filter     object  — NIP-01 filter (kinds, authors, ids, #e, #p, since, until)
//   - relays     []string — relay URLs (optional, overrides configured relays)
//   - limit      int     — max events to return (default 20, max 100)
//   - timeout_seconds int (default 10)
// NostrFetchDef is the ToolDefinition for nostr_fetch.
var NostrFetchDef = agent.ToolDefinition{
	Name:        "nostr_fetch",
	Description: "Fetch Nostr events using a NIP-01 filter. Returns matching events as JSON. Use to read notes, profiles, lists, DMs, and any other Nostr content. Tag filters use '#' prefix (e.g. '#d' for d-tag, '#p' for p-tag).",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"kinds": {
				Type:        "array",
				Description: "Event kind numbers, e.g. [1] for short notes, [0] for profiles, [4] for DMs, [30000] for categorized people lists.",
				Items:       &agent.ToolParamProp{Type: "integer"},
			},
			"authors": {
				Type:        "array",
				Description: "Filter by author hex pubkeys or npubs.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"ids": {
				Type:        "array",
				Description: "Filter by event IDs (hex).",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"limit": {
				Type:        "integer",
				Description: "Maximum number of events to return (default 10, max 100).",
			},
			"since": {
				Type:        "integer",
				Description: "Unix timestamp: only return events after this time.",
			},
			"until": {
				Type:        "integer",
				Description: "Unix timestamp: only return events before this time.",
			},
			"tag_d": {
				Type:        "array",
				Description: "Filter by d-tag values (NIP-01 #d filter). Used for parameterized replaceable events, e.g. kind:30000 lists. Example: [\"cascadia-agents\"]",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"tag_p": {
				Type:        "array",
				Description: "Filter by p-tag pubkeys (NIP-01 #p filter). Events tagged with specific pubkeys.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"tag_e": {
				Type:        "array",
				Description: "Filter by e-tag event IDs (NIP-01 #e filter). Events referencing specific events.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"tag_t": {
				Type:        "array",
				Description: "Filter by t-tag topic/hashtag values (NIP-01 #t filter).",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"relays": {
				Type:        "array",
				Description: "Optional relay URLs to query (overrides defaults).",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
	},
}

// NostrPublishDef is the ToolDefinition for nostr_publish.
var NostrPublishDef = agent.ToolDefinition{
	Name:        "nostr_publish",
	Description: "Publish a signed Nostr event to the configured relays. Use to post notes (kind 1), reactions, articles, or any other Nostr event kind.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"kind": {
				Type:        "integer",
				Description: "Nostr event kind number (e.g. 1 for short text note).",
			},
			"content": {
				Type:        "string",
				Description: "The event content (text, JSON, etc.).",
			},
			"tags": {
				Type:        "array",
				Description: "Optional NIP-01 tags as array-of-arrays, e.g. [[\"e\",\"<event-id>\"], [\"p\",\"<pubkey>\"]].",
				Items:       &agent.ToolParamProp{Type: "array"},
			},
		},
		Required: []string{"kind", "content"},
	},
}

// NostrSendDMDef is the ToolDefinition for nostr_send_dm.
var NostrSendDMDef = agent.ToolDefinition{
	Name:        "nostr_send_dm",
	Description: "Send an encrypted direct message to a Nostr user via NIP-17 (or NIP-04 fallback). Use for direct communication with known pubkeys.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"to": {
				Type:        "string",
				Description: "Recipient hex pubkey or npub.",
			},
			"message": {
				Type:        "string",
				Description: "The message text to send.",
			},
			"encryption": {
				Type:        "string",
				Description: "Optional encryption mode preference: auto|nip17|nip44|giftwrap|nip04.",
			},
		},
		Required: []string{"to", "message"},
	},
}

func NostrFetchTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		timeoutSec := 10
		if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
			timeoutSec = int(v)
		}
		ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()

		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			if int(v) < 100 {
				limit = int(v)
			} else {
				limit = 100
			}
		}

		// Parse relay list from args.
		var overrideRelays []string
		if rv, ok := args["relays"]; ok {
			overrideRelays = toStringSlice(rv)
		}
		relays := opts.resolveRelays(overrideRelays)
		if len(relays) == 0 {
			return "", fmt.Errorf("nostr_fetch: no relays configured")
		}

		// Build NIP-01 filter directly from top-level tool arguments to match
		// NostrFetchDef (kinds/authors/ids/since/until/#tags).
		f, err := buildNostrFilter(args, limit)
		if err != nil {
			return "", fmt.Errorf("nostr_fetch: invalid filter: %w", err)
		}

		pool := opts.NewPoolNIP42()
		defer pool.Close("fetch done")

		sub := pool.SubscribeMany(ctx, relays, f, nostr.SubscriptionOptions{})
		var events []map[string]any
		for re := range sub {
			events = append(events, eventToMap(re.Event))
			if len(events) >= limit {
				break
			}
		}

		out, _ := json.Marshal(events)
		return string(out), nil
	}
}

// ─── nostr_publish ────────────────────────────────────────────────────────────

// NostrPublishTool returns an agent tool that signs and publishes a Nostr event.
//
// Parameters:
//   - kind    int    — event kind (required)
//   - content string — event content
//   - tags    [][]string — optional tags array
//   - relays  []string  — optional relay override
func NostrPublishTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		// Build signer: prefer Keyer (supports NIP-46), fall back to raw private key.
		signFn, err := opts.signerFunc()
		if err != nil {
			return "", fmt.Errorf("nostr_publish: %w", err)
		}

		kindVal, ok := args["kind"].(float64)
		if !ok {
			return "", fmt.Errorf("nostr_publish: kind (int) is required")
		}

		content, _ := args["content"].(string)

		// Parse tags: accept [][]string or [][]interface{}.
		var tags nostr.Tags
		if tagsRaw, ok := args["tags"]; ok {
			var parseErr error
			tags, parseErr = parseTagsArg(tagsRaw)
			if parseErr != nil {
				return "", fmt.Errorf("nostr_publish: invalid tags: %w", parseErr)
			}
		}

		var overrideRelays []string
		if rv, ok := args["relays"]; ok {
			overrideRelays = toStringSlice(rv)
		}
		relays := opts.resolveRelays(overrideRelays)
		if len(relays) == 0 {
			return "", fmt.Errorf("nostr_publish: no relays configured")
		}

		evt := nostr.Event{
			Kind:      nostr.Kind(int(kindVal)),
			Content:   content,
			Tags:      tags,
			CreatedAt: nostr.Timestamp(time.Now().Unix()),
		}
		if err := signFn(ctx, &evt); err != nil {
			return "", fmt.Errorf("nostr_publish: sign: %w", err)
		}

		ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		pool := opts.NewPoolNIP42()
		defer pool.Close("publish done")

		var lastErr error
		published := 0
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
			return "", fmt.Errorf("nostr_publish: failed on all relays: %w", lastErr)
		}

		result := map[string]any{
			"id":         evt.ID.Hex(),
			"pubkey":     evt.PubKey.Hex(),
			"kind":       evt.Kind,
			"created_at": int64(evt.CreatedAt),
			"published":  published,
		}
		out, _ := json.Marshal(result)
		return string(out), nil
	}
}

// ─── nostr_send_dm ────────────────────────────────────────────────────────────

// NostrSendDMTool returns an agent tool that sends a NIP-17 DM to a pubkey.
//
// Parameters:
//   - to_pubkey string — hex pubkey or npub (required)
//   - text      string — message text (required)
func NostrSendDMTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		if opts.DMTransport == nil {
			return "", fmt.Errorf("nostr_send_dm: DM transport not available")
		}
		// Accept "to" (schema name) or "to_pubkey" (legacy) for the recipient.
		toPubKey, _ := args["to"].(string)
		if toPubKey == "" {
			toPubKey, _ = args["to_pubkey"].(string)
		}
		if toPubKey == "" {
			return "", fmt.Errorf("nostr_send_dm: to (recipient pubkey or npub) is required")
		}
		// Accept "message" (schema name) or "text" (legacy) for the body.
		text, _ := args["message"].(string)
		if text == "" {
			text, _ = args["text"].(string)
		}
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("nostr_send_dm: message is required")
		}

		// Resolve npub → hex.
		toPubKey, err := resolveNostrPubkey(toPubKey)
		if err != nil {
			return "", fmt.Errorf("nostr_send_dm: %w", err)
		}

		ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		encryption := strings.ToLower(strings.TrimSpace(argString(args, "encryption")))
		if encryption != "" {
			if sender, ok := opts.DMTransport.(interface {
				SendDMWithScheme(ctx context.Context, toPubKey string, text string, scheme string) error
			}); ok {
				if err := sender.SendDMWithScheme(ctx2, toPubKey, text, encryption); err != nil {
					return "", fmt.Errorf("nostr_send_dm: %w", err)
				}
			} else {
				transportType := fmt.Sprintf("%T", opts.DMTransport)
				return "", fmt.Errorf("nostr_send_dm: transport %s does not support explicit encryption selection (use default encryption)", transportType)
			}
		} else {
			if err := opts.DMTransport.SendDM(ctx2, toPubKey, text); err != nil {
				return "", fmt.Errorf("nostr_send_dm: %w", err)
			}
		}
		out, _ := json.Marshal(map[string]any{"sent": true, "to": toPubKey, "encryption": encryption})
		return string(out), nil
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// buildNostrFilter converts a map[string]any (from agent args) into a nostr.Filter.
func buildNostrFilter(m map[string]any, limit int) (nostr.Filter, error) {
	f := nostr.Filter{}

	if m == nil {
		f.Limit = limit
		return f, nil
	}

	// kinds
	if kinds, ok := m["kinds"]; ok {
		for _, k := range toFloat64Slice(kinds) {
			f.Kinds = append(f.Kinds, nostr.Kind(int(k)))
		}
	}
	// authors
	if authors, ok := m["authors"]; ok {
		for _, a := range toStringSlice(authors) {
			pk, err := resolveNostrPubkey(a)
			if err == nil {
				f.Authors = append(f.Authors, mustPubKey(pk))
			}
		}
	}
	// ids
	if ids, ok := m["ids"]; ok {
		for _, id := range toStringSlice(ids) {
			f.IDs = append(f.IDs, mustEventID(id))
		}
	}
	// since / until
	if v, ok := m["since"].(float64); ok {
		ts := nostr.Timestamp(int64(v))
		f.Since = ts
	}
	if v, ok := m["until"].(float64); ok {
		ts := nostr.Timestamp(int64(v))
		f.Until = ts
	}
	// tag filters: accept both "#d" style (relay-native) and "tag_d" style (schema-safe)
	tagMap := nostr.TagMap{}
	for k, v := range m {
		if strings.HasPrefix(k, "#") {
			tagMap[k[1:]] = toStringSlice(v)
		} else if strings.HasPrefix(k, "tag_") && len(k) == 5 {
			tagMap[k[4:]] = toStringSlice(v)
		}
	}
	if len(tagMap) > 0 {
		f.Tags = tagMap
	}

	f.Limit = limit
	return f, nil
}

// eventToMap converts a nostr.Event to a plain map for JSON serialization.
func eventToMap(ev nostr.Event) map[string]any {
	tags := make([][]string, 0, len(ev.Tags))
	for _, t := range ev.Tags {
		tags = append(tags, []string(t))
	}
	return map[string]any{
		"id":         ev.ID.Hex(),
		"pubkey":     ev.PubKey.Hex(),
		"kind":       int(ev.Kind),
		"content":    ev.Content,
		"created_at": int64(ev.CreatedAt),
		"tags":       tags,
	}
}

// resolveNostrPubkey converts npub/hex to hex, returning error on failure.
func resolveNostrPubkey(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "npub") {
		prefix, value, err := nip19.Decode(raw)
		if err != nil {
			return "", fmt.Errorf("decode npub: %w", err)
		}
		if prefix != "npub" {
			return "", fmt.Errorf("expected npub, got %s", prefix)
		}
		pk, ok := value.(nostr.PubKey)
		if !ok {
			return "", fmt.Errorf("unexpected npub value type")
		}
		return pk.Hex(), nil
	}
	return raw, nil
}

func mustPubKey(hex string) nostr.PubKey {
	pk, _ := nostr.PubKeyFromHex(hex)
	return pk
}

func mustEventID(hex string) nostr.ID {
	id, _ := nostr.IDFromHex(hex)
	return id
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{t}
	}
	return nil
}

func toFloat64Slice(v any) []float64 {
	switch t := v.(type) {
	case []float64:
		return t
	case []any:
		out := make([]float64, 0, len(t))
		for _, item := range t {
			if f, ok := item.(float64); ok {
				out = append(out, f)
			}
		}
		return out
	case float64:
		return []float64{t}
	}
	return nil
}

func parseTagsArg(v any) (nostr.Tags, error) {
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("tags must be an array")
	}
	tags := make(nostr.Tags, 0, len(raw))
	for _, row := range raw {
		rowSlice, ok := row.([]any)
		if !ok {
			continue
		}
		var tag nostr.Tag
		for _, cell := range rowSlice {
			s, ok := cell.(string)
			if !ok {
				s = fmt.Sprintf("%v", cell)
			}
			tag = append(tag, s)
		}
		if len(tag) > 0 {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}
