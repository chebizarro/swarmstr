package memory

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

type MemoryQueryExplanation struct {
	Query        string                    `json:"query"`
	SearchQuery  string                    `json:"search_query"`
	Mode         string                    `json:"mode"`
	Intent       QueryIntent               `json:"intent"`
	Filters      map[string]any            `json:"filters,omitempty"`
	Weights      MemoryRankingWeights      `json:"weights"`
	ResultCount  int                       `json:"result_count"`
	TokenCost    int                       `json:"token_cost,omitempty"`
	TokenBudget  int                       `json:"token_budget,omitempty"`
	Results      []MemoryCard              `json:"results"`
	Excluded     []MemoryQueryExclusion    `json:"excluded,omitempty"`
	CandidateSet MemoryCandidateSetSummary `json:"candidate_set"`
}

type MemoryCandidateSetSummary struct {
	FTSQuery         string `json:"fts_query,omitempty"`
	Candidates       int    `json:"candidates"`
	VectorCandidates int    `json:"vector_candidates,omitempty"`
	VectorEnabled    bool   `json:"vector_enabled,omitempty"`
	VectorFallback   bool   `json:"vector_fallback,omitempty"`
	Fallback         string `json:"fallback,omitempty"`
	CandidateLimit   int    `json:"candidate_limit"`
	ReturnedLimit    int    `json:"returned_limit"`
}

type MemoryQueryExclusion struct {
	ID      string   `json:"id"`
	Type    string   `json:"type,omitempty"`
	Scope   string   `json:"scope,omitempty"`
	Reasons []string `json:"reasons"`
}

type MemoryStatsReport struct {
	Backend       string         `json:"backend"`
	Path          string         `json:"path,omitempty"`
	TotalRecords  int            `json:"total_records"`
	ActiveRecords int            `json:"active_records"`
	Deleted       int            `json:"deleted"`
	Superseded    int            `json:"superseded"`
	Expired       int            `json:"expired"`
	Pinned        int            `json:"pinned"`
	Durable       int            `json:"durable"`
	ByType        map[string]int `json:"by_type"`
	ByScope       map[string]int `json:"by_scope"`
	Sessions      int            `json:"sessions"`
}

type MemoryHealthReport struct {
	Status            string                           `json:"status"`
	Backend           string                           `json:"backend"`
	CheckedAt         string                           `json:"checked_at"`
	RecordCount       int                              `json:"record_count"`
	HealthScore       float64                          `json:"health_score"`
	IssueCounts       map[string]int                   `json:"issue_counts"`
	IssueSamples      map[string][]string              `json:"issue_samples,omitempty"`
	RepairSuggestions map[string][]MemoryRepairCommand `json:"repair_suggestions,omitempty"`
	Warnings          []MemoryHealthWarning            `json:"warnings,omitempty"`
	Index             map[string]any                   `json:"index,omitempty"`
}

type MemoryRepairCommand struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	Safe        bool   `json:"safe,omitempty"`
}

type MemoryHealthWarning struct {
	Issue       string                `json:"issue"`
	Count       int                   `json:"count"`
	Samples     []string              `json:"samples,omitempty"`
	Suggestions []MemoryRepairCommand `json:"repair_commands,omitempty"`
}

type MemoryHealthRepairOptions struct {
	SafeOnly bool      `json:"safe_only,omitempty"`
	FixAll   bool      `json:"fix_all,omitempty"`
	Now      time.Time `json:"-"`
}

type MemoryHealthRepairAction struct {
	Issue   string `json:"issue"`
	Command string `json:"command"`
	Applied bool   `json:"applied"`
	Detail  string `json:"detail,omitempty"`
}

type MemoryHealthRepairReport struct {
	Before  MemoryHealthReport         `json:"before"`
	After   MemoryHealthReport         `json:"after"`
	Actions []MemoryHealthRepairAction `json:"actions"`
}

