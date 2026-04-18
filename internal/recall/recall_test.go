package recall

import (
	"context"
	"sync"
	"testing"
	"time"

	"metiq/internal/memory"
)

// ── Stub memory searcher ────────────────────────────────────────────────────

type stubSearcher struct {
	hits  []memory.IndexedMemory
	delay time.Duration
	calls int
	mu    sync.Mutex
}

func (s *stubSearcher) Search(query string, limit int) []memory.IndexedMemory {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if limit < len(s.hits) {
		return s.hits[:limit]
	}
	return s.hits
}

func (s *stubSearcher) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// ── DefaultConfig ───────────────────────────────────────────────────────────

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Enabled {
		t.Fatal("should be enabled by default")
	}
	if cfg.MaxSummaryChars != DefaultMaxSummaryChars {
		t.Fatalf("MaxSummaryChars = %d", cfg.MaxSummaryChars)
	}
	if len(cfg.AllowedChatTypes) != 1 || cfg.AllowedChatTypes[0] != ChatTypeDirect {
		t.Fatalf("AllowedChatTypes = %v", cfg.AllowedChatTypes)
	}
}

// ── isNoRecallValue ─────────────────────────────────────────────────────────

func TestIsNoRecallValue(t *testing.T) {
	positives := []string{"", "none", "NONE", "  None  ", "no_reply", "no relevant memory", "null", "n/a", "[]", "{}"}
	for _, v := range positives {
		if !isNoRecallValue(v) {
			t.Errorf("isNoRecallValue(%q) = false, want true", v)
		}
	}
	negatives := []string{"User prefers ramen", "Hello world", "some memory"}
	for _, v := range negatives {
		if isNoRecallValue(v) {
			t.Errorf("isNoRecallValue(%q) = true, want false", v)
		}
	}
}

// ── Cache ───────────────────────────────────────────────────────────────────

func TestResultCache_SetGetExpiry(t *testing.T) {
	c := newResultCache(100)
	c.set("k1", Result{Status: StatusOK, Summary: "test"}, time.Hour)
	got, ok := c.get("k1")
	if !ok || got.Summary != "test" {
		t.Fatalf("cache miss or wrong value: ok=%v got=%+v", ok, got)
	}

	// Expired entry.
	c.set("k2", Result{Status: StatusOK, Summary: "expired"}, 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	_, ok = c.get("k2")
	if ok {
		t.Fatal("expired entry should be evicted")
	}
}

func TestResultCache_Eviction(t *testing.T) {
	c := newResultCache(3)
	c.set("k1", Result{Status: StatusOK}, time.Hour)
	c.set("k2", Result{Status: StatusOK}, time.Hour)
	c.set("k3", Result{Status: StatusOK}, time.Hour)
	c.set("k4", Result{Status: StatusOK}, time.Hour)
	if c.size() > 3 {
		t.Fatalf("cache size = %d, want <= 3", c.size())
	}
}

func TestResultCache_Clear(t *testing.T) {
	c := newResultCache(100)
	c.set("k1", Result{Status: StatusOK}, time.Hour)
	c.clear()
	if c.size() != 0 {
		t.Fatalf("cache size after clear = %d", c.size())
	}
}

// ── Session toggles ─────────────────────────────────────────────────────────

func TestSessionToggles(t *testing.T) {
	s := newSessionToggles()
	if s.isDisabled("sess1") {
		t.Fatal("should not be disabled by default")
	}
	s.setDisabled("sess1", true)
	if !s.isDisabled("sess1") {
		t.Fatal("should be disabled after setDisabled(true)")
	}
	s.setDisabled("sess1", false)
	if s.isDisabled("sess1") {
		t.Fatal("should be re-enabled after setDisabled(false)")
	}
}

// ── Engine: Recall ──────────────────────────────────────────────────────────

func TestRecall_GlobalDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false
	e := NewEngine(cfg, &stubSearcher{})
	r := e.Recall(context.Background(), RecallRequest{AgentID: "main", LatestMessage: "hello"})
	if r.Status != StatusDisabled {
		t.Fatalf("Status = %q, want disabled", r.Status)
	}
}

