package planner

import (
	"encoding/json"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// ── Policy validation ───────────────────────────────────────────────────────

func TestAggregationPolicy_Validate_First(t *testing.T) {
	p := AggregationPolicy{Strategy: AggregateFirst, MinWorkers: 1}
	if err := p.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAggregationPolicy_Validate_BestOfN_TooFew(t *testing.T) {
	p := AggregationPolicy{Strategy: AggregateBestOfN, MinWorkers: 1}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for best_of_n with min_workers=1")
	}
}

func TestAggregationPolicy_Validate_Quorum_MissingThreshold(t *testing.T) {
	p := AggregationPolicy{Strategy: AggregateQuorum, MinWorkers: 3, QuorumThreshold: 0}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for zero quorum threshold")
	}
}

func TestAggregationPolicy_Validate_Quorum_OK(t *testing.T) {
	p := AggregationPolicy{Strategy: AggregateQuorum, MinWorkers: 3, QuorumThreshold: 0.5}
	if err := p.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAggregationPolicy_Validate_ReviewerWorker(t *testing.T) {
	p := AggregationPolicy{Strategy: AggregateReviewerWorker, MinWorkers: 1}
	if err := p.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAggregationPolicy_Validate_Unknown(t *testing.T) {
	p := AggregationPolicy{Strategy: "magic"}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}

func TestValidAggregationStrategy(t *testing.T) {
	for _, s := range []AggregationStrategy{AggregateFirst, AggregateBestOfN, AggregateQuorum, AggregateReviewerWorker} {
		if !ValidAggregationStrategy(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
	if ValidAggregationStrategy("invalid") {
		t.Error("expected 'invalid' to be invalid")
	}
}

// ── First strategy ──────────────────────────────────────────────────────────

func TestAggregate_First_NoResults(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateFirst, MinWorkers: 1}
	out := Aggregate(policy, nil, time.Now().Unix())
	if out.Decision != AggDecisionPending {
		t.Fatalf("expected pending, got %s", out.Decision)
	}
}

func TestAggregate_First_OneResult(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateFirst, MinWorkers: 1}
	results := []WorkerResult{{WorkerID: "w1", Output: "hello", CompletedAt: 100}}
	out := Aggregate(policy, results, 101)
	if out.Decision != AggDecisionSelected {
		t.Fatalf("expected selected, got %s", out.Decision)
	}
	if out.SelectedID != "w1" {
		t.Fatalf("expected w1, got %s", out.SelectedID)
	}
	if out.SelectedIndex != 0 {
		t.Fatalf("expected index 0, got %d", out.SelectedIndex)
	}
}

// ── Best-of-N strategy ──────────────────────────────────────────────────────

func TestAggregate_BestOfN_NotEnough(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateBestOfN, MinWorkers: 3}
	results := []WorkerResult{
		{WorkerID: "w1", Score: 0.8},
	}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionPending {
		t.Fatalf("expected pending, got %s", out.Decision)
	}
}

func TestAggregate_BestOfN_SelectsHighest(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateBestOfN, MinWorkers: 3, ScoringFunc: "quality"}
	results := []WorkerResult{
		{WorkerID: "w1", Score: 0.6, CompletedAt: 100},
		{WorkerID: "w2", Score: 0.9, CompletedAt: 101},
		{WorkerID: "w3", Score: 0.7, CompletedAt: 102},
	}
	out := Aggregate(policy, results, 103)
	if out.Decision != AggDecisionSelected {
		t.Fatalf("expected selected, got %s", out.Decision)
	}
	if out.SelectedID != "w2" {
		t.Fatalf("expected w2, got %s", out.SelectedID)
	}
}

func TestAggregate_BestOfN_SpeedScoring(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateBestOfN, MinWorkers: 2, ScoringFunc: "speed"}
	results := []WorkerResult{
		{WorkerID: "slow", Score: 0.9, CompletedAt: 200},
		{WorkerID: "fast", Score: 0.5, CompletedAt: 100},
	}
	out := Aggregate(policy, results, 201)
	if out.Decision != AggDecisionSelected {
		t.Fatalf("expected selected, got %s", out.Decision)
	}
	if out.SelectedID != "fast" {
		t.Fatalf("expected fast, got %s", out.SelectedID)
	}
}