func (b *SQLiteBackend) ExplainMemoryQuery(ctx context.Context, q MemoryQuery) (MemoryQueryExplanation, error) {
	if err := b.ensureUnifiedSchema(); err != nil {
		return MemoryQueryExplanation{}, err
	}
	routed, intent := applyQueryIntentRouting(q)
	routed = normalizeMemoryQuery(routed)
	searchQuery := strings.TrimSpace(intent.SearchQuery)
	if searchQuery == "" {
		searchQuery = routed.Query
	}
	ftsQuery := buildFTSQuery(searchQuery)
	candidateLimit := memoryCandidateLimit(routed.Limit)
	records, ranks, fallback, err := b.fetchMemoryQueryCandidates(routed, ftsQuery, candidateLimit)
	if err != nil {
		return MemoryQueryExplanation{}, err
	}
	if len(records) == 0 && ftsQuery != "" {
		records, ranks, fallback, err = b.fetchMemoryLikeCandidates(routed, searchQuery, candidateLimit)
		if err != nil {
			return MemoryQueryExplanation{}, err
		}
	}
	b.recordVectorQueryActivityAndScheduleIdleReindex()
	vectorCfg, _, _, vectorEnabled := b.vectorRetrievalState()
	vectorStatsBefore := b.MemoryVectorStats()
	vectorLimit := int(float64(candidateLimit) * vectorCfg.CandidateMultiplier)
	if vectorLimit < candidateLimit {
		vectorLimit = candidateLimit
	}
	vectors, err := b.fetchMemoryVectorCandidates(ctx, routed, searchQuery, vectorLimit)
	if err != nil {
		vectors = nil
	}
	vectorStatsAfter := b.MemoryVectorStats()
	vectorFallback := vectorStatsAfter.Fallbacks > vectorStatsBefore.Fallbacks
	ranked := mergeBM25VectorCandidates(records, ranks, vectors, routed, intent, vectorCfg, time.Now().UTC())
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		return ranked[i].Record.UpdatedAt.After(ranked[j].Record.UpdatedAt)
	})
	cards := make([]MemoryCard, 0, minInt(len(ranked), routed.Limit))
	for i, item := range ranked {
		if i >= routed.Limit {
			break
		}
		card := MemoryCardFromRecord(item.Record, item.Score, routed.IncludeSources)
		if routed.IncludeDebug {
			why := item.Why
			card.Why = &why
		}
		cards = append(cards, card)
	}
	tokenCost := EstimateMemoryCardsTokenCost(cards)
	if routed.TokenBudget > 0 {
		cards, tokenCost = trimCardsToTokenBudget(cards, routed.TokenBudget)
	}
	explain := MemoryQueryExplanation{
		Query:       routed.Query,
		SearchQuery: searchQuery,
		Mode:        routed.Mode,
		Intent:      intent,
		Filters: map[string]any{
			"scope": routed.Scopes,
			"types": routed.Types,
			"tags":  routed.Tags,
		},
		Weights:     normalizeRankingWeights(routed.RankingWeights),
		ResultCount: len(cards),
		TokenCost:   tokenCost,
		TokenBudget: routed.TokenBudget,
		Results:     cards,
		CandidateSet: MemoryCandidateSetSummary{
			FTSQuery:         ftsQuery,
			Candidates:       len(records),
			VectorCandidates: len(vectors),
			VectorEnabled:    vectorEnabled,
			VectorFallback:   vectorFallback,
			Fallback:         fallback,
			CandidateLimit:   candidateLimit,
			ReturnedLimit:    routed.Limit,
		},
	}
	if routed.IncludeDebug {
		explain.Excluded = b.explainMemoryExclusions(routed, ftsQuery, candidateLimit)
	}
	return explain, nil
}

