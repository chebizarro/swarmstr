package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FlowStatus is the lifecycle state of an ACP multi-step flow.
type FlowStatus string

const (
	FlowStatusQueued    FlowStatus = "queued"
	FlowStatusRunning   FlowStatus = "running"
	FlowStatusWaiting   FlowStatus = "waiting"
	FlowStatusBlocked   FlowStatus = "blocked"
	FlowStatusSucceeded FlowStatus = "succeeded"
	FlowStatusFailed    FlowStatus = "failed"
	FlowStatusCancelled FlowStatus = "cancelled"
	FlowStatusLost      FlowStatus = "lost"
)

func (s FlowStatus) Terminal() bool {
	switch s {
	case FlowStatusSucceeded, FlowStatusFailed, FlowStatusCancelled, FlowStatusLost:
		return true
	default:
		return false
	}
}

// FlowRecord tracks orchestration state for ACP pipeline/flow execution.
type FlowRecord struct {
	FlowID              string            `json:"flow_id"`
	OwnerSessionKey     string            `json:"owner_session_key,omitempty"`
	Goal                string            `json:"goal,omitempty"`
	Status              FlowStatus        `json:"status"`
	Revision            int64             `json:"revision"`
	CurrentStep         int               `json:"current_step,omitempty"`
	StateJSON           json.RawMessage   `json:"state_json,omitempty"`
	WaitJSON            json.RawMessage   `json:"wait_json,omitempty"`
	BlockedTaskID       string            `json:"blocked_task_id,omitempty"`
	BlockedSummary      string            `json:"blocked_summary,omitempty"`
	TaskIDs             []string          `json:"task_ids,omitempty"`
	Artifacts           []ArtifactPayload `json:"artifacts,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	EndedAt             *time.Time        `json:"ended_at,omitempty"`
	CancellationReason  string            `json:"cancellation_reason,omitempty"`
	LastTransitionError string            `json:"last_transition_error,omitempty"`
}

func (r FlowRecord) Normalize(now time.Time) FlowRecord {
	r.FlowID = strings.TrimSpace(r.FlowID)
	r.OwnerSessionKey = strings.TrimSpace(r.OwnerSessionKey)
	r.Goal = strings.TrimSpace(r.Goal)
	if r.Status == "" {
		r.Status = FlowStatusQueued
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = now
	}
	r.BlockedTaskID = strings.TrimSpace(r.BlockedTaskID)
	r.TaskIDs = cloneStrings(r.TaskIDs)
	r.Artifacts = cloneArtifacts(r.Artifacts)
	if len(r.StateJSON) > 0 {
		r.StateJSON = append(json.RawMessage(nil), r.StateJSON...)
	}
	if len(r.WaitJSON) > 0 {
		r.WaitJSON = append(json.RawMessage(nil), r.WaitJSON...)
	}
	r.EndedAt = cloneTimePtr(r.EndedAt)
	return r
}

// FlowPatch mutates a FlowRecord. ExpectedRevision implements optimistic concurrency.
type FlowPatch struct {
	ExpectedRevision *int64
	Status           *FlowStatus
	CurrentStep      *int
	StateJSON        *json.RawMessage
	WaitJSON         *json.RawMessage
	BlockedTaskID    *string
	BlockedSummary   *string
	AppendTaskIDs    []string
	Artifacts        *[]ArtifactPayload
	EndedAt          **time.Time
	CancelReason     *string
	TransitionError  *string
}

type FlowFilter struct {
	Statuses        []FlowStatus
	OwnerSessionKey string
	Limit           int
}

type FlowStore interface {
	Create(ctx context.Context, record FlowRecord) error
	Get(ctx context.Context, flowID string) (*FlowRecord, error)
	Update(ctx context.Context, flowID string, patch FlowPatch) (*FlowRecord, error)
	List(ctx context.Context, filter FlowFilter) ([]FlowRecord, error)
	Delete(ctx context.Context, flowID string) error
}

type InMemoryFlowStore struct {
	mu    sync.RWMutex
	now   func() time.Time
	flows map[string]FlowRecord
}

func NewInMemoryFlowStore() *InMemoryFlowStore {
	return &InMemoryFlowStore{now: time.Now, flows: make(map[string]FlowRecord)}
}

func (s *InMemoryFlowStore) Create(_ context.Context, record FlowRecord) error {
	record = record.Normalize(s.now())
	if record.FlowID == "" {
		return fmt.Errorf("acp flow store: flow_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.flows[record.FlowID]; ok {
		return fmt.Errorf("acp flow store: flow %q already exists", record.FlowID)
	}
	s.flows[record.FlowID] = cloneFlowRecord(record)
	return nil
}

func (s *InMemoryFlowStore) Get(_ context.Context, flowID string) (*FlowRecord, error) {
	s.mu.RLock()
	rec, ok := s.flows[strings.TrimSpace(flowID)]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	cp := cloneFlowRecord(rec)
	return &cp, nil
}

func (s *InMemoryFlowStore) Update(_ context.Context, flowID string, patch FlowPatch) (*FlowRecord, error) {
	flowID = strings.TrimSpace(flowID)
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.flows[flowID]
	if !ok {
		return nil, fmt.Errorf("acp flow store: flow %q not found", flowID)
	}
	if patch.ExpectedRevision != nil && rec.Revision != *patch.ExpectedRevision {
		return nil, fmt.Errorf("acp flow store: revision conflict for %q: have %d want %d", flowID, rec.Revision, *patch.ExpectedRevision)
	}
	applyFlowPatch(&rec, patch)
	rec.Revision++
	rec.UpdatedAt = s.now()
	rec = rec.Normalize(s.now())
	s.flows[flowID] = rec
	cp := cloneFlowRecord(rec)
	return &cp, nil
}

func (s *InMemoryFlowStore) List(_ context.Context, filter FlowFilter) ([]FlowRecord, error) {
	s.mu.RLock()
	out := make([]FlowRecord, 0, len(s.flows))
	for _, rec := range s.flows {
		if matchFlowRecord(rec, filter) {
			out = append(out, cloneFlowRecord(rec))
		}
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *InMemoryFlowStore) Delete(_ context.Context, flowID string) error {
	s.mu.Lock()
	delete(s.flows, strings.TrimSpace(flowID))
	s.mu.Unlock()
	return nil
}

// FileFlowStore persists flow records in one JSON document.
type FileFlowStore struct {
	mem  *InMemoryFlowStore
	path string
	mu   sync.Mutex
}

type flowStoreDoc struct {
	Version   int                   `json:"version"`
	Flows     map[string]FlowRecord `json:"flows"`
	UpdatedAt int64                 `json:"updated_at"`
}

const flowStoreVersion = 1

func NewFileFlowStore(dir string) (*FileFlowStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("acp flow store: create dir %q: %w", dir, err)
	}
	s := &FileFlowStore{mem: NewInMemoryFlowStore(), path: filepath.Join(dir, "acp_flows.json")}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileFlowStore) Create(ctx context.Context, record FlowRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mem.Create(ctx, record); err != nil {
		return err
	}
	return s.saveLocked()
}
func (s *FileFlowStore) Get(ctx context.Context, flowID string) (*FlowRecord, error) {
	return s.mem.Get(ctx, flowID)
}
func (s *FileFlowStore) Update(ctx context.Context, flowID string, patch FlowPatch) (*FlowRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.mem.Update(ctx, flowID, patch)
	if err != nil {
		return nil, err
	}
	return rec, s.saveLocked()
}
func (s *FileFlowStore) List(ctx context.Context, filter FlowFilter) ([]FlowRecord, error) {
	return s.mem.List(ctx, filter)
}
func (s *FileFlowStore) Delete(ctx context.Context, flowID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mem.Delete(ctx, flowID); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *FileFlowStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("acp flow store: load: %w", err)
	}
	var doc flowStoreDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("acp flow store: decode: %w", err)
	}
	s.mem.mu.Lock()
	s.mem.flows = make(map[string]FlowRecord, len(doc.Flows))
	for id, rec := range doc.Flows {
		s.mem.flows[id] = rec.Normalize(s.mem.now())
	}
	s.mem.mu.Unlock()
	return nil
}

func (s *FileFlowStore) saveLocked() error {
	s.mem.mu.RLock()
	flows := make(map[string]FlowRecord, len(s.mem.flows))
	for id, rec := range s.mem.flows {
		flows[id] = cloneFlowRecord(rec)
	}
	s.mem.mu.RUnlock()
	data, err := json.MarshalIndent(flowStoreDoc{Version: flowStoreVersion, Flows: flows, UpdatedAt: time.Now().Unix()}, "", "  ")
	if err != nil {
		return fmt.Errorf("acp flow store: encode: %w", err)
	}
	tmp := s.path + "." + randomFileSuffix() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("acp flow store: write temp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("acp flow store: rename temp: %w", err)
	}
	return nil
}

// FlowTransition is one persisted managed-flow lifecycle transition.
type FlowTransition struct {
	Action   string
	Previous *FlowRecord
	Current  FlowRecord
	Announce bool
}

type flowAnnouncementContextKey struct{}

// ContextWithFlowAnnouncement marks mutations in this invocation as requesting
// a compact room announcement. The persisted transition is emitted regardless.
func ContextWithFlowAnnouncement(ctx context.Context, announce bool) context.Context {
	return context.WithValue(ctx, flowAnnouncementContextKey{}, announce)
}

func flowAnnouncementRequested(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	announce, _ := ctx.Value(flowAnnouncementContextKey{}).(bool)
	return announce
}

// FlowRegistry provides state-machine helpers over a FlowStore.
type FlowRegistry struct {
	store FlowStore

	observerMu sync.RWMutex
	observers  map[uint64]func(FlowTransition)
	nextID     uint64
}

func NewFlowRegistry(store FlowStore) *FlowRegistry {
	if store == nil {
		store = NewInMemoryFlowStore()
	}
	return &FlowRegistry{store: store, observers: map[uint64]func(FlowTransition){}}
}

func (r *FlowRegistry) Store() FlowStore { return r.store }

// ObserveTransitions subscribes to successfully persisted lifecycle changes.
func (r *FlowRegistry) ObserveTransitions(observer func(FlowTransition)) func() {
	if r == nil || observer == nil {
		return func() {}
	}
	r.observerMu.Lock()
	r.nextID++
	id := r.nextID
	r.observers[id] = observer
	r.observerMu.Unlock()
	return func() {
		r.observerMu.Lock()
		delete(r.observers, id)
		r.observerMu.Unlock()
	}
}

func (r *FlowRegistry) notify(transition FlowTransition) {
	r.observerMu.RLock()
	observers := make([]func(FlowTransition), 0, len(r.observers))
	for _, observer := range r.observers {
		observers = append(observers, observer)
	}
	r.observerMu.RUnlock()
	for _, observer := range observers {
		observer(transition)
	}
}

func (r *FlowRegistry) update(ctx context.Context, action, flowID string, patch FlowPatch) (*FlowRecord, error) {
	previous, _ := r.store.Get(ctx, flowID)
	current, err := r.store.Update(ctx, flowID, patch)
	if err != nil {
		return nil, err
	}
	r.notify(FlowTransition{Action: action, Previous: previous, Current: cloneFlowRecord(*current), Announce: flowAnnouncementRequested(ctx)})
	return current, nil
}

func (r *FlowRegistry) Create(ctx context.Context, rec FlowRecord) (*FlowRecord, error) {
	if rec.FlowID == "" {
		rec.FlowID = GenerateFlowID()
	}
	if err := r.store.Create(ctx, rec); err != nil {
		return nil, err
	}
	current, err := r.store.Get(ctx, rec.FlowID)
	if err != nil {
		return nil, err
	}
	r.notify(FlowTransition{Action: "create", Current: cloneFlowRecord(*current), Announce: flowAnnouncementRequested(ctx)})
	return current, nil
}

func (r *FlowRegistry) Get(ctx context.Context, flowID string) (*FlowRecord, error) {
	return r.store.Get(ctx, flowID)
}
func (r *FlowRegistry) List(ctx context.Context, filter FlowFilter) ([]FlowRecord, error) {
	return r.store.List(ctx, filter)
}
func (r *FlowRegistry) Start(ctx context.Context, flowID string, step int) (*FlowRecord, error) {
	return r.update(ctx, "start", flowID, FlowPatch{Status: flowStatusPtr(FlowStatusRunning), CurrentStep: intPtr(step), WaitJSON: rawMessagePtr(nil), TransitionError: stringPtr("")})
}
func (r *FlowRegistry) SetWaiting(ctx context.Context, flowID string, wait json.RawMessage) (*FlowRecord, error) {
	return r.update(ctx, "wait", flowID, FlowPatch{Status: flowStatusPtr(FlowStatusWaiting), WaitJSON: rawMessagePtr(wait)})
}
func (r *FlowRegistry) Resume(ctx context.Context, flowID string, state json.RawMessage) (*FlowRecord, error) {
	return r.update(ctx, "resume", flowID, FlowPatch{Status: flowStatusPtr(FlowStatusRunning), StateJSON: rawMessagePtr(state), WaitJSON: rawMessagePtr(nil)})
}
func (r *FlowRegistry) Block(ctx context.Context, flowID, taskID, summary string) (*FlowRecord, error) {
	return r.update(ctx, "block", flowID, FlowPatch{Status: flowStatusPtr(FlowStatusBlocked), BlockedTaskID: stringPtr(taskID), BlockedSummary: stringPtr(summary), TransitionError: stringPtr(summary)})
}
func (r *FlowRegistry) Finish(ctx context.Context, flowID string, artifacts []ArtifactPayload) (*FlowRecord, error) {
	now := time.Now()
	return r.update(ctx, "finish", flowID, FlowPatch{Status: flowStatusPtr(FlowStatusSucceeded), Artifacts: artifactsPtr(artifacts), EndedAt: timePtrPtr(&now), WaitJSON: rawMessagePtr(nil), TransitionError: stringPtr("")})
}
func (r *FlowRegistry) Fail(ctx context.Context, flowID, summary string) (*FlowRecord, error) {
	now := time.Now()
	return r.update(ctx, "fail", flowID, FlowPatch{Status: flowStatusPtr(FlowStatusFailed), EndedAt: timePtrPtr(&now), TransitionError: stringPtr(summary), BlockedSummary: stringPtr(summary)})
}
func (r *FlowRegistry) Cancel(ctx context.Context, flowID, reason string) (*FlowRecord, error) {
	now := time.Now()
	return r.update(ctx, "cancel", flowID, FlowPatch{Status: flowStatusPtr(FlowStatusCancelled), EndedAt: timePtrPtr(&now), CancelReason: stringPtr(reason)})
}
func (r *FlowRegistry) AppendTask(ctx context.Context, flowID, taskID string) (*FlowRecord, error) {
	return r.update(ctx, "append_task", flowID, FlowPatch{AppendTaskIDs: []string{taskID}})
}

func GenerateFlowID() string { return "flow-" + strings.TrimPrefix(GenerateTaskID(), "task-") }

func applyFlowPatch(rec *FlowRecord, patch FlowPatch) {
	if patch.Status != nil {
		rec.Status = *patch.Status
	}
	if patch.CurrentStep != nil {
		rec.CurrentStep = *patch.CurrentStep
	}
	if patch.StateJSON != nil {
		rec.StateJSON = cloneRawMessage(*patch.StateJSON)
	}
	if patch.WaitJSON != nil {
		rec.WaitJSON = cloneRawMessage(*patch.WaitJSON)
	}
	if patch.BlockedTaskID != nil {
		rec.BlockedTaskID = *patch.BlockedTaskID
	}
	if patch.BlockedSummary != nil {
		rec.BlockedSummary = *patch.BlockedSummary
	}
	for _, id := range patch.AppendTaskIDs {
		rec.TaskIDs = appendUniqueString(rec.TaskIDs, strings.TrimSpace(id))
	}
	if patch.Artifacts != nil {
		rec.Artifacts = cloneArtifacts(*patch.Artifacts)
	}
	if patch.EndedAt != nil {
		rec.EndedAt = cloneTimePtr(*patch.EndedAt)
	}
	if patch.CancelReason != nil {
		rec.CancellationReason = *patch.CancelReason
	}
	if patch.TransitionError != nil {
		rec.LastTransitionError = *patch.TransitionError
	}
}

func matchFlowRecord(rec FlowRecord, filter FlowFilter) bool {
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
	if strings.TrimSpace(filter.OwnerSessionKey) != "" && rec.OwnerSessionKey != strings.TrimSpace(filter.OwnerSessionKey) {
		return false
	}
	return true
}

func cloneFlowRecord(rec FlowRecord) FlowRecord {
	rec.StateJSON = cloneRawMessage(rec.StateJSON)
	rec.WaitJSON = cloneRawMessage(rec.WaitJSON)
	rec.TaskIDs = cloneStrings(rec.TaskIDs)
	rec.Artifacts = cloneArtifacts(rec.Artifacts)
	rec.EndedAt = cloneTimePtr(rec.EndedAt)
	return rec
}

func cloneRawMessage(in json.RawMessage) json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), in...)
}

func flowStatusPtr(v FlowStatus) *FlowStatus           { return &v }
func intPtr(v int) *int                                { return &v }
func rawMessagePtr(v json.RawMessage) *json.RawMessage { return &v }
