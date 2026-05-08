package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type CompactionConfig struct {
	EpisodeTTLDays int
	AfterWrites    int
	Now            time.Time
}

type CompactionResult struct {
	Expired    int `json:"expired"`
	Deduped    int `json:"deduped"`
	Superseded int `json:"superseded"`
}

type MemoryEvalCase struct {
	ID              string   `json:"id"`
	Query           string   `json:"query"`
	ExpectedIDs     []string `json:"expected_memory_ids,omitempty"`
	ExpectedSubject string   `json:"expected_subject,omitempty"`
	ExpectedType    string   `json:"expected_type,omitempty"`
	Scope           string   `json:"scope,omitempty"`
	Notes           string   `json:"notes,omitempty"`
}

type MemoryEvalRun struct {
	CaseCount     int      `json:"case_count"`
	RecallAt5     float64  `json:"recall_at_5"`
	RecallAt10    float64  `json:"recall_at_10"`
	NoResultRate  float64  `json:"no_result_rate"`
	StaleHitRate  float64  `json:"stale_hit_rate"`
	DuplicateRate float64  `json:"duplicate_hit_rate"`
	P50LatencyMS  float64  `json:"p50_latency_ms"`
	P95LatencyMS  float64  `json:"p95_latency_ms"`
	P99LatencyMS  float64  `json:"p99_latency_ms"`
	FailedCaseIDs []string `json:"failed_case_ids,omitempty"`
}

func (b *SQLiteBackend) ensureUnifiedSchema() error {
	if b == nil || b.db == nil {
		return fmt.Errorf("sqlite backend is closed")
	}
	schema := `
	CREATE TABLE IF NOT EXISTS memory_records (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		scope TEXT NOT NULL,
		subject TEXT,
		text TEXT NOT NULL,
		summary TEXT,
		keywords TEXT,
		tags TEXT,
		confidence REAL NOT NULL DEFAULT 0.5,
		salience REAL NOT NULL DEFAULT 0.0,
		source_kind TEXT,
		source_ref TEXT,
		source_session_id TEXT,
		source_event_id TEXT,
		source_file_path TEXT,
		source_nostr_event_id TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		valid_from INTEGER,
		valid_until INTEGER,
		pinned INTEGER NOT NULL DEFAULT 0,
		supersedes TEXT,
		superseded_by TEXT,
		deleted_at INTEGER,
		embedding_model TEXT,
		embedding_version TEXT,
		metadata TEXT,
		hash TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_memory_records_type ON memory_records(type);
	CREATE INDEX IF NOT EXISTS idx_memory_records_scope ON memory_records(scope);
	CREATE INDEX IF NOT EXISTS idx_memory_records_subject ON memory_records(subject);
	CREATE INDEX IF NOT EXISTS idx_memory_records_updated ON memory_records(updated_at);
	CREATE INDEX IF NOT EXISTS idx_memory_records_deleted ON memory_records(deleted_at);
	CREATE INDEX IF NOT EXISTS idx_memory_records_superseded ON memory_records(superseded_by);
	CREATE INDEX IF NOT EXISTS idx_memory_records_hash ON memory_records(hash);

	CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
		id UNINDEXED,
		type,
		scope,
		subject,
		summary,
		text,
		keywords,
		tags,
		content='memory_records',
		content_rowid='rowid',
		tokenize='unicode61 remove_diacritics 2'
	);
	CREATE TRIGGER IF NOT EXISTS memory_records_ai AFTER INSERT ON memory_records BEGIN
		INSERT INTO memory_fts(rowid, id, type, scope, subject, summary, text, keywords, tags)
		VALUES (new.rowid, new.id, new.type, new.scope, new.subject, new.summary, new.text, new.keywords, new.tags);
	END;
	CREATE TRIGGER IF NOT EXISTS memory_records_ad AFTER DELETE ON memory_records BEGIN
		INSERT INTO memory_fts(memory_fts, rowid, id, type, scope, subject, summary, text, keywords, tags)
		VALUES ('delete', old.rowid, old.id, old.type, old.scope, old.subject, old.summary, old.text, old.keywords, old.tags);
	END;
	CREATE TRIGGER IF NOT EXISTS memory_records_au AFTER UPDATE ON memory_records BEGIN
		INSERT INTO memory_fts(memory_fts, rowid, id, type, scope, subject, summary, text, keywords, tags)
		VALUES ('delete', old.rowid, old.id, old.type, old.scope, old.subject, old.summary, old.text, old.keywords, old.tags);
		INSERT INTO memory_fts(rowid, id, type, scope, subject, summary, text, keywords, tags)
		VALUES (new.rowid, new.id, new.type, new.scope, new.subject, new.summary, new.text, new.keywords, new.tags);
	END;

	CREATE TABLE IF NOT EXISTS memory_sources (
		record_id TEXT NOT NULL,
		source_kind TEXT,
		source_ref TEXT,
		session_id TEXT,
		event_id TEXT,
		file_path TEXT,
		nostr_event_id TEXT,
		created_at INTEGER NOT NULL,
		PRIMARY KEY(record_id, source_kind, source_ref)
	);
	CREATE TABLE IF NOT EXISTS memory_sync_state (
		namespace TEXT PRIMARY KEY,
		last_event_id TEXT,
		last_created_at INTEGER,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS memory_eval_cases (
		id TEXT PRIMARY KEY,
		query TEXT NOT NULL,
		expected_ids TEXT,
		expected_subject TEXT,
		expected_type TEXT,
		scope TEXT,
		notes TEXT
	);
	CREATE TABLE IF NOT EXISTS memory_eval_runs (
		id TEXT PRIMARY KEY,
		run_at INTEGER NOT NULL,
		metrics TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS memory_events_outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		record_id TEXT NOT NULL,
		event_kind TEXT NOT NULL,
		payload TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT
	);
	CREATE TABLE IF NOT EXISTS memory_embeddings (
		record_id TEXT NOT NULL,
		embedding_model TEXT NOT NULL,
		embedding_version TEXT NOT NULL,
		embedding TEXT NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY(record_id, embedding_model, embedding_version)
	);
	`
	_, err := b.db.Exec(schema)
	return err
}

