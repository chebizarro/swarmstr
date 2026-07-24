package main

import (
	"context"
	"testing"

	"metiq/internal/security/commandanalysis"
)

func TestPluginRuntimeServicesUsesLiveExecApprovalRegistry(t *testing.T) {
	previous := controlExecApprovals
	registry := newExecApprovalsRegistry()
	controlExecApprovals = registry
	t.Cleanup(func() { controlExecApprovals = previous })

	analysis := commandanalysis.Analyze("", []string{"cat", "README.md"})
	if !analysis.AllowAlways || analysis.Signature == "" {
		t.Fatalf("expected safe command analysis: %+v", analysis)
	}
	registry.SetGlobal(map[string]any{"allow_always_signatures": []string{analysis.Signature}})
	services := pluginRuntimeServices(nil)

	evaluated, err := services.ExecApprovalEvaluate(context.Background(), map[string]any{"argv": []string{"cat", "README.md"}})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if evaluated["allowed"] != true || evaluated["decision"] != "allow" {
		t.Fatalf("unexpected evaluation: %#v", evaluated)
	}
	requested, err := services.ExecApprovalRequest(context.Background(), map[string]any{
		"command":      "cat README.md",
		"command_argv": []string{"cat", "README.md"},
		"timeout_ms":   5000,
	})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if requested["status"] != "pending" || requested["id"] == "" {
		t.Fatalf("unexpected approval request: %#v", requested)
	}
	snapshot := services.ExecApprovalSnapshot()
	if signatures, ok := snapshot["allow_always_signatures"].([]string); !ok || len(signatures) != 1 || signatures[0] != analysis.Signature {
		t.Fatalf("unexpected live snapshot: %#v", snapshot)
	}
}

func TestPluginRuntimeServicesFailsClosedBeforeRegistryInitialization(t *testing.T) {
	previous := controlExecApprovals
	controlExecApprovals = nil
	t.Cleanup(func() { controlExecApprovals = previous })
	services := pluginRuntimeServices(nil)
	if _, err := services.ExecApprovalEvaluate(context.Background(), map[string]any{"argv": []string{"cat", "README.md"}}); err == nil {
		t.Fatal("evaluation succeeded without live registry")
	}
	if _, err := services.ExecApprovalRequest(context.Background(), map[string]any{"command": "cat README.md"}); err == nil {
		t.Fatal("request succeeded without live registry")
	}
}
