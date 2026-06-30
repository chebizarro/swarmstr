package lancedb

import (
	"context"
	"path/filepath"
	"testing"
)

func TestEmbeddedBackendUpsertSearchDeleteHealth(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "vectors.json")
	backend, err := New(Options{Path: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := backend.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}

	docs := []VectorDocument{
		{ID: "cat", Text: "cat memory", Vector: []float32{1, 0, 0}},
		{ID: "dog", Text: "dog memory", Vector: []float32{0.9, 0.1, 0}},
		{ID: "sky", Text: "sky memory", Vector: []float32{0, 0, 1}},
	}
	if err := backend.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := backend.Search(ctx, []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results)=%d, want 2", len(results))
	}
	if results[0].ID != "cat" || results[1].ID != "dog" {
		t.Fatalf("nearest results = %v, want cat then dog", []string{results[0].ID, results[1].ID})
	}

	if err := backend.Delete(ctx, []string{"cat"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	results, err = backend.Search(ctx, []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	if len(results) == 0 || results[0].ID == "cat" {
		t.Fatalf("deleted document returned: %#v", results)
	}
}

func TestEmbeddedBackendPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "vectors.json")
	backend, err := New(Options{Path: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := backend.Upsert(ctx, []VectorDocument{{ID: "one", Text: "first", Vector: []float32{1, 0}}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	reopened, err := New(Options{Path: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	results, err := reopened.Search(ctx, []float32{1, 0}, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "one" {
		t.Fatalf("persisted results = %#v", results)
	}
}
