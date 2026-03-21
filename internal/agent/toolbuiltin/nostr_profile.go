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
			return "", fmt.Errorf("nostr_profile: pubkey is required")
		}
		pubkeyHex, err := resolveNostrPubkey(rawPubkey)
		if err != nil {
			return "", fmt.Errorf("nostr_profile: %w", err)
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
			return "", fmt.Errorf("nostr_profile: no relays configured")
		}

		ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		pk, err := nostr.PubKeyFromHex(pubkeyHex)
		if err != nil {
			return "", fmt.Errorf("nostr_profile: invalid pubkey: %w", err)
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
