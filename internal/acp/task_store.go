package acp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// TaskStatus is the ACP dispatcher lifecycle for a delegated task.
type TaskStatus string

const (
	TaskStatusQueued    TaskStatus = "queued"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusSucceeded TaskStatus = "succeeded"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusTimedOut  TaskStatus = "timed_out"
	TaskStatusBlocked   TaskStatus = "blocked"
	TaskStatusCancelled TaskStatus = "cancelled"
	TaskStatusLost      TaskStatus = "lost"
)

// Terminal reports whether no further worker execution is expected.
func (s TaskStatus) Terminal() bool {
	switch s {
	case TaskStatusSucceeded, TaskStatusFailed, TaskStatusTimedOut, TaskStatusBlocked, TaskStatusCancelled, TaskStatusLost:
		return true
	default:
		return false
	}
}

// DeliveryStatus tracks whether a terminal result was delivered to a waiter/requester.
type DeliveryStatus string

const (
	DeliveryPending       DeliveryStatus = "pending"
	DeliveryDelivered     DeliveryStatus = "delivered"
	DeliveryFailed        DeliveryStatus = "failed"
	DeliveryNotApplicable DeliveryStatus = "not_applicable"
)

// ArtifactPayload carries typed data between ACP pipeline/flow steps.
type ArtifactPayload struct {
	Type      string          `json:"type"` // text | json | file_ref
	Text      string          `json:"text,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Ref       string          `json:"ref,omitempty"`
	MediaType string          `json:"media_type,omitempty"`
	Name      string          `json:"name,omitempty"`
}

// Normalize makes the artifact safe to persist and replay.
func (a ArtifactPayload) Normalize() ArtifactPayload {
	a.Type = strings.TrimSpace(strings.ToLower(a.Type))
	if a.Type == "" {
		switch {
		case len(a.Data) > 0:
			a.Type = "json"
		case strings.TrimSpace(a.Ref) != "":
			a.Type = "file_ref"
		default:
			a.Type = "text"
		}
	}
	a.Ref = strings.TrimSpace(a.Ref)
	a.MediaType = strings.TrimSpace(a.MediaType)
	a.Name = strings.TrimSpace(a.Name)
	if len(a.Data) > 0 {
		a.Data = append(json.RawMessage(nil), a.Data...)
	}
	return a
}

// WorkerTaskMetadata records worker/session routing details for a task record.
type WorkerTaskMetadata struct {
	PubKey        string `json:"pubkey,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	SessionKey    string `json:"session_key,omitempty"`
	Backend       string `json:"backend,omitempty"`
	TransportUsed string `json:"transport_used,omitempty"`
}

// TaskRecord is the durable ACP view of one delegated task.
type TaskRecord struct {
	TaskID              string              `json:"task_id"`
	FlowID              string              `json:"flow_id,omitempty"`
	StepIndex           int                 `json:"step_index,omitempty"`
	Runtime             string              `json:"runtime,omitempty"`
	Status              TaskStatus          `json:"status"`
	DeliveryStatus      DeliveryStatus      `json:"delivery_status"`
	RequesterSessionKey string              `json:"requester_session_key,omitempty"`
	WorkerSessionKey    string              `json:"worker_session_key,omitempty"`
	Instructions        string              `json:"instructions,omitempty"`
	Label               string              `json:"label,omitempty"`
	CreatedAt           time.Time           `json:"created_at"`
	StartedAt           *time.Time          `json:"started_at,omitempty"`
	EndedAt             *time.Time          `json:"ended_at,omitempty"`
	LastEventAt         *time.Time          `json:"last_event_at,omitempty"`
	CleanupAfter        *time.Time          `json:"cleanup_after,omitempty"`
	Error               string              `json:"error,omitempty"`
	ProgressSummary     string              `json:"progress_summary,omitempty"`
	TerminalSummary     string              `json:"terminal_summary,omitempty"`
	Worker              *WorkerTaskMetadata `json:"worker,omitempty"`
	ResultWorker        *WorkerMetadata     `json:"result_worker,omitempty"`
	Artifacts           []ArtifactPayload   `json:"artifacts,omitempty"`
}

