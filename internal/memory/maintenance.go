package memory

// maintenance.go — memory-store maintenance operations backing the gateway
// migrations.memory.* and doctor.memory.remHarness methods.
//
// migrations.memory.plan / apply operate over the real versioned SQLite schema
// (schema_version table + sqliteSchemaVersion + the unified memory_records /
// memory_fts / memory_embeddings tables). remHarness exercises the real REM
// dreaming/consolidation phase (RunDreamingPhases / PromotionManager) in a
// non-committing dry run by default.

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Migration plan / apply
// ---------------------------------------------------------------------------

// MemoryMigrationStep describes one unit of work in a memory-store migration
// plan (or one action taken by an apply run).
type MemoryMigrationStep struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"` // schema | fts_rebuild | reindex | backfill
	Description string `json:"description"`
	Pending     bool   `json:"pending,omitempty"`
	Applied     bool   `json:"applied,omitempty"`
	Count       int    `json:"count,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// MemoryMigrationPlan reports the difference between the on-disk memory store
// and the schema/index state the current binary expects.
type MemoryMigrationPlan struct {
	Backend        string                `json:"backend"`
	Path           string                `json:"path,omitempty"`
	CurrentVersion int                   `json:"current_version"`
	TargetVersion  int                   `json:"target_version"`
	SchemaPending  bool                  `json:"schema_pending"`
	FTSDrift       int                   `json:"fts_drift"`
	ReindexBacklog int                   `json:"reindex_backlog"`
	LegacyChunks   int                   `json:"legacy_chunks"`
	Steps          []MemoryMigrationStep `json:"steps"`
	Dirty          bool                  `json:"dirty"`
}

// MemoryMigrationApplyOptions controls migrations.memory.apply.
type MemoryMigrationApplyOptions struct {
	// RebuildFTS forces a full-text index rebuild even when no drift is
	// detected.
	RebuildFTS bool `json:"rebuild_fts,omitempty"`
	// Backfill imports legacy chunk rows into the unified memory_records table.
	Backfill bool `json:"backfill,omitempty"`
	// DryRun computes the plan and the actions that would run without mutating
	// the store.
	DryRun bool `json:"dry_run,omitempty"`
}

// MemoryMigrationApplyReport reports the result of migrations.memory.apply.
type MemoryMigrationApplyReport struct {
	Before  MemoryMigrationPlan   `json:"before"`
	After   MemoryMigrationPlan   `json:"after"`
	Actions []MemoryMigrationStep `json:"actions"`
	DryRun  bool                  `json:"dry_run"`
}

// MemoryMigrationPlan computes a read-only migration/maintenance plan. It never
// creates or repairs schema objects; missing objects make SchemaPending true even
// when a stale schema_version row already equals the target.
func (b *SQLiteBackend) MemoryMigrationPlan(ctx context.Context) (MemoryMigrationPlan, error) {
	if err := ctx.Err(); err != nil {
		return MemoryMigrationPlan{}, err
	}
	plan := MemoryMigrationPlan{Backend: "sqlite", Path: b.path, TargetVersion: sqliteSchemaVersion}
	if tableExists(b.db, "schema_version") {
		_ = b.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&plan.CurrentVersion)
	}
	hasRecords := tableExists(b.db, "memory_records")
	hasFTS := tableExists(b.db, "memory_fts")
	hasEmbeddings := tableExists(b.db, "memory_embeddings")
	plan.SchemaPending = plan.CurrentVersion < plan.TargetVersion || !unifiedSchemaObjectsPresent(b.db)

	var records, fts int
	if hasRecords {
		_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_records`).Scan(&records)
	}
	if hasRecords && hasFTS {
		_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_fts`).Scan(&fts)
		plan.FTSDrift = absInt(records - fts)
	}
	if hasRecords && hasEmbeddings {
		_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_records r LEFT JOIN memory_embeddings e ON e.record_id = r.id WHERE r.deleted_at IS NULL AND e.record_id IS NULL`).Scan(&plan.ReindexBacklog)
	}
	if tableExists(b.db, "chunks") {
		if hasRecords {
			_ = b.db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE id NOT IN (SELECT id FROM memory_records)`).Scan(&plan.LegacyChunks)
		} else {
			_ = b.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&plan.LegacyChunks)
		}
	}

	if plan.SchemaPending {
		count := plan.TargetVersion - plan.CurrentVersion
		if count < 1 {
			count = 1
		}
		plan.Steps = append(plan.Steps, MemoryMigrationStep{ID: "schema_upgrade", Kind: "schema", Pending: true, Count: count, Description: fmt.Sprintf("migrate unified schema from v%d to v%d", plan.CurrentVersion, plan.TargetVersion)})
	}
	if plan.FTSDrift > 0 {
		plan.Steps = append(plan.Steps, MemoryMigrationStep{ID: "fts_rebuild", Kind: "fts_rebuild", Pending: true, Count: plan.FTSDrift, Description: "rebuild memory_fts to match memory_records"})
	}
	if plan.LegacyChunks > 0 {
		plan.Steps = append(plan.Steps, MemoryMigrationStep{ID: "backfill_chunks", Kind: "backfill", Pending: true, Count: plan.LegacyChunks, Description: "backfill unified memory_records from legacy chunks"})
	}
	if plan.ReindexBacklog > 0 {
		plan.Steps = append(plan.Steps, MemoryMigrationStep{ID: "embedding_reindex", Kind: "reindex", Pending: true, Count: plan.ReindexBacklog, Description: "records missing vector embeddings (reindexed lazily on query)"})
	}
	plan.Dirty = plan.SchemaPending || plan.FTSDrift > 0 || plan.LegacyChunks > 0
	return plan, nil
}

func unifiedSchemaObjectsPresent(db *sql.DB) bool {
	required := map[string][]string{
		"table": {
			"memory_records", "memory_fts", "memory_sources", "memory_sync_state",
			"memory_eval_cases", "memory_eval_runs", "memory_events_outbox",
			"memory_embeddings", "memory_nostr_provenance", "reindex_status",
			"memory_maintenance_state", "memory_health_scores",
		},
		"trigger": {"memory_records_ai", "memory_records_ad", "memory_records_au"},
	}
	for objectType, names := range required {
		for _, name := range names {
			var found string
			if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = ? AND name = ?`, objectType, name).Scan(&found); err != nil {
				return false
			}
		}
	}
	return true
}

