// Package migrate — OpenClaw SQLite memory import.
//
// This file provides functionality to import memories from OpenClaw's SQLite
// databases into swarmstr's new SQLite backend. OpenClaw stores memory in:
//   ~/.openclaw/agents/<id>/memory/<id>.sqlite
//
// The importer:
//  1. Scans for OpenClaw memory databases
//  2. Reads chunks from each database
//  3. Maps OpenClaw fields to swarmstr's IndexedMemory format
//  4. Deduplicates by content hash
//  5. Imports into swarmstr's SQLite backend
package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"metiq/internal/memory"
	"metiq/internal/store/state"

	_ "modernc.org/sqlite"
)

// MemoryImportConfig configures the OpenClaw memory import.
type MemoryImportConfig struct {
	// SourcePaths are glob patterns for OpenClaw memory databases.
	// Default: ~/.openclaw/agents/*/memory/*.sqlite
	SourcePaths []string `json:"source_paths,omitempty"`

	// TargetPath is the target database path.
	// Default: ~/.metiq/memory.sqlite
	TargetPath string `json:"target_path,omitempty"`

	// Backend is the target memory backend (sqlite, json-fts, qdrant).
	// Default: "sqlite"
	Backend string `json:"backend,omitempty"`

	// Deduplicate removes duplicate entries by content hash.
	// Default: true
	Deduplicate bool `json:"deduplicate"`

	// CopyEmbeddings copies embeddings if the model matches.
	// Default: true
	CopyEmbeddings bool `json:"copy_embeddings"`

	// ExpectedModel is the embedding model name to match for copying embeddings.
	// If empty, embeddings are always copied.
	ExpectedModel string `json:"expected_model,omitempty"`

	// DryRun simulates the import without writing.
	DryRun bool `json:"dry_run"`

	// Verbose enables detailed logging.
	Verbose bool `json:"verbose"`

	// Classify configures optional LLM classification.
	Classify ClassifyConfig `json:"classify,omitempty"`
}

// DefaultMemoryImportConfig returns sensible defaults.
func DefaultMemoryImportConfig() MemoryImportConfig {
	home, _ := os.UserHomeDir()
	return MemoryImportConfig{
		SourcePaths: []string{
			filepath.Join(home, ".openclaw", "agents", "*", "memory", "*.sqlite"),
		},
		TargetPath:     filepath.Join(home, ".metiq", "memory.sqlite"),
		Backend:        "sqlite",
		Deduplicate:    true,
		CopyEmbeddings: true,
	}
}

// MemoryImportStats reports import statistics.
type MemoryImportStats struct {
	DatabasesFound     int      `json:"databases_found"`
	DatabasesImported  int      `json:"databases_imported"`
	ChunksFound        int      `json:"chunks_found"`
	ChunksImported     int      `json:"chunks_imported"`
	ChunksSkipped      int      `json:"chunks_skipped"`
	ChunksDeduplicated int      `json:"chunks_deduplicated"`
	EmbeddingsCopied   int      `json:"embeddings_copied"`
	EmbeddingsSkipped  int      `json:"embeddings_skipped"`
	ChunksClassified   int      `json:"chunks_classified,omitempty"`
	ClassifyErrors     int      `json:"classify_errors,omitempty"`
	Errors             []string `json:"errors,omitempty"`
	DurationMs         int64    `json:"duration_ms"`
}

// OpenClawChunk represents a chunk from OpenClaw's chunks table.
type OpenClawChunk struct {
	ID        string
	Path      string
	Source    string
	StartLine int
	EndLine   int
	Hash      string
	Model     string
	Text      string
	Embedding string // JSON array
	UpdatedAt int64
}

// MemoryImporter handles OpenClaw → swarmstr memory import.
type MemoryImporter struct {
	cfg        MemoryImportConfig
	stats      MemoryImportStats
	seen       map[string]bool // content hash → already imported
	classifier *MemoryClassifier
}

// NewMemoryImporter creates a new memory importer.
func NewMemoryImporter(cfg MemoryImportConfig) *MemoryImporter {
	im := &MemoryImporter{
		cfg:  cfg,
		seen: make(map[string]bool),
	}
	if cfg.Classify.Enabled {
		im.classifier = NewMemoryClassifier(cfg.Classify)
	}
	return im
}

