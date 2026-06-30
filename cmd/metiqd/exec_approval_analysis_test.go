package main

import "testing"

func TestExecApprovalRememberSignature(t *testing.T) {
	reg := newExecApprovalsRegistry()
	const sig = `["exec","git","status"]`
	if execApprovalSignatureAllowed(reg.GetGlobal(), sig) {
		t.Fatalf("signature should not be allowed before remember")
	}
	execApprovalRememberSignature(reg, sig)
	if !execApprovalSignatureAllowed(reg.GetGlobal(), sig) {
		t.Fatalf("signature should be allowed after remember")
	}
}

func TestApprovalCommandDisplayArgv(t *testing.T) {
	text, argv := approvalCommandDisplay("bash_exec", map[string]any{"cmd": []any{"git", "status"}})
	if text != "git status" || len(argv) != 2 || argv[0] != "git" || argv[1] != "status" {
		t.Fatalf("unexpected display: text=%q argv=%v", text, argv)
	}
}
