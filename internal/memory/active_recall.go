package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
	"time"

	ctxengine "metiq/internal/context"
)

const (
	DefaultActiveRecallTimeout          = 1500 * time.Millisecond
	DefaultActiveRecallCacheTTL         = 15 * time.Second
	DefaultActiveRecallSearchLimit      = 8
	DefaultActiveRecallMaxContextChars  = 1200
	DefaultActiveRecallRecentUsers      = 2
	DefaultActiveRecallRecentAssistants = 1
	DefaultActiveRecallTurnChars        = 240
)

type ActiveRecallConfig struct {
	Enabled                        bool
	Timeout                        time.Duration
	CacheTTL                       time.Duration
	SearchLimit                    int
	MaxContextChars                int
	RecentUserTurns                int
	RecentAssistantTurns           int
	MaxTurnChars                   int
	CitationsMode                  CitationsMode
	ToolAllowlist                  []string
	Provider                       string
	Model                          string
	CircuitBreakerFailureThreshold int
	CircuitBreakerCooldown         time.Duration
}

type ActiveRecallTurn struct {
	Role    string
	Content string
}

type ActiveRecallRequest struct {
	SessionID     string
	LatestMessage string
	RecentTurns   []ActiveRecallTurn
}

type ActiveRecallResult struct {
	Context  string
	Query    string
	HitCount int
	Cached   bool
	TimedOut bool
	Status   string
	Error    string
	Provider string
	Model    string
	Partial  bool
}

type ActiveRecallSearcher interface {
	Search(query string, limit int) []IndexedMemory
}

type ActiveRecallPartialSearcher interface {
	SearchPartial(ctx context.Context, query string, limit int) ([]IndexedMemory, error)
}

type activeRecallBreaker struct {
	Failures int
	OpenedAt time.Time
}

type activeRecallCacheEntry struct {
	result    ActiveRecallResult
	expiresAt time.Time
}

type ActiveRecallAssembler struct {
	cfg      ActiveRecallConfig
	searcher ActiveRecallSearcher
	mu       sync.Mutex
	cache    map[string]activeRecallCacheEntry
	last     map[string]ActiveRecallResult
	breakers map[string]activeRecallBreaker
}

func NewActiveRecallAssembler(cfg ActiveRecallConfig, searcher ActiveRecallSearcher) *ActiveRecallAssembler {
	if !cfg.Enabled && cfg.Timeout == 0 && cfg.CacheTTL == 0 && cfg.SearchLimit == 0 && cfg.MaxContextChars == 0 && cfg.RecentUserTurns == 0 && cfg.RecentAssistantTurns == 0 && cfg.MaxTurnChars == 0 && cfg.CitationsMode == "" && len(cfg.ToolAllowlist) == 0 && cfg.Provider == "" && cfg.Model == "" && cfg.CircuitBreakerFailureThreshold == 0 && cfg.CircuitBreakerCooldown == 0 {
		cfg.Enabled = true
	}
	cfg = normalizeActiveRecallConfig(cfg)
	return &ActiveRecallAssembler{cfg: cfg, searcher: searcher, cache: map[string]activeRecallCacheEntry{}, last: map[string]ActiveRecallResult{}, breakers: map[string]activeRecallBreaker{}}
}

func (a *ActiveRecallAssembler) AssembleActiveRecallForContext(ctx context.Context, sessionID string, latest ctxengine.Message, recent []ctxengine.Message, maxChars int) (string, error) {
	turns := make([]ActiveRecallTurn, 0, len(recent))
	for _, msg := range recent {
		if msg.Role == "user" || msg.Role == "assistant" {
			turns = append(turns, ActiveRecallTurn{Role: msg.Role, Content: msg.Content})
		}
	}
	cfg := a.cfg
	if maxChars > 0 {
		cfg.MaxContextChars = maxChars
	}
	// Create temporary assembler with modified config (avoid copying mutex)
	temp := &ActiveRecallAssembler{
		cfg:      cfg,
		searcher: a.searcher,
		cache:    make(map[string]activeRecallCacheEntry),
		last:     map[string]ActiveRecallResult{},
		breakers: map[string]activeRecallBreaker{},
	}
	result, err := temp.Recall(ctx, ActiveRecallRequest{SessionID: sessionID, LatestMessage: latest.Content, RecentTurns: turns})
	if err != nil || result.Context == "" {
		return "", err
	}
	return result.Context, nil
}

// AssembleActiveRecall implements context.ActiveRecallProvider.
func (a *ActiveRecallAssembler) AssembleActiveRecall(ctx context.Context, sessionID string, latest ctxengine.Message, recent []ctxengine.Message, maxChars int) (string, error) {
	return a.AssembleActiveRecallForContext(ctx, sessionID, latest, recent, maxChars)
}

