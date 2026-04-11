package planner

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"metiq/internal/store/state"
)

func taskWithVerification(policy state.VerificationPolicy, checks []state.VerificationCheck) state.TaskSpec {
	return state.TaskSpec{
		Version:      1,
		TaskID:       "task-v1",
		Title:        "Verifiable task",
		Instructions: "Do something verifiable",
		Status:       state.TaskStatusVerifying,
		Verification: state.VerificationSpec{
			Policy: policy,
			Checks: checks,
		},
	}
}

func requiredCheck(id string, status state.VerificationStatus) state.VerificationCheck {
	return state.VerificationCheck{
		CheckID:     id,
		Type:        state.VerificationCheckTest,
		Description: fmt.Sprintf("Check %s", id),
		Required:    true,
		Status:      status,
	}
}

func optionalCheck(id string, status state.VerificationStatus) state.VerificationCheck {
	c := requiredCheck(id, status)
	c.Required = false
	return c
}

// --- VerificationSpec model tests ---

func TestVerificationSpec_Validate_Valid(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "c1", Type: state.VerificationCheckTest, Description: "test passes", Required: true},
		},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}
}

func TestVerificationSpec_Validate_DuplicateCheckID(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "c1", Description: "first"},
			{CheckID: "c1", Description: "duplicate"},
		},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for duplicate check_id")
	}
}

func TestVerificationSpec_Validate_MissingCheckID(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{Description: "no id"},
		},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for missing check_id")
	}
}

func TestVerificationSpec_AllRequiredPassed(t *testing.T) {
	spec := state.VerificationSpec{
		Checks: []state.VerificationCheck{
			requiredCheck("c1", state.VerificationStatusPassed),
			requiredCheck("c2", state.VerificationStatusSkipped),
			optionalCheck("c3", state.VerificationStatusFailed),
		},
	}
	if !spec.AllRequiredPassed() {
		t.Error("all required passed/skipped should return true")
	}
}

func TestVerificationSpec_AllRequiredPassed_WithPending(t *testing.T) {
	spec := state.VerificationSpec{
		Checks: []state.VerificationCheck{
			requiredCheck("c1", state.VerificationStatusPassed),
			requiredCheck("c2", state.VerificationStatusPending),
		},
	}
	if spec.AllRequiredPassed() {
		t.Error("pending required check should prevent AllRequiredPassed")
	}
}

func TestVerificationSpec_AnyRequiredFailed(t *testing.T) {
	spec := state.VerificationSpec{
		Checks: []state.VerificationCheck{
			requiredCheck("c1", state.VerificationStatusPassed),
			requiredCheck("c2", state.VerificationStatusFailed),
		},
	}
	if !spec.AnyRequiredFailed() {
		t.Error("failed required check should return true")
	}
}

func TestVerificationSpec_AnyRequiredFailed_ErrorCounts(t *testing.T) {
	spec := state.VerificationSpec{
		Checks: []state.VerificationCheck{
			requiredCheck("c1", state.VerificationStatusError),
		},
	}
	if !spec.AnyRequiredFailed() {
		t.Error("error status should count as failed for required checks")
	}
}

func TestVerificationSpec_PendingChecks(t *testing.T) {
	spec := state.VerificationSpec{
		Checks: []state.VerificationCheck{
			requiredCheck("c1", state.VerificationStatusPending),
			requiredCheck("c2", state.VerificationStatusPassed),
			requiredCheck("c3", state.VerificationStatusPending),
		},
	}
	pending := spec.PendingChecks()
	if len(pending) != 2 {
		t.Errorf("PendingChecks = %d, want 2", len(pending))
	}
}

func TestVerificationSpec_RequiredChecks(t *testing.T) {
	spec := state.VerificationSpec{
		Checks: []state.VerificationCheck{
			requiredCheck("c1", state.VerificationStatusPending),
			optionalCheck("c2", state.VerificationStatusPending),
		},
	}
	req := spec.RequiredChecks()
	if len(req) != 1 || req[0].CheckID != "c1" {
		t.Errorf("RequiredChecks = %v", req)
	}
}

func TestVerificationSpec_JSONRoundTrip(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "c1", Type: state.VerificationCheckTest, Description: "test", Required: true, Status: state.VerificationStatusPassed},
		},
		VerifiedAt: 5000,
		VerifiedBy: "agent",
	}
	blob, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded state.VerificationSpec
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Policy != state.VerificationPolicyRequired {
		t.Errorf("policy = %q", decoded.Policy)
	}
	if len(decoded.Checks) != 1 || decoded.Checks[0].Status != state.VerificationStatusPassed {
		t.Errorf("checks roundtrip failed: %+v", decoded.Checks)
	}
}

