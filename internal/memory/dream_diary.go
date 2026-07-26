package memory

// dream_diary.go — persisted, structured "dream diary" for the memory
// consolidation ("dreaming") subsystem (swarmstr-qc53.1).
//
// Historically swarmstr's dreaming phase (dreaming.go / RunDreamingPhases)
// produced EPHEMERAL narratives that were reported once and then lost. This
// module adds a durable, dated diary: each consolidation cycle records a
// structured entry {date, phase, candidates-considered, promoted-record-ids,
// counts, narrative} in a local SQLite table, and mirrors that entry — NIP-44
// encrypted when a payload encryptor is configured — through the EXISTING
// memory outbox seam (nostr_outbox.go: memory_events_outbox). No new transport
// is introduced; the diary reuses EnqueueMemoryOutboxEvent with a dedicated
// event kind.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MemoryOutboxKindDreamDiary is the memory_events_outbox event kind used for
// mirrored dream-diary entries.
const MemoryOutboxKindDreamDiary = "dream_diary"

// dreamDiaryDateLayout is the canonical UTC calendar-day bucket for entries.
const dreamDiaryDateLayout = "2006-01-02"

// MemoryPayloadEncryptor encrypts a memory outbox payload before it is enqueued
// for publish. Implementations wrap a NIP-44 self codec (encrypt-to-self) so
// diary entries leave the node as ciphertext, exactly like other encrypted
// memory outbox events.
type MemoryPayloadEncryptor interface {
	// EncryptMemoryPayload returns the ciphertext and the algorithm label used.
	EncryptMemoryPayload(plaintext string) (ciphertext string, algo string, err error)
}

// DreamDiaryEntry is one persisted, dated consolidation record.
type DreamDiaryEntry struct {
	ID                   string         `json:"id"`
	CreatedAt            time.Time      `json:"created_at"`
	Date                 string         `json:"date"` // UTC YYYY-MM-DD bucket
	Phase                DreamingPhase  `json:"phase"`
	Scope                string         `json:"scope,omitempty"`
	CandidatesConsidered int            `json:"candidates_considered"`
	CandidateIDs         []string       `json:"candidate_ids,omitempty"`
	PromotedRecordIDs    []string       `json:"promoted_record_ids,omitempty"`
	Counts               map[string]int `json:"counts,omitempty"`
	Narrative            string         `json:"narrative,omitempty"`
	Synthetic            bool           `json:"synthetic,omitempty"`
	DurationMS           int64          `json:"duration_ms,omitempty"`
}

// DreamDiaryFilter constrains ListDreamDiaryEntries.
type DreamDiaryFilter struct {
	Scope     string        `json:"scope,omitempty"`
	Phase     DreamingPhase `json:"phase,omitempty"`
	SinceUnix int64         `json:"since_unix,omitempty"`
	UntilUnix int64         `json:"until_unix,omitempty"`
	Synthetic *bool         `json:"synthetic,omitempty"`
	Limit     int           `json:"limit,omitempty"`
}

// DreamDiaryOutboxPayload is the JSON envelope enqueued into
// memory_events_outbox for each diary entry. When Encrypted is true, Content is
// NIP-44 ciphertext of the entry JSON; otherwise Content is the entry JSON in
// cleartext (local-only / test).
type DreamDiaryOutboxPayload struct {
	EntryID   string `json:"entry_id"`
	Date      string `json:"date"`
	Phase     string `json:"phase"`
	Scope     string `json:"scope,omitempty"`
	Encrypted bool   `json:"encrypted"`
	Algo      string `json:"algo,omitempty"`
	Content   string `json:"content"`
}

// DreamDiaryStore is implemented by backends that persist dream-diary entries.
type DreamDiaryStore interface {
	AppendDreamDiaryEntry(ctx context.Context, entry DreamDiaryEntry) (DreamDiaryEntry, error)
	ListDreamDiaryEntries(ctx context.Context, filter DreamDiaryFilter) ([]DreamDiaryEntry, error)
	ResetDreamDiary(ctx context.Context, scope string) (int, error)
	DreamDiaryEntryExists(ctx context.Context, scope, date string, phase DreamingPhase, synthetic bool) (bool, error)
}

