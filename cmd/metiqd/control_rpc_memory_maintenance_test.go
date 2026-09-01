package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/gateway/methods"
	"metiq/internal/memory"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func newSQLiteBackedStore(t *testing.T) memory.Store {
	t.Helper()
	dir := t.TempDir()
	idx, err := memory.OpenIndex(filepath.Join(dir, "memory.json"))
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	be, err := memory.OpenSQLiteBackend(filepath.Join(dir, "memory.sqlite"))
	if err != nil {
		t.Fatalf("OpenSQLiteBackend: %v", err)
	}
	return memory.NewHybridIndex(idx, be)
}

func memoryMaintCall(t *testing.T, h controlRPCHandler, method, params string) (nostruntime.ControlRPCResult, bool, error) {
	t.Helper()
	return h.handleMemoryMaintenanceRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, state.ConfigDoc{})
}

func TestMemoryMaintenance_MigrationsPlan(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{memoryIndex: newSQLiteBackedStore(t)})
	res, handled, err := memoryMaintCall(t, h, methods.MethodMigrationsMemoryPlan, `{}`)
	if !handled || err != nil {
		t.Fatalf("plan handled=%v err=%v", handled, err)
	}
	out := res.Result.(map[string]any)
	if out["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", out)
	}
	plan, ok := out["plan"].(memory.MemoryMigrationPlan)
	if !ok {
		t.Fatalf("expected plan payload, got %#v", out["plan"])
	}
	if plan.Backend != "sqlite" || plan.TargetVersion == 0 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestMemoryMaintenance_MigrationsApplyDryRun(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{memoryIndex: newSQLiteBackedStore(t)})
	res, handled, err := memoryMaintCall(t, h, methods.MethodMigrationsMemoryApply, `{"rebuildFts":true,"dryRun":true}`)
	if !handled || err != nil {
		t.Fatalf("apply handled=%v err=%v", handled, err)
	}
	out := res.Result.(map[string]any)
	report, ok := out["report"].(memory.MemoryMigrationApplyReport)
	if !ok {
		t.Fatalf("expected report payload, got %#v", out["report"])
	}
	if !report.DryRun {
		t.Fatalf("expected DryRun=true")
	}
	for _, a := range report.Actions {
		if a.Applied {
			t.Fatalf("dry run should not apply actions: %#v", a)
		}
	}
}

func TestMemoryMaintenance_RepairAndDedupe(t *testing.T) {
	store := newSQLiteBackedStore(t)
	h := newControlRPCHandler(controlRPCDeps{memoryIndex: store})

	res, handled, err := memoryMaintCall(t, h, methods.MethodDoctorMemoryRepairDreamingArtifacts, `{}`)
	if !handled || err != nil {
		t.Fatalf("repair handled=%v err=%v", handled, err)
	}
	if _, ok := res.Result.(map[string]any)["report"].(memory.MemoryHealthRepairReport); !ok {
		t.Fatalf("expected repair report, got %#v", res.Result)
	}

	res, handled, err = memoryMaintCall(t, h, methods.MethodDoctorMemoryDedupeDreamDiary, `{}`)
	if !handled || err != nil {
		t.Fatalf("dedupe handled=%v err=%v", handled, err)
	}
	if _, ok := res.Result.(map[string]any)["compaction"].(memory.CompactionResult); !ok {
		t.Fatalf("expected compaction result, got %#v", res.Result)
	}
}

func TestMemoryMaintenance_RemHarness(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{memoryIndex: newSQLiteBackedStore(t)})
	res, handled, err := memoryMaintCall(t, h, methods.MethodDoctorMemoryRemHarness, `{"phase":"rem"}`)
	if !handled || err != nil {
		t.Fatalf("remHarness handled=%v err=%v", handled, err)
	}
	harness, ok := res.Result.(map[string]any)["harness"].(memory.REMHarnessResult)
	if !ok {
		t.Fatalf("expected harness result, got %#v", res.Result)
	}
	if harness.Phase != memory.DreamingPhaseREM {
		t.Fatalf("expected rem phase, got %q", harness.Phase)
	}
	if harness.Applied {
		t.Fatalf("default harness run must be a dry run")
	}
}

func TestMemoryMaintenance_RemHarnessRejectsBadPhase(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{memoryIndex: newSQLiteBackedStore(t)})
	_, handled, err := memoryMaintCall(t, h, methods.MethodDoctorMemoryRemHarness, `{"phase":"deep"}`)
	if !handled {
		t.Fatalf("expected method to be handled")
	}
	if err == nil || !strings.Contains(err.Error(), "phase") {
		t.Fatalf("expected phase validation error, got %v", err)
	}
}

func TestMemoryMaintenance_DreamDiaryReadAndBackfill(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{memoryIndex: newSQLiteBackedStore(t)})

	res, handled, err := memoryMaintCall(t, h, methods.MethodDoctorMemoryDreamDiary, `{}`)
	if !handled || err != nil {
		t.Fatalf("dreamDiary handled=%v err=%v", handled, err)
	}
	out := res.Result.(map[string]any)
	if out["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", out)
	}
	if _, ok := out["entries"].([]memory.DreamDiaryEntry); !ok {
		t.Fatalf("expected entries payload, got %#v", out["entries"])
	}

	res, handled, err = memoryMaintCall(t, h, methods.MethodDoctorMemoryBackfillDreamDiary, `{"days":30}`)
	if !handled || err != nil {
		t.Fatalf("backfill handled=%v err=%v", handled, err)
	}
	if _, ok := res.Result.(map[string]any)["backfill"].(memory.BackfillDreamDiaryResult); !ok {
		t.Fatalf("expected backfill result, got %#v", res.Result)
	}
}