func TestVerificationSpec_Normalize_EmptyPolicyBecomesNone(t *testing.T) {
	spec := state.VerificationSpec{Policy: ""}
	normalized := spec.Normalize()
	if normalized.Policy != state.VerificationPolicyNone {
		t.Errorf("empty policy normalized to %q, want none", normalized.Policy)
	}
}

func TestVerificationSpec_Normalize_EmptyStatusBecomesPending(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "c1", Description: "test", Status: ""},
		},
	}
	normalized := spec.Normalize()
	if normalized.Checks[0].Status != state.VerificationStatusPending {
		t.Errorf("empty status normalized to %q, want pending", normalized.Checks[0].Status)
	}
}

func TestNormalizeVerificationStatus_InvalidReturnsPending(t *testing.T) {
	got := state.NormalizeVerificationStatus("bogus")
	if got != state.VerificationStatusPending {
		t.Errorf("invalid input normalized to %q, want pending", got)
	}
}

func TestVerificationSpec_Validate_RequiredNoChecks(t *testing.T) {
	spec := state.VerificationSpec{Policy: state.VerificationPolicyRequired}
	if err := spec.Validate(); err == nil {
		t.Fatal("required policy with no checks should fail validation")
	}
}

// --- VerificationStatus tests ---

func TestVerificationStatus_ParseValid(t *testing.T) {
	tests := []struct {
		input string
		want  state.VerificationStatus
	}{
		{"pending", state.VerificationStatusPending},
		{"passed", state.VerificationStatusPassed},
		{"failed", state.VerificationStatusFailed},
		{"skipped", state.VerificationStatusSkipped},
		{"error", state.VerificationStatusError},
		{"running", state.VerificationStatusRunning},
		{"", state.VerificationStatusPending},
	}
	for _, tt := range tests {
		got, ok := state.ParseVerificationStatus(tt.input)
		if !ok {
			t.Errorf("ParseVerificationStatus(%q) !ok", tt.input)
		}
		if got != tt.want {
			t.Errorf("ParseVerificationStatus(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestVerificationStatus_IsTerminal(t *testing.T) {
	terminal := []state.VerificationStatus{
		state.VerificationStatusPassed,
		state.VerificationStatusFailed,
		state.VerificationStatusSkipped,
		state.VerificationStatusError,
	}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%q should be terminal", s)
		}
	}
	nonTerminal := []state.VerificationStatus{
		state.VerificationStatusPending,
		state.VerificationStatusRunning,
	}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("%q should not be terminal", s)
		}
	}
}

// --- VerificationPolicy tests ---

func TestVerificationPolicy_ParseValid(t *testing.T) {
	for _, p := range []string{"required", "advisory", "none", ""} {
		_, ok := state.ParseVerificationPolicy(p)
		if !ok {
			t.Errorf("ParseVerificationPolicy(%q) !ok", p)
		}
	}
}

func TestVerificationPolicy_ParseInvalid(t *testing.T) {
	_, ok := state.ParseVerificationPolicy("bogus")
	if ok {
		t.Error("bogus should not parse")
	}
}

// --- Verifier.Evaluate tests ---

func TestVerifier_Evaluate_NoVerification(t *testing.T) {
	v := NewVerifier(nil)
	task := taskWithVerification(state.VerificationPolicyNone, nil)
	result := v.Evaluate(task, "agent", 1000)
	if !result.Passed {
		t.Error("task with no verification should pass")
	}
}

func TestVerifier_Evaluate_AllChecksPassed(t *testing.T) {
	evaluator := func(check state.VerificationCheck, task state.TaskSpec) (bool, string, string, error) {
		return true, "ok", "evidence-data", nil
	}
	v := NewVerifier(evaluator)
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPending),
		requiredCheck("c2", state.VerificationStatusPending),
	})
	result := v.Evaluate(task, "agent", 2000)
	if !result.Passed {
		t.Errorf("all checks passed but result.Passed = false: %s", result.Summary)
	}
	if len(result.Blocking) != 0 {
		t.Errorf("Blocking = %v", result.Blocking)
	}
	// Check that statuses were updated.
	for _, c := range result.UpdatedSpec.Checks {
		if c.Status != state.VerificationStatusPassed {
			t.Errorf("check %s status = %q, want passed", c.CheckID, c.Status)
		}
		if c.EvaluatedBy != "agent" {
			t.Errorf("check %s evaluatedBy = %q", c.CheckID, c.EvaluatedBy)
		}
	}
}

func TestVerifier_Evaluate_RequiredCheckFailed(t *testing.T) {
	evaluator := func(check state.VerificationCheck, task state.TaskSpec) (bool, string, string, error) {
		if check.CheckID == "c2" {
			return false, "assertion failed", "", nil
		}
		return true, "ok", "", nil
	}
	v := NewVerifier(evaluator)
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPending),
		requiredCheck("c2", state.VerificationStatusPending),
	})
	result := v.Evaluate(task, "agent", 3000)
	if result.Passed {
		t.Error("should not pass with failed required check")
	}
	if len(result.Blocking) != 1 || result.Blocking[0] != "c2" {
		t.Errorf("Blocking = %v, want [c2]", result.Blocking)
	}
}

