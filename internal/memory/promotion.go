// Package memory — Memory promotion (dreaming) for consolidating short-term memories.
//
// This module implements memory consolidation that promotes frequently-recalled
// short-term memories to long-term storage. Inspired by human sleep consolidation,
// memories that meet certain thresholds (recall count, unique queries, relevance
// score) are promoted and optionally summarized.
//
// The promotion process:
//  1. Track recalls during Search/SearchSession operations
//  2. Periodically scan for promotion candidates
//  3. Group related memories by topic/embedding similarity
//  4. Generate consolidated summaries (optional, requires LLM)
//  5. Write promoted memories to persistent topic files
package memory

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// PromotionConfig configures memory promotion behavior.
type PromotionConfig struct {
	// Enabled toggles memory promotion (default: true).
	Enabled bool `json:"enabled"`

	// MinRecallCount is the minimum number of recalls before promotion (default: 3).
	MinRecallCount int `json:"min_recall_count"`

	// MinUniqueQueries is the minimum number of distinct queries (default: 2).
	MinUniqueQueries int `json:"min_unique_queries"`

	// MinScore is the minimum average relevance score (default: 0.75).
	MinScore float64 `json:"min_score"`

	// RecencyHalfLife is the number of days for recency decay (default: 14).
	// Older memories need higher recall counts to compensate.
	RecencyHalfLife int `json:"recency_half_life"`

	// MaxBatchSize limits how many memories to promote per sweep (default: 100).
	MaxBatchSize int `json:"max_batch_size"`

	// PromotedTopic is the default topic for promoted memories (default: "consolidated").
	PromotedTopic string `json:"promoted_topic"`

	// EnableSummary enables LLM-based summary generation for promoted memories.
	EnableSummary bool `json:"enable_summary"`

	// SummaryModel is the LLM model to use for summaries (default: "").
	SummaryModel string `json:"summary_model,omitempty"`
}

// DefaultPromotionConfig returns sensible defaults.
func DefaultPromotionConfig() PromotionConfig {
	return PromotionConfig{
		Enabled:          true,
		MinRecallCount:   3,
		MinUniqueQueries: 2,
		MinScore:         0.75,
		RecencyHalfLife:  14,
		MaxBatchSize:     100,
		PromotedTopic:    "consolidated",
		EnableSummary:    false,
	}
}

// RecallRecord represents tracking data for a single memory.
type RecallRecord struct {
	MemoryID       string   `json:"memory_id"`
	RecallCount    int      `json:"recall_count"`
	UniqueQueries  int      `json:"unique_queries"`
	QueryHashes    []string `json:"query_hashes"`
	LastRecallUnix int64    `json:"last_recall_unix"`
	FirstRecallUnix int64   `json:"first_recall_unix"`
	AvgScore       float64  `json:"avg_score"`
	PromotedAt     int64    `json:"promoted_at,omitempty"`
	PromotedTo     string   `json:"promoted_to,omitempty"`
}

// PromotionCandidate represents a memory eligible for promotion.
type PromotionCandidate struct {
	Memory       IndexedMemory
	RecallRecord RecallRecord
	Score        float64 // Computed promotion score
}

// PromotionResult represents the outcome of a promotion sweep.
type PromotionResult struct {
	Candidates  int      `json:"candidates"`
	Promoted    int      `json:"promoted"`
	Skipped     int      `json:"skipped"`
	Errors      int      `json:"errors"`
	PromotedIDs []string `json:"promoted_ids,omitempty"`
	StartTime   int64    `json:"start_time"`
	EndTime     int64    `json:"end_time"`
	DurationMs  int64    `json:"duration_ms"`
}

// ── Recall Tracker ──────────────────────────────────────────────────────────

// RecallTracker tracks memory recalls for promotion analysis.
type RecallTracker struct {
	db  *sql.DB
	mu  sync.Mutex
	cfg PromotionConfig

	// Batch tracking for efficiency
	pending map[string]*recallUpdate
}

type recallUpdate struct {
	memoryID  string
	queryHash string
	score     float64
	timestamp int64
}

// NewRecallTracker creates a new recall tracker.
func NewRecallTracker(db *sql.DB, cfg PromotionConfig) *RecallTracker {
	return &RecallTracker{
		db:      db,
		cfg:     cfg,
		pending: make(map[string]*recallUpdate),
	}
}

