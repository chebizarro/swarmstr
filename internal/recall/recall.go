// Package recall provides active memory recall for agent conversations.
//
// Before the agent processes a user message, the recall system searches the
// memory index for relevant historical context and returns a compact summary
// that can be injected into the agent's system prompt (via ExtraSystemPrompt).
//
// Features:
//   - TTL-based result cache to avoid redundant searches
//   - Per-session toggle (enable/disable active memory)
//   - Per-agent scoping
//   - Chat type filtering (direct, group, channel)
//   - Configurable prompt styles for recall relevance
//   - Recent turn extraction for query context
//   - "No useful result" detection to avoid injecting noise
//
// This is the swarmstr equivalent of openclaw's active-memory extension.
package recall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"metiq/internal/memory"
)

// ── Configuration ───────────────────────────────────────────────────────────

// DefaultTimeoutMS is the default timeout for a recall search.
const DefaultTimeoutMS = 15_000

// DefaultMaxSummaryChars is the max length of the recalled context summary.
const DefaultMaxSummaryChars = 220

// DefaultCacheTTL is how long recall results are cached.
const DefaultCacheTTL = 15 * time.Second

// DefaultMaxCacheEntries is the maximum number of cached recall results.
const DefaultMaxCacheEntries = 1000

// DefaultRecentUserTurns is how many recent user messages to include as query context.
const DefaultRecentUserTurns = 2

// DefaultRecentAssistantTurns is how many recent assistant messages to include.
const DefaultRecentAssistantTurns = 1

// DefaultRecentUserChars caps each user turn excerpt.
const DefaultRecentUserChars = 220

// DefaultRecentAssistantChars caps each assistant turn excerpt.
const DefaultRecentAssistantChars = 180

// DefaultSearchLimit is how many memory hits to examine.
const DefaultSearchLimit = 10

// PromptStyle controls how aggressively the recall engine returns results.
type PromptStyle string

const (
	PromptStyleBalanced       PromptStyle = "balanced"
	PromptStyleStrict         PromptStyle = "strict"
	PromptStyleContextual     PromptStyle = "contextual"
	PromptStyleRecallHeavy    PromptStyle = "recall-heavy"
	PromptStylePrecisionHeavy PromptStyle = "precision-heavy"
	PromptStylePreferenceOnly PromptStyle = "preference-only"
)

// ChatType classifies the conversation context for filtering.
type ChatType string

const (
	ChatTypeDirect  ChatType = "direct"
	ChatTypeGroup   ChatType = "group"
	ChatTypeChannel ChatType = "channel"
)

// Config controls the active recall engine's behaviour.
type Config struct {
	// Enabled globally enables/disables active recall.
	Enabled bool

	// Agents lists agent IDs that may use recall. Empty = all agents.
	Agents []string

	// AllowedChatTypes controls which session types may trigger recall.
	// Empty defaults to direct only.
	AllowedChatTypes []ChatType

	// PromptStyle controls recall aggressiveness.
	PromptStyle PromptStyle

	// MaxSummaryChars caps the output summary length.
	MaxSummaryChars int

	// TimeoutMS is the max time to spend on a recall search.
	TimeoutMS int

	// CacheTTL is how long recall results are cached.
	CacheTTL time.Duration

	// MaxCacheEntries caps the cache size.
	MaxCacheEntries int

	// SearchLimit is how many memory hits to examine per search.
	SearchLimit int

	// RecentUserTurns is how many recent user turns to include as search context.
	RecentUserTurns int

	// RecentAssistantTurns is how many recent assistant turns to include.
	RecentAssistantTurns int

	// RecentUserChars caps each user turn excerpt.
	RecentUserChars int

	// RecentAssistantChars caps each assistant turn excerpt.
	RecentAssistantChars int
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:              true,
		AllowedChatTypes:     []ChatType{ChatTypeDirect},
		PromptStyle:          PromptStyleBalanced,
		MaxSummaryChars:      DefaultMaxSummaryChars,
		TimeoutMS:            DefaultTimeoutMS,
		CacheTTL:             DefaultCacheTTL,
		MaxCacheEntries:      DefaultMaxCacheEntries,
		SearchLimit:          DefaultSearchLimit,
		RecentUserTurns:      DefaultRecentUserTurns,
		RecentAssistantTurns: DefaultRecentAssistantTurns,
		RecentUserChars:      DefaultRecentUserChars,
		RecentAssistantChars: DefaultRecentAssistantChars,
	}
}