func (b *SQLiteBackend) MemoryStats(ctx context.Context) (MemoryStatsReport, error) {
	_ = ctx
	if err := b.ensureUnifiedSchema(); err != nil {
		return MemoryStatsReport{}, err
	}
	now := time.Now().UTC().Unix()
	report := MemoryStatsReport{Backend: "sqlite", Path: b.path, ByType: map[string]int{}, ByScope: map[string]int{}}
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_records`).Scan(&report.TotalRecords)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_records WHERE deleted_at IS NULL AND (superseded_by IS NULL OR superseded_by = '') AND (valid_until IS NULL OR valid_until = 0 OR valid_until > ?)`, now).Scan(&report.ActiveRecords)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_records WHERE deleted_at IS NOT NULL AND deleted_at > 0`).Scan(&report.Deleted)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_records WHERE superseded_by IS NOT NULL AND superseded_by != ''`).Scan(&report.Superseded)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_records WHERE valid_until IS NOT NULL AND valid_until > 0 AND valid_until <= ?`, now).Scan(&report.Expired)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_records WHERE pinned != 0`).Scan(&report.Pinned)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_records WHERE pinned != 0 OR COALESCE(source_file_path, '') != '' OR metadata LIKE '%"durable":true%'`).Scan(&report.Durable)
	_ = b.db.QueryRow(`SELECT COUNT(DISTINCT source_session_id) FROM memory_records WHERE COALESCE(source_session_id, '') != ''`).Scan(&report.Sessions)
	fillGroupCounts(b.db, `SELECT type, COUNT(*) FROM memory_records GROUP BY type`, report.ByType)
	fillGroupCounts(b.db, `SELECT scope, COUNT(*) FROM memory_records GROUP BY scope`, report.ByScope)
	return report, nil
}

func (b *SQLiteBackend) MemoryHealth(ctx context.Context) (MemoryHealthReport, error) {
	_ = ctx
	if err := b.ensureUnifiedSchema(); err != nil {
		return MemoryHealthReport{}, err
	}
	now := time.Now().UTC().Unix()
	report := MemoryHealthReport{Status: "ok", Backend: "sqlite", CheckedAt: time.Now().UTC().Format(time.RFC3339), IssueCounts: map[string]int{}, IssueSamples: map[string][]string{}, Index: map[string]any{}}
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_records`).Scan(&report.RecordCount)
	var ftsCount int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_fts`).Scan(&ftsCount)
	report.Index["memory_records"] = report.RecordCount
	report.Index["fts_records"] = ftsCount
	if ftsCount != report.RecordCount {
		report.IssueCounts["index_drift"] = absInt(ftsCount - report.RecordCount)
		report.IssueSamples["index_drift"] = []string{fmt.Sprintf("memory_records=%d memory_fts=%d", report.RecordCount, ftsCount)}
	}
	countIssue(b.db, `SELECT COUNT(*) FROM memory_records WHERE deleted_at IS NULL AND (valid_until IS NOT NULL AND valid_until > 0 AND valid_until <= ?)`, `SELECT id FROM memory_records WHERE deleted_at IS NULL AND (valid_until IS NOT NULL AND valid_until > 0 AND valid_until <= ?) LIMIT 5`, []any{now}, &report, "expired_active")
	countIssue(b.db, `SELECT COUNT(*) FROM memory_records r LEFT JOIN memory_records target ON target.id = r.superseded_by WHERE COALESCE(r.superseded_by, '') != '' AND target.id IS NULL`, `SELECT r.id FROM memory_records r LEFT JOIN memory_records target ON target.id = r.superseded_by WHERE COALESCE(r.superseded_by, '') != '' AND target.id IS NULL LIMIT 5`, nil, &report, "missing_supersession_target")
	countIssue(b.db, `SELECT COUNT(*) FROM (SELECT hash FROM memory_records WHERE pinned = 0 AND deleted_at IS NULL AND COALESCE(superseded_by, '') = '' GROUP BY hash HAVING COUNT(*) > 1)`, `SELECT hash FROM memory_records WHERE pinned = 0 AND deleted_at IS NULL AND COALESCE(superseded_by, '') = '' GROUP BY hash HAVING COUNT(*) > 1 LIMIT 5`, nil, &report, "duplicate_hash")
	countIssue(b.db, `SELECT COUNT(*) FROM memory_records WHERE metadata LIKE '%"stale":true%'`, `SELECT id FROM memory_records WHERE metadata LIKE '%"stale":true%' LIMIT 5`, nil, &report, "stale_flagged")
	countIssue(b.db, `SELECT COUNT(*) FROM memory_records r JOIN memory_records t ON t.id = r.superseded_by WHERE COALESCE(r.superseded_by, '') != '' AND t.superseded_by = r.id`, `SELECT r.id FROM memory_records r JOIN memory_records t ON t.id = r.superseded_by WHERE COALESCE(r.superseded_by, '') != '' AND t.superseded_by = r.id LIMIT 5`, nil, &report, "supersession_cycle")
	countIssue(b.db, `SELECT COUNT(*) FROM memory_records newer JOIN memory_records older ON instr(COALESCE(newer.supersedes,''), older.id) > 0 WHERE newer.deleted_at IS NULL AND COALESCE(newer.superseded_by,'') = '' AND older.deleted_at IS NULL AND COALESCE(older.superseded_by,'') = ''`, `SELECT older.id FROM memory_records newer JOIN memory_records older ON instr(COALESCE(newer.supersedes,''), older.id) > 0 WHERE newer.deleted_at IS NULL AND COALESCE(newer.superseded_by,'') = '' AND older.deleted_at IS NULL AND COALESCE(older.superseded_by,'') = '' LIMIT 5`, nil, &report, "supersession_conflict_active")
	countIssue(b.db, `SELECT COUNT(*) FROM memory_records WHERE deleted_at IS NULL AND COALESCE(superseded_by, '') = '' AND metadata LIKE '%"nostr_conflict":true%'`, `SELECT id FROM memory_records WHERE deleted_at IS NULL AND COALESCE(superseded_by, '') = '' AND metadata LIKE '%"nostr_conflict":true%' LIMIT 5`, nil, &report, "nostr_conflict_active")
	countIssue(b.db, `SELECT COUNT(*) FROM memory_records WHERE metadata LIKE '%"clock_drift_warning":true%'`, `SELECT id FROM memory_records WHERE metadata LIKE '%"clock_drift_warning":true%' LIMIT 5`, nil, &report, "nostr_clock_drift_warning")

	report.Index["duplicate_clusters"] = report.IssueCounts["duplicate_hash"]
	report.Index["stale_records"] = report.IssueCounts["stale_flagged"]
	report.Index["nostr_conflict_active"] = report.IssueCounts["nostr_conflict_active"]
	report.Index["crdt_resolution"] = "deferred_phase_3"
	if outbox, err := b.MemoryOutboxStats(ctx); err == nil {
		report.Index["outbox_depth"] = outbox.OutboxDepth
		report.Index["publish_failures"] = outbox.PublishFailures
		report.Index["retry_counts"] = outbox.RetryCounts
		report.Index["oldest_pending"] = outbox.OldestPending
		if outbox.OutboxDepth > 100 {
			report.IssueCounts["outbox_depth_high"] = outbox.OutboxDepth
			report.IssueSamples["outbox_depth_high"] = []string{fmt.Sprintf("outbox_depth=%d exceeds warning threshold 100", outbox.OutboxDepth)}
		}
	}
	if tableExists(b.db, "recall_tracking") {
		var tracked, promoted int
		_ = b.db.QueryRow(`SELECT COUNT(*) FROM recall_tracking`).Scan(&tracked)
		_ = b.db.QueryRow(`SELECT COUNT(*) FROM recall_tracking WHERE promoted_at IS NOT NULL`).Scan(&promoted)
		report.Index["promotion_tracked"] = tracked
		report.Index["promotion_promoted"] = promoted
	}
	if tableExists(b.db, "memory_eval_runs") {
		var evalRuns int
		_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_eval_runs`).Scan(&evalRuns)
		report.Index["query_eval_runs"] = evalRuns
	}
	vectorCfg, _, _, vectorEnabled := b.vectorRetrievalState()
	vectorStats := b.MemoryVectorStats()
	report.Index["vector_enabled"] = vectorEnabled
	if vectorEnabled {
		normalized := normalizeMemoryVectorRetrievalConfig(vectorCfg)
		report.Index["vector_rrf_k"] = normalized.RRFK
		report.Index["vector_queries"] = vectorStats.Queries
		report.Index["vector_candidates"] = vectorStats.Candidates
		report.Index["vector_fallbacks"] = vectorStats.Fallbacks
		report.Index["vector_reindexed"] = vectorStats.Reindexed
		report.Index["vector_version_skips"] = vectorStats.VersionSkips
		report.Index["reindex_batch_size"] = normalized.ReindexBatchSize
		report.Index["reindex_daily_limit"] = normalized.ReindexDailyLimit
	}
	countIssue(b.db, `SELECT COUNT(*) FROM memory_records r LEFT JOIN memory_embeddings e ON e.record_id = r.id WHERE r.deleted_at IS NULL AND e.record_id IS NULL`, `SELECT r.id FROM memory_records r LEFT JOIN memory_embeddings e ON e.record_id = r.id WHERE r.deleted_at IS NULL AND e.record_id IS NULL LIMIT 5`, nil, &report, "reindex_backlog")
	if report.IssueCounts["reindex_backlog"] > 10000 {
		report.Status = "warn"
		report.IssueSamples["reindex_backlog"] = append(report.IssueSamples["reindex_backlog"], "Backlog > 10000. Suggested manual action: memory_reindex --batch")
	}
	tokenTelemetry := MemoryTokenTelemetrySnapshot()
	report.Index["token_cost_p50"] = tokenTelemetry.TokenCostP50
	report.Index["token_cost_p95"] = tokenTelemetry.TokenCostP95
	report.Index["token_cost_p99"] = tokenTelemetry.TokenCostP99
	report.Index["session_token_budget_exceeded_count"] = tokenTelemetry.SessionTokenBudgetExceededCount
	finalizeMemoryHealthReport(&report)
	b.recordHealthScore(report)
	return report, nil
}

