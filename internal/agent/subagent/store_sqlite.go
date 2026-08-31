package subagent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// SQLiteRunStore persists canonical records and indexed ownership/delivery
// projections in one row/transaction.
type SQLiteRunStore struct {
	db *sql.DB
}

func OpenSQLiteRunStore(path string) (*SQLiteRunStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("subagent registry path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	dsn := path
	if path != ":memory:" {
		dsn = fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL", path)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &SQLiteRunStore{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if path != ":memory:" {
		_ = os.Chmod(path, 0o600)
	}
	return s, nil
}

func (s *SQLiteRunStore) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS subagent_runs (
  run_id TEXT PRIMARY KEY,
  child_session_key TEXT NOT NULL,
  controller_session_key TEXT NOT NULL DEFAULT '',
  requester_session_key TEXT NOT NULL DEFAULT '',
  parent_run_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  execution_status TEXT NOT NULL,
  delivery_status TEXT NOT NULL,
  next_delivery_at INTEGER NOT NULL DEFAULT 0,
  payload_json BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS subagent_runs_child_idx ON subagent_runs(child_session_key, created_at DESC);
CREATE INDEX IF NOT EXISTS subagent_runs_controller_idx ON subagent_runs(controller_session_key, created_at DESC);
CREATE INDEX IF NOT EXISTS subagent_runs_requester_idx ON subagent_runs(requester_session_key, created_at DESC);
CREATE INDEX IF NOT EXISTS subagent_runs_execution_idx ON subagent_runs(execution_status, updated_at);
CREATE INDEX IF NOT EXISTS subagent_runs_delivery_idx ON subagent_runs(delivery_status, next_delivery_at);
CREATE TABLE IF NOT EXISTS subagent_schema (version INTEGER NOT NULL);
INSERT INTO subagent_schema(version) SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM subagent_schema);`
	_, err := s.db.Exec(schema)
	return err
}

func (s *SQLiteRunStore) LoadAll() ([]SubagentRunRecord, error) {
	rows, err := s.db.Query(`SELECT run_id, child_session_key, controller_session_key, requester_session_key, parent_run_id, created_at, updated_at, execution_status, delivery_status, next_delivery_at, payload_json FROM subagent_runs ORDER BY created_at, run_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubagentRunRecord
	for rows.Next() {
		var runID, child, controller, requester, parent, execution, delivery string
		var created, updated, next int64
		var payload []byte
		if err := rows.Scan(&runID, &child, &controller, &requester, &parent, &created, &updated, &execution, &delivery, &next, &payload); err != nil {
			return nil, err
		}
		var rec SubagentRunRecord
		if err := json.Unmarshal(payload, &rec); err != nil {
			return nil, fmt.Errorf("decode subagent run %s: %w", runID, err)
		}
		// Indexed identity/state projections are authoritative.
		rec.RunID, rec.ChildSessionKey, rec.ControllerSessionKey = runID, child, controller
		rec.RequesterSessionKey, rec.ParentRunID = requester, parent
		rec.CreatedAt, rec.UpdatedAt = created, updated
		rec.ExecutionStatus = execution
		rec.Delivery.Status, rec.Delivery.NextAttemptAt = delivery, next
		out = append(out, normalizeRunRecord(rec))
	}
	return out, rows.Err()
}

func (s *SQLiteRunStore) Insert(rec SubagentRunRecord) error {
	return s.write(rec, false)
}

func (s *SQLiteRunStore) Upsert(rec SubagentRunRecord) error {
	return s.write(rec, true)
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func (s *SQLiteRunStore) write(rec SubagentRunRecord, upsert bool) error {
	return writeRunRecord(s.db, rec, upsert)
}

func writeRunRecord(exec sqlExecer, rec SubagentRunRecord, upsert bool) error {
	rec = normalizeRunRecord(rec)
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	query := `INSERT INTO subagent_runs(run_id, child_session_key, controller_session_key, requester_session_key, parent_run_id, created_at, updated_at, execution_status, delivery_status, next_delivery_at, payload_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)`
	if upsert {
		query += ` ON CONFLICT(run_id) DO UPDATE SET child_session_key=excluded.child_session_key, controller_session_key=excluded.controller_session_key, requester_session_key=excluded.requester_session_key, parent_run_id=excluded.parent_run_id, created_at=excluded.created_at, updated_at=excluded.updated_at, execution_status=excluded.execution_status, delivery_status=excluded.delivery_status, next_delivery_at=excluded.next_delivery_at, payload_json=excluded.payload_json`
	}
	_, err = exec.Exec(query, rec.RunID, rec.ChildSessionKey, rec.ControllerSessionKey, rec.RequesterSessionKey, rec.ParentRunID, rec.CreatedAt, rec.UpdatedAt, rec.ExecutionStatus, rec.Delivery.Status, rec.Delivery.NextAttemptAt, payload)
	return err
}

func (s *SQLiteRunStore) Replace(oldRunID string, replacement SubagentRunRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if oldRunID == replacement.RunID {
		if err := writeRunRecord(tx, replacement, true); err != nil {
			return err
		}
	} else {
		if err := writeRunRecord(tx, replacement, false); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM subagent_runs WHERE run_id=?`, strings.TrimSpace(oldRunID)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteRunStore) Delete(runID string) error {
	_, err := s.db.Exec(`DELETE FROM subagent_runs WHERE run_id=?`, strings.TrimSpace(runID))
	return err
}

func (s *SQLiteRunStore) Close() error { return s.db.Close() }