// ── Turn representation ─────────────────────────────────────────────────────

// Turn represents a single conversational turn used for building recall context.
type Turn struct {
	Role string // "user" or "assistant"
	Text string
}

// ── Recall result ───────────────────────────────────────────────────────────

// Status describes the outcome of a recall attempt.
type Status string

const (
	StatusOK          Status = "ok"          // recall returned useful context
	StatusEmpty       Status = "empty"       // recall found nothing useful
	StatusTimeout     Status = "timeout"     // recall timed out
	StatusUnavailable Status = "unavailable" // memory store not available
	StatusDisabled    Status = "disabled"    // recall disabled for this context
	StatusSkipped     Status = "skipped"     // skipped (e.g., non-eligible session)
)

// Result is the output of a recall attempt.
type Result struct {
	// Status describes what happened.
	Status Status

	// Summary is the recalled memory context to inject into the prompt.
	// Empty when Status != StatusOK.
	Summary string

	// DurationMS is how long the recall took.
	DurationMS int64

	// Cached is true if this result came from cache.
	Cached bool

	// HitCount is the number of memory search hits examined.
	HitCount int
}

// ── No-recall value detection ───────────────────────────────────────────────

// noRecallValues is a set of strings that indicate the recall found nothing useful.
var noRecallValues = map[string]bool{
	"":                      true,
	"none":                  true,
	"no_reply":              true,
	"no reply":              true,
	"nothing useful":        true,
	"no relevant memory":    true,
	"no relevant memories":  true,
	"timeout":               true,
	"[]":                    true,
	"{}":                    true,
	"null":                  true,
	"n/a":                   true,
}

// isNoRecallValue returns true if the text represents a "nothing found" response.
func isNoRecallValue(text string) bool {
	return noRecallValues[strings.ToLower(strings.TrimSpace(text))]
}

// ── Cache ───────────────────────────────────────────────────────────────────

type cachedResult struct {
	result    Result
	expiresAt time.Time
}

type resultCache struct {
	mu      sync.Mutex
	entries map[string]cachedResult
	maxSize int
}

func newResultCache(maxSize int) *resultCache {
	if maxSize <= 0 {
		maxSize = DefaultMaxCacheEntries
	}
	return &resultCache{
		entries: make(map[string]cachedResult),
		maxSize: maxSize,
	}
}

func (c *resultCache) get(key string) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return Result{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return Result{}, false
	}
	return entry.result, true
}

func (c *resultCache) set(key string, result Result, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweepLocked()
	c.entries[key] = cachedResult{result: result, expiresAt: time.Now().Add(ttl)}
	// Evict oldest if over capacity.
	for len(c.entries) > c.maxSize {
		var oldest string
		var oldestTime time.Time
		for k, v := range c.entries {
			if oldest == "" || v.expiresAt.Before(oldestTime) {
				oldest = k
				oldestTime = v.expiresAt
			}
		}
		if oldest != "" {
			delete(c.entries, oldest)
		}
	}
}

func (c *resultCache) sweepLocked() {
	now := time.Now()
	for k, v := range c.entries {
		if now.After(v.expiresAt) {
			delete(c.entries, k)
		}
	}
}

func (c *resultCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cachedResult)
}