func (b *SQLiteBackend) ensureDreamDiarySchema() error {
	if b == nil || b.db == nil {
		return fmt.Errorf("sqlite backend is closed")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return err
	}
	_, err := b.db.Exec(`
		CREATE TABLE IF NOT EXISTS dream_diary (
			id TEXT PRIMARY KEY,
			created_at INTEGER NOT NULL,
			date TEXT NOT NULL,
			phase TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT '',
			candidates_considered INTEGER NOT NULL DEFAULT 0,
			candidate_ids TEXT,
			promoted_record_ids TEXT,
			counts TEXT,
			narrative TEXT,
			synthetic INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_dream_diary_created ON dream_diary(scope, created_at);
		CREATE INDEX IF NOT EXISTS idx_dream_diary_date ON dream_diary(date);
		-- Synthetic (backfilled) entries are unique per (scope, date, phase) so
		-- replaying backfill is idempotent. Live entries are NOT constrained: a
		-- node may dream many times per day.
		CREATE UNIQUE INDEX IF NOT EXISTS idx_dream_diary_synth_dedupe
			ON dream_diary(scope, date, phase) WHERE synthetic = 1;
	`)
	return err
}

func normalizeDreamDiaryEntry(entry DreamDiaryEntry) DreamDiaryEntry {
	entry.ID = strings.TrimSpace(entry.ID)
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	} else {
		entry.CreatedAt = entry.CreatedAt.UTC()
	}
	if entry.ID == "" {
		entry.ID = StableMemoryRecordID("dream_diary", entry.Scope, string(entry.Phase),
			fmt.Sprintf("%d", entry.CreatedAt.UnixNano()))
	}
	entry.Date = strings.TrimSpace(entry.Date)
	if entry.Date == "" {
		entry.Date = entry.CreatedAt.Format(dreamDiaryDateLayout)
	}
	if entry.Phase != DreamingPhaseLight && entry.Phase != DreamingPhaseREM {
		entry.Phase = DreamingPhaseREM
	}
	entry.Scope = strings.TrimSpace(entry.Scope)
	if entry.CandidateIDs == nil {
		entry.CandidateIDs = []string{}
	}
	if entry.PromotedRecordIDs == nil {
		entry.PromotedRecordIDs = []string{}
	}
	if entry.Counts == nil {
		entry.Counts = map[string]int{}
	}
	if _, ok := entry.Counts["considered"]; !ok {
		entry.Counts["considered"] = entry.CandidatesConsidered
	}
	if _, ok := entry.Counts["promoted"]; !ok {
		entry.Counts["promoted"] = len(entry.PromotedRecordIDs)
	}
	return entry
}

