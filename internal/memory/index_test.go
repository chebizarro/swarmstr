package memory

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestIndexSavePublishesCurrentGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory-index.json")
	idx, err := OpenIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	idx.Add(state.MemoryDoc{MemoryID: "m1", Text: "first generation", Unix: 1})
	idx.Add(state.MemoryDoc{MemoryID: "m2", Text: "second generation", Unix: 2})
	if err := idx.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	reopened, err := OpenIndex(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.Count() != 2 || reopened.generation != idx.generation || reopened.persistedGeneration != idx.generation {
		t.Fatalf("generation-safe reload failed: count=%d generation=%d persisted=%d want=%d", reopened.Count(), reopened.generation, reopened.persistedGeneration, idx.generation)
	}
}

func TestCompactWithAppendOnlyFlushPreservesProvenance(t *testing.T) {
	idx := newTestIndex(t)
	idx.Add(state.MemoryDoc{MemoryID: "old", Text: "old owner memory", Unix: 1, OriginClass: string(MemoryOriginOwner), SessionKind: string(MemorySessionInteractive)})
	idx.Add(state.MemoryDoc{MemoryID: "new", Text: "new owner memory", Unix: 2, OriginClass: string(MemoryOriginOwner), SessionKind: string(MemorySessionInteractive)})
	journalPath := filepath.Join(t.TempDir(), "compaction.jsonl")
	removed, err := idx.CompactWithFlush(context.Background(), 1, &AppendOnlyCompactionJournal{Path: journalPath})
	if err != nil || removed != 1 || idx.Count() != 1 {
		t.Fatalf("compact removed=%d count=%d err=%v", removed, idx.Count(), err)
	}
	file, err := os.Open(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("missing journal record: %v", scanner.Err())
	}
	var record compactionJournalRecord
	if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record.Generation == 0 || record.Memory.MemoryID != "old" || record.Memory.OriginClass != string(MemoryOriginOwner) || record.Memory.SessionKind != string(MemorySessionInteractive) {
		t.Fatalf("unexpected journal record: %+v", record)
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
