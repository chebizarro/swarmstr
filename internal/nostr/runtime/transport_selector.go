// Package runtime – TransportSelector: composite DMTransport with FIPS + relay fallback.
//
// TransportSelector wraps a FIPS transport and a relay-based transport (NIP-04
// or NIP-17), routing outbound messages according to a configured preference.
// It satisfies DMTransport so callers (ACP, fleet RPC, control bus) can use it
// as a drop-in replacement without any code changes.
package runtime

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// TransportPref enumerates the routing strategies for TransportSelector.
const (
	// TransportPrefFIPSFirst tries FIPS, falls back to relay on failure.
	TransportPrefFIPSFirst = "fips-first"
	// TransportPrefRelayFirst uses relay by default, FIPS only for explicitly reachable peers.
	TransportPrefRelayFirst = "relay-first"
	// TransportPrefFIPSOnly uses FIPS exclusively — no relay fallback.
	TransportPrefFIPSOnly = "fips-only"
)

// ReachabilityChecker tests whether a pubkey is reachable over FIPS.
// Returns true if the peer has an active FIPS session or is known-reachable.
type ReachabilityChecker func(pubkeyHex string) bool

// TransportSelectorOptions configures a TransportSelector.
type TransportSelectorOptions struct {
	// FIPS is the FIPS mesh transport. May be nil if FIPS is not available.
	FIPS DMTransport
	// Relay is the relay-based DM transport (NIP-17 or NIP-04). May be nil
	// if running in fips-only mode.
	Relay DMTransport
	// Pref is the routing preference (fips-first, relay-first, fips-only).
	Pref string
	// Reachable checks whether a destination pubkey is reachable via FIPS.
	// If nil, FIPS sends are always attempted (optimistic).
	Reachable ReachabilityChecker
	// ReachCacheTTL controls how long positive reachability results are cached.
	// Default: 30s.
	ReachCacheTTL time.Duration
	// OnFallback is called when a send falls back from the preferred transport.
	// Optional; used for observability / logging.
	OnFallback func(toPubKey string, preferredTransport string, err error)
}

// TransportSelector is a composite DMTransport that routes messages through
// FIPS or relay transports based on a configured preference and reachability.
type TransportSelector struct {
	fips  DMTransport
	relay DMTransport
	pref  string

	reachable  ReachabilityChecker
	onFallback func(toPubKey string, preferredTransport string, err error)

	// Reachability cache: pubkey → entry
	cacheMu  sync.RWMutex
	cache    map[string]reachCacheEntry
	cacheTTL time.Duration
}

type reachCacheEntry struct {
	reachable bool
	expiresAt time.Time
}

// NewTransportSelector creates a TransportSelector with the given options.
func NewTransportSelector(opts TransportSelectorOptions) (*TransportSelector, error) {
	pref := opts.Pref
	if pref == "" {
		pref = TransportPrefFIPSFirst
	}

	// Validate transports for the chosen preference.
	switch pref {
	case TransportPrefFIPSOnly:
		if opts.FIPS == nil {
			return nil, fmt.Errorf("transport selector: fips-only mode requires a FIPS transport")
		}
	case TransportPrefRelayFirst:
		if opts.Relay == nil {
			return nil, fmt.Errorf("transport selector: relay-first mode requires a relay transport")
		}
	case TransportPrefFIPSFirst:
		// At least one transport must be available.
		if opts.FIPS == nil && opts.Relay == nil {
			return nil, fmt.Errorf("transport selector: at least one transport is required")
		}
	default:
		return nil, fmt.Errorf("transport selector: unknown preference %q", pref)
	}

	cacheTTL := opts.ReachCacheTTL
	if cacheTTL <= 0 {
		cacheTTL = 30 * time.Second
	}

	return &TransportSelector{
		fips:       opts.FIPS,
		relay:      opts.Relay,
		pref:       pref,
		reachable:  opts.Reachable,
		onFallback: opts.OnFallback,
		cache:      make(map[string]reachCacheEntry),
		cacheTTL:   cacheTTL,
	}, nil
}

// ── DMTransport interface ─────────────────────────────────────────────────────

// SendDM routes the message through the preferred transport, falling back
// to the alternate transport on failure.
func (ts *TransportSelector) SendDM(ctx context.Context, toPubKey string, text string) error {
	switch ts.pref {
	case TransportPrefFIPSFirst:
		return ts.sendFIPSFirst(ctx, toPubKey, text)
	case TransportPrefRelayFirst:
		return ts.sendRelayFirst(ctx, toPubKey, text)
	case TransportPrefFIPSOnly:
		return ts.sendFIPSOnly(ctx, toPubKey, text)
	default:
		return fmt.Errorf("transport selector: unknown preference %q", ts.pref)
	}
}

// PublicKey returns the agent's public key from whichever transport is available.
func (ts *TransportSelector) PublicKey() string {
	if ts.fips != nil {
		return ts.fips.PublicKey()
	}
	if ts.relay != nil {
		return ts.relay.PublicKey()
	}
	return ""
}

// Relays delegates to the relay transport. FIPS has no relays.
func (ts *TransportSelector) Relays() []string {
	if ts.relay != nil {
		return ts.relay.Relays()
	}
	return nil
}

// SetRelays delegates to the relay transport.
func (ts *TransportSelector) SetRelays(relays []string) error {
	if ts.relay != nil {
		return ts.relay.SetRelays(relays)
	}
	return nil
}

// Close shuts down both underlying transports.
func (ts *TransportSelector) Close() {
	if ts.fips != nil {
		ts.fips.Close()
	}
	if ts.relay != nil {
		ts.relay.Close()
	}
}

