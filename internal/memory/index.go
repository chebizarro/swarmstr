package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

type IndexedMemory struct {
	MemoryID  string   `json:"memory_id"`
	SessionID string   `json:"session_id,omitempty"`
	Role      string   `json:"role,omitempty"`
	Topic     string   `json:"topic,omitempty"`
	Text      string   `json:"text"`
	Keywords  []string `json:"keywords,omitempty"`
	Unix      int64    `json:"unix"`

	// Type + episodic correlation fields.
	Type        string `json:"type,omitempty"`
	GoalID      string `json:"goal_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	EpisodeKind string `json:"episode_kind,omitempty"`

	// Trust & provenance metadata.
	Confidence        float64 `json:"confidence,omitempty"`
	Source            string  `json:"source,omitempty"`
	OriginClass       string  `json:"origin_class,omitempty"`
	SessionKind       string  `json:"session_kind,omitempty"`
	ExternalToolTaint bool    `json:"external_tool_taint,omitempty"`
	NetworkTaint      bool    `json:"network_taint,omitempty"`
	RecalledContent   bool    `json:"recalled_content,omitempty"`
	ReviewedAt        int64   `json:"reviewed_at,omitempty"`
	ReviewedBy        string  `json:"reviewed_by,omitempty"`
	ExpiresAt         int64   `json:"expires_at,omitempty"`

	// Invalidation state.
	MemStatus        string `json:"mem_status,omitempty"`
	SupersededBy     string `json:"superseded_by,omitempty"`
	InvalidatedAt    int64  `json:"invalidated_at,omitempty"`
	InvalidatedBy    string `json:"invalidated_by,omitempty"`
	InvalidateReason string `json:"invalidate_reason,omitempty"`
}

type Index struct {
	mu                  sync.RWMutex
	saveMu              sync.Mutex
	path                string
	generation          uint64
	persistedGeneration uint64
	docs                map[string]IndexedMemory
	byToken             map[string]map[string]struct{}
	cache               map[string][]IndexedMemory
	order               []string
	cacheCap            int
}

type diskIndex struct {
	Generation uint64          `json:"generation"`
	Docs       []IndexedMemory `json:"docs"`
}

// CompactionBatch is the immutable generation-scoped set selected for removal.
// Flushers should treat Generation+MemoryID as an idempotency key.
type CompactionBatch struct {
	Generation uint64          `json:"generation"`
	Entries    []IndexedMemory `json:"entries"`
}

// PreCompactionFlusher persists a compaction batch before the index publishes
// the corresponding deletion. Implementations must be safe to retry.
type PreCompactionFlusher interface {
	FlushBeforeCompaction(context.Context, CompactionBatch) error
}

func DefaultIndexPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".metiq", "memory-index.json"), nil
}

func OpenIndex(path string) (*Index, error) {
	if path == "" {
		defaultPath, err := DefaultIndexPath()
		if err != nil {
			return nil, err
		}
		path = defaultPath
	}
	idx := &Index{
		path:     path,
		docs:     map[string]IndexedMemory{},
		byToken:  map[string]map[string]struct{}{},
		cache:    map[string][]IndexedMemory{},
		cacheCap: 256,
	}
	if err := idx.load(); err != nil {
		return nil, err
	}
	return idx, nil
}

// GenerateMemoryID generates a random 8-byte hex string for use as a MemoryID.
func GenerateMemoryID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("mem-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// Store indexes the given text as a new memory entry and returns the
// generated MemoryID.  It is a convenience wrapper around Add that generates
// a unique ID and sets the current Unix timestamp.
func (i *Index) Store(sessionID, text string, tags []string) string {
	id := GenerateMemoryID()
	i.Add(state.MemoryDoc{
		MemoryID:    id,
		SessionID:   sessionID,
		Text:        text,
		Keywords:    append([]string(nil), tags...),
		Unix:        time.Now().Unix(),
		OriginClass: string(MemoryOriginAgent),
		SessionKind: string(InferMemorySessionKind(sessionID)),
	})
	return id
}

// Delete removes the memory entry with the given ID.
// Returns true if the entry existed, false if it was not found.
func (i *Index) Delete(id string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, ok := i.docs[id]; !ok {
		return false
	}
	delete(i.docs, id)
	i.generation++
	i.rebuildTokenMapLocked()
	i.clearCacheLocked()
	return true
}

func (i *Index) Add(doc state.MemoryDoc) {
	var err error
	doc, err = NormalizeMemoryDocProvenance(doc)
	if err != nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()

	if strings.TrimSpace(doc.MemoryID) == "" || strings.TrimSpace(doc.Text) == "" {
		return
	}
	im := IndexedMemory{
		MemoryID:          doc.MemoryID,
		SessionID:         doc.SessionID,
		Role:              doc.Role,
		Topic:             doc.Topic,
		Text:              doc.Text,
		Keywords:          append([]string{}, doc.Keywords...),
		Unix:              doc.Unix,
		Type:              doc.Type,
		GoalID:            doc.GoalID,
		TaskID:            doc.TaskID,
		RunID:             doc.RunID,
		EpisodeKind:       doc.EpisodeKind,
		Confidence:        doc.Confidence,
		Source:            doc.Source,
		OriginClass:       doc.OriginClass,
		SessionKind:       doc.SessionKind,
		ExternalToolTaint: doc.ExternalToolTaint,
		NetworkTaint:      doc.NetworkTaint,
		RecalledContent:   doc.RecalledContent,
		ReviewedAt:        doc.ReviewedAt,
		ReviewedBy:        doc.ReviewedBy,
		ExpiresAt:         doc.ExpiresAt,
		MemStatus:         doc.MemStatus,
		SupersededBy:      doc.SupersededBy,
		InvalidatedAt:     doc.InvalidatedAt,
		InvalidatedBy:     doc.InvalidatedBy,
		InvalidateReason:  doc.InvalidateReason,
	}
	i.docs[im.MemoryID] = im
	i.generation++
	i.rebuildTokenMapLocked()
	i.clearCacheLocked()
}

func (i *Index) Search(query string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 20
	}
	tokens := queryTokens(query)
	if len(tokens) == 0 {
		return nil
	}
	cacheKey := searchCacheKey("", strings.Join(tokens, " "), limit)

	i.mu.RLock()
	if cached, ok := i.getCachedLocked(cacheKey); ok {
		i.mu.RUnlock()
		return cloneMemories(cached)
	}

	scores := map[string]int{}
	for _, tk := range tokens {
		ids := i.byToken[tk]
		for id := range ids {
			scores[id]++
		}
	}

	results := make([]IndexedMemory, 0, len(scores))
	for id := range scores {
		if doc, ok := i.docs[id]; ok {
			results = append(results, doc)
		}
	}
	i.mu.RUnlock()

	sort.Slice(results, func(a, b int) bool {
		aScore := scores[results[a].MemoryID]
		bScore := scores[results[b].MemoryID]
		if aScore == bScore {
			return results[a].Unix > results[b].Unix
		}
		return aScore > bScore
	})
	if len(results) > limit {
		results = results[:limit]
	}

	i.mu.Lock()
	i.setCachedLocked(cacheKey, results)
	i.mu.Unlock()
	return cloneMemories(results)
}

// ListByTopic returns all entries whose Topic exactly matches the given topic,
// sorted newest-first, up to limit results.
func (i *Index) ListByTopic(topic string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 100
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	results := make([]IndexedMemory, 0, 8)
	for _, doc := range i.docs {
		if doc.Topic == topic {
			results = append(results, doc)
		}
	}
	sort.Slice(results, func(a, b int) bool { return results[a].Unix > results[b].Unix })
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// ListByType returns all entries whose Type matches, newest-first, up to limit.
func (i *Index) ListByType(memType string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 100
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	results := make([]IndexedMemory, 0, 8)
	for _, doc := range i.docs {
		if doc.Type == memType {
			results = append(results, doc)
		}
	}
	sort.Slice(results, func(a, b int) bool { return results[a].Unix > results[b].Unix })
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// ListByTaskID returns all entries whose TaskID matches, newest-first, up to limit.
func (i *Index) ListByTaskID(taskID string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 100
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	results := make([]IndexedMemory, 0, 8)
	for _, doc := range i.docs {
		if doc.TaskID == taskID {
			results = append(results, doc)
		}
	}
	sort.Slice(results, func(a, b int) bool { return results[a].Unix > results[b].Unix })
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// Compact removes the oldest entries (lowest Unix timestamp) to keep the total
// count at or below maxEntries. It preserves the historical API; callers that
// require a durable pre-compaction handoff should use CompactWithFlush.
func (i *Index) Compact(maxEntries int) int {
	removed, _ := i.CompactWithFlush(context.Background(), maxEntries, nil)
	return removed
}

// CompactWithFlush publishes the exact victim batch to flusher before removing
// it. If concurrent mutation changes the generation, selection and flush retry;
// deletion is only published for the generation that was flushed.
func (i *Index) CompactWithFlush(ctx context.Context, maxEntries int, flusher PreCompactionFlusher) (int, error) {
	if maxEntries < 0 {
		maxEntries = 0
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		i.mu.RLock()
		if len(i.docs) <= maxEntries {
			i.mu.RUnlock()
			return 0, nil
		}
		generation := i.generation
		entries := make([]IndexedMemory, 0, len(i.docs))
		for _, d := range i.docs {
			entries = append(entries, cloneMemory(d))
		}
		i.mu.RUnlock()
		sort.Slice(entries, func(a, b int) bool {
			if entries[a].Unix == entries[b].Unix {
				return entries[a].MemoryID < entries[b].MemoryID
			}
			return entries[a].Unix < entries[b].Unix
		})
		victims := entries[:len(entries)-maxEntries]
		batch := CompactionBatch{Generation: generation, Entries: cloneMemories(victims)}
		if flusher != nil {
			if err := flusher.FlushBeforeCompaction(ctx, batch); err != nil {
				return 0, fmt.Errorf("flush pre-compaction generation %d: %w", generation, err)
			}
		}

		i.mu.Lock()
		if i.generation != generation {
			i.mu.Unlock()
			continue
		}
		for _, victim := range victims {
			delete(i.docs, victim.MemoryID)
		}
		i.generation++
		i.rebuildTokenMapLocked()
		i.clearCacheLocked()
		i.mu.Unlock()
		return len(victims), nil
	}
}

// Count returns the total number of indexed memory entries.
func (i *Index) Count() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.docs)
}

func (i *Index) MemoryStatus() StoreStatus {
	return StoreStatus{
		Kind:    "index",
		Primary: BackendStatus{Name: "json-fts", Available: true},
	}
}

// SessionCount returns the number of distinct session IDs in the index.
func (i *Index) SessionCount() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	sessions := map[string]struct{}{}
	for _, doc := range i.docs {
		if doc.SessionID != "" {
			sessions[doc.SessionID] = struct{}{}
		}
	}
	return len(sessions)
}

func (i *Index) ListSession(sessionID string, limit int) []IndexedMemory {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	i.mu.RLock()
	defer i.mu.RUnlock()

	results := make([]IndexedMemory, 0, limit)
	for _, doc := range i.docs {
		if doc.SessionID == sessionID {
			results = append(results, doc)
		}
	}
	sort.Slice(results, func(a, b int) bool { return results[a].Unix > results[b].Unix })
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

func (i *Index) SearchSession(sessionID, query string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 8
	}
	cacheKey := searchCacheKey(sessionID, query, limit)
	i.mu.RLock()
	if cached, ok := i.getCachedLocked(cacheKey); ok {
		i.mu.RUnlock()
		return cloneMemories(cached)
	}
	i.mu.RUnlock()

	candidates := i.Search(query, limit*4)
	out := make([]IndexedMemory, 0, limit)
	seen := map[string]struct{}{}
	for _, doc := range candidates {
		if doc.SessionID != sessionID {
			continue
		}
		if _, ok := seen[doc.MemoryID]; ok {
			continue
		}
		seen[doc.MemoryID] = struct{}{}
		out = append(out, doc)
		if len(out) >= limit {
			i.mu.Lock()
			i.setCachedLocked(cacheKey, out)
			i.mu.Unlock()
			return cloneMemories(out)
		}
	}
	for _, doc := range i.ListSession(sessionID, limit*2) {
		if _, ok := seen[doc.MemoryID]; ok {
			continue
		}
		seen[doc.MemoryID] = struct{}{}
		out = append(out, doc)
		if len(out) >= limit {
			break
		}
	}
	i.mu.Lock()
	i.setCachedLocked(cacheKey, out)
	i.mu.Unlock()
	return cloneMemories(out)
}

func (i *Index) Save() error {
	i.saveMu.Lock()
	defer i.saveMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(i.path), 0o700); err != nil {
		return err
	}

	// Optimistic snapshot/write avoids blocking ordinary index mutations on I/O.
	// Rename is generation-checked under the write lock so a stale snapshot can
	// never replace a newer generation on disk.
	for attempt := 0; attempt < 5; attempt++ {
		i.mu.RLock()
		generation := i.generation
		docs := snapshotDocs(i.docs)
		i.mu.RUnlock()
		tmp, err := writeIndexTemp(i.path, diskIndex{Generation: generation, Docs: docs})
		if err != nil {
			return err
		}
		i.mu.Lock()
		if i.generation != generation {
			i.mu.Unlock()
			_ = os.Remove(tmp)
			continue
		}
		err = os.Rename(tmp, i.path)
		if err == nil {
			i.persistedGeneration = generation
		} else {
			_ = os.Remove(tmp)
		}
		i.mu.Unlock()
		return err
	}

	// Under sustained mutation, take a bounded blocking snapshot so progress is
	// guaranteed without weakening the publication invariant.
	i.mu.Lock()
	defer i.mu.Unlock()
	generation := i.generation
	tmp, err := writeIndexTemp(i.path, diskIndex{Generation: generation, Docs: snapshotDocs(i.docs)})
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, i.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	i.persistedGeneration = generation
	return nil
}

func writeIndexTemp(path string, disk diskIndex) (string, error) {
	sort.Slice(disk.Docs, func(a, b int) bool { return disk.Docs[a].Unix > disk.Docs[b].Unix })
	raw, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", err
	}
	tmp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return "", err
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		cleanup()
		return "", err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

func snapshotDocs(docs map[string]IndexedMemory) []IndexedMemory {
	out := make([]IndexedMemory, 0, len(docs))
	for _, doc := range docs {
		out = append(out, cloneMemory(doc))
	}
	return out
}

func (i *Index) load() error {
	raw, err := os.ReadFile(i.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var disk diskIndex
	if err := json.Unmarshal(raw, &disk); err != nil {
		return fmt.Errorf("parse index %s: %w", i.path, err)
	}
	for _, doc := range disk.Docs {
		i.docs[doc.MemoryID] = cloneMemory(doc)
	}
	i.generation = disk.Generation
	i.persistedGeneration = disk.Generation
	i.rebuildTokenMapLocked()
	i.clearCacheLocked()
	return nil
}

func (i *Index) rebuildTokenMapLocked() {
	i.byToken = map[string]map[string]struct{}{}
	for id, doc := range i.docs {
		for _, tk := range queryTokens(strings.Join(append(doc.Keywords, doc.Topic, doc.Text), " ")) {
			if i.byToken[tk] == nil {
				i.byToken[tk] = map[string]struct{}{}
			}
			i.byToken[tk][id] = struct{}{}
		}
	}
}

func searchCacheKey(sessionID, query string, limit int) string {
	query = strings.TrimSpace(strings.ToLower(query))
	return fmt.Sprintf("%s|%d|%s", sessionID, limit, query)
}

func cloneMemories(in []IndexedMemory) []IndexedMemory {
	if len(in) == 0 {
		return nil
	}
	out := make([]IndexedMemory, len(in))
	for idx := range in {
		out[idx] = cloneMemory(in[idx])
	}
	return out
}

func cloneMemory(in IndexedMemory) IndexedMemory {
	out := in
	out.Keywords = append([]string(nil), in.Keywords...)
	return out
}

func (i *Index) getCachedLocked(key string) ([]IndexedMemory, bool) {
	if i.cache == nil {
		return nil, false
	}
	v, ok := i.cache[key]
	return v, ok
}

func (i *Index) setCachedLocked(key string, value []IndexedMemory) {
	if i.cacheCap <= 0 {
		return
	}
	if i.cache == nil {
		i.cache = map[string][]IndexedMemory{}
	}
	if _, exists := i.cache[key]; !exists {
		i.order = append(i.order, key)
	}
	i.cache[key] = cloneMemories(value)
	if len(i.order) <= i.cacheCap {
		return
	}
	victim := i.order[0]
	i.order = i.order[1:]
	delete(i.cache, victim)
}

func (i *Index) clearCacheLocked() {
	i.cache = map[string][]IndexedMemory{}
	i.order = nil
}

func queryTokens(s string) []string {
	parts := splitter.Split(strings.ToLower(strings.TrimSpace(s)), -1)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		if _, stop := stopwords[p]; stop {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
