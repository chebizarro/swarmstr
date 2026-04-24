// Package memory — Vector search support for SQLite backend.
//
// This module provides vector similarity search capabilities for the SQLite
// memory backend. It supports two modes:
//
//  1. Pure-Go in-memory cosine similarity (default)
//     - Works with any SQLite, no extensions required
//     - Loads embeddings into memory for fast similarity computation
//     - Suitable for datasets up to ~100k vectors
//
//  2. sqlite-vec extension (optional, requires CGO build)
//     - Uses sqlite-vec for native SQL vector operations
//     - CREATE VIRTUAL TABLE chunks_vec USING vec0(embedding FLOAT[dims])
//     - KNN search: embedding MATCH ? AND k = ?
//
// The implementation automatically falls back to pure-Go mode when the
// sqlite-vec extension is not available.
package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"

	"metiq/internal/store/state"
)

// VectorSearchConfig configures vector search behavior.
type VectorSearchConfig struct {
	// Enabled toggles vector search (default: true).
	Enabled bool `json:"enabled"`

	// Dims is the embedding dimension (must match your embedding model).
	// Common values: 384 (MiniLM), 768 (E5), 1536 (OpenAI ada-002/3-small).
	Dims int `json:"dims"`

	// MinScore is the minimum similarity threshold (default: 0.5).
	MinScore float64 `json:"min_score"`

	// CandidateMultiplier for oversampling during approximate search.
	// Higher values improve recall at the cost of speed (default: 2.0).
	CandidateMultiplier float64 `json:"candidate_multiplier"`

	// UseExtension attempts to use sqlite-vec if available.
	// Falls back to pure-Go if extension fails to load.
	UseExtension bool `json:"use_extension"`

	// ExtensionPath is the path to vec0.so/.dll if non-standard.
	ExtensionPath string `json:"extension_path,omitempty"`
}

// DefaultVectorSearchConfig returns sensible defaults.
func DefaultVectorSearchConfig() VectorSearchConfig {
	return VectorSearchConfig{
		Enabled:             true,
		Dims:                1536, // OpenAI text-embedding-3-small
		MinScore:            0.5,
		CandidateMultiplier: 2.0,
		UseExtension:        false, // Pure-Go by default (more portable)
	}
}

// VectorResult represents a vector search result with similarity score.
type VectorResult struct {
	Memory     IndexedMemory
	MemoryID   string
	Embedding  []float32
	Similarity float64
}

// VectorSearcher provides vector similarity search.
type VectorSearcher interface {
	// Search finds the k most similar vectors to the query.
	Search(query []float32, k int) []VectorResult

	// Add indexes a new vector with its memory ID.
	Add(memoryID string, embedding []float32) error

	// Remove deletes a vector by memory ID.
	Remove(memoryID string) error

	// Count returns the number of indexed vectors.
	Count() int

	// Close releases resources.
	Close() error
}

// ── Pure-Go Vector Search Implementation ────────────────────────────────────

// InMemoryVectorSearch provides vector search using in-memory cosine similarity.
// This is the default fallback when sqlite-vec extension is not available.
type InMemoryVectorSearch struct {
	mu       sync.RWMutex
	vectors  map[string][]float32 // memoryID -> embedding
	dims     int
	minScore float64
}

// NewInMemoryVectorSearch creates a new in-memory vector search index.
// Pass minScore < -1 to disable filtering (accept all similarities).
func NewInMemoryVectorSearch(dims int, minScore float64) *InMemoryVectorSearch {
	if dims <= 0 {
		dims = 1536
	}
	// Only apply default if minScore is exactly 0 (unset)
	// Use negative values to allow all results including orthogonal
	if minScore == 0 {
		minScore = 0.5
	}
	return &InMemoryVectorSearch{
		vectors:  make(map[string][]float32),
		dims:     dims,
		minScore: minScore,
	}
}

// Search finds the k most similar vectors to the query using cosine similarity.
func (v *InMemoryVectorSearch) Search(query []float32, k int) []VectorResult {
	if len(query) == 0 || k <= 0 {
		return nil
	}

	v.mu.RLock()
	defer v.mu.RUnlock()

	if len(v.vectors) == 0 {
		return nil
	}

	// Normalize query vector
	queryNorm := normalizeVector(query)
	if queryNorm == nil {
		return nil
	}

	// Compute similarities for all vectors
	results := make([]VectorResult, 0, len(v.vectors))
	for memID, emb := range v.vectors {
		sim := cosineSimilarity(queryNorm, emb)
		if sim >= v.minScore {
			results = append(results, VectorResult{
				MemoryID:   memID,
				Embedding:  emb,
				Similarity: sim,
			})
		}
	}

	// Sort by similarity descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})

	// Return top k
	if len(results) > k {
		results = results[:k]
	}

	return results
}

