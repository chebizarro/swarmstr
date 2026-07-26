// Package channels — bot_loop_guard.go ports OpenClaw's core bot-loop pair
// guard (openclaw/src/plugin-sdk/pair-loop-guard-runtime.ts +
// src/channels/turn/bot-loop-protection.ts) into the Metiq harness.
//
// Nostr has no platform-level bot identity, so fleet agents sharing a NIP-29
// room can reply to each other O(n^2). This guard is Layer 2 of loop control:
// after the allowBots gate (Layer 1) admits a known-bot turn, the guard keys on
// the UNORDERED pair (A->B and B->A are the same pair) and applies a
// sliding-window budget + cooldown so a runaway bot-to-bot exchange is
// suppressed.
//
// TRUST BOUNDARY: this is loop-control only. It must never influence
// allow_from / command authorization. Only ADMITTED known-bot turns are
// recorded; humans/unknowns and gated bots never enter the guard, so they never
// consume a pair budget.
package channels

import (
	"sync"
	"time"
)

// Pair-guard defaults (parity table §8).
const (
	DefaultPairLoopEnabled     = true
	DefaultPairLoopMaxEvents   = 20
	DefaultPairLoopWindow      = 60 * time.Second
	DefaultPairLoopCooldown    = 60 * time.Second
	defaultPairPruneInterval   = 60 * time.Second
	defaultIdempotencyCacheCap = 512
	pairKeySeparator           = "\x01"
)

// PairLoopGuardConfig is the user-facing config accepted from room/account
// settings (durations in seconds). Nil fields defer to the next precedence
// level.
type PairLoopGuardConfig struct {
	Enabled            *bool
	MaxEventsPerWindow *int
	WindowSeconds      *int
	CooldownSeconds    *int
}

// PairLoopGuardSettings are the resolved runtime thresholds.
type PairLoopGuardSettings struct {
	Enabled            bool
	MaxEventsPerWindow int
	Window             time.Duration
	Cooldown           time.Duration
}

// PairLoopGuardResult reports whether a recorded interaction is suppressed.
type PairLoopGuardResult struct {
	Suppressed    bool
	CooldownUntil time.Time
}

// PairLoopGuardSnapshotEntry is a diagnostic view of one tracked pair.
type PairLoopGuardSnapshotEntry struct {
	Key           string
	RecentCount   int
	CooldownUntil time.Time
}

func positiveInt(v *int) (int, bool) {
	if v == nil || *v <= 0 {
		return 0, false
	}
	return *v, true
}

// ResolvePairLoopGuardSettings resolves runtime settings with precedence
// config > defaultsConfig > built-in defaults. A channel-level capability gate
// (defaultEnabled) can force protection off even when config/defaults enable it.
func ResolvePairLoopGuardSettings(config, defaultsConfig *PairLoopGuardConfig, defaultEnabled bool) PairLoopGuardSettings {
	enabled := DefaultPairLoopEnabled
	switch {
	case config != nil && config.Enabled != nil:
		enabled = *config.Enabled
	case defaultsConfig != nil && defaultsConfig.Enabled != nil:
		enabled = *defaultsConfig.Enabled
	}

	maxEvents := DefaultPairLoopMaxEvents
	if v, ok := positiveInt(cfgMaxEvents(config)); ok {
		maxEvents = v
	} else if v, ok := positiveInt(cfgMaxEvents(defaultsConfig)); ok {
		maxEvents = v
	}

	windowSeconds := int(DefaultPairLoopWindow / time.Second)
	if v, ok := positiveInt(cfgWindow(config)); ok {
		windowSeconds = v
	} else if v, ok := positiveInt(cfgWindow(defaultsConfig)); ok {
		windowSeconds = v
	}

	cooldownSeconds := int(DefaultPairLoopCooldown / time.Second)
	if v, ok := positiveInt(cfgCooldown(config)); ok {
		cooldownSeconds = v
	} else if v, ok := positiveInt(cfgCooldown(defaultsConfig)); ok {
		cooldownSeconds = v
	}

	return PairLoopGuardSettings{
		Enabled:            defaultEnabled && enabled,
		MaxEventsPerWindow: maxEvents,
		Window:             time.Duration(windowSeconds) * time.Second,
		Cooldown:           time.Duration(cooldownSeconds) * time.Second,
	}
}

func cfgMaxEvents(c *PairLoopGuardConfig) *int {
	if c == nil {
		return nil
	}
	return c.MaxEventsPerWindow
}
func cfgWindow(c *PairLoopGuardConfig) *int {
	if c == nil {
		return nil
	}
	return c.WindowSeconds
}
func cfgCooldown(c *PairLoopGuardConfig) *int {
	if c == nil {
		return nil
	}
	return c.CooldownSeconds
}

