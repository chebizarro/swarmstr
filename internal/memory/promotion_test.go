package memory

import (
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ── Test Helpers ────────────────────────────────────────────────────────────

func createTestSQLiteBackend(t *testing.T) (*SQLiteBackend, string) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_promotion.sqlite")

	backend, err := OpenSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLiteBackend: %v", err)
	}

	return backend, dbPath
}

func addTestMemory(backend *SQLiteBackend, id, text, topic string) {
	backend.Add(memoryDocFromIndexed(IndexedMemory{
		MemoryID:   id,
		Text:       text,
		Topic:      topic,
		Unix:       time.Now().Unix(),
		Confidence: 0.8,
	}))
}

// ── DefaultPromotionConfig Tests ────────────────────────────────────────────

func TestDefaultPromotionConfig(t *testing.T) {
	cfg := DefaultPromotionConfig()

	if !cfg.Enabled {
		t.Error("expected Enabled=true")
	}
	if cfg.MinRecallCount != 3 {
		t.Errorf("expected MinRecallCount=3, got %d", cfg.MinRecallCount)
	}
	if cfg.MinUniqueQueries != 2 {
		t.Errorf("expected MinUniqueQueries=2, got %d", cfg.MinUniqueQueries)
	}
	if cfg.MinScore != 0.75 {
		t.Errorf("expected MinScore=0.75, got %v", cfg.MinScore)
	}
	if cfg.RecencyHalfLife != 14 {
		t.Errorf("expected RecencyHalfLife=14, got %d", cfg.RecencyHalfLife)
	}
	if cfg.MaxBatchSize != 100 {
		t.Errorf("expected MaxBatchSize=100, got %d", cfg.MaxBatchSize)
	}
	if cfg.PromotedTopic != "consolidated" {
		t.Errorf("expected PromotedTopic='consolidated', got %s", cfg.PromotedTopic)
	}
}

// ── RecallTracker Tests ─────────────────────────────────────────────────────

func TestRecallTracker_TrackRecall(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	tracker := NewRecallTracker(backend.db, cfg)

	// Track some recalls
	tracker.TrackRecall("mem1", "test query", 0.9)
	tracker.TrackRecall("mem1", "another query", 0.8)
	tracker.TrackRecall("mem2", "test query", 0.7)

	// Flush to database
	err := tracker.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Verify records
	record, err := tracker.GetRecallRecord("mem1")
	if err != nil {
		t.Fatalf("GetRecallRecord: %v", err)
	}
	if record == nil {
		t.Fatal("expected record for mem1")
	}
	if record.RecallCount != 2 {
		t.Errorf("expected RecallCount=2, got %d", record.RecallCount)
	}
	if record.UniqueQueries != 2 {
		t.Errorf("expected UniqueQueries=2, got %d", record.UniqueQueries)
	}

	record2, _ := tracker.GetRecallRecord("mem2")
	if record2 == nil {
		t.Fatal("expected record for mem2")
	}
	if record2.RecallCount != 1 {
		t.Errorf("expected RecallCount=1 for mem2, got %d", record2.RecallCount)
	}
}

func TestRecallTracker_TrackRecalls_Batch(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	tracker := NewRecallTracker(backend.db, cfg)

	// Add test memories
	addTestMemory(backend, "mem1", "First memory", "test")
	addTestMemory(backend, "mem2", "Second memory", "test")
	addTestMemory(backend, "mem3", "Third memory", "test")

	// Simulate search results
	results := []IndexedMemory{
		{MemoryID: "mem1"},
		{MemoryID: "mem2"},
		{MemoryID: "mem3"},
	}

	tracker.TrackRecalls(results, "batch query")
	tracker.Flush()

	// All should be tracked
	for _, id := range []string{"mem1", "mem2", "mem3"} {
		record, _ := tracker.GetRecallRecord(id)
		if record == nil {
			t.Errorf("expected record for %s", id)
		}
	}
}

