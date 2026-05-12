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
	Enabled              bool
	Timeout              time.Duration
	CacheTTL             time.Duration
	SearchLimit          int
	MaxContextChars      int
	RecentUserTurns      int
	RecentAssistantTurns int
	MaxTurnChars         int
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
}

type ActiveRecallSearcher interface {
	Search(query string, limit int) []IndexedMemory
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
}

func NewActiveRecallAssembler(cfg ActiveRecallConfig, searcher ActiveRecallSearcher) *ActiveRecallAssembler {
	if cfg == (ActiveRecallConfig{}) {
		cfg.Enabled = true
	}
	cfg = normalizeActiveRecallConfig(cfg)
	return &ActiveRecallAssembler{cfg: cfg, searcher: searcher, cache: map[string]activeRecallCacheEntry{}}
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
	clone := *a
	clone.cfg = cfg
	result, err := clone.Recall(ctx, ActiveRecallRequest{SessionID: sessionID, LatestMessage: latest.Content, RecentTurns: turns})
	if err != nil || result.Context == "" {
		return "", err
	}
	return result.Context, nil
}

// AssembleActiveRecall implements context.ActiveRecallProvider.
func (a *ActiveRecallAssembler) AssembleActiveRecall(ctx context.Context, sessionID string, latest ctxengine.Message, recent []ctxengine.Message, maxChars int) (string, error) {
	return a.AssembleActiveRecallForContext(ctx, sessionID, latest, recent, maxChars)
}

func (a *ActiveRecallAssembler) Recall(ctx context.Context, req ActiveRecallRequest) (ActiveRecallResult, error) {
	var out ActiveRecallResult
	if a == nil || a.searcher == nil {
		return out, nil
	}
	cfg := normalizeActiveRecallConfig(a.cfg)
	if !cfg.Enabled {
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
	ch := make(chan []IndexedMemory, 1)
	go func() { ch <- a.searcher.Search(query, cfg.SearchLimit) }()
	select {
	case <-ctx.Done():
		out.TimedOut = true
		return out, nil
	case hits := <-ch:
		out.HitCount = len(hits)
		out.Context = FormatActiveRecallContext(hits, cfg.MaxContextChars)
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
	if len(hits) == 0 || maxChars == 0 {
		return ""
	}
	if maxChars < 0 {
		maxChars = DefaultActiveRecallMaxContextChars
	}
	parts := []string{}
	used := 0
	for _, hit := range hits {
		text := StripActiveRecallNoise(hit.Text)
		if text == "" {
			continue
		}
		line := "- " + text
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
	return "## Active Memory Recall\nRelevant session memory and durable memories:\n" + strings.Join(parts, "\n")
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
