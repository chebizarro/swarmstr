package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func newTestSQLiteBackend(t *testing.T) *SQLiteBackend {
	t.Helper()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test-memory.sqlite")
	backend, err := OpenSQLiteBackend(path)
	if err != nil {
		t.Fatalf("OpenSQLiteBackend: %v", err)
	}
	t.Cleanup(func() { backend.Close() })
	return backend
}

func TestSQLiteBackend_AddAndSearch(t *testing.T) {
	b := newTestSQLiteBackend(t)

	// Add some memories
	b.Add(state.MemoryDoc{
		MemoryID:  "mem-1",
		SessionID: "session-1",
		Text:      "The quick brown fox jumps over the lazy dog",
		Keywords:  []string{"fox", "dog"},
		Unix:      time.Now().Unix(),
	})
	b.Add(state.MemoryDoc{
		MemoryID:  "mem-2",
		SessionID: "session-1",
		Text:      "Hello world, this is a test memory",
		Keywords:  []string{"hello", "test"},
		Unix:      time.Now().Unix(),
	})
	b.Add(state.MemoryDoc{
		MemoryID:  "mem-3",
		SessionID: "session-2",
		Text:      "Another memory about foxes in the wild",
		Keywords:  []string{"fox", "wild"},
		Unix:      time.Now().Unix(),
	})

	// Test search
	results := b.Search("fox", 10)
	if len(results) != 2 {
		t.Errorf("Search 'fox': got %d results, want 2", len(results))
	}

	// Test session search
	results = b.SearchSession("session-1", "fox", 10)
	if len(results) != 1 {
		t.Errorf("SearchSession 'session-1' 'fox': got %d results, want 1", len(results))
	}

	// Test count
	if count := b.Count(); count != 3 {
		t.Errorf("Count: got %d, want 3", count)
	}

	// Test session count
	if count := b.SessionCount(); count != 2 {
		t.Errorf("SessionCount: got %d, want 2", count)
	}
}

func TestSQLiteBackend_Store(t *testing.T) {
	b := newTestSQLiteBackend(t)

	id := b.Store("test-session", "This is stored text", []string{"tag1", "tag2"})
	if id == "" {
		t.Error("Store returned empty ID")
	}

	results := b.Search("stored", 10)
	if len(results) != 1 {
		t.Errorf("Search 'stored': got %d results, want 1", len(results))
	}
	if results[0].MemoryID != id {
		t.Errorf("Search result ID: got %s, want %s", results[0].MemoryID, id)
	}
}

func TestSQLiteBackend_Delete(t *testing.T) {
	b := newTestSQLiteBackend(t)

	b.Add(state.MemoryDoc{
		MemoryID: "to-delete",
		Text:     "This will be deleted",
		Unix:     time.Now().Unix(),
	})

	if count := b.Count(); count != 1 {
		t.Fatalf("Count before delete: got %d, want 1", count)
	}

	if !b.Delete("to-delete") {
		t.Error("Delete returned false")
	}

	if count := b.Count(); count != 0 {
		t.Errorf("Count after delete: got %d, want 0", count)
	}

	// Delete non-existent should return false
	if b.Delete("non-existent") {
		t.Error("Delete non-existent returned true")
	}
}

func TestSQLiteBackend_ListSession(t *testing.T) {
	b := newTestSQLiteBackend(t)

	now := time.Now().Unix()
	b.Add(state.MemoryDoc{
		MemoryID:  "mem-1",
		SessionID: "session-a",
		Text:      "First message",
		Unix:      now - 100,
	})
	b.Add(state.MemoryDoc{
		MemoryID:  "mem-2",
		SessionID: "session-a",
		Text:      "Second message",
		Unix:      now - 50,
	})
	b.Add(state.MemoryDoc{
		MemoryID:  "mem-3",
		SessionID: "session-b",
		Text:      "Different session",
		Unix:      now,
	})

	results := b.ListSession("session-a", 10)
	if len(results) != 2 {
		t.Fatalf("ListSession: got %d results, want 2", len(results))
	}

	// Should be ordered by unix DESC (newest first)
	if results[0].MemoryID != "mem-2" {
		t.Errorf("ListSession first result: got %s, want mem-2", results[0].MemoryID)
	}
}

