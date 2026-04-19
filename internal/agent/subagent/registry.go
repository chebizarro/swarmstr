// Package subagent provides an in-memory registry for tracking subagent run
// lifecycle. It supports registering runs, marking them as ended, looking up
// runs by child session key, and reactivating completed sessions by replacing
// the ended run with a fresh one that preserves context.
package subagent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// RunOutcome records the final status of a subagent run.
type RunOutcome struct {
	Status string `json:"status"` // "ok", "timeout", "error", "unknown"
	Error  string `json:"error,omitempty"`
}

// SubagentRunRecord tracks a single subagent invocation through its lifecycle.
type SubagentRunRecord struct {
	RunID                string     `json:"run_id"`
	ChildSessionKey      string     `json:"child_session_key"`
	RequesterSessionKey  string     `json:"requester_session_key"`
	RequesterDisplayKey  string     `json:"requester_display_key"`
	Task                 string     `json:"task"`
	Cleanup              string     `json:"cleanup"` // "delete" | "keep"
	Label                string     `json:"label,omitempty"`
	RunTimeoutSeconds    int        `json:"run_timeout_seconds,omitempty"`
	CreatedAt            int64      `json:"created_at"`
	StartedAt            int64      `json:"started_at,omitempty"`
	EndedAt              int64      `json:"ended_at,omitempty"`
	Outcome              *RunOutcome `json:"outcome,omitempty"`
	SuppressAnnounce     string     `json:"suppress_announce,omitempty"` // "steer-restart" | "killed"
}

// Registry is a concurrent-safe in-memory store for subagent run records.
type Registry struct {
	mu   sync.RWMutex
	runs map[string]*SubagentRunRecord // keyed by RunID
}

// NewRegistry creates an empty subagent registry.
func NewRegistry() *Registry {
	return &Registry{
		runs: make(map[string]*SubagentRunRecord),
	}
}

// Register adds a new subagent run to the registry.
// Returns an error if a run with the same ID already exists.
func (r *Registry) Register(rec SubagentRunRecord) error {
	rec.RunID = strings.TrimSpace(rec.RunID)
	rec.ChildSessionKey = strings.TrimSpace(rec.ChildSessionKey)
	if rec.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if rec.ChildSessionKey == "" {
		return fmt.Errorf("child_session_key is required")
	}
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().UnixMilli()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.runs[rec.RunID]; exists {
		return fmt.Errorf("run %s already registered", rec.RunID)
	}
	cp := rec
	r.runs[rec.RunID] = &cp
	return nil
}

// Get returns the run record for the given run ID, or nil if not found.
func (r *Registry) Get(runID string) *SubagentRunRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec := r.runs[runID]
	if rec == nil {
		return nil
	}
	cp := *rec
	return &cp
}

// End marks a run as ended with the given outcome.
// Returns false if the run doesn't exist or is already ended.
func (r *Registry) End(runID string, outcome RunOutcome) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.runs[runID]
	if rec == nil || rec.EndedAt != 0 {
		return false
	}
	now := time.Now().UnixMilli()
	rec.EndedAt = now
	cp := outcome
	rec.Outcome = &cp
	return true
}

// Delete removes a run record from the registry.
func (r *Registry) Delete(runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, runID)
}

// GetByChildSessionKey returns the most relevant run for a child session key.
// It prefers an active (non-ended) run; if none, it returns the latest ended run.
func (r *Registry) GetByChildSessionKey(childSessionKey string) *SubagentRunRecord {
	key := strings.TrimSpace(childSessionKey)
	if key == "" {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var latestActive *SubagentRunRecord
	var latestEnded *SubagentRunRecord
	for _, rec := range r.runs {
		if rec.ChildSessionKey != key {
			continue
		}
		if rec.EndedAt == 0 {
			if latestActive == nil || rec.CreatedAt > latestActive.CreatedAt {
				latestActive = rec
			}
		} else {
			if latestEnded == nil || rec.CreatedAt > latestEnded.CreatedAt {
				latestEnded = rec
			}
		}
	}

	result := latestActive
	if result == nil {
		result = latestEnded
	}
	if result == nil {
		return nil
	}
	cp := *result
	return &cp
}

// GetLatestByChildSessionKey returns the most recently created run for a child
// session key, regardless of whether it is active or ended.
func (r *Registry) GetLatestByChildSessionKey(childSessionKey string) *SubagentRunRecord {
	key := strings.TrimSpace(childSessionKey)
	if key == "" {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var latest *SubagentRunRecord
	for _, rec := range r.runs {
		if rec.ChildSessionKey != key {
			continue
		}
		if latest == nil || rec.CreatedAt > latest.CreatedAt {
			latest = rec
		}
	}

	if latest == nil {
		return nil
	}
	cp := *latest
	return &cp
}

// ListByRequester returns all runs requested by the given session key,
// sorted newest-first by CreatedAt.
func (r *Registry) ListByRequester(requesterSessionKey string) []SubagentRunRecord {
	key := strings.TrimSpace(requesterSessionKey)
	if key == "" {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var results []SubagentRunRecord
	for _, rec := range r.runs {
		if rec.RequesterSessionKey != key {
			continue
		}
		results = append(results, *rec)
	}

	// Sort newest first.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].CreatedAt > results[j-1].CreatedAt; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
	return results
}

// CountActive returns the number of runs that have not yet ended.
func (r *Registry) CountActive() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, rec := range r.runs {
		if rec.EndedAt == 0 {
			count++
		}
	}
	return count
}

// Len returns the total number of tracked runs.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.runs)
}