// MergePairLoopGuardConfig merges configs from broad defaults to narrow
// overrides, ignoring nil fields. Returns nil when nothing was set.
func MergePairLoopGuardConfig(configs ...*PairLoopGuardConfig) *PairLoopGuardConfig {
	merged := PairLoopGuardConfig{}
	has := false
	for _, c := range configs {
		if c == nil {
			continue
		}
		if c.Enabled != nil {
			merged.Enabled = c.Enabled
			has = true
		}
		if c.MaxEventsPerWindow != nil {
			merged.MaxEventsPerWindow = c.MaxEventsPerWindow
			has = true
		}
		if c.WindowSeconds != nil {
			merged.WindowSeconds = c.WindowSeconds
			has = true
		}
		if c.CooldownSeconds != nil {
			merged.CooldownSeconds = c.CooldownSeconds
			has = true
		}
	}
	if !has {
		return nil
	}
	return &merged
}

type pairEntry struct {
	recent        []time.Time
	window        time.Duration
	cooldownStart time.Time
	cooldownUntil time.Time
}

// PairLoopGuard is an in-memory guard for suppressing repeated bidirectional
// bot pair loops. Safe for concurrent use.
type PairLoopGuard struct {
	mu            sync.Mutex
	tracked       map[string]*pairEntry
	pruneInterval time.Duration
	nextPruneAt   time.Time
	now           func() time.Time
}

// PairLoopGuardOptions configure a PairLoopGuard.
type PairLoopGuardOptions struct {
	// PruneInterval bounds periodic pruning of inactive pairs. Default 60s.
	PruneInterval time.Duration
	// Now is an injectable clock for tests. Default time.Now.
	Now func() time.Time
}

// NewPairLoopGuard creates an in-memory pair-loop guard with bounded pruning.
func NewPairLoopGuard(opts PairLoopGuardOptions) *PairLoopGuard {
	if opts.PruneInterval == 0 {
		opts.PruneInterval = defaultPairPruneInterval
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &PairLoopGuard{
		tracked:       make(map[string]*pairEntry),
		pruneInterval: opts.PruneInterval,
		now:           opts.Now,
	}
}

func buildPairKey(scopeID, conversationID, senderID, receiverID string) string {
	// Sort sender/receiver so A->B and B->A count as the same loop pair.
	lhs, rhs := senderID, receiverID
	if rhs < lhs {
		lhs, rhs = rhs, lhs
	}
	return scopeID + pairKeySeparator + conversationID + pairKeySeparator + lhs + pairKeySeparator + rhs
}

func pruneRecent(entry *pairEntry, now time.Time, window time.Duration) {
	cutoff := now.Add(-window)
	kept := entry.recent[:0]
	for _, ts := range entry.recent {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	entry.recent = kept
}

func (g *PairLoopGuard) pruneInactiveLocked(now time.Time) {
	if g.pruneInterval <= 0 || now.Before(g.nextPruneAt) {
		return
	}
	g.nextPruneAt = now.Add(g.pruneInterval)
	for key, entry := range g.tracked {
		pruneRecent(entry, now, entry.window)
		if len(entry.recent) == 0 && !entry.cooldownUntil.After(now) {
			delete(g.tracked, key)
		}
	}
}

// RecordAndCheckParams carry one sender/receiver interaction and its resolved
// thresholds.
type RecordAndCheckParams struct {
	ScopeID        string
	ConversationID string
	SenderID       string
	ReceiverID     string
	Settings       PairLoopGuardSettings
}

// RecordAndCheck records one interaction and reports whether it enters or is
// inside cooldown.
func (g *PairLoopGuard) RecordAndCheck(p RecordAndCheckParams) PairLoopGuardResult {
	if !p.Settings.Enabled {
		return PairLoopGuardResult{}
	}
	if p.ScopeID == "" || p.ConversationID == "" || p.SenderID == "" || p.ReceiverID == "" {
		return PairLoopGuardResult{}
	}
	if p.SenderID == p.ReceiverID {
		return PairLoopGuardResult{}
	}
	maxEvents := p.Settings.MaxEventsPerWindow
	window := p.Settings.Window
	cooldown := p.Settings.Cooldown
	if maxEvents <= 0 || window <= 0 || cooldown <= 0 {
		return PairLoopGuardResult{}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	g.pruneInactiveLocked(now)

	key := buildPairKey(p.ScopeID, p.ConversationID, p.SenderID, p.ReceiverID)
	entry := g.tracked[key]
	if entry == nil {
		entry = &pairEntry{window: window}
		g.tracked[key] = entry
	}

	// Inside an active cooldown window -> suppress.
	if !entry.cooldownStart.After(now) && entry.cooldownUntil.After(now) {
		return PairLoopGuardResult{Suppressed: true, CooldownUntil: entry.cooldownUntil}
	}

	entry.window = window
	pruneRecent(entry, now, window)
	entry.recent = append(entry.recent, now)

	// Count events in the current window. The budget is exceeded when the count
	// is STRICTLY GREATER than maxEventsPerWindow (so with a budget of 20, the
	// 21st event in the window trips the cooldown).
	count := 0
	for _, ts := range entry.recent {
		if !ts.After(now) {
			count++
		}
	}
	if count > maxEvents {
		entry.cooldownStart = now
		entry.cooldownUntil = now.Add(cooldown)
		// Keep only future records during cooldown; past events should not
		// extend suppression.
		kept := entry.recent[:0]
		for _, ts := range entry.recent {
			if ts.After(now) {
				kept = append(kept, ts)
			}
		}
		entry.recent = kept
		return PairLoopGuardResult{Suppressed: true, CooldownUntil: entry.cooldownUntil}
	}

	return PairLoopGuardResult{}
}

// Clear drops all tracked pair state.
func (g *PairLoopGuard) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tracked = make(map[string]*pairEntry)
	g.nextPruneAt = time.Time{}
}

// Snapshot returns tracked pair counters for diagnostics/tests.
func (g *PairLoopGuard) Snapshot() []PairLoopGuardSnapshotEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]PairLoopGuardSnapshotEntry, 0, len(g.tracked))
	for key, entry := range g.tracked {
		out = append(out, PairLoopGuardSnapshotEntry{
			Key:           key,
			RecentCount:   len(entry.recent),
			CooldownUntil: entry.cooldownUntil,
		})
	}
	return out
}

