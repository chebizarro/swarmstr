package main

import (
	"log"
	"strings"

	ctxengine "metiq/internal/context"
)

// ─── Post-compact cleanup ─────────────────────────────────────────────────────
//
// Centralized cleanup after both auto-compact and manual /compact. Resets
// accumulated state that would otherwise cause the session to immediately
// re-bloat after compaction.
//
// Ported from src/services/compact/postCompactCleanup.ts.
//
// What it clears:
//   - docsSectionCache: force rebuild of cached docs sections
//   - autocompact circuit breaker: reset on success
//   - session memory compact state: clear last-summarized tracking
//
// What it preserves:
//   - Session memory file: must survive across compactions so the next
//     session memory extraction has a baseline to update
//   - Skills prompt: rebuilt each call, no persistent state to clear

// runPostCompactCleanup is called after successful compaction.
// It clears accumulated caches and state that would re-bloat the session.
func runPostCompactCleanup(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}

	// 1. Clear prompt section caches — force rebuild on next assemble.
	clearDocsSectionCache()
	clearPromptSectionCache()

	// 2. Reset autocompact circuit breaker on success.
	if controlAutoCompactState != nil {
		controlAutoCompactState.RecordSuccess(sessionID)
	}

	log.Printf("post-compact cleanup session=%s", sessionID)
}

// clearDocsSectionCache purges the TTL cache for buildDocsSectionCached.
// After compaction, the system prompt should be rebuilt fresh to avoid
// carrying stale docs content that was computed for a larger context.
func clearDocsSectionCache() {
	docsSectionCacheMu.Lock()
	for k := range docsSectionCache {
		delete(docsSectionCache, k)
	}
	docsSectionCacheMu.Unlock()
}

// runPostCompactCleanupWithState is a variant that accepts the circuit
// breaker state explicitly (for testing or when the global isn't available).
func runPostCompactCleanupWithState(sessionID string, autoCompactState *ctxengine.AutoCompactState) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}

	clearDocsSectionCache()
	clearPromptSectionCache()

	if autoCompactState != nil {
		autoCompactState.RecordSuccess(sessionID)
	}
}