func MemoryStats(ctx context.Context, store Store) (MemoryStatsReport, error) {
	if typed, ok := any(store).(interface {
		MemoryStats(context.Context) (MemoryStatsReport, error)
	}); ok {
		return typed.MemoryStats(ctx)
	}
	if store == nil {
		return MemoryStatsReport{}, fmt.Errorf("memory store is nil")
	}
	return MemoryStatsReport{Backend: "legacy", TotalRecords: store.Count(), ActiveRecords: store.Count(), Sessions: store.SessionCount(), ByType: map[string]int{}, ByScope: map[string]int{}}, nil
}

func MemoryHealth(ctx context.Context, store Store) (MemoryHealthReport, error) {
	if typed, ok := any(store).(interface {
		MemoryHealth(context.Context) (MemoryHealthReport, error)
	}); ok {
		return typed.MemoryHealth(ctx)
	}
	if store == nil {
		return MemoryHealthReport{}, fmt.Errorf("memory store is nil")
	}
	status := "ok"
	report := MemoryHealthReport{Status: status, Backend: "legacy", CheckedAt: time.Now().UTC().Format(time.RFC3339), RecordCount: store.Count(), HealthScore: 1.0, IssueCounts: map[string]int{}, IssueSamples: map[string][]string{}, RepairSuggestions: map[string][]MemoryRepairCommand{}}
	finalizeMemoryHealthReport(&report)
	return report, nil
}