func TestAggregate_BestOfN_TokenEfficiency(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateBestOfN, MinWorkers: 2, ScoringFunc: "token_efficiency"}
	results := []WorkerResult{
		{WorkerID: "heavy", Usage: state.TaskUsage{TotalTokens: 5000}, CompletedAt: 100},
		{WorkerID: "light", Usage: state.TaskUsage{TotalTokens: 1000}, CompletedAt: 101},
	}
	out := Aggregate(policy, results, 102)
	if out.SelectedID != "light" {
		t.Fatalf("expected light, got %s", out.SelectedID)
	}
}

func TestAggregate_BestOfN_AllowPartial(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateBestOfN, MinWorkers: 5, AllowPartial: true}
	results := []WorkerResult{
		{WorkerID: "w1", Score: 0.3},
		{WorkerID: "w2", Score: 0.8},
	}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionSelected {
		t.Fatalf("expected selected with partial, got %s", out.Decision)
	}
	if out.SelectedID != "w2" {
		t.Fatalf("expected w2, got %s", out.SelectedID)
	}
}

func TestAggregate_BestOfN_AllowPartial_NoResults(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateBestOfN, MinWorkers: 5, AllowPartial: true}
	out := Aggregate(policy, nil, 100)
	if out.Decision != AggDecisionPending {
		t.Fatalf("expected pending even with allow_partial when no results, got %s", out.Decision)
	}
}

// ── Quorum strategy ─────────────────────────────────────────────────────────

func TestAggregate_Quorum_NotEnough(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateQuorum, MinWorkers: 3, QuorumThreshold: 0.5}
	results := []WorkerResult{{WorkerID: "w1", OutputHash: "abc"}}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionPending {
		t.Fatalf("expected pending, got %s", out.Decision)
	}
}

func TestAggregate_Quorum_Reached(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateQuorum, MinWorkers: 3, QuorumThreshold: 0.5}
	results := []WorkerResult{
		{WorkerID: "w1", OutputHash: "abc", CompletedAt: 100},
		{WorkerID: "w2", OutputHash: "abc", CompletedAt: 101},
		{WorkerID: "w3", OutputHash: "xyz", CompletedAt: 102},
	}
	out := Aggregate(policy, results, 103)
	if out.Decision != AggDecisionAgreed {
		t.Fatalf("expected agreed, got %s", out.Decision)
	}
	if len(out.Agreement) != 2 {
		t.Fatalf("expected 2 agreeing workers, got %d", len(out.Agreement))
	}
	// w1 and w2 agree.
	for _, id := range out.Agreement {
		if id != "w1" && id != "w2" {
			t.Fatalf("unexpected agreeing worker %s", id)
		}
	}
}

func TestAggregate_Quorum_NoAgreement(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateQuorum, MinWorkers: 3, QuorumThreshold: 0.67}
	results := []WorkerResult{
		{WorkerID: "w1", OutputHash: "a"},
		{WorkerID: "w2", OutputHash: "b"},
		{WorkerID: "w3", OutputHash: "c"},
	}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionNoQuorum {
		t.Fatalf("expected no_quorum, got %s", out.Decision)
	}
}

func TestAggregate_Quorum_Unanimous(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateQuorum, MinWorkers: 3, QuorumThreshold: 1.0}
	results := []WorkerResult{
		{WorkerID: "w1", OutputHash: "same"},
		{WorkerID: "w2", OutputHash: "same"},
		{WorkerID: "w3", OutputHash: "same"},
	}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionAgreed {
		t.Fatalf("expected agreed, got %s", out.Decision)
	}
	if len(out.Agreement) != 3 {
		t.Fatalf("expected 3 agreeing, got %d", len(out.Agreement))
	}
}

func TestAggregate_Quorum_FallbackToOutput(t *testing.T) {
	// When OutputHash is empty, falls back to Output string.
	policy := AggregationPolicy{Strategy: AggregateQuorum, MinWorkers: 2, QuorumThreshold: 0.5}
	results := []WorkerResult{
		{WorkerID: "w1", Output: "42"},
		{WorkerID: "w2", Output: "42"},
	}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionAgreed {
		t.Fatalf("expected agreed with output fallback, got %s", out.Decision)
	}
}