func TestSQLiteBackend_ListByTopic(t *testing.T) {
	b := newTestSQLiteBackend(t)

	b.Add(state.MemoryDoc{
		MemoryID: "mem-1",
		Topic:    "golang",
		Text:     "Go is a programming language",
		Unix:     time.Now().Unix(),
	})
	b.Add(state.MemoryDoc{
		MemoryID: "mem-2",
		Topic:    "python",
		Text:     "Python is also a programming language",
		Unix:     time.Now().Unix(),
	})
	b.Add(state.MemoryDoc{
		MemoryID: "mem-3",
		Topic:    "golang",
		Text:     "Go has great concurrency",
		Unix:     time.Now().Unix(),
	})

	results := b.ListByTopic("golang", 10)
	if len(results) != 2 {
		t.Errorf("ListByTopic 'golang': got %d results, want 2", len(results))
	}
}

func TestSQLiteBackend_ListByType(t *testing.T) {
	b := newTestSQLiteBackend(t)

	b.Add(state.MemoryDoc{
		MemoryID: "mem-1",
		Type:     "episodic",
		Text:     "An episodic memory",
		Unix:     time.Now().Unix(),
	})
	b.Add(state.MemoryDoc{
		MemoryID: "mem-2",
		Type:     "semantic",
		Text:     "A semantic memory",
		Unix:     time.Now().Unix(),
	})

	results := b.ListByType("episodic", 10)
	if len(results) != 1 {
		t.Errorf("ListByType 'episodic': got %d results, want 1", len(results))
	}
}

func TestSQLiteBackend_Compact(t *testing.T) {
	b := newTestSQLiteBackend(t)

	now := time.Now().Unix()
	for i := 0; i < 10; i++ {
		b.Add(state.MemoryDoc{
			MemoryID: GenerateMemoryID(),
			Text:     "Test memory content",
			Unix:     now + int64(i), // Newer ones have higher unix
		})
	}

	if count := b.Count(); count != 10 {
		t.Fatalf("Count before compact: got %d, want 10", count)
	}

	removed := b.Compact(5)
	if removed != 5 {
		t.Errorf("Compact(5): removed %d, want 5", removed)
	}

	if count := b.Count(); count != 5 {
		t.Errorf("Count after compact: got %d, want 5", count)
	}
}

