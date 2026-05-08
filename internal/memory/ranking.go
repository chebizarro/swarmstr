package memory

import (
	"math"
	"strings"
	"time"
)

type MemoryRankingWeights struct {
	BM25       float64 `json:"bm25"`
	Recency    float64 `json:"recency"`
	Salience   float64 `json:"salience"`
	Confidence float64 `json:"confidence"`
	Pinned     float64 `json:"pinned"`
	Durable    float64 `json:"durable"`
	TypeMatch  float64 `json:"type_match"`
	ScopeMatch float64 `json:"scope_match"`
}

type MemoryRetrievalWhy struct {
	Intent           string             `json:"intent,omitempty"`
	BM25Rank         float64            `json:"bm25_rank,omitempty"`
	VectorRank       int                `json:"vector_rank,omitempty"`
	VectorSimilarity float64            `json:"vector_similarity,omitempty"`
	Components       map[string]float64 `json:"components,omitempty"`
	MatchedType      bool               `json:"matched_type,omitempty"`
	MatchedScope     bool               `json:"matched_scope,omitempty"`
	Pinned           bool               `json:"pinned,omitempty"`
	Durable          bool               `json:"durable,omitempty"`
	Reasons          []string           `json:"reasons,omitempty"`
}

type memoryRankedRecord struct {
	Record MemoryRecord
	Rank   float64
	Score  float64
	Why    MemoryRetrievalWhy
}

func DefaultMemoryRankingWeights() MemoryRankingWeights {
	return MemoryRankingWeights{
		BM25:       0.48,
		Recency:    0.10,
		Salience:   0.13,
		Confidence: 0.09,
		Pinned:     0.07,
		Durable:    0.05,
		TypeMatch:  0.05,
		ScopeMatch: 0.03,
	}
}

func normalizeRankingWeights(w *MemoryRankingWeights) MemoryRankingWeights {
	out := DefaultMemoryRankingWeights()
	if w == nil {
		return out
	}
	if w.BM25 >= 0 {
		out.BM25 = w.BM25
	}
	if w.Recency >= 0 {
		out.Recency = w.Recency
	}
	if w.Salience >= 0 {
		out.Salience = w.Salience
	}
	if w.Confidence >= 0 {
		out.Confidence = w.Confidence
	}
	if w.Pinned >= 0 {
		out.Pinned = w.Pinned
	}
	if w.Durable >= 0 {
		out.Durable = w.Durable
	}
	if w.TypeMatch >= 0 {
		out.TypeMatch = w.TypeMatch
	}
	if w.ScopeMatch >= 0 {
		out.ScopeMatch = w.ScopeMatch
	}
	return out
}

func rankMemoryRecords(records []MemoryRecord, ranks []float64, q MemoryQuery, intent QueryIntent, now time.Time) []memoryRankedRecord {
	weights := normalizeRankingWeights(q.RankingWeights)
	out := make([]memoryRankedRecord, 0, len(records))
	for i, rec := range records {
		rank := 0.0
		if i < len(ranks) {
			rank = ranks[i]
		}
		bm25 := BM25RankToScore(rank)
		if q.Query == "" || q.Mode == "recent" {
			bm25 = 0
		}
		recency := recencyScore(rec.UpdatedAt, now)
		salience := clamp01(rec.Salience)
		confidence := clamp01(rec.Confidence)
		pinned := boolScore(rec.Pinned)
		durable := boolScore(isDurableMemory(rec))
		typeMatch := boolScore(len(q.Types) == 0 || matchesFilter(rec.Type, q.Types))
		scopeMatch := boolScore(len(q.Scopes) == 0 || matchesFilter(rec.Scope, q.Scopes))
		components := map[string]float64{
			"bm25":        roundScore(bm25 * weights.BM25),
			"recency":     roundScore(recency * weights.Recency),
			"salience":    roundScore(salience * weights.Salience),
			"confidence":  roundScore(confidence * weights.Confidence),
			"pinned":      roundScore(pinned * weights.Pinned),
			"durable":     roundScore(durable * weights.Durable),
			"type_match":  roundScore(typeMatch * weights.TypeMatch),
			"scope_match": roundScore(scopeMatch * weights.ScopeMatch),
		}
		score := 0.0
		for _, v := range components {
			score += v
		}
		why := MemoryRetrievalWhy{
			Intent:       intent.Name,
			BM25Rank:     rank,
			Components:   components,
			MatchedType:  typeMatch > 0,
			MatchedScope: scopeMatch > 0,
			Pinned:       rec.Pinned,
			Durable:      durable > 0,
			Reasons:      rankingReasons(rec, q, intent, bm25, recency),
		}
		out = append(out, memoryRankedRecord{Record: rec, Rank: rank, Score: clamp01(score), Why: why})
	}
	return out
}

func isDurableMemory(rec MemoryRecord) bool {
	if rec.Pinned || rec.Source.Kind == MemorySourceKindFile || strings.TrimSpace(rec.Source.FilePath) != "" {
		return true
	}
	if rec.Metadata != nil {
		if v, ok := rec.Metadata["durable"].(bool); ok {
			return v
		}
		if s, ok := rec.Metadata["durable"].(string); ok {
			return strings.EqualFold(strings.TrimSpace(s), "true")
		}
	}
	return false
}

func recencyScore(updatedAt time.Time, now time.Time) float64 {
	if updatedAt.IsZero() {
		return 0
	}
	ageDays := now.Sub(updatedAt).Hours() / 24
	if ageDays <= 0 {
		return 1
	}
	return math.Pow(0.5, ageDays/30)
}

func rankingReasons(rec MemoryRecord, q MemoryQuery, intent QueryIntent, bm25, recency float64) []string {
	reasons := []string{}
	if intent.Name != "" && intent.Name != QueryIntentGeneral {
		reasons = append(reasons, "intent:"+intent.Name)
	}
	if bm25 > 0 {
		reasons = append(reasons, "lexical match")
	}
	if rec.Pinned {
		reasons = append(reasons, "pinned")
	}
	if isDurableMemory(rec) {
		reasons = append(reasons, "durable")
	}
	if recency > 0.75 {
		reasons = append(reasons, "recent")
	}
	if len(q.Types) > 0 && matchesFilter(rec.Type, q.Types) {
		reasons = append(reasons, "type match")
	}
	if len(q.Scopes) > 0 && matchesFilter(rec.Scope, q.Scopes) {
		reasons = append(reasons, "scope match")
	}
	return reasons
}

func boolScore(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func roundScore(v float64) float64 {
	return math.Round(v*1000000) / 1000000
}
