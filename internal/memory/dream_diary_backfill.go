package memory

// dream_diary_backfill.go — retroactive dream-diary synthesis (swarmstr-qc53.4).
//
// backfillDreamDiary REPLAYS the consolidation tier over EXISTING memories to
// synthesize dated diary entries for a trailing window (default 30 days). It is
// non-mutating with respect to the memory records themselves: it reads the
// promotion tier (recall_tracking ⋈ chunks), buckets activity by UTC calendar
// day, and writes one synthetic REM diary entry per day that had consolidation
// activity. Idempotency is guaranteed by the partial unique index on synthetic
// (scope, date, phase) rows: re-running backfill skips days already synthesized.
//
// Bucketing choice: a record buckets on the day it was consolidated
// (promoted_at) if promoted, else the day it was last recalled. "Considered" for
// a day is the set of records that were promotion-eligible (met the default
// thresholds) or already promoted that day; "promoted" is the subset carrying a
// promotion marker dated to that day.

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DefaultDreamDiaryBackfillDays is the trailing window backfill synthesizes.
const DefaultDreamDiaryBackfillDays = 30

// BackfillDreamDiaryOptions parameterizes retroactive diary synthesis.
type BackfillDreamDiaryOptions struct {
	Days  int       // trailing window in days (default 30, max 365)
	Scope string    // diary namespace label applied to synthesized entries
	Now   time.Time // clock override
}

// BackfillDreamDiaryResult reports a backfill run.
type BackfillDreamDiaryResult struct {
	Days             int      `json:"days"`
	DaysWithActivity int      `json:"days_with_activity"`
	EntriesCreated   int      `json:"entries_created"`
	EntriesSkipped   int      `json:"entries_skipped"`
	EntryIDs         []string `json:"entry_ids,omitempty"`
}

type backfillRecord struct {
	memoryID   string
	topic      string
	eligible   bool
	promoted   bool
	promotedAt int64
	lastRecall int64
	firstSeen  int64
}

