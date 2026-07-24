package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// FTSLifecycleState describes the independently observable health of the
// SQLite full-text projection. The primary chunks table remains authoritative.
type FTSLifecycleState string

const (
	FTSStateHealthy   FTSLifecycleState = "healthy"
	FTSStateRepairing FTSLifecycleState = "repairing"
	FTSStateDegraded  FTSLifecycleState = "degraded"
)

// FTSHealth is a snapshot of the FTS projection and its last repair attempt.
type FTSHealth struct {
	State             FTSLifecycleState `json:"state"`
	CheckedAtUnix     int64             `json:"checked_at_unix"`
	SourceRows        int               `json:"source_rows"`
	IndexedRows       int               `json:"indexed_rows"`
	MissingRows       int               `json:"missing_rows,omitempty"`
	OrphanedRows      int               `json:"orphaned_rows,omitempty"`
	TargetedReindexed int               `json:"targeted_reindexed,omitempty"`
	FullRebuild       bool              `json:"full_rebuild,omitempty"`
	LastError         string            `json:"last_error,omitempty"`
}

// FTSHealth returns the last lifecycle snapshot.
func (b *SQLiteBackend) FTSHealth() FTSHealth {
	if b == nil {
		return FTSHealth{State: FTSStateDegraded, LastError: "nil sqlite backend"}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.ftsHealth
}

// CheckFTSHealth validates counts, missing/orphan rows, and the FTS5 index
// against its external-content table. It never mutates the index.
func (b *SQLiteBackend) CheckFTSHealth(ctx context.Context) (FTSHealth, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	health, _, _, err := b.checkFTSHealthLocked(ctx)
	b.ftsHealth = health
	return health, err
}

// EnsureFTSHealthy performs bounded self-healing. Missing/orphan rows are
// repaired selectively; a full rebuild is reserved for an integrity failure
// that cannot identify a safe targeted repair set.
func (b *SQLiteBackend) EnsureFTSHealthy(ctx context.Context) (FTSHealth, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	health, missing, orphaned, checkErr := b.checkFTSHealthLocked(ctx)
	if checkErr == nil && len(missing) == 0 && len(orphaned) == 0 {
		b.ftsHealth = health
		return health, nil
	}
	health.State = FTSStateRepairing
	b.ftsHealth = health

	if len(missing) > 0 || len(orphaned) > 0 {
		repaired, err := b.repairFTSRowsLocked(ctx, missing, orphaned, false)
		health.TargetedReindexed = repaired
		if err != nil {
			// Continue to verification so the authoritative-table full rebuild can
			// recover cases that the selective repair transaction could not.
			health.LastError = err.Error()
		}
	}

	verified, _, _, verifyErr := b.checkFTSHealthLocked(ctx)
	verified.TargetedReindexed = health.TargetedReindexed
	if verifyErr != nil {
		// FTS5 can detect term-level drift that row counts cannot localize. Only
		// that case falls back to the existing authoritative-table rebuild.
		if _, err := b.db.ExecContext(ctx, `INSERT INTO chunks_fts(chunks_fts) VALUES('rebuild')`); err != nil {
			verified.State = FTSStateDegraded
			verified.LastError = fmt.Sprintf("fts rebuild after %v: %v", verifyErr, err)
			b.ftsHealth = verified
			return verified, fmt.Errorf("%s", verified.LastError)
		}
		verified, _, _, verifyErr = b.checkFTSHealthLocked(ctx)
		verified.TargetedReindexed = health.TargetedReindexed
		verified.FullRebuild = true
	}
	if verifyErr != nil {
		verified.State = FTSStateDegraded
		verified.LastError = verifyErr.Error()
		b.ftsHealth = verified
		return verified, verifyErr
	}
	b.clearCacheLocked()
	b.ftsHealth = verified
	return verified, nil
}

// ReindexFTSEntries refreshes only the named authoritative chunk rows.
func (b *SQLiteBackend) ReindexFTSEntries(ctx context.Context, ids []string) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	unique := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	repaired, err := b.repairFTSRowsLocked(ctx, unique, nil, true)
	if err != nil {
		b.ftsHealth = FTSHealth{State: FTSStateDegraded, CheckedAtUnix: time.Now().Unix(), LastError: err.Error()}
		return repaired, err
	}
	b.clearCacheLocked()
	health, _, _, checkErr := b.checkFTSHealthLocked(ctx)
	health.TargetedReindexed = repaired
	b.ftsHealth = health
	return repaired, checkErr
}

