package memory

// grounded_short_term.go — the "grounded short-term" memory tier (swarmstr-qc53.3).
//
// Grounded short-term is a recency-bounded, source-cited buffer of recent
// memories that exist BEFORE promotion. It is modeled as a *view* over the
// existing promotion tier (recall_tracking ⋈ chunks) rather than a new table:
// promotion status already lives in recall_tracking keyed by chunks.id, so the
// tier and its reset must operate on those same rows to stay consistent.
//
//   - membership = last recalled within Window AND (optionally) source-cited
//     (chunks.source non-empty — the memory records its provenance).
//   - the read API surfaces the whole window, flagging which rows are already
//     promoted into long-term.
//   - resetGroundedShortTerm DEMOTES the promoted rows in the window: it clears
//     the promotion markers (promoted_at / promoted_to) so the records fall back
//     into the pre-promotion buffer. It is fully reversible and NEVER deletes a
//     memory — the underlying chunk rows are untouched.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DefaultGroundedShortTermWindow bounds how far back the grounded-short-term
// buffer reaches by default.
const DefaultGroundedShortTermWindow = 72 * time.Hour

// GroundedShortTermOptions parameterizes the tier view and its reset.
type GroundedShortTermOptions struct {
	// Window bounds recency (default 72h). Memories last recalled before
	// now-Window are excluded.
	Window time.Duration
	// RequireCitation requires a non-empty source (provenance) to be part of the
	// tier. Defaults to true.
	RequireCitation bool
	// Scope optionally restricts the tier to a single agent/workspace namespace.
	Scope ScopedContext
	// Limit caps the number of read rows (default 200).
	Limit int
	// Now overrides the clock (tests / backfill).
	Now time.Time
	// requireCitationSet distinguishes an explicit RequireCitation=false from the
	// zero value so the default can be true.
	requireCitationSet bool
}

// WithExplicitCitation marks RequireCitation as explicitly provided.
func (o GroundedShortTermOptions) WithExplicitCitation(v bool) GroundedShortTermOptions {
	o.RequireCitation = v
	o.requireCitationSet = true
	return o
}

func (o GroundedShortTermOptions) normalized() GroundedShortTermOptions {
	if o.Window <= 0 {
		o.Window = DefaultGroundedShortTermWindow
	}
	if !o.requireCitationSet {
		o.RequireCitation = true
	}
	if o.Limit <= 0 || o.Limit > 1000 {
		o.Limit = 200
	}
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	} else {
		o.Now = o.Now.UTC()
	}
	return o
}

