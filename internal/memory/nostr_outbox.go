package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

var memoryOutboxRetryBackoff = []time.Duration{
	time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
}

const (
	memoryOutboxTerminalFailureAfter = 7 * 24 * time.Hour
	memoryOutboxFailedRetention      = 30 * 24 * time.Hour
)

type MemoryOutboxEvent struct {
	ID            int64     `json:"id"`
	RecordID      string    `json:"record_id"`
	EventKind     string    `json:"event_kind"`
	Payload       string    `json:"payload"`
	CreatedAt     time.Time `json:"created_at"`
	Attempts      int       `json:"attempts"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	NextAttemptAt time.Time `json:"next_attempt_at,omitempty"`
	PublishFailed bool      `json:"publish_failed,omitempty"`
	FailedAt      time.Time `json:"failed_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

type MemoryOutboxStats struct {
	OutboxDepth     int            `json:"outbox_depth"`
	PublishFailures int            `json:"publish_failures"`
	RetryCounts     map[string]int `json:"retry_counts,omitempty"`
	OldestPending   string         `json:"oldest_pending,omitempty"`
}

func ensureMemoryOutboxColumns(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("sqlite backend is closed")
	}
	columns, err := sqliteTableColumns(db, "memory_events_outbox")
	if err != nil {
		return err
	}
	alter := map[string]string{
		"last_attempt_at": `ALTER TABLE memory_events_outbox ADD COLUMN last_attempt_at INTEGER`,
		"next_attempt_at": `ALTER TABLE memory_events_outbox ADD COLUMN next_attempt_at INTEGER`,
		"publish_failed":  `ALTER TABLE memory_events_outbox ADD COLUMN publish_failed INTEGER NOT NULL DEFAULT 0`,
		"failed_at":       `ALTER TABLE memory_events_outbox ADD COLUMN failed_at INTEGER`,
	}
	for col, stmt := range alter {
		if !columns[col] {
			if _, err := db.Exec(stmt); err != nil {
				return err
			}
		}
	}
	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_memory_outbox_retry ON memory_events_outbox(publish_failed, next_attempt_at, created_at);
		CREATE INDEX IF NOT EXISTS idx_memory_outbox_failed ON memory_events_outbox(publish_failed, failed_at);
	`)
	return err
}

func sqliteTableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func (b *SQLiteBackend) EnqueueMemoryOutboxEvent(ctx context.Context, recordID, eventKind string, payload any, now time.Time) (int64, error) {
	_ = ctx
	start := time.Now()
	if b == nil {
		return 0, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		recordMemoryTelemetry("outbox", start, map[string]any{"ok": false, "op": "enqueue", "error": err.Error()})
		return 0, err
	}
	recordID = strings.TrimSpace(recordID)
	eventKind = strings.TrimSpace(eventKind)
	if recordID == "" || eventKind == "" {
		return 0, fmt.Errorf("outbox record_id and event_kind are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	payloadJSON, err := normalizeOutboxPayload(payload)
	if err != nil {
		return 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	res, err := b.db.Exec(`
		INSERT INTO memory_events_outbox (record_id, event_kind, payload, created_at, attempts, next_attempt_at, publish_failed)
		VALUES (?, ?, ?, ?, 0, ?, 0)
	`, recordID, eventKind, payloadJSON, now.UTC().Unix(), now.UTC().Unix())
	if err != nil {
		recordMemoryTelemetry("outbox", start, map[string]any{"ok": false, "op": "enqueue", "error": err.Error()})
		return 0, err
	}
	id, _ := res.LastInsertId()
	recordMemoryTelemetry("outbox", start, map[string]any{"ok": true, "op": "enqueue", "id": id, "event_kind": eventKind})
	return id, nil
}

func normalizeOutboxPayload(payload any) (string, error) {
	switch v := payload.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("outbox payload is required")
		}
		return v, nil
	case []byte:
		if len(v) == 0 {
			return "", fmt.Errorf("outbox payload is required")
		}
		return string(v), nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

func (b *SQLiteBackend) DueMemoryOutboxEvents(ctx context.Context, now time.Time, limit int, force bool) ([]MemoryOutboxEvent, error) {
	_ = ctx
	if b == nil {
		return nil, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	where := `publish_failed = 0 AND (next_attempt_at IS NULL OR next_attempt_at <= ?)`
	args := []any{now.UTC().Unix(), limit}
	if force {
		where = `1 = 1`
		args = []any{limit}
	}
	rows, err := b.db.Query(`
		SELECT id, record_id, event_kind, payload, created_at, attempts, last_attempt_at, next_attempt_at, publish_failed, failed_at, last_error
		FROM memory_events_outbox
		WHERE `+where+`
		ORDER BY created_at ASC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemoryOutboxRows(rows)
}