func (c *resultCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// ── Session toggles ─────────────────────────────────────────────────────────

type sessionToggles struct {
	mu       sync.RWMutex
	disabled map[string]bool // sessionKey → disabled
}

func newSessionToggles() *sessionToggles {
	return &sessionToggles{disabled: make(map[string]bool)}
}

func (s *sessionToggles) isDisabled(sessionKey string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.disabled[sessionKey]
}

func (s *sessionToggles) setDisabled(sessionKey string, disabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if disabled {
		s.disabled[sessionKey] = true
	} else {
		delete(s.disabled, sessionKey)
	}
}

// ── Engine ──────────────────────────────────────────────────────────────────

// MemorySearcher is the interface for searching the memory index.
type MemorySearcher interface {
	Search(query string, limit int) []memory.IndexedMemory
}

// Engine is the active recall engine. It searches memory before agent
// processing and caches results.
type Engine struct {
	cfg     Config
	store   MemorySearcher
	cache   *resultCache
	toggles *sessionToggles
}

// NewEngine creates an active recall engine with the given configuration
// and memory store.
func NewEngine(cfg Config, store MemorySearcher) *Engine {
	return &Engine{
		cfg:     cfg,
		store:   store,
		cache:   newResultCache(cfg.MaxCacheEntries),
		toggles: newSessionToggles(),
	}
}

// SetConfig hot-reloads the engine configuration.
func (e *Engine) SetConfig(cfg Config) {
	e.cfg = cfg
}

// SetEnabled toggles active memory for a specific session.
func (e *Engine) SetEnabled(sessionKey string, enabled bool) {
	e.toggles.setDisabled(sessionKey, !enabled)
}

// IsEnabled returns whether active memory is enabled for a session.
func (e *Engine) IsEnabled(sessionKey string) bool {
	return !e.toggles.isDisabled(sessionKey)
}

// ClearCache invalidates all cached recall results.
func (e *Engine) ClearCache() {
	e.cache.clear()
}

// CacheSize returns the number of cached recall results.
func (e *Engine) CacheSize() int {
	return e.cache.size()
}

// ── Recall execution ────────────────────────────────────────────────────────

// RecallRequest contains all context needed for an active recall attempt.
type RecallRequest struct {
	// AgentID is the agent performing the recall.
	AgentID string

	// SessionKey identifies the conversation session.
	SessionKey string

	// ChatType classifies the conversation (direct, group, channel).
	ChatType ChatType

	// LatestMessage is the most recent user message.
	LatestMessage string

	// RecentTurns is the recent conversation history (newest last).
	RecentTurns []Turn
}

// Recall performs an active memory recall for the given request context.
// It returns a Result containing the recalled summary (if any).
func (e *Engine) Recall(ctx context.Context, req RecallRequest) Result {
	start := time.Now()

	// Check global enable.
	if !e.cfg.Enabled {
		return Result{Status: StatusDisabled, DurationMS: elapsed(start)}
	}

	// Check agent scoping.
	if len(e.cfg.Agents) > 0 && !containsAgent(e.cfg.Agents, req.AgentID) {
		return Result{Status: StatusSkipped, DurationMS: elapsed(start)}
	}

	// Check session toggle.
	if e.toggles.isDisabled(req.SessionKey) {
		return Result{Status: StatusDisabled, DurationMS: elapsed(start)}
	}

	// Check chat type filtering.
	if !e.isAllowedChatType(req.ChatType) {
		return Result{Status: StatusSkipped, DurationMS: elapsed(start)}
	}

	// Check for empty message.
	if strings.TrimSpace(req.LatestMessage) == "" {
		return Result{Status: StatusEmpty, DurationMS: elapsed(start)}
	}

	// Check store availability.
	if e.store == nil {
		return Result{Status: StatusUnavailable, DurationMS: elapsed(start)}
	}

	// Build query from recent turns + latest message.
	query := e.buildQuery(req)

	// Check cache.
	cacheKey := buildCacheKey(req.AgentID, req.SessionKey, query)
	if cached, ok := e.cache.get(cacheKey); ok {
		cached.Cached = true
		cached.DurationMS = elapsed(start)
		return cached
	}

	// Apply timeout.
	timeout := time.Duration(e.cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(DefaultTimeoutMS) * time.Millisecond
	}
	searchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Search memory.
	limit := e.cfg.SearchLimit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	resultCh := make(chan searchResult, 1)
	go func() {
		hits := e.store.Search(query, limit)
		resultCh <- searchResult{hits: hits}
	}()

	select {
	case <-searchCtx.Done():
		return Result{Status: StatusTimeout, DurationMS: elapsed(start)}
	case sr := <-resultCh:
		summary := e.buildSummary(sr.hits)
		status := StatusOK
		if isNoRecallValue(summary) || summary == "" {
			status = StatusEmpty
			summary = ""
		}

		result := Result{
			Status:     status,
			Summary:    summary,
			DurationMS: elapsed(start),
			HitCount:   len(sr.hits),
		}

		// Cache the result.
		ttl := e.cfg.CacheTTL
		if ttl <= 0 {
			ttl = DefaultCacheTTL
		}
		if result.Status == StatusOK || result.Status == StatusEmpty {
			e.cache.set(cacheKey, result, ttl)
		}

		return result
	}
}

type searchResult struct {
	hits []memory.IndexedMemory
}

// FormatContextInjection formats a recall result for injection into the
// agent's system prompt as ExtraSystemPrompt.
func FormatContextInjection(result Result) string {
	if result.Status != StatusOK || result.Summary == "" {
		return ""
	}
	return "🧩 Active memory: " + result.Summary
}

// ── Internal helpers ────────────────────────────────────────────────────────

func (e *Engine) isAllowedChatType(ct ChatType) bool {
	allowed := e.cfg.AllowedChatTypes
	if len(allowed) == 0 {
		allowed = []ChatType{ChatTypeDirect}
	}
	for _, a := range allowed {
		if a == ct {
			return true
		}
	}
	return false
}

// buildQuery constructs the search query from recent turns and the latest message.
func (e *Engine) buildQuery(req RecallRequest) string {
	var parts []string

	// Add recent turns for context.
	userTurns := e.cfg.RecentUserTurns
	if userTurns < 0 {
		userTurns = DefaultRecentUserTurns
	}
	assistantTurns := e.cfg.RecentAssistantTurns
	if assistantTurns < 0 {
		assistantTurns = DefaultRecentAssistantTurns
	}
	userChars := e.cfg.RecentUserChars
	if userChars <= 0 {
		userChars = DefaultRecentUserChars
	}
	assistantChars := e.cfg.RecentAssistantChars
	if assistantChars <= 0 {
		assistantChars = DefaultRecentAssistantChars
	}

	userCount, assistantCount := 0, 0
	// Walk from newest to oldest.
	for i := len(req.RecentTurns) - 1; i >= 0; i-- {
		turn := req.RecentTurns[i]
		switch turn.Role {
		case "user":
			if userCount >= userTurns {
				continue
			}
			userCount++
			parts = append(parts, truncate(turn.Text, userChars))
		case "assistant":
			if assistantCount >= assistantTurns {
				continue
			}
			assistantCount++
			parts = append(parts, truncate(turn.Text, assistantChars))
		}
	}

	// Always include the latest message.
	parts = append(parts, req.LatestMessage)

	return strings.Join(parts, " ")
}

// buildSummary constructs a compact summary from memory search hits.
func (e *Engine) buildSummary(hits []memory.IndexedMemory) string {
	if len(hits) == 0 {
		return ""
	}

	maxChars := e.cfg.MaxSummaryChars
	if maxChars <= 0 {
		maxChars = DefaultMaxSummaryChars
	}

	var parts []string
	totalLen := 0
	for _, hit := range hits {
		text := strings.TrimSpace(hit.Text)
		if text == "" {
			continue
		}
		if totalLen+len(text)+2 > maxChars {
			remaining := maxChars - totalLen
			if remaining > 10 {
				parts = append(parts, truncate(text, remaining))
			}
			break
		}
		parts = append(parts, text)
		totalLen += len(text) + 2 // +2 for "; " separator
	}

	return strings.Join(parts, "; ")
}

func buildCacheKey(agentID, sessionKey, query string) string {
	h := sha256.Sum256([]byte(query))
	return agentID + ":" + sessionKey + ":" + hex.EncodeToString(h[:8])
}

func containsAgent(agents []string, agentID string) bool {
	for _, a := range agents {
		if strings.EqualFold(a, agentID) {
			return true
		}
	}
	return false
}

func truncate(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	if maxChars <= 0 {
		return ""
	}
	return s[:maxChars]
}

func elapsed(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