// ─── Idempotency wrapper ──────────────────────────────────────────────────────

// BotLoopProtectionFacts are the harness-level facts threaded into a prepared
// turn. scopeId = account id, conversationId = room key, senderId = sender bot
// pubkey, receiverId = own pubkey. EventID keys the replay-idempotency cache.
type BotLoopProtectionFacts struct {
	ScopeID        string
	ConversationID string
	SenderID       string
	ReceiverID     string
	Config         *PairLoopGuardConfig
	DefaultsConfig *PairLoopGuardConfig
	DefaultEnabled bool
	// EventID identifies the inbound event for replay idempotency. At-least-once
	// delivery + seen-rollback can redeliver the same event; the guard must
	// count it once.
	EventID string
}

// BotLoopProtection wraps the pair guard with a bounded per-account decision
// cache keyed by scopeId+conversationId+eventId, so a redelivered event replays
// its first decision instead of re-recording (which would double-count the pair
// budget). Caching a not-suppressed decision is correct: the event is one
// logical interaction, counted once.
type BotLoopProtection struct {
	guard *PairLoopGuard

	mu       sync.Mutex
	idem     map[string]PairLoopGuardResult
	idemFIFO []string
	idemCap  int
}

// NewBotLoopProtection creates the process-level bot-loop protection wrapper.
// A cap <= 0 uses the default idempotency-cache size (512).
func NewBotLoopProtection(guard *PairLoopGuard, idempotencyCap int) *BotLoopProtection {
	if guard == nil {
		guard = NewPairLoopGuard(PairLoopGuardOptions{})
	}
	if idempotencyCap <= 0 {
		idempotencyCap = defaultIdempotencyCacheCap
	}
	return &BotLoopProtection{
		guard:   guard,
		idem:    make(map[string]PairLoopGuardResult),
		idemCap: idempotencyCap,
	}
}

// RecordAndCheck resolves settings, replays a cached decision for a
// previously-seen event, or records the interaction and caches the decision.
func (b *BotLoopProtection) RecordAndCheck(facts BotLoopProtectionFacts) PairLoopGuardResult {
	// No event id: no idempotency possible; record directly.
	if facts.EventID == "" {
		return b.record(facts)
	}
	idemKey := facts.ScopeID + pairKeySeparator + facts.ConversationID + pairKeySeparator + facts.EventID

	// Hold b.mu across the cache lookup, the recording, and the insertion so a
	// concurrent redelivery of the same event cannot both miss the cache and
	// double-record the pair budget. b.mu is always acquired BEFORE the guard's
	// own mutex (b.record -> guard.RecordAndCheck), and the guard never calls
	// back into BotLoopProtection, so there is no lock-order inversion.
	b.mu.Lock()
	defer b.mu.Unlock()
	if cached, ok := b.idem[idemKey]; ok {
		return cached
	}
	result := b.record(facts)
	b.idem[idemKey] = result
	b.idemFIFO = append(b.idemFIFO, idemKey)
	for len(b.idemFIFO) > b.idemCap {
		evict := b.idemFIFO[0]
		b.idemFIFO = b.idemFIFO[1:]
		delete(b.idem, evict)
	}
	return result
}

func (b *BotLoopProtection) record(facts BotLoopProtectionFacts) PairLoopGuardResult {
	settings := ResolvePairLoopGuardSettings(facts.Config, facts.DefaultsConfig, facts.DefaultEnabled)
	return b.guard.RecordAndCheck(RecordAndCheckParams{
		ScopeID:        facts.ScopeID,
		ConversationID: facts.ConversationID,
		SenderID:       facts.SenderID,
		ReceiverID:     facts.ReceiverID,
		Settings:       settings,
	})
}

// Guard exposes the underlying pair guard (diagnostics/tests).
func (b *BotLoopProtection) Guard() *PairLoopGuard { return b.guard }
