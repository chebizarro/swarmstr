package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

const (
	backendRepairKindAdd    = "add"
	backendRepairKindDelete = "delete"
	maxRepairOpsPerPass     = 32
)

type BackendRepairResult struct {
	Attempted int
	Repaired  int
	Remaining int
}

type backendRepairEntry struct {
	Kind        string          `json:"kind"`
	MemoryID    string          `json:"memory_id"`
	Doc         state.MemoryDoc `json:"doc,omitempty"`
	CreatedUnix int64           `json:"created_unix"`
	UpdatedUnix int64           `json:"updated_unix"`
	Attempts    int             `json:"attempts,omitempty"`
	LastError   string          `json:"last_error,omitempty"`
}

type backendRepairDisk struct {
	Version int                  `json:"version"`
	Entries []backendRepairEntry `json:"entries"`
}

type backendRepairQueue struct {
	mu      sync.Mutex
	path    string
	entries []backendRepairEntry
}

func newBackendRepairQueue(indexPath string) *backendRepairQueue {
	indexPath = strings.TrimSpace(indexPath)
	if indexPath == "" {
		return nil
	}
	q := &backendRepairQueue{path: indexPath + ".backend-repair.json"}
	if err := q.load(); err != nil {
		log.Printf("memory hybrid: load backend repair queue %q: %v", q.path, err)
	}
	return q
}

func (q *backendRepairQueue) load() error {
	if q == nil || q.path == "" {
		return nil
	}
	raw, err := os.ReadFile(q.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var disk backendRepairDisk
	if err := json.Unmarshal(raw, &disk); err != nil {
		return fmt.Errorf("parse repair queue: %w", err)
	}
	q.entries = compactRepairEntries(disk.Entries)
	return nil
}

func (q *backendRepairQueue) EnqueueAdd(doc state.MemoryDoc) error {
	if q == nil || strings.TrimSpace(doc.MemoryID) == "" || strings.TrimSpace(doc.Text) == "" {
		return nil
	}
	now := time.Now().UnixNano()
	entry := backendRepairEntry{
		Kind:        backendRepairKindAdd,
		MemoryID:    doc.MemoryID,
		Doc:         cloneMemoryDoc(doc),
		CreatedUnix: now,
		UpdatedUnix: now,
	}
	return q.upsert(entry)
}

func (q *backendRepairQueue) EnqueueDelete(id string) error {
	id = strings.TrimSpace(id)
	if q == nil || id == "" {
		return nil
	}
	now := time.Now().UnixNano()
	entry := backendRepairEntry{
		Kind:        backendRepairKindDelete,
		MemoryID:    id,
		CreatedUnix: now,
		UpdatedUnix: now,
	}
	return q.upsert(entry)
}

func (q *backendRepairQueue) upsert(entry backendRepairEntry) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.entries {
		if q.entries[i].MemoryID == entry.MemoryID {
			entry.CreatedUnix = q.entries[i].CreatedUnix
			q.entries[i] = entry
			return q.saveLocked()
		}
	}
	q.entries = append(q.entries, entry)
	return q.saveLocked()
}

func (q *backendRepairQueue) snapshot(limit int) []backendRepairEntry {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if limit <= 0 || limit > len(q.entries) {
		limit = len(q.entries)
	}
	out := make([]backendRepairEntry, limit)
	for i := 0; i < limit; i++ {
		out[i] = cloneRepairEntry(q.entries[i])
	}
	return out
}

func (q *backendRepairQueue) removeIfMatch(entry backendRepairEntry) error {
	if q == nil || entry.MemoryID == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.entries {
		current := q.entries[i]
		if current.MemoryID != entry.MemoryID {
			continue
		}
		if current.Kind != entry.Kind || current.CreatedUnix != entry.CreatedUnix || current.UpdatedUnix != entry.UpdatedUnix || current.Attempts != entry.Attempts {
			return nil
		}
		q.entries = append(q.entries[:i], q.entries[i+1:]...)
		return q.saveLocked()
	}
	return nil
}

func (q *backendRepairQueue) markFailure(memoryID string, err error) error {
	if q == nil || memoryID == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.entries {
		if q.entries[i].MemoryID == memoryID {
			q.entries[i].Attempts++
			q.entries[i].UpdatedUnix = time.Now().UnixNano()
			if err != nil {
				q.entries[i].LastError = err.Error()
			}
			return q.saveLocked()
		}
	}
	return nil
}

func (q *backendRepairQueue) len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries)
}

