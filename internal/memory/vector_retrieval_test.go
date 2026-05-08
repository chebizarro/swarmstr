package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type stubEmbeddingProvider struct {
	provider EmbeddingProvider
	vectors  map[string][]float32
	err      error
}

func (p stubEmbeddingProvider) EmbeddingProvider() EmbeddingProvider { return p.provider }

func (p stubEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	_ = ctx
	if p.err != nil {
		return nil, p.err
	}
	for key, vec := range p.vectors {
		if strings.Contains(strings.ToLower(text), strings.ToLower(key)) {
			return append([]float32(nil), vec...), nil
		}
	}
	return []float32{1, 0, 0}, nil
}

func writeVectorTestRecord(t *testing.T, b *SQLiteBackend, rec MemoryRecord) MemoryRecord {
	t.Helper()
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = now
	}
	if rec.Confidence == 0 {
		rec.Confidence = 0.8
	}
	if err := b.WriteMemoryRecord(context.Background(), rec); err != nil {
		t.Fatalf("WriteMemoryRecord: %v", err)
	}
	return rec
}

func TestVectorRetrievalDisabledUsesBM25Fallback(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	writeVectorTestRecord(t, backend, MemoryRecord{ID: "bm25-only", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Text: "alpha lexical marker"})

	provider := stubEmbeddingProvider{provider: EmbeddingProvider{ID: "stub", Model: "m", Version: "v1"}, vectors: map[string][]float32{"alpha": {1, 0}}}
	if err := backend.ConfigureVectorRetrieval(MemoryVectorRetrievalConfig{Enabled: false}, provider); err != nil {
		t.Fatal(err)
	}
	if err := backend.StoreMemoryEmbedding(context.Background(), "bm25-only", provider.provider, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}

	explain, err := backend.ExplainMemoryQuery(context.Background(), MemoryQuery{Query: "alpha", IncludeDebug: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if explain.ResultCount != 1 || explain.Results[0].ID != "bm25-only" {
		t.Fatalf("unexpected results: %+v", explain.Results)
	}
	if explain.CandidateSet.VectorEnabled || explain.CandidateSet.VectorCandidates != 0 {
		t.Fatalf("vectors should be disabled in candidate summary: %+v", explain.CandidateSet)
	}
}

func TestVectorRetrievalMergesBM25AndCompatibleVectorsWithRRF(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	provider := stubEmbeddingProvider{provider: EmbeddingProvider{ID: "stub", Model: "toy", Version: "v1"}, vectors: map[string][]float32{"semantic": {0, 1}}}
	if err := backend.ConfigureVectorRetrieval(MemoryVectorRetrievalConfig{Enabled: true, MinSimilarity: -1, RRFK: 10}, provider); err != nil {
		t.Fatal(err)
	}
	writeVectorTestRecord(t, backend, MemoryRecord{ID: "lexical", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Text: "semantic keyword appears here"})
	writeVectorTestRecord(t, backend, MemoryRecord{ID: "vector-only", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Text: "unrelated words but close in vector space"})
	if err := backend.StoreMemoryEmbedding(context.Background(), "lexical", provider.provider, []float32{0, 1}); err != nil {
		t.Fatal(err)
	}
	if err := backend.StoreMemoryEmbedding(context.Background(), "vector-only", provider.provider, []float32{0, 1}); err != nil {
		t.Fatal(err)
	}

	explain, err := backend.ExplainMemoryQuery(context.Background(), MemoryQuery{Query: "semantic", IncludeDebug: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]MemoryCard{}
	for _, card := range explain.Results {
		ids[card.ID] = card
	}
	if _, ok := ids["lexical"]; !ok {
		t.Fatalf("expected lexical result in %+v", explain.Results)
	}
	vecCard, ok := ids["vector-only"]
	if !ok {
		t.Fatalf("expected vector-only result from RRF merge in %+v", explain.Results)
	}
	if vecCard.Why == nil || vecCard.Why.VectorRank == 0 || vecCard.Why.Components["rrf"] == 0 {
		t.Fatalf("expected vector/RRF debug metadata, got %+v", vecCard.Why)
	}
	if explain.CandidateSet.VectorCandidates == 0 {
		t.Fatalf("expected vector candidates in summary: %+v", explain.CandidateSet)
	}
}

func TestVectorRetrievalSkipsIncompatibleEmbeddingVersions(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	provider := stubEmbeddingProvider{provider: EmbeddingProvider{ID: "stub", Model: "toy", Version: "v1"}, vectors: map[string][]float32{"semantic": {1, 0}}}
	if err := backend.ConfigureVectorRetrieval(MemoryVectorRetrievalConfig{Enabled: true, MinSimilarity: -1}, provider); err != nil {
		t.Fatal(err)
	}
	writeVectorTestRecord(t, backend, MemoryRecord{ID: "v2-only", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Text: "unrelated text"})
	if err := backend.StoreMemoryEmbedding(context.Background(), "v2-only", EmbeddingProvider{ID: "stub", Model: "toy", Version: "v2"}, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}

	explain, err := backend.ExplainMemoryQuery(context.Background(), MemoryQuery{Query: "semantic", IncludeDebug: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, card := range explain.Results {
		if card.ID == "v2-only" {
			t.Fatalf("incompatible v2 embedding was compared by v1 provider: %+v", explain.Results)
		}
	}
}

func TestVectorReindexEmbedsMissingCompatibleVersion(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	provider := stubEmbeddingProvider{provider: EmbeddingProvider{ID: "stub", Model: "toy", Version: "v1"}, vectors: map[string][]float32{"durable": {1, 0}}}
	if err := backend.ConfigureVectorRetrieval(MemoryVectorRetrievalConfig{Enabled: true}, provider); err != nil {
		t.Fatal(err)
	}
	writeVectorTestRecord(t, backend, MemoryRecord{ID: "needs-vector", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Text: "durable fact"})
	res, err := backend.ReindexMemoryEmbeddings(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reindexed != 1 || backend.MemoryVectorStats().Reindexed != 1 {
		t.Fatalf("unexpected reindex result/stats: %+v %+v", res, backend.MemoryVectorStats())
	}
}

func TestVectorReindexSessionCap100(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	provider := stubEmbeddingProvider{provider: EmbeddingProvider{ID: "stub", Model: "toy", Version: "v1"}, vectors: map[string][]float32{"item": {1, 0}}}
	if err := backend.ConfigureVectorRetrieval(MemoryVectorRetrievalConfig{Enabled: true, ReindexBatchSize: 200, ReindexDailyLimit: 1000}, provider); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 130; i++ {
		writeVectorTestRecord(t, backend, MemoryRecord{ID: fmt.Sprintf("rec-%03d", i), Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Text: fmt.Sprintf("item %d", i)})
	}
	res, err := backend.ReindexMemoryEmbeddings(context.Background(), 500)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reindexed != 100 {
		t.Fatalf("expected session cap 100 reindexed, got %+v", res)
	}
}

func TestVectorReindexDailyLimitAndPriority(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	provider := stubEmbeddingProvider{provider: EmbeddingProvider{ID: "stub", Model: "toy", Version: "v1"}, vectors: map[string][]float32{"x": {1, 0}}}
	if err := backend.ConfigureVectorRetrieval(MemoryVectorRetrievalConfig{Enabled: true, ReindexBatchSize: 10, ReindexDailyLimit: 2}, provider); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	writeVectorTestRecord(t, backend, MemoryRecord{ID: "pinned", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Text: "x pinned", Pinned: true, UpdatedAt: now.Add(-10 * time.Minute)})
	writeVectorTestRecord(t, backend, MemoryRecord{ID: "durable", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Text: "x durable", Source: MemorySource{FilePath: "notes.md"}, UpdatedAt: now.Add(-9 * time.Minute)})
	writeVectorTestRecord(t, backend, MemoryRecord{ID: "salient", Type: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject, Text: "x salient", Salience: 0.99, UpdatedAt: now.Add(-8 * time.Minute)})

	res, err := backend.ReindexMemoryEmbeddings(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reindexed != 2 {
		t.Fatalf("expected daily limit 2, got %+v", res)
	}
	rows, err := backend.db.Query(`SELECT record_id FROM reindex_status WHERE status='completed' ORDER BY id ASC LIMIT 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	order := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			order = append(order, id)
		}
	}
	if len(order) != 2 || order[0] != "pinned" || order[1] != "durable" {
		t.Fatalf("expected priority order pinned->durable, got %v", order)
	}
	res2, err := backend.ReindexMemoryEmbeddings(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Reindexed != 0 {
		t.Fatalf("expected daily cap to block second run, got %+v", res2)
	}
}
