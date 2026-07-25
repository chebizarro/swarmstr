package methods

// Param schemas for the memory-maintenance long tail (swarmstr-wvwk):
// migrations.memory.plan / migrations.memory.apply and the mutating
// doctor.memory.* consolidation/dedupe/repair ops. Shapes mirror OpenClaw
// src/gateway/server-methods/doctor*.ts + memory-migrations, adapted to
// swarmstr's memory subsystem (internal/memory diagnostics + schema_version).

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxRemHarnessLimit bounds the number of consolidation candidates a single
// harness run will consider, keeping a diagnostic call bounded.
const maxRemHarnessLimit = 5000

// MigrationsMemoryPlanRequest takes no parameters — the plan is computed over
// the active store.
type MigrationsMemoryPlanRequest struct{}

// MigrationsMemoryApplyRequest controls migrations.memory.apply.
type MigrationsMemoryApplyRequest struct {
	// RebuildFTS forces a full-text index rebuild even without detected drift.
	RebuildFTS bool `json:"rebuildFts,omitempty"`
	// Backfill imports legacy chunk rows into the unified records table.
	Backfill bool `json:"backfill,omitempty"`
	// DryRun reports the actions that would run without mutating the store.
	DryRun bool `json:"dryRun,omitempty"`
}

// DoctorMemoryRepairRequest controls doctor.memory.repairDreamingArtifacts.
// It maps onto memory.MemoryHealthRepairOptions: swarmstr's "dreaming
// artifacts" are the promoted/consolidated memory records, so repair runs the
// memory-health repair pass (supersession/index/dedupe integrity).
type DoctorMemoryRepairRequest struct {
	// SafeOnly restricts repairs to the safe subset (default true).
	SafeOnly bool `json:"safeOnly,omitempty"`
	// FixAll additionally applies non-safe repairs (stale flag / conflict).
	FixAll bool `json:"fixAll,omitempty"`
	// safeOnlySet tracks whether the caller explicitly provided safeOnly so the
	// default can be true without silently overriding an explicit false.
	safeOnlySet bool `json:"-"`
}

// DoctorMemoryDedupeRequest controls doctor.memory.dedupeDreamDiary. swarmstr
// has no persisted diary artifact; the analogous de-duplication target is the
// consolidated/promoted memory records, deduped via the compaction path.
type DoctorMemoryDedupeRequest struct {
	// ExpireStale also expires stale/expired records during the sweep. When
	// false the sweep is dedupe-only.
	ExpireStale bool `json:"expireStale,omitempty"`
}

// DoctorMemoryRemHarnessRequest controls doctor.memory.remHarness.
type DoctorMemoryRemHarnessRequest struct {
	// Phase selects the dreaming phase to exercise ("light" | "rem"; default
	// "rem").
	Phase string `json:"phase,omitempty"`
	// Limit caps consolidation candidates considered (default 100).
	Limit int `json:"limit,omitempty"`
	// Apply commits promotions. Default false = non-committing dry run.
	Apply bool `json:"apply,omitempty"`
}

func (r MigrationsMemoryPlanRequest) Normalize() (MigrationsMemoryPlanRequest, error) {
	return r, nil
}

func (r MigrationsMemoryApplyRequest) Normalize() (MigrationsMemoryApplyRequest, error) {
	return r, nil
}

func (r DoctorMemoryRepairRequest) Normalize() (DoctorMemoryRepairRequest, error) {
	if !r.safeOnlySet {
		r.SafeOnly = true
	}
	return r, nil
}

func (r DoctorMemoryDedupeRequest) Normalize() (DoctorMemoryDedupeRequest, error) {
	return r, nil
}

func (r DoctorMemoryRemHarnessRequest) Normalize() (DoctorMemoryRemHarnessRequest, error) {
	r.Phase = strings.ToLower(strings.TrimSpace(r.Phase))
	switch r.Phase {
	case "", "rem", "light":
	default:
		return r, fmt.Errorf("invalid doctor.memory.remHarness params: phase must be \"light\" or \"rem\"")
	}
	if r.Limit < 0 {
		return r, fmt.Errorf("invalid doctor.memory.remHarness params: limit must not be negative")
	}
	if r.Limit > maxRemHarnessLimit {
		r.Limit = maxRemHarnessLimit
	}
	return r, nil
}

func DecodeMigrationsMemoryPlanParams(params json.RawMessage) (MigrationsMemoryPlanRequest, error) {
	return decodeMethodParams[MigrationsMemoryPlanRequest](params)
}

func DecodeMigrationsMemoryApplyParams(params json.RawMessage) (MigrationsMemoryApplyRequest, error) {
	return decodeMethodParams[MigrationsMemoryApplyRequest](params)
}

func DecodeDoctorMemoryRepairParams(params json.RawMessage) (DoctorMemoryRepairRequest, error) {
	req, err := decodeMethodParams[DoctorMemoryRepairRequest](params)
	if err != nil {
		return req, err
	}
	// Detect whether safeOnly was explicitly present so Normalize can default
	// it to true only when omitted.
	if len(params) > 0 {
		var probe struct {
			SafeOnly *bool `json:"safeOnly"`
		}
		if json.Unmarshal(params, &probe) == nil && probe.SafeOnly != nil {
			req.safeOnlySet = true
		}
	}
	return req, nil
}

func DecodeDoctorMemoryDedupeParams(params json.RawMessage) (DoctorMemoryDedupeRequest, error) {
	return decodeMethodParams[DoctorMemoryDedupeRequest](params)
}

func DecodeDoctorMemoryRemHarnessParams(params json.RawMessage) (DoctorMemoryRemHarnessRequest, error) {
	return decodeMethodParams[DoctorMemoryRemHarnessRequest](params)
}
