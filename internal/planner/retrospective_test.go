package planner

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"metiq/internal/store/state"
)

// ── Trigger classification ───────────────────────────────────────────────────

func TestDetermineTrigger_Completed(t *testing.T) {
	run := state.TaskRun{Status: state.TaskRunStatusCompleted}
	if got := DetermineTrigger(run); got != state.RetroTriggerRunCompleted {
		t.Fatalf("expected run_completed, got %s", got)
	}
}

func TestDetermineTrigger_CompletedWithVerifyFail(t *testing.T) {
	run := state.TaskRun{
		Status: state.TaskRunStatusCompleted,
		Verification: state.VerificationSpec{
			Checks: []state.VerificationCheck{
				{CheckID: "c1", Required: true, Status: state.VerificationStatusFailed},
			},
		},
	}
	if got := DetermineTrigger(run); got != state.RetroTriggerVerifyFailed {
		t.Fatalf("expected verification_failed, got %s", got)
	}
}

func TestDetermineTrigger_Failed(t *testing.T) {
	run := state.TaskRun{Status: state.TaskRunStatusFailed, Error: "some error"}
	if got := DetermineTrigger(run); got != state.RetroTriggerRunFailed {
		t.Fatalf("expected run_failed, got %s", got)
	}
}

func TestDetermineTrigger_BudgetExhausted(t *testing.T) {
	run := state.TaskRun{Status: state.TaskRunStatusFailed, Error: "budget exhausted: token limit exceeded"}
	if got := DetermineTrigger(run); got != state.RetroTriggerBudgetExhausted {
		t.Fatalf("expected budget_exhausted, got %s", got)
	}
}

func TestDetermineTrigger_FailedVerifyFailed(t *testing.T) {
	run := state.TaskRun{
		Status: state.TaskRunStatusFailed,
		Error:  "check failed",
		Verification: state.VerificationSpec{
			Checks: []state.VerificationCheck{
				{CheckID: "c1", Required: true, Status: state.VerificationStatusFailed},
			},
		},
	}
	// Budget check has higher priority; verify takes precedence over plain failure.
	if got := DetermineTrigger(run); got != state.RetroTriggerVerifyFailed {
		t.Fatalf("expected verification_failed, got %s", got)
	}
}

func TestDetermineTrigger_NonTerminal(t *testing.T) {
	run := state.TaskRun{Status: state.TaskRunStatusRunning}
	if got := DetermineTrigger(run); got != state.RetroTriggerRunFailed {
		t.Fatalf("expected run_failed (fallback), got %s", got)
	}
}

// ── Outcome classification ───────────────────────────────────────────────────

func TestClassifyOutcome_Success(t *testing.T) {
	run := state.TaskRun{Status: state.TaskRunStatusCompleted}
	if got := ClassifyOutcome(run); got != state.RetroOutcomeSuccess {
		t.Fatalf("expected success, got %s", got)
	}
}

func TestClassifyOutcome_Partial(t *testing.T) {
	run := state.TaskRun{
		Status: state.TaskRunStatusCompleted,
		Verification: state.VerificationSpec{
			Checks: []state.VerificationCheck{
				{CheckID: "c1", Required: true, Status: state.VerificationStatusFailed},
			},
		},
	}
	if got := ClassifyOutcome(run); got != state.RetroOutcomePartial {
		t.Fatalf("expected partial, got %s", got)
	}
}

func TestClassifyOutcome_Failure(t *testing.T) {
	run := state.TaskRun{Status: state.TaskRunStatusFailed}
	if got := ClassifyOutcome(run); got != state.RetroOutcomeFailure {
		t.Fatalf("expected failure, got %s", got)
	}
}

// ── ShouldGenerate ───────────────────────────────────────────────────────────

func TestShouldGenerate_DefaultPolicy_FailedRun(t *testing.T) {
	policy := DefaultRetroPolicy()
	run := state.TaskRun{Status: state.TaskRunStatusFailed}
	if !ShouldGenerate(policy, run) {
		t.Fatal("expected true for failed run with default policy")
	}
}

