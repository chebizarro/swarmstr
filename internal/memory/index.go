package memory

import (
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

	"swarmstr/internal/store/state"
)

type IndexedMemory struct {
	MemoryID  string   `json:"memory_id"`
	SessionID string   `json:"session_id,omitempty"`
	Role      string   `json:"role,omitempty"`
	Topic     string   `json:"topic,omitempty"`
	Text      string   `json:"text"`
	Keywords  []string `json:"keywords,omitempty"`
	Unix      int64    `json:"unix"`
}

type Index struct {
	mu      sync.RWMutex
	path    string
	docs    map[string]IndexedMemory
	byToken map[string]map[string]struct{}
}

type diskIndex struct {
	Docs []IndexedMemory `json:"docs"`
}

func DefaultIndexPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".swarmstr", "memory-index.json"), nil
}

func OpenIndex(path string) (*Index, error) {
	if path == "" {
		defaultPath, err := DefaultIndexPath()
		if err != nil {
			return nil, err
		}
		path = defaultPath
	}
	idx := &Index{path: path, docs: map[string]IndexedMemory{}, byToken: map[string]map[string]struct{}{}}
	if err := idx.load(); err != nil {
		return nil, err
	}
	return idx, nil
}

// generateMemoryID generates a random 8-byte hex string for use as a MemoryID.
func generateMemoryID() string {
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
	id := generateMemoryID()
	i.Add(state.MemoryDoc{
		MemoryID:  id,
		SessionID: sessionID,
		Text:      text,
		Keywords:  append([]string(nil), tags...),
		Unix:      time.Now().Unix(),
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
	i.rebuildTokenMapLocked()
	return true
}

func (i *Index) Add(doc state.MemoryDoc) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if strings.TrimSpace(doc.MemoryID) == "" || strings.TrimSpace(doc.Text) == "" {
		return
	}
	im := IndexedMemory{
		MemoryID:  doc.MemoryID,
		SessionID: doc.SessionID,
		Role:      doc.Role,
		Topic:     doc.Topic,
		Text:      doc.Text,
		Keywords:  append([]string{}, doc.Keywords...),
		Unix:      doc.Unix,
	}
	i.docs[im.MemoryID] = im
	i.rebuildTokenMapLocked()
}

func (i *Index) Search(query string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 20
	}
	tokens := queryTokens(query)
	if len(tokens) == 0 {
		return nil
	}

	i.mu.RLock()
	defer i.mu.RUnlock()

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
	return results
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

// Compact removes the oldest entries (lowest Unix timestamp) to keep the total
// count at or below maxEntries.  Returns the number of entries removed.
func (i *Index) Compact(maxEntries int) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.docs) <= maxEntries {
		return 0
	}
	entries := make([]IndexedMemory, 0, len(i.docs))
	for _, d := range i.docs {
		entries = append(entries, d)
	}
	// Sort ascending by age (oldest first).
	sort.Slice(entries, func(a, b int) bool { return entries[a].Unix < entries[b].Unix })
	toRemove := len(entries) - maxEntries
	for idx := 0; idx < toRemove; idx++ {
		delete(i.docs, entries[idx].MemoryID)
	}
	i.rebuildTokenMapLocked()
	return toRemove
}

// Count returns the total number of indexed memory entries.
func (i *Index) Count() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.docs)
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
			return out
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
	return out
}

func (i *Index) Save() error {
	i.mu.RLock()
	docs := make([]IndexedMemory, 0, len(i.docs))
	for _, doc := range i.docs {
		docs = append(docs, doc)
	}
	i.mu.RUnlock()

	sort.Slice(docs, func(a, b int) bool { return docs[a].Unix > docs[b].Unix })

	raw, err := json.MarshalIndent(diskIndex{Docs: docs}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(i.path), 0o700); err != nil {
		return err
	}
	tmp := i.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, i.path)
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
		i.docs[doc.MemoryID] = doc
	}
	i.rebuildTokenMapLocked()
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