// ReindexFTSSession selectively refreshes transcript memories for one session.
func (b *SQLiteBackend) ReindexFTSSession(ctx context.Context, sessionID string) (int, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT id FROM chunks WHERE session_id = ? ORDER BY rowid`, sessionID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return b.ReindexFTSEntries(ctx, ids)
}

func (b *SQLiteBackend) checkFTSHealthLocked(ctx context.Context) (FTSHealth, []string, []int64, error) {
	health := FTSHealth{State: FTSStateHealthy, CheckedAtUnix: time.Now().Unix()}
	if b.db == nil {
		err := fmt.Errorf("sqlite backend is closed")
		health.State, health.LastError = FTSStateDegraded, err.Error()
		return health, nil, nil, err
	}
	if err := b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks`).Scan(&health.SourceRows); err != nil {
		health.State, health.LastError = FTSStateDegraded, err.Error()
		return health, nil, nil, err
	}
	if err := b.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT doc) FROM chunks_fts_vocab`).Scan(&health.IndexedRows); err != nil {
		health.State, health.LastError = FTSStateDegraded, err.Error()
		return health, nil, nil, err
	}
	missing, err := queryStringColumn(ctx, b.db, `SELECT c.id FROM chunks c LEFT JOIN (SELECT DISTINCT doc FROM chunks_fts_vocab) f ON f.doc = c.rowid WHERE f.doc IS NULL ORDER BY c.rowid`)
	if err != nil {
		health.State, health.LastError = FTSStateDegraded, err.Error()
		return health, nil, nil, err
	}
	orphaned, err := queryInt64Column(ctx, b.db, `SELECT DISTINCT f.doc FROM chunks_fts_vocab f LEFT JOIN chunks c ON c.rowid = f.doc WHERE c.rowid IS NULL ORDER BY f.doc`)
	if err != nil {
		health.State, health.LastError = FTSStateDegraded, err.Error()
		return health, nil, nil, err
	}
	health.MissingRows, health.OrphanedRows = len(missing), len(orphaned)
	if len(missing) > 0 || len(orphaned) > 0 {
		err := fmt.Errorf("fts projection mismatch: missing=%d orphaned=%d", len(missing), len(orphaned))
		health.State, health.LastError = FTSStateDegraded, err.Error()
		return health, missing, orphaned, err
	}
	if _, err := b.db.ExecContext(ctx, `INSERT INTO chunks_fts(chunks_fts, rank) VALUES('integrity-check', 1)`); err != nil {
		health.State, health.LastError = FTSStateDegraded, err.Error()
		return health, missing, orphaned, fmt.Errorf("fts integrity check: %w", err)
	}
	return health, missing, orphaned, nil
}

func (b *SQLiteBackend) repairFTSRowsLocked(ctx context.Context, ids []string, orphaned []int64, refresh bool) (int, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, rowid := range orphaned {
		if _, err := tx.ExecContext(ctx, `DELETE FROM chunks_fts WHERE rowid = ?`, rowid); err != nil {
			return 0, err
		}
	}
	repaired := 0
	for _, id := range ids {
		var rowid int64
		var storedID, text string
		var topic, keywords sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT rowid, id, text, topic, keywords FROM chunks WHERE id = ?`, id).Scan(&rowid, &storedID, &text, &topic, &keywords)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return repaired, err
		}
		if refresh {
			if _, err := tx.ExecContext(ctx, `DELETE FROM chunks_fts WHERE rowid = ?`, rowid); err != nil {
				return repaired, err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chunks_fts(rowid, id, text, topic, keywords) VALUES (?, ?, ?, ?, ?)`, rowid, storedID, text, topic, keywords); err != nil {
			return repaired, err
		}
		repaired++
	}
	if err := tx.Commit(); err != nil {
		return repaired, err
	}
	return repaired, nil
}

func queryStringColumn(ctx context.Context, db *sql.DB, query string) ([]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func queryInt64Column(ctx context.Context, db *sql.DB, query string) ([]int64, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
