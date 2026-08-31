// Package runtime – TransportSelector: composite DMTransport with FIPS + relay fallback.
//
// TransportSelector wraps a FIPS transport and a relay-based transport (NIP-04
// or NIP-17), routing outbound messages according to a configured preference.
// It satisfies DMTransport so callers (ACP, fleet RPC, control bus) can use it
// as a drop-in replacement without any code changes.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// TransportPref enumerates the routing strategies for TransportSelector.
const (
	// TransportPrefFIPSFirst optimistically tries FIPS, then falls back to relay
	// only when the FIPS error is a transport/path failure.
	TransportPrefFIPSFirst = "fips-first"
	// TransportPrefRelayFirst uses relay by default, then attempts FIPS only when
	// the relay send fails and FIPS is not in the negative failure cache.
	TransportPrefRelayFirst = "relay-first"
	// TransportPrefFIPSOnly uses FIPS exclusively — no relay fallback.
	TransportPrefFIPSOnly = "fips-only"
)

// ReachabilityChecker observes whether a pubkey is known reachable over FIPS.
// It is advisory only; TransportSelector does not synchronously gate sends on it.
type ReachabilityChecker func(pubkeyHex string) bool

// FIPSDaemonStateChecker reports the local daemon lifecycle state. It is used
// only for diagnosis and transport selection, never for ACP task completion.
type FIPSDaemonStateChecker func(context.Context) (string, error)

// TransportSelectorOptions configures a TransportSelector.
type TransportSelectorOptions struct {
	// FIPS is the FIPS mesh transport. May be nil if FIPS is not available.
	FIPS DMTransport
	// Relay is the relay-based DM transport (NIP-17 or NIP-04). May be nil
	// if running in fips-only mode.
	Relay DMTransport
	// Pref is the routing preference (fips-first, relay-first, fips-only).
	Pref string
	// Reachable is retained for advisory/control-query compatibility. It is not
	// consulted by the send path because FIPS discovery is asynchronous.
	Reachable ReachabilityChecker
	// ReachCacheTTL controls how long FIPS transport failures suppress optimistic
	// FIPS attempts for a peer. Default: 30s.
	ReachCacheTTL time.Duration
	// DaemonState optionally reports the local FIPS daemon lifecycle state.
	// Degraded, Failed, and Draining states make FIPS ineligible for selection.
	DaemonState FIPSDaemonStateChecker
	// OnFallback is called when a send falls back from the preferred transport.
	// Optional; used for observability / logging.
	OnFallback func(toPubKey string, preferredTransport string, err error)
}

// TransportSelector is a composite DMTransport that routes messages through
// FIPS or relay transports based on a configured preference and learned FIPS
// transport failures.
type TransportSelector struct {
	fips  DMTransport
	relay DMTransport
	pref  string

	// reachable is advisory only and intentionally not used to gate SendDM.
	reachable   ReachabilityChecker
	daemonState FIPSDaemonStateChecker
	onFallback  func(toPubKey string, preferredTransport string, err error)

	failureMu sync.RWMutex
	failures  map[string]fipsFailureState
	cacheTTL  time.Duration
}

type fipsFailureState struct {
	Until   time.Time
	Reason  FIPSErrorKind
	LastErr error
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
		fips:        opts.FIPS,
		relay:       opts.Relay,
		pref:        pref,
		reachable:   opts.Reachable,
		daemonState: opts.DaemonState,
		onFallback:  opts.OnFallback,
		failures:    make(map[string]fipsFailureState),
		cacheTTL:    cacheTTL,
	}, nil
}

// ── DMTransport interface ─────────────────────────────────────────────────────

// SendDM routes the message through the preferred transport, falling back
// to the alternate transport only when that is valid for the selected mode and
// FIPS error classification.
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
	if ts.fips == nil {
		if ts.relay != nil {
			return ts.relay.SendDM(ctx, toPubKey, text)
		}
		return fmt.Errorf("transport selector: no transport available")
	}

	if healthErr := ts.fipsHealthError(ctx); healthErr != nil {
		if ts.relay != nil {
			ts.emitFallback(toPubKey, "fips", healthErr)
			return ts.relay.SendDM(ctx, toPubKey, text)
		}
		return healthErr
	}

	if state, ok := ts.activeFIPSFailure(toPubKey); ok {
		cachedErr := cachedFIPSFailureError(toPubKey, state)
		if ts.relay != nil {
			ts.emitFallback(toPubKey, "fips", cachedErr)
			return ts.relay.SendDM(ctx, toPubKey, text)
		}
		return cachedErr
	}

	err := ts.fips.SendDM(ctx, toPubKey, text)
	if err == nil {
		ts.clearFIPSFailure(toPubKey)
		return nil
	}
	fallback, cache := fipsFallbackDecision(ctx, err)
	if !fallback {
		return err
	}

	if cache {
		ts.cacheFIPSFailure(toPubKey, err)
	}
	if ts.relay != nil {
		ts.emitFallback(toPubKey, "fips", err)
		return ts.relay.SendDM(ctx, toPubKey, text)
	}
	return fmt.Errorf("fips send failed and no relay fallback: %w", err)
}

