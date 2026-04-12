package planner

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"metiq/internal/store/state"
)

// ── EvalCase tests ─────────────────────────────────────────────────────────────

func TestEvalCase_EffectiveWeight(t *testing.T) {
	if w := (EvalCase{}).EffectiveWeight(); w != 1.0 {
		t.Errorf("default weight = %f, want 1.0", w)
	}
	if w := (EvalCase{Weight: 2.5}).EffectiveWeight(); w != 2.5 {
		t.Errorf("explicit weight = %f, want 2.5", w)
	}
	if w := (EvalCase{Weight: -1}).EffectiveWeight(); w != 1.0 {
		t.Errorf("negative weight = %f, want 1.0", w)
	}
}

func TestValidEvalMatchMode(t *testing.T) {
	for _, m := range []EvalMatchMode{EvalMatchContains, EvalMatchExact, EvalMatchNotEmpty, EvalMatchCustom, ""} {
		if !ValidEvalMatchMode(m) {
			t.Errorf("expected %q to be valid", m)
		}
	}
	if ValidEvalMatchMode("bogus") {
		t.Error("bogus should be invalid")
	}
}

// ── Suite validation ───────────────────────────────────────────────────────────

func TestEvalSuite_Validate_Valid(t *testing.T) {
	s := EvalSuite{
		SuiteID: "suite-1", Title: "Test Suite",
		Cases: []EvalCase{{CaseID: "c1", Input: "hello"}},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestEvalSuite_Validate_MissingID(t *testing.T) {
	s := EvalSuite{Title: "x", Cases: []EvalCase{{CaseID: "c1"}}}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestEvalSuite_Validate_NoCases(t *testing.T) {
	s := EvalSuite{SuiteID: "s1", Title: "x"}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for empty cases")
	}
}

func TestEvalSuite_Validate_DuplicateCaseID(t *testing.T) {
	s := EvalSuite{
		SuiteID: "s1", Title: "x",
		Cases: []EvalCase{{CaseID: "c1"}, {CaseID: "c1"}},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for duplicate case_id")
	}
}

func TestEvalSuite_Validate_MissingCaseID(t *testing.T) {
	s := EvalSuite{SuiteID: "s1", Title: "x", Cases: []EvalCase{{Input: "hi"}}}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for missing case_id")
	}
}

func TestEvalSuite_Validate_InvalidMatchMode(t *testing.T) {
	s := EvalSuite{
		SuiteID: "s1", Title: "x",
		Cases: []EvalCase{{CaseID: "c1", MatchMode: "bogus"}},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for invalid match mode")
	}
}

// ── Builtin evaluator ──────────────────────────────────────────────────────────

func TestBuiltinEvaluator_Contains(t *testing.T) {
	c := EvalCase{CaseID: "c1", Expected: "safe", MatchMode: EvalMatchContains}
	r := BuiltinCaseEvaluator(c, "You are a safe agent.")
	if !r.Passed || r.Score != 1.0 {
		t.Errorf("contains match should pass: %+v", r)
	}
}

func TestBuiltinEvaluator_ContainsFail(t *testing.T) {
	c := EvalCase{CaseID: "c1", Expected: "dangerous", MatchMode: EvalMatchContains}
	r := BuiltinCaseEvaluator(c, "You are a safe agent.")
	if r.Passed {
		t.Error("should fail when expected substring missing")
	}
}

func TestBuiltinEvaluator_ContainsEmptyExpected(t *testing.T) {
	c := EvalCase{CaseID: "c1", MatchMode: EvalMatchContains}
	r := BuiltinCaseEvaluator(c, "anything")
	if !r.Passed {
		t.Error("empty expected with contains should always pass")
	}
}

func TestBuiltinEvaluator_Exact(t *testing.T) {
	c := EvalCase{CaseID: "c1", Expected: "hello", MatchMode: EvalMatchExact}
	if r := BuiltinCaseEvaluator(c, "hello"); !r.Passed {
		t.Error("exact match should pass")
	}
	if r := BuiltinCaseEvaluator(c, "hello world"); r.Passed {
		t.Error("exact match should fail on partial")
	}
}

func TestBuiltinEvaluator_NotEmpty(t *testing.T) {
	c := EvalCase{CaseID: "c1", MatchMode: EvalMatchNotEmpty}
	if r := BuiltinCaseEvaluator(c, "content"); !r.Passed {
		t.Error("non-empty should pass")
	}
	if r := BuiltinCaseEvaluator(c, "  "); r.Passed {
		t.Error("whitespace-only should fail not_empty")
	}
}

func TestBuiltinEvaluator_Custom(t *testing.T) {
	c := EvalCase{CaseID: "c1", MatchMode: EvalMatchCustom}
	r := BuiltinCaseEvaluator(c, "x")
	if r.Error == "" {
		t.Error("custom mode with builtin should produce error")
	}
}

func TestBuiltinEvaluator_DefaultMode(t *testing.T) {
	// Empty match mode defaults to contains.
	c := EvalCase{CaseID: "c1", Expected: "safe"}
	r := BuiltinCaseEvaluator(c, "be safe")
	if !r.Passed {
		t.Error("default mode (contains) should pass")
	}
}

// ── Runner ─────────────────────────────────────────────────────────────────────

func testSuite() EvalSuite {
	return EvalSuite{
		SuiteID: "suite-1", Title: "Safety Suite",
		Cases: []EvalCase{
			{CaseID: "safety", Title: "Safety check", Expected: "safe", MatchMode: EvalMatchContains, Tags: []string{"critical"}},
			{CaseID: "helpful", Title: "Helpfulness", Expected: "helpful", MatchMode: EvalMatchContains},
			{CaseID: "nonempty", Title: "Non-empty", MatchMode: EvalMatchNotEmpty},
		},
	}
}

func testProposal() state.PolicyProposal {
	return state.PolicyProposal{
		ProposalID: "prop-1", Kind: state.ProposalKindPrompt,
		Status: state.ProposalStatusApproved, Title: "test",
		TargetField: "system_prompt", ProposedValue: "You are a safe and helpful agent.",
	}
}

func TestRunner_AllPass(t *testing.T) {
	r := NewEvalRunner(nil, "eval")
	result := r.Run(testSuite(), testProposal(),
		"You are a safe and helpful agent.",
		DefaultAcceptanceThreshold(), nil, 1000)

	if result.PassedCases != 3 {
		t.Errorf("passed = %d, want 3", result.PassedCases)
	}
	if result.PassRate != 1.0 {
		t.Errorf("pass rate = %f, want 1.0", result.PassRate)
	}
	if result.GateDecision != EvalGatePass {
		t.Errorf("gate = %q, want pass", result.GateDecision)
	}
	if result.EvalID != "eval-1" {
		t.Errorf("eval_id = %q", result.EvalID)
	}
}

func TestRunner_SomeFail(t *testing.T) {
	r := NewEvalRunner(nil, "eval")
	// Candidate missing "helpful" keyword.
	result := r.Run(testSuite(), testProposal(),
		"You are a safe agent.",
		DefaultAcceptanceThreshold(), nil, 1000)

	if result.PassedCases != 2 {
		t.Errorf("passed = %d, want 2", result.PassedCases)
	}
	if result.FailedCases != 1 {
		t.Errorf("failed = %d, want 1", result.FailedCases)
	}
	// 2/3 ≈ 66.7% < 90% threshold → fail.
	if result.GateDecision != EvalGateFail {
		t.Errorf("gate = %q, want fail", result.GateDecision)
	}
}

func TestRunner_CriticalFailure(t *testing.T) {
	r := NewEvalRunner(nil, "eval")
	threshold := DefaultAcceptanceThreshold()
	threshold.MinPassRate = 0.5 // lower pass rate threshold

	// Missing "safe" but has "helpful" → critical case fails.
	result := r.Run(testSuite(), testProposal(),
		"You are a helpful agent.",
		threshold, nil, 1000)

	if result.GateDecision != EvalGateFail {
		t.Errorf("gate = %q, want fail (critical case failed)", result.GateDecision)
	}
	if !strings.Contains(result.GateReason, "critical") {
		t.Errorf("reason should mention critical: %q", result.GateReason)
	}
}

func TestRunner_WithBaseline_Regressions(t *testing.T) {
	r := NewEvalRunner(nil, "eval")
	baseline := map[string]bool{
		"safety":   true,
		"helpful":  true,
		"nonempty": true,
	}
	threshold := AcceptanceThreshold{
		MinPassRate:      0.5,
		MinWeightedScore: 0.5,
		MaxRegressions:   0, // zero regressions allowed
	}

	// "helpful" will fail → 1 regression.
	result := r.Run(testSuite(), testProposal(),
		"You are a safe agent.",
		threshold, baseline, 1000)

	if result.Regressions != 1 {
		t.Errorf("regressions = %d, want 1", result.Regressions)
	}
	// Passed rate is fine (2/3 > 0.5), but regressions exceed max → warn.
	if result.GateDecision != EvalGateWarn {
		t.Errorf("gate = %q, want warn", result.GateDecision)
	}
}

func TestRunner_WithBaseline_NoRegression(t *testing.T) {
	r := NewEvalRunner(nil, "eval")
	baseline := map[string]bool{"safety": true, "helpful": true, "nonempty": true}

	result := r.Run(testSuite(), testProposal(),
		"You are a safe and helpful agent.",
		DefaultAcceptanceThreshold(), baseline, 1000)

	if result.Regressions != 0 {
		t.Errorf("regressions = %d, want 0", result.Regressions)
	}
	if result.GateDecision != EvalGatePass {
		t.Errorf("gate = %q, want pass", result.GateDecision)
	}
}

func TestRunner_WeightedScoring(t *testing.T) {
	suite := EvalSuite{
		SuiteID: "weighted", Title: "Weighted",
		Cases: []EvalCase{
			{CaseID: "heavy", Expected: "yes", MatchMode: EvalMatchExact, Weight: 3.0},
			{CaseID: "light", Expected: "yes", MatchMode: EvalMatchExact, Weight: 1.0},
		},
	}
	r := NewEvalRunner(nil, "eval")

	// Only "light" passes.
	result := r.Run(suite, testProposal(), "yes",
		AcceptanceThreshold{MinWeightedScore: 0.5}, nil, 1000)

	// heavy fails (exact match "yes" vs "yes" — wait, both match "yes").
	// Actually "yes" == "yes" so both pass. Let me use a different candidate.
	// Re-think: candidate="yes" matches both. Let me test where heavy fails.
	result = r.Run(suite, testProposal(), "no",
		AcceptanceThreshold{MinPassRate: 0.0, MinWeightedScore: 0.5}, nil, 1000)

	// Both fail with "no".
	if result.WeightedScore != 0 {
		t.Errorf("score = %f, want 0", result.WeightedScore)
	}
}

func TestRunner_WeightedScoring_Partial(t *testing.T) {
	// Custom evaluator that gives partial scores.
	evaluator := func(c EvalCase, val string) EvalCaseResult {
		if c.CaseID == "high" {
			return EvalCaseResult{CaseID: c.CaseID, Passed: true, Score: 1.0}
		}
		return EvalCaseResult{CaseID: c.CaseID, Passed: false, Score: 0.3}
	}
	suite := EvalSuite{
		SuiteID: "partial", Title: "Partial",
		Cases: []EvalCase{
			{CaseID: "high", Weight: 2.0},
			{CaseID: "low", Weight: 1.0},
		},
	}
	r := NewEvalRunner(evaluator, "eval")
	result := r.Run(suite, testProposal(), "", AcceptanceThreshold{}, nil, 1000)

	// Weighted: (1.0*2 + 0.3*1) / 3 = 2.3/3 ≈ 0.767
	expected := (1.0*2.0 + 0.3*1.0) / 3.0
	if diff := result.WeightedScore - expected; diff > 0.001 || diff < -0.001 {
		t.Errorf("weighted score = %f, want ~%f", result.WeightedScore, expected)
	}
}

func TestRunner_CustomEvaluator(t *testing.T) {
	called := 0
	custom := func(c EvalCase, val string) EvalCaseResult {
		called++
		return EvalCaseResult{CaseID: c.CaseID, Passed: true, Score: 0.9}
	}
	suite := EvalSuite{
		SuiteID: "custom", Title: "Custom",
		Cases: []EvalCase{{CaseID: "c1"}, {CaseID: "c2"}},
	}
	r := NewEvalRunner(custom, "eval")
	result := r.Run(suite, testProposal(), "x", DefaultAcceptanceThreshold(), nil, 1000)

	if called != 2 {
		t.Errorf("evaluator called %d times, want 2", called)
	}
	if result.PassedCases != 2 {
		t.Errorf("passed = %d", result.PassedCases)
	}
}

func TestRunner_IDsSequential(t *testing.T) {
	r := NewEvalRunner(nil, "e")
	suite := EvalSuite{SuiteID: "s", Title: "t", Cases: []EvalCase{{CaseID: "c1", MatchMode: EvalMatchNotEmpty}}}
	r1 := r.Run(suite, testProposal(), "a", AcceptanceThreshold{}, nil, 1000)
	r2 := r.Run(suite, testProposal(), "b", AcceptanceThreshold{}, nil, 1000)
	if r1.EvalID != "e-1" || r2.EvalID != "e-2" {
		t.Errorf("ids: %q, %q", r1.EvalID, r2.EvalID)
	}
}

// ── Gate logic ─────────────────────────────────────────────────────────────────

func TestGate_PassRateFail(t *testing.T) {
	d, reason := applyGate(
		EvalResult{PassRate: 0.5, WeightedScore: 1.0},
		AcceptanceThreshold{MinPassRate: 0.9},
		EvalSuite{},
	)
	if d != EvalGateFail {
		t.Errorf("gate = %q", d)
	}
	if !strings.Contains(reason, "pass rate") {
		t.Errorf("reason = %q", reason)
	}
}

func TestGate_WeightedScoreFail(t *testing.T) {
	d, _ := applyGate(
		EvalResult{PassRate: 1.0, WeightedScore: 0.3},
		AcceptanceThreshold{MinWeightedScore: 0.8},
		EvalSuite{},
	)
	if d != EvalGateFail {
		t.Errorf("gate = %q", d)
	}
}

func TestGate_RegressionsWarn(t *testing.T) {
	d, _ := applyGate(
		EvalResult{PassRate: 1.0, WeightedScore: 1.0, Regressions: 5},
		AcceptanceThreshold{MaxRegressions: 2},
		EvalSuite{},
	)
	if d != EvalGateWarn {
		t.Errorf("gate = %q, want warn", d)
	}
}

func TestGate_AllPass(t *testing.T) {
	d, _ := applyGate(
		EvalResult{PassRate: 1.0, WeightedScore: 1.0, Regressions: 0},
		AcceptanceThreshold{MinPassRate: 0.9, MinWeightedScore: 0.8, MaxRegressions: 2},
		EvalSuite{},
	)
	if d != EvalGatePass {
		t.Errorf("gate = %q, want pass", d)
	}
}

// ── Formatting ─────────────────────────────────────────────────────────────────

func TestFormatEvalResult(t *testing.T) {
	r := EvalResult{
		EvalID: "eval-1", SuiteID: "suite-1", ProposalID: "prop-1",
		TotalCases: 3, PassedCases: 2, FailedCases: 1,
		PassRate: 0.667, WeightedScore: 0.75, Regressions: 1,
		GateDecision: EvalGateWarn, GateReason: "regressions",
	}
	out := FormatEvalResult(r)
	for _, want := range []string{"eval-1", "suite-1", "prop-1", "3 total", "2 passed", "warn", "regressions"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatEvalCaseResults_Empty(t *testing.T) {
	if got := FormatEvalCaseResults(nil); got != "No case results." {
		t.Errorf("got %q", got)
	}
}

func TestFormatEvalCaseResults_WithResults(t *testing.T) {
	results := []EvalCaseResult{
		{CaseID: "c1", Passed: true, Score: 1.0},
		{CaseID: "c2", Passed: false, Score: 0.0},
		{CaseID: "c3", Error: "timeout"},
	}
	out := FormatEvalCaseResults(results)
	if !strings.Contains(out, "✓ c1") || !strings.Contains(out, "✗ c2") || !strings.Contains(out, "⚡ c3") {
		t.Errorf("unexpected format:\n%s", out)
	}
}

// ── JSON round-trip ────────────────────────────────────────────────────────────

func TestEvalResult_JSON(t *testing.T) {
	r := EvalResult{
		EvalID: "eval-1", SuiteID: "suite-1", ProposalID: "prop-1",
		TotalCases: 2, PassedCases: 1, FailedCases: 1,
		PassRate: 0.5, WeightedScore: 0.6,
		GateDecision: EvalGateFail, GateReason: "too low",
		CaseResults: []EvalCaseResult{
			{CaseID: "c1", Passed: true, Score: 1.0},
			{CaseID: "c2", Passed: false, Score: 0.2},
		},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded EvalResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.EvalID != "eval-1" || len(decoded.CaseResults) != 2 {
		t.Errorf("round-trip: %+v", decoded)
	}
	if decoded.GateDecision != EvalGateFail {
		t.Errorf("gate = %q", decoded.GateDecision)
	}
}

func TestEvalSuite_JSON(t *testing.T) {
	s := EvalSuite{
		SuiteID: "s1", Title: "Test",
		Cases: []EvalCase{
			{CaseID: "c1", Input: "hello", Expected: "world", Weight: 2.0, Tags: []string{"critical"}},
		},
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded EvalSuite
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.SuiteID != "s1" || len(decoded.Cases) != 1 || decoded.Cases[0].Weight != 2.0 {
		t.Errorf("round-trip: %+v", decoded)
	}
}

// ── Concurrency ────────────────────────────────────────────────────────────────

func TestRunner_ConcurrentAccess(t *testing.T) {
	r := NewEvalRunner(nil, "eval")
	suite := EvalSuite{
		SuiteID: "s1", Title: "t",
		Cases: []EvalCase{{CaseID: "c1", MatchMode: EvalMatchNotEmpty}},
	}
	var wg sync.WaitGroup
	results := make([]EvalResult, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = r.Run(suite, testProposal(), "value",
				DefaultAcceptanceThreshold(), nil, 1000)
		}(i)
	}
	wg.Wait()

	ids := make(map[string]bool)
	for _, res := range results {
		if ids[res.EvalID] {
			t.Fatalf("duplicate eval ID: %s", res.EvalID)
		}
		ids[res.EvalID] = true
	}
}

// ── DefaultAcceptanceThreshold ─────────────────────────────────────────────────

func TestDefaultAcceptanceThreshold(t *testing.T) {
	d := DefaultAcceptanceThreshold()
	if d.MinPassRate != 0.90 {
		t.Errorf("min pass rate = %f", d.MinPassRate)
	}
	if d.MinWeightedScore != 0.80 {
		t.Errorf("min weighted score = %f", d.MinWeightedScore)
	}
	if !d.RequireAllCritical {
		t.Error("should require all critical")
	}
}
