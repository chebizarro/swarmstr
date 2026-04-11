package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"metiq/internal/store/state"
)

func draftPlan() state.PlanSpec {
	return state.PlanSpec{
		Version:  1,
		PlanID:   "plan-test",
		GoalID:   "goal-1",
		Title:    "Test plan",
		Revision: 1,
		Status:   state.PlanStatusDraft,
		Steps: []state.PlanStep{
			{StepID: "s1", Title: "Step 1", Instructions: "do 1", Status: state.PlanStepStatusPending},
			{StepID: "s2", Title: "Step 2", Instructions: "do 2", Status: state.PlanStepStatusPending, DependsOn: []string{"s1"}},
		},
	}
}

func activePlan() state.PlanSpec {
	p := draftPlan()
	p.Status = state.PlanStatusActive
	return p
}

// --- Preview tests ---

func TestPreview_DraftPlanNeedsApproval(t *testing.T) {
	ctrl := NewPlanController(nil)
	preview := ctrl.Preview(draftPlan(), state.AutonomyPlanApproval)

	if !preview.NeedsApproval {
		t.Error("draft plan under plan_approval mode should need approval")
	}
	if preview.TotalSteps != 2 {
		t.Errorf("TotalSteps = %d, want 2", preview.TotalSteps)
	}
	if preview.AutonomyMode != state.AutonomyPlanApproval {
		t.Errorf("AutonomyMode = %q", preview.AutonomyMode)
	}
}

func TestPreview_DraftPlanFullAutonomy(t *testing.T) {
	ctrl := NewPlanController(nil)
	preview := ctrl.Preview(draftPlan(), state.AutonomyFull)

	if preview.NeedsApproval {
		t.Error("draft plan under full autonomy should not need approval")
	}
}

func TestPreview_ActivePlanNoApproval(t *testing.T) {
	ctrl := NewPlanController(nil)
	preview := ctrl.Preview(activePlan(), state.AutonomyPlanApproval)

	if preview.NeedsApproval {
		t.Error("active plan should not need approval regardless of mode")
	}
}

func TestPreview_StepBreakdown(t *testing.T) {
	plan := state.PlanSpec{
		Version: 1, PlanID: "p", Title: "P", Revision: 1,
		Status: state.PlanStatusActive,
		Steps: []state.PlanStep{
			{StepID: "a", Title: "A", Status: state.PlanStepStatusCompleted},
			{StepID: "b", Title: "B", Status: state.PlanStepStatusFailed},
			{StepID: "c", Title: "C", Status: state.PlanStepStatusBlocked},
			{StepID: "d", Title: "D", Status: state.PlanStepStatusReady},
			{StepID: "e", Title: "E", Status: state.PlanStepStatusPending},
		},
	}
	ctrl := NewPlanController(nil)
	preview := ctrl.Preview(plan, state.AutonomyFull)

	if len(preview.CompletedSteps) != 1 {
		t.Errorf("CompletedSteps = %d, want 1", len(preview.CompletedSteps))
	}
	if len(preview.FailedSteps) != 1 {
		t.Errorf("FailedSteps = %d, want 1", len(preview.FailedSteps))
	}
	if len(preview.BlockedSteps) != 1 {
		t.Errorf("BlockedSteps = %d, want 1", len(preview.BlockedSteps))
	}
	// Ready should include status=ready (d) plus ReadySteps (e, since no deps).
	if len(preview.ReadySteps) < 1 {
		t.Errorf("ReadySteps = %d, want >= 1", len(preview.ReadySteps))
	}
}

func TestPreview_IncludesApprovalHistory(t *testing.T) {
	ctrl := NewPlanController(nil)
	plan := draftPlan()
	// Approve, then preview.
	ctrl.Approve(ApproveRequest{Plan: plan, Actor: "alice", Now: 1000})
	preview := ctrl.Preview(plan, state.AutonomyPlanApproval)

	if len(preview.ApprovalHistory) != 1 {
		t.Fatalf("ApprovalHistory = %d, want 1", len(preview.ApprovalHistory))
	}
	if preview.ApprovalHistory[0].Actor != "alice" {
		t.Errorf("approval actor = %q", preview.ApprovalHistory[0].Actor)
	}
}

// --- Approve tests ---

func TestApprove_DraftToActive(t *testing.T) {
	ctrl := NewPlanController(nil)
	plan, approval, err := ctrl.Approve(ApproveRequest{
		Plan:   draftPlan(),
		Actor:  "operator",
		Reason: "looks good",
		Now:    5000,
	})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if plan.Status != state.PlanStatusActive {
		t.Errorf("plan status = %q, want active", plan.Status)
	}
	if plan.Meta["approved_by"] != "operator" {
		t.Errorf("approved_by = %v", plan.Meta["approved_by"])
	}
	if approval.Decision != state.PlanApprovalApproved {
		t.Errorf("decision = %q", approval.Decision)
	}
	if approval.PlanID != "plan-test" {
		t.Errorf("approval PlanID = %q", approval.PlanID)
	}
}

