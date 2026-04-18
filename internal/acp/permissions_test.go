package acp

import (
	"testing"
)

func TestPermissionMode_IsValid(t *testing.T) {
	tests := []struct {
		mode PermissionMode
		want bool
	}{
		{PermissionApproveAll, true},
		{PermissionApproveReads, true},
		{PermissionDenyAll, true},
		{"some-invalid", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := tt.mode.IsValid(); got != tt.want {
			t.Errorf("PermissionMode(%q).IsValid() = %v, want %v", tt.mode, got, tt.want)
		}
	}
}

func TestNonInteractivePolicy_IsValid(t *testing.T) {
	tests := []struct {
		policy NonInteractivePolicy
		want   bool
	}{
		{PolicyDeny, true},
		{PolicyFail, true},
		{"unknown", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := tt.policy.IsValid(); got != tt.want {
			t.Errorf("NonInteractivePolicy(%q).IsValid() = %v, want %v", tt.policy, got, tt.want)
		}
	}
}

func TestCheckPermission_ApproveAll(t *testing.T) {
	for _, op := range []OperationKind{OperationRead, OperationWrite, OperationExecute} {
		d := CheckPermission(PermissionApproveAll, op)
		if !d.Allowed {
			t.Errorf("approve-all should allow %s, got denied: %s", op, d.Reason)
		}
	}
}

func TestCheckPermission_ApproveReads(t *testing.T) {
	d := CheckPermission(PermissionApproveReads, OperationRead)
	if !d.Allowed {
		t.Fatalf("approve-reads should allow read, got denied: %s", d.Reason)
	}
	for _, op := range []OperationKind{OperationWrite, OperationExecute} {
		d := CheckPermission(PermissionApproveReads, op)
		if d.Allowed {
			t.Errorf("approve-reads should deny %s, got allowed", op)
		}
	}
}

func TestCheckPermission_DenyAll(t *testing.T) {
	for _, op := range []OperationKind{OperationRead, OperationWrite, OperationExecute} {
		d := CheckPermission(PermissionDenyAll, op)
		if d.Allowed {
			t.Errorf("deny-all should deny %s, got allowed", op)
		}
	}
}

func TestCheckPermission_UnknownMode(t *testing.T) {
	d := CheckPermission("bogus", OperationRead)
	if d.Allowed {
		t.Fatal("unknown mode should deny, got allowed")
	}
}

func TestApplyNonInteractivePolicy_Deny(t *testing.T) {
	d, err := ApplyNonInteractivePolicy(PolicyDeny, OperationWrite)
	if err != nil {
		t.Fatalf("PolicyDeny should not return error, got: %v", err)
	}
	if d.Allowed {
		t.Fatal("PolicyDeny should deny")
	}
}

func TestApplyNonInteractivePolicy_Fail(t *testing.T) {
	d, err := ApplyNonInteractivePolicy(PolicyFail, OperationExecute)
	if err == nil {
		t.Fatal("PolicyFail should return error")
	}
	if d.Allowed {
		t.Fatal("PolicyFail should deny")
	}
}

func TestApplyNonInteractivePolicy_Unknown(t *testing.T) {
	_, err := ApplyNonInteractivePolicy("bogus", OperationRead)
	if err == nil {
		t.Fatal("unknown policy should return error")
	}
}

func TestCheckPermission_AllCombinations(t *testing.T) {
	// Exhaustive truth table.
	type expect struct {
		mode    PermissionMode
		op      OperationKind
		allowed bool
	}
	table := []expect{
		{PermissionApproveAll, OperationRead, true},
		{PermissionApproveAll, OperationWrite, true},
		{PermissionApproveAll, OperationExecute, true},
		{PermissionApproveReads, OperationRead, true},
		{PermissionApproveReads, OperationWrite, false},
		{PermissionApproveReads, OperationExecute, false},
		{PermissionDenyAll, OperationRead, false},
		{PermissionDenyAll, OperationWrite, false},
		{PermissionDenyAll, OperationExecute, false},
	}
	for _, tc := range table {
		d := CheckPermission(tc.mode, tc.op)
		if d.Allowed != tc.allowed {
			t.Errorf("CheckPermission(%q, %q) = %v, want %v", tc.mode, tc.op, d.Allowed, tc.allowed)
		}
		if d.Reason == "" {
			t.Errorf("CheckPermission(%q, %q) has empty reason", tc.mode, tc.op)
		}
	}
}