func (q *backendRepairQueue) saveLocked() error {
	if q == nil || q.path == "" {
		return nil
	}
	if len(q.entries) == 0 {
		if err := os.Remove(q.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(q.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(backendRepairDisk{Version: 1, Entries: q.entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", q.path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, q.path)
}

func compactRepairEntries(entries []backendRepairEntry) []backendRepairEntry {
	if len(entries) == 0 {
		return nil
	}
	byID := make(map[string]backendRepairEntry, len(entries))
	order := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry.MemoryID = strings.TrimSpace(entry.MemoryID)
		if entry.MemoryID == "" || (entry.Kind != backendRepairKindAdd && entry.Kind != backendRepairKindDelete) {
			continue
		}
		if _, exists := byID[entry.MemoryID]; !exists {
			order = append(order, entry.MemoryID)
		}
		byID[entry.MemoryID] = cloneRepairEntry(entry)
	}
	out := make([]backendRepairEntry, 0, len(byID))
	for _, id := range order {
		if entry, ok := byID[id]; ok {
			out = append(out, entry)
		}
	}
	return out
}

func cloneRepairEntry(entry backendRepairEntry) backendRepairEntry {
	entry.Doc = cloneMemoryDoc(entry.Doc)
	return entry
}

func cloneMemoryDoc(doc state.MemoryDoc) state.MemoryDoc {
	out := doc
	if doc.Keywords != nil {
		out.Keywords = append([]string(nil), doc.Keywords...)
	}
	if doc.Meta != nil {
		out.Meta = make(map[string]any, len(doc.Meta))
		for k, v := range doc.Meta {
			out.Meta[k] = v
		}
	}
	return out
}

func indexedToMemoryDoc(mem IndexedMemory) state.MemoryDoc {
	return state.MemoryDoc{
		MemoryID:         mem.MemoryID,
		SessionID:        mem.SessionID,
		Role:             mem.Role,
		Topic:            mem.Topic,
		Text:             mem.Text,
		Keywords:         append([]string(nil), mem.Keywords...),
		Unix:             mem.Unix,
		Type:             mem.Type,
		GoalID:           mem.GoalID,
		TaskID:           mem.TaskID,
		RunID:            mem.RunID,
		EpisodeKind:      mem.EpisodeKind,
		Confidence:       mem.Confidence,
		Source:           mem.Source,
		ReviewedAt:       mem.ReviewedAt,
		ReviewedBy:       mem.ReviewedBy,
		ExpiresAt:        mem.ExpiresAt,
		MemStatus:        mem.MemStatus,
		SupersededBy:     mem.SupersededBy,
		InvalidatedAt:    mem.InvalidatedAt,
		InvalidatedBy:    mem.InvalidatedBy,
		InvalidateReason: mem.InvalidateReason,
	}
}

func backendIsDegraded(backend Backend) bool {
	if backend == nil {
		return false
	}
	if reporter, ok := backend.(interface{ BackendStatus() BackendStatus }); ok {
		status := reporter.BackendStatus()
		return status.Degraded || status.LastError != ""
	}
	return false
}

func (h *HybridIndex) enqueueBackendAddRepair(doc state.MemoryDoc) {
	if h == nil || h.repairQueue == nil {
		return
	}
	if err := h.repairQueue.EnqueueAdd(doc); err != nil {
		log.Printf("memory hybrid: enqueue backend add repair for %q: %v", doc.MemoryID, err)
	}
}

func (h *HybridIndex) enqueueBackendDeleteRepair(id string) {
	if h == nil || h.repairQueue == nil {
		return
	}
	if err := h.repairQueue.EnqueueDelete(id); err != nil {
		log.Printf("memory hybrid: enqueue backend delete repair for %q: %v", id, err)
	}
}

func (h *HybridIndex) indexedMemoryByID(id string) (IndexedMemory, bool) {
	if h == nil || h.Index == nil || strings.TrimSpace(id) == "" {
		return IndexedMemory{}, false
	}
	h.Index.mu.RLock()
	defer h.Index.mu.RUnlock()
	mem, ok := h.Index.docs[id]
	return mem, ok
}

// RepairBackendParity drains the durable backend repair queue by replaying
// missed writes/deletes against the vector backend using the JSON index as the
// source of truth. It is safe to call opportunistically after degraded-mode
// recovery; failed entries remain queued for a later pass.
func (h *HybridIndex) RepairBackendParity(ctx context.Context) BackendRepairResult {
	return h.repairBackendParity(ctx, 0)
}

func (h *HybridIndex) repairBackendParity(ctx context.Context, limit int) BackendRepairResult {
	var result BackendRepairResult
	if h == nil || h.backend == nil || h.repairQueue == nil || h.repairQueue.len() == 0 {
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.repairMu.Lock()
	defer h.repairMu.Unlock()
	entries := h.repairQueue.snapshot(limit)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			_ = h.repairQueue.markFailure(entry.MemoryID, err)
			break
		}
		result.Attempted++
		repaired := false
		switch entry.Kind {
		case backendRepairKindAdd:
			mem, exists := h.indexedMemoryByID(entry.MemoryID)
			if !exists {
				repaired = h.repairBackendDelete(ctx, entry.MemoryID)
			} else {
				repaired = h.repairBackendAdd(ctx, indexedToMemoryDoc(mem))
			}
		case backendRepairKindDelete:
			if mem, exists := h.indexedMemoryByID(entry.MemoryID); exists {
				repaired = h.repairBackendAdd(ctx, indexedToMemoryDoc(mem))
			} else {
				repaired = h.repairBackendDelete(ctx, entry.MemoryID)
			}
		default:
			repaired = true
		}
		if repaired {
			if err := h.repairQueue.removeIfMatch(entry); err != nil {
				log.Printf("memory hybrid: remove repaired backend parity entry %q: %v", entry.MemoryID, err)
			}
			result.Repaired++
			continue
		}
		_ = h.repairQueue.markFailure(entry.MemoryID, fmt.Errorf("backend still degraded or operation did not complete"))
		if backendIsDegraded(h.backend) {
			break
		}
	}
	result.Remaining = h.repairQueue.len()
	return result
}

func (h *HybridIndex) repairBackendAdd(ctx context.Context, doc state.MemoryDoc) bool {
	if strings.TrimSpace(doc.MemoryID) == "" || strings.TrimSpace(doc.Text) == "" {
		return true
	}
	if ctxBackend, ok := h.backend.(interface {
		AddWithContext(context.Context, state.MemoryDoc)
	}); ok {
		ctxBackend.AddWithContext(ctx, doc)
	} else {
		h.backend.Add(doc)
	}
	return !backendIsDegraded(h.backend)
}

func (h *HybridIndex) repairBackendDelete(ctx context.Context, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return true
	}
	deleted := h.backend.Delete(id)
	if deleted {
		return true
	}
	// Delete false is success when the backend is healthy because the point is
	// already absent. When the backend reports degraded/failure state, retain the
	// tombstone so stale vector points are deleted after recovery.
	return !backendIsDegraded(h.backend)
}