func TestSQLiteBackend_MigrateFromJSONIndex(t *testing.T) {
	b := newTestSQLiteBackend(t)
	tmpDir := t.TempDir()

	// Create a mock JSON index file
	jsonPath := filepath.Join(tmpDir, "memory-index.json")
	jsonContent := `{
		"docs": [
			{"memory_id": "json-1", "text": "Migrated memory one", "unix": 1000},
			{"memory_id": "json-2", "text": "Migrated memory two", "unix": 2000}
		]
	}`
	if err := os.WriteFile(jsonPath, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := b.MigrateFromJSONIndex(jsonPath); err != nil {
		t.Fatalf("MigrateFromJSONIndex: %v", err)
	}

	if count := b.Count(); count != 2 {
		t.Errorf("Count after migration: got %d, want 2", count)
	}

	results := b.Search("migrated", 10)
	if len(results) != 2 {
		t.Errorf("Search 'migrated': got %d results, want 2", len(results))
	}
}

func TestSQLiteBackend_MemoryStatus(t *testing.T) {
	b := newTestSQLiteBackend(t)

	status := b.MemoryStatus()
	if status.Kind != "sqlite" {
		t.Errorf("MemoryStatus.Kind: got %s, want sqlite", status.Kind)
	}
	if !status.Primary.Available {
		t.Error("MemoryStatus.Primary.Available: got false, want true")
	}
}

func TestSQLiteBackend_Stats(t *testing.T) {
	b := newTestSQLiteBackend(t)

	b.Add(state.MemoryDoc{
		MemoryID:  "mem-1",
		SessionID: "session-1",
		Text:      "Test memory",
		Unix:      time.Now().Unix(),
	})

	stats := b.Stats()
	if stats["backend"] != "sqlite" {
		t.Errorf("Stats backend: got %v, want sqlite", stats["backend"])
	}
	if stats["total_chunks"].(int) != 1 {
		t.Errorf("Stats total_chunks: got %v, want 1", stats["total_chunks"])
	}
}

func TestBuildFTSQuery(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello world", `"hello" AND "world"`},
		{"", ""},
		{"a", ""}, // Too short after filtering
		{"the is", ""}, // All stopwords
		{"golang programming", `"golang" AND "programming"`},
	}

	for _, tc := range tests {
		got := buildFTSQuery(tc.input)
		if got != tc.expected {
			t.Errorf("buildFTSQuery(%q): got %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestSQLiteBackend_EpisodicFields(t *testing.T) {
	b := newTestSQLiteBackend(t)

	b.Add(state.MemoryDoc{
		MemoryID:    "episodic-1",
		Text:        "Completed task X successfully",
		Unix:        time.Now().Unix(),
		Type:        "episodic",
		GoalID:      "goal-123",
		TaskID:      "task-456",
		RunID:       "run-789",
		EpisodeKind: "success",
	})

	results := b.ListByTaskID("task-456", 10)
	if len(results) != 1 {
		t.Fatalf("ListByTaskID: got %d results, want 1", len(results))
	}

	m := results[0]
	if m.Type != "episodic" {
		t.Errorf("Type: got %s, want episodic", m.Type)
	}
	if m.GoalID != "goal-123" {
		t.Errorf("GoalID: got %s, want goal-123", m.GoalID)
	}
	if m.TaskID != "task-456" {
		t.Errorf("TaskID: got %s, want task-456", m.TaskID)
	}
	if m.RunID != "run-789" {
		t.Errorf("RunID: got %s, want run-789", m.RunID)
	}
	if m.EpisodeKind != "success" {
		t.Errorf("EpisodeKind: got %s, want success", m.EpisodeKind)
	}
}

func TestSQLiteBackend_InvalidationFields(t *testing.T) {
	b := newTestSQLiteBackend(t)

	now := time.Now().Unix()
	b.Add(state.MemoryDoc{
		MemoryID:         "inv-1",
		Text:             "This memory is superseded",
		Unix:             now,
		MemStatus:        state.MemStatusSuperseded,
		SupersededBy:     "inv-2",
		InvalidatedAt:    now,
		InvalidatedBy:    "user-123",
		InvalidateReason: "outdated information",
	})

	results := b.Search("superseded", 10)
	if len(results) != 1 {
		t.Fatalf("Search: got %d results, want 1", len(results))
	}

	m := results[0]
	if m.MemStatus != state.MemStatusSuperseded {
		t.Errorf("MemStatus: got %s, want %s", m.MemStatus, state.MemStatusSuperseded)
	}
	if m.SupersededBy != "inv-2" {
		t.Errorf("SupersededBy: got %s, want inv-2", m.SupersededBy)
	}
	if m.InvalidatedBy != "user-123" {
		t.Errorf("InvalidatedBy: got %s, want user-123", m.InvalidatedBy)
	}
}

func TestSQLiteBackend_MetadataFields(t *testing.T) {
	b := newTestSQLiteBackend(t)

	now := time.Now().Unix()
	b.Add(state.MemoryDoc{
		MemoryID:   "meta-1",
		Text:       "Memory with metadata",
		Unix:       now,
		Confidence: 0.95,
		Source:     "user",
		ReviewedAt: now,
		ReviewedBy: "reviewer-1",
		ExpiresAt:  now + 86400,
	})

	results := b.Search("metadata", 10)
	if len(results) != 1 {
		t.Fatalf("Search: got %d results, want 1", len(results))
	}

	m := results[0]
	if m.Confidence != 0.95 {
		t.Errorf("Confidence: got %f, want 0.95", m.Confidence)
	}
	if m.Source != "user" {
		t.Errorf("Source: got %s, want user", m.Source)
	}
	if m.ReviewedAt != now {
		t.Errorf("ReviewedAt: got %d, want %d", m.ReviewedAt, now)
	}
}
