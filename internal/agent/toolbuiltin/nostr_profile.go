// Package toolbuiltin nostr_profile.go — profile resolver and NIP-05 lookup tools.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
)

// ─── kind:0 profile cache ─────────────────────────────────────────────────────

type profileCacheEntry struct {
	data      map[string]any
	fetchedAt time.Time
}

var (
	profileCacheMu  sync.Mutex
	profileCache    = map[string]profileCacheEntry{}
	profileCacheTTL = 10 * time.Minute
)

func cachedProfile(pubkeyHex string) (map[string]any, bool) {
	profileCacheMu.Lock()
	defer profileCacheMu.Unlock()
	e, ok := profileCache[pubkeyHex]
	if !ok || time.Since(e.fetchedAt) > profileCacheTTL {
		return nil, false
	}
	return e.data, true
}

func storeProfile(pubkeyHex string, data map[string]any) {
	profileCacheMu.Lock()
	defer profileCacheMu.Unlock()
	profileCache[pubkeyHex] = profileCacheEntry{data: data, fetchedAt: time.Now()}
}

func canonicalNIP05Identifier(ident string) string {
	parts := strings.SplitN(strings.TrimSpace(ident), "@", 2)
	if len(parts) != 2 {
		return strings.TrimSpace(ident)
	}
	local := strings.ToLower(strings.TrimSpace(parts[0]))
	domain := strings.ToLower(strings.TrimSpace(parts[1]))
	return local + "@" + domain
}

// ─── nostr_profile ────────────────────────────────────────────────────────────

// NostrProfileTool returns an agent tool that fetches a Nostr profile (kind:0).
//
// Parameters:
//   - pubkey  string   — hex pubkey or npub (required)
//   - relays  []string — optional relay override
func NostrProfileTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		rawPubkey, _ := args["pubkey"].(string)
		if rawPubkey == "" {
			return "", nostrToolErr("nostr_profile", "invalid_input", "pubkey is required", nil)
		}
		pubkeyHex, err := resolveNostrPubkey(rawPubkey)
		if err != nil {
			return "", nostrToolErr("nostr_profile", "invalid_input", err.Error(), nil)
		}

		if cached, ok := cachedProfile(pubkeyHex); ok {
			out, _ := json.Marshal(cached)
			return string(out), nil
		}

		var overrideRelays []string
		if rv, ok := args["relays"]; ok {
			overrideRelays = toStringSlice(rv)
		}
		relays := opts.resolveRelays(overrideRelays)
		if len(relays) == 0 {
			return "", nostrToolErr("nostr_profile", "no_relays", "no relays configured", nil)
		}

		ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		pk, err := nostr.PubKeyFromHex(pubkeyHex)
		if err != nil {
			return "", nostrToolErr("nostr_profile", "invalid_input", fmt.Sprintf("invalid pubkey: %v", err), nil)
		}

		pool, releasePool := opts.AcquirePool("profile done")
		defer releasePool()

		f := nostr.Filter{
			Kinds:   []nostr.Kind{0},
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
			return `{"error":"profile not found"}`, nil
		}

		var meta map[string]any
		if err := json.Unmarshal([]byte(best.Content), &meta); err != nil {
			meta = map[string]any{}
		}

		result := map[string]any{
			"pubkey_hex": pubkeyHex,
			"name":       meta["name"],
			"about":      meta["about"],
			"nip05":      meta["nip05"],
			"lud16":      meta["lud16"],
			"picture":    meta["picture"],
			"created_at": int64(best.CreatedAt),
		}
		storeProfile(pubkeyHex, result)
		out, _ := json.Marshal(result)
		return string(out), nil
	}
}

// invalidateProfile removes a cached profile so the next fetch gets fresh data.
func invalidateProfile(pubkeyHex string) {
	profileCacheMu.Lock()
	defer profileCacheMu.Unlock()
	delete(profileCache, pubkeyHex)
}

// ─── nostr_profile_set ────────────────────────────────────────────────────────