func TestVerifier_Evaluate_OptionalFailureDoesntBlock(t *testing.T) {
	evaluator := func(check state.VerificationCheck, task state.TaskSpec) (bool, string, string, error) {
		return false, "failed", "", nil
	}
	v := NewVerifier(evaluator)
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		optionalCheck("c1", state.VerificationStatusPending),
	})
	result := v.Evaluate(task, "agent", 4000)
	if !result.Passed {
		t.Error("optional failure should not block completion")
	}
}

func TestVerifier_Evaluate_EvaluatorError(t *testing.T) {
	evaluator := func(check state.VerificationCheck, task state.TaskSpec) (bool, string, string, error) {
		return false, "", "", fmt.Errorf("evaluator crashed")
	}
	v := NewVerifier(evaluator)
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPending),
	})
	result := v.Evaluate(task, "agent", 5000)
	if result.Passed {
		t.Error("evaluator error on required check should block")
	}
	if result.UpdatedSpec.Checks[0].Status != state.VerificationStatusError {
		t.Errorf("status = %q, want error", result.UpdatedSpec.Checks[0].Status)
	}
}

func TestVerifier_Evaluate_SetsVerifiedAt(t *testing.T) {
	evaluator := func(check state.VerificationCheck, task state.TaskSpec) (bool, string, string, error) {
		return true, "ok", "", nil
	}
	v := NewVerifier(evaluator)
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPending),
	})
	result := v.Evaluate(task, "verifier-agent", 9999)
	if !result.Passed {
		t.Fatal("should pass")
	}
	if result.UpdatedSpec.VerifiedAt != 9999 {
		t.Errorf("VerifiedAt = %d, want 9999", result.UpdatedSpec.VerifiedAt)
	}
	if result.UpdatedSpec.VerifiedBy != "verifier-agent" {
		t.Errorf("VerifiedBy = %q", result.UpdatedSpec.VerifiedBy)
	}
}

func TestVerifier_Evaluate_SkipsTerminalChecks(t *testing.T) {
	called := false
	evaluator := func(check state.VerificationCheck, task state.TaskSpec) (bool, string, string, error) {
		called = true
		return true, "ok", "", nil
	}
	v := NewVerifier(evaluator)
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPassed), // already terminal
	})
	result := v.Evaluate(task, "agent", 6000)
	if called {
		t.Error("evaluator should not be called for terminal checks")
	}
	if !result.Passed {
		t.Error("already-passed check should allow completion")
	}
}

func TestVerifier_Evaluate_NilEvaluator(t *testing.T) {
	v := NewVerifier(nil)
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPending),
	})
	result := v.Evaluate(task, "agent", 7000)
	// Without evaluator, pending required checks block.
	if result.Passed {
		t.Error("nil evaluator with pending required checks should block")
	}
}

func TestVerifier_Evaluate_Advisory(t *testing.T) {
	evaluator := func(check state.VerificationCheck, task state.TaskSpec) (bool, string, string, error) {
		return false, "failed", "", nil
	}
	v := NewVerifier(evaluator)
	task := taskWithVerification(state.VerificationPolicyAdvisory, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPending),
	})
	result := v.Evaluate(task, "agent", 8000)
	if !result.Passed {
		t.Error("advisory policy should never block completion")
	}
}

// --- MayComplete tests ---

func TestVerifier_MayComplete_PassedChecks(t *testing.T) {
	v := NewVerifier(nil)
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPassed),
	})
	result := v.MayComplete(task)
	if !result.Passed {
		t.Error("MayComplete should pass with all required checks passed")
	}
}

func TestVerifier_MayComplete_PendingChecks(t *testing.T) {
	v := NewVerifier(nil)
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPending),
	})
	result := v.MayComplete(task)
	if result.Passed {
		t.Error("MayComplete should block with pending required checks")
	}
}

// --- RecordCheckResult tests ---

func TestRecordCheckResult_Pass(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			requiredCheck("c1", state.VerificationStatusPending),
		},
	}
	updated, err := RecordCheckResult(spec, "c1", true, "looks good", "screenshot.png", "reviewer", 9000)
	if err != nil {
		t.Fatalf("RecordCheckResult: %v", err)
	}
	if updated.Checks[0].Status != state.VerificationStatusPassed {
		t.Errorf("status = %q, want passed", updated.Checks[0].Status)
	}
	if updated.Checks[0].EvaluatedBy != "reviewer" {
		t.Errorf("evaluatedBy = %q", updated.Checks[0].EvaluatedBy)
	}
}

