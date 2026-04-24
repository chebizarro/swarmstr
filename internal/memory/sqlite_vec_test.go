package memory

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// ── Vector Math Tests ───────────────────────────────────────────────────────

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float64
		delta    float64
	}{
		{
			name:     "identical vectors",
			a:        []float32{1, 0, 0},
			b:        []float32{1, 0, 0},
			expected: 1.0,
			delta:    0.001,
		},
		{
			name:     "opposite vectors",
			a:        []float32{1, 0, 0},
			b:        []float32{-1, 0, 0},
			expected: -1.0,
			delta:    0.001,
		},
		{
			name:     "orthogonal vectors",
			a:        []float32{1, 0, 0},
			b:        []float32{0, 1, 0},
			expected: 0.0,
			delta:    0.001,
		},
		{
			name:     "45 degree angle",
			a:        normalizeVector([]float32{1, 0, 0}),
			b:        normalizeVector([]float32{1, 1, 0}),
			expected: 0.707, // cos(45°) ≈ 0.707
			delta:    0.01,
		},
		{
			name:     "empty vectors",
			a:        []float32{},
			b:        []float32{},
			expected: 0.0,
			delta:    0.001,
		},
		{
			name:     "mismatched lengths",
			a:        []float32{1, 0},
			b:        []float32{1, 0, 0},
			expected: 0.0,
			delta:    0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cosineSimilarity(tt.a, tt.b)
			if math.Abs(result-tt.expected) > tt.delta {
				t.Errorf("cosineSimilarity() = %v, want %v (±%v)", result, tt.expected, tt.delta)
			}
		})
	}
}

func TestCosineSimilarityRaw(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float64
		delta    float64
	}{
		{
			name:     "non-normalized identical direction",
			a:        []float32{2, 0, 0},
			b:        []float32{5, 0, 0},
			expected: 1.0,
			delta:    0.001,
		},
		{
			name:     "non-normalized 45 degrees",
			a:        []float32{3, 0, 0},
			b:        []float32{4, 4, 0},
			expected: 0.707,
			delta:    0.01,
		},
		{
			name:     "zero vector",
			a:        []float32{0, 0, 0},
			b:        []float32{1, 0, 0},
			expected: 0.0,
			delta:    0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cosineSimilarityRaw(tt.a, tt.b)
			if math.Abs(result-tt.expected) > tt.delta {
				t.Errorf("cosineSimilarityRaw() = %v, want %v (±%v)", result, tt.expected, tt.delta)
			}
		})
	}
}

