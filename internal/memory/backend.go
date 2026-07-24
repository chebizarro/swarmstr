// Package memory provides the memory backend abstraction and registry.
//
// A Backend is a pluggable store for conversation memories.  The daemon
// selects a backend at startup based on the config Extra["memory"]["backend"]
// field (default: "memory").
//
// Built-in backends:
//   - "memory"   – in-process JSON inverted index (default, zero config)
//   - "json-fts" – alias for "memory" (same implementation, different name)
//   - "lancedb"  – local JSON cosine vector store (NOT a real LanceDB
//     integration; the name is retained only for config compatibility)
//
// Third-party backends can register themselves via RegisterBackend before
// the daemon initialises its index.
package memory

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"metiq/internal/memory/lancedb"
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
	// ListByType returns entries whose Type exactly matches, newest-first.
	ListByType(memType string, limit int) []IndexedMemory
	// ListByTaskID returns entries linked to the given task, newest-first.
	ListByTaskID(taskID string, limit int) []IndexedMemory
	// Close releases any resources held by the backend.
	Close() error
}

// BackendStatus reports the health of a single memory backend.
//
// Semantics:
//   - Available means the backend is currently usable for reads/writes.
//     When Degraded is true, Available is false (backend is not in use).
//   - Degraded means the backend has entered a failure/backoff state and
//     the system has fallen back to an alternative (e.g., JSON-FTS index).
//   - Both false: healthy and in use.
//   - Degraded=true, Available=false: backend is down, fallback is active.
type BackendStatus struct {
	Name                string `json:"name"`
	Available           bool   `json:"available"`
	Degraded            bool   `json:"degraded,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	LastFailureUnix     int64  `json:"last_failure_unix,omitempty"`
	NextRetryUnix       int64  `json:"next_retry_unix,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
}

type StoreStatus struct {
	Kind           string                `json:"kind"`
	FallbackActive bool                  `json:"fallback_active,omitempty"`
	Primary        BackendStatus         `json:"primary"`
	Fallback       *BackendStatus        `json:"fallback,omitempty"`
	Recovery       *SQLiteRecoveryReport `json:"recovery,omitempty"`
	FTS            *FTSHealth            `json:"fts,omitempty"`
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
	// The "lancedb" backend is a LOCAL JSON cosine vector store, not a real
	// LanceDB integration. "local-vector" is the honest, preferred name; the
	// "lancedb" key is kept as a backward-compatible alias.
	localVectorFactory := func(path string) (Backend, error) {
		return OpenLanceDBBackend(path)
	}
	RegisterBackend("lancedb", localVectorFactory)
	RegisterBackend("local-vector", localVectorFactory)
}

// IndexBackend adapts the existing *Index to the Backend interface.
type IndexBackend struct {
	idx *Index
}