func TestApprove_ActivePlanRejected(t *testing.T) {
	ctrl := NewPlanController(nil)
	_, _, err := ctrl.Approve(ApproveRequest{
		Plan:  activePlan(),
		Actor: "operator",
	})
	if err == nil {
		t.Fatal("approving an already-active plan should fail")
	}
}

func TestApprove_MissingActor(t *testing.T) {
	ctrl := NewPlanController(nil)
	_, _, err := ctrl.Approve(ApproveRequest{Plan: draftPlan()})
	if err == nil {
		t.Fatal("approving without actor should fail")
	}
}

func TestApprove_RevisingPlan(t *testing.T) {
	ctrl := NewPlanController(nil)
	plan := draftPlan()
	plan.Status = state.PlanStatusRevising
	result, _, err := ctrl.Approve(ApproveRequest{Plan: plan, Actor: "op", Now: 100})
	if err != nil {
		t.Fatalf("Approve revising plan: %v", err)
	}
	if result.Status != state.PlanStatusActive {
		t.Errorf("status = %q, want active", result.Status)
	}
}

// --- Reject tests ---

func TestReject_DraftToCancelled(t *testing.T) {
	ctrl := NewPlanController(nil)
	plan, approval, err := ctrl.Reject(RejectRequest{
		Plan:   draftPlan(),
		Actor:  "reviewer",
		Reason: "too many steps",
		Now:    6000,
	})
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if plan.Status != state.PlanStatusCancelled {
		t.Errorf("status = %q, want cancelled", plan.Status)
	}
	if plan.Meta["rejected_by"] != "reviewer" {
		t.Errorf("rejected_by = %v", plan.Meta["rejected_by"])
	}
	if approval.Decision != state.PlanApprovalRejected {
		t.Errorf("decision = %q", approval.Decision)
	}
}

func TestReject_MissingReason(t *testing.T) {
	ctrl := NewPlanController(nil)
	_, _, err := ctrl.Reject(RejectRequest{Plan: draftPlan(), Actor: "reviewer"})
	if err == nil {
		t.Fatal("rejecting without reason should fail")
	}
}

func TestReject_MissingActor(t *testing.T) {
	ctrl := NewPlanController(nil)
	_, _, err := ctrl.Reject(RejectRequest{Plan: draftPlan(), Reason: "bad"})
	if err == nil {
		t.Fatal("rejecting without actor should fail")
	}
}

func TestReject_TerminalPlanFails(t *testing.T) {
	ctrl := NewPlanController(nil)
	plan := draftPlan()
	plan.Status = state.PlanStatusCompleted
	_, _, err := ctrl.Reject(RejectRequest{Plan: plan, Actor: "op", Reason: "nah"})
	if err == nil {
		t.Fatal("rejecting a terminal plan should fail")
	}
}

// --- Amend tests ---

func TestAmend_GeneratesNewRevision(t *testing.T) {
	provider := &fakeProvider{text: `{
  "plan_id": "plan-amended",
  "title": "Amended plan",
  "steps": [
    {"step_id": "s1", "title": "Revised step", "instructions": "do better", "status": "pending"}
  ]
}`}
	p := New(provider)
	ctrl := NewPlanController(p)

	newPlan, approval, err := ctrl.Amend(context.Background(), AmendRequest{
		Plan:     draftPlan(),
		Goal:     validGoal(),
		Actor:    "operator",
		Feedback: "Please add error handling",
		Now:      7000,
	})
	if err != nil {
		t.Fatalf("Amend: %v", err)
	}
	if newPlan.Status != state.PlanStatusDraft {
		t.Errorf("amended plan status = %q, want draft (needs re-approval)", newPlan.Status)
	}
	if newPlan.Revision != 2 {
		t.Errorf("revision = %d, want 2", newPlan.Revision)
	}
	if approval.Decision != state.PlanApprovalAmended {
		t.Errorf("decision = %q", approval.Decision)
	}
}

func TestAmend_MissingFeedback(t *testing.T) {
	ctrl := NewPlanController(New(&fakeProvider{text: "{}"}))
	_, _, err := ctrl.Amend(context.Background(), AmendRequest{
		Plan:  draftPlan(),
		Goal:  validGoal(),
		Actor: "op",
	})
	if err == nil {
		t.Fatal("amending without feedback should fail")
	}
}