func RepairMemoryHealth(ctx context.Context, store Store, opts MemoryHealthRepairOptions) (MemoryHealthRepairReport, error) {
	if typed, ok := any(store).(interface {
		RepairMemoryHealth(context.Context, MemoryHealthRepairOptions) (MemoryHealthRepairReport, error)
	}); ok {
		return typed.RepairMemoryHealth(ctx, opts)
	}
	return MemoryHealthRepairReport{}, fmt.Errorf("memory health repairs are not supported by this backend")
}

func (b *SQLiteBackend) RepairMemoryHealth(ctx context.Context, opts MemoryHealthRepairOptions) (MemoryHealthRepairReport, error) {
	if err := b.ensureUnifiedSchema(); err != nil {
		return MemoryHealthRepairReport{}, err
	}
	before, err := b.MemoryHealth(ctx)
	if err != nil {
		return MemoryHealthRepairReport{}, err
	}
	actions := []MemoryHealthRepairAction{}
	start := time.Now()
	if before.IssueCounts["index_drift"] > 0 {
		_, err := b.db.Exec(`INSERT INTO memory_fts(memory_fts) VALUES('rebuild')`)
		actions = append(actions, MemoryHealthRepairAction{Issue: "index_drift", Command: "metiq memory health --fix-safe", Applied: err == nil, Detail: errString(err)})
	}
	needsSafeCompaction := before.IssueCounts["expired_active"] > 0 || before.IssueCounts["missing_supersession_target"] > 0 || before.IssueCounts["supersession_cycle"] > 0 || before.IssueCounts["duplicate_hash"] > 0
	needsFullCompaction := before.IssueCounts["stale_flagged"] > 0 || before.IssueCounts["supersession_conflict_active"] > 0
	if needsSafeCompaction || (opts.FixAll && needsFullCompaction) {
		now := opts.Now
		if now.IsZero() {
			now = time.Now().UTC()
		}
		cfg := CompactionConfig{Now: now, Reason: "health_repair", SkipExpireStale: !opts.FixAll && before.IssueCounts["expired_active"] == 0, SkipDedupe: before.IssueCounts["duplicate_hash"] == 0}
		result, err := b.CompactMemoryRecords(ctx, cfg)
		detail := fmt.Sprintf("expired=%d deduped=%d supersession_fix=%d stale=%d", result.Expired, result.Deduped, result.SupersessionFix, result.StaleFlagged)
		if err != nil {
			detail = err.Error()
		}
		actions = append(actions, MemoryHealthRepairAction{Issue: "record_lifecycle", Command: "metiq memory compact --dedupe --expire-stale", Applied: err == nil, Detail: detail})
	}
	after, err := b.MemoryHealth(ctx)
	if err != nil {
		return MemoryHealthRepairReport{}, err
	}
	recordMemoryTelemetry("health_repair", start, map[string]any{"ok": true, "safe_only": opts.SafeOnly, "fix_all": opts.FixAll, "actions": len(actions), "health_score_before": before.HealthScore, "health_score_after": after.HealthScore})
	return MemoryHealthRepairReport{Before: before, After: after, Actions: actions}, nil
}

