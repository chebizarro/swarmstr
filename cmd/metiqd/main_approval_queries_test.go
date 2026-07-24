package main

import (
	"testing"
	"time"

	"metiq/internal/gateway/methods"
)

func TestExecApprovalGetListFiltersExpiresAndSorts(t *testing.T) {
	reg := newExecApprovalsRegistry()
	first := reg.Request(methods.ExecApprovalRequestRequest{Command: "first", TimeoutMS: 60_000})
	second := reg.Request(methods.ExecApprovalRequestRequest{Command: "second", TimeoutMS: 60_000})
	expired := reg.Request(methods.ExecApprovalRequestRequest{Command: "expired", TimeoutMS: 60_000})
	reg.mu.Lock()
	firstRec := reg.pending[first.ID]
	firstRec.Requested = 10
	reg.pending[first.ID] = firstRec
	secondRec := reg.pending[second.ID]
	secondRec.Requested = 20
	reg.pending[second.ID] = secondRec
	expiredRec := reg.pending[expired.ID]
	expiredRec.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	reg.pending[expired.ID] = expiredRec
	reg.mu.Unlock()

	listed, err := applyExecApprovalList(reg, methods.ExecApprovalListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0]["id"] != first.ID || listed[1]["id"] != second.ID {
		t.Fatalf("listed=%#v", listed)
	}
	if _, err := applyExecApprovalGet(reg, methods.ExecApprovalGetRequest{ID: expired.ID}); err == nil {
		t.Fatal("expected expired approval to be absent")
	}
	got, err := applyExecApprovalGet(reg, methods.ExecApprovalGetRequest{ID: first.ID})
	if err != nil || got["commandText"] != "first" || got["expiresAtMs"] == nil {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestUnifiedApprovalGetResolveExec(t *testing.T) {
	reg := newExecApprovalsRegistry()
	rec := reg.Request(methods.ExecApprovalRequestRequest{Command: "git status", TimeoutMS: 60_000, AllowAlwaysAvailable: true})
	got, err := applyApprovalGet(reg, methods.ExecApprovalGetRequest{ID: rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	approval := got["approval"].(map[string]any)
	presentation := approval["presentation"].(map[string]any)
	if approval["status"] != "pending" || presentation["kind"] != "exec" {
		t.Fatalf("approval=%#v", approval)
	}
	resolved, err := applyApprovalResolve(reg, methods.ApprovalResolveRequest{ID: rec.ID, Kind: "exec", Decision: "allow-always"})
	if err != nil {
		t.Fatal(err)
	}
	terminal := resolved["approval"].(map[string]any)
	if resolved["applied"] != true || terminal["status"] != "allowed" || terminal["decision"] != "allow-always" {
		t.Fatalf("resolved=%#v", resolved)
	}
	if len(reg.ListPending()) != 0 {
		t.Fatal("resolved approval remained pending")
	}
}