func (a *ActiveRecallAssembler) Recall(ctx context.Context, req ActiveRecallRequest) (out ActiveRecallResult, err error) {
	defer func() {
		out.Provider = normalizeActiveRecallConfig(a.cfg).Provider
		out.Model = normalizeActiveRecallConfig(a.cfg).Model
		if out.Status == "" {
			if out.Context != "" {
				out.Status = "ok"
			} else if out.TimedOut {
				out.Status = "timeout"
			} else {
				out.Status = "empty"
			}
		}
		if a != nil {
			a.recordLast(req.SessionID, out)
		}
	}()
	if a == nil || a.searcher == nil {
		return out, nil
	}
	cfg := normalizeActiveRecallConfig(a.cfg)
	if !cfg.Enabled {
		out.Status = "disabled"
		return out, nil
	}
	if a.breakerOpen(cfg) {
		out.Status = "circuit_open"
		out.Error = "active recall circuit breaker open"
		return out, nil
	}
	query := BuildActiveRecallQuery(req, cfg)
	out.Query = query
	if strings.TrimSpace(query) == "" {
		return out, nil
	}
	key := activeRecallCacheKey(req.SessionID, query)
	if cached, ok := a.getCached(key); ok {
		cached.Cached = true
		return cached, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	type sr struct {
		hits []IndexedMemory
		err  error
	}
	ch := make(chan sr, 1)
	if ps, ok := a.searcher.(ActiveRecallPartialSearcher); ok {
		go func() { hits, err := ps.SearchPartial(ctx, query, cfg.SearchLimit); ch <- sr{hits: hits, err: err} }()
		got := <-ch
		if ctx.Err() != nil {
			out.TimedOut = true
			out.Partial = len(got.hits) > 0
			out.Error = errStringMemory(got.err, ctx.Err())
			out.HitCount = len(got.hits)
			out.Context = FormatActiveRecallContextWithCitations(got.hits, cfg.MaxContextChars, cfg.CitationsMode)
			a.recordActiveRecallFailure(cfg)
			return out, nil
		}
		hits := got.hits
		out.HitCount = len(hits)
		out.Context = FormatActiveRecallContextWithCitations(hits, cfg.MaxContextChars, cfg.CitationsMode)
		if got.err != nil {
			out.Error = got.err.Error()
		}
		if out.Context != "" {
			a.recordActiveRecallSuccess(cfg)
		} else {
			a.recordActiveRecallFailure(cfg)
		}
		a.setCached(key, out, cfg.CacheTTL)
		return out, nil
	}
	go func() { ch <- sr{hits: a.searcher.Search(query, cfg.SearchLimit)} }()
	select {
	case <-ctx.Done():
		out.TimedOut = true
		out.Error = ctx.Err().Error()
		a.recordActiveRecallFailure(cfg)
		return out, nil
	case got := <-ch:
		hits := got.hits
		out.HitCount = len(hits)
		out.Context = FormatActiveRecallContextWithCitations(hits, cfg.MaxContextChars, cfg.CitationsMode)
		if got.err != nil {
			out.Error = got.err.Error()
		}
		if out.Context != "" {
			a.recordActiveRecallSuccess(cfg)
		} else {
			a.recordActiveRecallFailure(cfg)
		}
		a.setCached(key, out, cfg.CacheTTL)
		return out, nil
	}
}

func BuildActiveRecallQuery(req ActiveRecallRequest, cfg ActiveRecallConfig) string {
	cfg = normalizeActiveRecallConfig(cfg)
	parts := []string{}
	latest := StripActiveRecallNoise(req.LatestMessage)
	if latest != "" {
		parts = append(parts, latest)
	}
	users, assistants := 0, 0
	for i := len(req.RecentTurns) - 1; i >= 0; i-- {
		turn := req.RecentTurns[i]
		text := truncateActiveRecall(StripActiveRecallNoise(turn.Content), cfg.MaxTurnChars)
		if text == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(turn.Role)) {
		case "user":
			if users >= cfg.RecentUserTurns {
				continue
			}
			users++
			parts = append(parts, text)
		case "assistant":
			if assistants >= cfg.RecentAssistantTurns {
				continue
			}
			assistants++
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func FormatActiveRecallContext(hits []IndexedMemory, maxChars int) string {
	return FormatActiveRecallContextWithCitations(hits, maxChars, CitationsModeOff)
}

func FormatActiveRecallContextWithCitations(hits []IndexedMemory, maxChars int, mode CitationsMode) string {
	hits = FilterMemoryInjectionEligible(hits)
	if len(hits) == 0 || maxChars == 0 {
		return ""
	}
	if maxChars < 0 {
		maxChars = DefaultActiveRecallMaxContextChars
	}
	mode = NormalizeCitationsMode(mode)
	parts := []string{}
	used := 0
	for _, hit := range hits {
		text := StripActiveRecallNoise(hit.Text)
		if text == "" {
			continue
		}
		line := "- " + text
		if mode == CitationsModeOn {
			if citation := FormatIndexedMemoryCitation(hit); citation != "" {
				line += " " + citation
			}
		}
		if used+len(line)+1 > maxChars {
			remaining := maxChars - used
			if remaining > 8 {
				parts = append(parts, truncateActiveRecall(line, remaining))
			}
			break
		}
		parts = append(parts, line)
		used += len(line) + 1
	}
	if len(parts) == 0 {
		return ""
	}
	header := "## Active Memory Recall\nRelevant session memory and durable memories:"
	if mode == CitationsModeOn {
		header += " cite bracketed memory references when using them."
	}
	return header + "\n" + strings.Join(parts, "\n")
}

var activeRecallNoisePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<tool_result>.*?</tool_result>`),
	regexp.MustCompile(`(?is)<recent_transcript>.*?</recent_transcript>`),
	regexp.MustCompile("(?m)^```.*$"),
	regexp.MustCompile(`(?m)^\s*(tool|system|debug):\s*`),
}

func StripActiveRecallNoise(text string) string {
	text = strings.TrimSpace(text)
	for _, re := range activeRecallNoisePatterns {
		text = re.ReplaceAllString(text, " ")
	}
	return strings.Join(strings.Fields(text), " ")
}

func normalizeActiveRecallConfig(cfg ActiveRecallConfig) ActiveRecallConfig {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultActiveRecallTimeout
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = DefaultActiveRecallCacheTTL
	}
	if cfg.SearchLimit <= 0 {
		cfg.SearchLimit = DefaultActiveRecallSearchLimit
	}
	if cfg.MaxContextChars == 0 {
		cfg.MaxContextChars = DefaultActiveRecallMaxContextChars
	}
	if cfg.RecentUserTurns < 0 {
		cfg.RecentUserTurns = 0
	} else if cfg.RecentUserTurns == 0 {
		cfg.RecentUserTurns = DefaultActiveRecallRecentUsers
	}
	if cfg.RecentAssistantTurns < 0 {
		cfg.RecentAssistantTurns = 0
	} else if cfg.RecentAssistantTurns == 0 {
		cfg.RecentAssistantTurns = DefaultActiveRecallRecentAssistants
	}
	if cfg.MaxTurnChars <= 0 {
		cfg.MaxTurnChars = DefaultActiveRecallTurnChars
	}
	return cfg
}

func (a *ActiveRecallAssembler) getCached(key string) (ActiveRecallResult, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.cache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(a.cache, key)
		return ActiveRecallResult{}, false
	}
	return entry.result, true
}

