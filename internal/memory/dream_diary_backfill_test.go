package memory

import (
	"context"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestBackfillDreamDiary_BucketsByDayAndIsIdempotent(t *testing.T) {
	b, _ := createTestSQLiteBackend(t)
	defer b.Close()
	ctx := context.Background()

	now := time.Now().UTC()
	day1 := now.Add(-2 * 24 * time.Hour).Unix()
	day2 := now.Add(-5 * 24 * time.Hour).Unix()
	outside := now.Add(-40 * 24 * time.Hour).Unix()

	// Day 1: two promoted records.
	seedPromotableMemory(t, b, "d1a", "alpha", "tool", day1)
	markPromoted(t, b, "d1a", day1)
	seedPromotableMemory(t, b, "d1b", "alpha", "file", day1)
	markPromoted(t, b, "d1b", day1)
	// Day 2: one promoted + one eligible-but-unpromoted (buckets by last_recall).
	seedPromotableMemory(t, b, "d2a", "beta", "tool", day2)
	markPromoted(t, b, "d2a", day2)
	seedPromotableMemory(t, b, "d2b", "beta", "file", day2)
	// Outside the window: excluded.
	seedPromotableMemory(t, b, "old", "gamma", "tool", outside)
	markPromoted(t, b, "old", outside)

	res, err := b.BackfillDreamDiary(ctx, BackfillDreamDiaryOptions{Days: 30, Scope: "node", Now: now})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if res.DaysWithActivity != 2 {
		t.Fatalf("expected 2 active days, got %d", res.DaysWithActivity)
	}
	if res.EntriesCreated != 2 {
		t.Fatalf("expected 2 entries created, got %d", res.EntriesCreated)
	}

	entries, _ := b.ListDreamDiaryEntries(ctx, DreamDiaryFilter{Scope: "node"})
	if len(entries) != 2 {
		t.Fatalf("expected 2 synthetic entries, got %d", len(entries))
	}
	byDate := map[string]DreamDiaryEntry{}
	for _, e := range entries {
		if !e.Synthetic {
			t.Fatalf("backfill entries must be synthetic")
		}
		byDate[e.Date] = e
	}
	day1Str := time.Unix(day1, 0).UTC().Format(dreamDiaryDateLayout)
	day2Str := time.Unix(day2, 0).UTC().Format(dreamDiaryDateLayout)
	if e, ok := byDate[day1Str]; !ok || e.CandidatesConsidered != 2 || len(e.PromotedRecordIDs) != 2 {
		t.Fatalf("day1 bucket wrong: %+v", e)
	}
	if e, ok := byDate[day2Str]; !ok || e.CandidatesConsidered != 2 || len(e.PromotedRecordIDs) != 1 {
		t.Fatalf("day2 bucket wrong: %+v", e)
	}

	// Idempotent re-run: no new entries, both skipped.
	res2, err := b.BackfillDreamDiary(ctx, BackfillDreamDiaryOptions{Days: 30, Scope: "node", Now: now})
	if err != nil {
		t.Fatalf("backfill re-run: %v", err)
	}
	if res2.EntriesCreated != 0 || res2.EntriesSkipped != 2 {
		t.Fatalf("expected idempotent re-run (0 created, 2 skipped), got created=%d skipped=%d", res2.EntriesCreated, res2.EntriesSkipped)
	}
	after, _ := b.ListDreamDiaryEntries(ctx, DreamDiaryFilter{Scope: "node"})
	if len(after) != 2 {
		t.Fatalf("re-run must not duplicate entries, got %d", len(after))
	}
}
