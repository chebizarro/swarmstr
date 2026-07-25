// Package tasksuggestions is the process-local registry for model-proposed
// follow-up tasks (OpenClaw taskSuggestions.* parity). The registry is
// deliberately ephemeral — suggestion ids vanish on restart — while the
// acceptance state machine keeps accepted results idempotent for retries.
package tasksuggestions

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// Retention limits (OpenClaw task-suggestion-registry parity).
const (
	MaxSuggestions   = 100
	MaxRetainedBytes = 2 * 1024 * 1024
	statusPending    = "pending"
	statusAccepting  = "accepting"
	statusAccepted   = "accepted"
	statusDismissed  = "dismissed"
)

// Suggestion is one model-proposed follow-up task waiting for operator action.
type Suggestion struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Prompt     string `json:"prompt"`
	Tldr       string `json:"tldr"`
	CWD        string `json:"cwd"`
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agentId,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

type record struct {
	status     string
	suggestion Suggestion
	sessionKey string // set when status == accepted
}

// CreateParams describes one taskSuggestions.create invocation after schema
// normalization.
type CreateParams struct {
	Title      string
	Prompt     string
	Tldr       string
	CWD        string
	SessionKey string
	AgentID    string
}

// CreateResult reports a created suggestion plus any pending suggestions the
// registry evicted to make room.
type CreateResult struct {
	Suggestion            Suggestion
	EvictedPendingTaskIDs []string
	Full                  bool
}

// Acceptance is the claim outcome for taskSuggestions.accept.
type Acceptance struct {
	// Status is one of claimed | accepted | accepting | dismissed | missing.
	Status     string
	Suggestion Suggestion
	SessionKey string
}

// Registry tracks suggestions with bounded retention.
type Registry struct {
	mu            sync.Mutex
	records       map[string]*record
	order         []string // insertion order for deterministic eviction and listing
	retainedBytes int
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{records: map[string]*record{}}
}

func newTaskID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "task_" + hex.EncodeToString([]byte(time.Now().String()))[:32]
	}
	return "task_" + hex.EncodeToString(buf)
}

func retainedBytesFor(s Suggestion) int {
	raw, err := json.Marshal(s)
	if err != nil {
		return 1
	}
	// One delimiter byte per record keeps the whole list response bounded.
	return len(raw) + 1
}

func (r *Registry) removeLocked(taskID string) {
	rec, ok := r.records[taskID]
	if !ok {
		return
	}
	r.retainedBytes -= retainedBytesFor(rec.suggestion)
	delete(r.records, taskID)
	for i, id := range r.order {
		if id == taskID {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// evictLocked removes one record: resolved records first, pending second.
// Returns the evicted pending task id (empty when a resolved record was
// evicted) and false when every record is an in-flight acceptance.
func (r *Registry) evictLocked() (string, bool) {
	for _, id := range r.order {
		if rec := r.records[id]; rec.status == statusAccepted || rec.status == statusDismissed {
			r.removeLocked(id)
			return "", true
		}
	}
	for _, id := range r.order {
		if rec := r.records[id]; rec.status == statusPending {
			r.removeLocked(id)
			return id, true
		}
	}
	return "", false
}

// Create records one suggestion without starting any work.
func (r *Registry) Create(params CreateParams) CreateResult {
	suggestion := Suggestion{
		ID:         newTaskID(),
		Title:      params.Title,
		Prompt:     params.Prompt,
		Tldr:       params.Tldr,
		CWD:        params.CWD,
		SessionKey: params.SessionKey,
		AgentID:    params.AgentID,
		CreatedAt:  time.Now().UnixMilli(),
	}
	bytes := retainedBytesFor(suggestion)
	r.mu.Lock()
	defer r.mu.Unlock()
	evicted := []string{}
	for len(r.records) >= MaxSuggestions || r.retainedBytes+bytes+1 > MaxRetainedBytes {
		pendingID, ok := r.evictLocked()
		if !ok {
			// Every retained record is an in-flight acceptance; reject new
			// work instead of losing an acceptance's idempotency result.
			return CreateResult{Full: true}
		}
		if pendingID != "" {
			evicted = append(evicted, pendingID)
		}
	}
	r.records[suggestion.ID] = &record{status: statusPending, suggestion: suggestion}
	r.order = append(r.order, suggestion.ID)
	r.retainedBytes += bytes
	return CreateResult{Suggestion: suggestion, EvictedPendingTaskIDs: evicted}
}

// List returns pending suggestions newest first, optionally filtered by the
// source session and agent.
func (r *Registry) List(sessionKey, agentID string) []Suggestion {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Suggestion, 0, len(r.order))
	for i := len(r.order) - 1; i >= 0; i-- {
		rec := r.records[r.order[i]]
		if rec.status != statusPending {
			continue
		}
		if sessionKey != "" && rec.suggestion.SessionKey != sessionKey {
			continue
		}
		if agentID != "" && rec.suggestion.AgentID != agentID {
			continue
		}
		out = append(out, rec.suggestion)
	}
	return out
}

// BeginAcceptance atomically claims one pending suggestion before any
// privileged session side effects begin.
func (r *Registry) BeginAcceptance(taskID string) Acceptance {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[taskID]
	if !ok {
		return Acceptance{Status: "missing"}
	}
	if rec.status == statusAccepted {
		return Acceptance{Status: statusAccepted, SessionKey: rec.sessionKey}
	}
	if rec.status != statusPending {
		return Acceptance{Status: rec.status}
	}
	rec.status = statusAccepting
	return Acceptance{Status: "claimed", Suggestion: rec.suggestion}
}

// CancelAcceptance restores a claim when session creation fails cleanly.
// Returns the restored suggestion so callers can re-announce it.
func (r *Registry) CancelAcceptance(taskID string) (Suggestion, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[taskID]
	if !ok || rec.status != statusAccepting {
		return Suggestion{}, false
	}
	rec.status = statusPending
	return rec.suggestion, true
}

// AbandonAcceptance retires a claimed suggestion whose partial side effects
// could not be rolled back safely.
func (r *Registry) AbandonAcceptance(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[taskID]
	if !ok || rec.status != statusAccepting {
		return false
	}
	rec.status = statusDismissed
	return true
}

// CompleteAcceptance retains the created session key so retried accepts
// return the same result.
func (r *Registry) CompleteAcceptance(taskID, sessionKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[taskID]
	if !ok || rec.status != statusAccepting {
		return
	}
	rec.status = statusAccepted
	rec.sessionKey = sessionKey
}

// Dismiss removes only a pending suggestion; accepted or in-flight records
// stay immutable.
func (r *Registry) Dismiss(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[taskID]
	if !ok || rec.status != statusPending {
		return false
	}
	rec.status = statusDismissed
	return true
}