func TestAmend_NilPlanner(t *testing.T) {
	ctrl := NewPlanController(nil)
	_, _, err := ctrl.Amend(context.Background(), AmendRequest{
		Plan:     draftPlan(),
		Goal:     validGoal(),
		Actor:    "op",
		Feedback: "fix it",
	})
	if err == nil {
		t.Fatal("amending with nil planner should fail")
	}
}

func TestAmend_TerminalPlanFails(t *testing.T) {
	ctrl := NewPlanController(New(&fakeProvider{text: "{}"}))
	plan := draftPlan()
	plan.Status = state.PlanStatusCompleted
	_, _, err := ctrl.Amend(context.Background(), AmendRequest{
		Plan: plan, Goal: validGoal(), Actor: "op", Feedback: "fix",
	})
	if err == nil {
		t.Fatal("amending a terminal plan should fail")
	}
}

// --- MayCompile tests ---

func TestMayCompile_FullAutonomy(t *testing.T) {
	ctrl := NewPlanController(nil)
	if !ctrl.MayCompile(activePlan(), state.AutonomyFull) {
		t.Error("active plan under full autonomy should be compilable")
	}
}

func TestMayCompile_DraftNotCompilable(t *testing.T) {
	ctrl := NewPlanController(nil)
	if ctrl.MayCompile(draftPlan(), state.AutonomyFull) {
		t.Error("draft plan should not be compilable")
	}
}

func TestMayCompile_PlanApprovalWithoutApproval(t *testing.T) {
	ctrl := NewPlanController(nil)
	if ctrl.MayCompile(activePlan(), state.AutonomyPlanApproval) {
		t.Error("active plan without approval record should not be compilable under plan_approval mode")
	}
}

func TestMayCompile_PlanApprovalWithApproval(t *testing.T) {
	ctrl := NewPlanController(nil)
	plan := draftPlan()
	approved, _, _ := ctrl.Approve(ApproveRequest{Plan: plan, Actor: "op", Now: 100})
	if !ctrl.MayCompile(approved, state.AutonomyPlanApproval) {
		t.Error("approved active plan should be compilable under plan_approval mode")
	}
}

// --- MayExecuteStep tests ---

func TestMayExecuteStep_FullAutonomy(t *testing.T) {
	ctrl := NewPlanController(nil)
	if !ctrl.MayExecuteStep(activePlan(), "s1", state.AutonomyFull) {
		t.Error("full autonomy should allow step execution")
	}
}

func TestMayExecuteStep_StepApprovalWithout(t *testing.T) {
	ctrl := NewPlanController(nil)
	plan := draftPlan()
	approved, _, _ := ctrl.Approve(ApproveRequest{Plan: plan, Actor: "op", Now: 100})
	if ctrl.MayExecuteStep(approved, "s1", state.AutonomyStepApproval) {
		t.Error("step_approval mode without step approval should block execution")
	}
}

func TestMayExecuteStep_StepApprovalWith(t *testing.T) {
	ctrl := NewPlanController(nil)
	plan := draftPlan()
	approved, _, _ := ctrl.Approve(ApproveRequest{Plan: plan, Actor: "op", Now: 100})
	ctrl.ApproveStep(approved, "s1", "op", "", 200)
	if !ctrl.MayExecuteStep(approved, "s1", state.AutonomyStepApproval) {
		t.Error("step with approval should be executable under step_approval mode")
	}
}

// --- ApproveStep tests ---

func TestApproveStep_Valid(t *testing.T) {
	ctrl := NewPlanController(nil)
	approval, err := ctrl.ApproveStep(activePlan(), "s1", "op", "ready to go", 300)
	if err != nil {
		t.Fatalf("ApproveStep: %v", err)
	}
	if approval.Meta["step_id"] != "s1" {
		t.Errorf("step_id = %v", approval.Meta["step_id"])
	}
}

func TestApproveStep_MissingActor(t *testing.T) {
	ctrl := NewPlanController(nil)
	_, err := ctrl.ApproveStep(activePlan(), "s1", "", "", 0)
	if err == nil {
		t.Fatal("approve step without actor should fail")
	}
}

func TestApproveStep_UnknownStep(t *testing.T) {
	ctrl := NewPlanController(nil)
	_, err := ctrl.ApproveStep(activePlan(), "nonexistent", "op", "", 0)
	if err == nil {
		t.Fatal("approve step for unknown step_id should fail")
	}
}

// --- Approvals audit trail ---

