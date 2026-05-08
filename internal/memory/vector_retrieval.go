package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// MemoryEmbeddingProvider is the optional embedding hook used by unified memory
// vector retrieval. Implementations must be deterministic for their returned
// provider/model/version metadata; the version is part of the embedding space
// and is used to prevent incompatible vector comparisons.
type MemoryEmbeddingProvider interface {
	EmbeddingProvider() EmbeddingProvider
	Embed(ctx context.Context, text string) ([]float32, error)
}

// MemoryVectorRetrievalConfig controls optional vector retrieval. The zero value
// is disabled, so BM25/FTS remains the default and requires no external service.
type MemoryVectorRetrievalConfig struct {
	Enabled             bool    `json:"enabled"`
	CandidateMultiplier float64 `json:"candidate_multiplier,omitempty"`
	RRFK                int     `json:"rrf_k,omitempty"`
	MinSimilarity       float64 `json:"min_similarity,omitempty"`
	ReindexBatchSize    int     `json:"reindex_batch_size,omitempty"`
	ReindexDailyLimit   int     `json:"reindex_daily_limit,omitempty"`
}

type MemoryVectorCounters struct {
	Queries      int `json:"queries"`
	Candidates   int `json:"candidates"`
	Fallbacks    int `json:"fallbacks"`
	Reindexed    int `json:"reindexed"`
	VersionSkips int `json:"version_skips"`
}

type MemoryVectorReindexResult struct {
	Provider  EmbeddingProvider `json:"provider"`
	Checked   int               `json:"checked"`
	Reindexed int               `json:"reindexed"`
	Skipped   int               `json:"skipped"`
}

type memoryVectorCandidate struct {
	Record     MemoryRecord
	Similarity float64
}

func DefaultMemoryVectorRetrievalConfig() MemoryVectorRetrievalConfig {
	return MemoryVectorRetrievalConfig{Enabled: false, CandidateMultiplier: 2, RRFK: 60, MinSimilarity: -1, ReindexBatchSize: 100, ReindexDailyLimit: 1000}
}

func normalizeMemoryVectorRetrievalConfig(cfg MemoryVectorRetrievalConfig) MemoryVectorRetrievalConfig {
	if cfg.CandidateMultiplier <= 0 {
		cfg.CandidateMultiplier = 2
	}
	if cfg.RRFK <= 0 {
		cfg.RRFK = 60
	}
	if cfg.MinSimilarity == 0 {
		cfg.MinSimilarity = -1
	}
	if cfg.ReindexBatchSize <= 0 {
		cfg.ReindexBatchSize = 100
	}
	if cfg.ReindexBatchSize > 100 {
		cfg.ReindexBatchSize = 100
	}
	if cfg.ReindexDailyLimit <= 0 {
		cfg.ReindexDailyLimit = 1000
	}
	return cfg
}

// ConfigureVectorRetrieval installs an optional embedding provider. Passing a
// disabled config or nil provider disables vector retrieval; query paths will
// keep using BM25/LIKE fallback.
func (b *SQLiteBackend) ConfigureVectorRetrieval(cfg MemoryVectorRetrievalConfig, provider MemoryEmbeddingProvider) error {
	if b == nil {
		return fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return err
	}
	cfg = normalizeMemoryVectorRetrievalConfig(cfg)
	if !cfg.Enabled || provider == nil {
		cfg.Enabled = false
		provider = nil
	}
	b.mu.Lock()
	b.vectorCfg = cfg
	b.vectorProvider = provider
	b.mu.Unlock()
	return nil
}

func (b *SQLiteBackend) vectorRetrievalState() (MemoryVectorRetrievalConfig, MemoryEmbeddingProvider, EmbeddingProvider, bool) {
	if b == nil {
		return MemoryVectorRetrievalConfig{}, nil, EmbeddingProvider{}, false
	}
	b.mu.RLock()
	cfg := b.vectorCfg
	provider := b.vectorProvider
	b.mu.RUnlock()
	cfg = normalizeMemoryVectorRetrievalConfig(cfg)
	if !cfg.Enabled || provider == nil {
		return cfg, nil, EmbeddingProvider{}, false
	}
	meta := provider.EmbeddingProvider().Normalized()
	if meta.ID == "" || meta.Model == "" {
		return cfg, nil, EmbeddingProvider{}, false
	}
	return cfg, provider, meta, true
}

