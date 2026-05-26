// Package lancedb is a placeholder for a future LanceDB vector backend.
package lancedb

import "context"

// VectorDocument is the minimal shape expected by a LanceDB-backed vector
// memory store. TODO: adapt this to internal/memory.IndexedMemory and the
// embedding provider registry when the backend is implemented.
type VectorDocument struct {
	ID       string
	Text     string
	Vector   []float32
	Metadata map[string]any
}

// Backend is the future LanceDB vector-store integration seam. TODO: implement
// collection management, upsert/delete, nearest-neighbor search, and health
// checks while preserving sqlite-vec as the default embedded backend.
type Backend interface {
	Upsert(ctx context.Context, docs []VectorDocument) error
	Delete(ctx context.Context, ids []string) error
	Search(ctx context.Context, vector []float32, limit int) ([]VectorDocument, error)
	Health(ctx context.Context) error
}
