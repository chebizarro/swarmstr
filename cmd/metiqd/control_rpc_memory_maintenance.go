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
// mapped onto swarmstr's memory subsystem.
//
// The persisted dream-diary + grounded-short-term subsystem (swarmstr-qc53) is
// now implemented, so the remaining four OpenClaw ops are handled here for real:
//
//	doctor.memory.dreamDiary              — read/list durable diary entries
//	doctor.memory.backfillDreamDiary      — replay consolidation over existing
//	                                        memories into retroactive dated entries
//	doctor.memory.resetDreamDiary         — clear the diary (confirm-gated)
//	doctor.memory.resetGroundedShortTerm  — demote/unpromote the grounded-short-
//	                                        term buffer (confirm-gated, reversible)
//
// The two reset ops are per-scope and require a confirmation token: a tokenless
// call previews the required token; a mismatched token is rejected.

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

	case methods.MethodDoctorMemoryDreamDiary:
		req, err := methods.DecodeDoctorMemoryDreamDiaryParams(in.Params)
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
		entries, err := memory.ListMemoryDreamDiary(ctx, store, memory.DreamDiaryFilter{
			Scope:     req.Scope,
			Phase:     memory.DreamingPhase(req.Phase),
			SinceUnix: req.SinceUnix,
			UntilUnix: req.UntilUnix,
			Synthetic: req.Synthetic,
			Limit:     req.Limit,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "entries": entries, "count": len(entries)}}, true, nil

	case methods.MethodDoctorMemoryBackfillDreamDiary:
		req, err := methods.DecodeDoctorMemoryBackfillDreamDiaryParams(in.Params)
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
		result, err := memory.BackfillMemoryDreamDiary(ctx, store, memory.BackfillDreamDiaryOptions{
			Days:  req.Days,
			Scope: req.Scope,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "backfill": result}}, true, nil

	case methods.MethodDoctorMemoryResetDreamDiary:
		req, err := methods.DecodeDoctorMemoryResetDreamDiaryParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		confirmation, err := memory.EvaluateMaintenanceConfirmation("resetDreamDiary", req.Scope, req.Confirm)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		store, err := h.memoryMaintenanceStore()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if !confirmation.Confirmed {
			return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "applied": false, "confirmation": confirmation, "note": "confirmation token required; re-issue with the confirm token to clear the diary"}}, true, nil
		}
		removed, err := memory.ResetMemoryDreamDiary(ctx, store, req.Scope)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "applied": true, "removed": removed, "scope": req.Scope, "confirmation": confirmation}}, true, nil

	case methods.MethodDoctorMemoryResetGroundedShortTerm:
		req, err := methods.DecodeDoctorMemoryResetGroundedShortTermParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		scopeKey := req.ScopeKey()
		confirmation, err := memory.EvaluateMaintenanceConfirmation("resetGroundedShortTerm", scopeKey, req.Confirm)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		store, err := h.memoryMaintenanceStore()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		opts := memory.GroundedShortTermOptions{
			Window: time.Duration(req.WindowHours) * time.Hour,
			Scope: memory.ScopedContext{
				Scope:        state.NormalizeAgentMemoryScope(req.ScopeKind),
				AgentID:      req.AgentID,
				WorkspaceDir: req.WorkspaceDir,
				SessionID:    req.SessionID,
			},
		}
		if req.RequireCitation != nil {
			opts = opts.WithExplicitCitation(*req.RequireCitation)
		}
		if !confirmation.Confirmed {
			return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "applied": false, "confirmation": confirmation, "note": "confirmation token required; re-issue with the confirm token to demote the grounded-short-term buffer"}}, true, nil
		}
		result, err := memory.ResetMemoryGroundedShortTerm(ctx, store, opts)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "applied": true, "result": result, "scope": scopeKey, "confirmation": confirmation}}, true, nil

	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