func (b *SQLiteBackend) MemoryVectorStats() MemoryVectorCounters {
	if b == nil {
		return MemoryVectorCounters{}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.vectorCounters
}

func (b *SQLiteBackend) addVectorCounters(fn func(*MemoryVectorCounters)) {
	if b == nil || fn == nil {
		return
	}
	b.mu.Lock()
	fn(&b.vectorCounters)
	b.mu.Unlock()
}

// StoreMemoryEmbedding records a vector for exactly one provider/model/version
// and stamps the record with that metadata for lazy reindex visibility.
func (b *SQLiteBackend) StoreMemoryEmbedding(ctx context.Context, recordID string, provider EmbeddingProvider, embedding []float32) error {
	_ = ctx
	if b == nil {
		return fmt.Errorf("sqlite backend is nil")
	}
	provider = provider.Normalized()
	if provider.ID == "" || provider.Model == "" {
		return fmt.Errorf("embedding provider id and model are required")
	}
	recordID = strings.TrimSpace(recordID)
	if recordID == "" {
		return fmt.Errorf("record id is required")
	}
	if len(embedding) == 0 {
		return fmt.Errorf("embedding is required")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return err
	}
	embJSON, err := json.Marshal(embedding)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	_, err = b.db.Exec(`
		INSERT OR REPLACE INTO memory_embeddings (record_id, embedding_model, embedding_version, embedding, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, recordID, provider.Model, provider.Version, string(embJSON), now)
	if err != nil {
		return err
	}
	_, _ = b.db.Exec(`UPDATE memory_records SET embedding_model = ?, embedding_version = ? WHERE id = ?`, provider.Model, provider.Version, recordID)
	return nil
}

// ReindexMemoryEmbeddings is a lazy, caller-triggered hook. It only embeds
// records missing the currently configured compatible provider/version; callers
// can invoke it during maintenance without changing the hot BM25 path.
func (b *SQLiteBackend) ReindexMemoryEmbeddings(ctx context.Context, limit int) (MemoryVectorReindexResult, error) {
	cfg, provider, meta, ok := b.vectorRetrievalState()
	result := MemoryVectorReindexResult{Provider: meta}
	if !ok || !cfg.Enabled {
		return result, nil
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return result, err
	}
	if limit <= 0 || limit > cfg.ReindexBatchSize {
		limit = cfg.ReindexBatchSize
	}
	if limit > 100 {
		limit = 100
	}
	remainingSession, remainingDay, err := b.reindexGovernanceAllowance(cfg)
	if err != nil {
		return result, err
	}
	if remainingSession <= 0 || remainingDay <= 0 {
		return result, nil
	}
	if limit > remainingSession {
		limit = remainingSession
	}
	if limit > remainingDay {
		limit = remainingDay
	}
	if limit <= 0 {
		return result, nil
	}
	rows, err := b.db.Query(memoryRecordSelectSQL("0.0")+`
		FROM memory_records r
		LEFT JOIN memory_embeddings e ON e.record_id = r.id AND e.embedding_model = ? AND e.embedding_version = ?
		WHERE r.deleted_at IS NULL AND e.record_id IS NULL
		ORDER BY r.pinned DESC,
			CASE WHEN (r.pinned != 0 OR COALESCE(r.source_file_path, '') != '' OR r.metadata LIKE '%"durable":true%') THEN 1 ELSE 0 END DESC,
			r.salience DESC,
			r.updated_at DESC
		LIMIT ?`, meta.Model, meta.Version, limit)
	if err != nil {
		return result, err
	}
	records, _ := b.scanMemoryRecordRows(rows)
	rows.Close()
	result.Checked = len(records)
	for _, rec := range records {
		statusID, _ := b.insertReindexStatus(rec.ID)
		vec, err := provider.Embed(ctx, rec.Text)
		if err != nil || len(vec) == 0 {
			result.Skipped++
			b.completeReindexStatus(statusID, "skipped")
			continue
		}
		if err := b.StoreMemoryEmbedding(ctx, rec.ID, meta, vec); err != nil {
			result.Skipped++
			b.completeReindexStatus(statusID, "skipped")
			continue
		}
		result.Reindexed++
		b.completeReindexStatus(statusID, "completed")
	}
	if result.Reindexed > 0 {
		b.addVectorCounters(func(c *MemoryVectorCounters) { c.Reindexed += result.Reindexed })
		b.mu.Lock()
		b.vectorSessionReindexed += result.Reindexed
		b.mu.Unlock()
	}
	return result, nil
}

func (b *SQLiteBackend) insertReindexStatus(recordID string) (int64, error) {
	if b == nil || b.db == nil {
		return 0, fmt.Errorf("sqlite backend is closed")
	}
	now := time.Now().UTC().Unix()
	res, err := b.db.Exec(`INSERT INTO reindex_status (record_id, started_at, status) VALUES (?, ?, 'started')`, strings.TrimSpace(recordID), now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func (b *SQLiteBackend) completeReindexStatus(id int64, status string) {
	if b == nil || b.db == nil || id <= 0 {
		return
	}
	if strings.TrimSpace(status) == "" {
		status = "completed"
	}
	_, _ = b.db.Exec(`UPDATE reindex_status SET completed_at = ?, status = ? WHERE id = ?`, time.Now().UTC().Unix(), strings.TrimSpace(status), id)
}

func (b *SQLiteBackend) reindexGovernanceAllowance(cfg MemoryVectorRetrievalConfig) (sessionRemaining int, dayRemaining int, err error) {
	if b == nil || b.db == nil {
		return 0, 0, fmt.Errorf("sqlite backend is closed")
	}
	b.mu.RLock()
	sessionUsed := b.vectorSessionReindexed
	b.mu.RUnlock()
	sessionRemaining = 100 - sessionUsed
	if sessionRemaining < 0 {
		sessionRemaining = 0
	}
	dayStart := time.Now().UTC().Truncate(24 * time.Hour).Unix()
	var dayUsed int
	if scanErr := b.db.QueryRow(`SELECT COUNT(*) FROM reindex_status WHERE started_at >= ?`, dayStart).Scan(&dayUsed); scanErr != nil {
		return 0, 0, scanErr
	}
	dayRemaining = cfg.ReindexDailyLimit - dayUsed
	if dayRemaining < 0 {
		dayRemaining = 0
	}
	return sessionRemaining, dayRemaining, nil
}

func (b *SQLiteBackend) recordVectorQueryActivityAndScheduleIdleReindex() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.vectorLastQueryAt = time.Now().UTC()
	if b.vectorIdleReindexTimer != nil {
		b.vectorIdleReindexTimer.Stop()
	}
	b.vectorIdleReindexTimer = time.AfterFunc(5*time.Minute, func() {
		b.mu.RLock()
		lastQuery := b.vectorLastQueryAt
		b.mu.RUnlock()
		if time.Since(lastQuery) < 5*time.Minute {
			return
		}
		_, _ = b.ReindexMemoryEmbeddings(context.Background(), 0)
	})
	b.mu.Unlock()
}

func (b *SQLiteBackend) fetchMemoryVectorCandidates(ctx context.Context, q MemoryQuery, searchQuery string, limit int) ([]memoryVectorCandidate, error) {
	cfg, provider, meta, ok := b.vectorRetrievalState()
	if !ok {
		return nil, nil
	}
	if strings.TrimSpace(searchQuery) == "" || limit <= 0 {
		return nil, nil
	}
	b.addVectorCounters(func(c *MemoryVectorCounters) { c.Queries++ })
	queryVec, err := provider.Embed(ctx, searchQuery)
	if err != nil || len(queryVec) == 0 {
		b.addVectorCounters(func(c *MemoryVectorCounters) { c.Fallbacks++ })
		return nil, nil
	}
	args := []any{meta.Model, meta.Version}
	where := unifiedMetadataWhere(q, &args)
	rows, err := b.db.Query(`
		SELECT r.id, e.embedding, r.embedding_model, r.embedding_version
		FROM memory_embeddings e JOIN memory_records r ON r.id = e.record_id
		WHERE e.embedding_model = ? AND e.embedding_version = ? `+where, args...)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		b.addVectorCounters(func(c *MemoryVectorCounters) { c.Fallbacks++ })
		return nil, nil
	}
	defer rows.Close()

	type scored struct {
		id         string
		similarity float64
	}
	scoredRows := []scored{}
	for rows.Next() {
		var id, embJSON string
		var recModel, recVersion sql.NullString
		if err := rows.Scan(&id, &embJSON, &recModel, &recVersion); err != nil {
			continue
		}
		if recModel.Valid || recVersion.Valid {
			recProvider := EmbeddingProvider{ID: meta.ID, Model: recModel.String, Version: recVersion.String}
			if !meta.Compatible(recProvider) {
				b.addVectorCounters(func(c *MemoryVectorCounters) { c.VersionSkips++ })
				continue
			}
		}
		var emb []float32
		if err := json.Unmarshal([]byte(embJSON), &emb); err != nil || len(emb) == 0 {
			continue
		}
		sim := cosineSimilarityRaw(queryVec, emb)
		if sim >= cfg.MinSimilarity {
			scoredRows = append(scoredRows, scored{id: id, similarity: sim})
		}
	}
	sort.SliceStable(scoredRows, func(i, j int) bool { return scoredRows[i].similarity > scoredRows[j].similarity })
	if len(scoredRows) > limit {
		scoredRows = scoredRows[:limit]
	}
	out := make([]memoryVectorCandidate, 0, len(scoredRows))
	for _, item := range scoredRows {
		rec, ok, err := b.GetMemoryRecord(ctx, item.id)
		if err != nil || !ok {
			continue
		}
		out = append(out, memoryVectorCandidate{Record: rec, Similarity: item.similarity})
	}
	if len(out) > 0 {
		b.addVectorCounters(func(c *MemoryVectorCounters) { c.Candidates += len(out) })
	}
	return out, nil
}

func mergeBM25VectorCandidates(records []MemoryRecord, ranks []float64, vectors []memoryVectorCandidate, q MemoryQuery, intent QueryIntent, cfg MemoryVectorRetrievalConfig, now time.Time) []memoryRankedRecord {
	base := rankMemoryRecords(records, ranks, q, intent, now)
	if len(vectors) == 0 {
		return base
	}
	type fusion struct {
		rec       MemoryRecord
		rank      float64
		bm25Pos   int
		vectorPos int
		vectorSim float64
		base      memoryRankedRecord
	}
	items := map[string]*fusion{}
	for i, item := range base {
		copyItem := item
		items[item.Record.ID] = &fusion{rec: item.Record, rank: item.Rank, bm25Pos: i + 1, base: copyItem}
	}
	for i, v := range vectors {
		entry := items[v.Record.ID]
		if entry == nil {
			entry = &fusion{rec: v.Record, vectorPos: i + 1, vectorSim: v.Similarity}
			items[v.Record.ID] = entry
			continue
		}
		entry.vectorPos = i + 1
		entry.vectorSim = v.Similarity
	}
	merged := make([]memoryRankedRecord, 0, len(items))
	for _, item := range items {
		ranked := item.base
		if ranked.Record.ID == "" {
			ranked = rankMemoryRecords([]MemoryRecord{item.rec}, []float64{0}, q, intent, now)[0]
		}
		rrf := 0.0
		if item.bm25Pos > 0 {
			rrf += 1.0 / float64(cfg.RRFK+item.bm25Pos)
		}
		if item.vectorPos > 0 {
			rrf += 1.0 / float64(cfg.RRFK+item.vectorPos)
		}
		maxRRF := 2.0 / float64(cfg.RRFK+1)
		rrfNorm := 0.0
		if maxRRF > 0 {
			rrfNorm = rrf / maxRRF
		}
		if ranked.Why.Components == nil {
			ranked.Why.Components = map[string]float64{}
		}
		ranked.Why.Components["rrf"] = roundScore(rrfNorm * 0.25)
		if item.vectorPos > 0 {
			ranked.Why.Components["vector"] = roundScore(clamp01(item.vectorSim) * 0.25)
			ranked.Why.VectorRank = item.vectorPos
			ranked.Why.VectorSimilarity = roundScore(item.vectorSim)
			ranked.Why.Reasons = append(ranked.Why.Reasons, "vector match", "hybrid rrf")
		}
		ranked.Score = clamp01(roundScore((ranked.Score * 0.75) + (rrfNorm * 0.25)))
		merged = append(merged, ranked)
	}
	return merged
}
