package memory

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newUnifiedTestSQLiteBackend(t *testing.T) *SQLiteBackend {
	t.Helper()
	b, err := OpenSQLiteBackend(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func TestMemoryRecordValidationAndSQLiteQueryFilters(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	mustWriteRecord(t, b, MemoryRecord{ID: "pref-1", Type: MemoryRecordTypePreference, Scope: MemoryRecordScopeUser, Subject: "editor", Text: "User prefers concise responses and Vim for quick edits.", Tags: []string{"editor"}, Confidence: 0.9, Salience: 0.91, Source: MemorySource{Kind: MemorySourceKindManual}})
	mustWriteRecord(t, b, MemoryRecord{ID: "decision-old", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deployment", Text: "Old deployment decision.", SupersededBy: "decision-new", Confidence: 0.9, Salience: 0.9})
	mustWriteRecord(t, b, MemoryRecord{ID: "decision-new", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deployment", Text: "Production deployments require canaries before global rollout.", Tags: []string{"deployment", "canary"}, Confidence: 0.95, Salience: 0.95})

	cards, err := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "deployment canary", Scopes: []string{MemoryRecordScopeProject}, Types: []string{MemoryRecordTypeDecision}, Limit: 8, IncludeSources: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].ID != "decision-new" {
		t.Fatalf("expected only active decision-new, got %#v", cards)
	}

	audit, err := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "deployment decision", Mode: "audit", Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if !hasCard(audit, "decision-old") {
		t.Fatalf("audit mode should include superseded record, got %#v", audit)
	}
}

func TestMemoryWriteCompatibilityOldTopicMapping(t *testing.T) {
	b := newUnifiedTestSQLiteBackend(t)
	confidence := 0.92
	rec := MemoryRecord{Type: "user", Scope: MemoryRecordScopeUser, Subject: "style", Text: "User prefers brief direct answers.", Confidence: confidence, Salience: 0.95, Source: MemorySource{Kind: MemorySourceKindManual}}
	if err := WriteMemoryRecord(context.Background(), b, rec); err != nil {
		t.Fatal(err)
	}
	cards, err := QueryMemoryRecords(context.Background(), b, MemoryQuery{Query: "brief answers", Types: []string{MemoryRecordTypePreference}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) == 0 || cards[0].Type != MemoryRecordTypePreference {
		t.Fatalf("expected preference from old user topic mapping, got %#v", cards)
	}
}

func TestSalienceClassifierPromotesAndRejects(t *testing.T) {
	pref := ClassifyMemorySalience("I prefer that you keep answers concise and avoid filler.", "user", nil)
	if !pref.Promote || pref.ProposedType != MemoryRecordTypePreference || pref.Score < SalienceSearchableThreshold {
		t.Fatalf("preference should promote, got %#v", pref)
	}
	decision := ClassifyMemorySalience("We decided to require canary deployments for production.", "user", nil)
	if !decision.Promote || decision.ProposedType != MemoryRecordTypeDecision {
		t.Fatalf("decision should promote, got %#v", decision)
	}
	chatter := ClassifyMemorySalience("thanks", "user", nil)
	if chatter.Promote || chatter.Score >= SalienceDiscardThreshold {
		t.Fatalf("chatter should be rejected, got %#v", chatter)
	}
}

func TestDurableMarkdownRoundTripAndIndexGeneration(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	root := filepath.Join(t.TempDir(), ".metiq", "agent-memory")
	rec := MemoryRecord{ID: "deploy-memory", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deployment", Text: "Production deploys require canaries.", Summary: "Prod deploys require canaries.", Tags: []string{"deployment", "canary"}, Confidence: 0.92, Salience: 0.95, Source: MemorySource{Kind: MemorySourceKindManual}}
	path, err := WriteDurableMemoryFile(root, rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, FileMemoryEntrypointName)); err != nil {
		t.Fatalf("MEMORY.md not generated: %v", err)
	}
	count, err := IngestDurableMemoryFiles(ctx, b, root)
	if err != nil || count != 1 {
		t.Fatalf("ingest count=%d err=%v", count, err)
	}
	cards, err := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "canaries production", Types: []string{MemoryRecordTypeDecision}, Limit: 5})
	if err != nil || len(cards) == 0 || cards[0].ID != "deploy-memory" {
		t.Fatalf("expected ingested markdown memory, cards=%#v err=%v", cards, err)
	}
}

func TestSessionSummaryIsSearchable(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	workspace := t.TempDir()
	dir := filepath.Join(workspace, sessionMemoryDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sess-123.md"), []byte("# Session Title\nModel context window investigation\n\n# Learnings\nGemma supports about 131072 tokens."), 0o600); err != nil {
		t.Fatal(err)
	}
	count, err := IngestSessionMemoryFiles(ctx, b, workspace, "sess-123")
	if err != nil || count != 1 {
		t.Fatalf("ingest session count=%d err=%v", count, err)
	}
	cards, err := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "Gemma context window", Types: []string{MemoryRecordTypeSummary}, Limit: 5})
	if err != nil || len(cards) == 0 {
		t.Fatalf("expected session summary search hit, cards=%#v err=%v", cards, err)
	}
}

func TestForgetAndAuditMode(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	mustWriteRecord(t, b, MemoryRecord{ID: "forget-me", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "obsolete", Text: "Obsolete provider config fact.", Confidence: 0.7, Salience: 0.8})
	ok, err := b.ForgetMemoryRecord(ctx, "forget-me", "soft_delete")
	if err != nil || !ok {
		t.Fatalf("forget ok=%v err=%v", ok, err)
	}
	normal, _ := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "obsolete provider", Limit: 5})
	if hasCard(normal, "forget-me") {
		t.Fatalf("normal query included deleted record: %#v", normal)
	}
	audit, _ := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "obsolete provider", Mode: "audit", Limit: 5})
	if !hasCard(audit, "forget-me") {
		t.Fatalf("audit query did not include deleted record: %#v", audit)
	}
}