func TestAggregate_Quorum_AllowPartial(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateQuorum, MinWorkers: 5, QuorumThreshold: 0.5, AllowPartial: true}
	results := []WorkerResult{
		{WorkerID: "w1", OutputHash: "same"},
		{WorkerID: "w2", OutputHash: "same"},
		{WorkerID: "w3", OutputHash: "diff"},
	}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionAgreed {
		t.Fatalf("expected agreed with partial, got %s", out.Decision)
	}
}

// ── Reviewer-worker strategy ────────────────────────────────────────────────

func TestAggregate_ReviewerWorker_NeedsTwoResults(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateReviewerWorker, MinWorkers: 1}
	results := []WorkerResult{{WorkerID: "w1", Output: "draft"}}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionPending {
		t.Fatalf("expected pending, got %s", out.Decision)
	}
}

func TestAggregate_ReviewerWorker_Approved(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateReviewerWorker, MinWorkers: 1}
	results := []WorkerResult{
		{WorkerID: "worker1", Output: "my draft output"},
		{WorkerID: "reviewer1", Output: "looks good", Meta: map[string]any{"verdict": "approved"}},
	}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionApproved {
		t.Fatalf("expected approved, got %s", out.Decision)
	}
	if out.SelectedID != "worker1" {
		t.Fatalf("expected worker1 selected, got %s", out.SelectedID)
	}
	if out.ReviewerID != "reviewer1" {
		t.Fatalf("expected reviewer1, got %s", out.ReviewerID)
	}
}

func TestAggregate_ReviewerWorker_Rejected(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateReviewerWorker, MinWorkers: 1}
	results := []WorkerResult{
		{WorkerID: "worker1", Output: "bad output"},
		{WorkerID: "reviewer1", Output: "needs improvement", Meta: map[string]any{"verdict": "rejected"}},
	}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionRejected {
		t.Fatalf("expected rejected, got %s", out.Decision)
	}
	if out.SelectedIndex != -1 {
		t.Fatalf("expected no selection, got index %d", out.SelectedIndex)
	}
}

func TestAggregate_ReviewerWorker_DefaultApproved(t *testing.T) {
	// When no verdict metadata, defaults to approved.
	policy := AggregationPolicy{Strategy: AggregateReviewerWorker, MinWorkers: 1}
	results := []WorkerResult{
		{WorkerID: "worker1", Output: "output"},
		{WorkerID: "reviewer1", Output: "no explicit verdict"},
	}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionApproved {
		t.Fatalf("expected approved (default), got %s", out.Decision)
	}
}

func TestAggregate_ReviewerWorker_AlternateVerdicts(t *testing.T) {
	// "accept", "pass" should work as approved; "fail" as rejected.
	for _, tc := range []struct {
		verdict  string
		expected AggregationDecision
	}{
		{"accept", AggDecisionApproved},
		{"pass", AggDecisionApproved},
		{"fail", AggDecisionRejected},
		{"reject", AggDecisionRejected},
	} {
		policy := AggregationPolicy{Strategy: AggregateReviewerWorker, MinWorkers: 1}
		results := []WorkerResult{
			{WorkerID: "w1", Output: "work"},
			{WorkerID: "r1", Output: "review", Meta: map[string]any{"verdict": tc.verdict}},
		}
		out := Aggregate(policy, results, 100)
		if out.Decision != tc.expected {
			t.Errorf("verdict=%q: expected %s, got %s", tc.verdict, tc.expected, out.Decision)
		}
	}
}

// ── Scoring functions ───────────────────────────────────────────────────────

func TestGetScorer_Known(t *testing.T) {
	for _, name := range []string{"quality", "speed", "token_efficiency"} {
		fn := GetScorer(name)
		if fn == nil {
			t.Errorf("expected scorer for %q", name)
		}
	}
}

func TestGetScorer_Unknown_DefaultsToQuality(t *testing.T) {
	fn := GetScorer("nonexistent")
	r := WorkerResult{Score: 42.0}
	if fn(r) != 42.0 {
		t.Fatal("expected fallback to quality scorer")
	}
}

func TestSpeedScore(t *testing.T) {
	fast := WorkerResult{CompletedAt: 100}
	slow := WorkerResult{CompletedAt: 200}
	if SpeedScore(fast) <= SpeedScore(slow) {
		t.Fatal("expected faster to score higher")
	}
}

