package memory

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestClassifyQueryIntentSupportedSet(t *testing.T) {
	cases := map[string]string{
		"what preferences do you remember about editors": QueryIntentPreference,
		"what did we decide about deploys":               QueryIntentDecision,
		"what constraints apply to prod":                 QueryIntentConstraint,
		"any tool lessons for go test":                   QueryIntentToolLesson,
		"what happened earlier with auth":                QueryIntentEpisodic,
		"show the session summary":                       QueryIntentSummary,
		"audit deleted memory about deploy":              QueryIntentAudit,
		"latest memory about canary":                     QueryIntentRecent,
		"reference docs for sqlite":                      QueryIntentReference,
		"canary rollout":                                 QueryIntentGeneral,
	}
	for query, want := range cases {
		got := ClassifyQueryIntent(query)
		if got.Name != want {
			t.Fatalf("ClassifyQueryIntent(%q)=%q want %q (%#v)", query, got.Name, want, got)
		}
	}
}

func TestQueryIntentRoutingAndExplicitTypeOverride(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	mustWriteRecord(t, b, MemoryRecord{ID: "pref-editor", Type: MemoryRecordTypePreference, Scope: MemoryRecordScopeUser, Subject: "editor", Text: "User preference: editor is Vim for quick edits.", Confidence: 0.9, Salience: 0.9})
	mustWriteRecord(t, b, MemoryRecord{ID: "decision-editor", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "editor", Text: "Editor decision: use VS Code for paired demos.", Confidence: 0.9, Salience: 0.9})

	cards, err := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "what preferences about editor", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) == 0 || cards[0].ID != "pref-editor" {
		t.Fatalf("intent-routed preference query got %#v", cards)
	}

	override, err := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "what preferences about editor", Types: []string{MemoryRecordTypeDecision}, ExplicitTypes: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(override) == 0 || override[0].ID != "decision-editor" {
		t.Fatalf("explicit type override should return decision, got %#v", override)
	}
}

func TestNaturalLanguageRewriteAndRecentIntentRetrieveExpectedRecords(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	old := time.Now().Add(-48 * time.Hour)
	mustWriteRecord(t, b, MemoryRecord{ID: "old-canary", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: "Canary deploys used to be optional.", Confidence: 0.7, Salience: 0.7, CreatedAt: old, UpdatedAt: old})
	mustWriteRecord(t, b, MemoryRecord{ID: "deploy-decision", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: "Production deploys require canary rollout.", Confidence: 0.9, Salience: 0.9})

	decision, err := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "what did we decide about deploys", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision) == 0 || decision[0].ID != "deploy-decision" {
		t.Fatalf("natural-language decision query got %#v", decision)
	}

	recent, err := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "latest memory about canary", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) == 0 || recent[0].ID != "deploy-decision" {
		t.Fatalf("recent intent should not hard-filter project decisions/facts, got %#v", recent)
	}
}

func TestHybridRankingDebugWhyAndDiagnostics(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	old := time.Now().Add(-90 * 24 * time.Hour)
	mustWriteRecord(t, b, MemoryRecord{ID: "plain-canary", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: "Canary rollout note.", Confidence: 0.5, Salience: 0.2, CreatedAt: old, UpdatedAt: old})
	mustWriteRecord(t, b, MemoryRecord{ID: "pinned-canary", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: "Canary rollout is required for production deploys.", Confidence: 0.95, Salience: 0.95, Pinned: true, Metadata: map[string]any{"durable": true}})
	mustWriteRecord(t, b, MemoryRecord{ID: "dup-a", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "dup", Text: "Duplicate diagnostic memory", Confidence: 0.7, Salience: 0.7})
	mustWriteRecord(t, b, MemoryRecord{ID: "dup-b", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "dup", Text: "Duplicate diagnostic memory", Confidence: 0.7, Salience: 0.7})

	explain, err := b.ExplainMemoryQuery(ctx, MemoryQuery{Query: "decision canary rollout", IncludeDebug: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if explain.Intent.Name != QueryIntentDecision {
		t.Fatalf("intent=%#v want decision", explain.Intent)
	}
	if len(explain.Results) == 0 || explain.Results[0].ID != "pinned-canary" {
		t.Fatalf("expected pinned durable decision first, got %#v", explain.Results)
	}
	if explain.Results[0].Why == nil || explain.Results[0].Why.Components["pinned"] == 0 || explain.Results[0].Why.Components["durable"] == 0 {
		t.Fatalf("missing ranking why components: %#v", explain.Results[0].Why)
	}
	if len(explain.Excluded) == 0 {
		t.Fatalf("expected exclusions for type-routed non-decision candidates: %#v", explain)
	}

	stats, err := b.MemoryStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRecords != 4 || stats.ByType[MemoryRecordTypeDecision] != 1 || stats.Pinned != 1 || stats.Durable != 1 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
	health, err := b.MemoryHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != "warn" || health.IssueCounts["duplicate_hash"] == 0 {
		t.Fatalf("expected duplicate warning, got %#v", health)
	}
}

func TestMemoryHealthWarnsOnLargeReindexBacklog(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	for i := 0; i < 10001; i++ {
		mustWriteRecord(t, b, MemoryRecord{ID: fmt.Sprintf("backlog-%d", i), Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Subject: "backlog", Text: fmt.Sprintf("backlog item %d", i), Confidence: 0.5, Salience: 0.2})
	}
	health, err := b.MemoryHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if health.IssueCounts["reindex_backlog"] <= 10000 {
		t.Fatalf("expected reindex backlog warning threshold, got %#v", health.IssueCounts)
	}
	samples := health.IssueSamples["reindex_backlog"]
	if len(samples) == 0 || samples[len(samples)-1] != "Backlog > 10000. Suggested manual action: memory_reindex --batch" {
		t.Fatalf("expected manual batch suggestion, got %#v", samples)
	}
}
