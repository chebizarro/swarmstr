// Package memory — Maximal Marginal Relevance (MMR) re-ranking for diversity.
//
// MMR balances relevance with diversity by iteratively selecting results
// that maximize: λ * relevance - (1-λ) * max_similarity_to_selected
//
// Reference: Carbonell & Goldstein, "The Use of MMR, Diversity-Based Reranking" (1998)
package memory

import (
	"strings"
	"unicode"
)

// MMRConfig configures the MMR re-ranking algorithm.
type MMRConfig struct {
	// Enabled toggles MMR re-ranking (default: false, opt-in).
	Enabled bool `json:"enabled"`

	// Lambda controls the relevance/diversity tradeoff.
	// 0 = max diversity, 1 = max relevance.
	// Default: 0.7 (favor relevance while adding diversity).
	Lambda float64 `json:"lambda"`
}

// DefaultMMRConfig returns sensible defaults for MMR re-ranking.
func DefaultMMRConfig() MMRConfig {
	return MMRConfig{
		Enabled: false,
		Lambda:  0.7,
	}
}

// MMRItem is the interface for items that can be re-ranked using MMR.
type MMRItem interface {
	// MMRScore returns the item's relevance score.
	MMRScore() float64
	// MMRContent returns the item's content for similarity comparison.
	MMRContent() string
	// MMRID returns a unique identifier for the item.
	MMRID() string
}

// cjkRanges defines Unicode ranges for CJK characters.
var cjkRanges = []*unicode.RangeTable{
	unicode.Han,      // Chinese hanzi, Japanese kanji, Korean hanja
	unicode.Hiragana, // Japanese hiragana
	unicode.Katakana, // Japanese katakana
	unicode.Hangul,   // Korean hangul
}

// isCJK returns true if the rune is a CJK character.
func isCJK(r rune) bool {
	for _, rt := range cjkRanges {
		if unicode.Is(rt, r) {
			return true
		}
	}
	return false
}

// Tokenize extracts tokens from text for Jaccard similarity computation.
// It handles:
//   - ASCII alphanumeric tokens (lowercased)
//   - CJK characters as unigrams
//   - Adjacent CJK character pairs as bigrams
func Tokenize(text string) map[string]struct{} {
	lower := strings.ToLower(text)
	tokens := make(map[string]struct{})

	// Extract ASCII tokens
	var current strings.Builder
	chars := []rune(lower)

	for i := 0; i < len(chars); i++ {
		r := chars[i]

		if isCJK(r) {
			// Flush any pending ASCII token
			if current.Len() > 0 {
				tokens[current.String()] = struct{}{}
				current.Reset()
			}

			// Add CJK unigram
			tokens[string(r)] = struct{}{}

			// Add bigram if next char is also CJK and adjacent
			if i+1 < len(chars) && isCJK(chars[i+1]) {
				tokens[string(r)+string(chars[i+1])] = struct{}{}
			}
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			current.WriteRune(r)
		} else {
			// Non-alphanumeric: flush token
			if current.Len() > 0 {
				tokens[current.String()] = struct{}{}
				current.Reset()
			}
		}
	}

	// Flush final token
	if current.Len() > 0 {
		tokens[current.String()] = struct{}{}
	}

	return tokens
}

// JaccardSimilarity computes the Jaccard similarity between two token sets.
// Returns a value in [0, 1] where 1 means identical sets.
func JaccardSimilarity(setA, setB map[string]struct{}) float64 {
	if len(setA) == 0 && len(setB) == 0 {
		return 1.0
	}
	if len(setA) == 0 || len(setB) == 0 {
		return 0.0
	}

	// Count intersection
	var intersectionSize int
	smaller, larger := setA, setB
	if len(setA) > len(setB) {
		smaller, larger = setB, setA
	}

	for token := range smaller {
		if _, ok := larger[token]; ok {
			intersectionSize++
		}
	}

	unionSize := len(setA) + len(setB) - intersectionSize
	if unionSize == 0 {
		return 0.0
	}

	return float64(intersectionSize) / float64(unionSize)
}

// TextSimilarity computes text similarity using Jaccard on tokens.
func TextSimilarity(contentA, contentB string) float64 {
	return JaccardSimilarity(Tokenize(contentA), Tokenize(contentB))
}

// maxSimilarityToSelected computes the maximum similarity between an item
// and all already-selected items.
func maxSimilarityToSelected(
	itemTokens map[string]struct{},
	selectedTokens []map[string]struct{},
) float64 {
	if len(selectedTokens) == 0 {
		return 0.0
	}

	var maxSim float64
	for _, selTokens := range selectedTokens {
		sim := JaccardSimilarity(itemTokens, selTokens)
		if sim > maxSim {
			maxSim = sim
		}
	}

	return maxSim
}

// computeMMRScore computes the MMR score for a candidate item.
// MMR = λ * relevance - (1-λ) * max_similarity_to_selected
func computeMMRScore(relevance, maxSimilarity, lambda float64) float64 {
	return lambda*relevance - (1-lambda)*maxSimilarity
}

