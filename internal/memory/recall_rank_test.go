package memory

import (
	"testing"
	"time"

	"metiq/internal/store/state"
)

func TestRankRecallResults_Empty(t *testing.T) {
	results := RankRecallResults(nil, DefaultRecallPolicy(), 10, time.Now().Unix())
	if len(results) != 0 {
		t.Fatalf("expected empty, got %d", len(results))
	}
}

func TestRankRecallResults_FiltersExpired(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		{MemoryID: "expired", Text: "old", ExpiresAt: now - 3600, Unix: now - 7200},
		{MemoryID: "valid", Text: "good", Unix: now - 100},
	}
	results := RankRecallResults(items, DefaultRecallPolicy(), 10, now)
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	if results[0].Memory.MemoryID != "valid" {
		t.Errorf("expected valid, got %q", results[0].Memory.MemoryID)
	}
}

func TestRankRecallResults_FiltersInvalidated(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		{MemoryID: "stale", Text: "old info", MemStatus: state.MemStatusStale, Unix: now},
		{MemoryID: "superseded", Text: "replaced", MemStatus: state.MemStatusSuperseded, Unix: now},
		{MemoryID: "contradicted", Text: "wrong", MemStatus: state.MemStatusContradicted, Unix: now},
		{MemoryID: "active-explicit", Text: "ok", MemStatus: state.MemStatusActive, Unix: now},
		{MemoryID: "active-empty", Text: "also ok", Unix: now},
	}
	results := RankRecallResults(items, DefaultRecallPolicy(), 10, now)
	if len(results) != 2 {
		t.Fatalf("expected 2 active, got %d", len(results))
	}
	ids := map[string]bool{}
	for _, r := range results {
		ids[r.Memory.MemoryID] = true
	}
	if !ids["active-explicit"] || !ids["active-empty"] {
		t.Errorf("expected active-explicit and active-empty, got %v", ids)
	}
}

func TestRankRecallResults_PrefersHighConfidence(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		{MemoryID: "low", Text: "low conf", Confidence: 0.2, Unix: now},
		{MemoryID: "high", Text: "high conf", Confidence: 0.95, Unix: now},
	}
	policy := DefaultRecallPolicy()
	// Increase confidence weight, zero others for isolation.
	policy.WeightConfidence = 1.0
	policy.WeightReviewed = 0
	policy.WeightRecency = 0
	policy.WeightEpisodic = 0

	results := RankRecallResults(items, policy, 10, now)
	if len(results) != 2 {
		t.Fatalf("expected 2, got %d", len(results))
	}
	if results[0].Memory.MemoryID != "high" {
		t.Errorf("expected high confidence first, got %q", results[0].Memory.MemoryID)
	}
}

func TestRankRecallResults_PrefersReviewed(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		{MemoryID: "unreviewed", Text: "not reviewed", Confidence: 0.5, Unix: now},
		{MemoryID: "reviewed", Text: "reviewed", Confidence: 0.5, ReviewedAt: now - 100, Unix: now},
	}
	policy := DefaultRecallPolicy()
	policy.WeightConfidence = 0
	policy.WeightReviewed = 1.0
	policy.WeightRecency = 0
	policy.WeightEpisodic = 0

	results := RankRecallResults(items, policy, 10, now)
	if results[0].Memory.MemoryID != "reviewed" {
		t.Errorf("expected reviewed first, got %q", results[0].Memory.MemoryID)
	}
}

func TestRankRecallResults_PrefersRecent(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		{MemoryID: "old", Text: "old", Confidence: 0.5, Unix: now - 90*86400}, // 90 days
		{MemoryID: "new", Text: "new", Confidence: 0.5, Unix: now - 3600},     // 1 hour
	}
	policy := DefaultRecallPolicy()
	policy.WeightConfidence = 0
	policy.WeightReviewed = 0
	policy.WeightRecency = 1.0
	policy.WeightEpisodic = 0

	results := RankRecallResults(items, policy, 10, now)
	if results[0].Memory.MemoryID != "new" {
		t.Errorf("expected recent first, got %q (score=%.4f)", results[0].Memory.MemoryID, results[0].Score)
	}
	// Recent should have much higher score.
	if results[0].Score <= results[1].Score {
		t.Errorf("recent score %.4f should be > old score %.4f", results[0].Score, results[1].Score)
	}
}

func TestRankRecallResults_PrefersEpisodic(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		{MemoryID: "fact", Text: "fact", Type: "fact", Confidence: 0.5, Unix: now},
		{MemoryID: "ep", Text: "episodic", Type: "episodic", Confidence: 0.5, Unix: now},
	}
	policy := DefaultRecallPolicy()
	policy.WeightConfidence = 0
	policy.WeightReviewed = 0
	policy.WeightRecency = 0
	policy.WeightEpisodic = 1.0

	results := RankRecallResults(items, policy, 10, now)
	if results[0].Memory.MemoryID != "ep" {
		t.Errorf("expected episodic first, got %q", results[0].Memory.MemoryID)
	}
}

