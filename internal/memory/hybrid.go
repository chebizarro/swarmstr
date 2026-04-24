// Package memory — Hybrid search combining vector similarity with keyword matching.
//
// Hybrid search merges results from vector (semantic) search and FTS (keyword)
// search with configurable weights. This provides better recall than either
// method alone:
//   - Vector search captures semantic meaning ("car" matches "automobile")
//   - Keyword search ensures exact term matches are found
//
// The merge algorithm:
//  1. Fetch candidates from both vector and keyword search (oversampled)
//  2. Merge by ID, combining scores: vectorWeight*vecScore + textWeight*ftsScore
//  3. Apply temporal decay (optional)
//  4. Apply MMR re-ranking (optional)
//  5. Filter by minScore and return top results
package memory

import (
	"math"
	"sort"
	"time"
)

// HybridSearchConfig configures hybrid search behavior.
type HybridSearchConfig struct {
	// Enabled toggles hybrid search (default: true).
	// When disabled, only vector search is used (if available).
	Enabled bool `json:"enabled"`

	// VectorWeight is the weight for vector similarity scores (default: 0.7).
	VectorWeight float64 `json:"vector_weight"`

	// TextWeight is the weight for FTS/keyword scores (default: 0.3).
	TextWeight float64 `json:"text_weight"`

	// CandidateMultiplier controls oversampling for better recall (default: 4.0).
	// We fetch limit * multiplier candidates from each source before merging.
	CandidateMultiplier float64 `json:"candidate_multiplier"`

	// MinScore is the minimum score threshold for results (default: 0.35).
	MinScore float64 `json:"min_score"`

	// TemporalDecay configures recency-based score adjustment.
	TemporalDecay TemporalDecayConfig `json:"temporal_decay"`

	// MMR configures diversity-based re-ranking.
	MMR MMRConfig `json:"mmr"`
}

// TemporalDecayConfig configures temporal decay for search results.
type TemporalDecayConfig struct {
	// Enabled toggles temporal decay (default: false).
	Enabled bool `json:"enabled"`

	// HalfLifeDays is the number of days after which a memory's score is halved.
	// Default: 30 days.
	HalfLifeDays float64 `json:"half_life_days"`
}

// DefaultHybridSearchConfig returns sensible defaults.
func DefaultHybridSearchConfig() HybridSearchConfig {
	return HybridSearchConfig{
		Enabled:             true,
		VectorWeight:        0.7,
		TextWeight:          0.3,
		CandidateMultiplier: 4.0,
		MinScore:            0.35,
		TemporalDecay: TemporalDecayConfig{
			Enabled:      false,
			HalfLifeDays: 30,
		},
		MMR: DefaultMMRConfig(),
	}
}

// DefaultTemporalDecayConfig returns sensible defaults.
func DefaultTemporalDecayConfig() TemporalDecayConfig {
	return TemporalDecayConfig{
		Enabled:      false,
		HalfLifeDays: 30,
	}
}

// ── Hybrid search types ─────────────────────────────────────────────────────

// HybridResult represents a merged search result with combined score.
type HybridResult struct {
	Memory      IndexedMemory
	VectorScore float64
	TextScore   float64
	Score       float64 // Combined score
}

// HybridVectorResult represents a vector search result.
type HybridVectorResult struct {
	Memory IndexedMemory
	Score  float64
}

// HybridKeywordResult represents a keyword search result.
type HybridKeywordResult struct {
	Memory IndexedMemory
	Score  float64
}

// ── Merge algorithm ─────────────────────────────────────────────────────────

// MergeHybridResults merges vector and keyword search results.
func MergeHybridResults(
	vector []HybridVectorResult,
	keyword []HybridKeywordResult,
	cfg HybridSearchConfig,
) []HybridResult {
	// Build map by memory ID
	byID := make(map[string]*HybridResult)

	for _, r := range vector {
		id := r.Memory.MemoryID
		byID[id] = &HybridResult{
			Memory:      r.Memory,
			VectorScore: r.Score,
			TextScore:   0,
		}
	}

	for _, r := range keyword {
		id := r.Memory.MemoryID
		if existing, ok := byID[id]; ok {
			existing.TextScore = r.Score
			// Prefer keyword result's text if available (may have highlights)
			if r.Memory.Text != "" {
				existing.Memory.Text = r.Memory.Text
			}
		} else {
			byID[id] = &HybridResult{
				Memory:      r.Memory,
				VectorScore: 0,
				TextScore:   r.Score,
			}
		}
	}

	// Calculate combined scores
	results := make([]HybridResult, 0, len(byID))
	for _, r := range byID {
		r.Score = cfg.VectorWeight*r.VectorScore + cfg.TextWeight*r.TextScore
		results = append(results, *r)
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		// Tiebreaker: prefer newer memories
		return results[i].Memory.Unix > results[j].Memory.Unix
	})

	return results
}

// ApplyTemporalDecay applies time-based score decay to results.
func ApplyTemporalDecay(results []HybridResult, cfg TemporalDecayConfig, nowUnix int64) []HybridResult {
	if !cfg.Enabled || cfg.HalfLifeDays <= 0 {
		return results
	}

	halfLifeSec := cfg.HalfLifeDays * 24 * 3600

	for i := range results {
		ageSec := float64(nowUnix - results[i].Memory.Unix)
		if ageSec <= 0 {
			continue
		}

		// Exponential decay: score * 0.5^(age/halfLife)
		decay := math.Pow(0.5, ageSec/halfLifeSec)
		results[i].Score *= decay
	}

	// Re-sort after decay
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Memory.Unix > results[j].Memory.Unix
	})

	return results
}