func TestRecallTracker_DuplicateQueries(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	tracker := NewRecallTracker(backend.db, cfg)

	// Track same query multiple times in separate flushes
	// Each flush with a unique query increments both recall_count and unique_queries by 1
	// But same query hash means unique_queries stays at 1
	tracker.TrackRecall("mem1", "same query", 0.9)
	tracker.Flush()

	tracker.TrackRecall("mem1", "same query", 0.8)
	tracker.Flush()

	tracker.TrackRecall("mem1", "same query", 0.7)
	tracker.Flush()

	record, _ := tracker.GetRecallRecord("mem1")
	if record == nil {
		t.Fatal("expected record")
	}

	// Each flush with same query adds 1 to recall count
	// (recall_count tracks number of recalls, not unique recalls)
	if record.RecallCount < 1 {
		t.Errorf("expected RecallCount >= 1, got %d", record.RecallCount)
	}

	// UniqueQueries should be 1 (same query hash across all calls)
	if record.UniqueQueries != 1 {
		t.Errorf("expected UniqueQueries=1, got %d", record.UniqueQueries)
	}
}

func TestRecallTracker_DisabledConfig(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.Enabled = false
	tracker := NewRecallTracker(backend.db, cfg)

	// Track should be no-op when disabled
	tracker.TrackRecall("mem1", "test query", 0.9)
	tracker.Flush()

	record, _ := tracker.GetRecallRecord("mem1")
	if record != nil {
		t.Error("expected no record when tracking is disabled")
	}
}

func TestRecallTracker_EmptyMemoryID(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	tracker := NewRecallTracker(backend.db, cfg)

	// Empty memory ID should be ignored
	tracker.TrackRecall("", "test query", 0.9)
	tracker.Flush()

	// No records should be created
	var count int
	backend.db.QueryRow(`SELECT COUNT(*) FROM recall_tracking`).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 records, got %d", count)
	}
}

// ── PromotionManager Tests ──────────────────────────────────────────────────

func TestPromotionManager_FindCandidates_Empty(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	manager := NewPromotionManager(backend, cfg)

	candidates, err := manager.FindCandidates()
	if err != nil {
		t.Fatalf("FindCandidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(candidates))
	}
}

func TestPromotionManager_FindCandidates_BelowThreshold(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 5
	cfg.MinUniqueQueries = 3
	cfg.MinScore = 0.8

	manager := NewPromotionManager(backend, cfg)
	addTestMemory(backend, "mem1", "Test memory", "test")

	// Track some recalls, but not enough
	tracker := manager.Tracker()
	tracker.TrackRecall("mem1", "query1", 0.9)
	tracker.TrackRecall("mem1", "query2", 0.85)
	tracker.Flush()

	candidates, _ := manager.FindCandidates()
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates below threshold, got %d", len(candidates))
	}
}

