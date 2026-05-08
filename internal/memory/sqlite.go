// Package memory — SQLite-based memory backend with FTS5 full-text search.
//
// This backend stores memories in a SQLite database with an FTS5 virtual table
// for efficient full-text search. It replaces the JSON file-based storage with
// a proper database backend that supports concurrent access and better query
// performance.
//
// Features:
//   - SQLite storage with FTS5 full-text search
//   - Automatic schema migration
//   - Busy timeout for concurrent access
//   - Content hash deduplication
//   - Compatible with all IndexedMemory fields
package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"

	_ "modernc.org/sqlite"
)

const (
	sqliteDefaultPath   = ".metiq/memory.sqlite"
	sqliteBusyTimeoutMs = 5000
	sqliteSchemaVersion = 1
	sqliteFTSTable      = "chunks_fts"
	sqliteChunksTable   = "chunks"
	sqliteMaxBatchSize  = 500
)

// SQLiteBackend implements the Backend interface using SQLite with FTS5.
type SQLiteBackend struct {
	mu   sync.RWMutex
	db   *sql.DB
	path string

	// Cache for search results (similar to Index)
	cache    map[string][]IndexedMemory
	order    []string
	cacheCap int

	// Optional vector retrieval is disabled by default. It is intentionally
	// local-only and best-effort; BM25/FTS must continue to work if this is nil
	// or an embedding provider fails.
	vectorCfg      MemoryVectorRetrievalConfig
	vectorProvider MemoryEmbeddingProvider
	vectorCounters MemoryVectorCounters

	vectorSessionReindexed int
	vectorLastQueryAt      time.Time
	vectorIdleReindexTimer *time.Timer

	recovery SQLiteRecoveryOptions
}

func init() {
	RegisterBackend("sqlite", func(path string) (Backend, error) {
		return OpenSQLiteBackend(path)
	})
}

// OpenSQLiteBackend opens or creates a SQLite memory database at the given path.
// If path is empty, uses the default path (~/.metiq/memory.sqlite).
func OpenSQLiteBackend(path string) (*SQLiteBackend, error) {
	return OpenSQLiteBackendWithRecoveryOptions(path, defaultSQLiteRecoveryOptions())
}

// OpenSQLiteBackendWithRecoveryOptions opens SQLite with startup integrity
// checking, corruption quarantine/restore/rebuild, and weekly backup defaults.
func OpenSQLiteBackendWithRecoveryOptions(path string, opts SQLiteRecoveryOptions) (*SQLiteBackend, error) {
	path, err := resolveSQLiteBackendPath(path)
	if err != nil {
		return nil, err
	}
	opts = normalizeSQLiteRecoveryOptions(path, opts)
	if err := checkSQLiteIntegrity(path); err != nil {
		if !isSQLiteCorruptionError(err) {
			return nil, fmt.Errorf("sqlite startup integrity_check: %w", err)
		}
		if recoverErr := recoverSQLiteDatabase(context.Background(), path, opts, err); recoverErr != nil {
			return nil, recoverErr
		}
	}
	backend, err := openSQLiteBackendWithoutRecovery(path)
	if err != nil {
		return nil, err
	}
	backend.recovery = opts
	if err := backend.backupIfDue(); err != nil {
		opts.Logf("memory sqlite weekly backup warning path=%q err=%v", path, err)
	}
	return backend, nil
}

func resolveSQLiteBackendPath(path string) (string, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		path = filepath.Join(home, sqliteDefaultPath)
	}
	return path, nil
}