// MemoryMigrationApply executes the migration plan. FTS rebuild and legacy
// backfill run only when the plan reports pending work (or the corresponding
// option forces them). Embedding reindex is intentionally left to the lazy
// idle-reindex path and is only reported, never forced here.
//
// A non-dry-run apply is a store-mutating maintenance op: it runs under the
// store maintenance gate (swarmstr-r34j) so it cannot interleave with
// compaction / promotion / repair. A dry run mutates nothing and is left
// ungated so a planning preview is never serialized behind live maintenance.
func (b *SQLiteBackend) MemoryMigrationApply(ctx context.Context, opts MemoryMigrationApplyOptions) (MemoryMigrationApplyReport, error) {
	if opts.DryRun {
		return b.memoryMigrationApply(ctx, opts)
	}
	if err := ctx.Err(); err != nil {
		return MemoryMigrationApplyReport{}, err
	}
	var report MemoryMigrationApplyReport
	var applyErr error
	gateErr := b.WithMaintenanceLock(func() error {
		report, applyErr = b.memoryMigrationApply(ctx, opts)
		return applyErr
	})
	if applyErr != nil {
		return report, applyErr
	}
	if gateErr != nil {
		return MemoryMigrationApplyReport{}, gateErr
	}
	return report, nil
}

// memoryMigrationApply is the ungated core. For a non-dry run the caller holds
// the maintenance gate. Ordering matters: data migration (backfill + FTS
// rebuild) runs FIRST and schema_version is bumped LAST, and only when the data
// migration succeeded, so CurrentVersion advances only on a fully-migrated store.
func (b *SQLiteBackend) memoryMigrationApply(ctx context.Context, opts MemoryMigrationApplyOptions) (MemoryMigrationApplyReport, error) {
	before, err := b.MemoryMigrationPlan(ctx)
	if err != nil {
		return MemoryMigrationApplyReport{}, err
	}
	report := MemoryMigrationApplyReport{Before: before, DryRun: opts.DryRun}
	if !opts.DryRun && before.SchemaPending {
		if err := b.ensureUnifiedSchema(); err != nil {
			return report, err
		}
	}

	rebuildFTS := before.FTSDrift > 0 || opts.RebuildFTS
	backfill := before.LegacyChunks > 0 || opts.Backfill

	// dataOK gates the schema_version bump: it flips false if any data-migration
	// step fails, so a partial migration never advances the recorded version.
	dataOK := true

	var backfillStep *MemoryMigrationStep
	if backfill {
		step := MemoryMigrationStep{ID: "backfill_chunks", Kind: "backfill", Description: "backfill unified memory_records from legacy chunks", Pending: true, Count: before.LegacyChunks}
		if !opts.DryRun {
			n, err := b.BackfillUnifiedFromChunks(ctx)
			step.Count = n
			if err != nil {
				step.Detail = err.Error()
				dataOK = false
			} else {
				step.Applied = true
			}
		}
		backfillStep = &step
	}

	var ftsStep *MemoryMigrationStep
	if rebuildFTS {
		step := MemoryMigrationStep{ID: "fts_rebuild", Kind: "fts_rebuild", Description: "rebuild memory_fts index", Pending: before.FTSDrift > 0, Count: before.FTSDrift}
		if !opts.DryRun {
			if !dataOK {
				step.Detail = "skipped: prior data migration step failed"
			} else if _, err := b.db.Exec(`INSERT INTO memory_fts(memory_fts) VALUES('rebuild')`); err != nil {
				step.Detail = err.Error()
				dataOK = false
			} else {
				step.Applied = true
			}
		}
		ftsStep = &step
	}

	// Schema version LAST, only after data migration + FTS rebuild succeeded.
	schemaStep := MemoryMigrationStep{ID: "schema_upgrade", Kind: "schema", Description: fmt.Sprintf("ensure unified schema at v%d", before.TargetVersion), Pending: before.SchemaPending}
	if !opts.DryRun {
		if !dataOK {
			schemaStep.Detail = "skipped schema_version bump: data migration/FTS rebuild failed"
		} else if err := b.stampSchemaVersion(); err != nil {
			schemaStep.Detail = err.Error()
		} else {
			schemaStep.Applied = true
		}
	}
	// Report the schema action first for readability, then the data steps that
	// actually ran before it.
	report.Actions = append(report.Actions, schemaStep)
	if backfillStep != nil {
		report.Actions = append(report.Actions, *backfillStep)
	}
	if ftsStep != nil {
		report.Actions = append(report.Actions, *ftsStep)
	}

	after, err := b.MemoryMigrationPlan(ctx)
	if err != nil {
		return MemoryMigrationApplyReport{}, err
	}
	report.After = after
	recordMemoryTelemetry("migration_apply", time.Time{}, map[string]any{"ok": true, "dry_run": opts.DryRun, "actions": len(report.Actions), "fts_drift_before": before.FTSDrift, "fts_drift_after": after.FTSDrift, "legacy_before": before.LegacyChunks, "legacy_after": after.LegacyChunks})
	return report, nil
}

