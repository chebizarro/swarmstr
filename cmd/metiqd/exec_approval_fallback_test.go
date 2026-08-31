package main

import (
	"testing"

	"metiq/internal/gateway/methods"
)

func TestExecApprovalFallbackOutcomeIsDurableAndImmutable(t *testing.T) {
	reg := newExecApprovalsRegistry()
	rec, err := reg.RequestDurable(methods.ExecApprovalRequestRequest{Command: "printf ok", TimeoutMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := reg.ResolveFallback(rec.ID, true, "full fallback permits execution")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "resolved" || resolved.Decision != "approve" || resolved.GrantScope != "policy-fallback" {
		t.Fatalf("fallback outcome = %#v", resolved)
	}
	if _, err := reg.Resolve(methods.ExecApprovalResolveRequest{ID: rec.ID, Decision: "deny", Reason: "late reviewer"}); err == nil {
		t.Fatal("late reviewer replaced a durable fallback outcome")
	}
}

func TestExecApprovalFallbackRejectsReviewerDenialWithExpiryText(t *testing.T) {
	reg := newExecApprovalsRegistry()
	rec, err := reg.RequestDurable(methods.ExecApprovalRequestRequest{Command: "printf ok", TimeoutMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Resolve(methods.ExecApprovalResolveRequest{ID: rec.ID, Decision: "deny", Reason: "approval expired"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.ResolveFallback(rec.ID, true, "full fallback"); err == nil {
		t.Fatal("reviewer denial was mistaken for a system expiry")
	}
}

func TestExecApprovalFallbackMayReplaceOnlySystemExpiry(t *testing.T) {
	reg := newExecApprovalsRegistry()
	rec, err := reg.RequestDurable(methods.ExecApprovalRequestRequest{Command: "printf ok", TimeoutMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reg.terminalizePending(rec.ID, "approval expired"); err != nil {
		t.Fatal(err)
	}
	resolved, err := reg.ResolveFallback(rec.ID, false, "deny fallback")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Decision != "deny" || resolved.Reason != "deny fallback" || resolved.GrantScope != "policy-fallback" {
		t.Fatalf("fallback outcome = %#v", resolved)
	}
}
