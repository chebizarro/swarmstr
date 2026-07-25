package memory

import (
	"context"
	"testing"
	"time"
)

func TestMemoryMigrationPlanAndApply(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	ctx := context.Background()

	// Seed fully-populated chunk rows via Add (which also mirrors them into the
	// unified memory_records table), then drop the unified rows so the chunks
	// present as pending backfill work — exactly the legacy-store shape the
	// migration plan is meant to detect.
	addTestMemory(backend, "m1", "alpha bravo charlie", "topic-a")
	addTestMemory(backend, "m2", "delta echo foxtrot", "topic-b")
	if err := backend.ensureUnifiedSchema(); err != nil {
		t.Fatalf("ensureUnifiedSchema: %v", err)
	}
	if _, err := backend.db.Exec(`DELETE FROM memory_records`); err != nil {
		t.Fatalf("clear memory_records: %v", err)
	}

	plan, err := backend.MemoryMigrationPlan(ctx)
	if err != nil {
		t.Fatalf("MemoryMigrationPlan: %v", err)
	}
	if plan.TargetVersion != sqliteSchemaVersion {
		t.Fatalf("target version = %d, want %d", plan.TargetVersion, sqliteSchemaVersion)
	}
	if plan.CurrentVersion != sqliteSchemaVersion {
		t.Fatalf("current version = %d, want %d", plan.CurrentVersion, sqliteSchemaVersion)
	}
	if plan.SchemaPending {
		t.Fatalf("schema should not be pending on a freshly opened store")
	}
	if plan.LegacyChunks != 2 {
		t.Fatalf("legacy chunks = %d, want 2", plan.LegacyChunks)
	}
	if !plan.Dirty {
		t.Fatalf("plan should be dirty with legacy chunks pending")
	}
	foundBackfill := false
	for _, step := range plan.Steps {
		if step.ID == "backfill_chunks" && step.Pending && step.Count == 2 {
			foundBackfill = true
		}
	}
	if !foundBackfill {
		t.Fatalf("expected pending backfill step, got steps %+v", plan.Steps)
	}

	// Dry run must not mutate the store.
	dry, err := backend.MemoryMigrationApply(ctx, MemoryMigrationApplyOptions{Backfill: true, DryRun: true})
	if err != nil {
		t.Fatalf("MemoryMigrationApply dry run: %v", err)
	}
	if !dry.DryRun {
		t.Fatalf("expected DryRun=true in report")
	}
	if dry.After.LegacyChunks != 2 {
		t.Fatalf("dry run mutated store: legacy chunks after = %d, want 2", dry.After.LegacyChunks)
	}
	for _, a := range dry.Actions {
		if a.Applied {
			t.Fatalf("dry run reported an applied action: %+v", a)
		}
	}

	// Real apply backfills the unified records.
	report, err := backend.MemoryMigrationApply(ctx, MemoryMigrationApplyOptions{Backfill: true})
	if err != nil {
		t.Fatalf("MemoryMigrationApply: %v", err)
	}
	if report.After.LegacyChunks != 0 {
		t.Fatalf("legacy chunks after apply = %d, want 0", report.After.LegacyChunks)
	}
	appliedBackfill := false
	for _, a := range report.Actions {
		if a.ID == "backfill_chunks" && a.Applied {
			appliedBackfill = true
		}
	}
	if !appliedBackfill {
		t.Fatalf("expected applied backfill action, got %+v", report.Actions)
	}
}

func TestMemoryMigrationApplyRebuildsFTS(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	ctx := context.Background()
	addTestMemory(backend, "m1", "alpha bravo charlie", "topic-a")

	report, err := backend.MemoryMigrationApply(ctx, MemoryMigrationApplyOptions{RebuildFTS: true})
	if err != nil {
		t.Fatalf("MemoryMigrationApply: %v", err)
	}
	rebuilt := false
	for _, a := range report.Actions {
		if a.ID == "fts_rebuild" && a.Applied {
			rebuilt = true
		}
	}
	if !rebuilt {
		t.Fatalf("expected applied fts_rebuild action, got %+v", report.Actions)
	}
}

func TestRunREMHarnessDryRunAndApply(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	ctx := context.Background()

	addTestMemory(backend, "hot-1", "recurring insight about deployments", "ops")
	now := time.Now().Unix()
	// Seed a recall record that clears DefaultPromotionConfig thresholds
	// (recall>=3, unique>=2, score>=0.75) so the memory is a live promotion
	// candidate.
	if _, err := backend.db.Exec(`INSERT INTO recall_tracking
		(memory_id, recall_count, unique_queries, query_hashes, last_recall_unix, first_recall_unix, avg_score, promoted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`,
		"hot-1", 5, 3, "[]", now, now-100, 0.9); err != nil {
		t.Fatalf("seed recall_tracking: %v", err)
	}

	dry, err := backend.RunREMHarness(ctx, REMHarnessOptions{})
	if err != nil {
		t.Fatalf("RunREMHarness dry run: %v", err)
	}
	if dry.Phase != DreamingPhaseREM {
		t.Fatalf("default phase = %q, want rem", dry.Phase)
	}
	if dry.Applied {
		t.Fatalf("dry run should not report Applied")
	}
	if dry.Candidates < 1 {
		t.Fatalf("dry run candidates = %d, want >=1", dry.Candidates)
	}
	if dry.Promoted != 0 {
		t.Fatalf("dry run must not promote, got promoted = %d", dry.Promoted)
	}
	if dry.Narrative == "" {
		t.Fatalf("expected a non-empty narrative")
	}

	var promotedAt any
	if err := backend.db.QueryRow(`SELECT promoted_at FROM recall_tracking WHERE memory_id = ?`, "hot-1").Scan(&promotedAt); err != nil {
		t.Fatalf("read promoted_at after dry run: %v", err)
	}
	if promotedAt != nil {
		t.Fatalf("dry run mutated recall_tracking.promoted_at = %v", promotedAt)
	}

	applied, err := backend.RunREMHarness(ctx, REMHarnessOptions{Apply: true})
	if err != nil {
		t.Fatalf("RunREMHarness apply: %v", err)
	}
	if !applied.Applied {
		t.Fatalf("apply run should report Applied")
	}
	if applied.Promoted < 1 {
		t.Fatalf("apply promoted = %d, want >=1", applied.Promoted)
	}
}

func TestRunREMHarnessRejectsUnknownPhaseDefaultsToREM(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	res, err := backend.RunREMHarness(context.Background(), REMHarnessOptions{Phase: DreamingPhase("bogus")})
	if err != nil {
		t.Fatalf("RunREMHarness: %v", err)
	}
	if res.Phase != DreamingPhaseREM {
		t.Fatalf("unknown phase should default to rem, got %q", res.Phase)
	}
}