// GroundedShortTermItem is one memory in the tier view.
type GroundedShortTermItem struct {
	MemoryID        string   `json:"memory_id"`
	Topic           string   `json:"topic,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	Source          string   `json:"source,omitempty"`
	Keywords        []string `json:"keywords,omitempty"`
	RecallCount     int      `json:"recall_count"`
	UniqueQueries   int      `json:"unique_queries"`
	FirstRecallUnix int64    `json:"first_recall_unix"`
	LastRecallUnix  int64    `json:"last_recall_unix"`
	Promoted        bool     `json:"promoted"`
}

// GroundedShortTermResetResult reports a demote-based reset.
type GroundedShortTermResetResult struct {
	WindowSeconds int64    `json:"window_seconds"`
	Considered    int      `json:"considered"`
	Demoted       int      `json:"demoted"`
	DemotedIDs    []string `json:"demoted_ids,omitempty"`
}

// GroundedShortTermStore is implemented by backends that expose the tier.
type GroundedShortTermStore interface {
	GroundedShortTerm(ctx context.Context, opts GroundedShortTermOptions) ([]GroundedShortTermItem, error)
	ResetGroundedShortTerm(ctx context.Context, opts GroundedShortTermOptions) (GroundedShortTermResetResult, error)
}

type groundedRow struct {
	item      GroundedShortTermItem
	sessionID string
}

// queryGroundedRows returns the tier rows for the window. When onlyPromoted is
// true only rows already promoted into long-term are returned (used by reset).
func (b *SQLiteBackend) queryGroundedRows(opts GroundedShortTermOptions, onlyPromoted bool) ([]groundedRow, error) {
	cutoff := opts.Now.Add(-opts.Window).Unix()
	where := []string{"rt.last_recall_unix >= ?"}
	args := []any{cutoff}
	if opts.RequireCitation {
		where = append(where, "c.source IS NOT NULL AND TRIM(c.source) != ''")
	}
	if onlyPromoted {
		where = append(where, "rt.promoted_at IS NOT NULL")
	}
	limitClause := ""
	if !onlyPromoted {
		limitClause = "LIMIT ?"
		args = append(args, opts.Limit)
	}
	b.mu.RLock()
	rows, err := b.db.Query(`
		SELECT rt.memory_id, rt.recall_count, rt.unique_queries,
		       rt.first_recall_unix, rt.last_recall_unix, rt.promoted_at,
		       c.topic, c.text, c.source, c.keywords, c.session_id
		FROM recall_tracking rt
		JOIN chunks c ON c.id = rt.memory_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY rt.last_recall_unix DESC
		`+limitClause, args...)
	if err != nil {
		b.mu.RUnlock()
		return nil, err
	}
	defer func() {
		rows.Close()
		b.mu.RUnlock()
	}()

	out := make([]groundedRow, 0, 32)
	for rows.Next() {
		var (
			gr           groundedRow
			promotedAt   sql.NullInt64
			text         string
			source       sql.NullString
			keywordsJSON sql.NullString
			sessionID    sql.NullString
		)
		if err := rows.Scan(&gr.item.MemoryID, &gr.item.RecallCount, &gr.item.UniqueQueries,
			&gr.item.FirstRecallUnix, &gr.item.LastRecallUnix, &promotedAt,
			&gr.item.Topic, &text, &source, &keywordsJSON, &sessionID); err != nil {
			return nil, err
		}
		gr.item.Promoted = promotedAt.Valid && promotedAt.Int64 > 0
		gr.item.Source = source.String
		gr.item.Summary = summarizeMemoryText(text, 220)
		gr.sessionID = sessionID.String
		if keywordsJSON.Valid && keywordsJSON.String != "" {
			_ = json.Unmarshal([]byte(keywordsJSON.String), &gr.item.Keywords)
		}
		out = append(out, gr)
	}
	return out, rows.Err()
}

// filterGroundedByScope drops rows that do not match the requested scope. Scope
// membership reuses the shared scope keyword/session semantics (scope.go).
func filterGroundedByScope(rows []groundedRow, scope ScopedContext) []groundedRow {
	if !scope.Enabled() {
		return rows
	}
	out := make([]groundedRow, 0, len(rows))
	for _, gr := range rows {
		im := IndexedMemory{MemoryID: gr.item.MemoryID, Keywords: gr.item.Keywords, SessionID: gr.sessionID}
		if MatchScope(im, scope) {
			out = append(out, gr)
		}
	}
	return out
}

// GroundedShortTerm returns the recency-bounded, source-cited buffer view.
func (b *SQLiteBackend) GroundedShortTerm(ctx context.Context, opts GroundedShortTermOptions) ([]GroundedShortTermItem, error) {
	_ = ctx
	if b == nil {
		return nil, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return nil, err
	}
	opts = opts.normalized()
	rows, err := b.queryGroundedRows(opts, false)
	if err != nil {
		return nil, err
	}
	rows = filterGroundedByScope(rows, opts.Scope)
	out := make([]GroundedShortTermItem, 0, len(rows))
	for _, gr := range rows {
		out = append(out, gr.item)
	}
	return out, nil
}

// ResetGroundedShortTerm demotes (unpromotes) the promoted memories inside the
// grounded-short-term window, moving them back out of long-term. It never
// deletes records; it only clears the promotion markers in recall_tracking, so
// the operation is fully reversible (a later dreaming cycle can re-promote).
func (b *SQLiteBackend) ResetGroundedShortTerm(ctx context.Context, opts GroundedShortTermOptions) (GroundedShortTermResetResult, error) {
	_ = ctx
	if b == nil {
		return GroundedShortTermResetResult{}, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return GroundedShortTermResetResult{}, err
	}
	opts = opts.normalized()
	result := GroundedShortTermResetResult{WindowSeconds: int64(opts.Window.Seconds())}

	err := b.WithMaintenanceLock(func() error {
		rows, qErr := b.queryGroundedRows(opts, true)
		if qErr != nil {
			return qErr
		}
		rows = filterGroundedByScope(rows, opts.Scope)
		result.Considered = len(rows)
		if len(rows) == 0 {
			return nil
		}
		ids := make([]string, 0, len(rows))
		for _, gr := range rows {
			ids = append(ids, gr.item.MemoryID)
		}
		// Batch the UPDATE so a large window cannot exceed SQLite's bind-variable
		// limit. All batches run under the single maintenance lock.
		const batchSize = 500
		var demoted int64
		b.mu.Lock()
		for start := 0; start < len(ids); start += batchSize {
			end := start + batchSize
			if end > len(ids) {
				end = len(ids)
			}
			chunk := ids[start:end]
			placeholders := make([]string, len(chunk))
			args := make([]any, len(chunk))
			for i, id := range chunk {
				placeholders[i] = "?"
				args[i] = id
			}
			res, execErr := b.db.Exec(`
				UPDATE recall_tracking
				SET promoted_at = NULL, promoted_to = NULL
				WHERE memory_id IN (`+strings.Join(placeholders, ",")+`)
			`, args...)
			if execErr != nil {
				b.mu.Unlock()
				return execErr
			}
			n, _ := res.RowsAffected()
			demoted += n
		}
		b.mu.Unlock()
		result.Demoted = int(demoted)
		result.DemotedIDs = ids
		return nil
	})
	if err != nil {
		return GroundedShortTermResetResult{}, err
	}
	recordMemoryTelemetry("grounded_short_term", time.Time{}, map[string]any{"ok": true, "op": "reset", "considered": result.Considered, "demoted": result.Demoted})
	return result, nil
}

// ── Store dispatch ──────────────────────────────────────────────────────────

var errNoGroundedShortTerm = fmt.Errorf("memory store does not support a grounded-short-term tier")

// MemoryGroundedShortTerm returns the tier view via the store.
func MemoryGroundedShortTerm(ctx context.Context, store Store, opts GroundedShortTermOptions) ([]GroundedShortTermItem, error) {
	if gs, ok := any(store).(GroundedShortTermStore); ok {
		return gs.GroundedShortTerm(ctx, opts)
	}
	return nil, errNoGroundedShortTerm
}

// ResetMemoryGroundedShortTerm demotes the tier via the store.
func ResetMemoryGroundedShortTerm(ctx context.Context, store Store, opts GroundedShortTermOptions) (GroundedShortTermResetResult, error) {
	if gs, ok := any(store).(GroundedShortTermStore); ok {
		return gs.ResetGroundedShortTerm(ctx, opts)
	}
	return GroundedShortTermResetResult{}, errNoGroundedShortTerm
}
