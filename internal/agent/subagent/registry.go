// Package subagent provides durable subagent lifecycle and completion tracking.
package subagent

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ExecutionQueued      = "queued"
	ExecutionRunning     = "running"
	ExecutionInterrupted = "interrupted"
	ExecutionTerminal    = "terminal"

	DeliveryNotRequired = "not_required"
	DeliveryPending     = "pending"
	DeliveryInProgress  = "in_progress"
	DeliveryDelivered   = "delivered"
	DeliveryFailed      = "failed"
	DeliverySuspended   = "suspended"
)

// RunOutcome records the final status of a subagent run.
type RunOutcome struct {
	Status string `json:"status"` // "ok", "timeout", "error", "unknown"
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type CompletionState struct {
	Required   bool        `json:"required,omitempty"`
	Outcome    *RunOutcome `json:"outcome,omitempty"`
	PreparedAt int64       `json:"prepared_at,omitempty"`
}

type DeliveryState struct {
	Status         string `json:"status,omitempty"`
	Attempts       int    `json:"attempts,omitempty"`
	NextAttemptAt  int64  `json:"next_attempt_at,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	LeaseID        string `json:"lease_id,omitempty"`
	LeaseExpiresAt int64  `json:"lease_expires_at,omitempty"`
	DeliveredAt    int64  `json:"delivered_at,omitempty"`
}

// SubagentRunRecord tracks a single subagent invocation through its lifecycle.
type SubagentRunRecord struct {
	RunID                string          `json:"run_id"`
	ParentRunID          string          `json:"parent_run_id,omitempty"`
	AgentID              string          `json:"agent_id,omitempty"`
	ParentAgentID        string          `json:"parent_agent_id,omitempty"`
	Depth                int             `json:"depth,omitempty"`
	Budget               Budget          `json:"budget,omitempty"`
	ChildSessionKey      string          `json:"child_session_key"`
	ControllerSessionKey string          `json:"controller_session_key,omitempty"`
	RequesterSessionKey  string          `json:"requester_session_key"`
	RequesterDisplayKey  string          `json:"requester_display_key"`
	Task                 string          `json:"task"`
	Cleanup              string          `json:"cleanup"` // "delete" | "keep"
	Label                string          `json:"label,omitempty"`
	RunTimeoutSeconds    int             `json:"run_timeout_seconds,omitempty"`
	CreatedAt            int64           `json:"created_at"`
	UpdatedAt            int64           `json:"updated_at,omitempty"`
	StartedAt            int64           `json:"started_at,omitempty"`
	EndedAt              int64           `json:"ended_at,omitempty"`
	Generation           int             `json:"generation,omitempty"`
	ExecutionStatus      string          `json:"execution_status,omitempty"`
	Outcome              *RunOutcome     `json:"outcome,omitempty"`
	Completion           CompletionState `json:"completion,omitempty"`
	Delivery             DeliveryState   `json:"delivery,omitempty"`
	SuppressAnnounce     string          `json:"suppress_announce,omitempty"` // "steer-restart" | "killed"
}

// Registry is a concurrent-safe projection over a durable RunStore.
type Registry struct {
	mu    sync.RWMutex
	runs  map[string]*SubagentRunRecord
	store RunStore
	now   func() time.Time
}

func NewRegistry() *Registry {
	r, _ := NewRegistryWithStore(newMemoryRunStore())
	return r
}

func NewRegistryWithStore(store RunStore) (*Registry, error) {
	if store == nil {
		return nil, fmt.Errorf("subagent run store is required")
	}
	records, err := store.LoadAll()
	if err != nil {
		return nil, err
	}
	r := &Registry{runs: map[string]*SubagentRunRecord{}, store: store, now: time.Now}
	for _, rec := range records {
		normalized := normalizeRunRecord(rec)
		copy := normalized
		r.runs[copy.RunID] = &copy
	}
	return r, nil
}

func OpenSQLiteRegistry(path string) (*Registry, error) {
	store, err := OpenSQLiteRunStore(path)
	if err != nil {
		return nil, err
	}
	r, err := NewRegistryWithStore(store)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return r, nil
}

func (r *Registry) Close() error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.Close()
}

func normalizeRunRecord(rec SubagentRunRecord) SubagentRunRecord {
	rec.RunID = strings.TrimSpace(rec.RunID)
	rec.ChildSessionKey = strings.TrimSpace(rec.ChildSessionKey)
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().UnixMilli()
	}
	if rec.UpdatedAt == 0 {
		rec.UpdatedAt = rec.CreatedAt
	}
	if rec.Generation <= 0 {
		rec.Generation = 1
	}
	if rec.ExecutionStatus == "" {
		switch {
		case rec.EndedAt != 0 || rec.Outcome != nil:
			rec.ExecutionStatus = ExecutionTerminal
		case rec.StartedAt != 0:
			rec.ExecutionStatus = ExecutionRunning
		default:
			rec.ExecutionStatus = ExecutionQueued
		}
	}
	if rec.Delivery.Status == "" {
		if rec.EndedAt != 0 && rec.RequesterSessionKey != "" && rec.SuppressAnnounce == "" {
			rec.Delivery.Status = DeliveryPending
			rec.Completion.Required = true
		} else {
			rec.Delivery.Status = DeliveryNotRequired
		}
	}
	return rec
}

// Register adds and durably persists a new subagent run.
func (r *Registry) Register(rec SubagentRunRecord) error {
	rec = normalizeRunRecord(rec)
	if rec.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if rec.ChildSessionKey == "" {
		return fmt.Errorf("child_session_key is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.runs[rec.RunID]; exists {
		return fmt.Errorf("run %s already registered", rec.RunID)
	}
	if err := r.store.Insert(rec); err != nil {
		return fmt.Errorf("persist subagent run: %w", err)
	}
	cp := cloneRunRecord(rec)
	r.runs[rec.RunID] = &cp
	return nil
}

func (r *Registry) Get(runID string) *SubagentRunRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec := r.runs[strings.TrimSpace(runID)]
	if rec == nil {
		return nil
	}
	cp := cloneRunRecord(*rec)
	return &cp
}

func (r *Registry) MarkRunning(runID string) error {
	return r.mutate(runID, func(rec *SubagentRunRecord) error {
		if rec.EndedAt != 0 {
			return fmt.Errorf("run already ended")
		}
		now := r.now().UnixMilli()
		if rec.StartedAt == 0 {
			rec.StartedAt = now
		}
		rec.UpdatedAt = now
		rec.ExecutionStatus = ExecutionRunning
		return nil
	})
}

// End preserves the compatibility bool API; false includes durable write failure.
func (r *Registry) End(runID string, outcome RunOutcome) bool {
	ended, _ := r.EndWithError(runID, outcome)
	return ended
}

func (r *Registry) EndWithError(runID string, outcome RunOutcome) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.runs[strings.TrimSpace(runID)]
	if rec == nil || rec.EndedAt != 0 {
		return false, nil
	}
	copy := cloneRunRecord(*rec)
	now := r.now().UnixMilli()
	copy.EndedAt, copy.UpdatedAt = now, now
	copy.ExecutionStatus = ExecutionTerminal
	outcomeCopy := outcome
	copy.Outcome = &outcomeCopy
	copy.Completion = CompletionState{Required: copy.RequesterSessionKey != "" && copy.SuppressAnnounce == "", Outcome: &outcomeCopy, PreparedAt: now}
	if copy.Completion.Required {
		copy.Delivery.Status = DeliveryPending
		copy.Delivery.NextAttemptAt = now
	} else {
		copy.Delivery.Status = DeliveryNotRequired
	}
	if err := r.store.Upsert(copy); err != nil {
		return false, fmt.Errorf("persist subagent completion: %w", err)
	}
	*rec = copy
	return true, nil
}

func (r *Registry) Delete(runID string) { _ = r.DeleteWithError(runID) }
func (r *Registry) DeleteWithError(runID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	runID = strings.TrimSpace(runID)
	if err := r.store.Delete(runID); err != nil {
		return err
	}
	delete(r.runs, runID)
	return nil
}

func (r *Registry) mutate(runID string, fn func(*SubagentRunRecord) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.runs[strings.TrimSpace(runID)]
	if rec == nil {
		return fmt.Errorf("run %q not found", runID)
	}
	copy := cloneRunRecord(*rec)
	if err := fn(&copy); err != nil {
		return err
	}
	copy = normalizeRunRecord(copy)
	if err := r.store.Upsert(copy); err != nil {
		return err
	}
	*rec = copy
	return nil
}

func (r *Registry) GetByChildSessionKey(childSessionKey string) *SubagentRunRecord {
	key := strings.TrimSpace(childSessionKey)
	if key == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var active, ended *SubagentRunRecord
	for _, rec := range r.runs {
		if rec.ChildSessionKey != key {
			continue
		}
		if rec.EndedAt == 0 {
			if active == nil || rec.CreatedAt > active.CreatedAt {
				active = rec
			}
		} else if ended == nil || rec.CreatedAt > ended.CreatedAt {
			ended = rec
		}
	}
	if active == nil {
		active = ended
	}
	if active == nil {
		return nil
	}
	copy := cloneRunRecord(*active)
	return &copy
}

func (r *Registry) GetLatestByChildSessionKey(childSessionKey string) *SubagentRunRecord {
	key := strings.TrimSpace(childSessionKey)
	if key == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var latest *SubagentRunRecord
	for _, rec := range r.runs {
		if rec.ChildSessionKey == key && (latest == nil || rec.CreatedAt > latest.CreatedAt || (rec.CreatedAt == latest.CreatedAt && rec.Generation > latest.Generation)) {
			latest = rec
		}
	}
	if latest == nil {
		return nil
	}
	copy := cloneRunRecord(*latest)
	return &copy
}

func (r *Registry) ListByRequester(requesterSessionKey string) []SubagentRunRecord {
	key := strings.TrimSpace(requesterSessionKey)
	if key == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var results []SubagentRunRecord
	for _, rec := range r.runs {
		if rec.RequesterSessionKey == key {
			results = append(results, cloneRunRecord(*rec))
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].CreatedAt > results[j].CreatedAt })
	return results
}

func (r *Registry) ListAll() []SubagentRunRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SubagentRunRecord, 0, len(r.runs))
	for _, rec := range r.runs {
		out = append(out, cloneRunRecord(*rec))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

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
func (r *Registry) Len() int { r.mu.RLock(); defer r.mu.RUnlock(); return len(r.runs) }

// ReconcileRestart terminally records runs that cannot survive daemon restart
// and makes their completion announcements retryable.
func (r *Registry) ReconcileRestart(now time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for id, rec := range r.runs {
		if rec.EndedAt != 0 {
			if rec.Delivery.Status == DeliveryInProgress && rec.Delivery.LeaseExpiresAt <= now.UnixMilli() {
				copy := cloneRunRecord(*rec)
				copy.Delivery.Status = DeliveryPending
				copy.Delivery.LeaseID = ""
				copy.Delivery.LeaseExpiresAt = 0
				if err := r.store.Upsert(copy); err != nil {
					return count, err
				}
				*rec = copy
			}
			continue
		}
		copy := cloneRunRecord(*rec)
		copy.ExecutionStatus = ExecutionTerminal
		copy.EndedAt, copy.UpdatedAt = now.UnixMilli(), now.UnixMilli()
		outcome := RunOutcome{Status: "unknown", Error: "daemon_restart"}
		copy.Outcome = &outcome
		copy.Completion = CompletionState{Required: copy.RequesterSessionKey != "" && copy.SuppressAnnounce == "", Outcome: &outcome, PreparedAt: now.UnixMilli()}
		if copy.Completion.Required {
			copy.Delivery.Status, copy.Delivery.NextAttemptAt = DeliveryPending, now.UnixMilli()
		} else {
			copy.Delivery.Status = DeliveryNotRequired
		}
		if err := r.store.Upsert(copy); err != nil {
			return count, fmt.Errorf("reconcile run %s: %w", id, err)
		}
		*rec = copy
		count++
	}
	return count, nil
}

func (r *Registry) DueCompletions(now time.Time, limit int) []SubagentRunRecord {
	if limit <= 0 {
		limit = 100
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []SubagentRunRecord
	for _, rec := range r.runs {
		if (rec.Delivery.Status == DeliveryPending || rec.Delivery.Status == DeliveryFailed) && rec.Delivery.NextAttemptAt <= now.UnixMilli() {
			out = append(out, cloneRunRecord(*rec))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Delivery.NextAttemptAt < out[j].Delivery.NextAttemptAt })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// RetryCompletion makes a terminal completion immediately due again. Retrying
// a delivered or leased completion is allowed but reports duplicateRisk.
func (r *Registry) RetryCompletion(runID string) (record SubagentRunRecord, duplicateRisk bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.runs[strings.TrimSpace(runID)]
	if rec == nil {
		return SubagentRunRecord{}, false, fmt.Errorf("run %q not found", runID)
	}
	if rec.EndedAt == 0 || rec.Outcome == nil {
		return SubagentRunRecord{}, false, fmt.Errorf("task is still running")
	}
	if !rec.Completion.Required || rec.Delivery.Status == DeliveryNotRequired {
		return SubagentRunRecord{}, false, fmt.Errorf("completion delivery is not required")
	}
	copy := cloneRunRecord(*rec)
	duplicateRisk = copy.Delivery.Status == DeliveryDelivered || copy.Delivery.Status == DeliveryInProgress
	copy.Delivery.Status = DeliveryPending
	copy.Delivery.NextAttemptAt = r.now().UnixMilli()
	copy.Delivery.LeaseID = ""
	copy.Delivery.LeaseExpiresAt = 0
	copy.Delivery.LastError = ""
	copy.UpdatedAt = r.now().UnixMilli()
	if err := r.store.Upsert(copy); err != nil {
		return SubagentRunRecord{}, false, err
	}
	*rec = copy
	return cloneRunRecord(copy), duplicateRisk, nil
}

// DismissCompletion permanently suppresses a terminal completion delivery. It
// is idempotent and does not delete the durable task/run audit record.
func (r *Registry) DismissCompletion(runID string) (SubagentRunRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.runs[strings.TrimSpace(runID)]
	if rec == nil {
		return SubagentRunRecord{}, fmt.Errorf("run %q not found", runID)
	}
	if rec.EndedAt == 0 || rec.Outcome == nil {
		return SubagentRunRecord{}, fmt.Errorf("task is still running")
	}
	copy := cloneRunRecord(*rec)
	copy.Completion.Required = false
	copy.SuppressAnnounce = "dismissed"
	copy.Delivery.Status = DeliveryNotRequired
	copy.Delivery.NextAttemptAt = 0
	copy.Delivery.LeaseID = ""
	copy.Delivery.LeaseExpiresAt = 0
	copy.Delivery.LastError = ""
	copy.UpdatedAt = r.now().UnixMilli()
	if err := r.store.Upsert(copy); err != nil {
		return SubagentRunRecord{}, err
	}
	*rec = copy
	return cloneRunRecord(copy), nil
}

func (r *Registry) MarkDeliveryInProgress(runID, leaseID string, expiresAt time.Time) error {
	return r.mutate(runID, func(rec *SubagentRunRecord) error {
		if rec.Delivery.Status != DeliveryPending && rec.Delivery.Status != DeliveryFailed {
			return fmt.Errorf("completion is not due")
		}
		rec.Delivery.Status, rec.Delivery.LeaseID, rec.Delivery.LeaseExpiresAt = DeliveryInProgress, leaseID, expiresAt.UnixMilli()
		rec.Delivery.Attempts++
		rec.UpdatedAt = r.now().UnixMilli()
		return nil
	})
}
func (r *Registry) MarkDeliveryDelivered(runID, leaseID string) error {
	return r.mutate(runID, func(rec *SubagentRunRecord) error {
		if rec.Delivery.Status != DeliveryInProgress || rec.Delivery.LeaseID != leaseID {
			return fmt.Errorf("completion lease mismatch")
		}
		rec.Delivery.Status, rec.Delivery.DeliveredAt = DeliveryDelivered, r.now().UnixMilli()
		rec.Delivery.LeaseID, rec.Delivery.LeaseExpiresAt, rec.Delivery.LastError = "", 0, ""
		return nil
	})
}
func (r *Registry) MarkDeliveryFailed(runID, leaseID string, deliveryErr error, maxAttempts int) error {
	return r.mutate(runID, func(rec *SubagentRunRecord) error {
		if rec.Delivery.Status != DeliveryInProgress || rec.Delivery.LeaseID != leaseID {
			return fmt.Errorf("completion lease mismatch")
		}
		if maxAttempts > 0 && rec.Delivery.Attempts >= maxAttempts {
			rec.Delivery.Status = DeliverySuspended
		} else {
			rec.Delivery.Status = DeliveryFailed
		}
		rec.Delivery.LastError = errorText(deliveryErr)
		backoff := time.Second << minInt(rec.Delivery.Attempts-1, 8)
		if backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
		rec.Delivery.NextAttemptAt = r.now().Add(backoff).UnixMilli()
		rec.Delivery.LeaseID, rec.Delivery.LeaseExpiresAt = "", 0
		return nil
	})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
