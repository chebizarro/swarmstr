package main

import (
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/gateway/methods"
	"metiq/internal/security/commandanalysis"
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

func TestDurableExecPolicyAndExecutionBindingSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	reg, err := newExecApprovalsRegistryAt(path)
	if err != nil {
		t.Fatal(err)
	}
	policy := map[string]any{"mode": "allowlist", "ask": "on-miss", "allowlist": []any{"sh"}}
	if _, err := reg.SetGlobalChecked(policy); err != nil {
		t.Fatal(err)
	}

	cwd := t.TempDir()
	execReq := commandanalysis.ExecutionRequest{Argv: []string{"/bin/sh", "-c", "printf ok"}, CWD: cwd}
	binding, err := commandanalysis.CaptureExecutionBinding(execReq)
	if err != nil {
		t.Fatal(err)
	}
	canonicalCWD := binding.CanonicalCWD
	record, err := reg.RequestDurable(methods.ExecApprovalRequestRequest{
		Command:           "printf ok",
		CommandArgv:       append([]string(nil), binding.Argv...),
		CWD:               &canonicalCWD,
		TimeoutMS:         60_000,
		ExecutionBinding:  &binding,
		PolicyFingerprint: "sha256:test-policy",
	})
	if err != nil {
		t.Fatal(err)
	}

	reopened, err := newExecApprovalsRegistryAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.GetGlobal(); got["mode"] != "allowlist" || got["ask"] != "on-miss" {
		t.Fatalf("global policy did not survive restart: %#v", got)
	}
	got, err := reopened.GetApproval(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExecutionBinding == nil || got.ExecutionBinding.Fingerprint != binding.Fingerprint || got.PolicyFingerprint != "sha256:test-policy" {
		t.Fatalf("execution trust did not survive restart: %#v", got)
	}
	if err := commandanalysis.RevalidateExecutionBinding(*got.ExecutionBinding, execReq); err != nil {
		t.Fatalf("reloaded binding did not revalidate: %v", err)
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