// NostrProfileSetDef is the ToolDefinition for nostr_profile_set.
var NostrProfileSetDef = agent.ToolDefinition{
	Name:        "nostr_profile_set",
	Description: "Update your Nostr profile (kind:0 metadata) by merging fields. Fetches current profile, merges provided fields, signs and publishes. Only provided fields are changed; omitted fields are preserved.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"name":       {Type: "string", Description: "Display name"},
			"about":      {Type: "string", Description: "Bio / about text"},
			"picture":    {Type: "string", Description: "Profile picture URL"},
			"banner":     {Type: "string", Description: "Banner image URL"},
			"nip05":      {Type: "string", Description: "NIP-05 identifier (user@domain)"},
			"lud16":      {Type: "string", Description: "Lightning address (user@domain)"},
			"display_name": {Type: "string", Description: "Display name (NIP-24 extended)"},
			"website":    {Type: "string", Description: "Website URL"},
			"bot":        {Type: "boolean", Description: "Whether this is a bot account (NIP-24)"},
			"extra":      {Type: "object", Description: "Any additional key-value pairs to merge into the profile JSON"},
			"relays":     {Type: "array", Description: "Optional relay URLs (overrides defaults)", Items: &agent.ToolParamProp{Type: "string"}},
		},
	},
}

// NostrProfileSetTool returns an agent tool that atomically merges fields into
// the agent's kind:0 profile metadata.
func NostrProfileSetTool(opts NostrToolOpts) agent.ToolFunc {
	// Well-known profile fields that map directly from tool args to kind:0 JSON.
	knownFields := []string{"name", "about", "picture", "banner", "nip05", "lud16", "display_name", "website"}

	return func(ctx context.Context, args map[string]any) (string, error) {
		signFn, err := opts.signerFunc()
		if err != nil {
			return "", nostrToolErr("nostr_profile_set", "no_keyer", err.Error(), nil)
		}

		keyer := opts.ResolveKeyer()
		if keyer == nil {
			return "", nostrToolErr("nostr_profile_set", "no_keyer", "no keyer configured", nil)
		}
		pk, err := keyer.GetPublicKey(ctx)
		if err != nil {
			return "", nostrToolErr("nostr_profile_set", "no_keyer", fmt.Sprintf("get pubkey: %v", err), nil)
		}
		pubkeyHex := pk.Hex()

		var overrideRelays []string
		if rv, ok := args["relays"]; ok {
			overrideRelays = toStringSlice(rv)
		}
		relays := opts.resolveRelays(overrideRelays)
		if len(relays) == 0 {
			return "", nostrToolErr("nostr_profile_set", "no_relays", "no relays configured", nil)
		}

		// Step 1: Fetch current kind:0.
		pool, releasePool := opts.AcquirePool("profile_set done")
		defer releasePool()

		fetchCtx, fetchCancel := context.WithTimeout(ctx, 10*time.Second)
		defer fetchCancel()

		f := nostr.Filter{
			Kinds:   []nostr.Kind{0},
			Authors: []nostr.PubKey{pk},
			Limit:   1,
		}
		var best *nostr.Event
		for re := range pool.SubscribeMany(fetchCtx, relays, f, nostr.SubscriptionOptions{}) {
			ev := re.Event
			if best == nil || ev.CreatedAt > best.CreatedAt {
				cp := ev
				best = &cp
			}
		}

		// Step 2: Parse existing metadata (or start fresh).
		existing := make(map[string]any)
		if best != nil {
			_ = json.Unmarshal([]byte(best.Content), &existing)
		}

		// Step 3: Merge provided fields (only count actually-changed values).
		changed := 0
		for _, field := range knownFields {
			if v, ok := args[field]; ok {
				if s, ok := v.(string); ok {
					if existing[field] != s {
						existing[field] = s
						changed++
					}
				}
			}
		}
		// Handle "bot" boolean field.
		if v, ok := args["bot"]; ok {
			if b, ok := v.(bool); ok {
				if existing["bot"] != b {
					existing["bot"] = b
					changed++
				}
			}
		}
		// Merge extra fields.
		if extra, ok := args["extra"].(map[string]any); ok {
			for k, v := range extra {
				if existing[k] != v {
					existing[k] = v
					changed++
				}
			}
		}

		if changed == 0 {
			return "", nostrToolErr("nostr_profile_set", "invalid_input", "no fields provided to update", nil)
		}

		// Step 4: Sign and publish.
		content, _ := json.Marshal(existing)
		evt := nostr.Event{
			Kind:      0,
			Content:   string(content),
			CreatedAt: nostr.Timestamp(time.Now().Unix()),
		}
		if err := opts.checkOutboundEvent(&evt); err != nil {
			return "", nostrToolErr("nostr_profile_set", "content_blocked", err.Error(), nil)
		}
		if err := signFn(ctx, &evt); err != nil {
			return "", nostrToolErr("nostr_profile_set", "sign_failed", err.Error(), nil)
		}

		pubCtx, pubCancel := context.WithTimeout(ctx, 15*time.Second)
		defer pubCancel()

		published := 0
		var lastErr error
		for _, relayURL := range relays {
			r, rErr := pool.EnsureRelay(relayURL)
			if rErr != nil {
				lastErr = rErr
				continue
			}
			if pErr := r.Publish(pubCtx, evt); pErr != nil {
				lastErr = pErr
				continue
			}
			published++
		}
		if published == 0 && lastErr != nil {
			return "", nostrToolErr("nostr_profile_set", "publish_failed", lastErr.Error(), nil)
		}

		// Invalidate cache so next nostr_profile fetch gets the update.
		invalidateProfile(pubkeyHex)

		result := map[string]any{
			"id":             evt.ID.Hex(),
			"pubkey":         pubkeyHex,
			"fields_changed": changed,
			"published":      published,
			"profile":        existing,
		}
		out, _ := json.Marshal(result)
		return string(out), nil
	}
}

