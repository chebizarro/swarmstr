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
	searcher := &activeRecallTestSearcher{hits: []IndexedMemory{{Text: "User prefers Docker sandbox by default", OriginClass: string(MemoryOriginOwner), SessionKind: string(MemorySessionInteractive)}}}
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
	searcher := &activeRecallTestSearcher{delay: 20 * time.Millisecond, hits: []IndexedMemory{{Text: "slow", OriginClass: string(MemoryOriginOwner), SessionKind: string(MemorySessionInteractive)}}}
	assembler := NewActiveRecallAssembler(ActiveRecallConfig{Enabled: true, Timeout: time.Millisecond}, searcher)
	result, err := assembler.Recall(context.Background(), ActiveRecallRequest{SessionID: "sess", LatestMessage: "slow?"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if !result.TimedOut || result.Context != "" {
		t.Fatalf("expected timeout without context, got %+v", result)
	}
}

type activeRecallPartialTestSearcher struct {
	hits []IndexedMemory
}

func (s *activeRecallPartialTestSearcher) Search(query string, limit int) []IndexedMemory { return nil }
func (s *activeRecallPartialTestSearcher) SearchPartial(ctx context.Context, query string, limit int) ([]IndexedMemory, error) {
	<-ctx.Done()
	return s.hits, ctx.Err()
}

func TestActiveRecallAssemblerStatusPersistence(t *testing.T) {
	searcher := &activeRecallTestSearcher{hits: []IndexedMemory{{Text: "debug status", OriginClass: string(MemoryOriginOwner), SessionKind: string(MemorySessionInteractive)}}}
	assembler := NewActiveRecallAssembler(ActiveRecallConfig{Enabled: true}, searcher)
	result, err := assembler.Recall(context.Background(), ActiveRecallRequest{SessionID: "sess-status", LatestMessage: "status?"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	got, ok := assembler.LastStatus("sess-status")
	if !ok || got.Status != result.Status || got.Context == "" {
		t.Fatalf("last status not persisted: ok=%v got=%+v result=%+v", ok, got, result)
	}
}

func TestActiveRecallAssemblerCircuitBreakerOpenAndCooldown(t *testing.T) {
	searcher := &activeRecallTestSearcher{delay: 20 * time.Millisecond}
	assembler := NewActiveRecallAssembler(ActiveRecallConfig{Enabled: true, Timeout: time.Millisecond, Provider: "p", Model: "m", CircuitBreakerFailureThreshold: 1, CircuitBreakerCooldown: 5 * time.Millisecond}, searcher)
	first, err := assembler.Recall(context.Background(), ActiveRecallRequest{SessionID: "sess-cb", LatestMessage: "one"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if !first.TimedOut {
		t.Fatalf("first should time out: %+v", first)
	}
	open, _ := assembler.Recall(context.Background(), ActiveRecallRequest{SessionID: "sess-cb", LatestMessage: "two"})
	if open.Status != "circuit_open" {
		t.Fatalf("breaker status=%q, want circuit_open", open.Status)
	}
	time.Sleep(8 * time.Millisecond)
	after, _ := assembler.Recall(context.Background(), ActiveRecallRequest{SessionID: "sess-cb", LatestMessage: "three"})
	if after.Status == "circuit_open" {
		t.Fatalf("breaker should allow probe after cooldown: %+v", after)
	}
}

func TestActiveRecallAssemblerPartialTimeoutReturnsPartial(t *testing.T) {
	assembler := NewActiveRecallAssembler(ActiveRecallConfig{Enabled: true, Timeout: time.Millisecond}, &activeRecallPartialTestSearcher{hits: []IndexedMemory{{Text: "partial memory", OriginClass: string(MemoryOriginOwner), SessionKind: string(MemorySessionInteractive)}}})
	result, err := assembler.Recall(context.Background(), ActiveRecallRequest{SessionID: "sess-partial", LatestMessage: "partial?"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if !result.TimedOut || !result.Partial || result.Context == "" || result.HitCount != 1 {
		t.Fatalf("expected partial timeout context, got %+v", result)
	}
}