// Import runs the memory import process.
func (m *MemoryImporter) Import() (*MemoryImportStats, error) {
	start := time.Now()
	defer func() {
		m.stats.DurationMs = time.Since(start).Milliseconds()
	}()

	// Find all OpenClaw memory databases
	databases, err := m.findDatabases()
	if err != nil {
		return &m.stats, fmt.Errorf("find databases: %w", err)
	}
	m.stats.DatabasesFound = len(databases)

	if len(databases) == 0 {
		return &m.stats, nil
	}

	// Determine backend to use
	backend := m.cfg.Backend
	if backend == "" {
		backend = "sqlite"
	}

	// Open target backend (unless dry run)
	var target memory.Backend
	if !m.cfg.DryRun {
		target, err = memory.OpenBackend(backend, m.cfg.TargetPath)
		if err != nil {
			return &m.stats, fmt.Errorf("open target backend %q: %w", backend, err)
		}
		defer target.Close()
	}

	// Import each database
	for _, dbPath := range databases {
		if err := m.importDatabase(dbPath, target); err != nil {
			m.stats.Errors = append(m.stats.Errors, fmt.Sprintf("%s: %v", dbPath, err))
			continue
		}
		m.stats.DatabasesImported++
	}

	// Rebuild FTS index after import (SQLite backend only)
	if sqliteTarget, ok := target.(*memory.SQLiteBackend); ok && m.stats.ChunksImported > 0 {
		if err := sqliteTarget.RebuildFTSIndex(); err != nil {
			m.stats.Errors = append(m.stats.Errors, fmt.Sprintf("rebuild FTS: %v", err))
		}
	}

	// Save backend state
	if target != nil {
		if err := target.Save(); err != nil {
			m.stats.Errors = append(m.stats.Errors, fmt.Sprintf("save backend: %v", err))
		}
	}

	return &m.stats, nil
}

// findDatabases finds OpenClaw memory databases matching the source patterns.
func (m *MemoryImporter) findDatabases() ([]string, error) {
	var databases []string
	seen := make(map[string]bool)

	for _, pattern := range m.cfg.SourcePaths {
		// Expand ~ to home directory
		if strings.HasPrefix(pattern, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				continue
			}
			pattern = filepath.Join(home, pattern[2:])
		}

		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}

		for _, match := range matches {
			// Resolve to absolute path for deduplication
			abs, err := filepath.Abs(match)
			if err != nil {
				continue
			}

			if seen[abs] {
				continue
			}
			seen[abs] = true

			// Verify it's a SQLite database
			if !m.isSQLiteDatabase(abs) {
				continue
			}

			databases = append(databases, abs)
		}
	}

	return databases, nil
}

// isSQLiteDatabase checks if a file looks like a SQLite database.
func (m *MemoryImporter) isSQLiteDatabase(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// SQLite magic bytes: "SQLite format 3\x00"
	header := make([]byte, 16)
	if _, err := f.Read(header); err != nil {
		return false
	}

	return string(header[:15]) == "SQLite format 3"
}