func TestSQLiteWALConcurrentReadWrite(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	var mode string
	if err := b.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode=%q, want wal", mode)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := StableMemoryRecordID("concurrent", string(rune('a'+i)))
			_ = b.WriteMemoryRecord(ctx, MemoryRecord{ID: id, Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeLocal, Subject: "concurrency", Text: "Concurrent SQLite WAL memory write about readers and writers.", Confidence: 0.7, Salience: 0.8})
			_, _ = b.QueryMemoryRecords(ctx, MemoryQuery{Query: "concurrent readers writers", Limit: 5})
		}(i)
	}
	wg.Wait()
}

func TestCompactionExpiresEpisodesAndDedupes(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	old := time.Now().Add(-45 * 24 * time.Hour)
	mustWriteRecord(t, b, MemoryRecord{ID: "old-episode", Type: MemoryRecordTypeEpisode, Scope: MemoryRecordScopeSession, Subject: "old", Text: "Old unpinned episode", Confidence: 0.5, Salience: 0.45, CreatedAt: old, UpdatedAt: old})
	mustWriteRecord(t, b, MemoryRecord{ID: "dup-a", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "dup", Text: "Duplicate deploy lesson", Confidence: 0.7, Salience: 0.8})
	mustWriteRecord(t, b, MemoryRecord{ID: "dup-b", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "dup", Text: "Duplicate deploy lesson", Confidence: 0.7, Salience: 0.8})
	result, err := b.CompactMemoryRecords(ctx, CompactionConfig{EpisodeTTLDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if result.Expired == 0 || result.Deduped == 0 {
		t.Fatalf("expected expiry and dedupe, got %#v", result)
	}
}

func TestMigrateJSONBackfillsUnifiedRecords(t *testing.T) {
	ctx := context.Background()
	jsonPath := filepath.Join(t.TempDir(), "memory-index.json")
	idx, err := OpenIndex(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	idx.Add(MemoryRecord{ID: "legacy-json", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "legacy", Text: "Legacy JSON memory mentions SQLite migration.", Confidence: 0.8, Salience: 0.8}.ToDoc())
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	b := newUnifiedTestSQLiteBackend(t)
	if err := b.MigrateFromJSONIndex(jsonPath); err != nil {
		t.Fatal(err)
	}
	cards, err := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "SQLite migration", Limit: 5})
	if err != nil || !hasCard(cards, "legacy-json") {
		t.Fatalf("expected migrated JSON record in unified query, cards=%#v err=%v", cards, err)
	}
}

func TestMemoryEvalHarness(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	mustWriteRecord(t, b, MemoryRecord{ID: "eval-deploy", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deployment", Text: "Deployment decisions require canary rollout.", Confidence: 0.9, Salience: 0.9})
	run := RunMemoryEvals(ctx, b, []MemoryEvalCase{{ID: "case-1", Query: "canary rollout", ExpectedIDs: []string{"eval-deploy"}, Scope: MemoryRecordScopeProject}})
	if run.CaseCount != 1 || run.RecallAt10 != 1 || run.P50LatencyMS < 0 {
		t.Fatalf("unexpected eval run: %#v", run)
	}
	if len(DefaultSyntheticMemoryEvalCases()) < 50 {
		t.Fatal("expected at least 50 synthetic eval cases")
	}
	if run.TokenCost <= 0 {
		t.Fatalf("expected token cost metric, got %#v", run)
	}
}

func TestCompactionV2PreservesPinnedAndDurableDuplicates(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	text := "Keep canary rollout for prod deploys"
	mustWriteRecord(t, b, MemoryRecord{ID: "dup-pinned", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: text, Pinned: true})
	mustWriteRecord(t, b, MemoryRecord{ID: "dup-durable", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: text, Metadata: map[string]any{"durable": true}})
	mustWriteRecord(t, b, MemoryRecord{ID: "dup-plain", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: text})

	res, err := b.CompactMemoryRecords(ctx, CompactionConfig{EpisodeTTLDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deduped == 0 {
		t.Fatalf("expected dedupe activity, got %#v", res)
	}
	pinned, _, _ := b.GetMemoryRecord(ctx, "dup-pinned")
	durable, _, _ := b.GetMemoryRecord(ctx, "dup-durable")
	plain, _, _ := b.GetMemoryRecord(ctx, "dup-plain")
	if pinned.SupersededBy != "" || durable.SupersededBy != "" {
		t.Fatalf("pinned/durable should remain active: pinned=%q durable=%q", pinned.SupersededBy, durable.SupersededBy)
	}
	if plain.SupersededBy == "" {
		t.Fatalf("plain duplicate should be superseded")
	}
}

func TestCompactionV2DoesNotSupersedePinnedDurableTargets(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	text := "Pinned decision preserves referenced prior record"
	mustWriteRecord(t, b, MemoryRecord{ID: "prior-record", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: text})
	mustWriteRecord(t, b, MemoryRecord{ID: "pinned-new", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: text, Pinned: true, Supersedes: []string{"prior-record"}})

	res, err := b.CompactMemoryRecords(ctx, CompactionConfig{EpisodeTTLDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	prior, _, _ := b.GetMemoryRecord(ctx, "prior-record")
	if prior.SupersededBy != "" {
		t.Fatalf("record referenced by pinned supersedes should not be dedupe-superseded, result=%#v prior=%#v", res, prior)
	}
}

func TestCompactionV2RepairsSupersessionAndFlagsStale(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	old := time.Now().Add(-60 * 24 * time.Hour)
	mustWriteRecord(t, b, MemoryRecord{ID: "missing-target", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "x", Text: "stuck supersession", SupersededBy: "does-not-exist"})
	mustWriteRecord(t, b, MemoryRecord{ID: "cycle-a", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "x", Text: "cycle a", SupersededBy: "cycle-b"})
	mustWriteRecord(t, b, MemoryRecord{ID: "cycle-b", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "x", Text: "cycle b", SupersededBy: "cycle-c"})
	mustWriteRecord(t, b, MemoryRecord{ID: "cycle-c", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "x", Text: "cycle c", SupersededBy: "cycle-a"})
	mustWriteRecord(t, b, MemoryRecord{ID: "stale-episode", Type: MemoryRecordTypeEpisode, Scope: MemoryRecordScopeSession, Subject: "old", Text: "old episode", CreatedAt: old, UpdatedAt: old})

	res, err := b.CompactMemoryRecords(ctx, CompactionConfig{EpisodeTTLDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if res.SupersessionFix == 0 || res.StaleFlagged == 0 {
		t.Fatalf("expected supersession fixes and stale flags, got %#v", res)
	}
	fixed, _, _ := b.GetMemoryRecord(ctx, "missing-target")
	if fixed.SupersededBy != "" {
		t.Fatalf("missing supersession target should be repaired, got %q", fixed.SupersededBy)
	}
	cycleA, _, _ := b.GetMemoryRecord(ctx, "cycle-a")
	cycleB, _, _ := b.GetMemoryRecord(ctx, "cycle-b")
	cycleC, _, _ := b.GetMemoryRecord(ctx, "cycle-c")
	cycleNext := map[string]string{"cycle-a": cycleA.SupersededBy, "cycle-b": cycleB.SupersededBy, "cycle-c": cycleC.SupersededBy}
	for id := range cycleNext {
		seen := map[string]bool{}
		for cur := id; cur != ""; cur = cycleNext[cur] {
			if seen[cur] {
				t.Fatalf("supersession cycle still present starting at %s: %#v", id, cycleNext)
			}
			seen[cur] = true
		}
	}
	stale, _, _ := b.GetMemoryRecord(ctx, "stale-episode")
	if stale.Metadata["stale"] != true {
		t.Fatalf("stale flag expected in metadata, got %#v", stale.Metadata)
	}
}

func TestMemoryHealthIncludesV2Signals(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	mustWriteRecord(t, b, MemoryRecord{ID: "h-a", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "dup", Text: "same text"})
	mustWriteRecord(t, b, MemoryRecord{ID: "h-b", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "dup", Text: "same text"})
	mustWriteRecord(t, b, MemoryRecord{ID: "h-cycle-1", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "x", Text: "cycle1", SupersededBy: "h-cycle-2"})
	mustWriteRecord(t, b, MemoryRecord{ID: "h-cycle-2", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "x", Text: "cycle2", SupersededBy: "h-cycle-1"})
	mustWriteRecord(t, b, MemoryRecord{ID: "h-stale", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "x", Text: "stale", Metadata: map[string]any{"stale": true, "stale_reason": "expired"}})

	health, err := b.MemoryHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if health.IssueCounts["duplicate_hash"] == 0 || health.IssueCounts["supersession_cycle"] == 0 || health.IssueCounts["stale_flagged"] == 0 {
		t.Fatalf("expected v2 health signals, got %#v", health)
	}
	if _, ok := health.Index["duplicate_clusters"]; !ok {
		t.Fatalf("expected duplicate_clusters index summary, got %#v", health.Index)
	}
}

func mustWriteRecord(t *testing.T, b *SQLiteBackend, rec MemoryRecord) {
	t.Helper()
	if err := b.WriteMemoryRecord(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

func hasCard(cards []MemoryCard, id string) bool {
	for _, card := range cards {
		if card.ID == id {
			return true
		}
	}
	return false
}