func TestRecall_AgentScoping(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agents = []string{"agent-a"}
	e := NewEngine(cfg, &stubSearcher{})
	r := e.Recall(context.Background(), RecallRequest{
		AgentID:       "agent-b",
		ChatType:      ChatTypeDirect,
		LatestMessage: "hello",
	})
	if r.Status != StatusSkipped {
		t.Fatalf("Status = %q, want skipped (wrong agent)", r.Status)
	}

	// Correct agent.
	r = e.Recall(context.Background(), RecallRequest{
		AgentID:       "agent-a",
		ChatType:      ChatTypeDirect,
		LatestMessage: "hello",
	})
	if r.Status == StatusSkipped {
		t.Fatal("should not skip for matching agent")
	}
}

func TestRecall_SessionToggle(t *testing.T) {
	cfg := DefaultConfig()
	searcher := &stubSearcher{hits: []memory.IndexedMemory{{Text: "User likes pizza"}}}
	e := NewEngine(cfg, searcher)

	e.SetEnabled("sess1", false)
	r := e.Recall(context.Background(), RecallRequest{
		AgentID:       "main",
		SessionKey:    "sess1",
		ChatType:      ChatTypeDirect,
		LatestMessage: "what food?",
	})
	if r.Status != StatusDisabled {
		t.Fatalf("Status = %q, want disabled", r.Status)
	}

	e.SetEnabled("sess1", true)
	if !e.IsEnabled("sess1") {
		t.Fatal("should be re-enabled")
	}
}

func TestRecall_ChatTypeFiltering(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowedChatTypes = []ChatType{ChatTypeDirect}
	e := NewEngine(cfg, &stubSearcher{hits: []memory.IndexedMemory{{Text: "context"}}})

	r := e.Recall(context.Background(), RecallRequest{
		AgentID:       "main",
		ChatType:      ChatTypeGroup,
		LatestMessage: "hello",
	})
	if r.Status != StatusSkipped {
		t.Fatalf("Status = %q, want skipped (wrong chat type)", r.Status)
	}

	r = e.Recall(context.Background(), RecallRequest{
		AgentID:       "main",
		ChatType:      ChatTypeDirect,
		LatestMessage: "hello",
	})
	if r.Status == StatusSkipped {
		t.Fatal("should not skip for allowed chat type")
	}
}

func TestRecall_EmptyMessage(t *testing.T) {
	cfg := DefaultConfig()
	e := NewEngine(cfg, &stubSearcher{})
	r := e.Recall(context.Background(), RecallRequest{
		AgentID:       "main",
		ChatType:      ChatTypeDirect,
		LatestMessage: "  ",
	})
	if r.Status != StatusEmpty {
		t.Fatalf("Status = %q, want empty for blank message", r.Status)
	}
}

func TestRecall_NilStore(t *testing.T) {
	cfg := DefaultConfig()
	e := NewEngine(cfg, nil)
	r := e.Recall(context.Background(), RecallRequest{
		AgentID:       "main",
		ChatType:      ChatTypeDirect,
		LatestMessage: "hello",
	})
	if r.Status != StatusUnavailable {
		t.Fatalf("Status = %q, want unavailable", r.Status)
	}
}

func TestRecall_HappyPath(t *testing.T) {
	cfg := DefaultConfig()
	searcher := &stubSearcher{hits: []memory.IndexedMemory{
		{Text: "User's favorite food is ramen"},
		{Text: "User prefers window seats on flights"},
	}}
	e := NewEngine(cfg, searcher)

	r := e.Recall(context.Background(), RecallRequest{
		AgentID:       "main",
		SessionKey:    "sess1",
		ChatType:      ChatTypeDirect,
		LatestMessage: "What is my favorite food?",
	})
	if r.Status != StatusOK {
		t.Fatalf("Status = %q, want ok", r.Status)
	}
	if r.Summary == "" {
		t.Fatal("Summary should not be empty")
	}
	if r.HitCount != 2 {
		t.Fatalf("HitCount = %d, want 2", r.HitCount)
	}
	if r.Cached {
		t.Fatal("first call should not be cached")
	}
}