func (b *SQLiteBackend) BackfillUnifiedFromChunks(ctx context.Context) (int, error) {
	_ = ctx
	if err := b.ensureUnifiedSchema(); err != nil {
		return 0, err
	}
	rows, err := b.db.Query(`
		SELECT id, session_id, role, topic, text, keywords, unix,
		type, goal_id, task_id, run_id, episode_kind,
		confidence, source, reviewed_at, reviewed_by, expires_at,
		mem_status, superseded_by, invalidated_at, invalidated_by, invalidate_reason
		FROM chunks
		WHERE id NOT IN (SELECT id FROM memory_records)
		ORDER BY unix ASC
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	mems := b.scanRowsNoRank(rows)
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for _, mem := range mems {
		if err := b.writeMemoryRecordLocked(MemoryRecordFromIndexed(mem)); err == nil {
			count++
		}
	}
	return count, nil
}

func (b *SQLiteBackend) WriteMemoryRecord(ctx context.Context, rec MemoryRecord) error {
	_ = ctx
	if err := b.ensureUnifiedSchema(); err != nil {
		return err
	}
	rec, err := NormalizeMemoryRecord(rec)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writeMemoryRecordLocked(rec)
}

func (b *SQLiteBackend) writeMemoryRecordLocked(rec MemoryRecord) error {
	_, err := b.db.Exec(`
		INSERT OR REPLACE INTO memory_records (
			id, type, scope, subject, text, summary, keywords, tags,
			confidence, salience, source_kind, source_ref, source_session_id,
			source_event_id, source_file_path, source_nostr_event_id,
			created_at, updated_at, valid_from, valid_until, pinned,
			supersedes, superseded_by, deleted_at, embedding_model,
			embedding_version, metadata, hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, rec.ID, rec.Type, rec.Scope, rec.Subject, rec.Text, rec.Summary, recordJSON(rec.Keywords), recordJSON(rec.Tags),
		rec.Confidence, rec.Salience, rec.Source.Kind, rec.Source.Ref, rec.Source.SessionID, rec.Source.EventID, rec.Source.FilePath, rec.Source.NostrEventID,
		rec.CreatedAt.Unix(), rec.UpdatedAt.Unix(), rec.ValidFrom.Unix(), nullableUnix(rec.ValidUntil), boolInt(rec.Pinned),
		recordJSON(rec.Supersedes), rec.SupersededBy, nullableUnix(rec.DeletedAt), rec.EmbeddingModel, rec.EmbeddingVersion, recordJSON(rec.Metadata), contentHash(rec.Text))
	if err != nil {
		return err
	}
	_, _ = b.db.Exec(`INSERT OR REPLACE INTO memory_sources (record_id, source_kind, source_ref, session_id, event_id, file_path, nostr_event_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.Source.Kind, rec.Source.Ref, rec.Source.SessionID, rec.Source.EventID, rec.Source.FilePath, rec.Source.NostrEventID, rec.CreatedAt.Unix())
	b.clearCacheLocked()
	return nil
}

func (b *SQLiteBackend) QueryMemoryRecords(ctx context.Context, q MemoryQuery) ([]MemoryCard, error) {
	_ = ctx
	if err := b.ensureUnifiedSchema(); err != nil {
		return nil, err
	}
	q = normalizeMemoryQuery(q)
	ftsQuery := buildFTSQuery(q.Query)
	var rows *sql.Rows
	var err error
	args := []any{}
	where := unifiedMetadataWhere(q, &args)
	limit := q.Limit
	if ftsQuery != "" && q.Mode != "recent" {
		args = append([]any{ftsQuery}, args...)
		args = append(args, limit)
		rows, err = b.db.Query(`
			SELECT r.id, r.type, r.scope, r.subject, r.text, r.summary, r.keywords, r.tags,
			       r.confidence, r.salience, r.source_kind, r.source_ref, r.source_session_id,
			       r.source_event_id, r.source_file_path, r.source_nostr_event_id,
			       r.created_at, r.updated_at, r.valid_from, r.valid_until, r.pinned,
			       r.supersedes, r.superseded_by, r.deleted_at, r.embedding_model,
			       r.embedding_version, r.metadata, bm25(memory_fts) AS rank
			FROM memory_fts fts JOIN memory_records r ON r.id = fts.id
			WHERE memory_fts MATCH ? `+where+`
			ORDER BY rank, r.pinned DESC, r.updated_at DESC
			LIMIT ?
		`, args...)
	} else {
		args = append(args, limit)
		rows, err = b.db.Query(`
			SELECT r.id, r.type, r.scope, r.subject, r.text, r.summary, r.keywords, r.tags,
			       r.confidence, r.salience, r.source_kind, r.source_ref, r.source_session_id,
			       r.source_event_id, r.source_file_path, r.source_nostr_event_id,
			       r.created_at, r.updated_at, r.valid_from, r.valid_until, r.pinned,
			       r.supersedes, r.superseded_by, r.deleted_at, r.embedding_model,
			       r.embedding_version, r.metadata, 0.0 AS rank
			FROM memory_records r
			WHERE 1=1 `+where+`
			ORDER BY r.pinned DESC, r.updated_at DESC
			LIMIT ?
		`, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records, ranks := b.scanMemoryRecordRows(rows)
	if len(records) == 0 && ftsQuery != "" {
		return b.queryMemoryRecordsLike(q)
	}
	cards := make([]MemoryCard, 0, len(records))
	now := time.Now().UTC()
	for i, rec := range records {
		score := BM25RankToScore(ranks[i])
		if ftsQuery == "" || q.Mode == "recent" {
			score = 0.5
		}
		if rec.Pinned {
			score += 0.15
		}
		if rec.Salience > 0 {
			score += math.Min(rec.Salience, 1) * 0.10
		}
		ageDays := now.Sub(rec.UpdatedAt).Hours() / 24
		if ageDays >= 0 && ageDays < 30 {
			score += (30 - ageDays) / 30 * 0.05
		}
		if score > 1 {
			score = 1
		}
		cards = append(cards, MemoryCardFromRecord(rec, score, q.IncludeSources))
	}
	sort.SliceStable(cards, func(i, j int) bool {
		if cards[i].Score != cards[j].Score {
			return cards[i].Score > cards[j].Score
		}
		return cards[i].UpdatedAt > cards[j].UpdatedAt
	})
	return cards, nil
}

func (b *SQLiteBackend) queryMemoryRecordsLike(q MemoryQuery) ([]MemoryCard, error) {
	args := []any{}
	where := unifiedMetadataWhere(q, &args)
	for _, token := range tokenizeFTSQuery(q.Query) {
		where += " AND (r.text LIKE ? OR r.summary LIKE ? OR r.subject LIKE ?)"
		pattern := "%" + token + "%"
		args = append(args, pattern, pattern, pattern)
	}
	args = append(args, q.Limit)
	rows, err := b.db.Query(`
		SELECT r.id, r.type, r.scope, r.subject, r.text, r.summary, r.keywords, r.tags,
		r.confidence, r.salience, r.source_kind, r.source_ref, r.source_session_id,
		r.source_event_id, r.source_file_path, r.source_nostr_event_id,
		r.created_at, r.updated_at, r.valid_from, r.valid_until, r.pinned,
		r.supersedes, r.superseded_by, r.deleted_at, r.embedding_model,
		r.embedding_version, r.metadata, 0.0 AS rank
		FROM memory_records r
		WHERE 1=1 `+where+`
		ORDER BY r.pinned DESC, r.updated_at DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records, _ := b.scanMemoryRecordRows(rows)
	cards := make([]MemoryCard, 0, len(records))
	for _, rec := range records {
		cards = append(cards, MemoryCardFromRecord(rec, 0.45, q.IncludeSources))
	}
	return cards, nil
}

func (b *SQLiteBackend) GetMemoryRecord(ctx context.Context, id string) (MemoryRecord, bool, error) {
	_ = ctx
	if err := b.ensureUnifiedSchema(); err != nil {
		return MemoryRecord{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return MemoryRecord{}, false, nil
	}
	rows, err := b.db.Query(`
		SELECT id, type, scope, subject, text, summary, keywords, tags,
		       confidence, salience, source_kind, source_ref, source_session_id,
		       source_event_id, source_file_path, source_nostr_event_id,
		       created_at, updated_at, valid_from, valid_until, pinned,
		       supersedes, superseded_by, deleted_at, embedding_model,
		       embedding_version, metadata, 0.0 AS rank
		FROM memory_records WHERE id = ?
	`, id)
	if err != nil {
		return MemoryRecord{}, false, err
	}
	defer rows.Close()
	records, _ := b.scanMemoryRecordRows(rows)
	if len(records) == 0 {
		return MemoryRecord{}, false, nil
	}
	return records[0], true, nil
}

func (b *SQLiteBackend) UpdateMemoryRecord(ctx context.Context, id string, patch map[string]any) (MemoryRecord, error) {
	rec, ok, err := b.GetMemoryRecord(ctx, id)
	if err != nil {
		return MemoryRecord{}, err
	}
	if !ok {
		return MemoryRecord{}, fmt.Errorf("memory record %q not found", id)
	}
	oldText := rec.Text
	applyRecordPatch(&rec, patch)
	rec.UpdatedAt = time.Now().UTC()
	if strings.TrimSpace(oldText) != "" && strings.TrimSpace(rec.Text) != strings.TrimSpace(oldText) && normalizedTextHash(oldText) != normalizedTextHash(rec.Text) {
		replacement := rec
		replacement.ID = NewMemoryRecordID()
		replacement.CreatedAt = rec.UpdatedAt
		replacement.Supersedes = append(replacement.Supersedes, id)
		rec.SupersededBy = replacement.ID
		if err := b.WriteMemoryRecord(ctx, rec); err != nil {
			return MemoryRecord{}, err
		}
		if err := b.WriteMemoryRecord(ctx, replacement); err != nil {
			return MemoryRecord{}, err
		}
		return replacement, nil
	}
	if err := b.WriteMemoryRecord(ctx, rec); err != nil {
		return MemoryRecord{}, err
	}
	return rec, nil
}

func (b *SQLiteBackend) ForgetMemoryRecord(ctx context.Context, id string, mode string) (bool, error) {
	rec, ok, err := b.GetMemoryRecord(ctx, id)
	if err != nil || !ok {
		return ok, err
	}
	now := time.Now().UTC()
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "soft_delete"
	}
	if mode == "local_only" {
		return b.Delete(id), nil
	}
	rec.DeletedAt = &now
	rec.UpdatedAt = now
	if rec.Metadata == nil {
		rec.Metadata = map[string]any{}
	}
	rec.Metadata["forget_mode"] = mode
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err = b.db.Exec(`UPDATE memory_records SET deleted_at = ?, updated_at = ?, metadata = ? WHERE id = ?`, now.Unix(), now.Unix(), recordJSON(rec.Metadata), id)
	if err == nil {
		b.clearCacheLocked()
	}
	return true, err
}

func (b *SQLiteBackend) CompactMemoryRecords(ctx context.Context, cfg CompactionConfig) (CompactionResult, error) {
	_ = ctx
	if err := b.ensureUnifiedSchema(); err != nil {
		return CompactionResult{}, err
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now().UTC()
	}
	if cfg.EpisodeTTLDays <= 0 {
		cfg.EpisodeTTLDays = 30
	}
	cutoff := cfg.Now.Add(-time.Duration(cfg.EpisodeTTLDays) * 24 * time.Hour).Unix()
	b.mu.Lock()
	defer b.mu.Unlock()
	var result CompactionResult
	res, err := b.db.Exec(`UPDATE memory_records SET valid_until = COALESCE(valid_until, ?), updated_at = ? WHERE pinned = 0 AND deleted_at IS NULL AND COALESCE(superseded_by, '') = '' AND type = ? AND updated_at < ?`, cutoff, cfg.Now.Unix(), MemoryRecordTypeEpisode, cutoff)
	if err == nil {
		if n, nerr := res.RowsAffected(); nerr == nil {
			result.Expired = int(n)
		}
	}
	rows, err := b.db.Query(`SELECT hash, MIN(id), COUNT(*) FROM memory_records WHERE pinned = 0 AND deleted_at IS NULL AND COALESCE(superseded_by, '') = '' GROUP BY hash HAVING COUNT(*) > 1`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var hash, keep string
		var count int
		if err := rows.Scan(&hash, &keep, &count); err != nil || hash == "" || keep == "" || count <= 1 {
			continue
		}
		res, err := b.db.Exec(`UPDATE memory_records SET superseded_by = ?, updated_at = ? WHERE hash = ? AND id != ? AND pinned = 0 AND deleted_at IS NULL AND COALESCE(superseded_by, '') = ''`, keep, cfg.Now.Unix(), hash, keep)
		if err == nil {
			if n, nerr := res.RowsAffected(); nerr == nil {
				result.Deduped += int(n)
				result.Superseded += int(n)
			}
		}
	}
	b.clearCacheLocked()
	return result, nil
}

func normalizeMemoryQuery(q MemoryQuery) MemoryQuery {
	q.Query = strings.TrimSpace(q.Query)
	q.Mode = strings.TrimSpace(strings.ToLower(q.Mode))
	if q.Mode == "" {
		q.Mode = "fast"
	}
	if q.Limit <= 0 {
		q.Limit = 8
	}
	if q.Limit > 50 {
		q.Limit = 50
	}
	q.Scopes = normalizeStringSlice(q.Scopes)
	q.Types = normalizeStringSlice(q.Types)
	for i := range q.Types {
		q.Types[i] = NormalizeMemoryRecordType(q.Types[i])
	}
	q.Tags = normalizeStringSlice(q.Tags)
	return q
}

func unifiedMetadataWhere(q MemoryQuery, args *[]any) string {
	parts := []string{}
	now := time.Now().UTC().Unix()
	if q.Mode != "audit" {
		parts = append(parts, "r.deleted_at IS NULL")
		parts = append(parts, "(r.superseded_by IS NULL OR r.superseded_by = '')")
		parts = append(parts, "(r.valid_until IS NULL OR r.valid_until = 0 OR r.valid_until > ?)")
		*args = append(*args, now)
	}
	if len(q.Scopes) > 0 {
		parts = append(parts, "r.scope IN ("+placeholders(len(q.Scopes))+")")
		for _, s := range q.Scopes {
			*args = append(*args, s)
		}
	}
	if q.SessionID != "" {
		parts = append(parts, "(r.scope NOT IN ('session','local') OR r.source_session_id = ?)")
		*args = append(*args, q.SessionID)
	}
	if len(q.Types) > 0 {
		parts = append(parts, "r.type IN ("+placeholders(len(q.Types))+")")
		for _, t := range q.Types {
			*args = append(*args, t)
		}
	}
	for _, tag := range q.Tags {
		parts = append(parts, "(r.tags LIKE ? OR r.keywords LIKE ?)")
		pattern := `%"` + strings.ReplaceAll(tag, `"`, `""`) + `"%`
		*args = append(*args, pattern, pattern)
	}
	if len(parts) == 0 {
		return ""
	}
	return " AND " + strings.Join(parts, " AND ")
}

func (b *SQLiteBackend) scanMemoryRecordRows(rows *sql.Rows) ([]MemoryRecord, []float64) {
	var records []MemoryRecord
	var ranks []float64
	for rows.Next() {
		var rec MemoryRecord
		var keywords, tags, supersedes, metadata sql.NullString
		var validFrom, validUntil, deletedAt sql.NullInt64
		var pinned int
		var rank float64
		var createdAt, updatedAt int64
		err := rows.Scan(&rec.ID, &rec.Type, &rec.Scope, &rec.Subject, &rec.Text, &rec.Summary, &keywords, &tags,
			&rec.Confidence, &rec.Salience, &rec.Source.Kind, &rec.Source.Ref, &rec.Source.SessionID, &rec.Source.EventID, &rec.Source.FilePath, &rec.Source.NostrEventID,
			&createdAt, &updatedAt, &validFrom, &validUntil, &pinned, &supersedes, &rec.SupersededBy, &deletedAt, &rec.EmbeddingModel, &rec.EmbeddingVersion, &metadata, &rank)
		if err != nil {
			continue
		}
		rec.CreatedAt = time.Unix(createdAt, 0).UTC()
		rec.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		if validFrom.Valid && validFrom.Int64 > 0 {
			rec.ValidFrom = time.Unix(validFrom.Int64, 0).UTC()
		}
		if validUntil.Valid && validUntil.Int64 > 0 {
			v := time.Unix(validUntil.Int64, 0).UTC()
			rec.ValidUntil = &v
		}
		if deletedAt.Valid && deletedAt.Int64 > 0 {
			d := time.Unix(deletedAt.Int64, 0).UTC()
			rec.DeletedAt = &d
		}
		rec.Pinned = pinned != 0
		_ = json.Unmarshal([]byte(keywords.String), &rec.Keywords)
		_ = json.Unmarshal([]byte(tags.String), &rec.Tags)
		_ = json.Unmarshal([]byte(supersedes.String), &rec.Supersedes)
		_ = json.Unmarshal([]byte(metadata.String), &rec.Metadata)
		records = append(records, rec)
		ranks = append(ranks, rank)
	}
	return records, ranks
}

func applyRecordPatch(rec *MemoryRecord, patch map[string]any) {
	for k, v := range patch {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "text":
			if s, ok := v.(string); ok {
				rec.Text = s
			}
		case "summary":
			if s, ok := v.(string); ok {
				rec.Summary = s
			}
		case "type":
			if s, ok := v.(string); ok {
				rec.Type = NormalizeMemoryRecordType(s)
			}
		case "scope":
			if s, ok := v.(string); ok {
				rec.Scope = NormalizeMemoryRecordScope(s)
			}
		case "subject":
			if s, ok := v.(string); ok {
				rec.Subject = normalizeSubject(s)
			}
		case "tags":
			rec.Tags = anyStringSlice(v)
		case "pinned":
			if b, ok := v.(bool); ok {
				rec.Pinned = b
			}
		case "confidence":
			if f, ok := anyFloat(v); ok {
				rec.Confidence = f
			}
		}
	}
}

func anyStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return normalizeStringSlice(t)
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return normalizeStringSlice(out)
	case string:
		return normalizeStringSlice(strings.Split(t, ","))
	default:
		return nil
	}
}

func anyFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	default:
		return 0, false
	}
}

func nullableUnix(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().Unix()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func normalizedTextHash(text string) string {
	return contentHash(strings.ToLower(strings.Join(strings.Fields(text), " ")))
}