func TestMemoryMaintenance_ResetDreamDiaryConfirmationGate(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{memoryIndex: newSQLiteBackedStore(t)})

	// Tokenless call previews the required token and does not apply.
	res, handled, err := memoryMaintCall(t, h, methods.MethodDoctorMemoryResetDreamDiary, `{"scope":"agentA"}`)
	if !handled || err != nil {
		t.Fatalf("preview handled=%v err=%v", handled, err)
	}
	out := res.Result.(map[string]any)
	if out["applied"] != false {
		t.Fatalf("tokenless call must not apply: %#v", out)
	}
	confirmation := out["confirmation"].(memory.MaintenanceConfirmation)
	if !confirmation.Required || confirmation.ConfirmToken == "" {
		t.Fatalf("expected confirmation token in preview: %#v", confirmation)
	}

	// Wrong token is rejected.
	_, handled, err = memoryMaintCall(t, h, methods.MethodDoctorMemoryResetDreamDiary, `{"scope":"agentA","confirm":"nope"}`)
	if !handled {
		t.Fatalf("expected handled")
	}
	if err == nil {
		t.Fatalf("expected mismatch error for wrong token")
	}

	// Correct token applies.
	params := `{"scope":"agentA","confirm":"` + confirmation.ConfirmToken + `"}`
	res, handled, err = memoryMaintCall(t, h, methods.MethodDoctorMemoryResetDreamDiary, params)
	if !handled || err != nil {
		t.Fatalf("apply handled=%v err=%v", handled, err)
	}
	if res.Result.(map[string]any)["applied"] != true {
		t.Fatalf("expected applied=true with correct token: %#v", res.Result)
	}
}

func TestMemoryMaintenance_ResetGroundedShortTermConfirmationGate(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{memoryIndex: newSQLiteBackedStore(t)})

	// Store-wide (empty scope key) preview.
	res, handled, err := memoryMaintCall(t, h, methods.MethodDoctorMemoryResetGroundedShortTerm, `{}`)
	if !handled || err != nil {
		t.Fatalf("preview handled=%v err=%v", handled, err)
	}
	out := res.Result.(map[string]any)
	if out["applied"] != false {
		t.Fatalf("tokenless call must not apply: %#v", out)
	}
	confirmation := out["confirmation"].(memory.MaintenanceConfirmation)
	if confirmation.ConfirmToken == "" || confirmation.StateVersion == "" {
		t.Fatalf("expected state-bound token for empty scope: %#v", confirmation)
	}

	// Correct token applies (empty tier -> demoted 0, but applied).
	params := `{"confirm":"` + confirmation.ConfirmToken + `"}`
	res, handled, err = memoryMaintCall(t, h, methods.MethodDoctorMemoryResetGroundedShortTerm, params)
	if !handled || err != nil {
		t.Fatalf("apply handled=%v err=%v", handled, err)
	}
	if res.Result.(map[string]any)["applied"] != true {
		t.Fatalf("expected applied=true: %#v", res.Result)
	}
}

func TestMemoryMaintenance_GroundedScopeTokenIsolation(t *testing.T) {
	// A token minted for one agent must not authorize another agent's scope.
	h := newControlRPCHandler(controlRPCDeps{memoryIndex: newSQLiteBackedStore(t)})
	preview, handled, err := memoryMaintCall(t, h, methods.MethodDoctorMemoryResetGroundedShortTerm, `{"AgentID":"agentA"}`)
	if !handled || err != nil {
		t.Fatalf("preview handled=%v err=%v", handled, err)
	}
	tokenA := preview.Result.(map[string]any)["confirmation"].(memory.MaintenanceConfirmation).ConfirmToken
	params := `{"AgentID":"agentB","confirm":"` + tokenA + `"}`
	_, handled, err = memoryMaintCall(t, h, methods.MethodDoctorMemoryResetGroundedShortTerm, params)
	if !handled {
		t.Fatalf("expected handled")
	}
	if err == nil {
		t.Fatalf("cross-scope token must be rejected")
	}
}

func TestMemoryMaintenance_StoreUnavailable(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	_, handled, err := memoryMaintCall(t, h, methods.MethodMigrationsMemoryPlan, `{}`)
	if !handled {
		t.Fatalf("expected method to be handled")
	}
	if err == nil || !strings.Contains(err.Error(), "memory store unavailable") {
		t.Fatalf("expected store-unavailable error, got %v", err)
	}
}

func TestMemoryMaintenance_UnownedMethodNotHandled(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	_, handled, _ := memoryMaintCall(t, h, methods.MethodDoctorMemoryStatus, `{}`)
	if handled {
		t.Fatalf("doctor.memory.status must not be claimed by the maintenance handler")
	}
}