// BackfillDreamDiary synthesizes retroactive diary entries for the trailing
// window. It runs under the store maintenance lock and is idempotent.
func (b *SQLiteBackend) BackfillDreamDiary(ctx context.Context, opts BackfillDreamDiaryOptions) (BackfillDreamDiaryResult, error) {
	if b == nil {
		return BackfillDreamDiaryResult{}, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureDreamDiarySchema(); err != nil {
		return BackfillDreamDiaryResult{}, err
	}
	days := opts.Days
	if days <= 0 {
		days = DefaultDreamDiaryBackfillDays
	}
	if days > 365 {
		days = 365
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	scope := strings.TrimSpace(opts.Scope)
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour).Unix()
	pcfg := DefaultPromotionConfig()

	result := BackfillDreamDiaryResult{Days: days}

	err := b.WithMaintenanceLock(func() error {
		byDay, dayErr := b.collectBackfillBuckets(cutoff, pcfg)
		if dayErr != nil {
			return dayErr
		}
		dayKeys := make([]string, 0, len(byDay))
		for day := range byDay {
			dayKeys = append(dayKeys, day)
		}
		sort.Strings(dayKeys) // chronological, deterministic
		for _, day := range dayKeys {
			records := byDay[day]
			considered := make([]string, 0, len(records))
			promoted := make([]string, 0, len(records))
			topics := map[string]int{}
			for _, rec := range records {
				if rec.eligible || rec.promoted {
					considered = append(considered, rec.memoryID)
					topic := strings.TrimSpace(rec.topic)
					if topic == "" {
						topic = "uncategorized"
					}
					topics[topic]++
				}
				if rec.promoted {
					promoted = append(promoted, rec.memoryID)
				}
			}
			if len(considered) == 0 {
				continue
			}
			result.DaysWithActivity++
			// Anchor the synthetic entry at noon UTC of the bucket day so its
			// created_at falls unambiguously inside the day.
			createdAt, _ := time.Parse(dreamDiaryDateLayout, day)
			createdAt = createdAt.Add(12 * time.Hour).UTC()
			entry := DreamDiaryEntry{
				CreatedAt:            createdAt,
				Date:                 day,
				Phase:                DreamingPhaseREM,
				Scope:                scope,
				CandidatesConsidered: len(considered),
				CandidateIDs:         considered,
				PromotedRecordIDs:    promoted,
				Counts:               map[string]int{"considered": len(considered), "promoted": len(promoted)},
				Narrative:            backfillNarrative(day, len(considered), len(promoted), topics),
				Synthetic:            true,
			}
			existed, exErr := b.getDreamDiaryEntryExistsLocked(scope, day, DreamingPhaseREM, true)
			if exErr != nil {
				return exErr
			}
			saved, appendErr := b.AppendDreamDiaryEntry(ctx, entry)
			if appendErr != nil {
				return appendErr
			}
			if existed {
				result.EntriesSkipped++
				continue
			}
			result.EntriesCreated++
			result.EntryIDs = append(result.EntryIDs, saved.ID)
		}
		return nil
	})
	if err != nil {
		return BackfillDreamDiaryResult{}, err
	}
	recordMemoryTelemetry("dream_diary", time.Time{}, map[string]any{"ok": true, "op": "backfill", "scope": scope, "days": days, "created": result.EntriesCreated, "skipped": result.EntriesSkipped})
	return result, nil
}

func (b *SQLiteBackend) collectBackfillBuckets(cutoff int64, pcfg PromotionConfig) (map[string][]backfillRecord, error) {
	b.mu.RLock()
	rows, err := b.db.Query(`
		SELECT rt.memory_id, rt.recall_count, rt.unique_queries, rt.avg_score,
		       rt.first_recall_unix, rt.last_recall_unix, rt.promoted_at, c.topic
		FROM recall_tracking rt
		JOIN chunks c ON c.id = rt.memory_id
		WHERE COALESCE(rt.promoted_at, rt.last_recall_unix, rt.first_recall_unix) >= ?
	`, cutoff)
	if err != nil {
		b.mu.RUnlock()
		return nil, err
	}
	defer func() {
		rows.Close()
		b.mu.RUnlock()
	}()

	byDay := map[string][]backfillRecord{}
	for rows.Next() {
		var (
			rec           backfillRecord
			recallCount   int
			uniqueQueries int
			avgScore      float64
			promotedAt    sql.NullInt64
			topic         sql.NullString
		)
		if err := rows.Scan(&rec.memoryID, &recallCount, &uniqueQueries, &avgScore,
			&rec.firstSeen, &rec.lastRecall, &promotedAt, &topic); err != nil {
			return nil, err
		}
		rec.topic = topic.String
		rec.promoted = promotedAt.Valid && promotedAt.Int64 > 0
		rec.promotedAt = promotedAt.Int64
		rec.eligible = recallCount >= pcfg.MinRecallCount &&
			uniqueQueries >= pcfg.MinUniqueQueries &&
			avgScore >= pcfg.MinScore
		bucketUnix := rec.lastRecall
		if rec.promoted {
			bucketUnix = rec.promotedAt
		} else if bucketUnix <= 0 {
			bucketUnix = rec.firstSeen
		}
		day := time.Unix(bucketUnix, 0).UTC().Format(dreamDiaryDateLayout)
		byDay[day] = append(byDay[day], rec)
	}
	return byDay, rows.Err()
}

func (b *SQLiteBackend) getDreamDiaryEntryExistsLocked(scope, date string, phase DreamingPhase, synthetic bool) (bool, error) {
	_, found, err := b.getDreamDiaryEntryByKey(scope, date, phase, synthetic)
	return found, err
}

func backfillNarrative(day string, considered, promoted int, topics map[string]int) string {
	pairs := make([]string, 0, len(topics))
	for topic, count := range topics {
		pairs = append(pairs, fmt.Sprintf("%s=%d", topic, count))
	}
	sort.Strings(pairs)
	text := fmt.Sprintf("Backfilled dreaming for %s reviewed %d candidates and promoted %d memories.", day, considered, promoted)
	if len(pairs) > 0 {
		text += " Topics: " + strings.Join(pairs, ", ") + "."
	}
	return truncateDreamingNarrative(text, 1200)
}

// BackfillDreamDiaryRunner is implemented by backends that can synthesize
// retroactive diary entries.
type BackfillDreamDiaryRunner interface {
	BackfillDreamDiary(ctx context.Context, opts BackfillDreamDiaryOptions) (BackfillDreamDiaryResult, error)
}

// BackfillMemoryDreamDiary synthesizes retroactive diary entries via the store.
func BackfillMemoryDreamDiary(ctx context.Context, store Store, opts BackfillDreamDiaryOptions) (BackfillDreamDiaryResult, error) {
	if r, ok := any(store).(BackfillDreamDiaryRunner); ok {
		return r.BackfillDreamDiary(ctx, opts)
	}
	return BackfillDreamDiaryResult{}, errNoDreamDiaryStore
}