func scanMemoryOutboxRows(rows *sql.Rows) ([]MemoryOutboxEvent, error) {
	out := []MemoryOutboxEvent{}
	for rows.Next() {
		var ev MemoryOutboxEvent
		var createdAt int64
		var lastAttemptAt, nextAttemptAt, failedAt sql.NullInt64
		var publishFailed int
		var lastError sql.NullString
		if err := rows.Scan(&ev.ID, &ev.RecordID, &ev.EventKind, &ev.Payload, &createdAt, &ev.Attempts, &lastAttemptAt, &nextAttemptAt, &publishFailed, &failedAt, &lastError); err != nil {
			return nil, err
		}
		ev.CreatedAt = time.Unix(createdAt, 0).UTC()
		if lastAttemptAt.Valid && lastAttemptAt.Int64 > 0 {
			ev.LastAttemptAt = time.Unix(lastAttemptAt.Int64, 0).UTC()
		}
		if nextAttemptAt.Valid && nextAttemptAt.Int64 > 0 {
			ev.NextAttemptAt = time.Unix(nextAttemptAt.Int64, 0).UTC()
		}
		if failedAt.Valid && failedAt.Int64 > 0 {
			ev.FailedAt = time.Unix(failedAt.Int64, 0).UTC()
		}
		ev.PublishFailed = publishFailed != 0
		ev.LastError = lastError.String
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (b *SQLiteBackend) MarkMemoryOutboxPublished(ctx context.Context, id int64) error {
	_ = ctx
	start := time.Now()
	if b == nil {
		return fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.db.Exec(`DELETE FROM memory_events_outbox WHERE id = ?`, id)
	recordMemoryTelemetry("outbox", start, map[string]any{"ok": err == nil, "op": "published", "id": id})
	return err
}

func (b *SQLiteBackend) MarkMemoryOutboxAttempt(ctx context.Context, id int64, publishErr error, now time.Time) error {
	_ = ctx
	start := time.Now()
	if b == nil {
		return fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	msg := "publish failed"
	if publishErr != nil && strings.TrimSpace(publishErr.Error()) != "" {
		msg = publishErr.Error()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var attempts int
	var createdAt int64
	if err := b.db.QueryRow(`SELECT attempts, created_at FROM memory_events_outbox WHERE id = ?`, id).Scan(&attempts, &createdAt); err != nil {
		return err
	}
	nextAttempts := attempts + 1
	age := now.UTC().Sub(time.Unix(createdAt, 0).UTC())
	if age >= memoryOutboxTerminalFailureAfter {
		_, err := b.db.Exec(`
			UPDATE memory_events_outbox
			SET attempts = ?, last_attempt_at = ?, next_attempt_at = NULL, publish_failed = 1, failed_at = ?, last_error = ?
			WHERE id = ?
		`, nextAttempts, now.UTC().Unix(), now.UTC().Unix(), msg, id)
		recordMemoryTelemetry("outbox", start, map[string]any{"ok": err == nil, "op": "attempt", "id": id, "attempts": nextAttempts, "publish_failed": true})
		return err
	}
	backoff := memoryOutboxBackoffForAttempt(nextAttempts)
	_, err := b.db.Exec(`
		UPDATE memory_events_outbox
		SET attempts = ?, last_attempt_at = ?, next_attempt_at = ?, last_error = ?
		WHERE id = ?
	`, nextAttempts, now.UTC().Unix(), now.UTC().Add(backoff).Unix(), msg, id)
	recordMemoryTelemetry("outbox", start, map[string]any{"ok": err == nil, "op": "attempt", "id": id, "attempts": nextAttempts, "next_backoff_seconds": int(backoff.Seconds())})
	return err
}

func memoryOutboxBackoffForAttempt(attempt int) time.Duration {
	if attempt <= 0 {
		return memoryOutboxRetryBackoff[0]
	}
	idx := attempt - 1
	if idx >= len(memoryOutboxRetryBackoff) {
		idx = len(memoryOutboxRetryBackoff) - 1
	}
	return memoryOutboxRetryBackoff[idx]
}

func (b *SQLiteBackend) ForceRepublishMemoryOutbox(ctx context.Context, now time.Time) (int, error) {
	_ = ctx
	start := time.Now()
	if b == nil {
		return 0, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return 0, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	res, err := b.db.Exec(`
		UPDATE memory_events_outbox
		SET publish_failed = 0, failed_at = NULL, attempts = 0, last_error = NULL, next_attempt_at = ?
		WHERE publish_failed != 0
	`, now.UTC().Unix())
	n := 0
	if err == nil {
		if rows, rerr := res.RowsAffected(); rerr == nil {
			n = int(rows)
		}
	}
	recordMemoryTelemetry("outbox", start, map[string]any{"ok": err == nil, "op": "force_republish", "reset": n})
	return n, err
}

func (b *SQLiteBackend) CompactMemoryOutbox(ctx context.Context, now time.Time) (int, error) {
	_ = ctx
	if b == nil {
		return 0, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return 0, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return compactMemoryOutboxLocked(b.db, now)
}

func compactMemoryOutboxLocked(db *sql.DB, now time.Time) (int, error) {
	cutoff := now.UTC().Add(-memoryOutboxFailedRetention).Unix()
	res, err := db.Exec(`DELETE FROM memory_events_outbox WHERE publish_failed != 0 AND failed_at IS NOT NULL AND failed_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (b *SQLiteBackend) MemoryOutboxStats(ctx context.Context) (MemoryOutboxStats, error) {
	_ = ctx
	if b == nil {
		return MemoryOutboxStats{}, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return MemoryOutboxStats{}, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	stats := MemoryOutboxStats{RetryCounts: map[string]int{}}
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_events_outbox WHERE publish_failed = 0`).Scan(&stats.OutboxDepth)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_events_outbox WHERE publish_failed != 0`).Scan(&stats.PublishFailures)
	var oldest sql.NullInt64
	_ = b.db.QueryRow(`SELECT MIN(created_at) FROM memory_events_outbox WHERE publish_failed = 0`).Scan(&oldest)
	if oldest.Valid && oldest.Int64 > 0 {
		stats.OldestPending = time.Unix(oldest.Int64, 0).UTC().Format(time.RFC3339)
	}
	rows, err := b.db.Query(`SELECT attempts, COUNT(*) FROM memory_events_outbox GROUP BY attempts ORDER BY attempts`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var attempts, count int
			if rows.Scan(&attempts, &count) == nil {
				stats.RetryCounts[fmt.Sprintf("%d", attempts)] = count
			}
		}
	}
	if len(stats.RetryCounts) == 0 {
		stats.RetryCounts = nil
	}
	return stats, nil
}