func (a *ActiveRecallAssembler) setCached(key string, result ActiveRecallResult, ttl time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cache[key] = activeRecallCacheEntry{result: result, expiresAt: time.Now().Add(ttl)}
}

func activeRecallCacheKey(sessionID, query string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + query))
	return hex.EncodeToString(sum[:])
}

func truncateActiveRecall(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func (a *ActiveRecallAssembler) LastStatus(sessionID string) (ActiveRecallResult, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.last[sessionID]
	return r, ok
}

func (a *ActiveRecallAssembler) recordLast(sessionID string, r ActiveRecallResult) {
	if sessionID == "" {
		sessionID = "default"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		a.last = map[string]ActiveRecallResult{}
	}
	a.last[sessionID] = r
}

func (a *ActiveRecallAssembler) activeRecallBreakerKey(cfg ActiveRecallConfig) string {
	return strings.ToLower(strings.TrimSpace(cfg.Provider)) + "/" + strings.ToLower(strings.TrimSpace(cfg.Model))
}

func (a *ActiveRecallAssembler) breakerOpen(cfg ActiveRecallConfig) bool {
	key := a.activeRecallBreakerKey(cfg)
	if key == "/" {
		return false
	}
	threshold := cfg.CircuitBreakerFailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	cooldown := cfg.CircuitBreakerCooldown
	if cooldown <= 0 {
		cooldown = time.Minute
	}
	a.mu.Lock()
	b := a.breakers[key]
	a.mu.Unlock()
	return b.Failures >= threshold && time.Since(b.OpenedAt) < cooldown
}

func (a *ActiveRecallAssembler) recordActiveRecallFailure(cfg ActiveRecallConfig) {
	key := a.activeRecallBreakerKey(cfg)
	if key == "/" {
		return
	}
	threshold := cfg.CircuitBreakerFailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.breakers == nil {
		a.breakers = map[string]activeRecallBreaker{}
	}
	b := a.breakers[key]
	b.Failures++
	if b.Failures >= threshold && b.OpenedAt.IsZero() {
		b.OpenedAt = time.Now()
	}
	a.breakers[key] = b
}

func (a *ActiveRecallAssembler) recordActiveRecallSuccess(cfg ActiveRecallConfig) {
	key := a.activeRecallBreakerKey(cfg)
	if key == "/" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.breakers, key)
}

func errStringMemory(errs ...error) string {
	for _, err := range errs {
		if err != nil {
			return err.Error()
		}
	}
	return ""
}

func (a *ActiveRecallAssembler) ToolAllowed(tool string) bool {
	if a == nil {
		return false
	}
	return activeRecallToolAllowed(a.cfg.ToolAllowlist, tool)
}

func activeRecallToolAllowed(allowlist []string, tool string) bool {
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