func TestApprovals_AuditTrail(t *testing.T) {
	ctrl := NewPlanController(nil)
	if len(ctrl.Approvals()) != 0 {
		t.Fatal("fresh controller should have no approvals")
	}

	plan := draftPlan()
	ctrl.Approve(ApproveRequest{Plan: plan, Actor: "alice", Now: 100})
	ctrl.ApproveStep(plan, "s1", "bob", "", 200)

	approvals := ctrl.Approvals()
	if len(approvals) != 2 {
		t.Fatalf("approvals = %d, want 2", len(approvals))
	}
	if approvals[0].Actor != "alice" {
		t.Errorf("first approval actor = %q", approvals[0].Actor)
	}
	if approvals[1].Meta["step_id"] != "s1" {
		t.Errorf("second approval meta = %v", approvals[1].Meta)
	}
}

// --- AutonomyMode tests (model-level) ---

func TestAutonomyMode_ParseValid(t *testing.T) {
	tests := []struct {
		input string
		want  state.AutonomyMode
	}{
		{"full", state.AutonomyFull},
		{"plan_approval", state.AutonomyPlanApproval},
		{"step_approval", state.AutonomyStepApproval},
		{"supervised", state.AutonomySupervised},
		{"", state.AutonomyFull},
	}
	for _, tt := range tests {
		got, ok := state.ParseAutonomyMode(tt.input)
		if !ok {
			t.Errorf("ParseAutonomyMode(%q) returned !ok", tt.input)
		}
		if got != tt.want {
			t.Errorf("ParseAutonomyMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAutonomyMode_ParseInvalid(t *testing.T) {
	_, ok := state.ParseAutonomyMode("unknown_mode")
	if ok {
		t.Error("ParseAutonomyMode(unknown_mode) should return !ok")
	}
}

func TestAutonomyMode_RequiresPlanApproval(t *testing.T) {
	cases := map[state.AutonomyMode]bool{
		state.AutonomyFull:         false,
		state.AutonomyPlanApproval: true,
		state.AutonomyStepApproval: true,
		state.AutonomySupervised:   true,
	}
	for mode, want := range cases {
		if got := mode.RequiresPlanApproval(); got != want {
			t.Errorf("%s.RequiresPlanApproval() = %v, want %v", mode, got, want)
		}
	}
}

func TestAutonomyMode_RequiresStepApproval(t *testing.T) {
	cases := map[state.AutonomyMode]bool{
		state.AutonomyFull:         false,
		state.AutonomyPlanApproval: false,
		state.AutonomyStepApproval: true,
		state.AutonomySupervised:   true,
	}
	for mode, want := range cases {
		if got := mode.RequiresStepApproval(); got != want {
			t.Errorf("%s.RequiresStepApproval() = %v, want %v", mode, got, want)
		}
	}
}

// --- PlanApproval JSON round-trip ---

func TestPlanApproval_JSONRoundTrip(t *testing.T) {
	a := state.PlanApproval{
		PlanID:    "plan-1",
		Revision:  2,
		Decision:  state.PlanApprovalApproved,
		Actor:     "alice",
		Reason:    "good plan",
		CreatedAt: 5000,
		Meta:      map[string]any{"step_id": "s1"},
	}
	blob, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded state.PlanApproval
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.PlanID != "plan-1" || decoded.Decision != state.PlanApprovalApproved {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
}

// --- Concurrency test ---

func TestPlanController_ConcurrentAccess(t *testing.T) {
	ctrl := NewPlanController(nil)
	plan := draftPlan()
	// Approve to get an active plan for concurrent reads.
	ctrl.Approve(ApproveRequest{Plan: plan, Actor: "op", Now: 100})

	done := make(chan struct{})
	// Writers: approve steps concurrently.
	for i := 0; i < 10; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			ctrl.ApproveStep(plan, "s1", fmt.Sprintf("actor-%d", n), "", int64(200+n))
		}(i)
	}
	// Readers: preview and check MayCompile concurrently.
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			ctrl.Preview(plan, state.AutonomyPlanApproval)
			ctrl.MayCompile(plan, state.AutonomyPlanApproval)
			ctrl.Approvals()
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	// If we get here without a race detector complaint, concurrency is safe.
	approvals := ctrl.Approvals()
	// 1 initial approve + 10 step approvals = 11
	if len(approvals) != 11 {
		t.Errorf("approvals = %d, want 11", len(approvals))
	}
}

// --- PlanApprovalDecision validation ---

func TestPlanApprovalDecision_Valid(t *testing.T) {
	for _, d := range []state.PlanApprovalDecision{
		state.PlanApprovalPending,
		state.PlanApprovalApproved,
		state.PlanApprovalRejected,
		state.PlanApprovalAmended,
	} {
		if !d.Valid() {
			t.Errorf("%q should be valid", d)
		}
	}
}

func TestPlanApprovalDecision_Invalid(t *testing.T) {
	if state.PlanApprovalDecision("bogus").Valid() {
		t.Error("bogus should not be valid")
	}
}
