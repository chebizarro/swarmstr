// Package memory — recall ranking and filtering for memory retrieval.
//
// RankRecallResults applies a configurable policy to re-rank and filter
// retrieved memories before they're packaged into the model prompt.
package memory

import (
	"sort"

	"metiq/internal/store/state"
)

// RecallPolicy controls how recall results are ranked and filtered.
type RecallPolicy struct {
	// FilterExpired removes memories past their ExpiresAt.
	FilterExpired bool
	// FilterInvalidated removes memories with a non-active MemStatus.
	FilterInvalidated bool

	// Weight factors for composite scoring (0 disables the signal).
	WeightConfidence float64 // default 0.3
	WeightReviewed   float64 // default 0.2
	WeightRecency    float64 // default 0.3
	WeightEpisodic   float64 // default 0.2

	// RecencyHalfLifeDays controls how fast recency decays.
	// A memory exactly this many days old scores 0.5 recency.
	// Default: 30 days.
	RecencyHalfLifeDays float64
}

// DefaultRecallPolicy returns sensible defaults.
func DefaultRecallPolicy() RecallPolicy {
	return RecallPolicy{
		FilterExpired:       true,
		FilterInvalidated:   true,
		WeightConfidence:    0.3,
		WeightReviewed:      0.2,
		WeightRecency:       0.3,
		WeightEpisodic:      0.2,
		RecencyHalfLifeDays: 30,
	}
}

// ScoredMemory pairs an IndexedMemory with its computed recall score.
type ScoredMemory struct {
	Memory IndexedMemory
	Score  float64
}

// RankRecallResults filters and re-ranks memories according to policy.
// It returns at most limit results, highest-score first.
// nowUnix is the current time (pass time.Now().Unix()); extracted for testability.
func RankRecallResults(items []IndexedMemory, policy RecallPolicy, limit int, nowUnix int64) []ScoredMemory {
	if len(items) == 0 || limit <= 0 {
		return nil
	}

	halfLife := policy.RecencyHalfLifeDays
	if halfLife <= 0 {
		halfLife = 30
	}
	halfLifeSec := halfLife * 24 * 3600

	scored := make([]ScoredMemory, 0, len(items))
	for _, item := range items {
		// Filter expired.
		if policy.FilterExpired && item.ExpiresAt > 0 && item.ExpiresAt <= nowUnix {
			continue
		}
		// Filter invalidated.
		if policy.FilterInvalidated && item.MemStatus != "" && item.MemStatus != state.MemStatusActive {
			continue
		}

		// Confidence signal (0–1).
		conf := item.Confidence
		if conf <= 0 {
			conf = 0.5 // DefaultConfidence
		}

		// Reviewed signal: 1.0 if reviewed, 0.0 if not.
		reviewed := 0.0
		if item.ReviewedAt > 0 {
			reviewed = 1.0
		}

		// Recency signal: exponential decay based on age.
		ageSec := float64(nowUnix - item.Unix)
		if ageSec < 0 {
			ageSec = 0
		}
		recency := 1.0
		if halfLifeSec > 0 && ageSec > 0 {
			// Exponential decay: score = 0.5^(age/halfLife)
			// Approximation: exp(-0.693 * age / halfLife)
			recency = exp2Decay(ageSec / halfLifeSec)
		}

		// Episodic signal: 1.0 if episodic type, 0.0 otherwise.
		episodic := 0.0
		if item.Type == "episodic" {
			episodic = 1.0
		}

		// Composite score.
		score := policy.WeightConfidence*conf +
			policy.WeightReviewed*reviewed +
			policy.WeightRecency*recency +
			policy.WeightEpisodic*episodic

		scored = append(scored, ScoredMemory{Memory: item, Score: score})
	}

	// Sort: highest score first; break ties by recency (newest first),
	// then by MemoryID for stability.
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		if scored[i].Memory.Unix != scored[j].Memory.Unix {
			return scored[i].Memory.Unix > scored[j].Memory.Unix
		}
		return scored[i].Memory.MemoryID < scored[j].Memory.MemoryID
	})

	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

// FilterActiveMemories returns only memories with active status and not expired.
// A convenience wrapper for retrieval paths that don't need full scoring.
func FilterActiveMemories(items []IndexedMemory, nowUnix int64) []IndexedMemory {
	out := make([]IndexedMemory, 0, len(items))
	for _, item := range items {
		if item.ExpiresAt > 0 && item.ExpiresAt <= nowUnix {
			continue
		}
		if item.MemStatus != "" && item.MemStatus != state.MemStatusActive {
			continue
		}
		out = append(out, item)
	}
	return out
}

// exp2Decay computes 0.5^x using the identity 2^(-x) = exp(-x * ln2).
func exp2Decay(x float64) float64 {
	if x <= 0 {
		return 1.0
	}
	// Fast path: for very large x, clamp to near-zero.
	if x > 50 {
		return 0.0
	}
	// Taylor series for exp(-x * ln2) — accurate enough for ranking.
	const ln2 = 0.6931471805599453
	t := -x * ln2
	// exp(t) via Horner's method, degree 12.
	result := 1.0
	term := 1.0
	for i := 1; i <= 12; i++ {
		term *= t / float64(i)
		result += term
	}
	if result < 0 {
		return 0
	}
	return result
}