// TrackRecall records a memory recall event.
func (t *RecallTracker) TrackRecall(memoryID, query string, score float64) {
	if !t.cfg.Enabled || memoryID == "" {
		return
	}

	queryHash := hashQuery(query)
	now := time.Now().Unix()

	t.mu.Lock()
	key := memoryID + ":" + queryHash
	t.pending[key] = &recallUpdate{
		memoryID:  memoryID,
		queryHash: queryHash,
		score:     score,
		timestamp: now,
	}
	t.mu.Unlock()
}

// TrackRecalls records multiple recall events (batch operation).
func (t *RecallTracker) TrackRecalls(results []IndexedMemory, query string) {
	if !t.cfg.Enabled || len(results) == 0 {
		return
	}

	queryHash := hashQuery(query)
	now := time.Now().Unix()

	t.mu.Lock()
	for i, r := range results {
		// Score decays with position (top result = 1.0, decay per position)
		positionScore := 1.0 / float64(i+1)
		key := r.MemoryID + ":" + queryHash
		t.pending[key] = &recallUpdate{
			memoryID:  r.MemoryID,
			queryHash: queryHash,
			score:     positionScore,
			timestamp: now,
		}
	}
	t.mu.Unlock()
}

// Flush writes pending recall updates to the database.
func (t *RecallTracker) Flush() error {
	t.mu.Lock()
	if len(t.pending) == 0 {
		t.mu.Unlock()
		return nil
	}

	updates := make([]*recallUpdate, 0, len(t.pending))
	for _, u := range t.pending {
		updates = append(updates, u)
	}
	t.pending = make(map[string]*recallUpdate)
	t.mu.Unlock()

	// Group by memory ID
	byMemory := make(map[string][]*recallUpdate)
	for _, u := range updates {
		byMemory[u.memoryID] = append(byMemory[u.memoryID], u)
	}

	// Update each memory's recall tracking
	for memoryID, recalls := range byMemory {
		if err := t.updateRecallRecord(memoryID, recalls); err != nil {
			// Log error but continue with other updates
			continue
		}
	}

	return nil
}

// updateRecallRecord updates the recall tracking for a single memory.
func (t *RecallTracker) updateRecallRecord(memoryID string, recalls []*recallUpdate) error {
	// Get existing record
	var record RecallRecord
	var queryHashesJSON sql.NullString

	err := t.db.QueryRow(`
		SELECT memory_id, recall_count, unique_queries, query_hashes,
		       last_recall_unix, first_recall_unix, avg_score, promoted_at, promoted_to
		FROM recall_tracking
		WHERE memory_id = ?
	`, memoryID).Scan(
		&record.MemoryID, &record.RecallCount, &record.UniqueQueries, &queryHashesJSON,
		&record.LastRecallUnix, &record.FirstRecallUnix, &record.AvgScore,
		&record.PromotedAt, &record.PromotedTo,
	)

	isNew := err == sql.ErrNoRows
	if err != nil && !isNew {
		return fmt.Errorf("query recall record: %w", err)
	}

	// Parse existing query hashes
	existingHashes := make(map[string]struct{})
	if queryHashesJSON.Valid && queryHashesJSON.String != "" {
		var hashes []string
		if json.Unmarshal([]byte(queryHashesJSON.String), &hashes) == nil {
			for _, h := range hashes {
				existingHashes[h] = struct{}{}
			}
		}
	}

	// Process new recalls
	var totalScore float64
	var latestTimestamp int64
	var earliestTimestamp int64 = record.FirstRecallUnix

	for _, r := range recalls {
		record.RecallCount++
		totalScore += r.score

		if _, exists := existingHashes[r.queryHash]; !exists {
			existingHashes[r.queryHash] = struct{}{}
			record.UniqueQueries++
		}

		if r.timestamp > latestTimestamp {
			latestTimestamp = r.timestamp
		}
		if earliestTimestamp == 0 || r.timestamp < earliestTimestamp {
			earliestTimestamp = r.timestamp
		}
	}

	record.MemoryID = memoryID
	record.LastRecallUnix = latestTimestamp
	record.FirstRecallUnix = earliestTimestamp

	// Compute running average score
	if record.RecallCount > 0 {
		// Weighted average: 70% existing avg, 30% new recalls
		newAvg := totalScore / float64(len(recalls))
		if isNew || record.AvgScore == 0 {
			record.AvgScore = newAvg
		} else {
			record.AvgScore = 0.7*record.AvgScore + 0.3*newAvg
		}
	}

	// Serialize query hashes
	hashes := make([]string, 0, len(existingHashes))
	for h := range existingHashes {
		hashes = append(hashes, h)
	}
	hashesJSON, _ := json.Marshal(hashes)

	// Upsert record
	_, err = t.db.Exec(`
		INSERT INTO recall_tracking (
			memory_id, recall_count, unique_queries, query_hashes,
			last_recall_unix, first_recall_unix, avg_score
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(memory_id) DO UPDATE SET
			recall_count = excluded.recall_count,
			unique_queries = excluded.unique_queries,
			query_hashes = excluded.query_hashes,
			last_recall_unix = excluded.last_recall_unix,
			first_recall_unix = COALESCE(recall_tracking.first_recall_unix, excluded.first_recall_unix),
			avg_score = excluded.avg_score
	`, record.MemoryID, record.RecallCount, record.UniqueQueries, string(hashesJSON),
		record.LastRecallUnix, record.FirstRecallUnix, record.AvgScore)

	return err
}