// ─── nostr_resolve_nip05 ──────────────────────────────────────────────────────

// NIP-05 well-known cache.
var (
	nip05CacheMu  sync.Mutex
	nip05Cache    = map[string]nip05CacheEntry{}
	nip05CacheTTL = 10 * time.Minute
)

type nip05CacheEntry struct {
	data      map[string]any
	fetchedAt time.Time
}

// NostrResolveNIP05Tool returns an agent tool that resolves a NIP-05 identifier.
//
// Parameters:
//   - identifier string — name@domain (required)
func NostrResolveNIP05Tool() agent.ToolFunc {
	client := &http.Client{Timeout: 10 * time.Second}
	return func(ctx context.Context, args map[string]any) (string, error) {
		ident, _ := args["identifier"].(string)
		if ident == "" {
			return "", fmt.Errorf("nostr_resolve_nip05: identifier is required")
		}
		canonicalIdent := canonicalNIP05Identifier(ident)
		parts := strings.SplitN(ident, "@", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("nostr_resolve_nip05: identifier must be name@domain")
		}
		name, domain := strings.ToLower(parts[0]), strings.ToLower(parts[1])

		nip05CacheMu.Lock()
		if e, ok := nip05Cache[canonicalIdent]; ok && time.Since(e.fetchedAt) < nip05CacheTTL {
			nip05CacheMu.Unlock()
			out, _ := json.Marshal(e.data)
			return string(out), nil
		}
		nip05CacheMu.Unlock()

		reqURL := fmt.Sprintf("https://%s/.well-known/nostr.json?name=%s",
			domain, url.QueryEscape(name))

		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("nostr_resolve_nip05: HTTP request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("nostr_resolve_nip05: server returned %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		var doc struct {
			Names  map[string]string   `json:"names"`
			Relays map[string][]string `json:"relays"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			return "", fmt.Errorf("nostr_resolve_nip05: invalid JSON: %w", err)
		}

		pubkeyHex, ok := doc.Names[name]
		if !ok {
			pubkeyHex, ok = doc.Names["_"]
		}
		if !ok {
			return `{"error":"name not found in NIP-05 document"}`, nil
		}

		result := map[string]any{
			"pubkey":     pubkeyHex,
			"identifier": canonicalIdent,
			"relays":     doc.Relays[pubkeyHex],
		}

		nip05CacheMu.Lock()
		nip05Cache[canonicalIdent] = nip05CacheEntry{data: result, fetchedAt: time.Now()}
		nip05CacheMu.Unlock()

		out, _ := json.Marshal(result)
		return string(out), nil
	}
}
