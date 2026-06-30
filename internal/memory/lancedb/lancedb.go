// Package lancedb provides an embedded LanceDB-compatible vector memory backend.
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
	ID       string         `json:"id"`
	Text     string         `json:"text"`
	Vector   []float32      `json:"vector"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Backend stores vector documents and supports cosine nearest-neighbor search.
type Backend interface {
	Upsert(ctx context.Context, docs []VectorDocument) error
	Delete(ctx context.Context, ids []string) error
	Search(ctx context.Context, vector []float32, limit int) ([]VectorDocument, error)
	Health(ctx context.Context) error
}

// Options configures the embedded backend.
type Options struct {
	// Path is the JSON persistence file. Empty uses ~/.metiq/lancedb-memory.json.
	Path string
}

// New opens an embedded LanceDB-compatible vector backend.
func New(opts Options) (*EmbeddedBackend, error) {
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		path = filepath.Join(home, ".metiq", "lancedb-memory.json")
	}
	b := &EmbeddedBackend{path: path, docs: map[string]VectorDocument{}}
	if err := b.load(); err != nil {
		return nil, err
	}
	return b, nil
}

// EmbeddedBackend is a local, dependency-light vector store that mirrors the
// LanceDB backend contract without requiring an external service.
type EmbeddedBackend struct {
	mu   sync.RWMutex
	path string
	docs map[string]VectorDocument
}

// Upsert inserts or replaces vector documents.
func (b *EmbeddedBackend) Upsert(ctx context.Context, docs []VectorDocument) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, doc := range docs {
		id := strings.TrimSpace(doc.ID)
		if id == "" {
			return errors.New("lancedb: document id is required")
		}
		if strings.TrimSpace(doc.Text) == "" {
			return fmt.Errorf("lancedb: document %q text is required", id)
		}
		vec := normalize(doc.Vector)
		if vec == nil {
			return fmt.Errorf("lancedb: document %q vector is empty or zero", id)
		}
		md := map[string]any(nil)
		if len(doc.Metadata) > 0 {
			md = make(map[string]any, len(doc.Metadata))
			for k, v := range doc.Metadata {
				md[k] = v
			}
		}
		b.docs[id] = VectorDocument{ID: id, Text: doc.Text, Vector: vec, Metadata: md}
	}
	return b.saveLocked()
}

// Delete removes vector documents by ID.
func (b *EmbeddedBackend) Delete(ctx context.Context, ids []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range ids {
		delete(b.docs, strings.TrimSpace(id))
	}
	return b.saveLocked()
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
		return errors.New("lancedb: backend is closed")
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
	Docs []VectorDocument `json:"docs"`
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
		return fmt.Errorf("lancedb: parse %s: %w", b.path, err)
	}
	for _, doc := range state.Docs {
		if strings.TrimSpace(doc.ID) == "" || strings.TrimSpace(doc.Text) == "" {
			continue
		}
		vec := normalize(doc.Vector)
		if vec == nil {
			continue
		}
		doc.Vector = vec
		b.docs[doc.ID] = cloneDoc(doc)
	}
	return nil
}

func (b *EmbeddedBackend) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
		return err
	}
	docs := make([]VectorDocument, 0, len(b.docs))
	for _, doc := range b.docs {
		docs = append(docs, cloneDoc(doc))
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	raw, err := json.MarshalIndent(diskState{Docs: docs}, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", b.path, os.Getpid())
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}

func cloneDoc(doc VectorDocument) VectorDocument {
	out := VectorDocument{ID: doc.ID, Text: doc.Text}
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
