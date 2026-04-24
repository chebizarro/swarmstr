package memory

import (
	"testing"
)

func TestTokenize_ASCII(t *testing.T) {
	tokens := Tokenize("Hello World, this is a test!")
	
	expected := map[string]struct{}{
		"hello": {},
		"world": {},
		"this":  {},
		"is":    {},
		"a":     {},
		"test":  {},
	}
	
	if len(tokens) != len(expected) {
		t.Errorf("Tokenize: got %d tokens, want %d", len(tokens), len(expected))
	}
	
	for tok := range expected {
		if _, ok := tokens[tok]; !ok {
			t.Errorf("Tokenize: missing token %q", tok)
		}
	}
}

func TestTokenize_CJK(t *testing.T) {
	// Chinese text: "我喜欢编程" (I like programming)
	tokens := Tokenize("我喜欢编程")
	
	// Should contain unigrams
	for _, char := range []string{"我", "喜", "欢", "编", "程"} {
		if _, ok := tokens[char]; !ok {
			t.Errorf("Tokenize CJK: missing unigram %q", char)
		}
	}
	
	// Should contain bigrams for adjacent chars
	expectedBigrams := []string{"我喜", "喜欢", "欢编", "编程"}
	for _, bigram := range expectedBigrams {
		if _, ok := tokens[bigram]; !ok {
			t.Errorf("Tokenize CJK: missing bigram %q", bigram)
		}
	}
}

func TestTokenize_Mixed(t *testing.T) {
	// Mixed content: "Hello世界"
	tokens := Tokenize("Hello世界")
	
	// Should have ASCII token
	if _, ok := tokens["hello"]; !ok {
		t.Error("Tokenize mixed: missing 'hello'")
	}
	
	// Should have CJK unigrams
	if _, ok := tokens["世"]; !ok {
		t.Error("Tokenize mixed: missing '世'")
	}
	if _, ok := tokens["界"]; !ok {
		t.Error("Tokenize mixed: missing '界'")
	}
	
	// Should have CJK bigram
	if _, ok := tokens["世界"]; !ok {
		t.Error("Tokenize mixed: missing '世界'")
	}
}

func TestTokenize_NoBigramAcrossNonCJK(t *testing.T) {
	// "我hello你" should NOT produce bigram "我你"
	tokens := Tokenize("我hello你")
	
	if _, ok := tokens["我你"]; ok {
		t.Error("Tokenize: should not produce bigram across non-CJK text")
	}
}

func TestJaccardSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a, b     map[string]struct{}
		expected float64
	}{
		{
			name:     "identical",
			a:        map[string]struct{}{"a": {}, "b": {}, "c": {}},
			b:        map[string]struct{}{"a": {}, "b": {}, "c": {}},
			expected: 1.0,
		},
		{
			name:     "disjoint",
			a:        map[string]struct{}{"a": {}, "b": {}},
			b:        map[string]struct{}{"c": {}, "d": {}},
			expected: 0.0,
		},
		{
			name:     "half overlap",
			a:        map[string]struct{}{"a": {}, "b": {}},
			b:        map[string]struct{}{"b": {}, "c": {}},
			expected: 1.0 / 3.0, // intersection=1, union=3
		},
		{
			name:     "both empty",
			a:        map[string]struct{}{},
			b:        map[string]struct{}{},
			expected: 1.0,
		},
		{
			name:     "one empty",
			a:        map[string]struct{}{"a": {}},
			b:        map[string]struct{}{},
			expected: 0.0,
		},
	}
	
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := JaccardSimilarity(tc.a, tc.b)
			if abs(got-tc.expected) > 0.001 {
				t.Errorf("JaccardSimilarity: got %f, want %f", got, tc.expected)
			}
		})
	}
}

func TestTextSimilarity(t *testing.T) {
	// Identical text should have similarity 1.0
	sim := TextSimilarity("hello world", "hello world")
	if abs(sim-1.0) > 0.001 {
		t.Errorf("TextSimilarity identical: got %f, want 1.0", sim)
	}
	
	// Completely different text should have low similarity
	sim = TextSimilarity("hello world", "foo bar baz")
	if sim > 0.1 {
		t.Errorf("TextSimilarity different: got %f, want < 0.1", sim)
	}
	
	// Partial overlap
	sim = TextSimilarity("hello world test", "hello world foo")
	if sim < 0.4 || sim > 0.8 {
		t.Errorf("TextSimilarity partial: got %f, want between 0.4 and 0.8", sim)
	}
}

// mockMMRItem implements MMRItem for testing.
type mockMMRItem struct {
	id      string
	score   float64
	content string
}

func (m mockMMRItem) MMRID() string       { return m.id }
func (m mockMMRItem) MMRScore() float64   { return m.score }
func (m mockMMRItem) MMRContent() string  { return m.content }

func TestMMRRerank_Disabled(t *testing.T) {
	items := []mockMMRItem{
		{id: "1", score: 1.0, content: "hello"},
		{id: "2", score: 0.9, content: "world"},
	}
	
	cfg := MMRConfig{Enabled: false, Lambda: 0.7}
	result := MMRRerank(items, cfg)
	
	if len(result) != 2 {
		t.Fatalf("MMRRerank disabled: got %d items, want 2", len(result))
	}
	
	// Should be unchanged
	if result[0].id != "1" || result[1].id != "2" {
		t.Error("MMRRerank disabled: should not change order")
	}
}

func TestMMRRerank_SingleItem(t *testing.T) {
	items := []mockMMRItem{
		{id: "1", score: 1.0, content: "hello"},
	}
	
	cfg := MMRConfig{Enabled: true, Lambda: 0.7}
	result := MMRRerank(items, cfg)
	
	if len(result) != 1 {
		t.Fatalf("MMRRerank single: got %d items, want 1", len(result))
	}
}

