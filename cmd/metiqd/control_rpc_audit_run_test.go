package main

import (
	"context"
	"encoding/json"
	"testing"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/permissions"
	"metiq/internal/store/state"
)

func TestAuditRunInspectProjectsOnlyCorrelatedDurableDecisions(t *testing.T) {
	engine := permissions.NewEngine(t.TempDir(), permissions.PermissiveEngineConfig())
	for _, tool := range []string{"read_file", "write_file"} {
		req := permissions.NewToolRequest(tool, permissions.CategoryFilesystem).
			WithContext("user", "project", "agent", "session").
			WithExecution("run-1", "run-1")
		engine.Evaluate(context.Background(), req)
	}
	engine.Evaluate(context.Background(), permissions.NewToolRequest("other", permissions.CategoryBuiltin).WithExecution("run-2", "run-2"))

	h := newControlRPCHandler(controlRPCDeps{permEngine: engine})
	raw, _ := json.Marshal(map[string]any{"runId": "run-1", "decisionLimit": 1})
	result, handled, err := h.handleAuditRunRPC(context.Background(), nostruntime.ControlRPCInbound{Method: methods.MethodAuditRunInspect, Params: raw, Internal: true}, methods.MethodAuditRunInspect, state.ConfigDoc{})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	out := result.Result.(map[string]any)
	run := out["run"].(map[string]any)
	if run["status"] != "known" || run["runId"] != "run-1" || run["executionId"] != "run-1" {
		t.Fatalf("run=%+v", run)
	}
	if out["identity"].(map[string]any)["state"] != "unknown" {
		t.Fatalf("identity=%+v", out["identity"])
	}
	displays := out["decisionDisplays"].([]map[string]any)
	if len(displays) != 1 || out["nextDecisionCursor"] != "1" {
		t.Fatalf("displays=%+v next=%v", displays, out["nextDecisionCursor"])
	}
	if displays[0]["provenance"].(map[string]any)["state"] != "unverified" {
		t.Fatalf("display=%+v", displays[0])
	}
}