func TestRecall_CachingWorks(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CacheTTL = time.Hour
	searcher := &stubSearcher{hits: []memory.IndexedMemory{{Text: "cached context"}}}
	e := NewEngine(cfg, searcher)

	req := RecallRequest{
		AgentID:       "main",
		SessionKey:    "sess1",
		ChatType:      ChatTypeDirect,
		LatestMessage: "hello",
	}

	r1 := e.Recall(context.Background(), req)
	if r1.Status != StatusOK || r1.Cached {
		t.Fatalf("first call: Status=%q Cached=%v", r1.Status, r1.Cached)
	}

	r2 := e.Recall(context.Background(), req)
	if r2.Status != StatusOK || !r2.Cached {
		t.Fatalf("second call: Status=%q Cached=%v", r2.Status, r2.Cached)
	}

	// Searcher should only have been called once.
	if searcher.callCount() != 1 {
		t.Fatalf("searcher calls = %d, want 1 (cache should prevent second call)", searcher.callCount())
	}
}

func TestRecall_EmptyHitsReturnsEmpty(t *testing.T) {
	cfg := DefaultConfig()
	searcher := &stubSearcher{hits: nil}
	e := NewEngine(cfg, searcher)

	r := e.Recall(context.Background(), RecallRequest{
		AgentID:       "main",
		ChatType:      ChatTypeDirect,
		LatestMessage: "hello",
	})
	if r.Status != StatusEmpty {
		t.Fatalf("Status = %q, want empty", r.Status)
	}
}

func TestRecall_Timeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TimeoutMS = 1 // 1ms timeout
	searcher := &stubSearcher{
		hits:  []memory.IndexedMemory{{Text: "slow result"}},
		delay: 100 * time.Millisecond,
	}
	e := NewEngine(cfg, searcher)

	r := e.Recall(context.Background(), RecallRequest{
		AgentID:       "main",
		ChatType:      ChatTypeDirect,
		LatestMessage: "hello",
	})
	if r.Status != StatusTimeout {
		t.Fatalf("Status = %q, want timeout", r.Status)
	}
}

func TestRecall_ClearCache(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CacheTTL = time.Hour
	searcher := &stubSearcher{hits: []memory.IndexedMemory{{Text: "context"}}}
	e := NewEngine(cfg, searcher)

	req := RecallRequest{
		AgentID:       "main",
		ChatType:      ChatTypeDirect,
		LatestMessage: "hello",
	}
	e.Recall(context.Background(), req)
	e.ClearCache()
	if e.CacheSize() != 0 {
		t.Fatalf("cache size after clear = %d", e.CacheSize())
	}

	// Should re-search after cache clear.
	e.Recall(context.Background(), req)
	if searcher.callCount() != 2 {
		t.Fatalf("searcher calls = %d, want 2 after cache clear", searcher.callCount())
	}
}

// ── buildQuery ──────────────────────────────────────────────────────────────

func TestBuildQuery_IncludesRecentTurns(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RecentUserTurns = 1
	cfg.RecentAssistantTurns = 1
	e := NewEngine(cfg, nil)

	req := RecallRequest{
		LatestMessage: "current question",
		RecentTurns: []Turn{
			{Role: "user", Text: "previous question"},
			{Role: "assistant", Text: "previous answer"},
		},
	}
	q := e.buildQuery(req)
	if q == "" {
		t.Fatal("query should not be empty")
	}
	if !contains(q, "previous answer") {
		t.Fatalf("query should include assistant turn: %q", q)
	}
	if !contains(q, "current question") {
		t.Fatalf("query should include latest message: %q", q)
	}
}

func TestBuildQuery_RespectsLimits(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RecentUserTurns = 1
	cfg.RecentAssistantTurns = 0
	e := NewEngine(cfg, nil)

	req := RecallRequest{
		LatestMessage: "current",
		RecentTurns: []Turn{
			{Role: "user", Text: "old user"},
			{Role: "user", Text: "newer user"},
			{Role: "assistant", Text: "should be excluded"},
		},
	}
	q := e.buildQuery(req)
	// Should include only 1 user turn (the newest) + the latest message.
	if contains(q, "old user") {
		t.Fatalf("should only include 1 recent user turn, got: %q", q)
	}
	if contains(q, "should be excluded") {
		t.Fatalf("should exclude assistant turns when limit=0: %q", q)
	}
}

