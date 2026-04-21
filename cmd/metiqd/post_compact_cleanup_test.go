package main

import (
	"testing"
	"time"

	ctxengine "metiq/internal/context"
)

func TestClearDocsSectionCache(t *testing.T) {
	// Populate the cache.
	docsSectionCacheMu.Lock()
	docsSectionCache["ws1"] = ttlCacheEntry[string]{value: "docs content", expiresAt: time.Now().Add(5 * time.Minute)}
	docsSectionCache["ws2"] = ttlCacheEntry[string]{value: "more docs", expiresAt: time.Now().Add(5 * time.Minute)}
	docsSectionCacheMu.Unlock()

	clearDocsSectionCache()

	docsSectionCacheMu.Lock()
	remaining := len(docsSectionCache)
	docsSectionCacheMu.Unlock()

	if remaining != 0 {
		t.Errorf("expected empty cache after clear, got %d entries", remaining)
	}
}

func TestRunPostCompactCleanupWithState_ResetsCircuitBreaker(t *testing.T) {
	state := ctxengine.NewAutoCompactState()
	// Trigger the circuit breaker.
	for i := 0; i < ctxengine.DefaultMaxConsecutiveFailures; i++ {
		state.RecordFailure("sess1")
	}
	if !state.ShouldSkipCompaction("sess1") {
		t.Fatal("expected circuit to be open before cleanup")
	}

	runPostCompactCleanupWithState("sess1", state)

	if state.ShouldSkipCompaction("sess1") {
		t.Error("expected circuit to close after cleanup")
	}
}

func TestRunPostCompactCleanup_EmptySessionID(t *testing.T) {
	// Should not panic on empty session ID.
	runPostCompactCleanup("")
	runPostCompactCleanup("  ")
}
