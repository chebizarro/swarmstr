package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestShouldCompactMemoryWriteThreshold(t *testing.T) {
	now := time.Date(2026, 5, 8, 10, 0, 0, 0, time.Local)
	decision := ShouldCompactMemory(context.Background(), MemoryCompactionState{WritesSinceCompact: 1000, Now: now}, MemoryCompactionPolicy{WriteThreshold: 1000, Now: func() time.Time { return now }, Load: func(context.Context) MemoryCompactionLoad { return MemoryCompactionLoad{} }})
	if !decision.Due || decision.Reason != "write_threshold" || decision.Skipped {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestShouldCompactMemoryDaily3AMAndLoadSkip(t *testing.T) {
	now := time.Date(2026, 5, 8, 3, 5, 0, 0, time.Local)
	decision := ShouldCompactMemory(context.Background(), MemoryCompactionState{WritesSinceCompact: 1, LastWriteAt: now.Add(-time.Hour), Now: now}, MemoryCompactionPolicy{WriteThreshold: 1000, DailyHour: 3, Now: func() time.Time { return now }, Load: func(context.Context) MemoryCompactionLoad { return MemoryCompactionLoad{CPUPercent: 81} }})
	if !decision.Due || decision.Reason != "daily_3am" || !decision.Skipped {
		t.Fatalf("expected daily due but CPU skipped, got: %+v", decision)
	}
}

func TestShouldCompactMemoryStartupAge(t *testing.T) {
	now := time.Date(2026, 5, 8, 9, 0, 0, 0, time.UTC)
	decision := ShouldCompactMemory(context.Background(), MemoryCompactionState{Startup: true, RecordCount: 1, LastCompactedAt: now.Add(-31 * 24 * time.Hour), Now: now}, MemoryCompactionPolicy{StartupMaxAge: 30 * 24 * time.Hour, Now: func() time.Time { return now }, Load: func(context.Context) MemoryCompactionLoad { return MemoryCompactionLoad{} }})
	if !decision.Due || decision.Reason != "startup_age_threshold" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestSQLiteCompactionStateTracksWritesAndReset(t *testing.T) {
	b, err := OpenSQLiteBackend(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	mustWriteRecord(t, b, MemoryRecord{ID: "state-1", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Text: "state tracking"})
	state, err := b.MemoryCompactionState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.WritesSinceCompact == 0 || state.RecordCount != 1 {
		t.Fatalf("expected write count and record count, got %+v", state)
	}
	if _, err := b.CompactMemoryRecords(context.Background(), CompactionConfig{Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	state, err = b.MemoryCompactionState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.WritesSinceCompact != 0 || state.LastCompactedAt.IsZero() {
		t.Fatalf("expected compact reset, got %+v", state)
	}
}