func TestRankRecallResults_CompositeScoring(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		// High confidence, not reviewed, old, fact.
		{MemoryID: "a", Text: "a", Confidence: 0.9, Unix: now - 60*86400},
		// Medium confidence, reviewed, recent, episodic.
		{MemoryID: "b", Text: "b", Confidence: 0.6, ReviewedAt: now - 100, Type: "episodic", Unix: now - 3600},
	}
	policy := DefaultRecallPolicy()
	results := RankRecallResults(items, policy, 10, now)
	if len(results) != 2 {
		t.Fatalf("expected 2, got %d", len(results))
	}
	// "b" should win: it has reviewed + recent + episodic bonuses.
	if results[0].Memory.MemoryID != "b" {
		t.Errorf("expected b first (composite win), got %q (scores: b=%.4f, a=%.4f)",
			results[0].Memory.MemoryID, results[0].Score, results[1].Score)
	}
}

func TestRankRecallResults_Limit(t *testing.T) {
	now := time.Now().Unix()
	items := make([]IndexedMemory, 20)
	for i := range items {
		items[i] = IndexedMemory{
			MemoryID: GenerateMemoryID(),
			Text:     "entry",
			Unix:     now - int64(i*3600),
		}
	}
	results := RankRecallResults(items, DefaultRecallPolicy(), 5, now)
	if len(results) != 5 {
		t.Fatalf("expected 5, got %d", len(results))
	}
}

func TestRankRecallResults_DefaultConfidenceForUnset(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		{MemoryID: "unset", Text: "no confidence", Unix: now},
	}
	policy := RecallPolicy{WeightConfidence: 1.0}
	results := RankRecallResults(items, policy, 10, now)
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	// Score should be 0.5 * 1.0 = 0.5 (default confidence).
	if results[0].Score < 0.49 || results[0].Score > 0.51 {
		t.Errorf("expected score ~0.5 for unset confidence, got %.4f", results[0].Score)
	}
}

func TestRankRecallResults_StableSortOnTie(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		{MemoryID: "b", Text: "b", Confidence: 0.5, Unix: now},
		{MemoryID: "a", Text: "a", Confidence: 0.5, Unix: now},
		{MemoryID: "c", Text: "c", Confidence: 0.5, Unix: now},
	}
	policy := DefaultRecallPolicy()
	results := RankRecallResults(items, policy, 10, now)
	// Same score + same unix → sort by MemoryID ascending.
	if results[0].Memory.MemoryID != "a" || results[1].Memory.MemoryID != "b" || results[2].Memory.MemoryID != "c" {
		t.Errorf("expected stable a,b,c order, got %q,%q,%q",
			results[0].Memory.MemoryID, results[1].Memory.MemoryID, results[2].Memory.MemoryID)
	}
}

func TestRankRecallResults_DisableFilters(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		{MemoryID: "expired", Text: "expired", ExpiresAt: now - 1, Unix: now},
		{MemoryID: "stale", Text: "stale", MemStatus: state.MemStatusStale, Unix: now},
	}
	policy := DefaultRecallPolicy()
	policy.FilterExpired = false
	policy.FilterInvalidated = false

	results := RankRecallResults(items, policy, 10, now)
	if len(results) != 2 {
		t.Fatalf("expected 2 (filters disabled), got %d", len(results))
	}
}

func TestFilterActiveMemories(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		{MemoryID: "active", Text: "active", Unix: now},
		{MemoryID: "expired", Text: "expired", ExpiresAt: now - 1, Unix: now},
		{MemoryID: "stale", Text: "stale", MemStatus: state.MemStatusStale, Unix: now},
		{MemoryID: "superseded", Text: "superseded", MemStatus: state.MemStatusSuperseded, Unix: now},
		{MemoryID: "explicit-active", Text: "explicit", MemStatus: state.MemStatusActive, Unix: now},
		{MemoryID: "future-expiry", Text: "future", ExpiresAt: now + 86400, Unix: now},
	}
	result := FilterActiveMemories(items, now)
	if len(result) != 3 {
		t.Fatalf("expected 3 active, got %d", len(result))
	}
	ids := map[string]bool{}
	for _, r := range result {
		ids[r.MemoryID] = true
	}
	if !ids["active"] || !ids["explicit-active"] || !ids["future-expiry"] {
		t.Errorf("unexpected results: %v", ids)
	}
}

func TestExp2Decay(t *testing.T) {
	// x=0 → 1.0
	if v := exp2Decay(0); v != 1.0 {
		t.Errorf("exp2Decay(0) = %v, want 1.0", v)
	}
	// x=1 → ~0.5
	if v := exp2Decay(1); v < 0.49 || v > 0.51 {
		t.Errorf("exp2Decay(1) = %v, want ~0.5", v)
	}
	// x=2 → ~0.25
	if v := exp2Decay(2); v < 0.24 || v > 0.26 {
		t.Errorf("exp2Decay(2) = %v, want ~0.25", v)
	}
	// x=100 → 0
	if v := exp2Decay(100); v != 0 {
		t.Errorf("exp2Decay(100) = %v, want 0", v)
	}
}
