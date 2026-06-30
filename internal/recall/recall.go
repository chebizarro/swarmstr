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

	// CitationsMode controls whether recall summaries include provenance labels.
	CitationsMode memory.CitationsMode

	// RecentUserTurns is how many recent user turns to include as search context.
	RecentUserTurns int

	// RecentAssistantTurns is how many recent assistant turns to include.
	RecentAssistantTurns int

	// RecentUserChars caps each user turn excerpt.
	RecentUserChars int

	// RecentAssistantChars caps each assistant turn excerpt.
	RecentAssistantChars int

	// ToolAllowlist lists tool names recall may surface. Empty means no tool filtering.
	ToolAllowlist []string

	// Provider and Model identify the recall lane for circuit breaker tracking.
	Provider string
	Model    string

	// CircuitBreakerFailureThreshold opens the breaker after this many consecutive failures.
	CircuitBreakerFailureThreshold int

	// CircuitBreakerCooldown is how long an opened breaker rejects calls before probing again.
	CircuitBreakerCooldown time.Duration
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
	StatusOK          Status = "ok"           // recall returned useful context
	StatusEmpty       Status = "empty"        // recall found nothing useful
	StatusTimeout     Status = "timeout"      // recall timed out
	StatusUnavailable Status = "unavailable"  // memory store not available
	StatusDisabled    Status = "disabled"     // recall disabled for this context
	StatusSkipped     Status = "skipped"      // skipped (e.g., non-eligible session)
	StatusError       Status = "error"        // recall failed
	StatusCircuitOpen Status = "circuit_open" // provider/model circuit breaker is open
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

	// Citations contains compact provenance labels for recalled memories when
	// citation mode is enabled.
	Citations []memory.MemoryCitation

	// Error records a short failure/no-result reason for doctor/debug views.
	Error string

	// Provider and Model identify the recall lane used.
	Provider string
	Model    string

	// Partial is true when returned context was assembled from partial timeout results.
	Partial bool
}

// ── No-recall value detection ───────────────────────────────────────────────

// noRecallValues is a set of strings that indicate the recall found nothing useful.
var noRecallValues = map[string]bool{
	"":                     true,
	"none":                 true,
	"no_reply":             true,
	"no reply":             true,
	"nothing useful":       true,
	"no relevant memory":   true,
	"no relevant memories": true,
	"timeout":              true,
	"[]":                   true,
	"{}":                   true,
	"null":                 true,
	"n/a":                  true,
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

type ToggleState struct {
	Version  int               `json:"version"`
	Sessions map[string]bool   `json:"sessions,omitempty"` // sessionKey → enabled
	Meta     map[string]string `json:"meta,omitempty"`
}

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

func (s *sessionToggles) exportState() ToggleState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := ToggleState{Version: 1, Sessions: map[string]bool{}}
	for key, disabled := range s.disabled {
		state.Sessions[key] = !disabled
	}
	return state
}

func (s *sessionToggles) importState(state ToggleState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disabled = map[string]bool{}
	for key, enabled := range state.Sessions {
		if !enabled {
			s.disabled[key] = true
		}
	}
}

// ── Engine ──────────────────────────────────────────────────────────────────

// MemorySearcher is the interface for searching the memory index.
type MemorySearcher interface {
	Search(query string, limit int) []memory.IndexedMemory
}

// PartialMemorySearcher may return any hits collected before ctx cancellation.
type PartialMemorySearcher interface {
	SearchPartial(ctx context.Context, query string, limit int) ([]memory.IndexedMemory, error)
}

type providerBreaker struct {
	Failures int
	OpenedAt time.Time
}

// Engine is the active recall engine. It searches memory before agent
// processing and caches results.
type Engine struct {
	mu       sync.RWMutex
	cfg      Config
	store    MemorySearcher
	cache    *resultCache
	toggles  *sessionToggles
	statuses map[string]Result
	breakers map[string]providerBreaker
}

// NewEngine creates an active recall engine with the given configuration
// and memory store.
func NewEngine(cfg Config, store MemorySearcher) *Engine {
	return &Engine{
		cfg:      cfg,
		store:    store,
		cache:    newResultCache(cfg.MaxCacheEntries),
		toggles:  newSessionToggles(),
		statuses: map[string]Result{},
		breakers: map[string]providerBreaker{},
	}
}