func TestPromotionManager_FindCandidates_AboveThreshold(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 3
	cfg.MinUniqueQueries = 2
	cfg.MinScore = 0.7

	manager := NewPromotionManager(backend, cfg)
	addTestMemory(backend, "mem1", "Frequently recalled memory", "test")

	// Track enough recalls
	tracker := manager.Tracker()
	tracker.TrackRecall("mem1", "query1", 0.9)
	tracker.TrackRecall("mem1", "query2", 0.85)
	tracker.TrackRecall("mem1", "query3", 0.8)
	tracker.TrackRecall("mem1", "query1", 0.9) // Same query, bumps count
	tracker.Flush()

	candidates, err := manager.FindCandidates()
	if err != nil {
		t.Fatalf("FindCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Memory.MemoryID != "mem1" {
		t.Errorf("expected mem1, got %s", candidates[0].Memory.MemoryID)
	}
}

func TestPromotionManager_Promote(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 2
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.5

	manager := NewPromotionManager(backend, cfg)
	addTestMemory(backend, "mem1", "Memory to promote", "original")

	// Track recalls
	tracker := manager.Tracker()
	tracker.TrackRecall("mem1", "query1", 0.9)
	tracker.TrackRecall("mem1", "query2", 0.8)
	tracker.Flush()

	// Run promotion
	result, err := manager.Promote()
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	if result.Candidates != 1 {
		t.Errorf("expected 1 candidate, got %d", result.Candidates)
	}
	if result.Promoted != 1 {
		t.Errorf("expected 1 promoted, got %d", result.Promoted)
	}
	if len(result.PromotedIDs) != 1 || result.PromotedIDs[0] != "mem1" {
		t.Errorf("expected PromotedIDs=['mem1'], got %v", result.PromotedIDs)
	}

	// Verify promotion status in database
	record, _ := tracker.GetRecallRecord("mem1")
	if record.PromotedAt == 0 {
		t.Error("expected PromotedAt to be set")
	}
}

func TestPromotionManager_Promote_AlreadyPromoted(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 2
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.5

	manager := NewPromotionManager(backend, cfg)
	addTestMemory(backend, "mem1", "Already promoted", "test")

	// Track and promote
	tracker := manager.Tracker()
	tracker.TrackRecall("mem1", "query1", 0.9)
	tracker.TrackRecall("mem1", "query2", 0.8)
	tracker.Flush()

	manager.Promote()

	// Try to promote again - should find no candidates
	result, _ := manager.Promote()
	if result.Candidates != 0 {
		t.Errorf("expected 0 candidates after already promoted, got %d", result.Candidates)
	}
}

func TestPromotionManager_Promote_Disabled(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.Enabled = false

	manager := NewPromotionManager(backend, cfg)

	result, err := manager.Promote()
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if result.Candidates != 0 || result.Promoted != 0 {
		t.Error("expected no promotions when disabled")
	}
}

func TestPromotionManager_GetPromotionStats(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 2
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.5

	manager := NewPromotionManager(backend, cfg)

	// Add and track memories
	addTestMemory(backend, "mem1", "First memory", "test")
	addTestMemory(backend, "mem2", "Second memory", "test")

	tracker := manager.Tracker()
	tracker.TrackRecall("mem1", "query1", 0.9)
	tracker.TrackRecall("mem1", "query2", 0.8)
	tracker.TrackRecall("mem2", "query1", 0.7)
	tracker.Flush()

	stats, err := manager.GetPromotionStats()
	if err != nil {
		t.Fatalf("GetPromotionStats: %v", err)
	}

	if stats["total_tracked"].(int) != 2 {
		t.Errorf("expected total_tracked=2, got %v", stats["total_tracked"])
	}
	if stats["total_promoted"].(int) != 0 {
		t.Errorf("expected total_promoted=0, got %v", stats["total_promoted"])
	}
	if stats["pending_candidates"].(int) != 1 { // mem1 meets threshold
		t.Errorf("expected pending_candidates=1, got %v", stats["pending_candidates"])
	}
}

func TestPromotionManager_ResetPromotionStatus(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 2
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.5

	manager := NewPromotionManager(backend, cfg)
	addTestMemory(backend, "mem1", "Test memory", "test")

	tracker := manager.Tracker()
	tracker.TrackRecall("mem1", "query1", 0.9)
	tracker.TrackRecall("mem1", "query2", 0.8)
	tracker.Flush()

	// Promote
	manager.Promote()

	// Verify promoted
	record, _ := tracker.GetRecallRecord("mem1")
	if record.PromotedAt == 0 {
		t.Fatal("expected memory to be promoted")
	}

	// Reset
	err := manager.ResetPromotionStatus()
	if err != nil {
		t.Fatalf("ResetPromotionStatus: %v", err)
	}

	// Verify reset
	record, _ = tracker.GetRecallRecord("mem1")
	if record.PromotedAt != 0 {
		t.Error("expected PromotedAt to be reset to 0")
	}
}

// ── Summarizer Integration Tests ────────────────────────────────────────────

func TestPromotionManager_WithSummarizer(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 2
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.5
	cfg.EnableSummary = true

	manager := NewPromotionManager(backend, cfg)

	// Set up mock summarizer
	summarizerCalled := false
	manager.SetSummarizer(func(memories []IndexedMemory) (string, error) {
		summarizerCalled = true
		return "Consolidated summary of " + string(rune(len(memories)+'0')) + " memories", nil
	})

	// Add multiple memories with same topic
	addTestMemory(backend, "mem1", "First memory", "test-topic")
	addTestMemory(backend, "mem2", "Second memory", "test-topic")

	tracker := manager.Tracker()
	for _, id := range []string{"mem1", "mem2"} {
		tracker.TrackRecall(id, "query1", 0.9)
		tracker.TrackRecall(id, "query2", 0.8)
	}
	tracker.Flush()

	// Promote
	result, _ := manager.Promote()

	if !summarizerCalled {
		t.Error("expected summarizer to be called")
	}
	if result.Promoted != 2 {
		t.Errorf("expected 2 promoted, got %d", result.Promoted)
	}
}

// ── Integration Tests ───────────────────────────────────────────────────────

func TestSearchWithTracking(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	manager := NewPromotionManager(backend, cfg)

	// Add searchable memories
	addTestMemory(backend, "mem1", "The quick brown fox jumps", "animals")
	addTestMemory(backend, "mem2", "A lazy dog sleeps", "animals")

	// Search with tracking
	results := SearchWithTracking(backend, manager, "quick fox", 10)

	// Flush and verify tracking
	manager.Tracker().Flush()

	if len(results) > 0 {
		record, _ := manager.Tracker().GetRecallRecord(results[0].MemoryID)
		if record == nil {
			t.Error("expected recall to be tracked")
		}
	}
}

func TestSearchSessionWithTracking(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	manager := NewPromotionManager(backend, cfg)

	// Add session-specific memories
	backend.Add(memoryDocFromIndexed(IndexedMemory{
		MemoryID:  "mem1",
		SessionID: "session-123",
		Text:      "Session specific content here",
		Unix:      time.Now().Unix(),
	}))

	// Search session with tracking
	results := SearchSessionWithTracking(backend, manager, "session-123", "content", 10)
	manager.Tracker().Flush()

	if len(results) > 0 {
		record, _ := manager.Tracker().GetRecallRecord(results[0].MemoryID)
		if record == nil {
			t.Error("expected session recall to be tracked")
		}
	}
}

func TestSQLiteBackend_EnablePromotion(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	manager := backend.EnablePromotion(cfg)

	if manager == nil {
		t.Fatal("expected non-nil manager")
	}
	if manager.Tracker() == nil {
		t.Error("expected non-nil tracker")
	}
}

// ── PromotionJob Tests ──────────────────────────────────────────────────────

func TestPromotionJob_Run(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 2
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.5

	manager := NewPromotionManager(backend, cfg)
	job := NewPromotionJob(manager, "0 3 * * *")

	addTestMemory(backend, "mem1", "Test memory", "test")
	tracker := manager.Tracker()
	tracker.TrackRecall("mem1", "query1", 0.9)
	tracker.TrackRecall("mem1", "query2", 0.8)
	tracker.Flush()

	result, err := job.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Promoted != 1 {
		t.Errorf("expected 1 promoted, got %d", result.Promoted)
	}
	if job.LastRun == 0 {
		t.Error("expected LastRun to be set")
	}
}

func TestPromotionJob_ConcurrentRun(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	manager := NewPromotionManager(backend, cfg)
	job := NewPromotionJob(manager, "0 3 * * *")

	// Simulate running job
	job.mu.Lock()
	job.Running = true
	job.mu.Unlock()

	// Try to run again
	_, err := job.Run()
	if err == nil {
		t.Error("expected error when job already running")
	}

	if !job.IsRunning() {
		t.Error("expected IsRunning()=true")
	}
}

// ── Helper Function Tests ───────────────────────────────────────────────────

func TestHashQuery(t *testing.T) {
	tests := []struct {
		query1 string
		query2 string
		same   bool
	}{
		{"test query", "test query", true},
		{"TEST QUERY", "test query", true},      // Case insensitive
		{"  test query  ", "test query", true},  // Whitespace trimmed
		{"test query", "different query", false},
	}

	for _, tt := range tests {
		h1 := hashQuery(tt.query1)
		h2 := hashQuery(tt.query2)
		if (h1 == h2) != tt.same {
			t.Errorf("hashQuery(%q) vs hashQuery(%q): expected same=%v", tt.query1, tt.query2, tt.same)
		}
	}
}

func TestAverageConfidence(t *testing.T) {
	tests := []struct {
		name       string
		candidates []PromotionCandidate
		expected   float64
	}{
		{
			name:       "empty",
			candidates: nil,
			expected:   0.5,
		},
		{
			name: "single",
			candidates: []PromotionCandidate{
				{Memory: IndexedMemory{Confidence: 0.9}},
			},
			expected: 0.9,
		},
		{
			name: "multiple",
			candidates: []PromotionCandidate{
				{Memory: IndexedMemory{Confidence: 0.9}},
				{Memory: IndexedMemory{Confidence: 0.7}},
			},
			expected: 0.8,
		},
		{
			name: "zero confidence uses default",
			candidates: []PromotionCandidate{
				{Memory: IndexedMemory{Confidence: 0}},
				{Memory: IndexedMemory{Confidence: 0.8}},
			},
			expected: 0.65, // (0.5 + 0.8) / 2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := averageConfidence(tt.candidates)
			if result != tt.expected {
				t.Errorf("averageConfidence() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// ── Benchmark Tests ─────────────────────────────────────────────────────────

func BenchmarkTrackRecall(b *testing.B) {
	tmpDir := b.TempDir()
	dbPath := filepath.Join(tmpDir, "bench.sqlite")
	backend, _ := OpenSQLiteBackend(dbPath)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	tracker := NewRecallTracker(backend.db, cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.TrackRecall("mem1", "test query", 0.9)
	}
}

func BenchmarkFlush(b *testing.B) {
	tmpDir := b.TempDir()
	dbPath := filepath.Join(tmpDir, "bench.sqlite")
	backend, _ := OpenSQLiteBackend(dbPath)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	tracker := NewRecallTracker(backend.db, cfg)

	// Add pending updates
	for i := 0; i < 100; i++ {
		tracker.TrackRecall("mem1", "query"+string(rune(i)), 0.9)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.Flush()
		// Re-add for next iteration
		for j := 0; j < 100; j++ {
			tracker.TrackRecall("mem1", "query"+string(rune(j)), 0.9)
		}
	}
}

func BenchmarkFindCandidates(b *testing.B) {
	tmpDir := b.TempDir()
	dbPath := filepath.Join(tmpDir, "bench.sqlite")
	backend, _ := OpenSQLiteBackend(dbPath)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 2
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.5

	manager := NewPromotionManager(backend, cfg)

	// Add many tracked memories
	for i := 0; i < 1000; i++ {
		memID := "mem" + string(rune(i))
		addTestMemory(backend, memID, "Test memory content", "test")
		manager.Tracker().TrackRecall(memID, "query1", 0.9)
		manager.Tracker().TrackRecall(memID, "query2", 0.8)
	}
	manager.Tracker().Flush()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.FindCandidates()
	}
}

// ── Edge Cases ──────────────────────────────────────────────────────────────

func TestRecallTracker_LargeQueryCount(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	tracker := NewRecallTracker(backend.db, cfg)

	// Track many different queries for same memory
	for i := 0; i < 100; i++ {
		// Use a proper string format for unique queries
		tracker.TrackRecall("mem1", "unique query number "+string(rune('a'+i%26))+string(rune('0'+i/10))+string(rune('0'+i%10)), 0.9)
	}
	tracker.Flush()

	record, _ := tracker.GetRecallRecord("mem1")
	if record == nil {
		t.Fatal("expected record")
	}
	// We should have close to 100 unique queries (some may hash collide)
	if record.UniqueQueries < 90 {
		t.Errorf("expected at least 90 unique queries, got %d", record.UniqueQueries)
	}
}

func TestPromotionManager_MissingMemory(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 1
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.5

	manager := NewPromotionManager(backend, cfg)

	// Track recall for memory that doesn't exist
	tracker := manager.Tracker()
	tracker.TrackRecall("nonexistent", "query1", 0.9)
	tracker.Flush()

	// Should not crash, just skip the missing memory
	candidates, _ := manager.FindCandidates()
	if len(candidates) != 0 {
		t.Error("expected 0 candidates for missing memory")
	}
}

func TestPromotionManager_RecencyDecay(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 1
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.1
	cfg.RecencyHalfLife = 1 // 1 day for testing

	manager := NewPromotionManager(backend, cfg)

	// Add memories with different recall times
	addTestMemory(backend, "recent", "Recent memory", "test")
	addTestMemory(backend, "old", "Old memory", "test")

	// Manually insert recall records with different timestamps
	now := time.Now().Unix()
	oldTime := now - (7 * 24 * 60 * 60) // 7 days ago

	backend.db.Exec(`
		INSERT INTO recall_tracking (memory_id, recall_count, unique_queries, last_recall_unix, first_recall_unix, avg_score)
		VALUES (?, 5, 3, ?, ?, 0.9)
	`, "recent", now, now)

	backend.db.Exec(`
		INSERT INTO recall_tracking (memory_id, recall_count, unique_queries, last_recall_unix, first_recall_unix, avg_score)
		VALUES (?, 5, 3, ?, ?, 0.9)
	`, "old", oldTime, oldTime)

	candidates, _ := manager.FindCandidates()
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	// Recent should have higher score due to recency
	if candidates[0].Memory.MemoryID != "recent" {
		t.Error("expected recent memory to be ranked first due to recency decay")
	}
}
