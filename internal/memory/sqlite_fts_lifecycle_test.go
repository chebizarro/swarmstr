package memory

import (
	"context"
	"testing"

	"metiq/internal/store/state"
)

func TestFTSLifecycleHealthyAtStartup(t *testing.T) {
	b := newTestSQLiteBackend(t)
	health := b.FTSHealth()
	if health.State != FTSStateHealthy || health.SourceRows != health.IndexedRows {
		t.Fatalf("unexpected startup health: %#v", health)
	}
	checked, err := b.CheckFTSHealth(context.Background())
	if err != nil || checked.TargetedReindexed != 0 || checked.FullRebuild {
		t.Fatalf("healthy check should not repair: health=%#v err=%v", checked, err)
	}
}

func TestFTSLifecycleRepairsMissingRowSelectively(t *testing.T) {
	b := newTestSQLiteBackend(t)
	b.Add(state.MemoryDoc{MemoryID: "one", SessionID: "s1", Text: "alpha memory"})
	b.Add(state.MemoryDoc{MemoryID: "two", SessionID: "s2", Text: "beta memory"})
	if _, err := b.db.Exec(`DELETE FROM chunks_fts WHERE rowid = (SELECT rowid FROM chunks WHERE id = 'one')`); err != nil {
		t.Fatal(err)
	}

	before, err := b.CheckFTSHealth(context.Background())
	if err == nil || before.MissingRows != 1 {
		t.Fatalf("expected one missing row: health=%#v err=%v", before, err)
	}
	after, err := b.EnsureFTSHealthy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.State != FTSStateHealthy || after.TargetedReindexed != 1 || after.FullRebuild {
		t.Fatalf("expected targeted repair: %#v", after)
	}
	results := b.Search("alpha", 5)
	if len(results) != 1 || results[0].MemoryID != "one" {
		t.Fatalf("repaired row is not searchable: %#v", results)
	}
}

func TestSearchSelfHealsMissingFTSRowAtRuntime(t *testing.T) {
	b := newTestSQLiteBackend(t)
	b.Add(state.MemoryDoc{MemoryID: "live", SessionID: "s-live", Text: "runtime drift sentinel"})
	if _, err := b.db.Exec(`DELETE FROM chunks_fts WHERE rowid = (SELECT rowid FROM chunks WHERE id = 'live')`); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.clearCacheLocked()
	b.mu.Unlock()

	results := b.SearchSession("s-live", "sentinel", 5)
	if len(results) != 1 || results[0].MemoryID != "live" {
		t.Fatalf("live search did not repair and retry: %#v", results)
	}
	health := b.FTSHealth()
	if health.State != FTSStateHealthy || health.TargetedReindexed != 1 {
		t.Fatalf("expected targeted runtime repair, got %#v", health)
	}
}

func TestSearchSelfHealsDroppedFTSTableAtRuntime(t *testing.T) {
	b := newTestSQLiteBackend(t)
	b.Add(state.MemoryDoc{MemoryID: "live", SessionID: "s-live", Text: "runtime query failure sentinel"})
	if _, err := b.db.Exec(`DROP TABLE chunks_fts`); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.clearCacheLocked()
	b.mu.Unlock()

	results := b.Search("sentinel", 5)
	if len(results) != 1 || results[0].MemoryID != "live" {
		t.Fatalf("failed query did not recreate, repair, and retry: %#v health=%#v", results, b.FTSHealth())
	}
	if health := b.FTSHealth(); health.State != FTSStateHealthy {
		t.Fatalf("expected healthy FTS after live recovery, got %#v", health)
	}
}

func TestReindexFTSSessionTargetsTranscriptRows(t *testing.T) {
	b := newTestSQLiteBackend(t)
	b.Add(state.MemoryDoc{MemoryID: "s1-user", SessionID: "s1", Role: "user", Text: "transcript question"})
	b.Add(state.MemoryDoc{MemoryID: "s1-assistant", SessionID: "s1", Role: "assistant", Text: "transcript answer"})
	b.Add(state.MemoryDoc{MemoryID: "s2-user", SessionID: "s2", Role: "user", Text: "other session"})

	reindexed, err := b.ReindexFTSSession(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if reindexed != 2 {
		t.Fatalf("expected two targeted transcript rows, got %d", reindexed)
	}
	health := b.FTSHealth()
	if health.State != FTSStateHealthy || health.TargetedReindexed != 2 {
		t.Fatalf("unexpected health after session reindex: %#v", health)
	}
}

func TestFTSLifecycleExposesDegradedState(t *testing.T) {
	b := newTestSQLiteBackend(t)
	if _, err := b.db.Exec(`DROP TABLE chunks_fts`); err != nil {
		t.Fatal(err)
	}
	health, err := b.CheckFTSHealth(context.Background())
	if err == nil || health.State != FTSStateDegraded || health.LastError == "" {
		t.Fatalf("expected degraded health: health=%#v err=%v", health, err)
	}
	status := b.MemoryStatus()
	if status.FTS == nil || !status.Primary.Degraded || status.Primary.Available {
		t.Fatalf("store status did not expose FTS degradation: %#v", status)
	}
}