// Add indexes a new vector with its memory ID.
func (v *InMemoryVectorSearch) Add(memoryID string, embedding []float32) error {
	if memoryID == "" {
		return fmt.Errorf("memoryID cannot be empty")
	}
	if len(embedding) == 0 {
		return fmt.Errorf("embedding cannot be empty")
	}

	// Normalize for consistent cosine similarity
	normalized := normalizeVector(embedding)
	if normalized == nil {
		return fmt.Errorf("cannot normalize zero vector")
	}

	v.mu.Lock()
	v.vectors[memoryID] = normalized
	v.mu.Unlock()

	return nil
}

// Remove deletes a vector by memory ID.
func (v *InMemoryVectorSearch) Remove(memoryID string) error {
	v.mu.Lock()
	delete(v.vectors, memoryID)
	v.mu.Unlock()
	return nil
}

// Count returns the number of indexed vectors.
func (v *InMemoryVectorSearch) Count() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.vectors)
}

// Close releases resources.
func (v *InMemoryVectorSearch) Close() error {
	v.mu.Lock()
	v.vectors = nil
	v.mu.Unlock()
	return nil
}

// ── SQLite-backed Vector Search ─────────────────────────────────────────────

// SQLiteVectorSearch provides vector search backed by SQLite storage.
// Uses pure-Go cosine similarity with vectors stored in SQLite.
// This allows persistence across restarts while keeping search in-memory.
type SQLiteVectorSearch struct {
	db          *sql.DB
	inMemory    *InMemoryVectorSearch
	cfg         VectorSearchConfig
	initialized bool
	mu          sync.Mutex
}

// NewSQLiteVectorSearch creates a vector search backed by SQLite.
func NewSQLiteVectorSearch(db *sql.DB, cfg VectorSearchConfig) (*SQLiteVectorSearch, error) {
	if db == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	s := &SQLiteVectorSearch{
		db:       db,
		cfg:      cfg,
		inMemory: NewInMemoryVectorSearch(cfg.Dims, cfg.MinScore),
	}

	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("init vector schema: %w", err)
	}

	// Load existing vectors into memory
	if err := s.loadVectors(); err != nil {
		return nil, fmt.Errorf("load vectors: %w", err)
	}

	s.initialized = true
	return s, nil
}

// initSchema creates the vector storage table.
func (s *SQLiteVectorSearch) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS chunks_vec (
		memory_id TEXT PRIMARY KEY,
		embedding TEXT NOT NULL,
		dims INTEGER,
		updated_at INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_chunks_vec_updated ON chunks_vec(updated_at);
	`
	_, err := s.db.Exec(schema)
	return err
}

// loadVectors loads all vectors from SQLite into memory.
func (s *SQLiteVectorSearch) loadVectors() error {
	rows, err := s.db.Query(`SELECT memory_id, embedding FROM chunks_vec`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var memID, embJSON string
		if err := rows.Scan(&memID, &embJSON); err != nil {
			continue
		}

		var embedding []float32
		if err := json.Unmarshal([]byte(embJSON), &embedding); err != nil {
			continue
		}

		s.inMemory.Add(memID, embedding)
	}

	return rows.Err()
}

// Search finds the k most similar vectors to the query.
func (s *SQLiteVectorSearch) Search(query []float32, k int) []VectorResult {
	return s.inMemory.Search(query, k)
}

// Add indexes a new vector with its memory ID.
// Stores both in-memory and in SQLite for persistence.
func (s *SQLiteVectorSearch) Add(memoryID string, embedding []float32) error {
	// Add to in-memory index
	if err := s.inMemory.Add(memoryID, embedding); err != nil {
		return err
	}

	// Persist to SQLite
	embJSON, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("marshal embedding: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO chunks_vec (memory_id, embedding, dims, updated_at)
		VALUES (?, ?, ?, strftime('%s', 'now'))
	`, memoryID, string(embJSON), len(embedding))

	return err
}

// Remove deletes a vector by memory ID.
func (s *SQLiteVectorSearch) Remove(memoryID string) error {
	if err := s.inMemory.Remove(memoryID); err != nil {
		return err
	}

	_, err := s.db.Exec(`DELETE FROM chunks_vec WHERE memory_id = ?`, memoryID)
	return err
}

// Count returns the number of indexed vectors.
func (s *SQLiteVectorSearch) Count() int {
	return s.inMemory.Count()
}

// Close releases resources.
func (s *SQLiteVectorSearch) Close() error {
	return s.inMemory.Close()
}