// importDatabase imports a single OpenClaw database.
func (m *MemoryImporter) importDatabase(dbPath string, target memory.Backend) error {
	// Open source database
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	// Check if chunks table exists
	var tableName string
	err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='chunks'`).Scan(&tableName)
	if err != nil {
		return fmt.Errorf("no chunks table found")
	}

	// Read all chunks
	rows, err := db.Query(`
		SELECT id, path, source, start_line, end_line, hash, model, text, embedding, updated_at
		FROM chunks
		ORDER BY updated_at ASC
	`)
	if err != nil {
		return fmt.Errorf("query chunks: %w", err)
	}
	defer rows.Close()

	// Collect all chunks first (needed for classification batching)
	var chunks []OpenClawChunk
	for rows.Next() {
		var chunk OpenClawChunk
		var embedding sql.NullString

		err := rows.Scan(
			&chunk.ID,
			&chunk.Path,
			&chunk.Source,
			&chunk.StartLine,
			&chunk.EndLine,
			&chunk.Hash,
			&chunk.Model,
			&chunk.Text,
			&embedding,
			&chunk.UpdatedAt,
		)
		if err != nil {
			m.stats.Errors = append(m.stats.Errors, fmt.Sprintf("scan row: %v", err))
			continue
		}

		if embedding.Valid {
			chunk.Embedding = embedding.String
		}

		m.stats.ChunksFound++
		chunks = append(chunks, chunk)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	// Classify chunks in batches if classifier is enabled
	var classifications map[int]ClassifyResult
	if m.classifier != nil && !m.cfg.DryRun {
		classifications = m.classifyChunks(chunks)
	}

	// Process each chunk
	for i, chunk := range chunks {
		var classification *ClassifyResult
		if c, ok := classifications[i]; ok {
			classification = &c
		}
		if err := m.processChunk(chunk, target, classification); err != nil {
			m.stats.Errors = append(m.stats.Errors, fmt.Sprintf("chunk %s: %v", chunk.ID, err))
			continue
		}
	}

	return nil
}

// classifyChunks classifies chunks in batches using the LLM classifier.
func (m *MemoryImporter) classifyChunks(chunks []OpenClawChunk) map[int]ClassifyResult {
	if m.classifier == nil || len(chunks) == 0 {
		return nil
	}

	results := make(map[int]ClassifyResult)
	batchSize := m.cfg.Classify.BatchSize
	if batchSize <= 0 {
		batchSize = 10
	}

	// Process in batches
	for i := 0; i < len(chunks); i += batchSize {
		end := i + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}

		batch := chunks[i:end]
		texts := make([]string, len(batch))
		for j, chunk := range batch {
			texts[j] = chunk.Text
		}

		if m.cfg.Verbose {
			fmt.Printf("Classifying batch %d-%d of %d...\n", i+1, end, len(chunks))
		}

		ctx, cancel := context.WithTimeout(context.Background(), m.classifier.cfg.Timeout)
		batchResults, err := m.classifier.ClassifyBatch(ctx, texts)
		cancel()

		if err != nil {
			m.stats.Errors = append(m.stats.Errors, fmt.Sprintf("classify batch %d-%d: %v", i+1, end, err))
			m.stats.ClassifyErrors += len(batch)
			continue
		}

		// Store results with original indices
		for j, r := range batchResults {
			results[i+j] = r
			m.stats.ChunksClassified++
		}
	}

	return results
}

// processChunk converts and imports a single chunk.
func (m *MemoryImporter) processChunk(chunk OpenClawChunk, target memory.Backend, classification *ClassifyResult) error {
	// Skip empty text
	if strings.TrimSpace(chunk.Text) == "" {
		m.stats.ChunksSkipped++
		return nil
	}

	// Deduplicate by content hash
	if m.cfg.Deduplicate && chunk.Hash != "" {
		if m.seen[chunk.Hash] {
			m.stats.ChunksDeduplicated++
			return nil
		}
		m.seen[chunk.Hash] = true
	}

	// Convert to swarmstr format (apply classification if available)
	doc := m.convertChunk(chunk, classification)

	// Dry run: just count
	if m.cfg.DryRun {
		m.stats.ChunksImported++
		return nil
	}

	// Import to target
	if target != nil {
		target.Add(doc)
		m.stats.ChunksImported++
	}

	return nil
}

// convertChunk converts an OpenClaw chunk to swarmstr state.MemoryDoc format.
// If classification is provided, it overrides the heuristic topic/keywords.
func (m *MemoryImporter) convertChunk(chunk OpenClawChunk, classification *ClassifyResult) state.MemoryDoc {
	// Generate memory ID with prefix to indicate origin
	memoryID := "oc-" + chunk.ID

	// Extract topic from path (basename without extension) as fallback
	topic := ""
	if chunk.Path != "" {
		base := filepath.Base(chunk.Path)
		topic = strings.TrimSuffix(base, filepath.Ext(base))
		// Clean up common patterns
		topic = strings.ReplaceAll(topic, "-", " ")
		topic = strings.ReplaceAll(topic, "_", " ")
	}

	// Convert updated_at to unix timestamp
	unix := chunk.UpdatedAt
	if unix == 0 {
		unix = time.Now().Unix()
	}

	// Build keywords from path components as fallback
	var keywords []string
	if chunk.Path != "" {
		parts := strings.Split(chunk.Path, string(os.PathSeparator))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" && part != "." && part != ".." {
				keywords = append(keywords, strings.ToLower(part))
			}
		}
	}
	if chunk.Source != "" {
		keywords = append(keywords, strings.ToLower(chunk.Source))
	}

	// Memory type (from classification or default)
	memType := ""

	// Apply LLM classification if available
	if classification != nil {
		if classification.Topic != "" {
			topic = classification.Topic
		}
		if len(classification.Keywords) > 0 {
			// Merge: classification keywords first, then path-based
			keywords = append(classification.Keywords, keywords...)
		}
		if classification.Type != "" {
			memType = classification.Type
		}
	}

	doc := state.MemoryDoc{
		MemoryID: memoryID,
		Topic:    topic,
		Type:     memType,
		Text:     chunk.Text,
		Keywords: keywords,
		Unix:     unix,
		Source:   chunk.Source,
	}

	// Note: Embeddings are handled separately via ImportChunkWithEmbedding
	// since state.MemoryDoc doesn't have embedding fields
	if m.cfg.CopyEmbeddings && chunk.Embedding != "" {
		if m.cfg.ExpectedModel == "" || chunk.Model == m.cfg.ExpectedModel {
			// Validate embedding is parseable
			var embedding []float32
			if err := json.Unmarshal([]byte(chunk.Embedding), &embedding); err == nil && len(embedding) > 0 {
				m.stats.EmbeddingsCopied++
			} else {
				m.stats.EmbeddingsSkipped++
			}
		} else {
			m.stats.EmbeddingsSkipped++
		}
	}

	return doc
}

// ScanOpenClawMemoryDatabases finds OpenClaw memory databases without importing.
// Useful for showing the user what will be imported.
func ScanOpenClawMemoryDatabases() ([]string, error) {
	cfg := DefaultMemoryImportConfig()
	importer := NewMemoryImporter(cfg)
	return importer.findDatabases()
}