func TestRecordCheckResult_Fail(t *testing.T) {
	spec := state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			requiredCheck("c1", state.VerificationStatusPending),
		},
	}
	updated, err := RecordCheckResult(spec, "c1", false, "assertion failed", "", "reviewer", 9000)
	if err != nil {
		t.Fatalf("RecordCheckResult: %v", err)
	}
	if updated.Checks[0].Status != state.VerificationStatusFailed {
		t.Errorf("status = %q, want failed", updated.Checks[0].Status)
	}
}

func TestRecordCheckResult_UnknownCheckID(t *testing.T) {
	spec := state.VerificationSpec{
		Checks: []state.VerificationCheck{
			requiredCheck("c1", state.VerificationStatusPending),
		},
	}
	_, err := RecordCheckResult(spec, "nonexistent", true, "", "", "", 0)
	if err == nil {
		t.Fatal("expected error for unknown check_id")
	}
}

func TestRecordCheckResult_EmptyCheckID(t *testing.T) {
	_, err := RecordCheckResult(state.VerificationSpec{}, "", true, "", "", "", 0)
	if err == nil {
		t.Fatal("expected error for empty check_id")
	}
}

// --- BuildFromAcceptanceCriteria tests ---

func TestBuildFromAcceptanceCriteria_Empty(t *testing.T) {
	spec := BuildFromAcceptanceCriteria(nil, state.VerificationPolicyRequired)
	if spec.Policy != state.VerificationPolicyNone {
		t.Errorf("empty criteria should produce policy=none, got %q", spec.Policy)
	}
}

func TestBuildFromAcceptanceCriteria_WithCriteria(t *testing.T) {
	criteria := []state.TaskAcceptanceCriterion{
		{Type: "test", Description: "unit tests pass", Required: true},
		{Description: "code reviewed", Required: false},
	}
	spec := BuildFromAcceptanceCriteria(criteria, state.VerificationPolicyRequired)
	if spec.Policy != state.VerificationPolicyRequired {
		t.Errorf("policy = %q", spec.Policy)
	}
	if len(spec.Checks) != 2 {
		t.Fatalf("checks = %d, want 2", len(spec.Checks))
	}
	if spec.Checks[0].Type != state.VerificationCheckTest {
		t.Errorf("check[0].type = %q, want test", spec.Checks[0].Type)
	}
	if spec.Checks[1].Type != state.VerificationCheckReview {
		t.Errorf("check[1].type = %q, want review (default)", spec.Checks[1].Type)
	}
	if !spec.Checks[0].Required {
		t.Error("check[0] should be required")
	}
	if spec.Checks[1].Required {
		t.Error("check[1] should not be required")
	}
}

// --- ValidateCompletionGate tests ---

func TestValidateCompletionGate_NoPolicy(t *testing.T) {
	task := taskWithVerification(state.VerificationPolicyNone, nil)
	if err := ValidateCompletionGate(task); err != nil {
		t.Fatalf("no-policy: %v", err)
	}
}

func TestValidateCompletionGate_Advisory(t *testing.T) {
	task := taskWithVerification(state.VerificationPolicyAdvisory, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusFailed),
	})
	if err := ValidateCompletionGate(task); err != nil {
		t.Fatalf("advisory with failed check should not block: %v", err)
	}
}

func TestValidateCompletionGate_Required_AllPassed(t *testing.T) {
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPassed),
		requiredCheck("c2", state.VerificationStatusSkipped),
	})
	if err := ValidateCompletionGate(task); err != nil {
		t.Fatalf("all passed/skipped: %v", err)
	}
}

func TestValidateCompletionGate_Required_PendingBlocks(t *testing.T) {
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPassed),
		requiredCheck("c2", state.VerificationStatusPending),
	})
	err := ValidateCompletionGate(task)
	if err == nil {
		t.Fatal("pending required check should block")
	}
	if !contains(err.Error(), "c2") {
		t.Errorf("error should mention blocking check: %v", err)
	}
}

func TestValidateCompletionGate_Required_FailedBlocks(t *testing.T) {
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusFailed),
	})
	err := ValidateCompletionGate(task)
	if err == nil {
		t.Fatal("failed required check should block")
	}
}

func TestValidateCompletionGate_Required_NoChecks(t *testing.T) {
	task := taskWithVerification(state.VerificationPolicyRequired, nil)
	err := ValidateCompletionGate(task)
	if err == nil {
		t.Fatal("required policy with no checks should error")
	}
}

func TestValidateCompletionGate_Required_OptionalFailureAllowed(t *testing.T) {
	task := taskWithVerification(state.VerificationPolicyRequired, []state.VerificationCheck{
		requiredCheck("c1", state.VerificationStatusPassed),
		optionalCheck("c2", state.VerificationStatusFailed),
	})
	if err := ValidateCompletionGate(task); err != nil {
		t.Fatalf("optional failure should not block: %v", err)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
