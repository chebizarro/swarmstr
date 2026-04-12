package planner

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// ── Aggregation strategies ───────────────────────────────────────────────────

// AggregationStrategy names the supported multi-worker aggregation modes.
type AggregationStrategy string

const (
	// AggregateFirst takes the first completed result.
	AggregateFirst AggregationStrategy = "first"
	// AggregateBestOfN collects N candidates and selects the best by scoring.
	AggregateBestOfN AggregationStrategy = "best_of_n"
	// AggregateQuorum requires a majority of workers to agree.
	AggregateQuorum AggregationStrategy = "quorum"
	// AggregateReviewerWorker uses a worker→reviewer pipeline.
	AggregateReviewerWorker AggregationStrategy = "reviewer_worker"
)

var validStrategies = map[AggregationStrategy]bool{
	AggregateFirst:          true,
	AggregateBestOfN:        true,
	AggregateQuorum:         true,
	AggregateReviewerWorker: true,
}

// ValidAggregationStrategy reports whether s is a recognised strategy.
func ValidAggregationStrategy(s AggregationStrategy) bool { return validStrategies[s] }

// ── Aggregation policy ──────────────────────────────────────────────────────

// AggregationPolicy describes how a parent task aggregates delegated results.
type AggregationPolicy struct {
	// Strategy selects the aggregation mode.
	Strategy AggregationStrategy `json:"strategy"`
	// MinWorkers is the minimum number of workers to collect results from.
	// For best_of_n, this is N. For quorum, this is the pool size.
	MinWorkers int `json:"min_workers"`
	// QuorumThreshold is the fraction of workers that must agree (0.0-1.0).
	// Only used with quorum strategy.
	QuorumThreshold float64 `json:"quorum_threshold,omitempty"`
	// ScoringFunc names the scoring function for best_of_n selection.
	// Built-in: "quality", "speed", "token_efficiency". Default: "quality".
	ScoringFunc string `json:"scoring_func,omitempty"`
	// Timeout is the maximum time to wait for results before deciding.
	Timeout time.Duration `json:"timeout,omitempty"`
	// AllowPartial lets the aggregator decide with fewer than MinWorkers
	// if Timeout is reached.
	AllowPartial bool `json:"allow_partial,omitempty"`
}

// Validate checks the policy for consistency.
func (p AggregationPolicy) Validate() error {
	if !ValidAggregationStrategy(p.Strategy) {
		return fmt.Errorf("unknown aggregation strategy %q", p.Strategy)
	}
	switch p.Strategy {
	case AggregateBestOfN:
		if p.MinWorkers < 2 {
			return fmt.Errorf("best_of_n requires min_workers >= 2, got %d", p.MinWorkers)
		}
	case AggregateQuorum:
		if p.MinWorkers < 2 {
			return fmt.Errorf("quorum requires min_workers >= 2, got %d", p.MinWorkers)
		}
		if p.QuorumThreshold <= 0 || p.QuorumThreshold > 1.0 {
			return fmt.Errorf("quorum_threshold must be in (0, 1.0], got %f", p.QuorumThreshold)
		}
	case AggregateReviewerWorker:
		if p.MinWorkers < 1 {
			return fmt.Errorf("reviewer_worker requires min_workers >= 1, got %d", p.MinWorkers)
		}
	case AggregateFirst:
		if p.MinWorkers < 1 {
			p.MinWorkers = 1
		}
	}
	return nil
}

// ── Worker result ────────────────────────────────────────────────────────────

// WorkerResult captures a completed worker's output for aggregation.
type WorkerResult struct {
	WorkerID    string          `json:"worker_id"`
	RunID       string          `json:"run_id"`
	ResultRef   string          `json:"result_ref,omitempty"`
	Output      string          `json:"output,omitempty"`
	OutputHash  string          `json:"output_hash,omitempty"`
	Score       float64         `json:"score,omitempty"`
	Usage       state.TaskUsage `json:"usage,omitempty"`
	CompletedAt int64           `json:"completed_at"`
	Meta        map[string]any  `json:"meta,omitempty"`
}

// ── Aggregation outcome ─────────────────────────────────────────────────────

// AggregationDecision is the result of running an aggregation strategy.
type AggregationDecision string

const (
	AggDecisionSelected  AggregationDecision = "selected"   // a single result was selected
	AggDecisionAgreed    AggregationDecision = "agreed"     // quorum reached agreement
	AggDecisionApproved  AggregationDecision = "approved"   // reviewer approved worker output
	AggDecisionRejected  AggregationDecision = "rejected"   // reviewer rejected; needs rework
	AggDecisionPending   AggregationDecision = "pending"    // not enough results yet
	AggDecisionNoQuorum  AggregationDecision = "no_quorum"  // quorum not reached
	AggDecisionTimedOut  AggregationDecision = "timed_out"  // timed out waiting for results
)