// GetRecallRecord retrieves the recall tracking for a memory.
func (t *RecallTracker) GetRecallRecord(memoryID string) (*RecallRecord, error) {
	var record RecallRecord
	var queryHashesJSON, promotedTo sql.NullString
	var promotedAt sql.NullInt64

	err := t.db.QueryRow(`
		SELECT memory_id, recall_count, unique_queries, query_hashes,
		       last_recall_unix, first_recall_unix, avg_score, promoted_at, promoted_to
		FROM recall_tracking
		WHERE memory_id = ?
	`, memoryID).Scan(
		&record.MemoryID, &record.RecallCount, &record.UniqueQueries, &queryHashesJSON,
		&record.LastRecallUnix, &record.FirstRecallUnix, &record.AvgScore,
		&promotedAt, &promotedTo,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if queryHashesJSON.Valid {
		json.Unmarshal([]byte(queryHashesJSON.String), &record.QueryHashes)
	}
	if promotedAt.Valid {
		record.PromotedAt = promotedAt.Int64
	}
	if promotedTo.Valid {
		record.PromotedTo = promotedTo.String
	}

	return &record, nil
}

// ── Promotion Manager ───────────────────────────────────────────────────────

// PromotionManager handles memory promotion sweeps.
type PromotionManager struct {
	db      *sql.DB
	backend *SQLiteBackend
	cfg     PromotionConfig
	tracker *RecallTracker
	mu      sync.Mutex

	// Optional callback for generating summaries
	summarizer func(memories []IndexedMemory) (string, error)
}

// NewPromotionManager creates a new promotion manager.
func NewPromotionManager(backend *SQLiteBackend, cfg PromotionConfig) *PromotionManager {
	return &PromotionManager{
		db:      backend.db,
		backend: backend,
		cfg:     cfg,
		tracker: NewRecallTracker(backend.db, cfg),
	}
}

// SetSummarizer sets the callback for generating memory summaries.
func (m *PromotionManager) SetSummarizer(fn func([]IndexedMemory) (string, error)) {
	m.summarizer = fn
}

// Tracker returns the recall tracker for use during search.
func (m *PromotionManager) Tracker() *RecallTracker {
	return m.tracker
}

// FindCandidates finds memories eligible for promotion.
func (m *PromotionManager) FindCandidates() ([]PromotionCandidate, error) {
	if !m.cfg.Enabled {
		return nil, nil
	}

	// Query recall tracking for candidates meeting thresholds
	rows, err := m.db.Query(`
		SELECT rt.memory_id, rt.recall_count, rt.unique_queries, rt.query_hashes,
		       rt.last_recall_unix, rt.first_recall_unix, rt.avg_score
		FROM recall_tracking rt
		WHERE rt.promoted_at IS NULL
		  AND rt.recall_count >= ?
		  AND rt.unique_queries >= ?
		  AND rt.avg_score >= ?
		ORDER BY rt.recall_count DESC, rt.avg_score DESC
		LIMIT ?
	`, m.cfg.MinRecallCount, m.cfg.MinUniqueQueries, m.cfg.MinScore, m.cfg.MaxBatchSize)

	if err != nil {
		return nil, fmt.Errorf("query candidates: %w", err)
	}
	defer rows.Close()

	var candidates []PromotionCandidate
	now := time.Now().Unix()
	halfLifeSeconds := int64(m.cfg.RecencyHalfLife * 24 * 60 * 60)

	for rows.Next() {
		var record RecallRecord
		var queryHashesJSON sql.NullString

		err := rows.Scan(
			&record.MemoryID, &record.RecallCount, &record.UniqueQueries, &queryHashesJSON,
			&record.LastRecallUnix, &record.FirstRecallUnix, &record.AvgScore,
		)
		if err != nil {
			continue
		}

		if queryHashesJSON.Valid {
			json.Unmarshal([]byte(queryHashesJSON.String), &record.QueryHashes)
		}

		// Fetch the actual memory
		memory, err := m.fetchMemory(record.MemoryID)
		if err != nil || memory == nil {
			continue
		}

		// Compute promotion score with recency decay
		age := now - record.LastRecallUnix
		recencyFactor := 1.0
		if halfLifeSeconds > 0 && age > 0 {
			recencyFactor = 1.0 / (1.0 + float64(age)/float64(halfLifeSeconds))
		}

		score := float64(record.RecallCount) * record.AvgScore * recencyFactor

		candidates = append(candidates, PromotionCandidate{
			Memory:       *memory,
			RecallRecord: record,
			Score:        score,
		})
	}

	// Sort by promotion score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	return candidates, nil
}

// fetchMemory retrieves a memory by ID.
func (m *PromotionManager) fetchMemory(memoryID string) (*IndexedMemory, error) {
	var mem IndexedMemory
	var keywords sql.NullString

	err := m.db.QueryRow(`
		SELECT id, session_id, role, topic, text, keywords, unix,
		       type, goal_id, task_id, run_id, episode_kind,
		       confidence, source, reviewed_at, reviewed_by, expires_at,
		       mem_status, superseded_by, invalidated_at, invalidated_by, invalidate_reason
		FROM chunks
		WHERE id = ?
	`, memoryID).Scan(
		&mem.MemoryID, &mem.SessionID, &mem.Role, &mem.Topic, &mem.Text, &keywords, &mem.Unix,
		&mem.Type, &mem.GoalID, &mem.TaskID, &mem.RunID, &mem.EpisodeKind,
		&mem.Confidence, &mem.Source, &mem.ReviewedAt, &mem.ReviewedBy, &mem.ExpiresAt,
		&mem.MemStatus, &mem.SupersededBy, &mem.InvalidatedAt, &mem.InvalidatedBy, &mem.InvalidateReason,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if keywords.Valid {
		json.Unmarshal([]byte(keywords.String), &mem.Keywords)
	}

	return &mem, nil
}

// Promote runs a promotion sweep, promoting eligible memories.
func (m *PromotionManager) Promote() (*PromotionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := &PromotionResult{
		StartTime: time.Now().Unix(),
	}

	if !m.cfg.Enabled {
		result.EndTime = time.Now().Unix()
		return result, nil
	}

	// Flush any pending recalls first
	if err := m.tracker.Flush(); err != nil {
		// Log but continue
	}

	// Find candidates
	candidates, err := m.FindCandidates()
	if err != nil {
		result.EndTime = time.Now().Unix()
		result.DurationMs = (result.EndTime - result.StartTime) * 1000
		return result, err
	}

	result.Candidates = len(candidates)

	// Group candidates by topic for batch processing
	byTopic := make(map[string][]PromotionCandidate)
	for _, c := range candidates {
		topic := c.Memory.Topic
		if topic == "" {
			topic = m.cfg.PromotedTopic
		}
		byTopic[topic] = append(byTopic[topic], c)
	}

	// Promote each group
	now := time.Now().Unix()
	for topic, group := range byTopic {
		promoted, err := m.promoteGroup(topic, group, now)
		if err != nil {
			result.Errors++
			continue
		}
		result.Promoted += promoted
		for _, c := range group[:promoted] {
			result.PromotedIDs = append(result.PromotedIDs, c.Memory.MemoryID)
		}
	}

	result.Skipped = result.Candidates - result.Promoted - result.Errors
	result.EndTime = time.Now().Unix()
	result.DurationMs = (result.EndTime - result.StartTime) * 1000

	return result, nil
}

// promoteGroup promotes a group of related memories.
func (m *PromotionManager) promoteGroup(topic string, candidates []PromotionCandidate, timestamp int64) (int, error) {
	if len(candidates) == 0 {
		return 0, nil
	}

	promoted := 0

	// If summarization is enabled and we have a summarizer, generate summary
	if m.cfg.EnableSummary && m.summarizer != nil && len(candidates) > 1 {
		memories := make([]IndexedMemory, len(candidates))
		for i, c := range candidates {
			memories[i] = c.Memory
		}

		summary, err := m.summarizer(memories)
		if err == nil && summary != "" {
			// Create consolidated memory from summary
			consolidatedID := GenerateMemoryID()
			consolidatedMem := IndexedMemory{
				MemoryID:   consolidatedID,
				Topic:      topic,
				Text:       summary,
				Unix:       timestamp,
				Type:       "consolidated",
				Confidence: averageConfidence(candidates),
				Source:     "promotion",
			}

			// Add to database
			m.backend.Add(memoryDocFromIndexed(consolidatedMem))

			// Mark all source memories as promoted
			for _, c := range candidates {
				if err := m.markPromoted(c.Memory.MemoryID, consolidatedID, timestamp); err != nil {
					continue
				}
				promoted++
			}

			return promoted, nil
		}
	}

	// Without summarization, promote individual memories
	for _, c := range candidates {
		// Update topic and mark as promoted
		promotedTopic := topic
		if promotedTopic == "" {
			promotedTopic = m.cfg.PromotedTopic
		}

		// Update the memory's topic if needed
		if c.Memory.Topic != promotedTopic {
			_, err := m.db.Exec(`UPDATE chunks SET topic = ? WHERE id = ?`, promotedTopic, c.Memory.MemoryID)
			if err != nil {
				continue
			}
		}

		// Mark as promoted
		if err := m.markPromoted(c.Memory.MemoryID, promotedTopic, timestamp); err != nil {
			continue
		}
		promoted++
	}

	return promoted, nil
}

// markPromoted updates the recall tracking to indicate a memory was promoted.
func (m *PromotionManager) markPromoted(memoryID, promotedTo string, timestamp int64) error {
	_, err := m.db.Exec(`
		UPDATE recall_tracking
		SET promoted_at = ?, promoted_to = ?
		WHERE memory_id = ?
	`, timestamp, promotedTo, memoryID)
	return err
}

// GetPromotionStats returns statistics about memory promotion.
func (m *PromotionManager) GetPromotionStats() (map[string]any, error) {
	stats := make(map[string]any)

	// Total tracked memories
	var totalTracked int
	m.db.QueryRow(`SELECT COUNT(*) FROM recall_tracking`).Scan(&totalTracked)
	stats["total_tracked"] = totalTracked

	// Promoted memories
	var totalPromoted int
	m.db.QueryRow(`SELECT COUNT(*) FROM recall_tracking WHERE promoted_at IS NOT NULL`).Scan(&totalPromoted)
	stats["total_promoted"] = totalPromoted

	// Pending candidates (eligible but not yet promoted)
	var pendingCandidates int
	m.db.QueryRow(`
		SELECT COUNT(*) FROM recall_tracking
		WHERE promoted_at IS NULL
		  AND recall_count >= ?
		  AND unique_queries >= ?
		  AND avg_score >= ?
	`, m.cfg.MinRecallCount, m.cfg.MinUniqueQueries, m.cfg.MinScore).Scan(&pendingCandidates)
	stats["pending_candidates"] = pendingCandidates

	// Average recall count
	var avgRecallCount float64
	m.db.QueryRow(`SELECT AVG(recall_count) FROM recall_tracking WHERE recall_count > 0`).Scan(&avgRecallCount)
	stats["avg_recall_count"] = avgRecallCount

	// Average score
	var avgScore float64
	m.db.QueryRow(`SELECT AVG(avg_score) FROM recall_tracking WHERE avg_score > 0`).Scan(&avgScore)
	stats["avg_score"] = avgScore

	// Top recalled memories
	rows, err := m.db.Query(`
		SELECT memory_id, recall_count, unique_queries, avg_score
		FROM recall_tracking
		WHERE promoted_at IS NULL
		ORDER BY recall_count DESC
		LIMIT 5
	`)
	if err == nil {
		defer rows.Close()
		var topRecalled []map[string]any
		for rows.Next() {
			var memID string
			var recallCount, uniqueQueries int
			var avgScore float64
			if err := rows.Scan(&memID, &recallCount, &uniqueQueries, &avgScore); err == nil {
				topRecalled = append(topRecalled, map[string]any{
					"memory_id":      memID,
					"recall_count":   recallCount,
					"unique_queries": uniqueQueries,
					"avg_score":      avgScore,
				})
			}
		}
		stats["top_recalled"] = topRecalled
	}

	return stats, nil
}

// ResetPromotionStatus clears the promotion status for all memories.
// Use with caution - primarily for testing.
func (m *PromotionManager) ResetPromotionStatus() error {
	_, err := m.db.Exec(`UPDATE recall_tracking SET promoted_at = NULL, promoted_to = NULL`)
	return err
}

// ── Helper Functions ────────────────────────────────────────────────────────

// hashQuery creates a short hash of a query for deduplication.
func hashQuery(query string) string {
	normalized := strings.ToLower(strings.TrimSpace(query))
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:8]) // Use first 8 bytes
}