// MMRRerank re-ranks items using Maximal Marginal Relevance.
//
// The algorithm iteratively selects items that balance relevance with diversity:
//  1. Start with the highest-scoring item
//  2. For each remaining slot, select the item that maximizes the MMR score
//  3. MMR score = λ * relevance - (1-λ) * max_similarity_to_already_selected
func MMRRerank[T MMRItem](items []T, cfg MMRConfig) []T {
	if !cfg.Enabled || len(items) <= 1 {
		return items
	}

	// Clamp lambda to valid range
	lambda := cfg.Lambda
	if lambda < 0 {
		lambda = 0
	} else if lambda > 1 {
		lambda = 1
	}

	// If lambda is 1, just return sorted by relevance (no diversity penalty)
	if lambda == 1 {
		// Already assumed sorted by score
		return items
	}

	// Pre-tokenize all items for efficiency
	tokenCache := make(map[string]map[string]struct{}, len(items))
	for _, item := range items {
		tokenCache[item.MMRID()] = Tokenize(item.MMRContent())
	}

	// Normalize scores to [0, 1] for fair comparison with similarity
	var minScore, maxScore float64 = items[0].MMRScore(), items[0].MMRScore()
	for _, item := range items[1:] {
		score := item.MMRScore()
		if score < minScore {
			minScore = score
		}
		if score > maxScore {
			maxScore = score
		}
	}
	scoreRange := maxScore - minScore

	normalizeScore := func(score float64) float64 {
		if scoreRange == 0 {
			return 1.0 // All scores equal
		}
		return (score - minScore) / scoreRange
	}

	// Track selected items and their tokens
	selected := make([]T, 0, len(items))
	selectedTokens := make([]map[string]struct{}, 0, len(items))
	remaining := make(map[string]T, len(items))
	for _, item := range items {
		remaining[item.MMRID()] = item
	}

	// Iteratively select items
	for len(remaining) > 0 {
		var bestID string
		var bestItem T
		bestMMRScore := -1e9

		for id, candidate := range remaining {
			normalizedRelevance := normalizeScore(candidate.MMRScore())
			candidateTokens := tokenCache[id]
			maxSim := maxSimilarityToSelected(candidateTokens, selectedTokens)
			mmrScore := computeMMRScore(normalizedRelevance, maxSim, lambda)

			// Use original score as tiebreaker (higher is better)
			if mmrScore > bestMMRScore ||
				(mmrScore == bestMMRScore && candidate.MMRScore() > bestItem.MMRScore()) {
				bestMMRScore = mmrScore
				bestID = id
				bestItem = candidate
			}
		}

		if bestID == "" {
			break // Should never happen
		}

		selected = append(selected, bestItem)
		selectedTokens = append(selectedTokens, tokenCache[bestID])
		delete(remaining, bestID)
	}

	return selected
}

// ── IndexedMemory MMR adapter ───────────────────────────────────────────────

// indexedMemoryMMR wraps IndexedMemory to implement MMRItem.
type indexedMemoryMMR struct {
	memory IndexedMemory
	score  float64
}

func (m indexedMemoryMMR) MMRScore() float64   { return m.score }
func (m indexedMemoryMMR) MMRContent() string  { return m.memory.Text }
func (m indexedMemoryMMR) MMRID() string       { return m.memory.MemoryID }

// ApplyMMRToSearchResults applies MMR re-ranking to memory search results.
// The score for each memory is computed based on its position in the original
// results (higher position = higher score).
func ApplyMMRToSearchResults(results []IndexedMemory, cfg MMRConfig) []IndexedMemory {
	if !cfg.Enabled || len(results) <= 1 {
		return results
	}

	// Wrap memories with position-based scores
	items := make([]indexedMemoryMMR, len(results))
	for i, m := range results {
		// Score based on position: first item gets score 1.0, last gets ~0
		score := 1.0 - float64(i)/float64(len(results))
		items[i] = indexedMemoryMMR{memory: m, score: score}
	}

	// Apply MMR
	reranked := MMRRerank(items, cfg)

	// Extract memories
	output := make([]IndexedMemory, len(reranked))
	for i, item := range reranked {
		output[i] = item.memory
	}

	return output
}

// ScoredIndexedMemory pairs an IndexedMemory with an explicit score.
type ScoredIndexedMemory struct {
	Memory IndexedMemory
	Score  float64
}

func (m ScoredIndexedMemory) MMRScore() float64   { return m.Score }
func (m ScoredIndexedMemory) MMRContent() string  { return m.Memory.Text }
func (m ScoredIndexedMemory) MMRID() string       { return m.Memory.MemoryID }

// ApplyMMRToScoredResults applies MMR re-ranking to scored memory results.
func ApplyMMRToScoredResults(results []ScoredIndexedMemory, cfg MMRConfig) []ScoredIndexedMemory {
	if !cfg.Enabled || len(results) <= 1 {
		return results
	}
	return MMRRerank(results, cfg)
}
