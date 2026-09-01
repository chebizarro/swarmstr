package main

import "testing"

func TestExecApprovalGrantListAndRevoke(t *testing.T) {
	reg := newExecApprovalsRegistry()
	const first = `["exec","git","status"]`
	const second = `["exec","go","test"]`
	if err := execApprovalRememberSignature(reg, first); err != nil {
		t.Fatal(err)
	}
	if err := execApprovalRememberSignature(reg, second); err != nil {
		t.Fatal(err)
	}
	grants, err := listExecApprovalGrants(reg, 1)
	if err != nil || len(grants) != 1 || grants[0].GrantID == "" || grants[0].Kind != "command-signature" {
		t.Fatalf("list grants: %+v err=%v", grants, err)
	}
	outcome, err := revokeExecApprovalGrant(reg, execApprovalGrantID(first))
	if err != nil || outcome != "revoked" {
		t.Fatalf("revoke: outcome=%q err=%v", outcome, err)
	}
	if execApprovalSignatureAllowed(reg.GetGlobal(), first) {
		t.Fatal("revoked signature remains allowed")
	}
	if !execApprovalSignatureAllowed(reg.GetGlobal(), second) {
		t.Fatal("unrelated signature was removed")
	}
	outcome, err = revokeExecApprovalGrant(reg, execApprovalGrantID(first))
	if err != nil || outcome != "not-found" {
		t.Fatalf("second revoke: outcome=%q err=%v", outcome, err)
	}
}
