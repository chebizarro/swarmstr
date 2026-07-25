package main

// control_rpc_memory_maintenance.go — control-RPC handlers for the memory-
// maintenance long tail (swarmstr-wvwk, WS-C):
//
//	migrations.memory.plan                  — compute a migration/maintenance
//	                                          plan over the versioned store
//	migrations.memory.apply                 — execute it (schema/FTS/backfill)
//	doctor.memory.repairDreamingArtifacts   — repair consolidated-record
//	                                          integrity (memory-health repair)
//	doctor.memory.dedupeDreamDiary          — dedupe consolidated/promoted
//	                                          records (compaction dedupe)
//	doctor.memory.remHarness                — exercise the REM consolidation
//	                                          phase (dry-run default)
//
// Mirrors OpenClaw src/gateway/server-methods/doctor*.ts + memory-migrations,
// mapped onto swarmstr's memory subsystem. The remaining OpenClaw ops —
// doctor.memory.dreamDiary / backfillDreamDiary / resetDreamDiary /
// resetGroundedShortTerm — are intentionally NOT handled here: swarmstr has no
// persisted dream-diary artifact store and no grounded-short-term memory tier,
// so exposing them would be a fake stub. They stay an honest UNAVAILABLE gap
// (unregistered -> parity status "missing"); the doctor/migrations triage
// prefixes are locked to the memory-health/implement category so a per-method
// accepted-deviation is not expressible in the parity matrix. Tracked by
// follow-up swarmstr-qc53.

import (
	"context"
	"fmt"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/memory"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) memoryMaintenanceStore() (memory.Store, error) {
	if h.deps.memoryIndex == nil {
		return nil, fmt.Errorf("memory store unavailable")
	}
	return h.deps.memoryIndex, nil
}

func (h controlRPCHandler) handleMemoryMaintenanceRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	_ = cfg
	switch method {
	case methods.MethodMigrationsMemoryPlan:
		if _, err := methods.DecodeMigrationsMemoryPlanParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		store, err := h.memoryMaintenanceStore()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		plan, err := memory.PlanMemoryMigration(ctx, store)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "plan": plan}}, true, nil

	case methods.MethodMigrationsMemoryApply:
		req, err := methods.DecodeMigrationsMemoryApplyParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		store, err := h.memoryMaintenanceStore()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		report, err := memory.ApplyMemoryMigration(ctx, store, memory.MemoryMigrationApplyOptions{
			RebuildFTS: req.RebuildFTS,
			Backfill:   req.Backfill,
			DryRun:     req.DryRun,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "report": report}}, true, nil

	case methods.MethodDoctorMemoryRepairDreamingArtifacts:
		req, err := methods.DecodeDoctorMemoryRepairParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		store, err := h.memoryMaintenanceStore()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		report, err := memory.RepairMemoryHealth(ctx, store, memory.MemoryHealthRepairOptions{
			SafeOnly: req.SafeOnly,
			FixAll:   req.FixAll,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		// Transparency: swarmstr promotes dreaming output into the general
		// consolidated-memory store rather than a separate diary artifact, so the
		// repair pass operates store-wide on record/index integrity.
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "report": report, "scope": "consolidated_memory_records", "note": "swarmstr has no separate dream-diary artifact; repairs record/index integrity store-wide"}}, true, nil

	case methods.MethodDoctorMemoryDedupeDreamDiary:
		req, err := methods.DecodeDoctorMemoryDedupeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		store, err := h.memoryMaintenanceStore()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := memory.CompactMemoryRecords(ctx, store, memory.CompactionConfig{
			Now:             time.Now().UTC(),
			Reason:          "dedupe_dream_diary",
			SkipDedupe:      false,
			SkipExpireStale: !req.ExpireStale,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		// Transparency: dedupe targets exact-duplicate consolidated/promoted
		// records store-wide (there is no separate diary artifact to dedupe).
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "compaction": result, "scope": "consolidated_memory_records", "note": "swarmstr has no separate dream-diary artifact; dedupes exact-duplicate consolidated records store-wide"}}, true, nil

	case methods.MethodDoctorMemoryRemHarness:
		req, err := methods.DecodeDoctorMemoryRemHarnessParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		store, err := h.memoryMaintenanceStore()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := memory.RunMemoryREMHarness(ctx, store, memory.REMHarnessOptions{
			Phase: memory.DreamingPhase(req.Phase),
			Limit: req.Limit,
			Apply: req.Apply,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "harness": result}}, true, nil

	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
