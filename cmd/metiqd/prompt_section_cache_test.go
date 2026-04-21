package main

import (
	"testing"
)

func TestPromptSectionCache_GetMissOnEmpty(t *testing.T) {
	cache := &promptSectionCache{
		entries: make(map[string]promptSectionCacheEntry),
	}
	_, ok := cache.get("agent1", 0)
	if ok {
		t.Error("expected cache miss on empty cache")
	}
}

func TestPromptSectionCache_SetAndGet(t *testing.T) {
	cache := &promptSectionCache{
		entries: make(map[string]promptSectionCacheEntry),
	}
	entry := promptSectionCacheEntry{
		staticSystemPrompt:  "You are a helpful assistant.",
		contextWindowTokens: 200000,
		configGeneration:    1,
	}
	cache.set("agent1", entry)

	got, ok := cache.get("agent1", 1)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.staticSystemPrompt != entry.staticSystemPrompt {
		t.Errorf("got %q, want %q", got.staticSystemPrompt, entry.staticSystemPrompt)
	}
	if got.contextWindowTokens != entry.contextWindowTokens {
		t.Errorf("got %d, want %d", got.contextWindowTokens, entry.contextWindowTokens)
	}
}

func TestPromptSectionCache_StaleOnGenerationMismatch(t *testing.T) {
	cache := &promptSectionCache{
		entries: make(map[string]promptSectionCacheEntry),
	}
	cache.set("agent1", promptSectionCacheEntry{
		staticSystemPrompt:  "old prompt",
		contextWindowTokens: 100000,
		configGeneration:    1,
	})

	// Different generation → cache miss.
	_, ok := cache.get("agent1", 2)
	if ok {
		t.Error("expected cache miss when generation doesn't match")
	}
}

func TestPromptSectionCache_Clear(t *testing.T) {
	cache := &promptSectionCache{
		entries: make(map[string]promptSectionCacheEntry),
	}
	cache.set("agent1", promptSectionCacheEntry{
		staticSystemPrompt: "prompt1",
		configGeneration:   1,
	})
	cache.set("agent2", promptSectionCacheEntry{
		staticSystemPrompt: "prompt2",
		configGeneration:   1,
	})

	cache.clear()

	if _, ok := cache.get("agent1", 1); ok {
		t.Error("expected miss after clear")
	}
	if _, ok := cache.get("agent2", 1); ok {
		t.Error("expected miss after clear")
	}
}

func TestPromptSectionCache_MultipleAgents(t *testing.T) {
	cache := &promptSectionCache{
		entries: make(map[string]promptSectionCacheEntry),
	}
	cache.set("main", promptSectionCacheEntry{
		staticSystemPrompt: "main prompt",
		configGeneration:   5,
	})
	cache.set("worker", promptSectionCacheEntry{
		staticSystemPrompt: "worker prompt",
		configGeneration:   5,
	})

	got1, ok1 := cache.get("main", 5)
	got2, ok2 := cache.get("worker", 5)
	if !ok1 || !ok2 {
		t.Fatal("expected both agents to hit cache")
	}
	if got1.staticSystemPrompt != "main prompt" {
		t.Errorf("main got %q", got1.staticSystemPrompt)
	}
	if got2.staticSystemPrompt != "worker prompt" {
		t.Errorf("worker got %q", got2.staticSystemPrompt)
	}
}

func TestClearPromptSectionCache_BumpsGeneration(t *testing.T) {
	// Use the global cache for this test.
	prevGen := promptConfigGeneration.Load()

	globalPromptSectionCache.set("test-agent", promptSectionCacheEntry{
		staticSystemPrompt: "cached",
		configGeneration:   prevGen,
	})

	// Verify hit before clear.
	if _, ok := globalPromptSectionCache.get("test-agent", prevGen); !ok {
		t.Fatal("expected hit before clear")
	}

	clearPromptSectionCache()

	newGen := promptConfigGeneration.Load()
	if newGen <= prevGen {
		t.Errorf("expected generation to increase: %d → %d", prevGen, newGen)
	}

	// Old generation → miss.
	if _, ok := globalPromptSectionCache.get("test-agent", prevGen); ok {
		t.Error("expected miss after clear (old generation)")
	}
	// New generation → also miss (cache was cleared).
	if _, ok := globalPromptSectionCache.get("test-agent", newGen); ok {
		t.Error("expected miss after clear (new generation)")
	}
}

func TestBumpPromptConfigGeneration_InvalidatesWithoutClearing(t *testing.T) {
	gen := promptConfigGeneration.Load()

	globalPromptSectionCache.set("bump-test", promptSectionCacheEntry{
		staticSystemPrompt: "cached",
		configGeneration:   gen,
	})

	bumpPromptConfigGeneration()
	newGen := promptConfigGeneration.Load()

	// Entry still exists in the map...
	globalPromptSectionCache.mu.RLock()
	_, exists := globalPromptSectionCache.entries["bump-test"]
	globalPromptSectionCache.mu.RUnlock()
	if !exists {
		t.Error("expected entry to still exist in map after bump")
	}

	// ...but get() returns miss because generation changed.
	if _, ok := globalPromptSectionCache.get("bump-test", newGen); ok {
		t.Error("expected miss after generation bump (entry has old gen)")
	}

	// Clean up.
	globalPromptSectionCache.clear()
}