func TestTokenEfficiencyScore(t *testing.T) {
	light := WorkerResult{Usage: state.TaskUsage{TotalTokens: 100}}
	heavy := WorkerResult{Usage: state.TaskUsage{TotalTokens: 1000}}
	if TokenEfficiencyScore(light) <= TokenEfficiencyScore(heavy) {
		t.Fatal("expected lighter to score higher")
	}
}

// ── Formatting ──────────────────────────────────────────────────────────────

func TestFormatAggregationOutcome(t *testing.T) {
	out := AggregationOutcome{
		Decision:   AggDecisionSelected,
		Strategy:   AggregateBestOfN,
		SelectedID: "w2",
		Reason:     "best of 3",
		Candidates: []WorkerResult{
			{WorkerID: "w1", Score: 0.5, Usage: state.TaskUsage{TotalTokens: 100}},
			{WorkerID: "w2", Score: 0.9, Usage: state.TaskUsage{TotalTokens: 200}},
		},
	}
	s := FormatAggregationOutcome(out)
	if s == "" {
		t.Fatal("expected non-empty format")
	}
	if !aggContains(s, "best_of_n") || !aggContains(s, "w2") {
		t.Fatalf("format missing key info: %s", s)
	}
}

func TestFormatAggregationOutcome_WithReview(t *testing.T) {
	out := AggregationOutcome{
		Decision:    AggDecisionApproved,
		Strategy:    AggregateReviewerWorker,
		SelectedID:  "w1",
		ReviewerID:  "r1",
		ReviewNotes: "good work",
		Reason:      "reviewer approved",
		Candidates: []WorkerResult{
			{WorkerID: "w1"},
			{WorkerID: "r1"},
		},
	}
	s := FormatAggregationOutcome(out)
	if !aggContains(s, "r1") || !aggContains(s, "good work") {
		t.Fatalf("format missing reviewer info: %s", s)
	}
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestAggregationPolicy_JSON(t *testing.T) {
	p := AggregationPolicy{
		Strategy:        AggregateBestOfN,
		MinWorkers:      3,
		ScoringFunc:     "quality",
		Timeout:         30 * time.Second,
		QuorumThreshold: 0.5,
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var p2 AggregationPolicy
	if err := json.Unmarshal(data, &p2); err != nil {
		t.Fatal(err)
	}
	if p2.Strategy != p.Strategy || p2.MinWorkers != p.MinWorkers {
		t.Fatalf("round-trip mismatch: %+v vs %+v", p, p2)
	}
}

func TestAggregationOutcome_JSON(t *testing.T) {
	out := AggregationOutcome{
		Decision:   AggDecisionAgreed,
		Strategy:   AggregateQuorum,
		SelectedID: "w1",
		Agreement:  []string{"w1", "w2", "w3"},
		Candidates: []WorkerResult{
			{WorkerID: "w1", OutputHash: "abc"},
			{WorkerID: "w2", OutputHash: "abc"},
			{WorkerID: "w3", OutputHash: "abc"},
		},
		Reason:    "quorum",
		DecidedAt: 12345,
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var out2 AggregationOutcome
	if err := json.Unmarshal(data, &out2); err != nil {
		t.Fatal(err)
	}
	if out2.Decision != out.Decision || len(out2.Agreement) != 3 {
		t.Fatalf("round-trip mismatch")
	}
}

func TestWorkerResult_JSON(t *testing.T) {
	r := WorkerResult{
		WorkerID:    "w1",
		RunID:       "run-1",
		ResultRef:   "ref-abc",
		Output:      "hello world",
		OutputHash:  "sha256:abc",
		Score:       0.95,
		Usage:       state.TaskUsage{TotalTokens: 500, ToolCalls: 3},
		CompletedAt: 12345,
		Meta:        map[string]any{"key": "value"},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var r2 WorkerResult
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatal(err)
	}
	if r2.WorkerID != "w1" || r2.Score != 0.95 || r2.Usage.TotalTokens != 500 {
		t.Fatalf("round-trip mismatch: %+v", r2)
	}
}

// ── End-to-end ──────────────────────────────────────────────────────────────

func TestEndToEnd_BestOfN_Pipeline(t *testing.T) {
	// Simulate: 3 workers produce results, best-of-3 picks highest quality.
	policy := AggregationPolicy{
		Strategy:    AggregateBestOfN,
		MinWorkers:  3,
		ScoringFunc: "quality",
	}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}

	// Workers submit results incrementally.
	var results []WorkerResult

	// Only 2 results: should be pending.
	results = append(results,
		WorkerResult{WorkerID: "w1", Score: 0.7, CompletedAt: 100},
		WorkerResult{WorkerID: "w2", Score: 0.5, CompletedAt: 101},
	)
	out := Aggregate(policy, results, 102)
	if out.Decision != AggDecisionPending {
		t.Fatalf("expected pending with 2/3, got %s", out.Decision)
	}

	// Third result arrives.
	results = append(results, WorkerResult{WorkerID: "w3", Score: 0.9, CompletedAt: 103})
	out = Aggregate(policy, results, 104)
	if out.Decision != AggDecisionSelected {
		t.Fatalf("expected selected, got %s", out.Decision)
	}
	if out.SelectedID != "w3" {
		t.Fatalf("expected w3 (highest score), got %s", out.SelectedID)
	}

	// Evidence preserved.
	if len(out.Candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(out.Candidates))
	}
}

func TestEndToEnd_Quorum_Pipeline(t *testing.T) {
	policy := AggregationPolicy{
		Strategy:        AggregateQuorum,
		MinWorkers:      3,
		QuorumThreshold: 0.6, // ceil(3*0.6) = 2 → 2 of 3 must agree
	}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}

	results := []WorkerResult{
		{WorkerID: "w1", OutputHash: "answer-A", CompletedAt: 100},
		{WorkerID: "w2", OutputHash: "answer-B", CompletedAt: 101},
		{WorkerID: "w3", OutputHash: "answer-A", CompletedAt: 102},
	}
	out := Aggregate(policy, results, 103)
	if out.Decision != AggDecisionAgreed {
		t.Fatalf("expected agreed, got %s", out.Decision)
	}
	if len(out.Agreement) != 2 {
		t.Fatalf("expected 2 in agreement, got %d", len(out.Agreement))
	}
	// Candidates preserved.
	if len(out.Candidates) != 3 {
		t.Fatalf("expected 3 candidates preserved, got %d", len(out.Candidates))
	}
}