func TestShouldGenerate_DefaultPolicy_CompletedRun(t *testing.T) {
	policy := DefaultRetroPolicy()
	run := state.TaskRun{Status: state.TaskRunStatusCompleted}
	if ShouldGenerate(policy, run) {
		t.Fatal("expected false for completed run with default policy")
	}
}

func TestShouldGenerate_AllPolicy_CompletedRun(t *testing.T) {
	policy := AllRetroPolicy()
	run := state.TaskRun{Status: state.TaskRunStatusCompleted}
	if !ShouldGenerate(policy, run) {
		t.Fatal("expected true for completed run with all policy")
	}
}

func TestShouldGenerate_MinDuration_TooShort(t *testing.T) {
	policy := AllRetroPolicy()
	policy.MinDurationMS = 5000
	run := state.TaskRun{
		Status:    state.TaskRunStatusCompleted,
		StartedAt: 1000,
		EndedAt:   1002, // 2 seconds
	}
	if ShouldGenerate(policy, run) {
		t.Fatal("expected false for run shorter than MinDurationMS")
	}
}

func TestShouldGenerate_MinDuration_LongEnough(t *testing.T) {
	policy := AllRetroPolicy()
	policy.MinDurationMS = 5000
	run := state.TaskRun{
		Status:    state.TaskRunStatusCompleted,
		StartedAt: 1000,
		EndedAt:   1010, // 10 seconds
	}
	if !ShouldGenerate(policy, run) {
		t.Fatal("expected true for run longer than MinDurationMS")
	}
}

func TestShouldGenerate_BudgetExhausted(t *testing.T) {
	policy := DefaultRetroPolicy()
	run := state.TaskRun{
		Status: state.TaskRunStatusFailed,
		Error:  "budget exhausted",
	}
	if !ShouldGenerate(policy, run) {
		t.Fatal("expected true for budget exhaustion with default policy")
	}
}

func TestShouldGenerate_VerifyFailed(t *testing.T) {
	policy := DefaultRetroPolicy()
	run := state.TaskRun{
		Status: state.TaskRunStatusCompleted,
		Verification: state.VerificationSpec{
			Checks: []state.VerificationCheck{
				{CheckID: "c1", Required: true, Status: state.VerificationStatusFailed},
			},
		},
	}
	if !ShouldGenerate(policy, run) {
		t.Fatal("expected true for verification failure with default policy")
	}
}

// ── Engine Generate ──────────────────────────────────────────────────────────

