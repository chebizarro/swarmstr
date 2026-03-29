// Package memory provides the memory backend abstraction and registry.
//
// A Backend is a pluggable store for conversation memories.  The daemon
// selects a backend at startup based on the config Extra["memory"]["backend"]
// field (default: "memory").
//
// Built-in backends:
//   - "memory"   – in-process JSON inverted index (default, zero config)
//   - "json-fts" – alias for "memory" (same implementation, different name)
//
// Third-party backends can register themselves via RegisterBackend before
// the daemon initialises its index.
package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"metiq/internal/store/state"
)

// Backend is the interface all memory store implementations must satisfy.
type Backend interface {
	// Add indexes a new memory document.
	Add(doc state.MemoryDoc)
	// Search performs a full-text search and returns up to limit results.
	Search(query string, limit int) []IndexedMemory
	// SearchSession performs a session-scoped full-text search.
	SearchSession(sessionID, query string, limit int) []IndexedMemory
	// ListSession returns recent entries for a specific session.
	ListSession(sessionID string, limit int) []IndexedMemory
	// Count returns the total number of stored memory entries.
	Count() int
	// SessionCount returns the number of distinct session IDs.
	SessionCount() int
	// Compact removes old entries to keep the total below maxEntries.
	// It removes the oldest entries first and returns the number removed.
	Compact(maxEntries int) int
	// Save persists the backend's state to disk (if applicable).
	// Implementations that are purely in-memory may return nil.
	Save() error
	// Store adds a new memory entry with the given text and optional tags,
	// returning the generated MemoryID.
	Store(sessionID, text string, tags []string) string
	// Delete removes the memory entry with the given ID.
	// Returns true if it existed, false if not found.
	Delete(id string) bool
	// ListByTopic returns entries whose Topic exactly matches the given topic,
	// newest-first, up to limit results.  Used to surface pinned agent knowledge.
	ListByTopic(topic string, limit int) []IndexedMemory
	// Close releases any resources held by the backend.
	Close() error
}

// BackendFactory is a function that opens a Backend at the given path.
// path may be "" to indicate the default platform location.
type BackendFactory func(path string) (Backend, error)

var (
	backendMu       sync.RWMutex
	backendRegistry = map[string]BackendFactory{}
)

// RegisterBackend registers a BackendFactory under the given name.
// It panics if name is empty or already registered.
func RegisterBackend(name string, factory BackendFactory) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		panic("memory: backend name must not be empty")
	}
	backendMu.Lock()
	defer backendMu.Unlock()
	if _, exists := backendRegistry[name]; exists {
		panic("memory: backend already registered: " + name)
	}
	backendRegistry[name] = factory
}

// ListBackends returns the sorted list of registered backend names.
func ListBackends() []string {
	backendMu.RLock()
	defer backendMu.RUnlock()
	names := make([]string, 0, len(backendRegistry))
	for k := range backendRegistry {
		names = append(names, k)
	}
	return names
}

// OpenBackend opens the named backend at path.
// If name is "" or "memory" or "json-fts", the built-in JSON index is used.
func OpenBackend(name, path string) (Backend, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = "memory"
	}
	backendMu.RLock()
	factory, ok := backendRegistry[name]
	backendMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("memory: unknown backend %q (registered: %v)", name, ListBackends())
	}
	return factory(path)
}

func init() {
	// Register the built-in JSON inverted-index backend under both canonical names.
	factory := func(path string) (Backend, error) {
		idx, err := OpenIndex(path)
		if err != nil {
			return nil, err
		}
		return &IndexBackend{idx: idx}, nil
	}
	RegisterBackend("memory", factory)
	RegisterBackend("json-fts", factory)
}

// IndexBackend adapts the existing *Index to the Backend interface.
type IndexBackend struct {
	idx *Index
}

func (b *IndexBackend) Add(doc state.MemoryDoc) { b.idx.Add(doc) }
func (b *IndexBackend) AddWithContext(ctx context.Context, doc state.MemoryDoc) {
	b.idx.Add(doc)
}
func (b *IndexBackend) Search(query string, limit int) []IndexedMemory {
	return b.idx.Search(query, limit)
}
func (b *IndexBackend) SearchWithContext(ctx context.Context, query string, limit int) []IndexedMemory {
	return b.idx.Search(query, limit)
}
func (b *IndexBackend) SearchSession(sid, q string, limit int) []IndexedMemory {
	return b.idx.SearchSession(sid, q, limit)
}
func (b *IndexBackend) SearchSessionWithContext(ctx context.Context, sid, q string, limit int) []IndexedMemory {
	return b.idx.SearchSession(sid, q, limit)
}
func (b *IndexBackend) ListSession(sid string, limit int) []IndexedMemory {
	return b.idx.ListSession(sid, limit)
}
func (b *IndexBackend) Count() int        { return b.idx.Count() }
func (b *IndexBackend) SessionCount() int { return b.idx.SessionCount() }
func (b *IndexBackend) Store(sid, text string, tags []string) string {
	return b.idx.Store(sid, text, tags)
}
func (b *IndexBackend) Delete(id string) bool { return b.idx.Delete(id) }
func (b *IndexBackend) ListByTopic(topic string, limit int) []IndexedMemory {
	return b.idx.ListByTopic(topic, limit)
}
func (b *IndexBackend) Save() error  { return b.idx.Save() }
func (b *IndexBackend) Close() error { return b.idx.Save() }

type contextAdder interface {
	AddWithContext(context.Context, state.MemoryDoc)
}

type contextSearcher interface {
	SearchWithContext(context.Context, string, int) []IndexedMemory
}

type contextSessionSearcher interface {
	SearchSessionWithContext(context.Context, string, string, int) []IndexedMemory
}

func AddDoc(ctx context.Context, store Store, doc state.MemoryDoc) {
	if ctxStore, ok := any(store).(contextAdder); ok {
		ctxStore.AddWithContext(ctx, doc)
		return
	}
	store.Add(doc)
}

func SearchDocs(ctx context.Context, store Store, query string, limit int) []IndexedMemory {
	if ctxStore, ok := any(store).(contextSearcher); ok {
		return ctxStore.SearchWithContext(ctx, query, limit)
	}
	return store.Search(query, limit)
}

func SearchSessionDocs(ctx context.Context, store Store, sessionID, query string, limit int) []IndexedMemory {
	if ctxStore, ok := any(store).(contextSessionSearcher); ok {
		return ctxStore.SearchSessionWithContext(ctx, sessionID, query, limit)
	}
	return store.SearchSession(sessionID, query, limit)
}

// Compact removes the oldest (lowest-Unix) entries to reduce total count.
func (b *IndexBackend) Compact(maxEntries int) int {
	b.idx.mu.Lock()
	defer b.idx.mu.Unlock()
	if len(b.idx.docs) <= maxEntries {
		return 0
	}
	// Collect all docs sorted by Unix ascending (oldest first).
	entries := make([]IndexedMemory, 0, len(b.idx.docs))
	for _, d := range b.idx.docs {
		entries = append(entries, d)
	}
	// Sort ascending (oldest first).
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Unix < entries[j-1].Unix; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
	toRemove := len(entries) - maxEntries
	for i := 0; i < toRemove; i++ {
		delete(b.idx.docs, entries[i].MemoryID)
	}
	b.idx.rebuildTokenMapLocked()
	return toRemove
}