// Normalize fills defaults and trims stable identifiers.
func (r TaskRecord) Normalize(now time.Time) TaskRecord {
	r.TaskID = strings.TrimSpace(r.TaskID)
	r.FlowID = strings.TrimSpace(r.FlowID)
	r.Runtime = strings.TrimSpace(r.Runtime)
	r.RequesterSessionKey = strings.TrimSpace(r.RequesterSessionKey)
	r.WorkerSessionKey = strings.TrimSpace(r.WorkerSessionKey)
	r.Label = strings.TrimSpace(r.Label)
	if r.Status == "" {
		r.Status = TaskStatusQueued
	}
	if r.DeliveryStatus == "" {
		r.DeliveryStatus = DeliveryPending
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.Status.Terminal() {
		if r.EndedAt == nil {
			ended := now
			r.EndedAt = &ended
		}
		if r.CleanupAfter == nil {
			cleanup := now.Add(acpTaskTerminalRetentionTTL)
			r.CleanupAfter = &cleanup
		}
	} else {
		r.CleanupAfter = nil
	}
	if r.Worker != nil {
		worker := *r.Worker
		worker.PubKey = strings.TrimSpace(worker.PubKey)
		worker.AgentID = strings.TrimSpace(worker.AgentID)
		worker.SessionKey = strings.TrimSpace(worker.SessionKey)
		worker.Backend = strings.TrimSpace(worker.Backend)
		worker.TransportUsed = strings.TrimSpace(worker.TransportUsed)
		r.Worker = &worker
	}
	if len(r.Artifacts) > 0 {
		arts := make([]ArtifactPayload, 0, len(r.Artifacts))
		for _, a := range r.Artifacts {
			arts = append(arts, a.Normalize())
		}
		r.Artifacts = arts
	}
	return r
}

// TaskPatch updates mutable fields of a task record. Nil pointer fields are ignored.
type TaskPatch struct {
	Status              *TaskStatus
	DeliveryStatus      *DeliveryStatus
	Runtime             *string
	RequesterSessionKey *string
	WorkerSessionKey    *string
	Instructions        *string
	Label               *string
	StartedAt           **time.Time
	EndedAt             **time.Time
	LastEventAt         **time.Time
	CleanupAfter        **time.Time
	Error               *string
	ProgressSummary     *string
	TerminalSummary     *string
	Worker              **WorkerTaskMetadata
	ResultWorker        **WorkerMetadata
	Artifacts           *[]ArtifactPayload
}

// TaskFilter controls task-store List queries.
type TaskFilter struct {
	Statuses            []TaskStatus
	DeliveryStatuses    []DeliveryStatus
	RequesterSessionKey string
	WorkerSessionKey    string
	FlowID              string
	Limit               int
}

// TaskStoreStats summarizes persisted ACP task state.
type TaskStoreStats struct {
	Total      int            `json:"total"`
	ByStatus   map[string]int `json:"by_status"`
	ByDelivery map[string]int `json:"by_delivery"`
}

type TaskRestartRecoveryStats struct {
	MarkedLost int `json:"marked_lost"`
}

// TaskStore persists ACP task lifecycle records.
type TaskStore interface {
	Create(ctx context.Context, record TaskRecord) error
	Get(ctx context.Context, taskID string) (*TaskRecord, error)
	Update(ctx context.Context, taskID string, patch TaskPatch) error
	List(ctx context.Context, filter TaskFilter) ([]TaskRecord, error)
	Delete(ctx context.Context, taskID string) error
	RecordProgress(ctx context.Context, taskID, summary string) error
	Stats(ctx context.Context) (TaskStoreStats, error)
	MarkNonTerminalLost(ctx context.Context, reason string) (TaskRestartRecoveryStats, error)
}

// InMemoryTaskStore is a concurrent-safe TaskStore. It is useful for tests and
// as the in-process cache used by FileTaskStore.
type InMemoryTaskStore struct {
	mu    sync.RWMutex
	now   func() time.Time
	tasks map[string]TaskRecord
}

func NewInMemoryTaskStore() *InMemoryTaskStore {
	return &InMemoryTaskStore{now: time.Now, tasks: make(map[string]TaskRecord)}
}

func (s *InMemoryTaskStore) Create(_ context.Context, record TaskRecord) error {
	now := s.now()
	record = record.Normalize(now)
	if record.TaskID == "" {
		return fmt.Errorf("acp task store: task_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tasks[record.TaskID]; exists {
		return fmt.Errorf("acp task store: task %q already exists", record.TaskID)
	}
	s.tasks[record.TaskID] = cloneTaskRecord(record)
	return nil
}

func (s *InMemoryTaskStore) Get(_ context.Context, taskID string) (*TaskRecord, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, nil
	}
	s.mu.RLock()
	rec, ok := s.tasks[taskID]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	cp := cloneTaskRecord(rec)
	return &cp, nil
}

func (s *InMemoryTaskStore) Update(_ context.Context, taskID string, patch TaskPatch) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("acp task store: task_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.tasks[taskID]
	if !ok {
		return fmt.Errorf("acp task store: task %q not found", taskID)
	}
	applyTaskPatch(&rec, patch)
	rec = rec.Normalize(s.now())
	s.tasks[taskID] = rec
	return nil
}

func (s *InMemoryTaskStore) List(_ context.Context, filter TaskFilter) ([]TaskRecord, error) {
	s.mu.Lock()
	s.pruneExpiredLocked(s.now())
	out := make([]TaskRecord, 0, len(s.tasks))
	for _, rec := range s.tasks {
		if matchTaskRecord(rec, filter) {
			out = append(out, cloneTaskRecord(rec))
		}
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *InMemoryTaskStore) Delete(_ context.Context, taskID string) error {
	s.mu.Lock()
	delete(s.tasks, strings.TrimSpace(taskID))
	s.mu.Unlock()
	return nil
}

func (s *InMemoryTaskStore) RecordProgress(ctx context.Context, taskID, summary string) error {
	now := s.now()
	return s.Update(ctx, taskID, TaskPatch{ProgressSummary: stringPtr(summary), LastEventAt: timePtrPtr(&now)})
}

func (s *InMemoryTaskStore) Stats(_ context.Context) (TaskStoreStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(s.now())
	stats := TaskStoreStats{ByStatus: make(map[string]int), ByDelivery: make(map[string]int)}
	for _, rec := range s.tasks {
		stats.Total++
		stats.ByStatus[string(rec.Status)]++
		stats.ByDelivery[string(rec.DeliveryStatus)]++
	}
	return stats, nil
}

func (s *InMemoryTaskStore) MarkNonTerminalLost(_ context.Context, reason string) (TaskRestartRecoveryStats, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "terminated by daemon restart"
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := TaskRestartRecoveryStats{}
	for id, rec := range s.tasks {
		if rec.Status.Terminal() {
			continue
		}
		rec.Status = TaskStatusLost
		rec.DeliveryStatus = DeliveryFailed
		rec.EndedAt = &now
		rec.LastEventAt = &now
		rec.Error = reason
		rec.TerminalSummary = reason
		cleanup := now.Add(acpTaskTerminalRetentionTTL)
		rec.CleanupAfter = &cleanup
		s.tasks[id] = rec.Normalize(now)
		stats.MarkedLost++
	}
	return stats, nil
}

func (s *InMemoryTaskStore) pruneExpiredLocked(now time.Time) int {
	if now.IsZero() {
		now = time.Now()
	}
	removed := 0
	for id, rec := range s.tasks {
		if rec.Status.Terminal() && rec.CleanupAfter != nil && !rec.CleanupAfter.After(now) {
			delete(s.tasks, id)
			removed++
		}
	}
	return removed
}

// FileTaskStore persists all task records to one JSON document using atomic writes.
type FileTaskStore struct {
	mem  *InMemoryTaskStore
	dir  string
	path string
	mu   sync.Mutex
}

type taskStoreDoc struct {
	Version   int                   `json:"version"`
	Tasks     map[string]TaskRecord `json:"tasks"`
	UpdatedAt int64                 `json:"updated_at"`
}

const (
	taskStoreVersion            = 1
	acpTaskTerminalRetentionTTL = 30 * 24 * time.Hour
)

func NewFileTaskStore(dir string) (*FileTaskStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("acp task store: create dir %q: %w", dir, err)
	}
	s := &FileTaskStore{mem: NewInMemoryTaskStore(), dir: dir, path: filepath.Join(dir, "acp_tasks.json")}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileTaskStore) Create(ctx context.Context, record TaskRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mem.Create(ctx, record); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *FileTaskStore) Get(ctx context.Context, taskID string) (*TaskRecord, error) {
	return s.mem.Get(ctx, taskID)
}

func (s *FileTaskStore) Update(ctx context.Context, taskID string, patch TaskPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mem.Update(ctx, taskID, patch); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *FileTaskStore) List(ctx context.Context, filter TaskFilter) ([]TaskRecord, error) {
	return s.mem.List(ctx, filter)
}

func (s *FileTaskStore) Delete(ctx context.Context, taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mem.Delete(ctx, taskID); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *FileTaskStore) RecordProgress(ctx context.Context, taskID, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.mem.now()
	if err := s.mem.Update(ctx, taskID, TaskPatch{ProgressSummary: stringPtr(summary), LastEventAt: timePtrPtr(&now)}); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *FileTaskStore) Stats(ctx context.Context) (TaskStoreStats, error) {
	return s.mem.Stats(ctx)
}

func (s *FileTaskStore) MarkNonTerminalLost(ctx context.Context, reason string) (TaskRestartRecoveryStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats, err := s.mem.MarkNonTerminalLost(ctx, reason)
	if err != nil || stats.MarkedLost == 0 {
		return stats, err
	}
	return stats, s.saveLocked()
}

func (s *FileTaskStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("acp task store: load: %w", err)
	}
	var doc taskStoreDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("acp task store: decode: %w", err)
	}
	if doc.Tasks == nil {
		return nil
	}
	s.mem.mu.Lock()
	s.mem.tasks = make(map[string]TaskRecord, len(doc.Tasks))
	for id, rec := range doc.Tasks {
		s.mem.tasks[id] = rec.Normalize(s.mem.now())
	}
	pruned := s.mem.pruneExpiredLocked(s.mem.now())
	s.mem.mu.Unlock()
	if pruned > 0 {
		return s.saveLocked()
	}
	return nil
}