// ---------------------------------------------------------------------------
// REM / dreaming consolidation harness
// ---------------------------------------------------------------------------

// REMHarnessOptions configures a REM/dreaming consolidation harness run.
type REMHarnessOptions struct {
	// Phase selects the dreaming phase to exercise (default: rem).
	Phase DreamingPhase `json:"phase,omitempty"`
	// Limit caps the number of consolidation candidates considered.
	Limit int `json:"limit,omitempty"`
	// Apply commits promotions. When false (default) the harness is a dry run
	// that selects candidates and synthesizes a narrative without mutating the
	// store.
	Apply bool `json:"apply,omitempty"`
}

// REMHarnessResult reports one harness run.
type REMHarnessResult struct {
	Phase      DreamingPhase `json:"phase"`
	Applied    bool          `json:"applied"`
	Candidates int           `json:"candidates"`
	Promoted   int           `json:"promoted"`
	Narrative  string        `json:"narrative"`
	DurationMS int64         `json:"duration_ms"`
}

// RunREMHarness exercises the memory consolidation ("dreaming") phase against
// the live store. By default it is a non-committing dry run: it selects the
// consolidation candidates the given phase would act on and synthesizes the
// deterministic narrative, without promoting anything. Setting Apply=true runs
// a real promotion sweep for the phase.
func (b *SQLiteBackend) RunREMHarness(ctx context.Context, opts REMHarnessOptions) (REMHarnessResult, error) {
	if err := b.ensureUnifiedSchema(); err != nil {
		return REMHarnessResult{}, err
	}
	phase := opts.Phase
	if phase != DreamingPhaseLight && phase != DreamingPhaseREM {
		phase = DreamingPhaseREM
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	start := time.Now()
	cfg := DefaultPromotionConfig()
	manager := NewPromotionManager(b, cfg)

	var result REMHarnessResult
	// run performs candidate selection and (for apply) the promotion sweep under
	// PromotionManager.mu. Selection happens INSIDE this closure so that on the
	// apply path it runs while the maintenance gate is held (candidates cannot be
	// demoted/removed between selection and promotion).
	run := func() error {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		if manager.tracker != nil {
			if err := manager.tracker.Flush(); err != nil {
				return err
			}
		}
		originalMax := manager.cfg.MaxBatchSize
		if limit > 0 && (manager.cfg.MaxBatchSize <= 0 || limit < manager.cfg.MaxBatchSize) {
			manager.cfg.MaxBatchSize = limit
		}
		candidates, err := manager.FindCandidates()
		manager.cfg.MaxBatchSize = originalMax
		if err != nil {
			return err
		}
		candidates = filterDreamingCandidates(phase, candidates, limit)

		result = REMHarnessResult{Phase: phase, Applied: opts.Apply, Candidates: len(candidates)}
		if opts.Apply && len(candidates) > 0 {
			byTopic := map[string][]PromotionCandidate{}
			now := time.Now().UTC().Unix()
			for _, candidate := range candidates {
				topic := candidate.Memory.Topic
				if topic == "" {
					topic = manager.cfg.PromotedTopic
				}
				byTopic[topic] = append(byTopic[topic], candidate)
			}
			for topic, group := range byTopic {
				// promoteGroup returns the IDs actually committed even on a
				// file-mirror error, so count its length rather than dropping the
				// group.
				promotedIDs, _ := manager.promoteGroup(topic, group, now)
				result.Promoted += len(promotedIDs)
			}
		}
		// 0 => BuildDreamingNarrative applies its default cap (1200 chars).
		result.Narrative = BuildDreamingNarrative(phase, candidates, result.Promoted, 0)
		return nil
	}

	if opts.Apply {
		// Mutating apply path: serialize behind the store maintenance gate before
		// acquiring PromotionManager.mu (lock order maintenanceMu → manager.mu).
		if err := ctx.Err(); err != nil {
			return REMHarnessResult{}, err
		}
		if err := b.WithMaintenanceLock(run); err != nil {
			return REMHarnessResult{}, err
		}
	} else {
		// Dry run is read-only; do NOT take the maintenance gate so a preview is
		// never serialized behind long-running maintenance.
		if err := run(); err != nil {
			return REMHarnessResult{}, err
		}
	}

	result.DurationMS = time.Since(start).Milliseconds()
	recordMemoryTelemetry("rem_harness", time.Time{}, map[string]any{"ok": true, "phase": string(phase), "apply": opts.Apply, "candidates": result.Candidates, "promoted": result.Promoted})
	return result, nil
}

// ---------------------------------------------------------------------------
// Store-level wrappers (type-assert the concrete backend, honest error if the
// active backend has no versioned-migration / consolidation machinery).
// ---------------------------------------------------------------------------

// MemoryMigrationPlanner is implemented by backends that support versioned
// migration planning over stored memory.
type MemoryMigrationPlanner interface {
	MemoryMigrationPlan(context.Context) (MemoryMigrationPlan, error)
	MemoryMigrationApply(context.Context, MemoryMigrationApplyOptions) (MemoryMigrationApplyReport, error)
}

// REMHarnessRunner is implemented by backends that can exercise the REM/dreaming
// consolidation phase.
type REMHarnessRunner interface {
	RunREMHarness(context.Context, REMHarnessOptions) (REMHarnessResult, error)
}

// PlanMemoryMigration computes a migration plan for the store, or returns an
// error when the active backend has no versioned-migration machinery.
func PlanMemoryMigration(ctx context.Context, store Store) (MemoryMigrationPlan, error) {
	if planner, ok := any(store).(MemoryMigrationPlanner); ok {
		return planner.MemoryMigrationPlan(ctx)
	}
	return MemoryMigrationPlan{}, fmt.Errorf("memory store does not support versioned migrations")
}

// ApplyMemoryMigration executes a migration plan for the store, or returns an
// error when the active backend has no versioned-migration machinery.
func ApplyMemoryMigration(ctx context.Context, store Store, opts MemoryMigrationApplyOptions) (MemoryMigrationApplyReport, error) {
	if planner, ok := any(store).(MemoryMigrationPlanner); ok {
		return planner.MemoryMigrationApply(ctx, opts)
	}
	return MemoryMigrationApplyReport{}, fmt.Errorf("memory store does not support versioned migrations")
}

// RunMemoryREMHarness exercises the consolidation phase for the store, or
// returns an error when the active backend has no dreaming/consolidation
// machinery.
func RunMemoryREMHarness(ctx context.Context, store Store, opts REMHarnessOptions) (REMHarnessResult, error) {
	if runner, ok := any(store).(REMHarnessRunner); ok {
		return runner.RunREMHarness(ctx, opts)
	}
	return REMHarnessResult{}, fmt.Errorf("memory store does not support REM consolidation harness")
}