func TestEndToEnd_ReviewerWorker_Pipeline(t *testing.T) {
	policy := AggregationPolicy{
		Strategy:   AggregateReviewerWorker,
		MinWorkers: 1,
	}

	// Step 1: worker produces output.
	results := []WorkerResult{
		{WorkerID: "worker-1", Output: "draft answer", CompletedAt: 100},
	}
	out := Aggregate(policy, results, 101)
	if out.Decision != AggDecisionPending {
		t.Fatalf("expected pending without reviewer, got %s", out.Decision)
	}

	// Step 2: reviewer approves.
	results = append(results, WorkerResult{
		WorkerID:    "reviewer-1",
		Output:      "LGTM",
		CompletedAt: 102,
		Meta:        map[string]any{"verdict": "approved"},
	})
	out = Aggregate(policy, results, 103)
	if out.Decision != AggDecisionApproved {
		t.Fatalf("expected approved, got %s", out.Decision)
	}
	if out.SelectedID != "worker-1" {
		t.Fatalf("expected worker-1, got %s", out.SelectedID)
	}
	if out.ReviewerID != "reviewer-1" {
		t.Fatalf("expected reviewer-1, got %s", out.ReviewerID)
	}

	// Evidence preserved.
	if len(out.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(out.Candidates))
	}
}

func TestEndToEnd_ReviewerWorker_RejectRework(t *testing.T) {
	policy := AggregationPolicy{Strategy: AggregateReviewerWorker, MinWorkers: 1}

	// Round 1: worker output rejected.
	results := []WorkerResult{
		{WorkerID: "w1", Output: "first attempt"},
		{WorkerID: "r1", Output: "too short", Meta: map[string]any{"verdict": "rejected"}},
	}
	out := Aggregate(policy, results, 100)
	if out.Decision != AggDecisionRejected {
		t.Fatalf("expected rejected, got %s", out.Decision)
	}

	// Round 2: worker tries again, reviewer approves.
	results2 := []WorkerResult{
		{WorkerID: "w1", Output: "improved second attempt"},
		{WorkerID: "r1", Output: "much better", Meta: map[string]any{"verdict": "approved"}},
	}
	out2 := Aggregate(policy, results2, 200)
	if out2.Decision != AggDecisionApproved {
		t.Fatalf("expected approved on rework, got %s", out2.Decision)
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func aggContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