// FilterByMinScore removes results below the minimum score threshold.
func FilterByMinScore(results []HybridResult, minScore float64) []HybridResult {
	if minScore <= 0 {
		return results
	}

	filtered := make([]HybridResult, 0, len(results))
	for _, r := range results {
		if r.Score >= minScore {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// LimitResults truncates results to the specified limit.
func LimitResults(results []HybridResult, limit int) []HybridResult {
	if limit <= 0 || len(results) <= limit {
		return results
	}
	return results[:limit]
}

// ── Full pipeline ───────────────────────────────────────────────────────────

// HybridSearchPipeline runs the full hybrid search pipeline.
func HybridSearchPipeline(
	vector []HybridVectorResult,
	keyword []HybridKeywordResult,
	cfg HybridSearchConfig,
	limit int,
) []IndexedMemory {
	// Merge results
	merged := MergeHybridResults(vector, keyword, cfg)

	// Apply temporal decay
	nowUnix := time.Now().Unix()
	merged = ApplyTemporalDecay(merged, cfg.TemporalDecay, nowUnix)

	// Apply MMR re-ranking if enabled
	if cfg.MMR.Enabled {
		// Convert to scored results for MMR
		scored := make([]ScoredIndexedMemory, len(merged))
		for i, r := range merged {
			scored[i] = ScoredIndexedMemory{Memory: r.Memory, Score: r.Score}
		}

		reranked := ApplyMMRToScoredResults(scored, cfg.MMR)

		// Convert back
		merged = make([]HybridResult, len(reranked))
		for i, r := range reranked {
			merged[i] = HybridResult{Memory: r.Memory, Score: r.Score}
		}
	}

	// Filter by min score
	merged = FilterByMinScore(merged, cfg.MinScore)

	// Limit results
	merged = LimitResults(merged, limit)

	// Extract memories
	memories := make([]IndexedMemory, len(merged))
	for i, r := range merged {
		memories[i] = r.Memory
	}

	return memories
}

// ── BM25 score conversion ───────────────────────────────────────────────────

// BM25RankToScore converts a BM25 rank (lower is better) to a score (higher is better).
// This normalizes the rank to a [0, 1] range suitable for merging with vector scores.
func BM25RankToScore(rank float64) float64 {
	if math.IsNaN(rank) || math.IsInf(rank, 0) {
		return 1.0 / (1.0 + 999)
	}
	if rank < 0 {
		// SQLite FTS5 bm25() returns negative values where lower (more negative) is better
		relevance := -rank
		return relevance / (1.0 + relevance)
	}
	return 1.0 / (1.0 + rank)
}

// ── Fallback modes ──────────────────────────────────────────────────────────

// SearchMode indicates which search method is being used.
type SearchMode string

const (
	SearchModeHybrid  SearchMode = "hybrid"  // Both vector and keyword
	SearchModeVector  SearchMode = "vector"  // Vector only (FTS unavailable)
	SearchModeKeyword SearchMode = "keyword" // Keyword only (vector unavailable)
	SearchModeNone    SearchMode = "none"    // Neither available
)

// DetermineSearchMode determines which search mode to use based on availability.
func DetermineSearchMode(vectorAvailable, ftsAvailable bool, cfg HybridSearchConfig) SearchMode {
	if !cfg.Enabled {
		if vectorAvailable {
			return SearchModeVector
		}
		if ftsAvailable {
			return SearchModeKeyword
		}
		return SearchModeNone
	}

	if vectorAvailable && ftsAvailable {
		return SearchModeHybrid
	}
	if vectorAvailable {
		return SearchModeVector
	}
	if ftsAvailable {
		return SearchModeKeyword
	}
	return SearchModeNone
}

// ── Score boosting for keyword-only mode ────────────────────────────────────

// BoostKeywordScore boosts a keyword-only score to be competitive with hybrid scores.
// This is used when vector search is unavailable.
func BoostKeywordScore(
	textScore float64,
	queryTokens []string,
	path string,
	text string,
) float64 {
	if len(queryTokens) == 0 {
		return textScore
	}

	// Count token overlap
	textTokens := Tokenize(text)
	pathLower := Tokenize(path)

	var overlap int
	for _, token := range queryTokens {
		if _, ok := textTokens[token]; ok {
			overlap++
		}
	}

	// Calculate boosts
	uniqueQueryOverlap := float64(overlap) / float64(len(queryTokens))
	density := float64(overlap) / math.Max(float64(len(textTokens)), 1)

	var pathBoost float64
	for _, token := range queryTokens {
		if _, ok := pathLower[token]; ok {
			pathBoost += 0.18
		}
	}

	textLengthBoost := math.Min(float64(len(text))/160, 0.18)

	lexicalBoost := uniqueQueryOverlap*0.45 + density*0.2 + pathBoost + textLengthBoost
	return math.Min(1.0, textScore+lexicalBoost)
}
