package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// FileStore persists task ledger entries to JSON files.
// It follows the same pattern as other metiq state stores.
type FileStore struct {
	mu       sync.RWMutex
	dir      string
	tasks    map[string]*LedgerEntry
	runs     map[string]*RunEntry
	dirty    bool
	lastSave time.Time
}

// StoreDoc is the on-disk representation of the task ledger.
type StoreDoc struct {
	Version   int                      `json:"version"`
	Tasks     map[string]*LedgerEntry  `json:"tasks"`
	Runs      map[string]*RunEntry     `json:"runs"`
	UpdatedAt int64                    `json:"updated_at"`
}

const (
	storeVersion  = 1
	storeFileName = "tasks.json"
)

// NewFileStore creates a new file-based task store.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}

	store := &FileStore{
		dir:   dir,
		tasks: make(map[string]*LedgerEntry),
		runs:  make(map[string]*RunEntry),
	}

	// Load existing data
	if err := store.load(); err != nil {
		// Log warning but continue with empty state
		log.Printf("tasks: failed to load existing store from %s: %v", dir, err)
	}

	return store, nil
}

func (s *FileStore) path() string {
	return filepath.Join(s.dir, storeFileName)
}

func (s *FileStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No existing data
		}
		return fmt.Errorf("read store file: %w", err)
	}

	var doc StoreDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("unmarshal store: %w", err)
	}

	if doc.Tasks != nil {
		s.tasks = doc.Tasks
	}
	if doc.Runs != nil {
		s.runs = doc.Runs
	}

	return nil
}

func (s *FileStore) save() error {
	doc := StoreDoc{
		Version:   storeVersion,
		Tasks:     s.tasks,
		Runs:      s.runs,
		UpdatedAt: time.Now().Unix(),
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal store: %w", err)
	}

	// Write atomically via temp file
	tmpPath := s.path() + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path()); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}

	s.dirty = false
	s.lastSave = time.Now()
	return nil
}

// SaveTask persists a task entry.
func (s *FileStore) SaveTask(ctx context.Context, entry *LedgerEntry) error {
	s.mu.Lock()
	s.tasks[entry.Task.TaskID] = entry
	s.dirty = true
	s.mu.Unlock()

	return s.flush()
}

// LoadTask retrieves a task by ID.
func (s *FileStore) LoadTask(ctx context.Context, taskID string) (*LedgerEntry, error) {
	s.mu.RLock()
	entry, ok := s.tasks[taskID]
	s.mu.RUnlock()

	if !ok {
		return nil, nil
	}
	return entry, nil
}

// ListTasks queries tasks with filters.
func (s *FileStore) ListTasks(ctx context.Context, opts ListTasksOptions) ([]*LedgerEntry, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}

	s.mu.RLock()
	var results []*LedgerEntry
	for _, entry := range s.tasks {
		if matchesTaskFilter(entry, opts) {
			results = append(results, entry)
		}
	}
	s.mu.RUnlock()

	// Sort
	sortTasks(results, opts.OrderBy, opts.OrderDesc)

	// Apply pagination
	if opts.Offset >= len(results) {
		return []*LedgerEntry{}, nil
	}
	results = results[opts.Offset:]
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

// DeleteTask removes a task and its runs.
func (s *FileStore) DeleteTask(ctx context.Context, taskID string) error {
	s.mu.Lock()
	entry, ok := s.tasks[taskID]
	if !ok {
		s.mu.Unlock()
		return nil
	}

	// Delete associated runs
	for _, run := range entry.Runs {
		delete(s.runs, run.RunID)
	}

	delete(s.tasks, taskID)
	s.dirty = true
	s.mu.Unlock()

	return s.flush()
}

// SaveRun persists a run entry.
func (s *FileStore) SaveRun(ctx context.Context, entry *RunEntry) error {
	s.mu.Lock()
	s.runs[entry.Run.RunID] = entry
	s.dirty = true
	s.mu.Unlock()

	return s.flush()
}

// LoadRun retrieves a run by ID.
func (s *FileStore) LoadRun(ctx context.Context, runID string) (*RunEntry, error) {
	s.mu.RLock()
	entry, ok := s.runs[runID]
	s.mu.RUnlock()

	if !ok {
		return nil, nil
	}
	return entry, nil
}

// ListRuns queries runs with filters.
func (s *FileStore) ListRuns(ctx context.Context, opts ListRunsOptions) ([]*RunEntry, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}

	s.mu.RLock()
	var results []*RunEntry
	for _, entry := range s.runs {
		if matchesRunFilter(entry, opts) {
			results = append(results, entry)
		}
	}
	s.mu.RUnlock()

	// Sort
	sortRuns(results, opts.OrderBy, opts.OrderDesc)

	// Apply pagination
	if opts.Offset >= len(results) {
		return []*RunEntry{}, nil
	}
	results = results[opts.Offset:]
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

// Stats returns aggregate statistics.
func (s *FileStore) Stats(ctx context.Context) (TaskStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := TaskStats{
		ByStatus: make(map[string]int),
		BySource: make(map[string]int),
	}

	todayStart := time.Now().Truncate(24 * time.Hour).Unix()

	for _, entry := range s.tasks {
		stats.TotalTasks++
		stats.ByStatus[string(entry.Task.Status)]++
		stats.BySource[string(entry.Source)]++
	}

	for _, entry := range s.runs {
		stats.TotalRuns++
		if entry.Run.Status == state.TaskRunStatusRunning {
			stats.ActiveRuns++
		}
		if entry.Run.EndedAt >= todayStart {
			if entry.Run.Status == state.TaskRunStatusCompleted {
				stats.CompletedToday++
			} else if entry.Run.Status == state.TaskRunStatusFailed {
				stats.FailedToday++
			}
		}
	}

	return stats, nil
}

// Prune removes old completed/failed entries based on retention policy.
func (s *FileStore) Prune(ctx context.Context, olderThan time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-olderThan).Unix()
	pruned := 0

	// Prune old completed/failed runs
	for runID, entry := range s.runs {
		if isTerminalRunStatus(entry.Run.Status) && entry.Run.EndedAt < cutoff {
			delete(s.runs, runID)
			pruned++
		}
	}

	// Prune old completed/cancelled/failed tasks with no active runs
	for taskID, entry := range s.tasks {
		if isTerminalTaskStatus(entry.Task.Status) && entry.UpdatedAt < cutoff {
			hasActiveRuns := false
			for _, run := range entry.Runs {
				if !isTerminalRunStatus(run.Status) {
					hasActiveRuns = true
					break
				}
			}
			if !hasActiveRuns {
				// Remove associated runs
				for _, run := range entry.Runs {
					delete(s.runs, run.RunID)
				}
				delete(s.tasks, taskID)
				pruned++
			}
		}
	}

	if pruned > 0 {
		s.dirty = true
		_ = s.save()
	}

	return pruned, nil
}

// flush saves if dirty, with debouncing.
func (s *FileStore) flush() error {
	s.mu.RLock()
	dirty := s.dirty
	lastSave := s.lastSave
	s.mu.RUnlock()

	if !dirty {
		return nil
	}

	// Debounce: don't save more than once per second
	if time.Since(lastSave) < time.Second {
		return nil
	}

	s.mu.Lock()
	err := s.save()
	s.mu.Unlock()

	return err
}

// Close flushes any pending changes.
func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dirty {
		return s.save()
	}
	return nil
}

func isTerminalTaskStatus(status state.TaskStatus) bool {
	switch status {
	case state.TaskStatusCompleted, state.TaskStatusFailed, state.TaskStatusCancelled:
		return true
	}
	return false
}