func (b *IndexBackend) Add(doc state.MemoryDoc) { b.idx.Add(doc) }
func (b *IndexBackend) AddWithContext(ctx context.Context, doc state.MemoryDoc) {
	b.idx.Add(doc)
}
func (b *IndexBackend) BackendStatus() BackendStatus {
	return BackendStatus{Name: "json-fts", Available: true}
}
func (b *IndexBackend) MemoryStatus() StoreStatus {
	primary := b.BackendStatus()
	return StoreStatus{Kind: "index", Primary: primary}
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
func (b *IndexBackend) ListByType(memType string, limit int) []IndexedMemory {
	return b.idx.ListByType(memType, limit)
}
func (b *IndexBackend) ListByTaskID(taskID string, limit int) []IndexedMemory {
	return b.idx.ListByTaskID(taskID, limit)
}
func (b *IndexBackend) Save() error  { return b.idx.Save() }
func (b *IndexBackend) Close() error { return b.idx.Save() }

// LanceDBBackend adapts the local JSON cosine vector store (see the lancedb
// package — a dependency-free local store, NOT a real LanceDB integration) to
// the memory Backend interface. It uses the memory embedding provider contract
// so stored documents carry vectors and searches embed the query text.
//
// The embedding provider is resolved from configuration (see
// ResolveMemoryEmbeddingProvider): the default is a real, semantic provider.
// The non-semantic StaticMemoryEmbeddingProvider is opt-in only.
type LanceDBBackend struct {
	store    lancedb.Backend
	provider MemoryEmbeddingProvider
}

// OpenLanceDBBackend opens the local JSON cosine vector memory backend using the
// embedding provider selected by the environment. By default this is a real,
// semantic provider (Ollama); the deterministic non-semantic
// StaticMemoryEmbeddingProvider is used only when explicitly opted in via
// METIQ_MEMORY_EMBEDDINGS=static, which logs a startup warning. This prevents
// fake byte-bucket "embeddings" from silently becoming the production default.
func OpenLanceDBBackend(path string) (*LanceDBBackend, error) {
	provider, err := ResolveMemoryEmbeddingProvider(os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("local-vector memory backend: %w", err)
	}
	log.Printf("memory: local-vector backend (local JSON cosine store, NOT LanceDB) using embedding provider %q model %q",
		provider.EmbeddingProvider().ID, provider.EmbeddingProvider().Model)
	return OpenLanceDBBackendWithProvider(path, provider)
}

// OpenLanceDBBackendWithProvider opens the local JSON cosine vector backend with
// an explicit embedding provider. It is used for injection in tests and by
// callers that construct their own provider. A nil provider is rejected so the
// backend can never run with fake/absent embeddings.
func OpenLanceDBBackendWithProvider(path string, provider MemoryEmbeddingProvider) (*LanceDBBackend, error) {
	if provider == nil {
		return nil, fmt.Errorf("local-vector memory backend: embedding provider is required")
	}
	store, err := lancedb.New(lancedb.Options{Path: path})
	if err != nil {
		return nil, err
	}
	return &LanceDBBackend{store: store, provider: provider}, nil
}

func (b *LanceDBBackend) Add(doc state.MemoryDoc) { b.AddWithContext(context.Background(), doc) }

func (b *LanceDBBackend) AddWithContext(ctx context.Context, doc state.MemoryDoc) {
	if strings.TrimSpace(doc.MemoryID) == "" || strings.TrimSpace(doc.Text) == "" {
		return
	}
	vec, err := b.provider.Embed(ctx, doc.Text)
	if err != nil {
		return
	}
	_ = b.store.Upsert(ctx, []lancedb.VectorDocument{{
		ID:       doc.MemoryID,
		Text:     doc.Text,
		Vector:   vec,
		Metadata: memoryDocMetadata(doc),
	}})
}

func (b *LanceDBBackend) Search(query string, limit int) []IndexedMemory {
	return b.SearchWithContext(context.Background(), query, limit)
}

func (b *LanceDBBackend) SearchWithContext(ctx context.Context, query string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 20
	}
	vec, err := b.provider.Embed(ctx, query)
	if err != nil {
		return nil
	}
	docs, err := b.store.Search(ctx, vec, limit)
	if err != nil {
		return nil
	}
	return vectorDocsToIndexed(docs)
}

func (b *LanceDBBackend) SearchSession(sessionID, query string, limit int) []IndexedMemory {
	return b.SearchSessionWithContext(context.Background(), sessionID, query, limit)
}