// AggregationOutcome is the output of an aggregation run.
type AggregationOutcome struct {
	Decision      AggregationDecision `json:"decision"`
	Strategy      AggregationStrategy `json:"strategy"`
	SelectedIndex int                 `json:"selected_index,omitempty"` // index into candidates
	SelectedID    string              `json:"selected_id,omitempty"`    // worker ID of selected
	Agreement     []string            `json:"agreement,omitempty"`      // worker IDs that agree
	ReviewerID    string              `json:"reviewer_id,omitempty"`
	ReviewNotes   string              `json:"review_notes,omitempty"`
	Candidates    []WorkerResult      `json:"candidates"`
	Reason        string              `json:"reason,omitempty"`
	DecidedAt     int64               `json:"decided_at"`
}

// ── Scoring functions ────────────────────────────────────────────────────────

// ScoringFunc scores a WorkerResult. Higher is better.
type ScoringFunc func(WorkerResult) float64

// QualityScore uses the worker's pre-assigned Score field (set by verifier
// or self-report).
func QualityScore(r WorkerResult) float64 { return r.Score }

// SpeedScore prefers faster completions. Returns inverse of completion time
// relative to earliest candidate.
func SpeedScore(r WorkerResult) float64 {
	if r.CompletedAt <= 0 {
		return 0
	}
	// Inverse: lower timestamp = higher score.
	return -float64(r.CompletedAt)
}

// TokenEfficiencyScore prefers lower token usage for equivalent results.
func TokenEfficiencyScore(r WorkerResult) float64 {
	if r.Usage.TotalTokens <= 0 {
		return 0
	}
	return -float64(r.Usage.TotalTokens)
}

var builtinScorers = map[string]ScoringFunc{
	"quality":          QualityScore,
	"speed":            SpeedScore,
	"token_efficiency": TokenEfficiencyScore,
}

// GetScorer returns a scoring function by name. Returns QualityScore if unknown.
func GetScorer(name string) ScoringFunc {
	if fn, ok := builtinScorers[strings.ToLower(strings.TrimSpace(name))]; ok {
		return fn
	}
	return QualityScore
}

// ── Aggregation engine ──────────────────────────────────────────────────────

// Aggregate runs the aggregation strategy against collected worker results.
func Aggregate(policy AggregationPolicy, results []WorkerResult, now int64) AggregationOutcome {
	base := AggregationOutcome{
		Strategy:   policy.Strategy,
		Candidates: results,
		DecidedAt:  now,
	}

	switch policy.Strategy {
	case AggregateFirst:
		return aggregateFirst(base, results)
	case AggregateBestOfN:
		return aggregateBestOfN(base, policy, results)
	case AggregateQuorum:
		return aggregateQuorum(base, policy, results)
	case AggregateReviewerWorker:
		return aggregateReviewerWorker(base, results)
	default:
		base.Decision = AggDecisionPending
		base.Reason = fmt.Sprintf("unknown strategy %q", policy.Strategy)
		return base
	}
}

// ── Strategy: first ─────────────────────────────────────────────────────────

func aggregateFirst(base AggregationOutcome, results []WorkerResult) AggregationOutcome {
	if len(results) == 0 {
		base.Decision = AggDecisionPending
		base.Reason = "no results yet"
		return base
	}
	base.Decision = AggDecisionSelected
	base.SelectedIndex = 0
	base.SelectedID = results[0].WorkerID
	base.Reason = "first completed result"
	return base
}

// ── Strategy: best_of_n ─────────────────────────────────────────────────────

func aggregateBestOfN(base AggregationOutcome, policy AggregationPolicy, results []WorkerResult) AggregationOutcome {
	if len(results) < policy.MinWorkers {
		if !policy.AllowPartial || len(results) == 0 {
			base.Decision = AggDecisionPending
			base.Reason = fmt.Sprintf("need %d results, have %d", policy.MinWorkers, len(results))
			return base
		}
	}

	scorer := GetScorer(policy.ScoringFunc)

	// Score all candidates.
	type scored struct {
		idx   int
		score float64
	}
	scoredResults := make([]scored, len(results))
	for i, r := range results {
		scoredResults[i] = scored{idx: i, score: scorer(r)}
	}
	sort.Slice(scoredResults, func(i, j int) bool {
		return scoredResults[i].score > scoredResults[j].score // descending
	})

	best := scoredResults[0]
	base.Decision = AggDecisionSelected
	base.SelectedIndex = best.idx
	base.SelectedID = results[best.idx].WorkerID
	base.Reason = fmt.Sprintf("best of %d by %s (score=%.2f)",
		len(results), aggNonEmpty(policy.ScoringFunc, "quality"), best.score)

	// Preserve per-candidate scores in the output (by original index, not sort order).
	for _, sr := range scoredResults {
		results[sr.idx].Score = sr.score
	}
	base.Candidates = results

	return base
}

// ── Strategy: quorum ────────────────────────────────────────────────────────

