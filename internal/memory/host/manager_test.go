package host

import (
	"context"
	"errors"
	"testing"

	"metiq/internal/memory"
)

type fakeBackend struct {
	hits     []memory.IndexedMemory
	records  map[string]memory.MemoryRecord
	canceled bool
}

func (b *fakeBackend) Search(query string, limit int) []memory.IndexedMemory {
	return limitHits(b.hits, limit)
}

func (b *fakeBackend) SearchWithContext(ctx context.Context, query string, limit int) []memory.IndexedMemory {
	if ctx.Err() != nil {
		b.canceled = true
		return nil
	}
	return b.Search(query, limit)
}

func (b *fakeBackend) SearchSession(sessionID, query string, limit int) []memory.IndexedMemory {
	var out []memory.IndexedMemory
	for _, h := range b.hits {
		if h.SessionID == sessionID {
			out = append(out, h)
		}
	}
	return limitHits(out, limit)
}

func (b *fakeBackend) GetMemoryRecord(ctx context.Context, id string) (memory.MemoryRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return memory.MemoryRecord{}, false, err
	}
	r, ok := b.records[id]
	return r, ok, nil
}

func (b *fakeBackend) Count() int        { return len(b.hits) }
func (b *fakeBackend) SessionCount() int { return 1 }
func (b *fakeBackend) BackendStatus() memory.BackendStatus {
	return memory.BackendStatus{Name: "fake", Available: true}
}
func (b *fakeBackend) MemoryStatus() memory.StoreStatus {
	return memory.StoreStatus{Kind: "fake", Primary: b.BackendStatus()}
}
func (b *fakeBackend) VectorAvailable() bool { return true }

func limitHits(hits []memory.IndexedMemory, limit int) []memory.IndexedMemory {
	if limit > 0 && len(hits) > limit {
		return hits[:limit]
	}
	return hits
}

func newTestManager(t *testing.T) *SearchManager {
	t.Helper()
	fb := &fakeBackend{
		hits: []memory.IndexedMemory{
			{MemoryID: "m1", Text: "ranked memory", Confidence: 0.9, Source: memory.MemorySourceKindManual},
			{MemoryID: "s1", SessionID: "session-a", Text: "session memory", Confidence: 0.8, Source: memory.MemorySourceKindTurn},
			{MemoryID: "m2", Text: "lower memory", Confidence: 0.7, Source: memory.MemorySourceKindManual},
		},
		records: map[string]memory.MemoryRecord{
			"m1": {ID: "m1", Text: "line1\nline2\nline3", Source: memory.MemorySource{Ref: "memory/m1"}},
		},
	}
	mgr, err := NewManager(Options{Backend: fb, EmbeddingProvider: memory.StaticMemoryEmbeddingProvider{Provider: memory.EmbeddingProvider{ID: "static", Model: "test", Version: "v1"}, Dims: 4}})
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}

func TestSearchFiltersSourcesAndReturnsRankedResults(t *testing.T) {
	mgr := newTestManager(t)
	var debug []DebugEvent
	results, err := mgr.Search(context.Background(), "memory", SearchOptions{
		MaxResults: 5,
		Sources:    []Source{SourceMemory},
		Debug: func(ctx context.Context, event DebugEvent) {
			debug = append(debug, event)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 memory results, got %d", len(results))
	}
	if results[0].Ref != "m1" || results[0].Score != 0.9 || results[1].Ref != "m2" {
		t.Fatalf("unexpected ranked results: %#v", results)
	}
	if len(debug) == 0 || debug[0].Operation != "search.start" {
		t.Fatalf("expected debug events, got %#v", debug)
	}
}

func TestStatusReportsCountsAndVectorCapability(t *testing.T) {
	mgr := newTestManager(t)
	status, err := mgr.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.Backend != "fake" || status.Count != 3 || status.SessionCount != 1 {
		t.Fatalf("unexpected status: %#v", status)
	}
	if !status.VectorsAvailable || !status.EmbeddingsAvailable || status.Embedding == nil || status.Embedding.Dims != 4 {
		t.Fatalf("unexpected capabilities: %#v", status)
	}
}

func TestReadFileByRef(t *testing.T) {
	mgr := newTestManager(t)
	read, err := mgr.ReadFile(context.Background(), FileRef{Ref: "m1", FromLine: 2, Lines: 1})
	if err != nil {
		t.Fatal(err)
	}
	if read.Ref != "m1" || read.Path != "memory/m1" || read.Content != "line2" {
		t.Fatalf("unexpected read result: %#v", read)
	}
}

func TestCancellationRespected(t *testing.T) {
	mgr := newTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := mgr.Search(ctx, "memory", SearchOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	_, err = mgr.ReadFile(ctx, FileRef{Ref: "m1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected read cancellation, got %v", err)
	}
	_, err = mgr.Status(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected status cancellation, got %v", err)
	}
}