func (s *FileTaskStore) saveLocked() error {
	s.mem.mu.Lock()
	s.mem.pruneExpiredLocked(s.mem.now())
	tasks := make(map[string]TaskRecord, len(s.mem.tasks))
	for id, rec := range s.mem.tasks {
		tasks[id] = cloneTaskRecord(rec)
	}
	s.mem.mu.Unlock()
	data, err := json.MarshalIndent(taskStoreDoc{Version: taskStoreVersion, Tasks: tasks, UpdatedAt: time.Now().Unix()}, "", "  ")
	if err != nil {
		return fmt.Errorf("acp task store: encode: %w", err)
	}
	tmp := s.path + "." + randomFileSuffix() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("acp task store: write temp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("acp task store: rename temp: %w", err)
	}
	return nil
}

func applyTaskPatch(rec *TaskRecord, patch TaskPatch) {
	if patch.Status != nil {
		rec.Status = *patch.Status
	}
	if patch.DeliveryStatus != nil {
		rec.DeliveryStatus = *patch.DeliveryStatus
	}
	if patch.Runtime != nil {
		rec.Runtime = *patch.Runtime
	}
	if patch.RequesterSessionKey != nil {
		rec.RequesterSessionKey = *patch.RequesterSessionKey
	}
	if patch.WorkerSessionKey != nil {
		rec.WorkerSessionKey = *patch.WorkerSessionKey
	}
	if patch.Instructions != nil {
		rec.Instructions = *patch.Instructions
	}
	if patch.Label != nil {
		rec.Label = *patch.Label
	}
	if patch.StartedAt != nil {
		rec.StartedAt = cloneTimePtr(*patch.StartedAt)
	}
	if patch.EndedAt != nil {
		rec.EndedAt = cloneTimePtr(*patch.EndedAt)
	}
	if patch.LastEventAt != nil {
		rec.LastEventAt = cloneTimePtr(*patch.LastEventAt)
	}
	if patch.CleanupAfter != nil {
		rec.CleanupAfter = cloneTimePtr(*patch.CleanupAfter)
	}
	if patch.Error != nil {
		rec.Error = *patch.Error
	}
	if patch.ProgressSummary != nil {
		rec.ProgressSummary = *patch.ProgressSummary
	}
	if patch.TerminalSummary != nil {
		rec.TerminalSummary = *patch.TerminalSummary
	}
	if patch.Worker != nil {
		rec.Worker = cloneWorkerTaskMetadata(*patch.Worker)
	}
	if patch.ResultWorker != nil {
		rec.ResultWorker = cloneWorkerMetadata(*patch.ResultWorker)
	}
	if patch.Artifacts != nil {
		rec.Artifacts = cloneArtifacts(*patch.Artifacts)
	}
}