// ── buildSummary ────────────────────────────────────────────────────────────

func TestBuildSummary_Basic(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxSummaryChars = 100
	e := NewEngine(cfg, nil)

	hits := []memory.IndexedMemory{
		{Text: "User likes pizza"},
		{Text: "User works at Acme"},
	}
	summary := e.buildSummary(hits)
	if summary == "" {
		t.Fatal("summary should not be empty")
	}
	if !contains(summary, "pizza") || !contains(summary, "Acme") {
		t.Fatalf("summary = %q, expected both hits", summary)
	}
}

func TestBuildSummary_TruncatesAtMaxChars(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxSummaryChars = 30
	e := NewEngine(cfg, nil)

	hits := []memory.IndexedMemory{
		{Text: "Short"},
		{Text: "This is a much longer memory entry that should be truncated"},
	}
	summary := e.buildSummary(hits)
	if len(summary) > 35 { // some slack for separator
		t.Fatalf("summary too long: %d chars: %q", len(summary), summary)
	}
}

func TestBuildSummary_EmptyHits(t *testing.T) {
	cfg := DefaultConfig()
	e := NewEngine(cfg, nil)
	if s := e.buildSummary(nil); s != "" {
		t.Fatalf("expected empty for no hits, got %q", s)
	}
}

// ── FormatContextInjection ──────────────────────────────────────────────────

func TestFormatContextInjection_OK(t *testing.T) {
	result := Result{Status: StatusOK, Summary: "User likes ramen"}
	got := FormatContextInjection(result)
	if got != "🧩 Active memory: User likes ramen" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatContextInjection_Empty(t *testing.T) {
	result := Result{Status: StatusEmpty}
	got := FormatContextInjection(result)
	if got != "" {
		t.Fatalf("expected empty for StatusEmpty, got %q", got)
	}
}

func TestFormatContextInjection_Disabled(t *testing.T) {
	result := Result{Status: StatusDisabled}
	got := FormatContextInjection(result)
	if got != "" {
		t.Fatalf("expected empty for StatusDisabled, got %q", got)
	}
}

// ── SetConfig ───────────────────────────────────────────────────────────────

func TestSetConfig(t *testing.T) {
	cfg := DefaultConfig()
	e := NewEngine(cfg, nil)
	cfg2 := DefaultConfig()
	cfg2.Enabled = false
	e.SetConfig(cfg2)
	r := e.Recall(context.Background(), RecallRequest{
		AgentID:       "main",
		ChatType:      ChatTypeDirect,
		LatestMessage: "hello",
	})
	if r.Status != StatusDisabled {
		t.Fatal("SetConfig should hot-reload")
	}
}

// ── containsAgent ───────────────────────────────────────────────────────────

func TestContainsAgent(t *testing.T) {
	if !containsAgent([]string{"main", "helper"}, "main") {
		t.Fatal("should find main")
	}
	if !containsAgent([]string{"Main"}, "main") {
		t.Fatal("should be case-insensitive")
	}
	if containsAgent([]string{"main"}, "other") {
		t.Fatal("should not find other")
	}
}

// ── truncate ────────────────────────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	if truncate("hello", 3) != "hel" {
		t.Fatal("should truncate")
	}
	if truncate("hi", 10) != "hi" {
		t.Fatal("should not truncate short strings")
	}
	if truncate("test", 0) != "" {
		t.Fatal("0 maxChars should return empty")
	}
}

// ── Concurrency ─────────────────────────────────────────────────────────────

func TestRecall_ConcurrentAccess(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CacheTTL = time.Hour
	searcher := &stubSearcher{hits: []memory.IndexedMemory{{Text: "concurrent context"}}}
	e := NewEngine(cfg, searcher)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r := e.Recall(context.Background(), RecallRequest{
				AgentID:       "main",
				ChatType:      ChatTypeDirect,
				LatestMessage: "concurrent test",
			})
			if r.Status != StatusOK {
				t.Errorf("goroutine %d: Status = %q", n, r.Status)
			}
		}(i)
	}
	wg.Wait()
}

// ── Helper ──────────────────────────────────────────────────────────────────

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsString(s, substr)
}

func containsString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