// AppendDreamDiaryEntry persists a diary entry and mirrors it through the
// memory outbox (encrypted when an encryptor is configured). Synthetic entries
// that would collide with an existing (scope, date, phase) row are treated as a
// no-op (idempotent backfill) and the existing row is returned.
func (b *SQLiteBackend) AppendDreamDiaryEntry(ctx context.Context, entry DreamDiaryEntry) (DreamDiaryEntry, error) {
	if b == nil {
		return DreamDiaryEntry{}, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureDreamDiarySchema(); err != nil {
		return DreamDiaryEntry{}, err
	}
	entry = normalizeDreamDiaryEntry(entry)

	candidateJSON, _ := json.Marshal(entry.CandidateIDs)
	promotedJSON, _ := json.Marshal(entry.PromotedRecordIDs)
	countsJSON, _ := json.Marshal(entry.Counts)
	synthetic := 0
	if entry.Synthetic {
		synthetic = 1
	}

	// Build the (possibly-encrypted) outbox payload BEFORE taking the write lock:
	// the encryptor may be a network-backed NIP-46 signer, and encryption must
	// not run under b.mu.
	outboxJSON, err := b.buildDreamDiaryOutboxPayloadJSON(entry)
	if err != nil {
		return DreamDiaryEntry{}, err
	}

	// Persist the diary row and enqueue the outbox mirror ATOMICALLY: either both
	// land or neither does, so a synthetic entry can never be left permanently
	// unpublished and a live entry can never be half-written.
	b.mu.Lock()
	affected, txErr := func() (int64, error) {
		tx, beginErr := b.db.Begin()
		if beginErr != nil {
			return 0, beginErr
		}
		res, execErr := tx.Exec(`
			INSERT OR IGNORE INTO dream_diary (
				id, created_at, date, phase, scope, candidates_considered,
				candidate_ids, promoted_record_ids, counts, narrative, synthetic, duration_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, entry.ID, entry.CreatedAt.Unix(), entry.Date, string(entry.Phase), entry.Scope,
			entry.CandidatesConsidered, string(candidateJSON), string(promotedJSON),
			string(countsJSON), entry.Narrative, synthetic, entry.DurationMS)
		if execErr != nil {
			_ = tx.Rollback()
			return 0, execErr
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			if _, oErr := tx.Exec(`
				INSERT INTO memory_events_outbox (record_id, event_kind, payload, created_at, attempts, next_attempt_at, publish_failed)
				VALUES (?, ?, ?, ?, 0, ?, 0)
			`, entry.ID, MemoryOutboxKindDreamDiary, outboxJSON, entry.CreatedAt.Unix(), entry.CreatedAt.Unix()); oErr != nil {
				_ = tx.Rollback()
				return 0, oErr
			}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return 0, commitErr
		}
		return n, nil
	}()
	b.mu.Unlock()
	if txErr != nil {
		return DreamDiaryEntry{}, fmt.Errorf("append dream diary: %w", txErr)
	}
	if affected == 0 {
		// Synthetic dedupe collision: return the pre-existing row unchanged. The
		// outbox event was written when the original row was inserted.
		existing, found, gErr := b.getDreamDiaryEntryByKey(entry.Scope, entry.Date, entry.Phase, true)
		if gErr != nil {
			return DreamDiaryEntry{}, gErr
		}
		if found {
			return existing, nil
		}
	}
	return entry, nil
}

// buildDreamDiaryOutboxPayloadJSON marshals the diary entry, encrypts it with
// the configured NIP-44 self encryptor when present, and returns the outbox
// envelope JSON. Encryption is performed outside any store lock.
func (b *SQLiteBackend) buildDreamDiaryOutboxPayloadJSON(entry DreamDiaryEntry) (string, error) {
	body, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	payload := DreamDiaryOutboxPayload{
		EntryID: entry.ID,
		Date:    entry.Date,
		Phase:   string(entry.Phase),
		Scope:   entry.Scope,
		Content: string(body),
	}
	b.mu.RLock()
	enc := b.payloadEncryptor
	b.mu.RUnlock()
	if enc != nil {
		ciphertext, algo, encErr := enc.EncryptMemoryPayload(string(body))
		if encErr != nil {
			return "", fmt.Errorf("encrypt dream diary payload: %w", encErr)
		}
		payload.Encrypted = true
		payload.Algo = algo
		payload.Content = ciphertext
	}
	envelope, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(envelope), nil
}

func (b *SQLiteBackend) getDreamDiaryEntryByKey(scope, date string, phase DreamingPhase, synthetic bool) (DreamDiaryEntry, bool, error) {
	synthVal := 0
	if synthetic {
		synthVal = 1
	}
	b.mu.RLock()
	row := b.db.QueryRow(`
		SELECT id, created_at, date, phase, scope, candidates_considered,
		       candidate_ids, promoted_record_ids, counts, narrative, synthetic, duration_ms
		FROM dream_diary
		WHERE scope = ? AND date = ? AND phase = ? AND synthetic = ?
		LIMIT 1
	`, scope, date, string(phase), synthVal)
	entry, err := scanDreamDiaryRow(row)
	b.mu.RUnlock()
	if err == sql.ErrNoRows {
		return DreamDiaryEntry{}, false, nil
	}
	if err != nil {
		return DreamDiaryEntry{}, false, err
	}
	return entry, true, nil
}

// DreamDiaryEntryExists reports whether a diary row already exists for the key.
func (b *SQLiteBackend) DreamDiaryEntryExists(ctx context.Context, scope, date string, phase DreamingPhase, synthetic bool) (bool, error) {
	_ = ctx
	if b == nil {
		return false, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureDreamDiarySchema(); err != nil {
		return false, err
	}
	_, found, err := b.getDreamDiaryEntryByKey(strings.TrimSpace(scope), strings.TrimSpace(date), phase, synthetic)
	return found, err
}

type dreamDiaryScanner interface {
	Scan(dest ...any) error
}

func scanDreamDiaryRow(row dreamDiaryScanner) (DreamDiaryEntry, error) {
	var (
		entry         DreamDiaryEntry
		createdAt     int64
		phase         string
		candidateJSON sql.NullString
		promotedJSON  sql.NullString
		countsJSON    sql.NullString
		narrative     sql.NullString
		synthetic     int
	)
	if err := row.Scan(&entry.ID, &createdAt, &entry.Date, &phase, &entry.Scope,
		&entry.CandidatesConsidered, &candidateJSON, &promotedJSON, &countsJSON,
		&narrative, &synthetic, &entry.DurationMS); err != nil {
		return DreamDiaryEntry{}, err
	}
	entry.CreatedAt = time.Unix(createdAt, 0).UTC()
	entry.Phase = DreamingPhase(phase)
	entry.Synthetic = synthetic != 0
	entry.Narrative = narrative.String
	entry.CandidateIDs = []string{}
	entry.PromotedRecordIDs = []string{}
	entry.Counts = map[string]int{}
	if candidateJSON.Valid && candidateJSON.String != "" {
		_ = json.Unmarshal([]byte(candidateJSON.String), &entry.CandidateIDs)
	}
	if promotedJSON.Valid && promotedJSON.String != "" {
		_ = json.Unmarshal([]byte(promotedJSON.String), &entry.PromotedRecordIDs)
	}
	if countsJSON.Valid && countsJSON.String != "" {
		_ = json.Unmarshal([]byte(countsJSON.String), &entry.Counts)
	}
	return entry, nil
}

// ListDreamDiaryEntries returns diary entries matching the filter, newest-first.
func (b *SQLiteBackend) ListDreamDiaryEntries(ctx context.Context, filter DreamDiaryFilter) ([]DreamDiaryEntry, error) {
	_ = ctx
	if b == nil {
		return nil, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureDreamDiarySchema(); err != nil {
		return nil, err
	}
	where := []string{"1 = 1"}
	args := []any{}
	if s := strings.TrimSpace(filter.Scope); s != "" {
		where = append(where, "scope = ?")
		args = append(args, s)
	}
	if filter.Phase == DreamingPhaseLight || filter.Phase == DreamingPhaseREM {
		where = append(where, "phase = ?")
		args = append(args, string(filter.Phase))
	}
	if filter.SinceUnix > 0 {
		where = append(where, "created_at >= ?")
		args = append(args, filter.SinceUnix)
	}
	if filter.UntilUnix > 0 {
		where = append(where, "created_at <= ?")
		args = append(args, filter.UntilUnix)
	}
	if filter.Synthetic != nil {
		v := 0
		if *filter.Synthetic {
			v = 1
		}
		where = append(where, "synthetic = ?")
		args = append(args, v)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	args = append(args, limit)

	b.mu.RLock()
	rows, err := b.db.Query(`
		SELECT id, created_at, date, phase, scope, candidates_considered,
		       candidate_ids, promoted_record_ids, counts, narrative, synthetic, duration_ms
		FROM dream_diary
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, args...)
	if err != nil {
		b.mu.RUnlock()
		return nil, err
	}
	out := []DreamDiaryEntry{}
	for rows.Next() {
		entry, scanErr := scanDreamDiaryRow(rows)
		if scanErr != nil {
			rows.Close()
			b.mu.RUnlock()
			return nil, scanErr
		}
		out = append(out, entry)
	}
	rows.Close()
	b.mu.RUnlock()
	return out, rows.Err()
}

// ResetDreamDiary clears diary entries for the given scope. It runs under the
// store maintenance lock so it cannot race a concurrent diary-writing dreaming
// cycle. It does NOT touch the underlying memory records — only the diary
// artifacts.
func (b *SQLiteBackend) ResetDreamDiary(ctx context.Context, scope string) (int, error) {
	_ = ctx
	if b == nil {
		return 0, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureDreamDiarySchema(); err != nil {
		return 0, err
	}
	scope = strings.TrimSpace(scope)
	var removed int
	err := b.WithMaintenanceLock(func() error {
		b.mu.Lock()
		defer b.mu.Unlock()
		res, execErr := b.db.Exec(`DELETE FROM dream_diary WHERE scope = ?`, scope)
		if execErr != nil {
			return execErr
		}
		n, _ := res.RowsAffected()
		removed = int(n)
		return nil
	})
	if err != nil {
		return 0, err
	}
	recordMemoryTelemetry("dream_diary", time.Time{}, map[string]any{"ok": true, "op": "reset", "scope": scope, "removed": removed})
	return removed, nil
}

// ── Diary-writing dreaming cycle ────────────────────────────────────────────

// DreamDiaryWriteOptions parameterizes a diary-writing dreaming cycle.
type DreamDiaryWriteOptions struct {
	Scope     string
	Synthetic bool
	Date      string    // optional YYYY-MM-DD override (used by backfill)
	Now       time.Time // optional clock override
}

// RunDreamingCycleWithDiary runs the light + REM dreaming phases and persists
// one structured diary entry per phase. It is the thin wrapper qc53.1 calls for
// into the consolidation path: the deterministic narrative is preserved but now
// durably recorded and mirrored through the outbox. Phases that consider zero
// candidates are skipped (no empty diary noise).
func RunDreamingCycleWithDiary(ctx context.Context, manager *PromotionManager, diary DreamDiaryStore, cfg DreamingConfig, opts DreamDiaryWriteOptions) (*DreamingResult, []DreamDiaryEntry, error) {
	if manager == nil {
		return &DreamingResult{}, nil, nil
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	// Force narratives on for the run so each persisted entry carries the
	// deterministic sleep report, regardless of the ephemeral-report toggle.
	runCfg := cfg
	runCfg.Enabled = true
	runCfg.Narratives = true

	var result *DreamingResult
	var entries []DreamDiaryEntry
	// Hold the maintenance lock across BOTH the promotion sweep and the diary
	// persistence so promotions and their audit records land as one logical
	// operation and a concurrent backfill cannot observe promotions before their
	// live diary entries exist.
	lockErr := manager.backend.WithMaintenanceLock(func() error {
		res, perr := RunDreamingPhases(manager, runCfg, nil)
		if perr != nil {
			return perr
		}
		result = res
		if res == nil || diary == nil {
			return nil
		}
		entries = make([]DreamDiaryEntry, 0, len(res.Phases))
		for _, phase := range res.Phases {
			if phase.Candidates == 0 && phase.Promoted == 0 {
				continue
			}
			entry := DreamDiaryEntry{
				CreatedAt:            now,
				Date:                 opts.Date,
				Phase:                phase.Phase,
				Scope:                opts.Scope,
				CandidatesConsidered: phase.Candidates,
				CandidateIDs:         phase.CandidateIDs,
				PromotedRecordIDs:    phase.PromotedIDs,
				Counts: map[string]int{
					"considered": phase.Candidates,
					"promoted":   phase.Promoted,
				},
				Narrative:  phase.Narrative,
				Synthetic:  opts.Synthetic,
				DurationMS: (phase.EndedUnix - phase.StartedUnix) * 1000,
			}
			saved, appendErr := diary.AppendDreamDiaryEntry(ctx, entry)
			if appendErr != nil {
				return appendErr
			}
			entries = append(entries, saved)
		}
		return nil
	})
	if lockErr != nil {
		return result, entries, lockErr
	}
	if result == nil {
		return &DreamingResult{}, nil, nil
	}
	return result, entries, nil
}

// ── Store dispatch (type-assert the active backend) ─────────────────────────

// AppendMemoryDreamDiaryEntry appends a diary entry via the store, or errors if
// the active backend has no diary store.
func AppendMemoryDreamDiaryEntry(ctx context.Context, store Store, entry DreamDiaryEntry) (DreamDiaryEntry, error) {
	if ds, ok := any(store).(DreamDiaryStore); ok {
		return ds.AppendDreamDiaryEntry(ctx, entry)
	}
	return DreamDiaryEntry{}, errNoDreamDiaryStore
}

// ListMemoryDreamDiary lists diary entries via the store.
func ListMemoryDreamDiary(ctx context.Context, store Store, filter DreamDiaryFilter) ([]DreamDiaryEntry, error) {
	if ds, ok := any(store).(DreamDiaryStore); ok {
		return ds.ListDreamDiaryEntries(ctx, filter)
	}
	return nil, errNoDreamDiaryStore
}

// ResetMemoryDreamDiary clears diary entries for a scope via the store.
func ResetMemoryDreamDiary(ctx context.Context, store Store, scope string) (int, error) {
	if ds, ok := any(store).(DreamDiaryStore); ok {
		return ds.ResetDreamDiary(ctx, scope)
	}
	return 0, errNoDreamDiaryStore
}

var errNoDreamDiaryStore = fmt.Errorf("memory store does not support a persisted dream diary")

// DreamDiaryManagerProvider is implemented by stores that can construct a
// PromotionManager over their concrete backend for diary-writing cycles.
type DreamDiaryManagerProvider interface {
	DreamDiaryManager(cfg PromotionConfig) *PromotionManager
}

// RunMemoryDreamingCycle runs a diary-writing dreaming cycle over the store. It
// is the entrypoint the background DreamingJob (qc53.2) and the gateway use. It
// returns errNoDreamDiaryStore when the active backend lacks a diary store.
func RunMemoryDreamingCycle(ctx context.Context, store Store, promoCfg PromotionConfig, cfg DreamingConfig, opts DreamDiaryWriteOptions) (*DreamingResult, []DreamDiaryEntry, error) {
	provider, ok := any(store).(DreamDiaryManagerProvider)
	if !ok {
		return nil, nil, errNoDreamDiaryStore
	}
	diary, dok := any(store).(DreamDiaryStore)
	if !dok {
		return nil, nil, errNoDreamDiaryStore
	}
	manager := provider.DreamDiaryManager(promoCfg)
	if manager == nil {
		return nil, nil, errNoDreamDiaryStore
	}
	return RunDreamingCycleWithDiary(ctx, manager, diary, cfg, opts)
}