func openSQLiteBackendWithoutRecovery(path string) (*SQLiteBackend, error) {
	path, err := resolveSQLiteBackendPath(path)
	if err != nil {
		return nil, err
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", dir, err)
	}

	// Open database with busy timeout
	dsn := fmt.Sprintf("file:%s?_busy_timeout=%d&_journal_mode=WAL&_synchronous=NORMAL",
		path, sqliteBusyTimeoutMs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}

	// Test connection and force WAL for crash-resilient concurrent local reads.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite pragmas: %w", err)
	}

	backend := &SQLiteBackend{
		db:       db,
		path:     path,
		cache:    make(map[string][]IndexedMemory),
		cacheCap: 256,
		recovery: defaultSQLiteRecoveryOptions(),
	}

	// Initialize schema
	if err := backend.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	if err := backend.ensureUnifiedSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init unified schema: %w", err)
	}
	if _, err := backend.BackfillUnifiedFromChunks(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("backfill unified schema: %w", err)
	}

	return backend, nil
}

// initSchema creates the database tables and FTS index if they don't exist.
func (b *SQLiteBackend) initSchema() error {
	schema := `
	-- Schema version tracking
	CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY
	);

	-- Main chunks table
	CREATE TABLE IF NOT EXISTS chunks (
		id TEXT PRIMARY KEY,
		session_id TEXT,
		role TEXT,
		topic TEXT,
		text TEXT NOT NULL,
		keywords TEXT,
		unix INTEGER NOT NULL,
		type TEXT,
		goal_id TEXT,
		task_id TEXT,
		run_id TEXT,
		episode_kind TEXT,
		confidence REAL DEFAULT 0.5,
		source TEXT,
		reviewed_at INTEGER,
		reviewed_by TEXT,
		expires_at INTEGER,
		mem_status TEXT DEFAULT 'active',
		superseded_by TEXT,
		invalidated_at INTEGER,
		invalidated_by TEXT,
		invalidate_reason TEXT,
		embedding TEXT,
		hash TEXT,
		model TEXT,
		updated_at INTEGER
	);

	-- Indexes for common queries
	CREATE INDEX IF NOT EXISTS idx_chunks_session ON chunks(session_id);
	CREATE INDEX IF NOT EXISTS idx_chunks_topic ON chunks(topic);
	CREATE INDEX IF NOT EXISTS idx_chunks_type ON chunks(type);
	CREATE INDEX IF NOT EXISTS idx_chunks_task ON chunks(task_id);
	CREATE INDEX IF NOT EXISTS idx_chunks_unix ON chunks(unix);
	CREATE INDEX IF NOT EXISTS idx_chunks_hash ON chunks(hash);
	CREATE INDEX IF NOT EXISTS idx_chunks_status ON chunks(mem_status);

	-- FTS5 virtual table for full-text search
	CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
		id,
		text,
		topic,
		keywords,
		content='chunks',
		content_rowid='rowid',
		tokenize='unicode61 remove_diacritics 2'
	);

	-- Triggers to keep FTS in sync with chunks table
	CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
		INSERT INTO chunks_fts(rowid, id, text, topic, keywords)
		VALUES (new.rowid, new.id, new.text, new.topic, new.keywords);
	END;

	CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
		INSERT INTO chunks_fts(chunks_fts, rowid, id, text, topic, keywords)
		VALUES ('delete', old.rowid, old.id, old.text, old.topic, old.keywords);
	END;

	CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
		INSERT INTO chunks_fts(chunks_fts, rowid, id, text, topic, keywords)
		VALUES ('delete', old.rowid, old.id, old.text, old.topic, old.keywords);
		INSERT INTO chunks_fts(rowid, id, text, topic, keywords)
		VALUES (new.rowid, new.id, new.text, new.topic, new.keywords);
	END;

	-- Embedding cache table
	CREATE TABLE IF NOT EXISTS embedding_cache (
		provider TEXT NOT NULL,
		model TEXT NOT NULL,
		provider_key TEXT NOT NULL,
		hash TEXT NOT NULL,
		embedding TEXT NOT NULL,
		dims INTEGER,
		updated_at INTEGER,
		PRIMARY KEY (provider, model, provider_key, hash)
	);

	-- Recall tracking for promotion
	CREATE TABLE IF NOT EXISTS recall_tracking (
		memory_id TEXT PRIMARY KEY,
		recall_count INTEGER DEFAULT 0,
		unique_queries INTEGER DEFAULT 0,
		query_hashes TEXT,
		last_recall_unix INTEGER,
		first_recall_unix INTEGER,
		avg_score REAL,
		promoted_at INTEGER,
		promoted_to TEXT
	);
	`

	_, err := b.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("exec schema: %w", err)
	}

	// Update schema version
	_, err = b.db.Exec(`INSERT OR REPLACE INTO schema_version (version) VALUES (?)`, sqliteSchemaVersion)
	return err
}

