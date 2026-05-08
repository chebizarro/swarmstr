package main

import (
	"context"
	"log"
	"time"

	"metiq/internal/memory"
	"metiq/internal/store/state"
)

func startMemoryMaintenance(ctx context.Context, store memory.Store, currentConfig func() state.ConfigDoc) {
	if store == nil || currentConfig == nil {
		return
	}
	runMemoryStartupMaintenance(ctx, store, currentConfig())
	go func() {
		policy := memory.MemoryCompactionPolicyFromMap(memoryExtraConfig(currentConfig()))
		policy = memory.NormalizeMemoryCompactionPolicy(policy)
		ticker := time.NewTicker(policy.CheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				policy = memory.MemoryCompactionPolicyFromMap(memoryExtraConfig(currentConfig()))
				decision, result, ran, err := memory.CompactMemoryIfDue(ctx, store, policy, false)
				if err != nil {
					log.Printf("memory compaction worker: %v", err)
					continue
				}
				if decision.Skipped {
					log.Printf("memory compaction worker skipped reason=%s trigger=%s", decision.SkipReason, decision.Reason)
					continue
				}
				if ran {
					log.Printf("memory compaction worker ran trigger=%s expired=%d deduped=%d supersession_fix=%d stale=%d", decision.Reason, result.Expired, result.Deduped, result.SupersessionFix, result.StaleFlagged)
				}
			}
		}
	}()
}

func runMemoryStartupMaintenance(ctx context.Context, store memory.Store, cfg state.ConfigDoc) {
	extra := memoryExtraConfig(cfg)
	if boolFromAny(extra["health_check_on_startup"]) || boolFromAny(extra["auto_fix_safe_issues"]) {
		report, err := memory.MemoryHealth(ctx, store)
		if err != nil {
			log.Printf("memory health startup check failed: %v", err)
		} else {
			log.Printf("memory health startup status=%s score=%.3f issues=%d", report.Status, report.HealthScore, len(report.Warnings))
			if boolFromAny(extra["auto_fix_safe_issues"]) && report.Status != "ok" {
				if repair, repairErr := memory.RepairMemoryHealth(ctx, store, memory.MemoryHealthRepairOptions{SafeOnly: true}); repairErr != nil {
					log.Printf("memory health safe repair failed: %v", repairErr)
				} else {
					log.Printf("memory health safe repair actions=%d score_before=%.3f score_after=%.3f", len(repair.Actions), repair.Before.HealthScore, repair.After.HealthScore)
				}
			}
		}
	}
	policy := memory.MemoryCompactionPolicyFromMap(extra)
	decision, result, ran, err := memory.CompactMemoryIfDue(ctx, store, policy, true)
	if err != nil {
		log.Printf("memory startup compaction failed: %v", err)
		return
	}
	if decision.Skipped {
		log.Printf("memory startup compaction skipped reason=%s trigger=%s", decision.SkipReason, decision.Reason)
		return
	}
	if ran {
		log.Printf("memory startup compaction ran trigger=%s expired=%d deduped=%d supersession_fix=%d stale=%d", decision.Reason, result.Expired, result.Deduped, result.SupersessionFix, result.StaleFlagged)
	}
}

func memoryExtraConfig(cfg state.ConfigDoc) map[string]any {
	if cfg.Extra == nil {
		return nil
	}
	if extra, ok := cfg.Extra["memory"].(map[string]any); ok {
		return extra
	}
	return nil
}

func boolFromAny(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1" || t == "yes"
	case float64:
		return t != 0
	case int:
		return t != 0
	}
	return false
}
