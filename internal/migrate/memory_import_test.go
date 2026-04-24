package migrate

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// createTestOpenClawDB creates a mock OpenClaw memory database for testing.
func createTestOpenClawDB(t *testing.T, path string, chunks []OpenClawChunk) {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Create chunks table matching OpenClaw schema
	_, err = db.Exec(`
		CREATE TABLE chunks (
			id TEXT PRIMARY KEY,
			path TEXT,
			source TEXT,
			start_line INTEGER,
			end_line INTEGER,
			hash TEXT,
			model TEXT,
			text TEXT NOT NULL,
			embedding TEXT,
			updated_at INTEGER
		)
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Insert test chunks
	stmt, err := db.Prepare(`
		INSERT INTO chunks (id, path, source, start_line, end_line, hash, model, text, embedding, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()

	for _, chunk := range chunks {
		_, err := stmt.Exec(
			chunk.ID,
			chunk.Path,
			chunk.Source,
			chunk.StartLine,
			chunk.EndLine,
			chunk.Hash,
			chunk.Model,
			chunk.Text,
			chunk.Embedding,
			chunk.UpdatedAt,
		)
		if err != nil {
			t.Fatalf("insert chunk: %v", err)
		}
	}
}

func TestMemoryImporter_FindDatabases(t *testing.T) {
	tmpDir := t.TempDir()

	// Create mock OpenClaw directory structure
	agentDir := filepath.Join(tmpDir, ".openclaw", "agents", "main", "memory")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create a mock database
	dbPath := filepath.Join(agentDir, "main.sqlite")
	createTestOpenClawDB(t, dbPath, []OpenClawChunk{
		{ID: "test-1", Text: "Hello world", UpdatedAt: 1000},
	})

	cfg := MemoryImportConfig{
		SourcePaths: []string{filepath.Join(tmpDir, ".openclaw", "agents", "*", "memory", "*.sqlite")},
		DryRun:      true,
	}

	importer := NewMemoryImporter(cfg)
	databases, err := importer.findDatabases()
	if err != nil {
		t.Fatalf("findDatabases: %v", err)
	}

	if len(databases) != 1 {
		t.Errorf("findDatabases: got %d databases, want 1", len(databases))
	}
}

func TestMemoryImporter_ImportDryRun(t *testing.T) {
	tmpDir := t.TempDir()

	// Create mock OpenClaw database
	agentDir := filepath.Join(tmpDir, ".openclaw", "agents", "test", "memory")
	os.MkdirAll(agentDir, 0755)
	dbPath := filepath.Join(agentDir, "test.sqlite")

	chunks := []OpenClawChunk{
		{
			ID:        "chunk-1",
			Path:      "memory/knowledge.md",
			Source:    "memory",
			Text:      "This is a test memory about golang",
			Hash:      "hash1",
			Model:     "text-embedding-3-small",
			UpdatedAt: 1000,
		},
		{
			ID:        "chunk-2",
			Path:      "memory/preferences.md",
			Source:    "memory",
			Text:      "User prefers dark mode",
			Hash:      "hash2",
			Model:     "text-embedding-3-small",
			UpdatedAt: 2000,
		},
	}
	createTestOpenClawDB(t, dbPath, chunks)

	cfg := MemoryImportConfig{
		SourcePaths: []string{filepath.Join(tmpDir, ".openclaw", "agents", "*", "memory", "*.sqlite")},
		TargetPath:  filepath.Join(tmpDir, "target", "memory.sqlite"),
		Deduplicate: true,
		DryRun:      true,
	}

	importer := NewMemoryImporter(cfg)
	stats, err := importer.Import()
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if stats.DatabasesFound != 1 {
		t.Errorf("DatabasesFound: got %d, want 1", stats.DatabasesFound)
	}
	if stats.ChunksFound != 2 {
		t.Errorf("ChunksFound: got %d, want 2", stats.ChunksFound)
	}
	if stats.ChunksImported != 2 {
		t.Errorf("ChunksImported: got %d, want 2", stats.ChunksImported)
	}
}

func TestMemoryImporter_Import(t *testing.T) {
	tmpDir := t.TempDir()

	// Create mock OpenClaw database
	agentDir := filepath.Join(tmpDir, ".openclaw", "agents", "test", "memory")
	os.MkdirAll(agentDir, 0755)
	dbPath := filepath.Join(agentDir, "test.sqlite")

	embedding, _ := json.Marshal([]float32{0.1, 0.2, 0.3})
	chunks := []OpenClawChunk{
		{
			ID:        "chunk-1",
			Path:      "memory/golang.md",
			Source:    "memory",
			Text:      "Go is a programming language developed by Google",
			Hash:      "hash1",
			Model:     "text-embedding-3-small",
			Embedding: string(embedding),
			UpdatedAt: 1000,
		},
	}
	createTestOpenClawDB(t, dbPath, chunks)

	targetPath := filepath.Join(tmpDir, "target", "memory.sqlite")
	cfg := MemoryImportConfig{
		SourcePaths:    []string{filepath.Join(tmpDir, ".openclaw", "agents", "*", "memory", "*.sqlite")},
		TargetPath:     targetPath,
		Deduplicate:    true,
		CopyEmbeddings: true,
		DryRun:         false,
	}

	importer := NewMemoryImporter(cfg)
	stats, err := importer.Import()
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if stats.DatabasesImported != 1 {
		t.Errorf("DatabasesImported: got %d, want 1", stats.DatabasesImported)
	}
	if stats.ChunksImported != 1 {
		t.Errorf("ChunksImported: got %d, want 1", stats.ChunksImported)
	}
	if stats.EmbeddingsCopied != 1 {
		t.Errorf("EmbeddingsCopied: got %d, want 1", stats.EmbeddingsCopied)
	}

	// Verify target database was created
	if _, err := os.Stat(targetPath); err != nil {
		t.Errorf("Target database not created: %v", err)
	}
}

func TestMemoryImporter_Deduplicate(t *testing.T) {
	tmpDir := t.TempDir()

	// Create two databases with duplicate content
	for _, agent := range []string{"agent1", "agent2"} {
		agentDir := filepath.Join(tmpDir, ".openclaw", "agents", agent, "memory")
		os.MkdirAll(agentDir, 0755)
		dbPath := filepath.Join(agentDir, agent+".sqlite")

		chunks := []OpenClawChunk{
			{
				ID:        agent + "-chunk",
				Path:      "memory/shared.md",
				Source:    "memory",
				Text:      "This is shared content",
				Hash:      "same-hash", // Same hash = duplicate
				UpdatedAt: 1000,
			},
		}
		createTestOpenClawDB(t, dbPath, chunks)
	}

	cfg := MemoryImportConfig{
		SourcePaths: []string{filepath.Join(tmpDir, ".openclaw", "agents", "*", "memory", "*.sqlite")},
		TargetPath:  filepath.Join(tmpDir, "target", "memory.sqlite"),
		Deduplicate: true,
		DryRun:      true,
	}

	importer := NewMemoryImporter(cfg)
	stats, err := importer.Import()
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if stats.DatabasesFound != 2 {
		t.Errorf("DatabasesFound: got %d, want 2", stats.DatabasesFound)
	}
	if stats.ChunksFound != 2 {
		t.Errorf("ChunksFound: got %d, want 2", stats.ChunksFound)
	}
	if stats.ChunksDeduplicated != 1 {
		t.Errorf("ChunksDeduplicated: got %d, want 1", stats.ChunksDeduplicated)
	}
	if stats.ChunksImported != 1 {
		t.Errorf("ChunksImported: got %d, want 1 (one deduped)", stats.ChunksImported)
	}
}

func TestMemoryImporter_SkipEmptyText(t *testing.T) {
	tmpDir := t.TempDir()

	agentDir := filepath.Join(tmpDir, ".openclaw", "agents", "test", "memory")
	os.MkdirAll(agentDir, 0755)
	dbPath := filepath.Join(agentDir, "test.sqlite")

	chunks := []OpenClawChunk{
		{ID: "valid", Text: "Valid text", Hash: "h1", UpdatedAt: 1000},
		{ID: "empty", Text: "", Hash: "h2", UpdatedAt: 1001},
		{ID: "whitespace", Text: "   ", Hash: "h3", UpdatedAt: 1002},
	}
	createTestOpenClawDB(t, dbPath, chunks)

	cfg := MemoryImportConfig{
		SourcePaths: []string{filepath.Join(tmpDir, ".openclaw", "agents", "*", "memory", "*.sqlite")},
		TargetPath:  filepath.Join(tmpDir, "target", "memory.sqlite"),
		DryRun:      true,
	}

	importer := NewMemoryImporter(cfg)
	stats, err := importer.Import()
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if stats.ChunksFound != 3 {
		t.Errorf("ChunksFound: got %d, want 3", stats.ChunksFound)
	}
	if stats.ChunksSkipped != 2 {
		t.Errorf("ChunksSkipped: got %d, want 2", stats.ChunksSkipped)
	}
	if stats.ChunksImported != 1 {
		t.Errorf("ChunksImported: got %d, want 1", stats.ChunksImported)
	}
}

func TestMemoryImporter_EmbeddingModelFilter(t *testing.T) {
	tmpDir := t.TempDir()

	agentDir := filepath.Join(tmpDir, ".openclaw", "agents", "test", "memory")
	os.MkdirAll(agentDir, 0755)
	dbPath := filepath.Join(agentDir, "test.sqlite")

	embedding, _ := json.Marshal([]float32{0.1, 0.2, 0.3})
	chunks := []OpenClawChunk{
		{
			ID:        "chunk-match",
			Text:      "Matching model",
			Hash:      "h1",
			Model:     "text-embedding-3-small",
			Embedding: string(embedding),
			UpdatedAt: 1000,
		},
		{
			ID:        "chunk-nomatch",
			Text:      "Different model",
			Hash:      "h2",
			Model:     "voyage-2",
			Embedding: string(embedding),
			UpdatedAt: 1001,
		},
	}
	createTestOpenClawDB(t, dbPath, chunks)

	cfg := MemoryImportConfig{
		SourcePaths:    []string{filepath.Join(tmpDir, ".openclaw", "agents", "*", "memory", "*.sqlite")},
		TargetPath:     filepath.Join(tmpDir, "target", "memory.sqlite"),
		CopyEmbeddings: true,
		ExpectedModel:  "text-embedding-3-small",
		DryRun:         true,
	}

	importer := NewMemoryImporter(cfg)
	stats, err := importer.Import()
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if stats.EmbeddingsCopied != 1 {
		t.Errorf("EmbeddingsCopied: got %d, want 1", stats.EmbeddingsCopied)
	}
	if stats.EmbeddingsSkipped != 1 {
		t.Errorf("EmbeddingsSkipped: got %d, want 1", stats.EmbeddingsSkipped)
	}
}

func TestScanOpenClawMemoryDatabases(t *testing.T) {
	// This tests the default home directory scan
	// In a real environment, this might find actual databases
	databases, err := ScanOpenClawMemoryDatabases()
	if err != nil {
		t.Fatalf("ScanOpenClawMemoryDatabases: %v", err)
	}

	// Just verify it doesn't error - may or may not find databases
	t.Logf("Found %d OpenClaw memory databases", len(databases))
}

func TestIsSQLiteDatabase(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a valid SQLite database
	sqlitePath := filepath.Join(tmpDir, "valid.sqlite")
	createTestOpenClawDB(t, sqlitePath, nil)

	// Create a non-SQLite file
	textPath := filepath.Join(tmpDir, "not-sqlite.txt")
	os.WriteFile(textPath, []byte("not a database"), 0644)

	importer := NewMemoryImporter(DefaultMemoryImportConfig())

	if !importer.isSQLiteDatabase(sqlitePath) {
		t.Error("isSQLiteDatabase: should detect valid SQLite file")
	}

	if importer.isSQLiteDatabase(textPath) {
		t.Error("isSQLiteDatabase: should reject non-SQLite file")
	}

	if importer.isSQLiteDatabase(filepath.Join(tmpDir, "nonexistent.sqlite")) {
		t.Error("isSQLiteDatabase: should reject nonexistent file")
	}
}

func TestConvertChunk(t *testing.T) {
	importer := NewMemoryImporter(DefaultMemoryImportConfig())

	chunk := OpenClawChunk{
		ID:        "test-chunk",
		Path:      "memory/knowledge/golang.md",
		Source:    "memory",
		Text:      "Go is a great language",
		Hash:      "abc123",
		UpdatedAt: 1234567890,
	}

	doc := importer.convertChunk(chunk)

	if doc.MemoryID != "oc-test-chunk" {
		t.Errorf("MemoryID: got %q, want 'oc-test-chunk'", doc.MemoryID)
	}

	if doc.Topic != "golang" {
		t.Errorf("Topic: got %q, want 'golang'", doc.Topic)
	}

	if doc.Text != "Go is a great language" {
		t.Errorf("Text: got %q, want 'Go is a great language'", doc.Text)
	}

	if doc.Unix != 1234567890 {
		t.Errorf("Unix: got %d, want 1234567890", doc.Unix)
	}

	if doc.Source != "memory" {
		t.Errorf("Source: got %q, want 'memory'", doc.Source)
	}

	// Keywords should include path components and source
	foundMemory := false
	for _, kw := range doc.Keywords {
		if kw == "memory" {
			foundMemory = true
		}
	}
	if !foundMemory {
		t.Errorf("Keywords should include 'memory', got: %v", doc.Keywords)
	}
}
