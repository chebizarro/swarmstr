package subagent

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// RunStore is the durable authority for subagent lifecycle records.
type RunStore interface {
	LoadAll() ([]SubagentRunRecord, error)
	Insert(SubagentRunRecord) error
	Upsert(SubagentRunRecord) error
	Replace(oldRunID string, replacement SubagentRunRecord) error
	Delete(runID string) error
	Close() error
}

type memoryRunStore struct {
	mu   sync.Mutex
	runs map[string]SubagentRunRecord
}

func newMemoryRunStore() *memoryRunStore {
	return &memoryRunStore{runs: map[string]SubagentRunRecord{}}
}

func (s *memoryRunStore) LoadAll() ([]SubagentRunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SubagentRunRecord, 0, len(s.runs))
	for _, rec := range s.runs {
		out = append(out, cloneRunRecord(rec))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}
func (s *memoryRunStore) Insert(rec SubagentRunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[rec.RunID]; ok {
		return fmt.Errorf("run %s already registered", rec.RunID)
	}
	s.runs[rec.RunID] = cloneRunRecord(rec)
	return nil
}
func (s *memoryRunStore) Upsert(rec SubagentRunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[rec.RunID] = cloneRunRecord(rec)
	return nil
}
func (s *memoryRunStore) Replace(oldRunID string, replacement SubagentRunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if oldRunID != replacement.RunID {
		if _, exists := s.runs[replacement.RunID]; exists {
			return fmt.Errorf("run %s already registered", replacement.RunID)
		}
		delete(s.runs, oldRunID)
	}
	s.runs[replacement.RunID] = cloneRunRecord(replacement)
	return nil
}
func (s *memoryRunStore) Delete(runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runs, runID)
	return nil
}
func (s *memoryRunStore) Close() error { return nil }

func cloneRunRecord(in SubagentRunRecord) SubagentRunRecord {
	raw, _ := json.Marshal(in)
	var out SubagentRunRecord
	_ = json.Unmarshal(raw, &out)
	return out
}
