// Package lancedb implements a local, dependency-free JSON-backed cosine vector
// store used as a memory backend.
//
// IMPORTANT: despite its name, this package is NOT a real LanceDB integration —
// there is no LanceDB SDK or service involved. Vectors are held in memory and
// persisted to a single JSON file, and nearest-neighbour search is a brute-force
// cosine scan. The "lancedb" name is retained only for backward compatibility of
// the existing memory backend key; treat it as a local JSON vector store.
package lancedb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// VectorDocument is a vector-indexed memory document.
type VectorDocument struct {
	// Generation identifies the publication generation. Zero requests assignment
	// to the backend's next generation; lower explicit generations are rejected.
	Generation uint64         `json:"generation,omitempty"`
	ID         string         `json:"id"`
	Text       string         `json:"text"`
	Vector     []float32      `json:"vector"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// Backend stores vector documents and supports cosine nearest-neighbor search.
type Backend interface {
	Upsert(ctx context.Context, docs []VectorDocument) error
	Delete(ctx context.Context, ids []string) error
	Search(ctx context.Context, vector []float32, limit int) ([]VectorDocument, error)
	// All returns a copy of every stored document, regardless of vector
	// dimensionality. Used for administrative operations (count, list, compact)
	// that must not depend on a probe query matching the stored vector length.
	All(ctx context.Context) ([]VectorDocument, error)
	Health(ctx context.Context) error
}

// Options configures the embedded local JSON vector backend.
type Options struct {
	// Path is the JSON persistence file. Empty uses ~/.metiq/json-vector-memory.json.
	Path string
}

// New opens the embedded local JSON vector backend. The package name remains
// for compatibility, but the default filename honestly identifies the storage.
func New(opts Options) (*EmbeddedBackend, error) {
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		path = filepath.Join(home, ".metiq", "json-vector-memory.json")
		legacy := filepath.Join(home, ".metiq", "lancedb-memory.json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if _, legacyErr := os.Stat(legacy); legacyErr == nil {
				path = legacy
			}
		}
	}
	b := &EmbeddedBackend{path: path, docs: map[string]VectorDocument{}}
	if err := b.load(); err != nil {
		return nil, err
	}
	return b, nil
}

// EmbeddedBackend is a local JSON-backed cosine vector store.
type EmbeddedBackend struct {
	mu         sync.RWMutex
	path       string
	generation uint64
	docs       map[string]VectorDocument
}

// StaleGenerationError reports an attempted replacement by an older publisher.
type StaleGenerationError struct {
	ID       string
	Incoming uint64
	Current  uint64
}

func (e *StaleGenerationError) Error() string {
	return fmt.Sprintf("local vector store: stale generation for %q: incoming=%d current=%d", e.ID, e.Incoming, e.Current)
}

// Upsert validates the whole batch and atomically publishes a new generation.
func (b *EmbeddedBackend) Upsert(ctx context.Context, docs []VectorDocument) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(docs) == 0 {
		return nil
	}
	next := cloneDocs(b.docs)
	commitGeneration := b.generation + 1
	for _, doc := range docs {
		id := strings.TrimSpace(doc.ID)
		if id == "" {
			return errors.New("local vector store: document id is required")
		}
		if strings.TrimSpace(doc.Text) == "" {
			return fmt.Errorf("local vector store: document %q text is required", id)
		}
		vec := normalize(doc.Vector)
		if vec == nil {
			return fmt.Errorf("local vector store: document %q vector is empty or zero", id)
		}
		if doc.Generation != 0 && doc.Generation < b.generation {
			return &StaleGenerationError{ID: id, Incoming: doc.Generation, Current: b.generation}
		}
		if current, ok := next[id]; ok && doc.Generation != 0 && doc.Generation < current.Generation {
			return &StaleGenerationError{ID: id, Incoming: doc.Generation, Current: current.Generation}
		}
		if doc.Generation > commitGeneration {
			commitGeneration = doc.Generation
		}
		doc.ID = id
		doc.Vector = vec
		next[id] = cloneDoc(doc)
	}
	for id, doc := range next {
		if doc.Generation == 0 {
			doc.Generation = commitGeneration
			next[id] = doc
		}
	}
	if err := b.saveStateLocked(next, commitGeneration); err != nil {
		return err
	}
	b.docs = next
	b.generation = commitGeneration
	return nil
}

// Delete atomically publishes removals as a new generation.
func (b *EmbeddedBackend) Delete(ctx context.Context, ids []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	next := cloneDocs(b.docs)
	changed := false
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if _, ok := next[id]; ok {
			delete(next, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	generation := b.generation + 1
	if err := b.saveStateLocked(next, generation); err != nil {
		return err
	}
	b.docs = next
	b.generation = generation
	return nil
}

// Search returns the nearest documents by cosine similarity.
func (b *EmbeddedBackend) Search(ctx context.Context, vector []float32, limit int) ([]VectorDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query := normalize(vector)
	if query == nil || limit <= 0 {
		return nil, nil
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	type scored struct {
		doc   VectorDocument
		score float64
	}
	scoredDocs := make([]scored, 0, len(b.docs))
	for _, doc := range b.docs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(doc.Vector) != len(query) {
			continue
		}
		scoredDocs = append(scoredDocs, scored{doc: cloneDoc(doc), score: dot(query, doc.Vector)})
	}
	sort.Slice(scoredDocs, func(i, j int) bool {
		if scoredDocs[i].score == scoredDocs[j].score {
			return scoredDocs[i].doc.ID < scoredDocs[j].doc.ID
		}
		return scoredDocs[i].score > scoredDocs[j].score
	})
	if len(scoredDocs) > limit {
		scoredDocs = scoredDocs[:limit]
	}
	out := make([]VectorDocument, len(scoredDocs))
	for i, scored := range scoredDocs {
		out[i] = scored.doc
	}
	return out, nil
}

// All returns a copy of every stored document. Order is unspecified.
func (b *EmbeddedBackend) All(ctx context.Context) ([]VectorDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]VectorDocument, 0, len(b.docs))
	for _, doc := range b.docs {
		out = append(out, cloneDoc(doc))
	}
	return out, nil
}

// Health verifies that the backend is usable and its persistence directory is writable.
func (b *EmbeddedBackend) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.RLock()
	path := b.path
	ok := b.docs != nil
	b.mu.RUnlock()
	if !ok {
		return errors.New("local vector store: backend is closed")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// Close flushes the backend to disk.
func (b *EmbeddedBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.saveLocked()
}

type diskState struct {
	Generation uint64           `json:"generation"`
	Docs       []VectorDocument `json:"docs"`
}

func (b *EmbeddedBackend) load() error {
	raw, err := os.ReadFile(b.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state diskState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("local vector store: parse %s: %w", b.path, err)
	}
	b.generation = state.Generation
	for _, doc := range state.Docs {
		if strings.TrimSpace(doc.ID) == "" || strings.TrimSpace(doc.Text) == "" {
			continue
		}
		vec := normalize(doc.Vector)
		if vec == nil {
			continue
		}
		doc.Vector = vec
		if doc.Generation > b.generation {
			b.generation = doc.Generation
		}
		b.docs[doc.ID] = cloneDoc(doc)
	}
	return nil
}

func (b *EmbeddedBackend) saveLocked() error {
	return b.saveStateLocked(b.docs, b.generation)
}

func (b *EmbeddedBackend) saveStateLocked(docMap map[string]VectorDocument, generation uint64) error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
		return err
	}
	docs := make([]VectorDocument, 0, len(docMap))
	for _, doc := range docMap {
		docs = append(docs, cloneDoc(doc))
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	raw, err := json.MarshalIndent(diskState{Generation: generation, Docs: docs}, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(b.path), filepath.Base(b.path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		cleanup()
		return err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, b.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func cloneDoc(doc VectorDocument) VectorDocument {
	out := VectorDocument{Generation: doc.Generation, ID: doc.ID, Text: doc.Text}
	if len(doc.Vector) > 0 {
		out.Vector = append([]float32(nil), doc.Vector...)
	}
	if len(doc.Metadata) > 0 {
		out.Metadata = make(map[string]any, len(doc.Metadata))
		for k, v := range doc.Metadata {
			out.Metadata[k] = v
		}
	}
	return out
}

func cloneDocs(in map[string]VectorDocument) map[string]VectorDocument {
	out := make(map[string]VectorDocument, len(in))
	for id, doc := range in {
		out[id] = cloneDoc(doc)
	}
	return out
}

func normalize(v []float32) []float32 {
	if len(v) == 0 {
		return nil
	}
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return nil
	}
	inv := float32(1 / math.Sqrt(norm))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

func dot(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}
