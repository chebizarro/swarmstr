// eval_harness.go defines evaluation suites and acceptance gates for
// prompt/policy revisions.  Before a PolicyProposal is applied, it can be
// evaluated against a reproducible suite of benchmark cases.  The gate
// compares candidate metrics against baseline thresholds to produce an
// explicit pass/fail decision.
package planner

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// ── Evaluation case ────────────────────────────────────────────────────────────

// EvalCase is a single benchmark case in an evaluation suite.
type EvalCase struct {
	CaseID      string         `json:"case_id"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Input       string         `json:"input"`                // the prompt or scenario input
	Expected    string         `json:"expected,omitempty"`    // expected output (substring, pattern, or semantic)
	MatchMode   EvalMatchMode  `json:"match_mode,omitempty"` // how to compare actual vs expected
	Tags        []string       `json:"tags,omitempty"`
	Weight      float64        `json:"weight,omitempty"` // 0 means 1.0
	Meta        map[string]any `json:"meta,omitempty"`
}

// EffectiveWeight returns the case weight, defaulting to 1.0.
func (c EvalCase) EffectiveWeight() float64 {
	if c.Weight <= 0 {
		return 1.0
	}
	return c.Weight
}

// EvalMatchMode describes how actual output is compared to expected.
type EvalMatchMode string

const (
	EvalMatchContains EvalMatchMode = "contains"
	EvalMatchExact    EvalMatchMode = "exact"
	EvalMatchNotEmpty EvalMatchMode = "not_empty"
	EvalMatchCustom   EvalMatchMode = "custom" // evaluated by external function
)

// ValidEvalMatchMode reports whether m is recognized.
func ValidEvalMatchMode(m EvalMatchMode) bool {
	switch m {
	case EvalMatchContains, EvalMatchExact, EvalMatchNotEmpty, EvalMatchCustom, "":
		return true
	}
	return false
}

// ── Evaluation suite ───────────────────────────────────────────────────────────

// EvalSuite is a named collection of benchmark cases.
type EvalSuite struct {
	SuiteID     string     `json:"suite_id"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Cases       []EvalCase `json:"cases"`
	CreatedAt   int64      `json:"created_at"`
	CreatedBy   string     `json:"created_by,omitempty"`
}

