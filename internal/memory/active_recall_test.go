package memory

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type activeRecallTestSearcher struct {
	mu      sync.Mutex
	queries []string
	hits    []IndexedMemory
	delay   time.Duration
}

func (s *activeRecallTestSearcher) Search(query string, limit int) []IndexedMemory {
	s.mu.Lock()
	s.queries = append(s.queries, query)
	s.mu.Unlock()
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if limit > 0 && len(s.hits) > limit {
		return s.hits[:limit]
	}
	return s.hits
}

func (s *activeRecallTestSearcher) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queries)
}

func (s *activeRecallTestSearcher) lastQuery() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queries) == 0 {
		return ""
	}
	return s.queries[len(s.queries)-1]
}

func TestBuildActiveRecallQueryLatestFirstRoleBudgetsAndNoiseStripping(t *testing.T) {
	cfg := ActiveRecallConfig{Enabled: true, RecentUserTurns: 1, RecentAssistantTurns: 1, MaxTurnChars: 80}
	query := BuildActiveRecallQuery(ActiveRecallRequest{
		LatestMessage: "What was my deployment preference? <tool_result>ignore me</tool_result>",
		RecentTurns: []ActiveRecallTurn{
			{Role: "user", Content: "old user should be omitted"},
			{Role: "assistant", Content: "assistant context"},
			{Role: "user", Content: "new user context"},
		},
	}, cfg)
	if !strings.HasPrefix(query, "What was my deployment preference?") {
		t.Fatalf("latest message should lead query: %q", query)
	}
	if strings.Contains(query, "ignore me") || strings.Contains(query, "old user") {
		t.Fatalf("query did not strip noise/enforce budgets: %q", query)
	}
	if !strings.Contains(query, "assistant context") || !strings.Contains(query, "new user context") {
		t.Fatalf("query missing recent role-budgeted context: %q", query)
	}
}

func TestActiveRecallAssemblerCachesAndFormats(t *testing.T) {
	searcher := &activeRecallTestSearcher{hits: []IndexedMemory{{Text: "User prefers Docker sandbox by default"}}}
	assembler := NewActiveRecallAssembler(ActiveRecallConfig{Enabled: true, CacheTTL: time.Hour}, searcher)
	req := ActiveRecallRequest{SessionID: "sess", LatestMessage: "sandbox preference?"}
	first, err := assembler.Recall(context.Background(), req)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	second, err := assembler.Recall(context.Background(), req)
	if err != nil {
		t.Fatalf("Recall cached: %v", err)
	}
	if first.Context == "" || !strings.Contains(first.Context, "Active Memory Recall") {
		t.Fatalf("missing formatted recall context: %q", first.Context)
	}
	if !second.Cached || searcher.callCount() != 1 {
		t.Fatalf("expected cached second recall, cached=%v calls=%d", second.Cached, searcher.callCount())
	}
}

func TestActiveRecallAssemblerTimeout(t *testing.T) {
	searcher := &activeRecallTestSearcher{delay: 20 * time.Millisecond, hits: []IndexedMemory{{Text: "slow"}}}
	assembler := NewActiveRecallAssembler(ActiveRecallConfig{Enabled: true, Timeout: time.Millisecond}, searcher)
	result, err := assembler.Recall(context.Background(), ActiveRecallRequest{SessionID: "sess", LatestMessage: "slow?"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if !result.TimedOut || result.Context != "" {
		t.Fatalf("expected timeout without context, got %+v", result)
	}
}
