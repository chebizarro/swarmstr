package memory

import (
	"testing"

	"metiq/internal/store/state"
)

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := OpenIndex(t.TempDir() + "/memory-index.json")
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	idx.cacheCap = 4
	return idx
}

func TestSearchCacheInvalidatedOnAdd(t *testing.T) {
	idx := newTestIndex(t)
	idx.Add(state.MemoryDoc{MemoryID: "m1", SessionID: "s1", Text: "hello world", Unix: 1})

	got := idx.Search("hello", 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}

	idx.Add(state.MemoryDoc{MemoryID: "m2", SessionID: "s1", Text: "hello again", Unix: 2})
	got = idx.Search("hello", 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 results after add invalidation, got %d", len(got))
	}
	if got[0].MemoryID != "m2" {
		t.Fatalf("expected newest hit first, got %q", got[0].MemoryID)
	}
}

func TestSearchSessionCacheInvalidatedOnDelete(t *testing.T) {
	idx := newTestIndex(t)
	idx.Add(state.MemoryDoc{MemoryID: "m1", SessionID: "s1", Text: "project notes", Unix: 1})
	idx.Add(state.MemoryDoc{MemoryID: "m2", SessionID: "s2", Text: "project notes", Unix: 2})

	got := idx.SearchSession("s1", "project", 5)
	if len(got) != 1 || got[0].MemoryID != "m1" {
		t.Fatalf("unexpected session search baseline: %+v", got)
	}

	if ok := idx.Delete("m1"); !ok {
		t.Fatal("expected delete to succeed")
	}
	got = idx.SearchSession("s1", "project", 5)
	if len(got) != 0 {
		t.Fatalf("expected 0 results after delete invalidation, got %d", len(got))
	}
}

func TestSearchCacheEvictsOldest(t *testing.T) {
	idx := newTestIndex(t)
	idx.cacheCap = 1
	idx.Add(state.MemoryDoc{MemoryID: "m1", SessionID: "s1", Text: "alpha beta gamma", Unix: 1})

	_ = idx.Search("alpha", 5)
	if len(idx.cache) != 1 {
		t.Fatalf("expected cache size 1, got %d", len(idx.cache))
	}
	_ = idx.Search("beta", 5)
	if len(idx.cache) != 1 {
		t.Fatalf("expected cache size 1 after eviction, got %d", len(idx.cache))
	}
	if _, ok := idx.cache[searchCacheKey("", "alpha", 5)]; ok {
		t.Fatal("expected oldest cache key to be evicted")
	}
}
