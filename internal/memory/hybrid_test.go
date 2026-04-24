package memory

import (
	"testing"
	"time"
)

func TestMergeHybridResults(t *testing.T) {
	cfg := DefaultHybridSearchConfig()

	vector := []HybridVectorResult{
		{Memory: IndexedMemory{MemoryID: "1", Text: "hello"}, Score: 0.9},
		{Memory: IndexedMemory{MemoryID: "2", Text: "world"}, Score: 0.8},
	}

	keyword := []HybridKeywordResult{
		{Memory: IndexedMemory{MemoryID: "2", Text: "world"}, Score: 0.7},
		{Memory: IndexedMemory{MemoryID: "3", Text: "test"}, Score: 0.6},
	}

	results := MergeHybridResults(vector, keyword, cfg)

	if len(results) != 3 {
		t.Fatalf("MergeHybridResults: got %d results, want 3", len(results))
	}

	// Check combined scores
	// Item 1: vectorWeight*0.9 + textWeight*0 = 0.7*0.9 = 0.63
	// Item 2: vectorWeight*0.8 + textWeight*0.7 = 0.7*0.8 + 0.3*0.7 = 0.56 + 0.21 = 0.77
	// Item 3: vectorWeight*0 + textWeight*0.6 = 0.3*0.6 = 0.18

	// Should be sorted by score: 2, 1, 3
	if results[0].Memory.MemoryID != "2" {
		t.Errorf("First result should be '2', got %q", results[0].Memory.MemoryID)
	}
	if results[1].Memory.MemoryID != "1" {
		t.Errorf("Second result should be '1', got %q", results[1].Memory.MemoryID)
	}
	if results[2].Memory.MemoryID != "3" {
		t.Errorf("Third result should be '3', got %q", results[2].Memory.MemoryID)
	}
}

func TestMergeHybridResults_Empty(t *testing.T) {
	cfg := DefaultHybridSearchConfig()

	results := MergeHybridResults(nil, nil, cfg)
	if len(results) != 0 {
		t.Errorf("MergeHybridResults empty: got %d results, want 0", len(results))
	}
}

func TestMergeHybridResults_VectorOnly(t *testing.T) {
	cfg := DefaultHybridSearchConfig()

	vector := []HybridVectorResult{
		{Memory: IndexedMemory{MemoryID: "1"}, Score: 0.9},
	}

	results := MergeHybridResults(vector, nil, cfg)

	if len(results) != 1 {
		t.Fatalf("MergeHybridResults vector-only: got %d results, want 1", len(results))
	}

	// Score should be vectorWeight * 0.9 = 0.63
	expectedScore := 0.7 * 0.9
	if abs(results[0].Score-expectedScore) > 0.01 {
		t.Errorf("Score: got %f, want %f", results[0].Score, expectedScore)
	}
}

func TestMergeHybridResults_KeywordOnly(t *testing.T) {
	cfg := DefaultHybridSearchConfig()

	keyword := []HybridKeywordResult{
		{Memory: IndexedMemory{MemoryID: "1"}, Score: 0.8},
	}

	results := MergeHybridResults(nil, keyword, cfg)

	if len(results) != 1 {
		t.Fatalf("MergeHybridResults keyword-only: got %d results, want 1", len(results))
	}

	// Score should be textWeight * 0.8 = 0.24
	expectedScore := 0.3 * 0.8
	if abs(results[0].Score-expectedScore) > 0.01 {
		t.Errorf("Score: got %f, want %f", results[0].Score, expectedScore)
	}
}