// Validate checks the suite for required fields and unique case IDs.
func (s EvalSuite) Validate() error {
	if strings.TrimSpace(s.SuiteID) == "" {
		return fmt.Errorf("suite_id is required")
	}
	if strings.TrimSpace(s.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if len(s.Cases) == 0 {
		return fmt.Errorf("at least one case is required")
	}
	seen := make(map[string]bool, len(s.Cases))
	for i, c := range s.Cases {
		if strings.TrimSpace(c.CaseID) == "" {
			return fmt.Errorf("case[%d].case_id is required", i)
		}
		if seen[c.CaseID] {
			return fmt.Errorf("duplicate case_id %q", c.CaseID)
		}
		seen[c.CaseID] = true
		if !ValidEvalMatchMode(c.MatchMode) {
			return fmt.Errorf("case %q: invalid match_mode %q", c.CaseID, c.MatchMode)
		}
	}
	return nil
}

// ── Case result ────────────────────────────────────────────────────────────────

// EvalCaseResult is the outcome of evaluating a single case.
type EvalCaseResult struct {
	CaseID   string  `json:"case_id"`
	Passed   bool    `json:"passed"`
	Actual   string  `json:"actual,omitempty"` // actual output
	Score    float64 `json:"score"`            // 0.0–1.0
	Error    string  `json:"error,omitempty"`
	Duration int64   `json:"duration_ms,omitempty"`
}

// ── Acceptance threshold ───────────────────────────────────────────────────────

// AcceptanceThreshold defines the criteria for gating a policy rollout.
type AcceptanceThreshold struct {
	// MinPassRate is the minimum fraction of cases that must pass (0.0–1.0).
	MinPassRate float64 `json:"min_pass_rate"`
	// MinWeightedScore is the minimum weighted average score (0.0–1.0).
	MinWeightedScore float64 `json:"min_weighted_score"`
	// MaxRegressions is the maximum number of cases that may regress vs baseline.
	MaxRegressions int `json:"max_regressions"`
	// RequireAllCritical requires all cases tagged "critical" to pass.
	RequireAllCritical bool `json:"require_all_critical"`
}

// DefaultAcceptanceThreshold returns production-suitable defaults.
func DefaultAcceptanceThreshold() AcceptanceThreshold {
	return AcceptanceThreshold{
		MinPassRate:        0.90,
		MinWeightedScore:   0.80,
		MaxRegressions:     2,
		RequireAllCritical: true,
	}
}

// ── Evaluation result ──────────────────────────────────────────────────────────

// EvalResult is the full outcome of evaluating a proposal against a suite.
type EvalResult struct {
	EvalID     string           `json:"eval_id"`
	SuiteID    string           `json:"suite_id"`
	ProposalID string           `json:"proposal_id"`
	VersionID  string           `json:"version_id,omitempty"` // if evaluated against a specific version

	CaseResults []EvalCaseResult `json:"case_results"`

	// Aggregate metrics.
	TotalCases  int     `json:"total_cases"`
	PassedCases int     `json:"passed_cases"`
	FailedCases int     `json:"failed_cases"`
	ErrorCases  int     `json:"error_cases"`
	PassRate    float64 `json:"pass_rate"`
	WeightedScore float64 `json:"weighted_score"`
	Regressions int     `json:"regressions"` // cases that passed in baseline but failed here

	// Gate decision.
	GateDecision EvalGateDecision `json:"gate_decision"`
	GateReason   string           `json:"gate_reason"`

	// Timestamps.
	StartedAt   int64 `json:"started_at"`
	CompletedAt int64 `json:"completed_at"`
	DurationMS  int64 `json:"duration_ms"`

	Meta map[string]any `json:"meta,omitempty"`
}

// EvalGateDecision is the rollout gate outcome.
type EvalGateDecision string

const (
	EvalGatePass EvalGateDecision = "pass"
	EvalGateFail EvalGateDecision = "fail"
	EvalGateWarn EvalGateDecision = "warn" // passed threshold but has regressions
)

// ── Case evaluator ─────────────────────────────────────────────────────────────

// CaseEvaluatorFunc evaluates a single case and returns the result.
// Implementations may call LLMs, run scripts, or apply heuristics.
type CaseEvaluatorFunc func(evalCase EvalCase, candidateValue string) EvalCaseResult

// BuiltinCaseEvaluator applies the built-in match modes.
func BuiltinCaseEvaluator(c EvalCase, candidateValue string) EvalCaseResult {
	start := time.Now()
	result := EvalCaseResult{CaseID: c.CaseID}

	mode := c.MatchMode
	if mode == "" {
		mode = EvalMatchContains
	}

	switch mode {
	case EvalMatchContains:
		if c.Expected == "" {
			result.Passed = true
			result.Score = 1.0
		} else {
			result.Passed = strings.Contains(candidateValue, c.Expected)
			if result.Passed {
				result.Score = 1.0
			}
		}
	case EvalMatchExact:
		result.Passed = candidateValue == c.Expected
		if result.Passed {
			result.Score = 1.0
		}
	case EvalMatchNotEmpty:
		result.Passed = strings.TrimSpace(candidateValue) != ""
		if result.Passed {
			result.Score = 1.0
		}
	case EvalMatchCustom:
		result.Error = "custom evaluator not provided"
	default:
		result.Error = fmt.Sprintf("unknown match mode %q", mode)
	}

	result.Actual = candidateValue
	result.Duration = time.Since(start).Milliseconds()
	return result
}

// ── Evaluation runner ──────────────────────────────────────────────────────────

// EvalRunner executes evaluation suites against candidate prompt/policy values.
type EvalRunner struct {
	mu        sync.Mutex
	evaluator CaseEvaluatorFunc
	nextID    int
	prefix    string
}

// NewEvalRunner creates a runner with the given evaluator and ID prefix.
func NewEvalRunner(evaluator CaseEvaluatorFunc, prefix string) *EvalRunner {
	if evaluator == nil {
		evaluator = BuiltinCaseEvaluator
	}
	if prefix == "" {
		prefix = "eval"
	}
	return &EvalRunner{evaluator: evaluator, prefix: prefix}
}

func (r *EvalRunner) generateID() string {
	r.mu.Lock()
	r.nextID++
	id := fmt.Sprintf("%s-%d", r.prefix, r.nextID)
	r.mu.Unlock()
	return id
}

// Run evaluates a candidate value against a suite and applies acceptance gating.
// If baseline is non-nil, regressions are counted against it.
//
// Note: EvalCase.Input carries the scenario description but the built-in
// evaluator matches candidateValue directly against Expected.  Callers that
// need per-case model invocation should supply a custom CaseEvaluatorFunc
// that uses Input to drive the model and returns the result.
func (r *EvalRunner) Run(
	suite EvalSuite,
	proposal state.PolicyProposal,
	candidateValue string,
	threshold AcceptanceThreshold,
	baseline map[string]bool, // case_id → passed in baseline (nil = no baseline)
	now int64,
) EvalResult {
	started := time.Now()

	// Validate suite upfront so duplicate case IDs / invalid modes are caught.
	if err := suite.Validate(); err != nil {
		return EvalResult{
			EvalID:       r.generateID(),
			SuiteID:      suite.SuiteID,
			ProposalID:   proposal.ProposalID,
			StartedAt:    now,
			CompletedAt:  time.Now().Unix(),
			DurationMS:   time.Since(started).Milliseconds(),
			GateDecision: EvalGateFail,
			GateReason:   fmt.Sprintf("invalid suite: %v", err),
		}
	}

	result := EvalResult{
		EvalID:     r.generateID(),
		SuiteID:    suite.SuiteID,
		ProposalID: proposal.ProposalID,
		TotalCases: len(suite.Cases),
		StartedAt:  now,
	}

	var totalWeight float64
	var weightedSum float64

	for _, c := range suite.Cases {
		cr := r.evaluator(c, candidateValue)

		if cr.Error != "" {
			result.ErrorCases++
		} else if cr.Passed {
			result.PassedCases++
		} else {
			result.FailedCases++
		}

		w := c.EffectiveWeight()
		totalWeight += w
		weightedSum += cr.Score * w

		// Count regressions.
		if baseline != nil {
			if baselinePassed, exists := baseline[c.CaseID]; exists && baselinePassed && !cr.Passed {
				result.Regressions++
			}
		}

		result.CaseResults = append(result.CaseResults, cr)
	}

	if result.TotalCases > 0 {
		result.PassRate = float64(result.PassedCases) / float64(result.TotalCases)
	}
	if totalWeight > 0 {
		result.WeightedScore = weightedSum / totalWeight
	}

	result.CompletedAt = time.Now().Unix()
	result.DurationMS = time.Since(started).Milliseconds()

	// Apply gate.
	result.GateDecision, result.GateReason = applyGate(result, threshold, suite)

	return result
}

// applyGate evaluates the result against acceptance thresholds.
func applyGate(result EvalResult, threshold AcceptanceThreshold, suite EvalSuite) (EvalGateDecision, string) {
	var reasons []string

	// Build a lookup map once to avoid O(n²) nested loops.
	caseByID := make(map[string]EvalCase, len(suite.Cases))
	for _, c := range suite.Cases {
		caseByID[c.CaseID] = c
	}

	// Check critical cases.
	if threshold.RequireAllCritical {
		for _, cr := range result.CaseResults {
			if !cr.Passed {
				if c, ok := caseByID[cr.CaseID]; ok && hasCriticalTag(c.Tags) {
					reasons = append(reasons, fmt.Sprintf("critical case %q failed", cr.CaseID))
				}
			}
		}
	}

	if result.PassRate < threshold.MinPassRate {
		reasons = append(reasons, fmt.Sprintf("pass rate %.1f%% < %.1f%% threshold",
			result.PassRate*100, threshold.MinPassRate*100))
	}

	if result.WeightedScore < threshold.MinWeightedScore {
		reasons = append(reasons, fmt.Sprintf("weighted score %.2f < %.2f threshold",
			result.WeightedScore, threshold.MinWeightedScore))
	}

	if len(reasons) > 0 {
		return EvalGateFail, strings.Join(reasons, "; ")
	}

	// Passed thresholds — check for regressions (warn but don't block).
	if result.Regressions > threshold.MaxRegressions {
		return EvalGateWarn, fmt.Sprintf("%d regressions exceed %d max (non-blocking)",
			result.Regressions, threshold.MaxRegressions)
	}

	return EvalGatePass, "all thresholds met"
}

func hasCriticalTag(tags []string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, "critical") {
			return true
		}
	}
	return false
}