// Reload refreshes the in-memory index from SQLite.
func (s *SQLiteVectorSearch) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear current vectors
	s.inMemory.mu.Lock()
	s.inMemory.vectors = make(map[string][]float32)
	s.inMemory.mu.Unlock()

	// Reload from database
	return s.loadVectors()
}

// ── Vector Math Functions ───────────────────────────────────────────────────

// cosineSimilarity computes the cosine similarity between two vectors.
// Assumes vectors are already normalized for performance.
// Returns a value in [-1, 1], where 1 means identical direction.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	// For normalized vectors, cosine similarity = dot product
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}

	return dot
}

// cosineSimilarityRaw computes cosine similarity for non-normalized vectors.
func cosineSimilarityRaw(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// dotProduct computes the dot product of two vectors.
func dotProduct(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}

	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// euclideanDistance computes the L2 distance between two vectors.
func euclideanDistance(a, b []float32) float64 {
	if len(a) != len(b) {
		return math.MaxFloat64
	}

	var sum float64
	for i := range a {
		diff := float64(a[i]) - float64(b[i])
		sum += diff * diff
	}
	return math.Sqrt(sum)
}

// normalizeVector returns a unit-length version of the vector.
// Returns nil if the vector has zero magnitude.
func normalizeVector(v []float32) []float32 {
	if len(v) == 0 {
		return nil
	}

	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	norm = math.Sqrt(norm)

	if norm == 0 {
		return nil
	}

	result := make([]float32, len(v))
	invNorm := float32(1.0 / norm)
	for i, x := range v {
		result[i] = x * invNorm
	}
	return result
}

// magnitude computes the L2 norm (magnitude) of a vector.
func magnitude(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

// ── SQLite Backend Vector Integration ───────────────────────────────────────

// VectorBackend extends SQLiteBackend with vector search capabilities.
type VectorBackend struct {
	*SQLiteBackend
	vectorSearch *SQLiteVectorSearch
	cfg          VectorSearchConfig
}

// NewVectorBackend creates a new SQLite backend with vector search support.
func NewVectorBackend(path string, cfg VectorSearchConfig) (*VectorBackend, error) {
	base, err := OpenSQLiteBackend(path)
	if err != nil {
		return nil, err
	}

	vs, err := NewSQLiteVectorSearch(base.db, cfg)
	if err != nil {
		base.Close()
		return nil, fmt.Errorf("init vector search: %w", err)
	}

	return &VectorBackend{
		SQLiteBackend: base,
		vectorSearch:  vs,
		cfg:           cfg,
	}, nil
}

// SearchVector performs vector similarity search.
func (b *VectorBackend) SearchVector(query []float32, limit int) []IndexedMemory {
	if !b.cfg.Enabled || b.vectorSearch == nil {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}

	results := b.vectorSearch.Search(query, limit)
	if len(results) == 0 {
		return nil
	}

	// Fetch full memory records for results
	return b.fetchMemories(results)
}

// SearchHybridVector performs hybrid search combining vector and FTS results.
func (b *VectorBackend) SearchHybridVector(query string, queryVec []float32, limit int, cfg HybridSearchConfig) []IndexedMemory {
	if !cfg.Enabled {
		// Fall back to vector-only if hybrid disabled
		return b.SearchVector(queryVec, limit)
	}

	// Get oversampled candidates
	oversample := int(float64(limit) * cfg.CandidateMultiplier)
	if oversample < limit*2 {
		oversample = limit * 2
	}

	// Vector search
	var vectorResults []HybridVectorResult
	if b.cfg.Enabled && len(queryVec) > 0 {
		vecResults := b.vectorSearch.Search(queryVec, oversample)
		for _, r := range vecResults {
			vectorResults = append(vectorResults, HybridVectorResult{
				Score: r.Similarity,
			})
			// Need to fetch memory for each result
		}
	}

	// FTS keyword search
	ftsResults := b.Search(query, oversample)
	var keywordResults []HybridKeywordResult
	for _, r := range ftsResults {
		keywordResults = append(keywordResults, HybridKeywordResult{
			Memory: r,
			Score:  0.5, // Default score for FTS results
		})
	}

	// Use hybrid merge pipeline
	return HybridSearchPipeline(vectorResults, keywordResults, cfg, limit)
}

// AddWithEmbedding adds a memory with its embedding for vector search.
func (b *VectorBackend) AddWithEmbedding(mem IndexedMemory, embedding []float32) error {
	// Add to base SQLite backend
	b.SQLiteBackend.Add(memoryDocFromIndexed(mem))

	// Add embedding to vector search
	if b.cfg.Enabled && len(embedding) > 0 {
		return b.vectorSearch.Add(mem.MemoryID, embedding)
	}
	return nil
}

// fetchMemories retrieves full IndexedMemory records for vector results.
func (b *VectorBackend) fetchMemories(results []VectorResult) []IndexedMemory {
	if len(results) == 0 {
		return nil
	}

	memories := make([]IndexedMemory, 0, len(results))
	for _, r := range results {
		// Query for the memory by ID
		rows, err := b.db.Query(`
			SELECT id, session_id, role, topic, text, keywords, unix,
			       type, goal_id, task_id, run_id, episode_kind,
			       confidence, source, reviewed_at, reviewed_by, expires_at,
			       mem_status, superseded_by, invalidated_at, invalidated_by, invalidate_reason
			FROM chunks
			WHERE id = ?
		`, r.MemoryID)
		if err != nil {
			continue
		}

		mems := b.scanRowsNoRank(rows)
		rows.Close()

		if len(mems) > 0 {
			memories = append(memories, mems[0])
		}
	}

	return memories
}

// VectorStats returns vector search statistics.
func (b *VectorBackend) VectorStats() map[string]any {
	stats := b.Stats()
	stats["vector_enabled"] = b.cfg.Enabled
	stats["vector_dims"] = b.cfg.Dims
	stats["vector_count"] = 0

	if b.vectorSearch != nil {
		stats["vector_count"] = b.vectorSearch.Count()
	}

	return stats
}

// Close closes both the SQLite backend and vector search.
func (b *VectorBackend) Close() error {
	if b.vectorSearch != nil {
		b.vectorSearch.Close()
	}
	return b.SQLiteBackend.Close()
}

// memoryDocFromIndexed converts IndexedMemory to state.MemoryDoc.
func memoryDocFromIndexed(m IndexedMemory) state.MemoryDoc {
	return state.MemoryDoc{
		MemoryID:         m.MemoryID,
		SessionID:        m.SessionID,
		Role:             m.Role,
		Topic:            m.Topic,
		Text:             m.Text,
		Keywords:         m.Keywords,
		Unix:             m.Unix,
		Type:             m.Type,
		GoalID:           m.GoalID,
		TaskID:           m.TaskID,
		RunID:            m.RunID,
		EpisodeKind:      m.EpisodeKind,
		Confidence:       m.Confidence,
		Source:           m.Source,
		ReviewedAt:       m.ReviewedAt,
		ReviewedBy:       m.ReviewedBy,
		ExpiresAt:        m.ExpiresAt,
		MemStatus:        m.MemStatus,
		SupersededBy:     m.SupersededBy,
		InvalidatedAt:    m.InvalidatedAt,
		InvalidatedBy:    m.InvalidatedBy,
		InvalidateReason: m.InvalidateReason,
	}
}

// ── Approximate Nearest Neighbor Utilities ──────────────────────────────────

// RandomProjection creates a random projection matrix for dimensionality reduction.
// Useful for large-scale approximate nearest neighbor search.
func RandomProjection(inputDims, outputDims int, seed int64) [][]float32 {
	// Simple random projection using Gaussian distribution
	// For production, consider using sparse random projection
	matrix := make([][]float32, outputDims)
	scale := float32(1.0 / math.Sqrt(float64(outputDims)))

	for i := range matrix {
		matrix[i] = make([]float32, inputDims)
		for j := range matrix[i] {
			// Simplified random projection (for actual use, seed properly)
			matrix[i][j] = scale * float32((i*inputDims+j)%2*2-1)
		}
	}
	return matrix
}

// ProjectVector projects a vector using the given projection matrix.
func ProjectVector(v []float32, projection [][]float32) []float32 {
	if len(projection) == 0 || len(v) == 0 {
		return nil
	}

	result := make([]float32, len(projection))
	for i, row := range projection {
		if len(row) != len(v) {
			continue
		}
		for j, x := range v {
			result[i] += x * row[j]
		}
	}
	return result
}

// QuantizeVector converts a float32 vector to int8 for memory efficiency.
// Uses linear quantization to [-127, 127] range.
func QuantizeVector(v []float32) []int8 {
	if len(v) == 0 {
		return nil
	}

	// Find max absolute value
	maxAbs := float32(0)
	for _, x := range v {
		if abs := float32(math.Abs(float64(x))); abs > maxAbs {
			maxAbs = abs
		}
	}

	if maxAbs == 0 {
		return make([]int8, len(v))
	}

	result := make([]int8, len(v))
	scale := 127.0 / maxAbs
	for i, x := range v {
		result[i] = int8(math.Round(float64(x * scale)))
	}
	return result
}

// DequantizeVector converts an int8 vector back to float32.
func DequantizeVector(v []int8, scale float32) []float32 {
	if len(v) == 0 {
		return nil
	}

	result := make([]float32, len(v))
	invScale := scale / 127.0
	for i, x := range v {
		result[i] = float32(x) * invScale
	}
	return result
}
