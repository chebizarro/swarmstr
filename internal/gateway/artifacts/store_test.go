package artifacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(filepath.Join(t.TempDir(), "artifacts"))
	base := time.UnixMilli(1_700_000_000_000)
	var mu sync.Mutex
	tick := 0
	s.SetNowFunc(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		tick++
		return base.Add(time.Duration(tick) * time.Second)
	})
	return s
}

func TestPutGetDownloadRoundTrip(t *testing.T) {
	s := newTestStore(t)
	data := []byte("hello artifact")
	sum := sha256.Sum256(data)

	summary, err := s.Put(PutRequest{
		Type:       "file",
		Title:      "hello.txt",
		MimeType:   "text/plain",
		SessionKey: "sess-1",
		RunID:      "run-1",
		AgentID:    "main",
		Source:     "test",
		Data:       data,
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if summary.ID != "art_"+hex.EncodeToString(sum[:])[:32] {
		t.Fatalf("unexpected artifact id %q", summary.ID)
	}
	if summary.SizeBytes != int64(len(data)) || summary.Download.Mode != "bytes" {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	got, err := s.Get(summary.ID, Query{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != summary {
		t.Fatalf("get mismatch: %+v != %+v", got, summary)
	}

	gotDL, payload, err := s.Download(summary.ID, Query{SessionKey: "sess-1"})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if gotDL.ID != summary.ID || !bytes.Equal(payload, data) {
		t.Fatalf("download mismatch: %+v payload=%q", gotDL, payload)
	}
}

func TestScopeFiltersRestrictVisibility(t *testing.T) {
	s := newTestStore(t)
	a, err := s.Put(PutRequest{Title: "a", SessionKey: "sess-a", RunID: "run-a", Data: []byte("payload-a")})
	if err != nil {
		t.Fatalf("put a: %v", err)
	}
	b, err := s.Put(PutRequest{Title: "b", SessionKey: "sess-b", TaskID: "task-b", Data: []byte("payload-b")})
	if err != nil {
		t.Fatalf("put b: %v", err)
	}

	all, err := s.List(Query{})
	if err != nil || len(all) != 2 {
		t.Fatalf("list all: %v %+v", err, all)
	}
	// Newest-first ordering.
	if all[0].ID != b.ID || all[1].ID != a.ID {
		t.Fatalf("unexpected order: %+v", all)
	}

	scoped, err := s.List(Query{SessionKey: "sess-a"})
	if err != nil || len(scoped) != 1 || scoped[0].ID != a.ID {
		t.Fatalf("scoped list: %v %+v", err, scoped)
	}

	if _, err := s.Get(a.ID, Query{SessionKey: "sess-b"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected scope miss, got %v", err)
	}
	if _, _, err := s.Download(b.ID, Query{TaskID: "other"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected scope miss on download, got %v", err)
	}
	if _, err := s.Get(b.ID, Query{SessionKey: "sess-b", TaskID: "task-b"}); err != nil {
		t.Fatalf("matching filters rejected: %v", err)
	}
}

func TestContentDeduplicationSharesBlob(t *testing.T) {
	s := newTestStore(t)
	data := []byte("same payload")
	first, err := s.Put(PutRequest{Title: "first", SessionKey: "s1", Data: data})
	if err != nil {
		t.Fatalf("put first: %v", err)
	}
	second, err := s.Put(PutRequest{Title: "second", SessionKey: "s2", Data: data})
	if err != nil {
		t.Fatalf("put second: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("content-addressed ids diverged: %q %q", first.ID, second.ID)
	}
	// The latest metadata wins for the shared id.
	got, err := s.Get(first.ID, Query{})
	if err != nil || got.Title != "second" || got.SessionKey != "s2" {
		t.Fatalf("dedupe metadata: %v %+v", err, got)
	}
}

func TestLookupRejectsUnmintedIDs(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Put(PutRequest{Title: "seed", Data: []byte("seed")}); err != nil {
		t.Fatalf("put: %v", err)
	}
	for _, id := range []string{"", "../../etc/passwd", "art_../escape", "art_ZZ", "unknown", "art_0123456789abcdef0123456789abcdef0"} {
		if _, err := s.Get(id, Query{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("id %q: expected ErrNotFound, got %v", id, err)
		}
	}
	// Well-formed but absent id.
	if _, err := s.Get("art_0123456789abcdef0123456789abcdef", Query{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent id: %v", err)
	}
}

func TestEmptyStoreListsAndValidation(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "never-created"))
	list, err := s.List(Query{})
	if err != nil || len(list) != 0 {
		t.Fatalf("empty list: %v %+v", err, list)
	}
	if _, err := s.Get("art_0123456789abcdef0123456789abcdef", Query{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty get: %v", err)
	}
	if _, err := s.Put(PutRequest{Title: "", Data: []byte("x")}); err == nil {
		t.Fatal("expected title validation error")
	}
	if _, err := s.Put(PutRequest{Title: "x", Data: nil}); err == nil {
		t.Fatal("expected data validation error")
	}
}

func TestConcurrentPutAndList(t *testing.T) {
	s := newTestStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			data := []byte{byte(n), 'p', 'a', 'y'}
			if _, err := s.Put(PutRequest{Title: "concurrent", SessionKey: "sess", Data: data}); err != nil {
				t.Errorf("put %d: %v", n, err)
			}
			if _, err := s.List(Query{SessionKey: "sess"}); err != nil {
				t.Errorf("list %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()
	list, err := s.List(Query{})
	if err != nil || len(list) != 8 {
		t.Fatalf("final list: %v len=%d", err, len(list))
	}
}