func TestMMRRerank_DiversityEffect(t *testing.T) {
	// Three items: two similar, one different
	items := []mockMMRItem{
		{id: "1", score: 1.0, content: "the quick brown fox jumps"},
		{id: "2", score: 0.95, content: "the quick brown fox leaps"},  // Similar to 1
		{id: "3", score: 0.9, content: "hello world testing different"},  // Different
	}
	
	// With diversity enabled (lambda < 1), the different item should be promoted
	cfg := MMRConfig{Enabled: true, Lambda: 0.5}
	result := MMRRerank(items, cfg)
	
	if len(result) != 3 {
		t.Fatalf("MMRRerank diversity: got %d items, want 3", len(result))
	}
	
	// First item should still be highest scored
	if result[0].id != "1" {
		t.Errorf("MMRRerank diversity: first item should be '1', got %q", result[0].id)
	}
	
	// Second should be the different one (item 3) due to diversity
	if result[1].id != "3" {
		t.Errorf("MMRRerank diversity: second item should be '3' (diverse), got %q", result[1].id)
	}
}

func TestMMRRerank_LambdaOne(t *testing.T) {
	// With lambda=1, should just return by relevance (no diversity penalty)
	items := []mockMMRItem{
		{id: "1", score: 1.0, content: "hello hello hello"},
		{id: "2", score: 0.9, content: "hello hello hello"},  // Same content!
		{id: "3", score: 0.8, content: "hello hello hello"},
	}
	
	cfg := MMRConfig{Enabled: true, Lambda: 1.0}
	result := MMRRerank(items, cfg)
	
	// Should maintain relevance order despite identical content
	if result[0].id != "1" || result[1].id != "2" || result[2].id != "3" {
		t.Error("MMRRerank lambda=1: should maintain relevance order")
	}
}

func TestMMRRerank_LambdaZero(t *testing.T) {
	// With lambda=0, should maximize diversity (ignore relevance)
	items := []mockMMRItem{
		{id: "1", score: 1.0, content: "aaa bbb ccc"},
		{id: "2", score: 0.9, content: "aaa bbb ccc"},  // Identical to 1
		{id: "3", score: 0.8, content: "xxx yyy zzz"},  // Different
	}
	
	cfg := MMRConfig{Enabled: true, Lambda: 0.0}
	result := MMRRerank(items, cfg)
	
	// Item 3 should be selected early due to diversity
	// First is still 1 (highest score with no penalty yet)
	if result[0].id != "1" {
		t.Errorf("MMRRerank lambda=0: first should be '1', got %q", result[0].id)
	}
	
	// Second should be 3 (most diverse from 1)
	if result[1].id != "3" {
		t.Errorf("MMRRerank lambda=0: second should be '3', got %q", result[1].id)
	}
}

func TestApplyMMRToSearchResults(t *testing.T) {
	results := []IndexedMemory{
		{MemoryID: "1", Text: "golang programming language"},
		{MemoryID: "2", Text: "golang programming tutorial"},  // Very similar to 1
		{MemoryID: "3", Text: "python machine learning"},      // Different from 1
	}
	
	cfg := MMRConfig{Enabled: true, Lambda: 0.5}
	reranked := ApplyMMRToSearchResults(results, cfg)
	
	if len(reranked) != 3 {
		t.Fatalf("ApplyMMRToSearchResults: got %d results, want 3", len(reranked))
	}
	
	// First should still be first (highest implicit score)
	if reranked[0].MemoryID != "1" {
		t.Errorf("ApplyMMRToSearchResults: first should be '1', got %q", reranked[0].MemoryID)
	}
	
	// All 3 results should be present
	ids := map[string]bool{}
	for _, r := range reranked {
		ids[r.MemoryID] = true
	}
	for _, id := range []string{"1", "2", "3"} {
		if !ids[id] {
			t.Errorf("ApplyMMRToSearchResults: missing result %q", id)
		}
	}
}

func TestApplyMMRToSearchResults_Disabled(t *testing.T) {
	results := []IndexedMemory{
		{MemoryID: "1", Text: "hello"},
		{MemoryID: "2", Text: "world"},
	}
	
	cfg := MMRConfig{Enabled: false, Lambda: 0.5}
	reranked := ApplyMMRToSearchResults(results, cfg)
	
	if len(reranked) != 2 {
		t.Fatalf("ApplyMMRToSearchResults disabled: got %d results, want 2", len(reranked))
	}
	
	// Should be unchanged
	if reranked[0].MemoryID != "1" || reranked[1].MemoryID != "2" {
		t.Error("ApplyMMRToSearchResults disabled: should not change order")
	}
}

func TestApplyMMRToScoredResults(t *testing.T) {
	results := []ScoredIndexedMemory{
		{Memory: IndexedMemory{MemoryID: "1", Text: "golang tutorial"}, Score: 0.9},
		{Memory: IndexedMemory{MemoryID: "2", Text: "golang guide"}, Score: 0.85},     // Similar
		{Memory: IndexedMemory{MemoryID: "3", Text: "python basics"}, Score: 0.8},     // Different
	}
	
	cfg := MMRConfig{Enabled: true, Lambda: 0.5}
	reranked := ApplyMMRToScoredResults(results, cfg)
	
	if len(reranked) != 3 {
		t.Fatalf("ApplyMMRToScoredResults: got %d results, want 3", len(reranked))
	}
	
	// First should still be highest scored
	if reranked[0].Memory.MemoryID != "1" {
		t.Errorf("ApplyMMRToScoredResults: first should be '1', got %q", reranked[0].Memory.MemoryID)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