// Pref returns the active routing preference.
func (ts *TransportSelector) Pref() string {
	return ts.pref
}

// HasFIPS returns true if a FIPS transport is configured.
func (ts *TransportSelector) HasFIPS() bool {
	return ts.fips != nil
}

// HasRelay returns true if a relay transport is configured.
func (ts *TransportSelector) HasRelay() bool {
	return ts.relay != nil
}

// ── Routing strategies ────────────────────────────────────────────────────────

func (ts *TransportSelector) sendFIPSFirst(ctx context.Context, toPubKey string, text string) error {
	// If FIPS is available and peer is reachable (or reachability unknown), try FIPS first.
	if ts.fips != nil && ts.isPeerFIPSReachable(toPubKey) {
		err := ts.fips.SendDM(ctx, toPubKey, text)
		if err == nil {
			// Successful FIPS send — cache positive reachability.
			ts.cacheReachability(toPubKey, true)
			return nil
		}

		// FIPS failed — cache negative reachability and fall back.
		ts.cacheReachability(toPubKey, false)
		ts.emitFallback(toPubKey, "fips", err)

		if ts.relay != nil {
			return ts.relay.SendDM(ctx, toPubKey, text)
		}
		return fmt.Errorf("fips send failed and no relay fallback: %w", err)
	}

	// FIPS not available or peer known-unreachable — use relay.
	if ts.relay != nil {
		return ts.relay.SendDM(ctx, toPubKey, text)
	}

	// Neither transport available.
	if ts.fips != nil {
		// FIPS exists but peer unreachable, and no relay. Try FIPS as last resort.
		return ts.fips.SendDM(ctx, toPubKey, text)
	}
	return fmt.Errorf("transport selector: no transport available")
}

func (ts *TransportSelector) sendRelayFirst(ctx context.Context, toPubKey string, text string) error {
	// In relay-first mode, only use FIPS for peers known to be reachable via FIPS.
	if ts.fips != nil && ts.isPeerExplicitlyReachable(toPubKey) {
		err := ts.fips.SendDM(ctx, toPubKey, text)
		if err == nil {
			return nil
		}
		// FIPS failed for explicitly-reachable peer — fall back to relay.
		ts.emitFallback(toPubKey, "fips", err)
	}

	if ts.relay != nil {
		return ts.relay.SendDM(ctx, toPubKey, text)
	}
	return fmt.Errorf("transport selector: relay transport not available")
}

func (ts *TransportSelector) sendFIPSOnly(ctx context.Context, toPubKey string, text string) error {
	if ts.fips == nil {
		return fmt.Errorf("transport selector: fips-only mode but no FIPS transport")
	}
	return ts.fips.SendDM(ctx, toPubKey, text)
}

// ── Reachability ──────────────────────────────────────────────────────────────

// isPeerFIPSReachable returns true if the peer is known-reachable or if
// reachability is unknown (optimistic for fips-first mode).
func (ts *TransportSelector) isPeerFIPSReachable(pubkey string) bool {
	// Check cache first.
	ts.cacheMu.RLock()
	entry, ok := ts.cache[pubkey]
	ts.cacheMu.RUnlock()

	if ok && time.Now().Before(entry.expiresAt) {
		return entry.reachable
	}

	// Cache miss or expired — check reachability function.
	if ts.reachable != nil {
		r := ts.reachable(pubkey)
		ts.cacheReachability(pubkey, r)
		return r
	}

	// No reachability checker — optimistically try FIPS.
	return true
}

// isPeerExplicitlyReachable returns true only if the reachability checker
// confirms the peer is reachable. Used in relay-first mode.
func (ts *TransportSelector) isPeerExplicitlyReachable(pubkey string) bool {
	// Check cache first.
	ts.cacheMu.RLock()
	entry, ok := ts.cache[pubkey]
	ts.cacheMu.RUnlock()

	if ok && time.Now().Before(entry.expiresAt) {
		return entry.reachable
	}

	// No reachability checker means we can't confirm — default to not reachable.
	if ts.reachable == nil {
		return false
	}

	r := ts.reachable(pubkey)
	ts.cacheReachability(pubkey, r)
	return r
}

func (ts *TransportSelector) cacheReachability(pubkey string, reachable bool) {
	ts.cacheMu.Lock()
	ts.cache[pubkey] = reachCacheEntry{
		reachable: reachable,
		expiresAt: time.Now().Add(ts.cacheTTL),
	}
	ts.cacheMu.Unlock()
}

// ClearReachabilityCache evicts all cached reachability entries.
// Useful when the network topology changes (e.g., new peer joins mesh).
func (ts *TransportSelector) ClearReachabilityCache() {
	ts.cacheMu.Lock()
	ts.cache = make(map[string]reachCacheEntry)
	ts.cacheMu.Unlock()
}

func (ts *TransportSelector) emitFallback(toPubKey, preferredTransport string, err error) {
	if ts.onFallback != nil {
		ts.onFallback(toPubKey, preferredTransport, err)
	}
	log.Printf("transport selector: %s send to %s failed, falling back: %v",
		preferredTransport, truncatePubkey(toPubKey), err)
}

func truncatePubkey(pk string) string {
	if len(pk) > 12 {
		return pk[:12] + "..."
	}
	return pk
}

// ── Compile-time interface check ──────────────────────────────────────────────

// Preference returns the routing preference string.
func (ts *TransportSelector) Preference() string {
	return ts.pref
}

// ReachabilityCacheSize returns the number of entries in the reachability cache.
func (ts *TransportSelector) ReachabilityCacheSize() int {
	ts.cacheMu.RLock()
	defer ts.cacheMu.RUnlock()
	return len(ts.cache)
}

var _ DMTransport = (*TransportSelector)(nil)