func TestApplyTemporalDecay(t *testing.T) {
	cfg := TemporalDecayConfig{
		Enabled:      true,
		HalfLifeDays: 30,
	}

	now := time.Now().Unix()
	halfLifeSec := int64(30 * 24 * 3600)

	results := []HybridResult{
		{Memory: IndexedMemory{MemoryID: "new", Unix: now}, Score: 1.0},
		{Memory: IndexedMemory{MemoryID: "old", Unix: now - halfLifeSec}, Score: 1.0},
		{Memory: IndexedMemory{MemoryID: "ancient", Unix: now - 2*halfLifeSec}, Score: 1.0},
	}

	decayed := ApplyTemporalDecay(results, cfg, now)

	// New item should have score ~1.0
	if decayed[0].Memory.MemoryID != "new" || abs(decayed[0].Score-1.0) > 0.01 {
		t.Errorf("New item: got score %f, want ~1.0", decayed[0].Score)
	}

	// Old item should have score ~0.5 (one half-life old)
	oldIdx := -1
	for i, r := range decayed {
		if r.Memory.MemoryID == "old" {
			oldIdx = i
			break
		}
	}
	if oldIdx < 0 || abs(decayed[oldIdx].Score-0.5) > 0.01 {
		t.Errorf("Old item: got score %f, want ~0.5", decayed[oldIdx].Score)
	}

	// Ancient item should have score ~0.25 (two half-lives old)
	ancientIdx := -1
	for i, r := range decayed {
		if r.Memory.MemoryID == "ancient" {
			ancientIdx = i
			break
		}
	}
	if ancientIdx < 0 || abs(decayed[ancientIdx].Score-0.25) > 0.01 {
		t.Errorf("Ancient item: got score %f, want ~0.25", decayed[ancientIdx].Score)
	}
}

func TestApplyTemporalDecay_Disabled(t *testing.T) {
	cfg := TemporalDecayConfig{Enabled: false}

	now := time.Now().Unix()
	results := []HybridResult{
		{Memory: IndexedMemory{MemoryID: "1", Unix: now - 86400*365}, Score: 1.0},
	}

	decayed := ApplyTemporalDecay(results, cfg, now)

	// Score should be unchanged when disabled
	if decayed[0].Score != 1.0 {
		t.Errorf("Disabled: score changed from 1.0 to %f", decayed[0].Score)
	}
}

func TestFilterByMinScore(t *testing.T) {
	results := []HybridResult{
		{Memory: IndexedMemory{MemoryID: "1"}, Score: 0.9},
		{Memory: IndexedMemory{MemoryID: "2"}, Score: 0.5},
		{Memory: IndexedMemory{MemoryID: "3"}, Score: 0.3},
	}

	filtered := FilterByMinScore(results, 0.4)

	if len(filtered) != 2 {
		t.Fatalf("FilterByMinScore: got %d results, want 2", len(filtered))
	}

	if filtered[0].Memory.MemoryID != "1" || filtered[1].Memory.MemoryID != "2" {
		t.Error("FilterByMinScore: wrong items kept")
	}
}

func TestLimitResults(t *testing.T) {
	results := []HybridResult{
		{Memory: IndexedMemory{MemoryID: "1"}},
		{Memory: IndexedMemory{MemoryID: "2"}},
		{Memory: IndexedMemory{MemoryID: "3"}},
	}

	limited := LimitResults(results, 2)

	if len(limited) != 2 {
		t.Errorf("LimitResults: got %d results, want 2", len(limited))
	}
}

func TestLimitResults_NoOp(t *testing.T) {
	results := []HybridResult{
		{Memory: IndexedMemory{MemoryID: "1"}},
	}

	limited := LimitResults(results, 10)

	if len(limited) != 1 {
		t.Errorf("LimitResults no-op: got %d results, want 1", len(limited))
	}
}

func TestHybridSearchPipeline(t *testing.T) {
	cfg := DefaultHybridSearchConfig()
	cfg.MinScore = 0.1 // Lower threshold for test

	vector := []HybridVectorResult{
		{Memory: IndexedMemory{MemoryID: "1", Text: "hello", Unix: 1000}, Score: 0.9},
		{Memory: IndexedMemory{MemoryID: "2", Text: "world", Unix: 1001}, Score: 0.8},
	}

	keyword := []HybridKeywordResult{
		{Memory: IndexedMemory{MemoryID: "2", Text: "world", Unix: 1001}, Score: 0.7},
		{Memory: IndexedMemory{MemoryID: "3", Text: "test", Unix: 1002}, Score: 0.6},
	}

	results := HybridSearchPipeline(vector, keyword, cfg, 10)

	if len(results) != 3 {
		t.Fatalf("HybridSearchPipeline: got %d results, want 3", len(results))
	}

	// Should be sorted by combined score
	if results[0].MemoryID != "2" {
		t.Errorf("First result should be '2', got %q", results[0].MemoryID)
	}
}