func TestEngine_Generate_Success(t *testing.T) {
	engine := NewRetrospectiveEngine("test")
	run := state.TaskRun{
		RunID:     "r1",
		TaskID:    "t1",
		GoalID:    "g1",
		AgentID:   "agent-1",
		Status:    state.TaskRunStatusCompleted,
		StartedAt: 100,
		EndedAt:   200,
		Usage:     state.TaskUsage{TotalTokens: 5000, WallClockMS: 100000},
	}
	retro, err := engine.Generate(RetroInput{Run: run}, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retro.RetroID == "" {
		t.Fatal("expected non-empty retro ID")
	}
	if retro.RunID != "r1" {
		t.Fatalf("expected run_id=r1, got %s", retro.RunID)
	}
	if retro.TaskID != "t1" {
		t.Fatalf("expected task_id=t1, got %s", retro.TaskID)
	}
	if retro.Trigger != state.RetroTriggerRunCompleted {
		t.Fatalf("expected trigger=run_completed, got %s", retro.Trigger)
	}
	if retro.Outcome != state.RetroOutcomeSuccess {
		t.Fatalf("expected outcome=success, got %s", retro.Outcome)
	}
	if retro.DurationMS != 100000 { // prefers WallClockMS
		t.Fatalf("expected duration_ms=100000, got %d", retro.DurationMS)
	}
}

func TestEngine_Generate_FailedRun(t *testing.T) {
	engine := NewRetrospectiveEngine("test")
	run := state.TaskRun{
		RunID:  "r2",
		TaskID: "t2",
		Status: state.TaskRunStatusFailed,
		Error:  "tool timeout",
	}
	retro, err := engine.Generate(RetroInput{Run: run}, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retro.Outcome != state.RetroOutcomeFailure {
		t.Fatalf("expected outcome=failure, got %s", retro.Outcome)
	}
	if retro.Trigger != state.RetroTriggerRunFailed {
		t.Fatalf("expected trigger=run_failed, got %s", retro.Trigger)
	}
	if len(retro.WhatFailed) == 0 {
		t.Fatal("expected at least one what_failed entry")
	}
}

func TestEngine_Generate_WithFeedbackAndProposals(t *testing.T) {
	engine := NewRetrospectiveEngine("test")
	run := state.TaskRun{
		RunID:  "r3",
		TaskID: "t3",
		Status: state.TaskRunStatusFailed,
		Error:  "assertion failed",
	}
	feedback := []state.FeedbackRecord{
		{FeedbackID: "fb1", Summary: "Output was wrong", Severity: state.FeedbackSeverityError},
		{FeedbackID: "fb2", Summary: "Minor style issue", Severity: state.FeedbackSeverityInfo, Source: state.FeedbackSourceReview, Detail: "Use shorter variable names"},
	}
	proposals := []state.PolicyProposal{
		{ProposalID: "prop1", Title: "Improve error handling"},
	}
	retro, err := engine.Generate(RetroInput{
		Run:       run,
		Feedback:  feedback,
		Proposals: proposals,
	}, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(retro.FeedbackIDs) != 2 {
		t.Fatalf("expected 2 feedback IDs, got %d", len(retro.FeedbackIDs))
	}
	if len(retro.ProposalIDs) != 1 {
		t.Fatalf("expected 1 proposal ID, got %d", len(retro.ProposalIDs))
	}
	// Error-severity feedback should appear in what_failed.
	found := false
	for _, f := range retro.WhatFailed {
		if strings.Contains(f, "Output was wrong") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected error-severity feedback in what_failed")
	}
	// Proposal title should appear in improvements.
	foundImprovement := false
	for _, imp := range retro.Improvements {
		if strings.Contains(imp, "Improve error handling") {
			foundImprovement = true
			break
		}
	}
	if !foundImprovement {
		t.Fatal("expected proposal in improvements")
	}
	// Review feedback detail should also appear.
	foundReview := false
	for _, imp := range retro.Improvements {
		if strings.Contains(imp, "shorter variable names") {
			foundReview = true
			break
		}
	}
	if !foundReview {
		t.Fatal("expected review feedback detail in improvements")
	}
}

func TestEngine_Generate_WithExplicitSections(t *testing.T) {
	engine := NewRetrospectiveEngine("test")
	run := state.TaskRun{
		RunID:  "r4",
		TaskID: "t4",
		Status: state.TaskRunStatusCompleted,
	}
	input := RetroInput{
		Run:          run,
		WhatWorked:   []string{"Custom success note"},
		WhatFailed:   []string{"Custom failure note"},
		Improvements: []string{"Custom improvement"},
	}
	retro, err := engine.Generate(input, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(retro.WhatWorked) != 1 || retro.WhatWorked[0] != "Custom success note" {
		t.Fatal("expected explicit what_worked to be preserved")
	}
	if len(retro.WhatFailed) != 1 || retro.WhatFailed[0] != "Custom failure note" {
		t.Fatal("expected explicit what_failed to be preserved")
	}
	if len(retro.Improvements) != 1 || retro.Improvements[0] != "Custom improvement" {
		t.Fatal("expected explicit improvements to be preserved")
	}
}

func TestEngine_Generate_VerificationChecks(t *testing.T) {
	engine := NewRetrospectiveEngine("test")
	run := state.TaskRun{
		RunID:  "r5",
		TaskID: "t5",
		Status: state.TaskRunStatusCompleted,
		Verification: state.VerificationSpec{
			Checks: []state.VerificationCheck{
				{CheckID: "c1", Required: true, Status: state.VerificationStatusPassed},
				{CheckID: "c2", Required: true, Status: state.VerificationStatusPassed},
				{CheckID: "c3", Required: false, Status: state.VerificationStatusFailed},
			},
		},
	}
	retro, err := engine.Generate(RetroInput{Run: run}, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 passed checks should appear in what_worked.
	foundPassed := false
	for _, w := range retro.WhatWorked {
		if strings.Contains(w, "2 verification") {
			foundPassed = true
		}
	}
	if !foundPassed {
		t.Fatal("expected passed verification checks in what_worked")
	}
	// 1 failed check should appear in what_failed.
	foundFailed := false
	for _, f := range retro.WhatFailed {
		if strings.Contains(f, "1 verification") {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Fatal("expected failed verification check in what_failed")
	}
}

func TestEngine_GenerateValidated_NonTerminal(t *testing.T) {
	engine := NewRetrospectiveEngine("test")
	run := state.TaskRun{
		RunID:  "r6",
		TaskID: "t6",
		Status: state.TaskRunStatusRunning,
	}
	_, err := engine.GenerateValidated(RetroInput{Run: run}, 1000)
	if err == nil {
		t.Fatal("expected error for non-terminal run")
	}
	if !strings.Contains(err.Error(), "not terminal") {
		t.Fatalf("expected 'not terminal' in error, got: %s", err)
	}
}

func TestEngine_GenerateValidated_Terminal(t *testing.T) {
	engine := NewRetrospectiveEngine("test")
	run := state.TaskRun{
		RunID:  "r7",
		TaskID: "t7",
		Status: state.TaskRunStatusCompleted,
	}
	retro, err := engine.GenerateValidated(RetroInput{Run: run}, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retro.RetroID == "" {
		t.Fatal("expected retro ID")
	}
}

// ── ID generation ────────────────────────────────────────────────────────────

func TestEngine_IDsAreUnique(t *testing.T) {
	engine := NewRetrospectiveEngine("test")
	run := state.TaskRun{RunID: "r1", TaskID: "t1", Status: state.TaskRunStatusCompleted}
	ids := make(map[string]bool)
	for i := 0; i < 10; i++ {
		retro, err := engine.Generate(RetroInput{Run: run}, 1000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ids[retro.RetroID] {
			t.Fatalf("duplicate ID: %s", retro.RetroID)
		}
		ids[retro.RetroID] = true
	}
}

// ── Duration ─────────────────────────────────────────────────────────────────

func TestEngine_Generate_DurationFallback(t *testing.T) {
	engine := NewRetrospectiveEngine("test")
	run := state.TaskRun{
		RunID:     "r8",
		TaskID:    "t8",
		Status:    state.TaskRunStatusCompleted,
		StartedAt: 100,
		EndedAt:   110,
		// No WallClockMS — should compute from timestamps.
	}
	retro, err := engine.Generate(RetroInput{Run: run}, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retro.DurationMS != 10000 {
		t.Fatalf("expected duration_ms=10000, got %d", retro.DurationMS)
	}
}

// ── Formatting ───────────────────────────────────────────────────────────────

func TestFormatRetrospective(t *testing.T) {
	retro := state.Retrospective{
		RetroID:      "retro-1",
		RunID:        "r1",
		TaskID:       "t1",
		Trigger:      state.RetroTriggerRunFailed,
		Outcome:      state.RetroOutcomeFailure,
		Summary:      "Run failed",
		WhatWorked:   []string{"Token usage was efficient"},
		WhatFailed:   []string{"Output was incorrect"},
		Improvements: []string{"Add verification checks"},
		FeedbackIDs:  []string{"fb1"},
		ProposalIDs:  []string{"prop1"},
	}
	out := FormatRetrospective(retro)
	if !strings.Contains(out, "retro-1") {
		t.Fatal("expected retro ID in output")
	}
	if !strings.Contains(out, "Token usage was efficient") {
		t.Fatal("expected what_worked in output")
	}
	if !strings.Contains(out, "Output was incorrect") {
		t.Fatal("expected what_failed in output")
	}
	if !strings.Contains(out, "Add verification checks") {
		t.Fatal("expected improvements in output")
	}
	if !strings.Contains(out, "1 record") {
		t.Fatal("expected feedback count in output")
	}
	if !strings.Contains(out, "1 linked") {
		t.Fatal("expected proposal count in output")
	}
}

func TestFormatRetroSummary_Empty(t *testing.T) {
	out := FormatRetroSummary(nil)
	if !strings.Contains(out, "No retrospectives") {
		t.Fatal("expected 'No retrospectives' message")
	}
}

func TestFormatRetroSummary_Multiple(t *testing.T) {
	retros := []state.Retrospective{
		{RetroID: "r1", Outcome: state.RetroOutcomeSuccess, Trigger: state.RetroTriggerRunCompleted},
		{RetroID: "r2", Outcome: state.RetroOutcomeFailure, Trigger: state.RetroTriggerRunFailed},
	}
	out := FormatRetroSummary(retros)
	if !strings.Contains(out, "2 total") {
		t.Fatal("expected count in summary")
	}
	if !strings.Contains(out, "r1") || !strings.Contains(out, "r2") {
		t.Fatal("expected both retro IDs in summary")
	}
}

// ── Policy ───────────────────────────────────────────────────────────────────

func TestDefaultRetroPolicy(t *testing.T) {
	p := DefaultRetroPolicy()
	if p.OnRunCompleted {
		t.Fatal("default should not fire on completion")
	}
	if !p.OnRunFailed {
		t.Fatal("default should fire on failure")
	}
	if !p.OnBudgetExhausted {
		t.Fatal("default should fire on budget exhaustion")
	}
	if !p.OnVerificationFailed {
		t.Fatal("default should fire on verify failure")
	}
}

func TestAllRetroPolicy(t *testing.T) {
	p := AllRetroPolicy()
	if !p.OnRunCompleted || !p.OnRunFailed || !p.OnBudgetExhausted || !p.OnVerificationFailed {
		t.Fatal("all policy should fire on everything")
	}
}

// ── Model validation ─────────────────────────────────────────────────────────

func TestRetrospective_Validate_MissingID(t *testing.T) {
	r := state.Retrospective{
		Summary:   "test",
		Trigger:   state.RetroTriggerRunCompleted,
		Outcome:   state.RetroOutcomeSuccess,
		CreatedAt: 1000,
	}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "retro_id") {
		t.Fatalf("expected retro_id error, got %v", err)
	}
}

func TestRetrospective_Validate_MissingSummary(t *testing.T) {
	r := state.Retrospective{
		RetroID:   "r1",
		Trigger:   state.RetroTriggerRunCompleted,
		Outcome:   state.RetroOutcomeSuccess,
		CreatedAt: 1000,
	}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "summary") {
		t.Fatalf("expected summary error, got %v", err)
	}
}

func TestRetrospective_Validate_InvalidTrigger(t *testing.T) {
	r := state.Retrospective{
		RetroID:   "r1",
		Summary:   "test",
		Trigger:   "bad",
		Outcome:   state.RetroOutcomeSuccess,
		CreatedAt: 1000,
	}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "trigger") {
		t.Fatalf("expected trigger error, got %v", err)
	}
}

func TestRetrospective_Validate_InvalidOutcome(t *testing.T) {
	r := state.Retrospective{
		RetroID:   "r1",
		Summary:   "test",
		Trigger:   state.RetroTriggerRunCompleted,
		Outcome:   "bad",
		CreatedAt: 1000,
	}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "outcome") {
		t.Fatalf("expected outcome error, got %v", err)
	}
}

func TestRetrospective_Validate_OK(t *testing.T) {
	r := state.Retrospective{
		RetroID:   "r1",
		Summary:   "test",
		Trigger:   state.RetroTriggerRunCompleted,
		Outcome:   state.RetroOutcomeSuccess,
		CreatedAt: 1000,
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRetrospective_Normalize(t *testing.T) {
	r := state.Retrospective{
		RetroID:   "  r1  ",
		TaskID:    "  t1  ",
		GoalID:    " g1 ",
		Summary:   "  summary  ",
		CreatedBy: " me ",
	}
	n := r.Normalize()
	if n.RetroID != "r1" {
		t.Fatalf("expected trimmed retro ID, got %q", n.RetroID)
	}
	if n.TaskID != "t1" {
		t.Fatalf("expected trimmed task ID, got %q", n.TaskID)
	}
	if n.GoalID != "g1" {
		t.Fatalf("expected trimmed goal ID, got %q", n.GoalID)
	}
	if n.Summary != "summary" {
		t.Fatalf("expected trimmed summary, got %q", n.Summary)
	}
	if n.Version != 1 {
		t.Fatalf("expected version=1, got %d", n.Version)
	}
}

func TestRetrospective_HasLinkage(t *testing.T) {
	r := state.Retrospective{}
	if r.HasLinkage() {
		t.Fatal("expected no linkage")
	}
	r.RunID = "r1"
	if !r.HasLinkage() {
		t.Fatal("expected linkage with RunID")
	}
}

func TestRetrospective_HasProposals(t *testing.T) {
	r := state.Retrospective{}
	if r.HasProposals() {
		t.Fatal("expected no proposals")
	}
	r.ProposalIDs = []string{"p1"}
	if !r.HasProposals() {
		t.Fatal("expected has proposals")
	}
}

func TestRetrospective_HasFeedback(t *testing.T) {
	r := state.Retrospective{}
	if r.HasFeedback() {
		t.Fatal("expected no feedback")
	}
	r.FeedbackIDs = []string{"f1"}
	if !r.HasFeedback() {
		t.Fatal("expected has feedback")
	}
}

// ── JSON round-trip ──────────────────────────────────────────────────────────

func TestRetrospective_JSONRoundTrip(t *testing.T) {
	original := state.Retrospective{
		Version:      1,
		RetroID:      "retro-1",
		GoalID:       "g1",
		TaskID:       "t1",
		RunID:        "r1",
		AgentID:      "agent-1",
		Trigger:      state.RetroTriggerRunFailed,
		Outcome:      state.RetroOutcomeFailure,
		Summary:      "Run failed due to timeout",
		WhatWorked:   []string{"Fast token usage"},
		WhatFailed:   []string{"Timeout on API call"},
		Improvements: []string{"Add retry logic"},
		FeedbackIDs:  []string{"fb1", "fb2"},
		ProposalIDs:  []string{"prop1"},
		Usage:        state.TaskUsage{TotalTokens: 1000, WallClockMS: 5000},
		DurationMS:   5000,
		CreatedAt:    12345,
		CreatedBy:    "system",
		Meta:         map[string]any{"key": "value"},
	}
	blob, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var decoded state.Retrospective
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.RetroID != original.RetroID {
		t.Fatal("retro_id mismatch")
	}
	if decoded.Trigger != original.Trigger {
		t.Fatal("trigger mismatch")
	}
	if decoded.Outcome != original.Outcome {
		t.Fatal("outcome mismatch")
	}
	if len(decoded.WhatWorked) != 1 {
		t.Fatal("what_worked mismatch")
	}
	if len(decoded.FeedbackIDs) != 2 {
		t.Fatal("feedback_ids mismatch")
	}
	if decoded.Meta["key"] != "value" {
		t.Fatal("meta mismatch")
	}
}

// ── Concurrency ──────────────────────────────────────────────────────────────

func TestEngine_ConcurrentGenerate(t *testing.T) {
	engine := NewRetrospectiveEngine("conc")
	run := state.TaskRun{RunID: "r1", TaskID: "t1", Status: state.TaskRunStatusCompleted}

	var wg sync.WaitGroup
	ids := make(chan string, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			retro, err := engine.Generate(RetroInput{Run: run}, 1000)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			ids <- retro.RetroID
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]bool)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate ID under concurrency: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 50 {
		t.Fatalf("expected 50 unique IDs, got %d", len(seen))
	}
}

// ── Truncate helper ──────────────────────────────────────────────────────────

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("short", 10); got != "short" {
		t.Fatalf("expected 'short', got %q", got)
	}
	long := strings.Repeat("x", 200)
	got := truncateStr(long, 50)
	if len(got) != 50 {
		t.Fatalf("expected length 50, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatal("expected ... suffix")
	}
}

// ── BuildSummary ─────────────────────────────────────────────────────────────

func TestBuildSummary_WithTaskAndError(t *testing.T) {
	run := state.TaskRun{
		RunID:  "r1",
		TaskID: "t1",
		Status: state.TaskRunStatusFailed,
		Error:  "something broke",
	}
	s := buildSummary(run, state.RetroTriggerRunFailed, state.RetroOutcomeFailure)
	if !strings.Contains(s, "r1") {
		t.Fatal("expected run ID in summary")
	}
	if !strings.Contains(s, "t1") {
		t.Fatal("expected task ID in summary")
	}
	if !strings.Contains(s, "something broke") {
		t.Fatal("expected error in summary")
	}
}

func TestBuildSummary_BudgetExhausted(t *testing.T) {
	run := state.TaskRun{RunID: "r1", Status: state.TaskRunStatusFailed}
	s := buildSummary(run, state.RetroTriggerBudgetExhausted, state.RetroOutcomeFailure)
	if !strings.Contains(s, "budget exhausted") {
		t.Fatal("expected budget exhausted in summary")
	}
}

func TestBuildSummary_VerifyFailed(t *testing.T) {
	run := state.TaskRun{RunID: "r1", Status: state.TaskRunStatusCompleted}
	s := buildSummary(run, state.RetroTriggerVerifyFailed, state.RetroOutcomePartial)
	if !strings.Contains(s, "verification failed") {
		t.Fatal("expected verification failed in summary")
	}
}

// ── deriveImprovements ───────────────────────────────────────────────────────

func TestDeriveImprovements_Empty(t *testing.T) {
	improvements := deriveImprovements(nil, nil)
	if len(improvements) != 0 {
		t.Fatalf("expected no improvements, got %d", len(improvements))
	}
}

func TestDeriveImprovements_FromProposals(t *testing.T) {
	proposals := []state.PolicyProposal{
		{ProposalID: "p1", Title: "Fix timeout"},
		{ProposalID: "p2", Title: ""},
	}
	improvements := deriveImprovements(nil, proposals)
	if len(improvements) != 1 {
		t.Fatalf("expected 1 improvement (skip empty title), got %d", len(improvements))
	}
}

func TestDeriveImprovements_FromReviewFeedback(t *testing.T) {
	feedback := []state.FeedbackRecord{
		{FeedbackID: "fb1", Source: state.FeedbackSourceReview, Detail: "Add better logging"},
		{FeedbackID: "fb2", Source: state.FeedbackSourceAgent, Detail: "Not a review"}, // non-review source
	}
	improvements := deriveImprovements(feedback, nil)
	if len(improvements) != 1 {
		t.Fatalf("expected 1 improvement from review, got %d", len(improvements))
	}
	if !strings.Contains(improvements[0], "better logging") {
		t.Fatal("expected review detail in improvement")
	}
}