func aggregateQuorum(base AggregationOutcome, policy AggregationPolicy, results []WorkerResult) AggregationOutcome {
	if len(results) < policy.MinWorkers {
		if !policy.AllowPartial || len(results) == 0 {
			base.Decision = AggDecisionPending
			base.Reason = fmt.Sprintf("need %d results, have %d", policy.MinWorkers, len(results))
			return base
		}
	}

	// Group by OutputHash for agreement detection.
	groups := map[string][]int{} // hash → indices
	for i, r := range results {
		key := r.OutputHash
		if key == "" {
			key = r.Output // fallback: raw output string
		}
		groups[key] = append(groups[key], i)
	}

	threshold := int(math.Ceil(float64(len(results)) * policy.QuorumThreshold))
	if threshold < 1 {
		threshold = 1
	}
	if threshold > len(results) {
		threshold = len(results)
	}

	// Find the largest agreement group.
	var bestKey string
	var bestGroup []int
	for k, indices := range groups {
		if len(indices) > len(bestGroup) {
			bestKey = k
			bestGroup = indices
		}
	}

	if len(bestGroup) >= threshold {
		agreeIDs := make([]string, len(bestGroup))
		for i, idx := range bestGroup {
			agreeIDs[i] = results[idx].WorkerID
		}
		base.Decision = AggDecisionAgreed
		base.Agreement = agreeIDs
		base.SelectedIndex = bestGroup[0]
		base.SelectedID = results[bestGroup[0]].WorkerID
		base.Reason = fmt.Sprintf("quorum reached: %d/%d agree (threshold=%d, hash=%s)",
			len(bestGroup), len(results), threshold, aggTruncate(bestKey, 16))
		return base
	}

	base.Decision = AggDecisionNoQuorum
	base.Reason = fmt.Sprintf("no quorum: largest group=%d, need=%d of %d",
		len(bestGroup), threshold, len(results))
	return base
}

// ── Strategy: reviewer_worker ───────────────────────────────────────────────

// aggregateReviewerWorker expects results in order: [worker_results..., reviewer_result].
// The last result is treated as the reviewer's verdict.
// Reviewer meta should contain "verdict": "approved" or "rejected".
func aggregateReviewerWorker(base AggregationOutcome, results []WorkerResult) AggregationOutcome {
	if len(results) < 2 {
		base.Decision = AggDecisionPending
		base.Reason = "need at least a worker result and a reviewer result"
		return base
	}

	reviewer := results[len(results)-1]
	workerResult := results[len(results)-2] // last worker before reviewer

	base.ReviewerID = reviewer.WorkerID
	base.ReviewNotes = reviewer.Output

	verdict := "approved" // default
	if reviewer.Meta != nil {
		if v, ok := reviewer.Meta["verdict"].(string); ok {
			verdict = strings.ToLower(strings.TrimSpace(v))
		}
	}

	switch verdict {
	case "approved", "accept", "pass":
		base.Decision = AggDecisionApproved
		base.SelectedIndex = len(results) - 2
		base.SelectedID = workerResult.WorkerID
		base.Reason = fmt.Sprintf("reviewer %s approved worker %s", reviewer.WorkerID, workerResult.WorkerID)
	case "rejected", "reject", "fail":
		base.Decision = AggDecisionRejected
		base.SelectedIndex = -1
		base.Reason = fmt.Sprintf("reviewer %s rejected: %s",
			reviewer.WorkerID, aggTruncate(reviewer.Output, 100))
	default:
		base.Decision = AggDecisionApproved
		base.SelectedIndex = len(results) - 2
		base.SelectedID = workerResult.WorkerID
		base.Reason = fmt.Sprintf("reviewer %s verdict=%q (treating as approved)", reviewer.WorkerID, verdict)
	}

	return base
}

// ── Formatting ──────────────────────────────────────────────────────────────

// FormatAggregationOutcome returns a human-readable summary.
func FormatAggregationOutcome(o AggregationOutcome) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Aggregation: strategy=%s decision=%s\n", o.Strategy, o.Decision)
	if o.SelectedID != "" {
		fmt.Fprintf(&b, "  Selected: worker=%s (index=%d)\n", o.SelectedID, o.SelectedIndex)
	}
	if len(o.Agreement) > 0 {
		fmt.Fprintf(&b, "  Agreement: %s\n", strings.Join(o.Agreement, ", "))
	}
	if o.ReviewerID != "" {
		fmt.Fprintf(&b, "  Reviewer: %s\n", o.ReviewerID)
	}
	if o.ReviewNotes != "" {
		fmt.Fprintf(&b, "  Review notes: %s\n", aggTruncate(o.ReviewNotes, 200))
	}
	fmt.Fprintf(&b, "  Reason: %s\n", o.Reason)
	fmt.Fprintf(&b, "  Candidates: %d\n", len(o.Candidates))
	for i, c := range o.Candidates {
		fmt.Fprintf(&b, "    [%d] worker=%s score=%.2f tokens=%d\n",
			i, c.WorkerID, c.Score, c.Usage.TotalTokens)
	}
	return b.String()
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func aggNonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func aggTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