func TestDotProduct(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float64
	}{
		{
			name:     "simple dot product",
			a:        []float32{1, 2, 3},
			b:        []float32{4, 5, 6},
			expected: 32, // 1*4 + 2*5 + 3*6 = 32
		},
		{
			name:     "orthogonal",
			a:        []float32{1, 0, 0},
			b:        []float32{0, 1, 0},
			expected: 0,
		},
		{
			name:     "mismatched length",
			a:        []float32{1, 2},
			b:        []float32{1, 2, 3},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dotProduct(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("dotProduct() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestEuclideanDistance(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float64
		delta    float64
	}{
		{
			name:     "same point",
			a:        []float32{1, 2, 3},
			b:        []float32{1, 2, 3},
			expected: 0,
			delta:    0.001,
		},
		{
			name:     "unit distance along axis",
			a:        []float32{0, 0, 0},
			b:        []float32{1, 0, 0},
			expected: 1,
			delta:    0.001,
		},
		{
			name:     "3-4-5 triangle",
			a:        []float32{0, 0},
			b:        []float32{3, 4},
			expected: 5,
			delta:    0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := euclideanDistance(tt.a, tt.b)
			if math.Abs(result-tt.expected) > tt.delta {
				t.Errorf("euclideanDistance() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNormalizeVector(t *testing.T) {
	tests := []struct {
		name   string
		input  []float32
		isNil  bool
		length float64
	}{
		{
			name:   "unit vector stays same",
			input:  []float32{1, 0, 0},
			length: 1.0,
		},
		{
			name:   "scales down",
			input:  []float32{3, 4, 0},
			length: 1.0, // sqrt(9+16) = 5, normalized to 1
		},
		{
			name:   "3D vector",
			input:  []float32{1, 2, 2},
			length: 1.0, // sqrt(1+4+4) = 3, normalized to 1
		},
		{
			name:  "zero vector returns nil",
			input: []float32{0, 0, 0},
			isNil: true,
		},
		{
			name:  "empty vector returns nil",
			input: []float32{},
			isNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeVector(tt.input)
			if tt.isNil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("unexpected nil result")
			}
			mag := magnitude(result)
			if math.Abs(mag-tt.length) > 0.001 {
				t.Errorf("magnitude = %v, want %v", mag, tt.length)
			}
		})
	}
}

func TestMagnitude(t *testing.T) {
	tests := []struct {
		name     string
		input    []float32
		expected float64
		delta    float64
	}{
		{
			name:     "unit vector",
			input:    []float32{1, 0, 0},
			expected: 1.0,
			delta:    0.001,
		},
		{
			name:     "3-4-5 triangle",
			input:    []float32{3, 4},
			expected: 5.0,
			delta:    0.001,
		},
		{
			name:     "empty",
			input:    []float32{},
			expected: 0.0,
			delta:    0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := magnitude(tt.input)
			if math.Abs(result-tt.expected) > tt.delta {
				t.Errorf("magnitude() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// ── InMemoryVectorSearch Tests ──────────────────────────────────────────────

func TestInMemoryVectorSearch_AddAndSearch(t *testing.T) {
	// Use minScore=-1 to allow all results including orthogonal vectors
	vs := NewInMemoryVectorSearch(3, -1.0)

	// Add some vectors
	vs.Add("mem1", []float32{1, 0, 0})
	vs.Add("mem2", []float32{0, 1, 0})
	vs.Add("mem3", []float32{0, 0, 1})
	vs.Add("mem4", []float32{1, 1, 0}) // 45° from mem1

	if vs.Count() != 4 {
		t.Errorf("Count() = %d, want 4", vs.Count())
	}

	// Search for vector similar to mem1, request top 3
	results := vs.Search([]float32{1, 0, 0}, 3)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// First result should be mem1 (exact match)
	if results[0].MemoryID != "mem1" {
		t.Errorf("expected mem1 as top result, got %s", results[0].MemoryID)
	}
	if results[0].Similarity < 0.99 {
		t.Errorf("expected similarity ~1.0, got %v", results[0].Similarity)
	}

	// Second should be mem4 (45° angle = 0.707 similarity)
	if results[1].MemoryID != "mem4" {
		t.Errorf("expected mem4 as second result, got %s", results[1].MemoryID)
	}
}

func TestInMemoryVectorSearch_MinScore(t *testing.T) {
	vs := NewInMemoryVectorSearch(3, 0.8)

	vs.Add("high", []float32{1, 0, 0})
	vs.Add("medium", normalizeVector([]float32{1, 1, 0})) // 0.707 similarity
	vs.Add("low", []float32{0, 1, 0})                     // 0.0 similarity

	results := vs.Search([]float32{1, 0, 0}, 10)

	// Only high should pass the 0.8 threshold
	if len(results) != 1 {
		t.Errorf("expected 1 result above threshold, got %d", len(results))
	}
	if len(results) > 0 && results[0].MemoryID != "high" {
		t.Errorf("expected 'high' result, got %s", results[0].MemoryID)
	}
}

func TestInMemoryVectorSearch_Remove(t *testing.T) {
	vs := NewInMemoryVectorSearch(3, 0.0)

	vs.Add("mem1", []float32{1, 0, 0})
	vs.Add("mem2", []float32{0, 1, 0})

	if vs.Count() != 2 {
		t.Errorf("Count() = %d, want 2", vs.Count())
	}

	vs.Remove("mem1")

	if vs.Count() != 1 {
		t.Errorf("after remove, Count() = %d, want 1", vs.Count())
	}

	results := vs.Search([]float32{1, 0, 0}, 10)
	for _, r := range results {
		if r.MemoryID == "mem1" {
			t.Error("mem1 should have been removed")
		}
	}
}

func TestInMemoryVectorSearch_EmptyQueries(t *testing.T) {
	vs := NewInMemoryVectorSearch(3, 0.0)

	// Search empty index
	results := vs.Search([]float32{1, 0, 0}, 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty index, got %d", len(results))
	}

	// Empty query
	vs.Add("mem1", []float32{1, 0, 0})
	results = vs.Search([]float32{}, 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}

	// Zero limit
	results = vs.Search([]float32{1, 0, 0}, 0)
	if len(results) != 0 {
		t.Errorf("expected 0 results for limit=0, got %d", len(results))
	}
}

func TestInMemoryVectorSearch_Close(t *testing.T) {
	vs := NewInMemoryVectorSearch(3, 0.0)
	vs.Add("mem1", []float32{1, 0, 0})

	err := vs.Close()
	if err != nil {
		t.Errorf("Close() error: %v", err)
	}

	// After close, count should be 0
	if vs.Count() != 0 {
		t.Errorf("after Close(), Count() = %d, want 0", vs.Count())
	}
}

// ── SQLiteVectorSearch Tests ────────────────────────────────────────────────

func createTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	return db, dbPath
}

func TestSQLiteVectorSearch_Basic(t *testing.T) {
	db, _ := createTestDB(t)
	defer db.Close()

	cfg := DefaultVectorSearchConfig()
	cfg.Dims = 3
	cfg.MinScore = -1.0 // Allow all results including orthogonal

	vs, err := NewSQLiteVectorSearch(db, cfg)
	if err != nil {
		t.Fatalf("NewSQLiteVectorSearch: %v", err)
	}
	defer vs.Close()

	// Add vectors - all have some similarity with query
	vs.Add("mem1", []float32{1, 0, 0})             // similarity 1.0
	vs.Add("mem2", normalizeVector([]float32{1, 0.5, 0})) // similarity ~0.89
	vs.Add("mem3", normalizeVector([]float32{1, 1, 0}))   // similarity ~0.71

	if vs.Count() != 3 {
		t.Errorf("Count() = %d, want 3", vs.Count())
	}

	// Search
	results := vs.Search([]float32{1, 0, 0}, 3)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// First should be mem1 (exact match)
	if results[0].MemoryID != "mem1" {
		t.Errorf("expected mem1 as top result, got %s", results[0].MemoryID)
	}
}

func TestSQLiteVectorSearch_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "persist.sqlite")

	// Create and add vectors
	db1, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	cfg := DefaultVectorSearchConfig()
	cfg.Dims = 3
	cfg.MinScore = -1.0 // Allow all vectors

	vs1, err := NewSQLiteVectorSearch(db1, cfg)
	if err != nil {
		t.Fatalf("NewSQLiteVectorSearch: %v", err)
	}

	// Use vectors that both have similarity with query
	vs1.Add("mem1", []float32{1, 0, 0})
	vs1.Add("mem2", normalizeVector([]float32{1, 0.5, 0}))

	vs1.Close()
	db1.Close()

	// Reopen and verify persistence
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db2.Close()

	vs2, err := NewSQLiteVectorSearch(db2, cfg)
	if err != nil {
		t.Fatalf("NewSQLiteVectorSearch (reopen): %v", err)
	}
	defer vs2.Close()

	if vs2.Count() != 2 {
		t.Errorf("after reopen, Count() = %d, want 2", vs2.Count())
	}

	results := vs2.Search([]float32{1, 0, 0}, 2)
	if len(results) != 2 {
		t.Errorf("after reopen, expected 2 results, got %d", len(results))
	}
}

func TestSQLiteVectorSearch_Remove(t *testing.T) {
	db, _ := createTestDB(t)
	defer db.Close()

	cfg := DefaultVectorSearchConfig()
	cfg.Dims = 3
	cfg.MinScore = 0.0

	vs, err := NewSQLiteVectorSearch(db, cfg)
	if err != nil {
		t.Fatalf("NewSQLiteVectorSearch: %v", err)
	}
	defer vs.Close()

	vs.Add("mem1", []float32{1, 0, 0})
	vs.Add("mem2", []float32{0, 1, 0})

	vs.Remove("mem1")

	if vs.Count() != 1 {
		t.Errorf("after remove, Count() = %d, want 1", vs.Count())
	}

	// Verify it's also removed from SQLite
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM chunks_vec WHERE memory_id = ?`, "mem1").Scan(&count)
	if count != 0 {
		t.Error("mem1 should be removed from SQLite")
	}
}

func TestSQLiteVectorSearch_Reload(t *testing.T) {
	db, _ := createTestDB(t)
	defer db.Close()

	cfg := DefaultVectorSearchConfig()
	cfg.Dims = 3
	cfg.MinScore = 0.0

	vs, err := NewSQLiteVectorSearch(db, cfg)
	if err != nil {
		t.Fatalf("NewSQLiteVectorSearch: %v", err)
	}
	defer vs.Close()

	vs.Add("mem1", []float32{1, 0, 0})

	// Directly insert into SQLite (bypassing in-memory)
	db.Exec(`INSERT INTO chunks_vec (memory_id, embedding, dims) VALUES (?, ?, ?)`,
		"mem2", `[0,1,0]`, 3)

	// Before reload, mem2 shouldn't be in memory
	if vs.Count() != 1 {
		t.Errorf("before reload, Count() = %d, want 1", vs.Count())
	}

	// Reload
	err = vs.Reload()
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// After reload, should have both
	if vs.Count() != 2 {
		t.Errorf("after reload, Count() = %d, want 2", vs.Count())
	}
}

// ── VectorBackend Tests ─────────────────────────────────────────────────────

func TestVectorBackend_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "vector_backend.sqlite")

	cfg := DefaultVectorSearchConfig()
	cfg.Dims = 3
	cfg.MinScore = 0.0

	vb, err := NewVectorBackend(dbPath, cfg)
	if err != nil {
		t.Fatalf("NewVectorBackend: %v", err)
	}
	defer vb.Close()

	// Add memories with embeddings
	mem1 := IndexedMemory{
		MemoryID: "mem1",
		Text:     "First memory about cats",
		Unix:     1000,
	}
	vb.AddWithEmbedding(mem1, []float32{1, 0, 0})

	mem2 := IndexedMemory{
		MemoryID: "mem2",
		Text:     "Second memory about dogs",
		Unix:     2000,
	}
	vb.AddWithEmbedding(mem2, []float32{0, 1, 0})

	// Vector search
	results := vb.SearchVector([]float32{1, 0, 0}, 2)
	if len(results) == 0 {
		t.Fatal("expected results from vector search")
	}

	// Should find mem1 first
	found := false
	for _, r := range results {
		if r.MemoryID == "mem1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find mem1 in vector search results")
	}

	// Stats should include vector info
	stats := vb.VectorStats()
	if stats["vector_enabled"] != true {
		t.Error("expected vector_enabled=true in stats")
	}
	if stats["vector_count"].(int) != 2 {
		t.Errorf("expected vector_count=2, got %v", stats["vector_count"])
	}
}

func TestVectorBackend_DisabledVector(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "disabled.sqlite")

	cfg := DefaultVectorSearchConfig()
	cfg.Enabled = false

	vb, err := NewVectorBackend(dbPath, cfg)
	if err != nil {
		t.Fatalf("NewVectorBackend: %v", err)
	}
	defer vb.Close()

	// Add should still work (just no embedding indexing)
	mem := IndexedMemory{
		MemoryID: "mem1",
		Text:     "Test memory",
		Unix:     1000,
	}
	vb.AddWithEmbedding(mem, []float32{1, 0, 0})

	// Vector search should return empty when disabled
	results := vb.SearchVector([]float32{1, 0, 0}, 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results when vector disabled, got %d", len(results))
	}
}

// ── Quantization Tests ──────────────────────────────────────────────────────

func TestQuantizeVector(t *testing.T) {
	input := []float32{1.0, -0.5, 0.25, 0}
	quantized := QuantizeVector(input)

	if len(quantized) != len(input) {
		t.Fatalf("length mismatch: %d vs %d", len(quantized), len(input))
	}

	// Max value should map to 127
	if quantized[0] != 127 {
		t.Errorf("expected 127 for max value, got %d", quantized[0])
	}

	// -0.5 should map to approximately -64
	if quantized[1] > -60 || quantized[1] < -70 {
		t.Errorf("expected ~-64 for -0.5, got %d", quantized[1])
	}

	// 0 should stay 0
	if quantized[3] != 0 {
		t.Errorf("expected 0 for zero, got %d", quantized[3])
	}
}

func TestQuantizeDequantize_Roundtrip(t *testing.T) {
	input := []float32{1.0, -0.5, 0.25, 0}

	// Find max for scale
	maxAbs := float32(0)
	for _, x := range input {
		if abs := float32(math.Abs(float64(x))); abs > maxAbs {
			maxAbs = abs
		}
	}

	quantized := QuantizeVector(input)
	dequantized := DequantizeVector(quantized, maxAbs)

	if len(dequantized) != len(input) {
		t.Fatalf("length mismatch after roundtrip")
	}

	// Check values are approximately equal
	for i := range input {
		diff := math.Abs(float64(input[i] - dequantized[i]))
		if diff > 0.02 { // Allow small quantization error
			t.Errorf("value %d: input=%v, roundtrip=%v, diff=%v",
				i, input[i], dequantized[i], diff)
		}
	}
}

// ── Default Config Tests ────────────────────────────────────────────────────

func TestDefaultVectorSearchConfig(t *testing.T) {
	cfg := DefaultVectorSearchConfig()

	if !cfg.Enabled {
		t.Error("expected Enabled=true by default")
	}
	if cfg.Dims != 1536 {
		t.Errorf("expected Dims=1536, got %d", cfg.Dims)
	}
	if cfg.MinScore != 0.5 {
		t.Errorf("expected MinScore=0.5, got %v", cfg.MinScore)
	}
	if cfg.CandidateMultiplier != 2.0 {
		t.Errorf("expected CandidateMultiplier=2.0, got %v", cfg.CandidateMultiplier)
	}
	if cfg.UseExtension {
		t.Error("expected UseExtension=false by default")
	}
}

// ── Projection Tests ────────────────────────────────────────────────────────

func TestRandomProjection(t *testing.T) {
	inputDims := 100
	outputDims := 10

	proj := RandomProjection(inputDims, outputDims, 42)

	if len(proj) != outputDims {
		t.Errorf("expected %d rows, got %d", outputDims, len(proj))
	}
	for i, row := range proj {
		if len(row) != inputDims {
			t.Errorf("row %d: expected %d cols, got %d", i, inputDims, len(row))
		}
	}
}

func TestProjectVector(t *testing.T) {
	proj := [][]float32{
		{1, 0, 0},
		{0, 1, 0},
	}
	v := []float32{3, 4, 5}

	result := ProjectVector(v, proj)

	if len(result) != 2 {
		t.Fatalf("expected 2D result, got %d", len(result))
	}
	if result[0] != 3 {
		t.Errorf("expected result[0]=3, got %v", result[0])
	}
	if result[1] != 4 {
		t.Errorf("expected result[1]=4, got %v", result[1])
	}
}

func TestProjectVector_Empty(t *testing.T) {
	result := ProjectVector([]float32{1, 2, 3}, nil)
	if result != nil {
		t.Error("expected nil for empty projection")
	}

	result = ProjectVector(nil, [][]float32{{1, 0}})
	if result != nil {
		t.Error("expected nil for empty vector")
	}
}

// ── Benchmark Tests ─────────────────────────────────────────────────────────

func BenchmarkCosineSimilarity(b *testing.B) {
	dims := 1536
	a := make([]float32, dims)
	vec := make([]float32, dims)
	for i := range a {
		a[i] = float32(i) / float32(dims)
		vec[i] = float32(dims-i) / float32(dims)
	}
	a = normalizeVector(a)
	vec = normalizeVector(vec)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cosineSimilarity(a, vec)
	}
}

func BenchmarkInMemorySearch(b *testing.B) {
	dims := 1536
	vs := NewInMemoryVectorSearch(dims, 0.0)

	// Add 10k vectors
	for i := 0; i < 10000; i++ {
		emb := make([]float32, dims)
		for j := range emb {
			emb[j] = float32(i*j%100) / 100.0
		}
		vs.Add(fmt.Sprintf("mem%d", i), emb)
	}

	query := make([]float32, dims)
	for i := range query {
		query[i] = float32(i) / float32(dims)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vs.Search(query, 10)
	}
}

func BenchmarkNormalizeVector(b *testing.B) {
	dims := 1536
	v := make([]float32, dims)
	for i := range v {
		v[i] = float32(i) / float32(dims)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		normalizeVector(v)
	}
}

// ── Integration with state.MemoryDoc ────────────────────────────────────────

func TestMemoryDocFromIndexed(t *testing.T) {
	indexed := IndexedMemory{
		MemoryID:   "test-id",
		SessionID:  "session-123",
		Role:       "assistant",
		Topic:      "testing",
		Text:       "This is test content",
		Keywords:   []string{"test", "content"},
		Unix:       1234567890,
		Type:       "knowledge",
		Confidence: 0.9,
		Source:     "unit-test",
	}

	doc := memoryDocFromIndexed(indexed)

	if doc.MemoryID != indexed.MemoryID {
		t.Errorf("MemoryID mismatch: %s vs %s", doc.MemoryID, indexed.MemoryID)
	}
	if doc.SessionID != indexed.SessionID {
		t.Errorf("SessionID mismatch")
	}
	if doc.Text != indexed.Text {
		t.Errorf("Text mismatch")
	}
	if len(doc.Keywords) != len(indexed.Keywords) {
		t.Errorf("Keywords length mismatch")
	}
}

// ── Edge Cases ──────────────────────────────────────────────────────────────

func TestInMemoryVectorSearch_DuplicateAdd(t *testing.T) {
	vs := NewInMemoryVectorSearch(3, 0.0)

	vs.Add("mem1", []float32{1, 0, 0})
	vs.Add("mem1", []float32{0, 1, 0}) // Replace with different vector

	if vs.Count() != 1 {
		t.Errorf("expected 1 vector after duplicate add, got %d", vs.Count())
	}

	// Should find the second vector
	results := vs.Search([]float32{0, 1, 0}, 1)
	if len(results) == 0 {
		t.Fatal("expected to find result")
	}
	if results[0].Similarity < 0.99 {
		t.Errorf("expected high similarity with replaced vector, got %v", results[0].Similarity)
	}
}

func TestInMemoryVectorSearch_ErrorCases(t *testing.T) {
	vs := NewInMemoryVectorSearch(3, 0.0)

	// Empty memoryID
	err := vs.Add("", []float32{1, 0, 0})
	if err == nil {
		t.Error("expected error for empty memoryID")
	}

	// Empty embedding
	err = vs.Add("mem1", nil)
	if err == nil {
		t.Error("expected error for nil embedding")
	}

	err = vs.Add("mem1", []float32{})
	if err == nil {
		t.Error("expected error for empty embedding")
	}
}

// ── File cleanup for temp databases ─────────────────────────────────────────

func TestCleanupTempFiles(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cleanup_test.sqlite")

	cfg := DefaultVectorSearchConfig()
	cfg.Dims = 3

	vb, err := NewVectorBackend(dbPath, cfg)
	if err != nil {
		t.Fatalf("NewVectorBackend: %v", err)
	}

	vb.AddWithEmbedding(IndexedMemory{
		MemoryID: "test",
		Text:     "test",
		Unix:     1000,
	}, []float32{1, 0, 0})

	vb.Close()

	// File should exist after close (persistence)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file should exist after close")
	}
}