func TestHybridSearchPipeline_WithMMR(t *testing.T) {
	cfg := DefaultHybridSearchConfig()
	cfg.MinScore = 0.0
	cfg.MMR.Enabled = true
	cfg.MMR.Lambda = 0.5

	vector := []HybridVectorResult{
		{Memory: IndexedMemory{MemoryID: "1", Text: "golang programming", Unix: 1000}, Score: 0.9},
		{Memory: IndexedMemory{MemoryID: "2", Text: "golang tutorial", Unix: 1001}, Score: 0.85},
		{Memory: IndexedMemory{MemoryID: "3", Text: "python basics", Unix: 1002}, Score: 0.8},
	}

	results := HybridSearchPipeline(vector, nil, cfg, 10)

	if len(results) != 3 {
		t.Fatalf("HybridSearchPipeline with MMR: got %d results, want 3", len(results))
	}

	// With MMR enabled, diverse results should be promoted
	// All results should be present
	ids := map[string]bool{}
	for _, r := range results {
		ids[r.MemoryID] = true
	}
	for _, id := range []string{"1", "2", "3"} {
		if !ids[id] {
			t.Errorf("Missing result %q", id)
		}
	}
}

func TestBM25RankToScore(t *testing.T) {
	tests := []struct {
		rank     float64
		expected float64
	}{
		{-10.0, 10.0 / 11.0},  // High relevance (more negative)
		{-1.0, 0.5},          // Medium relevance
		{0.0, 1.0 / 1.0},     // Perfect match (rare)
		{1.0, 0.5},           // Low relevance (positive)
	}

	for _, tc := range tests {
		got := BM25RankToScore(tc.rank)
		if abs(got-tc.expected) > 0.01 {
			t.Errorf("BM25RankToScore(%f): got %f, want %f", tc.rank, got, tc.expected)
		}
	}
}

func TestDetermineSearchMode(t *testing.T) {
	cfg := DefaultHybridSearchConfig()

	tests := []struct {
		name     string
		vector   bool
		fts      bool
		enabled  bool
		expected SearchMode
	}{
		{"both available, hybrid enabled", true, true, true, SearchModeHybrid},
		{"both available, hybrid disabled", true, true, false, SearchModeVector},
		{"vector only", true, false, true, SearchModeVector},
		{"fts only", false, true, true, SearchModeKeyword},
		{"neither available", false, false, true, SearchModeNone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg.Enabled = tc.enabled
			got := DetermineSearchMode(tc.vector, tc.fts, cfg)
			if got != tc.expected {
				t.Errorf("DetermineSearchMode: got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestBoostKeywordScore(t *testing.T) {
	// Test with overlapping tokens
	tokens := []string{"golang", "programming"}
	score := BoostKeywordScore(0.5, tokens, "golang_tutorial.md", "golang programming language")

	if score <= 0.5 {
		t.Errorf("BoostKeywordScore: should increase score, got %f", score)
	}
	if score > 1.0 {
		t.Errorf("BoostKeywordScore: should cap at 1.0, got %f", score)
	}
}

func TestBoostKeywordScore_NoTokens(t *testing.T) {
	score := BoostKeywordScore(0.5, nil, "path", "text")

	if score != 0.5 {
		t.Errorf("BoostKeywordScore with no tokens: got %f, want 0.5", score)
	}
}

func TestDefaultHybridSearchConfig(t *testing.T) {
	cfg := DefaultHybridSearchConfig()

	if !cfg.Enabled {
		t.Error("DefaultHybridSearchConfig: Enabled should be true")
	}
	if cfg.VectorWeight != 0.7 {
		t.Errorf("DefaultHybridSearchConfig: VectorWeight should be 0.7, got %f", cfg.VectorWeight)
	}
	if cfg.TextWeight != 0.3 {
		t.Errorf("DefaultHybridSearchConfig: TextWeight should be 0.3, got %f", cfg.TextWeight)
	}
	if cfg.CandidateMultiplier != 4.0 {
		t.Errorf("DefaultHybridSearchConfig: CandidateMultiplier should be 4.0, got %f", cfg.CandidateMultiplier)
	}
}