// ── Formatting ─────────────────────────────────────────────────────────────────

// FormatEvalResult returns a human-readable summary.
func FormatEvalResult(r EvalResult) string {
	var b strings.Builder
	icon := evalGateIcon(r.GateDecision)
	fmt.Fprintf(&b, "%s Evaluation %s (suite=%s proposal=%s)\n",
		icon, r.EvalID, r.SuiteID, r.ProposalID)
	fmt.Fprintf(&b, "  Cases: %d total, %d passed, %d failed, %d errors\n",
		r.TotalCases, r.PassedCases, r.FailedCases, r.ErrorCases)
	fmt.Fprintf(&b, "  Pass rate: %.1f%%  Weighted score: %.2f  Regressions: %d\n",
		r.PassRate*100, r.WeightedScore, r.Regressions)
	fmt.Fprintf(&b, "  Gate: %s — %s\n", r.GateDecision, r.GateReason)
	return b.String()
}

func evalGateIcon(d EvalGateDecision) string {
	switch d {
	case EvalGatePass:
		return "✅"
	case EvalGateFail:
		return "❌"
	case EvalGateWarn:
		return "⚠️"
	default:
		return "•"
	}
}

// FormatEvalCaseResults returns per-case details.
func FormatEvalCaseResults(results []EvalCaseResult) string {
	if len(results) == 0 {
		return "No case results."
	}
	var b strings.Builder
	for _, cr := range results {
		icon := "✓"
		if cr.Error != "" {
			icon = "⚡"
		} else if !cr.Passed {
			icon = "✗"
		}
		fmt.Fprintf(&b, "  %s %s score=%.2f", icon, cr.CaseID, cr.Score)
		if cr.Error != "" {
			fmt.Fprintf(&b, " error=%s", cr.Error)
		}
		b.WriteString("\n")
	}
	return b.String()
}
