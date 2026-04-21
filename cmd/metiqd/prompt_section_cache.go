package main

// prompt_section_cache.go implements per-agent system prompt section caching.
//
// Most system prompt sections (runtime info, model aliases, TTS hints, sandbox,
// skills, docs, bootstrap prompt) are stable between turns for a given agent.
// Only the time section changes every turn. Recomputing stable sections on
// every turn has no effect on the actual prompt content but defeats provider
// prompt caching: if even one byte changes in the static system prompt prefix,
// the provider must re-process the entire prefix at full input-token cost.
//
// This cache stores the fully assembled static system prompt per agent ID and
// invalidates on config change (detected via generation counter), /clear,
// /compact, or explicit cache clear. Volatile sections (time) are excluded
// from the cached output and appended at the call site.
//
// Ported from src/constants/systemPromptSections.ts.

import (
	"sync"
	"sync/atomic"
)

// promptSectionCacheEntry stores a cached static system prompt for one agent.
type promptSectionCacheEntry struct {
	// staticSystemPrompt is the fully assembled static portion (bootstrap +
	// agent system prompt + runtime static context). Excludes volatile
	// sections like time.
	staticSystemPrompt string
	// contextWindowTokens is the resolved context window for the agent.
	contextWindowTokens int
	// configGeneration is the config generation counter at cache time.
	// When the global counter advances, the entry is stale.
	configGeneration uint64
}

// promptSectionCache caches assembled static system prompts per agent.
// Entries are invalidated when the config generation changes or when
// clearPromptSectionCache is called (on /clear, /compact).
type promptSectionCache struct {
	mu      sync.RWMutex
	entries map[string]promptSectionCacheEntry
}

var (
	globalPromptSectionCache = &promptSectionCache{
		entries: make(map[string]promptSectionCacheEntry),
	}
	// promptConfigGeneration is bumped whenever the config changes or
	// an event occurs that should invalidate cached prompt sections.
	promptConfigGeneration atomic.Uint64
)

// get returns a cached entry if it exists and its config generation matches.
func (c *promptSectionCache) get(agentID string, currentGen uint64) (promptSectionCacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[agentID]
	if !ok || entry.configGeneration != currentGen {
		return promptSectionCacheEntry{}, false
	}
	return entry, true
}

// set stores a cache entry.
func (c *promptSectionCache) set(agentID string, entry promptSectionCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[agentID] = entry
}

// clear removes all cached entries. Called on /clear, /compact, config reload.
func (c *promptSectionCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]promptSectionCacheEntry)
}

// clearPromptSectionCache invalidates all cached system prompt sections.
// Call this on /clear, /compact, config reload, or any event that changes
// the system prompt content.
func clearPromptSectionCache() {
	promptConfigGeneration.Add(1)
	globalPromptSectionCache.clear()
}

// bumpPromptConfigGeneration increments the generation counter without
// clearing the cache. The next get() call for any agent will miss,
// triggering recomputation. Use this when the config may have changed
// but you don't want to eagerly clear all entries.
func bumpPromptConfigGeneration() {
	promptConfigGeneration.Add(1)
}