func (b *LanceDBBackend) SearchSessionWithContext(ctx context.Context, sessionID, query string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 8
	}
	results := b.SearchWithContext(ctx, query, limit*4)
	out := make([]IndexedMemory, 0, limit)
	for _, result := range results {
		if result.SessionID == sessionID {
			out = append(out, result)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func (b *LanceDBBackend) ListSession(sessionID string, limit int) []IndexedMemory {
	return b.filter(func(m IndexedMemory) bool { return m.SessionID == sessionID }, limit)
}
func (b *LanceDBBackend) ListByTopic(topic string, limit int) []IndexedMemory {
	return b.filter(func(m IndexedMemory) bool { return m.Topic == topic }, limit)
}
func (b *LanceDBBackend) ListByType(memType string, limit int) []IndexedMemory {
	return b.filter(func(m IndexedMemory) bool { return m.Type == memType }, limit)
}
func (b *LanceDBBackend) ListByTaskID(taskID string, limit int) []IndexedMemory {
	return b.filter(func(m IndexedMemory) bool { return m.TaskID == taskID }, limit)
}

func (b *LanceDBBackend) Count() int {
	docs, err := b.store.All(context.Background())
	if err != nil {
		return 0
	}
	return len(docs)
}

func (b *LanceDBBackend) SessionCount() int {
	sessions := map[string]struct{}{}
	for _, m := range vectorDocsToIndexed(b.allDocs()) {
		if m.SessionID != "" {
			sessions[m.SessionID] = struct{}{}
		}
	}
	return len(sessions)
}

func (b *LanceDBBackend) Compact(maxEntries int) int {
	if maxEntries < 0 {
		maxEntries = 0
	}
	memories := vectorDocsToIndexed(b.allDocs())
	if len(memories) <= maxEntries {
		return 0
	}
	for i := 1; i < len(memories); i++ {
		for j := i; j > 0 && memories[j].Unix < memories[j-1].Unix; j-- {
			memories[j], memories[j-1] = memories[j-1], memories[j]
		}
	}
	remove := len(memories) - maxEntries
	ids := make([]string, 0, remove)
	for i := 0; i < remove; i++ {
		ids = append(ids, memories[i].MemoryID)
	}
	if err := b.store.Delete(context.Background(), ids); err != nil {
		return 0
	}
	return remove
}

func (b *LanceDBBackend) Save() error { return b.store.Health(context.Background()) }

func (b *LanceDBBackend) Store(sessionID, text string, tags []string) string {
	id := GenerateMemoryID()
	b.Add(state.MemoryDoc{MemoryID: id, SessionID: sessionID, Text: text, Keywords: append([]string(nil), tags...)})
	return id
}

func (b *LanceDBBackend) Delete(id string) bool {
	before := b.Count()
	if err := b.store.Delete(context.Background(), []string{id}); err != nil {
		return false
	}
	return b.Count() < before
}

func (b *LanceDBBackend) Close() error {
	if closer, ok := b.store.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (b *LanceDBBackend) BackendStatus() BackendStatus {
	if err := b.store.Health(context.Background()); err != nil {
		return BackendStatus{Name: "local-vector", Available: false, LastError: err.Error()}
	}
	return BackendStatus{Name: "local-vector", Available: true}
}

func (b *LanceDBBackend) MemoryStatus() StoreStatus {
	primary := b.BackendStatus()
	return StoreStatus{Kind: "local-vector", Primary: primary}
}

func (b *LanceDBBackend) filter(keep func(IndexedMemory) bool, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 20
	}
	out := make([]IndexedMemory, 0, limit)
	for _, m := range vectorDocsToIndexed(b.allDocs()) {
		if keep(m) {
			out = append(out, m)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func (b *LanceDBBackend) allDocs() []lancedb.VectorDocument {
	docs, err := b.store.All(context.Background())
	if err != nil {
		return nil
	}
	return docs
}

func memoryDocMetadata(doc state.MemoryDoc) map[string]any {
	return map[string]any{
		"session_id":        doc.SessionID,
		"role":              doc.Role,
		"topic":             doc.Topic,
		"keywords":          append([]string(nil), doc.Keywords...),
		"unix":              doc.Unix,
		"type":              doc.Type,
		"goal_id":           doc.GoalID,
		"task_id":           doc.TaskID,
		"run_id":            doc.RunID,
		"episode_kind":      doc.EpisodeKind,
		"confidence":        doc.Confidence,
		"source":            doc.Source,
		"reviewed_at":       doc.ReviewedAt,
		"reviewed_by":       doc.ReviewedBy,
		"expires_at":        doc.ExpiresAt,
		"mem_status":        doc.MemStatus,
		"superseded_by":     doc.SupersededBy,
		"invalidated_at":    doc.InvalidatedAt,
		"invalidated_by":    doc.InvalidatedBy,
		"invalidate_reason": doc.InvalidateReason,
	}
}

func vectorDocsToIndexed(docs []lancedb.VectorDocument) []IndexedMemory {
	out := make([]IndexedMemory, 0, len(docs))
	for _, doc := range docs {
		m := IndexedMemory{MemoryID: doc.ID, Text: doc.Text}
		if md := doc.Metadata; md != nil {
			m.SessionID = stringMeta(md, "session_id")
			m.Role = stringMeta(md, "role")
			m.Topic = stringMeta(md, "topic")
			m.Keywords = stringSliceMeta(md, "keywords")
			m.Unix = int64Meta(md, "unix")
			m.Type = stringMeta(md, "type")
			m.GoalID = stringMeta(md, "goal_id")
			m.TaskID = stringMeta(md, "task_id")
			m.RunID = stringMeta(md, "run_id")
			m.EpisodeKind = stringMeta(md, "episode_kind")
			m.Confidence = float64Meta(md, "confidence")
			m.Source = stringMeta(md, "source")
			m.ReviewedAt = int64Meta(md, "reviewed_at")
			m.ReviewedBy = stringMeta(md, "reviewed_by")
			m.ExpiresAt = int64Meta(md, "expires_at")
			m.MemStatus = stringMeta(md, "mem_status")
			m.SupersededBy = stringMeta(md, "superseded_by")
			m.InvalidatedAt = int64Meta(md, "invalidated_at")
			m.InvalidatedBy = stringMeta(md, "invalidated_by")
			m.InvalidateReason = stringMeta(md, "invalidate_reason")
		}
		out = append(out, m)
	}
	return out
}

func stringMeta(md map[string]any, key string) string {
	if s, ok := md[key].(string); ok {
		return s
	}
	return ""
}

func int64Meta(md map[string]any, key string) int64 {
	switch v := md[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func float64Meta(md map[string]any, key string) float64 {
	switch v := md[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func stringSliceMeta(md map[string]any, key string) []string {
	switch v := md[key].(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

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