// averageConfidence computes the average confidence of promotion candidates.
func averageConfidence(candidates []PromotionCandidate) float64 {
	if len(candidates) == 0 {
		return 0.5
	}

	var sum float64
	for _, c := range candidates {
		conf := c.Memory.Confidence
		if conf <= 0 {
			conf = 0.5
		}
		sum += conf
	}
	return sum / float64(len(candidates))
}

// ── SQLite Backend Integration ──────────────────────────────────────────────

// EnablePromotion adds promotion tracking to a SQLiteBackend.
func (b *SQLiteBackend) EnablePromotion(cfg PromotionConfig) *PromotionManager {
	return NewPromotionManager(b, cfg)
}

// SearchWithTracking performs a search and tracks recalls for promotion.
func SearchWithTracking(backend *SQLiteBackend, manager *PromotionManager, query string, limit int) []IndexedMemory {
	results := backend.Search(query, limit)
	if manager != nil && len(results) > 0 {
		manager.Tracker().TrackRecalls(results, query)
	}
	return results
}

// SearchSessionWithTracking performs a session search and tracks recalls.
func SearchSessionWithTracking(backend *SQLiteBackend, manager *PromotionManager, sessionID, query string, limit int) []IndexedMemory {
	results := backend.SearchSession(sessionID, query, limit)
	if manager != nil && len(results) > 0 {
		manager.Tracker().TrackRecalls(results, query)
	}
	return results
}

// ── Cron Integration ────────────────────────────────────────────────────────

// PromotionJob represents a scheduled promotion job.
type PromotionJob struct {
	Manager  *PromotionManager
	Schedule string // Cron expression (e.g., "0 3 * * *")
	LastRun  int64
	Running  bool
	mu       sync.Mutex
}

// NewPromotionJob creates a new promotion job.
func NewPromotionJob(manager *PromotionManager, schedule string) *PromotionJob {
	return &PromotionJob{
		Manager:  manager,
		Schedule: schedule,
	}
}

// Run executes the promotion job.
func (j *PromotionJob) Run() (*PromotionResult, error) {
	j.mu.Lock()
	if j.Running {
		j.mu.Unlock()
		return nil, fmt.Errorf("promotion job already running")
	}
	j.Running = true
	j.mu.Unlock()

	defer func() {
		j.mu.Lock()
		j.Running = false
		j.LastRun = time.Now().Unix()
		j.mu.Unlock()
	}()

	return j.Manager.Promote()
}

// IsRunning returns whether the job is currently running.
func (j *PromotionJob) IsRunning() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Running
}