// Add indexes a new memory document.
func (b *SQLiteBackend) Add(doc state.MemoryDoc) {
	_ = b.ensureUnifiedSchema()
	b.mu.Lock()
	defer b.mu.Unlock()

	if strings.TrimSpace(doc.MemoryID) == "" || strings.TrimSpace(doc.Text) == "" {
		return
	}

	// Generate content hash for deduplication
	hash := contentHash(doc.Text)

	// Serialize keywords as JSON
	keywords := ""
	if len(doc.Keywords) > 0 {
		if data, err := json.Marshal(doc.Keywords); err == nil {
			keywords = string(data)
		}
	}

	now := time.Now().Unix()
	if doc.Unix == 0 {
		doc.Unix = now
	}

	_, err := b.db.Exec(`
		INSERT OR REPLACE INTO chunks (
			id, session_id, role, topic, text, keywords, unix,
			type, goal_id, task_id, run_id, episode_kind,
			confidence, source, reviewed_at, reviewed_by, expires_at,
			mem_status, superseded_by, invalidated_at, invalidated_by, invalidate_reason,
			hash, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		doc.MemoryID, doc.SessionID, doc.Role, doc.Topic, doc.Text, keywords, doc.Unix,
		doc.Type, doc.GoalID, doc.TaskID, doc.RunID, doc.EpisodeKind,
		doc.Confidence, doc.Source, doc.ReviewedAt, doc.ReviewedBy, doc.ExpiresAt,
		doc.MemStatus, doc.SupersededBy, doc.InvalidatedAt, doc.InvalidatedBy, doc.InvalidateReason,
		hash, now,
	)
	if err != nil {
		// Log error but don't fail - best effort
		return
	}
	_ = b.writeMemoryRecordLocked(MemoryRecordFromDoc(doc))

	b.clearCacheLocked()
}

func (b *SQLiteBackend) AddWithContext(ctx context.Context, doc state.MemoryDoc) {
	_ = ctx
	b.Add(doc)
}

// Search performs a full-text search using FTS5.
func (b *SQLiteBackend) Search(query string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 20
	}

	b.mu.RLock()
	cacheKey := searchCacheKey("", query, limit)
	if cached, ok := b.cache[cacheKey]; ok {
		b.mu.RUnlock()
		return cloneMemories(cached)
	}
	b.mu.RUnlock()

	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return nil
	}

	results := b.searchFTS(ftsQuery, "", limit)

	b.mu.Lock()
	b.setCacheLocked(cacheKey, results)
	b.mu.Unlock()

	return cloneMemories(results)
}

// SearchSession performs a session-scoped full-text search.
func (b *SQLiteBackend) SearchSession(sessionID, query string, limit int) []IndexedMemory {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if limit <= 0 {
		limit = 8
	}

	b.mu.RLock()
	cacheKey := searchCacheKey(sessionID, query, limit)
	if cached, ok := b.cache[cacheKey]; ok {
		b.mu.RUnlock()
		return cloneMemories(cached)
	}
	b.mu.RUnlock()

	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		// Fall back to listing session
		return b.ListSession(sessionID, limit)
	}

	results := b.searchFTS(ftsQuery, sessionID, limit)

	b.mu.Lock()
	b.setCacheLocked(cacheKey, results)
	b.mu.Unlock()

	return cloneMemories(results)
}

// searchFTS executes the FTS5 search query.
func (b *SQLiteBackend) searchFTS(ftsQuery, sessionID string, limit int) []IndexedMemory {
	var rows *sql.Rows
	var err error

	if sessionID != "" {
		rows, err = b.db.Query(`
			SELECT c.id, c.session_id, c.role, c.topic, c.text, c.keywords, c.unix,
			       c.type, c.goal_id, c.task_id, c.run_id, c.episode_kind,
			       c.confidence, c.source, c.reviewed_at, c.reviewed_by, c.expires_at,
			       c.mem_status, c.superseded_by, c.invalidated_at, c.invalidated_by, c.invalidate_reason,
			       bm25(chunks_fts) AS rank
			FROM chunks_fts fts
			JOIN chunks c ON c.id = fts.id
			WHERE chunks_fts MATCH ? AND c.session_id = ?
			ORDER BY rank
			LIMIT ?
		`, ftsQuery, sessionID, limit)
	} else {
		rows, err = b.db.Query(`
			SELECT c.id, c.session_id, c.role, c.topic, c.text, c.keywords, c.unix,
			       c.type, c.goal_id, c.task_id, c.run_id, c.episode_kind,
			       c.confidence, c.source, c.reviewed_at, c.reviewed_by, c.expires_at,
			       c.mem_status, c.superseded_by, c.invalidated_at, c.invalidated_by, c.invalidate_reason,
			       bm25(chunks_fts) AS rank
			FROM chunks_fts fts
			JOIN chunks c ON c.id = fts.id
			WHERE chunks_fts MATCH ?
			ORDER BY rank
			LIMIT ?
		`, ftsQuery, limit)
	}

	if err != nil {
		return nil
	}
	defer rows.Close()

	return b.scanRows(rows)
}

// ListSession returns recent entries for a specific session.
func (b *SQLiteBackend) ListSession(sessionID string, limit int) []IndexedMemory {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}

	rows, err := b.db.Query(`
		SELECT id, session_id, role, topic, text, keywords, unix,
		       type, goal_id, task_id, run_id, episode_kind,
		       confidence, source, reviewed_at, reviewed_by, expires_at,
		       mem_status, superseded_by, invalidated_at, invalidated_by, invalidate_reason
		FROM chunks
		WHERE session_id = ?
		ORDER BY unix DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	return b.scanRowsNoRank(rows)
}

// ListByTopic returns entries matching the given topic.
func (b *SQLiteBackend) ListByTopic(topic string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 100
	}

	rows, err := b.db.Query(`
		SELECT id, session_id, role, topic, text, keywords, unix,
		       type, goal_id, task_id, run_id, episode_kind,
		       confidence, source, reviewed_at, reviewed_by, expires_at,
		       mem_status, superseded_by, invalidated_at, invalidated_by, invalidate_reason
		FROM chunks
		WHERE topic = ?
		ORDER BY unix DESC
		LIMIT ?
	`, topic, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	return b.scanRowsNoRank(rows)
}

// ListByType returns entries matching the given type.
func (b *SQLiteBackend) ListByType(memType string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 100
	}

	rows, err := b.db.Query(`
		SELECT id, session_id, role, topic, text, keywords, unix,
		       type, goal_id, task_id, run_id, episode_kind,
		       confidence, source, reviewed_at, reviewed_by, expires_at,
		       mem_status, superseded_by, invalidated_at, invalidated_by, invalidate_reason
		FROM chunks
		WHERE type = ?
		ORDER BY unix DESC
		LIMIT ?
	`, memType, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	return b.scanRowsNoRank(rows)
}

// ListByTaskID returns entries linked to the given task.
func (b *SQLiteBackend) ListByTaskID(taskID string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 100
	}

	rows, err := b.db.Query(`
		SELECT id, session_id, role, topic, text, keywords, unix,
		       type, goal_id, task_id, run_id, episode_kind,
		       confidence, source, reviewed_at, reviewed_by, expires_at,
		       mem_status, superseded_by, invalidated_at, invalidated_by, invalidate_reason
		FROM chunks
		WHERE task_id = ?
		ORDER BY unix DESC
		LIMIT ?
	`, taskID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	return b.scanRowsNoRank(rows)
}

// Count returns the total number of memory entries.
func (b *SQLiteBackend) Count() int {
	var count int
	err := b.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// SessionCount returns the number of distinct session IDs.
func (b *SQLiteBackend) SessionCount() int {
	var count int
	err := b.db.QueryRow(`SELECT COUNT(DISTINCT session_id) FROM chunks WHERE session_id != ''`).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// Compact removes old entries to keep the total below maxEntries.
func (b *SQLiteBackend) Compact(maxEntries int) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	count := b.Count()
	if count <= maxEntries {
		return 0
	}

	toRemove := count - maxEntries

	// Delete oldest entries
	_, err := b.db.Exec(`
		DELETE FROM chunks WHERE id IN (
			SELECT id FROM chunks ORDER BY unix ASC LIMIT ?
		)
	`, toRemove)
	if err != nil {
		return 0
	}

	b.clearCacheLocked()
	return toRemove
}

// Store adds a new memory entry and returns the generated ID.
func (b *SQLiteBackend) Store(sessionID, text string, tags []string) string {
	id := GenerateMemoryID()
	b.Add(state.MemoryDoc{
		MemoryID:  id,
		SessionID: sessionID,
		Text:      text,
		Keywords:  append([]string(nil), tags...),
		Unix:      time.Now().Unix(),
	})
	return id
}

// Delete removes the memory entry with the given ID.
func (b *SQLiteBackend) Delete(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	result, err := b.db.Exec(`DELETE FROM chunks WHERE id = ?`, id)
	if err != nil {
		return false
	}

	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false
	}

	b.clearCacheLocked()
	return true
}

// Save is a no-op for SQLite since writes are immediate.
func (b *SQLiteBackend) Save() error {
	// SQLite writes are already persisted
	return nil
}

// Close closes the database connection. On clean shutdown it checkpoints WAL
// content and switches back to DELETE journaling where SQLite can do so safely;
// normal operation re-enables WAL on the next OpenSQLiteBackend call.
func (b *SQLiteBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.db == nil {
		return nil
	}
	_, pragmaErr := b.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE); PRAGMA journal_mode=DELETE`)
	closeErr := b.db.Close()
	b.db = nil
	if pragmaErr != nil {
		return fmt.Errorf("sqlite clean shutdown pragmas: %w", pragmaErr)
	}
	return closeErr
}

// MemoryStatus returns the backend status.
func (b *SQLiteBackend) MemoryStatus() StoreStatus {
	available := b.db != nil
	if available {
		if err := b.db.Ping(); err != nil {
			available = false
		}
	}
	return StoreStatus{
		Kind:    "sqlite",
		Primary: BackendStatus{Name: "sqlite", Available: available},
	}
}

// BackendStatus returns the health status of the backend.
func (b *SQLiteBackend) BackendStatus() BackendStatus {
	available := b.db != nil
	if available {
		if err := b.db.Ping(); err != nil {
			available = false
		}
	}
	return BackendStatus{
		Name:      "sqlite",
		Available: available,
	}
}

// scanRows scans result rows with rank column.
func (b *SQLiteBackend) scanRows(rows *sql.Rows) []IndexedMemory {
	var results []IndexedMemory
	for rows.Next() {
		var m IndexedMemory
		var keywords sql.NullString
		var rank float64

		err := rows.Scan(
			&m.MemoryID, &m.SessionID, &m.Role, &m.Topic, &m.Text, &keywords, &m.Unix,
			&m.Type, &m.GoalID, &m.TaskID, &m.RunID, &m.EpisodeKind,
			&m.Confidence, &m.Source, &m.ReviewedAt, &m.ReviewedBy, &m.ExpiresAt,
			&m.MemStatus, &m.SupersededBy, &m.InvalidatedAt, &m.InvalidatedBy, &m.InvalidateReason,
			&rank,
		)
		if err != nil {
			continue
		}

		if keywords.Valid {
			_ = json.Unmarshal([]byte(keywords.String), &m.Keywords)
		}

		results = append(results, m)
	}
	return results
}

// scanRowsNoRank scans result rows without rank column.
func (b *SQLiteBackend) scanRowsNoRank(rows *sql.Rows) []IndexedMemory {
	var results []IndexedMemory
	for rows.Next() {
		var m IndexedMemory
		var keywords sql.NullString

		err := rows.Scan(
			&m.MemoryID, &m.SessionID, &m.Role, &m.Topic, &m.Text, &keywords, &m.Unix,
			&m.Type, &m.GoalID, &m.TaskID, &m.RunID, &m.EpisodeKind,
			&m.Confidence, &m.Source, &m.ReviewedAt, &m.ReviewedBy, &m.ExpiresAt,
			&m.MemStatus, &m.SupersededBy, &m.InvalidatedAt, &m.InvalidatedBy, &m.InvalidateReason,
		)
		if err != nil {
			continue
		}

		if keywords.Valid {
			_ = json.Unmarshal([]byte(keywords.String), &m.Keywords)
		}

		results = append(results, m)
	}
	return results
}

// setCacheLocked adds a result to the cache (must hold lock).
func (b *SQLiteBackend) setCacheLocked(key string, value []IndexedMemory) {
	if b.cacheCap <= 0 {
		return
	}
	if b.cache == nil {
		b.cache = make(map[string][]IndexedMemory)
	}
	if _, exists := b.cache[key]; !exists {
		b.order = append(b.order, key)
	}
	b.cache[key] = cloneMemories(value)
	for len(b.order) > b.cacheCap {
		victim := b.order[0]
		b.order = b.order[1:]
		delete(b.cache, victim)
	}
}

// clearCacheLocked clears the cache (must hold lock).
func (b *SQLiteBackend) clearCacheLocked() {
	b.cache = make(map[string][]IndexedMemory)
	b.order = nil
}

// buildFTSQuery converts a natural language query to FTS5 syntax.
func buildFTSQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}

	// Tokenize and build FTS query
	tokens := tokenizeFTSQuery(query)
	if len(tokens) == 0 {
		return ""
	}

	// Build quoted phrase search with AND
	var parts []string
	for _, token := range tokens {
		// Escape quotes in token
		escaped := strings.ReplaceAll(token, "\"", "\"\"")
		parts = append(parts, fmt.Sprintf(`"%s"`, escaped))
	}

	return strings.Join(parts, " AND ")
}

// tokenizeFTSQuery extracts search tokens from a query.
func tokenizeFTSQuery(query string) []string {
	// Split on non-alphanumeric characters
	var tokens []string
	var current strings.Builder

	for _, r := range strings.ToLower(query) {
		if isAlphanumeric(r) {
			current.WriteRune(r)
		} else if current.Len() > 0 {
			token := current.String()
			if len(token) >= 2 && !isStopword(token) {
				tokens = append(tokens, token)
			}
			current.Reset()
		}
	}

	if current.Len() > 0 {
		token := current.String()
		if len(token) >= 2 && !isStopword(token) {
			tokens = append(tokens, token)
		}
	}

	return tokens
}

func isAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' ||
		// Include CJK characters
		(r >= 0x3040 && r <= 0x30ff) || // Hiragana + Katakana
		(r >= 0x3400 && r <= 0x9fff) || // CJK
		(r >= 0xac00 && r <= 0xd7af) // Hangul
}

func isStopword(token string) bool {
	stops := map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
		"be": true, "by": true, "for": true, "from": true, "has": true, "he": true,
		"in": true, "is": true, "it": true, "its": true, "of": true, "on": true,
		"or": true, "that": true, "the": true, "to": true, "was": true, "were": true,
		"will": true, "with": true,
	}
	return stops[token]
}

// contentHash generates a SHA256 hash of the content for deduplication.
func contentHash(text string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(h[:])
}

// MigrateFromJSONIndex imports memories from an existing JSON index file.
func (b *SQLiteBackend) MigrateFromJSONIndex(jsonPath string) error {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No existing index, nothing to migrate
		}
		return fmt.Errorf("read json index: %w", err)
	}

	var disk struct {
		Docs []IndexedMemory `json:"docs"`
	}
	if err := json.Unmarshal(data, &disk); err != nil {
		return fmt.Errorf("parse json index: %w", err)
	}

	if len(disk.Docs) == 0 {
		return nil
	}

	// Sort by unix timestamp (oldest first) for consistent import
	sort.Slice(disk.Docs, func(i, j int) bool {
		return disk.Docs[i].Unix < disk.Docs[j].Unix
	})

	// Import in batches
	tx, err := b.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO chunks (
			id, session_id, role, topic, text, keywords, unix,
			type, goal_id, task_id, run_id, episode_kind,
			confidence, source, reviewed_at, reviewed_by, expires_at,
			mem_status, superseded_by, invalidated_at, invalidated_by, invalidate_reason,
			hash, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Unix()
	imported := 0

	for _, doc := range disk.Docs {
		if strings.TrimSpace(doc.MemoryID) == "" || strings.TrimSpace(doc.Text) == "" {
			continue
		}

		keywords := ""
		if len(doc.Keywords) > 0 {
			if data, err := json.Marshal(doc.Keywords); err == nil {
				keywords = string(data)
			}
		}

		hash := contentHash(doc.Text)

		_, err := stmt.Exec(
			doc.MemoryID, doc.SessionID, doc.Role, doc.Topic, doc.Text, keywords, doc.Unix,
			doc.Type, doc.GoalID, doc.TaskID, doc.RunID, doc.EpisodeKind,
			doc.Confidence, doc.Source, doc.ReviewedAt, doc.ReviewedBy, doc.ExpiresAt,
			doc.MemStatus, doc.SupersededBy, doc.InvalidatedAt, doc.InvalidatedBy, doc.InvalidateReason,
			hash, now,
		)
		if err != nil {
			continue // Skip failed entries
		}
		imported++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	_, _ = b.BackfillUnifiedFromChunks(context.Background())

	return nil
}

// MigrateFromOpenClaw imports memories from an OpenClaw SQLite database.
// OpenClaw stores memories in ~/.openclaw/agents/<id>/memory/<id>.sqlite
// with a different schema that we map to Metiq's format.
func (b *SQLiteBackend) MigrateFromOpenClaw(srcPath string) (int, error) {
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		return 0, nil // No source database, nothing to migrate
	}

	// Open source database (read-only)
	srcDSN := fmt.Sprintf("file:%s?mode=ro&_busy_timeout=%d", srcPath, sqliteBusyTimeoutMs)
	srcDB, err := sql.Open("sqlite", srcDSN)
	if err != nil {
		return 0, fmt.Errorf("open source db: %w", err)
	}
	defer srcDB.Close()

	// Check if chunks table exists
	var tableName string
	err = srcDB.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='chunks'`).Scan(&tableName)
	if err != nil {
		return 0, fmt.Errorf("source db has no chunks table: %w", err)
	}

	// Query all chunks from OpenClaw database
	// OpenClaw schema: id, path, source, start_line, end_line, hash, model, text, embedding, updated_at
	rows, err := srcDB.Query(`
		SELECT id, path, source, text, embedding, hash, model, updated_at
		FROM chunks
		ORDER BY updated_at ASC
	`)
	if err != nil {
		return 0, fmt.Errorf("query source chunks: %w", err)
	}
	defer rows.Close()

	// Begin transaction for bulk insert
	tx, err := b.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO chunks (
			id, session_id, role, topic, text, keywords, unix,
			type, goal_id, task_id, run_id, episode_kind,
			confidence, source, reviewed_at, reviewed_by, expires_at,
			mem_status, superseded_by, invalidated_at, invalidated_by, invalidate_reason,
			embedding, hash, model, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	imported := 0
	now := time.Now().Unix()

	for rows.Next() {
		var (
			id         string
			path       sql.NullString
			source     sql.NullString
			text       string
			embedding  sql.NullString
			hash       sql.NullString
			model      sql.NullString
			updated_at sql.NullInt64
		)

		if err := rows.Scan(&id, &path, &source, &text, &embedding, &hash, &model, &updated_at); err != nil {
			continue // Skip malformed rows
		}

		if strings.TrimSpace(id) == "" || strings.TrimSpace(text) == "" {
			continue
		}

		// Map OpenClaw fields to Metiq fields
		topic := ""
		if path.Valid {
			// Extract filename as topic (e.g., "memory/2024-01-15.md" → "2024-01-15")
			topic = filepath.Base(path.String)
			topic = strings.TrimSuffix(topic, filepath.Ext(topic))
		}

		srcValue := "openclaw-migration"
		if source.Valid && source.String != "" {
			srcValue = source.String
		}

		unix := now
		if updated_at.Valid {
			unix = updated_at.Int64
		}

		// Use existing hash or generate new one
		hashValue := ""
		if hash.Valid {
			hashValue = hash.String
		} else {
			hashValue = contentHash(text)
		}

		_, err := stmt.Exec(
			id,                  // id
			"openclaw-migrated", // session_id (marker for migrated content)
			"",                  // role
			topic,               // topic (from path)
			text,                // text
			"",                  // keywords (empty, OpenClaw doesn't have this)
			unix,                // unix
			"migrated",          // type
			"",                  // goal_id
			"",                  // task_id
			"",                  // run_id
			"",                  // episode_kind
			0.5,                 // confidence (default)
			srcValue,            // source
			nil,                 // reviewed_at
			nil,                 // reviewed_by
			nil,                 // expires_at
			"active",            // mem_status
			nil,                 // superseded_by
			nil,                 // invalidated_at
			nil,                 // invalidated_by
			nil,                 // invalidate_reason
			embedding.String,    // embedding (may be empty)
			hashValue,           // hash
			model.String,        // model
			now,                 // updated_at
		)
		if err != nil {
			continue // Skip failed entries
		}
		imported++
	}

	if err := rows.Err(); err != nil {
		return imported, fmt.Errorf("iterate rows: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}

	// Rebuild FTS index after bulk import
	if imported > 0 {
		_, _ = b.BackfillUnifiedFromChunks(context.Background())
		b.mu.Lock()
		b.clearCacheLocked()
		b.mu.Unlock()
		_ = b.RebuildFTSIndex()
	}

	return imported, nil
}

// RebuildFTSIndex rebuilds the FTS index from the chunks table.
// Use this after bulk imports or if the index becomes corrupted.
func (b *SQLiteBackend) RebuildFTSIndex() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Delete and repopulate FTS index
	_, err := b.db.Exec(`INSERT INTO chunks_fts(chunks_fts) VALUES('rebuild')`)
	if err != nil {
		return fmt.Errorf("rebuild fts: %w", err)
	}

	b.clearCacheLocked()
	return nil
}

// Vacuum runs VACUUM on the database to reclaim space.
func (b *SQLiteBackend) Vacuum() error {
	_, err := b.db.Exec(`VACUUM`)
	return err
}

// Stats returns database statistics.
func (b *SQLiteBackend) Stats() map[string]any {
	stats := map[string]any{
		"backend": "sqlite",
		"path":    b.path,
	}

	var totalChunks, totalSessions int
	b.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&totalChunks)
	b.db.QueryRow(`SELECT COUNT(DISTINCT session_id) FROM chunks WHERE session_id != ''`).Scan(&totalSessions)

	stats["total_chunks"] = totalChunks
	stats["total_sessions"] = totalSessions

	// Get database file size
	if info, err := os.Stat(b.path); err == nil {
		stats["file_size_bytes"] = info.Size()
	}

	return stats
}