func matchTaskRecord(rec TaskRecord, filter TaskFilter) bool {
	if len(filter.Statuses) > 0 {
		ok := false
		for _, s := range filter.Statuses {
			if rec.Status == s {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(filter.DeliveryStatuses) > 0 {
		ok := false
		for _, s := range filter.DeliveryStatuses {
			if rec.DeliveryStatus == s {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if strings.TrimSpace(filter.RequesterSessionKey) != "" && rec.RequesterSessionKey != strings.TrimSpace(filter.RequesterSessionKey) {
		return false
	}
	if strings.TrimSpace(filter.WorkerSessionKey) != "" && rec.WorkerSessionKey != strings.TrimSpace(filter.WorkerSessionKey) {
		return false
	}
	if strings.TrimSpace(filter.FlowID) != "" && rec.FlowID != strings.TrimSpace(filter.FlowID) {
		return false
	}
	return true
}

func cloneTaskRecord(rec TaskRecord) TaskRecord {
	rec.StartedAt = cloneTimePtr(rec.StartedAt)
	rec.EndedAt = cloneTimePtr(rec.EndedAt)
	rec.LastEventAt = cloneTimePtr(rec.LastEventAt)
	rec.CleanupAfter = cloneTimePtr(rec.CleanupAfter)
	rec.Worker = cloneWorkerTaskMetadata(rec.Worker)
	rec.ResultWorker = cloneWorkerMetadata(rec.ResultWorker)
	rec.Artifacts = cloneArtifacts(rec.Artifacts)
	return rec
}

func cloneWorkerTaskMetadata(in *WorkerTaskMetadata) *WorkerTaskMetadata {
	if in == nil {
		return nil
	}
	cp := *in
	return &cp
}

func cloneArtifacts(in []ArtifactPayload) []ArtifactPayload {
	if len(in) == 0 {
		return nil
	}
	out := make([]ArtifactPayload, 0, len(in))
	for _, a := range in {
		out = append(out, a.Normalize())
	}
	return out
}

func cloneTimePtr(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	cp := *in
	return &cp
}

func stringPtr(v string) *string { return &v }

func taskStatusPtr(v TaskStatus) *TaskStatus { return &v }

func deliveryStatusPtr(v DeliveryStatus) *DeliveryStatus { return &v }

func timePtrPtr(v *time.Time) **time.Time { return &v }

func workerMetadataPtrPtr(v *WorkerMetadata) **WorkerMetadata { return &v }

func artifactsPtr(v []ArtifactPayload) *[]ArtifactPayload { return &v }

func randomFileSuffix() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return hex.EncodeToString(h[:4])
}