// SetConfig hot-reloads the engine configuration.
func (e *Engine) SetConfig(cfg Config) {
	e.mu.Lock()
	defer e.mu.Unlock()
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

func (e *Engine) ExportToggleState() ToggleState      { return e.toggles.exportState() }
func (e *Engine) ImportToggleState(state ToggleState) { e.toggles.importState(state) }

func (e *Engine) LastStatus(sessionKey string) (Result, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	r, ok := e.statuses[sessionKey]
	return r, ok
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
func (e *Engine) Recall(ctx context.Context, req RecallRequest) (out Result) {
	start := time.Now()
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()
	defer func() {
		out.DurationMS = elapsed(start)
		out.Provider = cfg.Provider
		out.Model = cfg.Model
		e.recordStatus(req.SessionKey, out)
	}()

	// Check global enable.
	if !cfg.Enabled {
		return Result{Status: StatusDisabled, DurationMS: elapsed(start)}
	}

	// Check agent scoping.
	if len(cfg.Agents) > 0 && !containsAgent(cfg.Agents, req.AgentID) {
		return Result{Status: StatusSkipped, DurationMS: elapsed(start)}
	}

	// Check session toggle.
	if e.toggles.isDisabled(req.SessionKey) {
		return Result{Status: StatusDisabled, DurationMS: elapsed(start)}
	}

	// Check chat type filtering.
	if !isAllowedChatType(cfg, req.ChatType) {
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
	query := e.buildQueryWithConfig(req, cfg)

	if e.breakerOpen(cfg) {
		return Result{Status: StatusCircuitOpen, Error: "recall circuit breaker open"}
	}

	// Check cache.
	cacheKey := buildCacheKey(req.AgentID, req.SessionKey, query)
	if cached, ok := e.cache.get(cacheKey); ok {
		cached.Cached = true
		cached.DurationMS = elapsed(start)
		return cached
	}

	// Apply timeout.
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(DefaultTimeoutMS) * time.Millisecond
	}
	searchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Search memory.
	limit := cfg.SearchLimit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	resultCh := make(chan searchResult, 1)
	if ps, ok := e.store.(PartialMemorySearcher); ok {
		go func() {
			hits, err := ps.SearchPartial(searchCtx, query, limit)
			resultCh <- searchResult{hits: hits, err: err, partial: searchCtx.Err() != nil}
		}()
		sr := <-resultCh
		if sr.partial && len(sr.hits) > 0 {
			summary := e.buildSummaryWithConfig(sr.hits, cfg)
			e.recordFailure(cfg)
			return Result{Status: StatusOK, Summary: summary, HitCount: len(sr.hits), Partial: true, Error: errString(sr.err, searchCtx.Err())}
		}
		if sr.partial {
			e.recordFailure(cfg)
			return Result{Status: StatusTimeout, Error: errString(sr.err, searchCtx.Err())}
		}
		summary := e.buildSummaryWithConfig(sr.hits, cfg)
		if memory.NormalizeCitationsMode(cfg.CitationsMode) == memory.CitationsModeOn {
			summary = memory.FormatMemorySummaryWithCitations(summary, sr.hits, cfg.CitationsMode)
		}
		status := StatusOK
		if isNoRecallValue(summary) || summary == "" {
			status = StatusEmpty
			summary = ""
		}
		result := Result{Status: status, Summary: summary, DurationMS: elapsed(start), HitCount: len(sr.hits)}
		if status == StatusOK && memory.NormalizeCitationsMode(cfg.CitationsMode) == memory.CitationsModeOn {
			result.Citations = memory.BuildMemoryCitations(sr.hits)
		}
		ttl := cfg.CacheTTL
		if ttl <= 0 {
			ttl = DefaultCacheTTL
		}
		if result.Status == StatusOK || result.Status == StatusEmpty {
			e.recordSuccess(cfg)
			e.cache.set(cacheKey, result, ttl)
		} else {
			e.recordFailure(cfg)
		}
		return result
	}
	go func() {
		hits := e.store.Search(query, limit)
		resultCh <- searchResult{hits: hits}
	}()

	select {
	case <-searchCtx.Done():
		e.recordFailure(cfg)
		return Result{Status: StatusTimeout, Error: searchCtx.Err().Error()}
	case sr := <-resultCh:
		summary := e.buildSummaryWithConfig(sr.hits, cfg)
		if memory.NormalizeCitationsMode(cfg.CitationsMode) == memory.CitationsModeOn {
			summary = memory.FormatMemorySummaryWithCitations(summary, sr.hits, cfg.CitationsMode)
		}
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
		if status == StatusOK && memory.NormalizeCitationsMode(cfg.CitationsMode) == memory.CitationsModeOn {
			result.Citations = memory.BuildMemoryCitations(sr.hits)
		}

		// Cache the result.
		ttl := cfg.CacheTTL
		if ttl <= 0 {
			ttl = DefaultCacheTTL
		}
		if result.Status == StatusOK || result.Status == StatusEmpty {
			e.recordSuccess(cfg)
			e.cache.set(cacheKey, result, ttl)
		} else {
			e.recordFailure(cfg)
		}

		return result
	}
}

type searchResult struct {
	hits    []memory.IndexedMemory
	err     error
	partial bool
}

// FormatContextInjection formats a recall result for injection into the
// agent's system prompt as ExtraSystemPrompt.
func FormatContextInjection(result Result) string {
	return FormatContextInjectionWithCitations(result, memory.CitationsModeOff)
}

func FormatContextInjectionWithCitations(result Result, mode memory.CitationsMode) string {
	if result.Status != StatusOK || result.Summary == "" {
		return ""
	}
	text := result.Summary
	if memory.NormalizeCitationsMode(mode) == memory.CitationsModeOn && len(result.Citations) > 0 && !strings.Contains(text, "Citations:") {
		parts := make([]string, 0, len(result.Citations))
		for _, citation := range result.Citations {
			if formatted := memory.FormatMemoryCitation(citation); formatted != "" {
				parts = append(parts, formatted)
			}
		}
		if len(parts) > 0 {
			text += "\nCitations: " + strings.Join(parts, "; ")
		}
	}
	return "🧩 Active memory: " + text
}

// ── Internal helpers ────────────────────────────────────────────────────────

func isAllowedChatType(cfg Config, ct ChatType) bool {
	allowed := cfg.AllowedChatTypes
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
func (e *Engine) buildQuery(req RecallRequest) string { return e.buildQueryWithConfig(req, e.cfg) }

func (e *Engine) buildQueryWithConfig(req RecallRequest, cfg Config) string {
	var parts []string

	// Add recent turns for context.
	userTurns := cfg.RecentUserTurns
	if userTurns < 0 {
		userTurns = DefaultRecentUserTurns
	}
	assistantTurns := cfg.RecentAssistantTurns
	if assistantTurns < 0 {
		assistantTurns = DefaultRecentAssistantTurns
	}
	userChars := cfg.RecentUserChars
	if userChars <= 0 {
		userChars = DefaultRecentUserChars
	}
	assistantChars := cfg.RecentAssistantChars
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
	return e.buildSummaryWithConfig(hits, e.cfg)
}

func (e *Engine) buildSummaryWithConfig(hits []memory.IndexedMemory, cfg Config) string {
	if len(hits) == 0 {
		return ""
	}

	maxChars := cfg.MaxSummaryChars
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

func (e *Engine) recordStatus(sessionKey string, r Result) {
	if sessionKey == "" {
		sessionKey = "default"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.statuses[sessionKey] = r
}

func (e *Engine) breakerKey(cfg Config) string {
	return strings.ToLower(strings.TrimSpace(cfg.Provider)) + "/" + strings.ToLower(strings.TrimSpace(cfg.Model))
}

func (e *Engine) breakerOpen(cfg Config) bool {
	threshold := cfg.CircuitBreakerFailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	cooldown := cfg.CircuitBreakerCooldown
	if cooldown <= 0 {
		cooldown = time.Minute
	}
	key := e.breakerKey(cfg)
	if key == "/" {
		return false
	}
	e.mu.RLock()
	b := e.breakers[key]
	e.mu.RUnlock()
	return b.Failures >= threshold && time.Since(b.OpenedAt) < cooldown
}

func (e *Engine) recordFailure(cfg Config) {
	key := e.breakerKey(cfg)
	if key == "/" {
		return
	}
	threshold := cfg.CircuitBreakerFailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.breakers[key]
	b.Failures++
	if b.Failures >= threshold && b.OpenedAt.IsZero() {
		b.OpenedAt = time.Now()
	}
	e.breakers[key] = b
}

func (e *Engine) recordSuccess(cfg Config) {
	key := e.breakerKey(cfg)
	if key == "/" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.breakers, key)
}

func errString(errs ...error) string {
	for _, err := range errs {
		if err != nil {
			return err.Error()
		}
	}
	return ""
}

func (e *Engine) ToolAllowed(tool string) bool {
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()
	return toolAllowed(cfg.ToolAllowlist, tool)
}

func toolAllowed(allowlist []string, tool string) bool {
	if len(allowlist) == 0 {
		return true
	}
	tool = strings.TrimSpace(tool)
	for _, allowed := range allowlist {
		if strings.EqualFold(strings.TrimSpace(allowed), tool) {
			return true
		}
	}
	return false
}