func ExplainMemoryQuery(ctx context.Context, store Store, q MemoryQuery) (MemoryQueryExplanation, error) {
	if typed, ok := any(store).(interface {
		ExplainMemoryQuery(context.Context, MemoryQuery) (MemoryQueryExplanation, error)
	}); ok {
		return typed.ExplainMemoryQuery(ctx, q)
	}
	q.IncludeDebug = true
	q, intent := applyQueryIntentRouting(q)
	cards := queryLegacyStore(ctx, store, q)
	return MemoryQueryExplanation{Query: q.Query, SearchQuery: intent.SearchQuery, Mode: normalizeMemoryQuery(q).Mode, Intent: intent, Weights: DefaultMemoryRankingWeights(), ResultCount: len(cards), Results: cards, CandidateSet: MemoryCandidateSetSummary{Candidates: len(cards), ReturnedLimit: q.Limit}}, nil
}

func (b *SQLiteBackend) fetchMemoryQueryCandidates(q MemoryQuery, ftsQuery string, limit int) ([]MemoryRecord, []float64, string, error) {
	args := []any{}
	where := unifiedMetadataWhere(q, &args)
	if ftsQuery != "" && q.Mode != "recent" {
		args = append([]any{ftsQuery}, args...)
		args = append(args, limit)
		rows, err := b.db.Query(memoryRecordSelectSQL("bm25(memory_fts)")+`
			FROM memory_fts fts JOIN memory_records r ON r.id = fts.id
			WHERE memory_fts MATCH ? `+where+`
			ORDER BY rank, r.pinned DESC, r.updated_at DESC
			LIMIT ?`, args...)
		if err != nil {
			return nil, nil, "", err
		}
		defer rows.Close()
		records, ranks := b.scanMemoryRecordRows(rows)
		return records, ranks, "", nil
	}
	args = append(args, limit)
	rows, err := b.db.Query(memoryRecordSelectSQL("0.0")+`
		FROM memory_records r
		WHERE 1=1 `+where+`
		ORDER BY r.pinned DESC, r.updated_at DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, nil, "", err
	}
	defer rows.Close()
	records, ranks := b.scanMemoryRecordRows(rows)
	return records, ranks, "recent_or_unscored", nil
}

func (b *SQLiteBackend) fetchMemoryLikeCandidates(q MemoryQuery, searchQuery string, limit int) ([]MemoryRecord, []float64, string, error) {
	args := []any{}
	where := unifiedMetadataWhere(q, &args)
	for _, token := range tokenizeFTSQuery(searchQuery) {
		where += " AND (r.text LIKE ? OR r.summary LIKE ? OR r.subject LIKE ? OR r.tags LIKE ? OR r.keywords LIKE ?)"
		pattern := "%" + token + "%"
		args = append(args, pattern, pattern, pattern, pattern, pattern)
	}
	args = append(args, limit)
	rows, err := b.db.Query(memoryRecordSelectSQL("0.0")+`
		FROM memory_records r
		WHERE 1=1 `+where+`
		ORDER BY r.pinned DESC, r.updated_at DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, nil, "", err
	}
	defer rows.Close()
	records, ranks := b.scanMemoryRecordRows(rows)
	return records, ranks, "like", nil
}

