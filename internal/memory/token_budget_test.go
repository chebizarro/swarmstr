package memory

import (
	"context"
	"strings"
	"testing"
)

func TestEstimateTokenCostText(t *testing.T) {
	got := EstimateTokenCostText("one two three four")
	if got != 6 { // ceil(4 * 1.3)
		t.Fatalf("EstimateTokenCostText = %d, want 6", got)
	}
}

func TestMemoryQueryTokenBudgetSoftTrim(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	mustWriteRecord(t, b, MemoryRecord{ID: "tb-1", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "alpha", Text: strings.Repeat("alpha ", 200)})
	mustWriteRecord(t, b, MemoryRecord{ID: "tb-2", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "alpha", Text: strings.Repeat("alpha ", 200)})

	cards, err := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "alpha", Limit: 10, TokenBudget: 200, SessionID: "sess-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) == 0 || len(cards) >= 2 {
		t.Fatalf("expected soft trim to reduce results, got %d", len(cards))
	}
	tm := MemoryTokenTelemetrySnapshot()
	if tm.TokenCostP50 <= 0 {
		t.Fatalf("expected token telemetry, got %+v", tm)
	}
}
