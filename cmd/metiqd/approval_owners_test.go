package main

import (
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/gateway/methods"
)

func TestDurableApprovalOwnersSurviveRestartAndKeepTerminalHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	reg, err := newExecApprovalsRegistryAt(path)
	if err != nil {
		t.Fatal(err)
	}
	plugin, err := reg.RequestOwned("plugin", map[string]any{
		"pluginId":    "calendar",
		"operation":   "install",
		"permissions": []any{"network"},
	}, 60_000)
	if err != nil {
		t.Fatal(err)
	}
	system, err := reg.RequestOwned("system", map[string]any{
		"operation": "agent-model-change",
		"agentId":   "worker",
	}, 60_000)
	if err != nil {
		t.Fatal(err)
	}

	pending, err := applyApprovalList(reg, methods.ApprovalListRequest{Status: "pending"})
	if err != nil || pending["count"] != 2 {
		t.Fatalf("pending = %#v, %v", pending, err)
	}
	resolved, err := applyApprovalResolve(reg, methods.ApprovalResolveRequest{ID: plugin.ID, Kind: "plugin", Decision: "allow-once"})
	if err != nil {
		t.Fatal(err)
	}
	approval := resolved["approval"].(map[string]any)
	if approval["status"] != "allowed" || approval["kind"] != "plugin" {
		t.Fatalf("resolved approval = %#v", approval)
	}
	if _, err := applyApprovalResolve(reg, methods.ApprovalResolveRequest{ID: plugin.ID, Kind: "plugin", Decision: "deny"}); err == nil {
		t.Fatal("second answer replaced the durable first answer")
	}
	if _, err := applyApprovalResolve(reg, methods.ApprovalResolveRequest{ID: system.ID, Kind: "plugin", Decision: "deny"}); err == nil {
		t.Fatal("wrong owner resolved system approval")
	}

	reopened, err := newExecApprovalsRegistryAt(path)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := reopened.GetApproval(plugin.ID)
	if err != nil || terminal.Status != "resolved" || terminal.Decision != "approve" || terminal.Kind != "plugin" {
		t.Fatalf("terminal = %#v, %v", terminal, err)
	}
	stillPending, err := reopened.GetApproval(system.ID)
	if err != nil || stillPending.Status != "pending" || stillPending.Kind != "system" {
		t.Fatalf("system = %#v, %v", stillPending, err)
	}
	history, err := applyApprovalList(reopened, methods.ApprovalListRequest{Status: "resolved"})
	if err != nil || history["count"] != 1 {
		t.Fatalf("history = %#v, %v", history, err)
	}
}

func TestDurableApprovalRequestFailsClosedWhenLedgerCannotCommit(t *testing.T) {
	reg := newExecApprovalsRegistry()
	reg.storagePath = filepath.Join(t.TempDir(), "missing-parent", "nested", "approvals.json")
	// Make the would-be parent a regular file so the atomic ledger write fails.
	parent := filepath.Dir(reg.storagePath)
	if err := os.MkdirAll(filepath.Dir(parent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.RequestOwned("plugin", map[string]any{"operation": "install"}, 60_000); err == nil {
		t.Fatal("approval request succeeded without durable ledger commit")
	}
	if got := reg.ListApprovals("", ""); len(got) != 0 {
		t.Fatalf("failed request leaked into memory: %#v", got)
	}
}