func (b *SQLiteBackend) explainMemoryExclusions(q MemoryQuery, ftsQuery string, limit int) []MemoryQueryExclusion {
	if ftsQuery == "" || q.Mode == "recent" {
		return nil
	}
	rows, err := b.db.Query(memoryRecordSelectSQL("bm25(memory_fts)")+`
		FROM memory_fts fts JOIN memory_records r ON r.id = fts.id
		WHERE memory_fts MATCH ?
		ORDER BY rank, r.updated_at DESC
		LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	records, _ := b.scanMemoryRecordRows(rows)
	out := []MemoryQueryExclusion{}
	now := time.Now().UTC().Unix()
	for _, rec := range records {
		reasons := excludedReasons(rec, q, now)
		if len(reasons) > 0 {
			out = append(out, MemoryQueryExclusion{ID: rec.ID, Type: rec.Type, Scope: rec.Scope, Reasons: reasons})
		}
		if len(out) >= 20 {
			break
		}
	}
	return out
}

func excludedReasons(rec MemoryRecord, q MemoryQuery, now int64) []string {
	var reasons []string
	if q.Mode != "audit" {
		if rec.DeletedAt != nil {
			reasons = append(reasons, "deleted")
		}
		if rec.SupersededBy != "" {
			reasons = append(reasons, "superseded")
		}
		if rec.ValidUntil != nil && rec.ValidUntil.Unix() <= now {
			reasons = append(reasons, "expired")
		}
	}
	if len(q.Scopes) > 0 && !matchesFilter(rec.Scope, q.Scopes) {
		reasons = append(reasons, "scope_not_allowed")
	}
	if len(q.Types) > 0 && !matchesFilter(rec.Type, q.Types) {
		reasons = append(reasons, "type_not_allowed")
	}
	if len(q.Tags) > 0 && !matchesTags(rec, q.Tags) {
		reasons = append(reasons, "missing_required_tag")
	}
	if q.SessionID != "" && (rec.Scope == MemoryRecordScopeSession || rec.Scope == MemoryRecordScopeLocal) && rec.Source.SessionID != q.SessionID {
		reasons = append(reasons, "session_not_allowed")
	}
	return reasons
}

func memoryRecordSelectSQL(rankExpr string) string {
	if strings.TrimSpace(rankExpr) == "" {
		rankExpr = "0.0"
	}
	return `
		SELECT r.id, r.type, r.scope, r.subject, r.text, r.summary, r.keywords, r.tags,
		       r.confidence, r.salience, r.source_kind, r.source_ref, r.source_session_id,
		       r.source_event_id, r.source_file_path, r.source_nostr_event_id,
		       r.created_at, r.updated_at, r.valid_from, r.valid_until, r.pinned,
		       r.supersedes, r.superseded_by, r.deleted_at, r.embedding_model,
		r.embedding_version, r.metadata, ` + rankExpr + ` AS rank`
}

func memoryCandidateLimit(limit int) int {
	if limit <= 0 {
		limit = 8
	}
	out := limit * 5
	if out < limit+20 {
		out = limit + 20
	}
	if out > 200 {
		out = 200
	}
	return out
}

func fillGroupCounts(db *sql.DB, query string, out map[string]int) {
	rows, err := db.Query(query)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var n int
		if rows.Scan(&key, &n) == nil && strings.TrimSpace(key) != "" {
			out[key] = n
		}
	}
}

func countIssue(db *sql.DB, countQuery string, sampleQuery string, args []any, report *MemoryHealthReport, key string) {
	var count int
	if err := db.QueryRow(countQuery, args...).Scan(&count); err != nil || count == 0 {
		return
	}
	report.IssueCounts[key] = count
	rows, err := db.Query(sampleQuery, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	samples := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && id != "" {
			samples = append(samples, id)
		}
	}
	if len(samples) > 0 {
		report.IssueSamples[key] = samples
	}
}

func finalizeMemoryHealthReport(report *MemoryHealthReport) {
	if report == nil {
		return
	}
	if report.IssueCounts == nil {
		report.IssueCounts = map[string]int{}
	}
	if report.IssueSamples == nil {
		report.IssueSamples = map[string][]string{}
	}
	report.RepairSuggestions = memoryRepairSuggestions(report.IssueCounts)
	report.Warnings = nil
	issueTypes := 0
	totalIssues := 0
	keys := make([]string, 0, len(report.IssueCounts))
	for key, count := range report.IssueCounts {
		if count <= 0 {
			continue
		}
		issueTypes++
		totalIssues += count
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		report.Warnings = append(report.Warnings, MemoryHealthWarning{Issue: key, Count: report.IssueCounts[key], Samples: report.IssueSamples[key], Suggestions: report.RepairSuggestions[key]})
	}
	if totalIssues > 0 {
		report.Status = "warn"
	} else if strings.TrimSpace(report.Status) == "" {
		report.Status = "ok"
	}
	report.HealthScore = memoryHealthScore(report.RecordCount, totalIssues, issueTypes)
}

func memoryRepairSuggestions(issueCounts map[string]int) map[string][]MemoryRepairCommand {
	out := map[string][]MemoryRepairCommand{}
	add := func(issue, command, description string, safe bool) {
		if issueCounts[issue] <= 0 {
			return
		}
		out[issue] = append(out[issue], MemoryRepairCommand{Command: command, Description: description, Safe: safe})
	}
	add("missing_supersession_target", "metiq memory repair --supersession", "repair dangling supersession links", true)
	add("supersession_cycle", "metiq memory repair --supersession", "break supersession cycles", true)
	add("supersession_conflict_active", "metiq memory repair --supersession", "resolve active supersession conflicts", false)
	add("duplicate_hash", "metiq memory compact --dedupe", "deduplicate exact duplicate records", true)
	add("expired_active", "metiq memory compact --expire-stale", "expire stale active records", true)
	add("stale_flagged", "metiq memory compact --expire-stale", "review and expire stale records", false)
	add("index_drift", "metiq memory sync --files", "rebuild or resync file-backed memory indexes", true)
	add("outbox_depth_high", "metiq memory sync --force-republish", "retry pending memory sync publishes", false)
	add("nostr_conflict_active", "metiq memory repair --supersession", "review Nostr conflict records", false)
	return out
}

func memoryHealthScore(recordCount, totalIssues, issueTypes int) float64 {
	if totalIssues <= 0 && issueTypes <= 0 {
		return 1.0
	}
	denom := recordCount
	if denom < 10 {
		denom = 10
	}
	penalty := float64(totalIssues)/float64(denom)*0.6 + float64(issueTypes)*0.07
	if penalty > 1 {
		penalty = 1
	}
	score := 1 - penalty
	if score < 0 {
		return 0
	}
	return score
}

func (b *SQLiteBackend) recordHealthScore(report MemoryHealthReport) {
	if b == nil || b.db == nil {
		return
	}
	checkedAt, err := time.Parse(time.RFC3339, report.CheckedAt)
	if err != nil {
		checkedAt = time.Now().UTC()
	}
	totalIssues := 0
	for _, count := range report.IssueCounts {
		if count > 0 {
			totalIssues += count
		}
	}
	_, _ = b.db.Exec(`INSERT OR REPLACE INTO memory_health_scores(checked_at, health_score, status, issue_count) VALUES (?, ?, ?, ?)`, checkedAt.Unix(), report.HealthScore, report.Status, totalIssues)
	recordMemoryTelemetry("health", time.Time{}, map[string]any{"status": report.Status, "health_score": report.HealthScore, "issue_count": totalIssues})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func tableExists(db *sql.DB, table string) bool {
	if db == nil || strings.TrimSpace(table) == "" {
		return false
	}
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	return err == nil && name == table
}