func (ts *TransportSelector) sendRelayFirst(ctx context.Context, toPubKey string, text string) error {
	if ts.relay == nil {
		return fmt.Errorf("transport selector: relay transport not available")
	}

	relayErr := ts.relay.SendDM(ctx, toPubKey, text)
	if relayErr == nil {
		return nil
	}
	if ctxErr := contextFailure(ctx, relayErr); ctxErr != nil {
		return ctxErr
	}
	if ts.fips == nil {
		return relayErr
	}
	if ts.fipsHealthError(ctx) != nil {
		return relayErr
	}
	if _, ok := ts.activeFIPSFailure(toPubKey); ok {
		return relayErr
	}

	ts.emitFallback(toPubKey, "relay", relayErr)
	fipsErr := ts.fips.SendDM(ctx, toPubKey, text)
	if fipsErr == nil {
		ts.clearFIPSFailure(toPubKey)
		return nil
	}
	fallback, cache := fipsFallbackDecision(ctx, fipsErr)
	if !fallback {
		return fipsErr
	}

	if cache {
		ts.cacheFIPSFailure(toPubKey, fipsErr)
	}
	return fmt.Errorf("relay send failed and fips fallback failed: relay error: %v; fips error: %w", relayErr, fipsErr)
}

func (ts *TransportSelector) sendFIPSOnly(ctx context.Context, toPubKey string, text string) error {
	if ts.fips == nil {
		return fmt.Errorf("transport selector: fips-only mode but no FIPS transport")
	}
	if healthErr := ts.fipsHealthError(ctx); healthErr != nil {
		return healthErr
	}
	if state, ok := ts.activeFIPSFailure(toPubKey); ok {
		return cachedFIPSFailureError(toPubKey, state)
	}

	err := ts.fips.SendDM(ctx, toPubKey, text)
	if err == nil {
		ts.clearFIPSFailure(toPubKey)
		return nil
	}
	_, cache := fipsFallbackDecision(ctx, err)
	if cache {
		ts.cacheFIPSFailure(toPubKey, err)
	}
	return err
}

func (ts *TransportSelector) fipsHealthError(ctx context.Context) error {
	if ts.daemonState == nil {
		return nil
	}
	state, err := ts.daemonState(ctx)
	if err != nil {
		// A missing diagnostic socket must not turn an otherwise usable mesh path
		// into a hard failure. Only explicit daemon lifecycle states gate FIPS.
		log.Printf("transport selector: FIPS daemon status unavailable: %v", err)
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "degraded", "failed", "draining":
		return fmt.Errorf("fips daemon lifecycle state %q is not selectable", state)
	default:
		return nil
	}
}

// ── FIPS failure cache ────────────────────────────────────────────────────────

func (ts *TransportSelector) activeFIPSFailure(pubkey string) (fipsFailureState, bool) {
	now := time.Now()

	ts.failureMu.RLock()
	entry, ok := ts.failures[pubkey]
	ts.failureMu.RUnlock()
	if !ok {
		return fipsFailureState{}, false
	}
	if now.Before(entry.Until) {
		return entry, true
	}

	ts.failureMu.Lock()
	// Re-check under the write lock so a concurrent send can refresh the entry.
	entry, ok = ts.failures[pubkey]
	// Recapture time for fresh timestamp in deletion check
	now = time.Now()
	if ok && !now.Before(entry.Until) {
		delete(ts.failures, pubkey)
		ok = false
	}
	ts.failureMu.Unlock()
	if ok {
		return entry, true
	}
	return fipsFailureState{}, false
}

func (ts *TransportSelector) cacheFIPSFailure(pubkey string, err error) {
	until := time.Now().Add(ts.cacheTTL)
	state := fipsFailureState{
		Until:   until,
		Reason:  fipsErrorReason(err),
		LastErr: err,
	}

	ts.failureMu.Lock()
	if existing, ok := ts.failures[pubkey]; ok && existing.Until.After(until) {
		state.Until = existing.Until
	}
	ts.failures[pubkey] = state
	ts.failureMu.Unlock()
}

func (ts *TransportSelector) clearFIPSFailure(pubkey string) {
	ts.failureMu.Lock()
	delete(ts.failures, pubkey)
	ts.failureMu.Unlock()
}

// ClearReachabilityCache evicts all learned FIPS failure entries.
// Kept for API compatibility with callers that clear selector network state.
func (ts *TransportSelector) ClearReachabilityCache() {
	ts.failureMu.Lock()
	ts.failures = make(map[string]fipsFailureState)
	ts.failureMu.Unlock()
}

func cachedFIPSFailureError(pubkey string, state fipsFailureState) error {
	if state.LastErr != nil {
		return fmt.Errorf("fips send to %s skipped until %s due to cached %s failure: %w",
			truncatePubkey(pubkey), state.Until.Format(time.RFC3339), state.Reason, state.LastErr)
	}
	return fmt.Errorf("fips send to %s skipped until %s due to cached %s failure",
		truncatePubkey(pubkey), state.Until.Format(time.RFC3339), state.Reason)
}

func fipsFallbackDecision(ctx context.Context, err error) (fallback bool, cache bool) {
	if err == nil {
		return false, false
	}
	if contextFailure(ctx, err) != nil {
		return false, false
	}

	var fipsErr *FIPSError
	if errors.As(err, &fipsErr) {
		if fipsErr.AllowFallback() {
			return true, true
		}
		return false, false
	}

	// Real FIPSTransport errors should be classified. Preserve relay fallback
	// compatibility for unclassified legacy/mock errors, but do not poison the
	// per-peer negative cache without an explicit transport classification.
	return true, false
}

func contextFailure(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return nil
}

func fipsErrorReason(err error) FIPSErrorKind {
	var fipsErr *FIPSError
	if errors.As(err, &fipsErr) && fipsErr.Kind != "" {
		return fipsErr.Kind
	}
	return FIPSErrorKindTransport
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

// ReachabilityCacheSize returns the number of learned FIPS failure entries.
func (ts *TransportSelector) ReachabilityCacheSize() int {
	ts.failureMu.RLock()
	defer ts.failureMu.RUnlock()
	return len(ts.failures)
}

var _ DMTransport = (*TransportSelector)(nil)
