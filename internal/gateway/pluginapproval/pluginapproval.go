// Package pluginapproval implements the OpenClaw plugin.approval.* surface: a
// plugin (or plugin host) requests operator approval for a sensitive action and
// blocks until the operator approves or denies. The manager mirrors the durable
// question ledger (internal/gateway/questions): pending approvals persist to an
// atomic JSON ledger so they survive daemon restarts and WebSocket reconnects,
// while waiters block in-process on watcher channels exactly like
// question.waitAnswer / exec.approval.waitDecision.
package pluginapproval

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

// Approval lifecycle statuses (OpenClaw plugin-approval parity).
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDenied   = "denied"
	StatusExpired  = "expired"
)

// Decision values recorded when an approval resolves.
const (
	DecisionApprove = "approve"
	DecisionDeny    = "deny"
)

// DefaultTimeoutMS matches OpenClaw's default plugin-approval window (5 min).
const DefaultTimeoutMS = 5 * 60 * 1000

// resolvedRetentionMS is the grace period during which resolved records stay
// queryable for late plugin.approval.waitDecision / get calls.
const resolvedRetentionMS = 15_000

const ledgerVersion = 1

// Error codes surfaced in error messages (OpenClaw parity).
const (
	ErrCodeNotFound        = "PLUGIN_APPROVAL_NOT_FOUND"
	ErrCodeAlreadyTerminal = "PLUGIN_APPROVAL_ALREADY_TERMINAL"
	ErrCodeIDInUse         = "PLUGIN_APPROVAL_ID_IN_USE"
	ErrCodeInvalidDecision = "PLUGIN_APPROVAL_INVALID_DECISION"
)

// Error is a typed approval lifecycle failure.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func notFoundErr(id string) *Error {
	return &Error{Code: ErrCodeNotFound, Message: fmt.Sprintf("plugin approval %q was not found", id)}
}

// Record is one pending or recently resolved plugin-approval request.
type Record struct {
	ID          string         `json:"id"`
	PluginID    string         `json:"pluginId,omitempty"`
	Action      string         `json:"action"`
	Reason      string         `json:"reason,omitempty"`
	SessionKey  string         `json:"sessionKey,omitempty"`
	AgentID     string         `json:"agentId,omitempty"`
	Detail      map[string]any `json:"detail,omitempty"`
	CreatedAtMs int64          `json:"createdAtMs"`
	ExpiresAtMs int64          `json:"expiresAtMs"`
	Status      string         `json:"status"`
	Decision    string         `json:"decision,omitempty"`
	DecidedBy   string         `json:"decidedBy,omitempty"`
	Note        string         `json:"note,omitempty"`
	// ResolvedAtMs anchors resolved-record retention; ledger-internal and
	// omitted from the wire record when zero.
	ResolvedAtMs int64 `json:"resolvedAtMs,omitempty"`
}

// WaitResult reports the outcome of a WaitDecision call.
type WaitResult struct {
	Status   string `json:"status"`
	Decision string `json:"decision,omitempty"`
	Note     string `json:"note,omitempty"`
}

type ledgerDocument struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

// RequestParams describes one plugin.approval.request invocation after schema
// normalization.
type RequestParams struct {
	ID         string
	PluginID   string
	Action     string
	Reason     string
	SessionKey string
	AgentID    string
	Detail     map[string]any
	TimeoutMS  int
}

// Manager is the durable lifecycle owner for pending plugin approvals.
type Manager struct {
	mu          sync.Mutex
	records     map[string]Record
	watchers    map[string][]chan Record
	storagePath string
	seq         int64
	onExpired   func(Record)
}

// SetExpiryHook registers a callback fired (outside the manager lock) when a
// pending approval lazily transitions to expired, so hosts can broadcast a
// resolution event for expiries they never see through Resolve.
func (m *Manager) SetExpiryHook(hook func(Record)) {
	m.mu.Lock()
	m.onExpired = hook
	m.mu.Unlock()
}

// NewManager returns an in-memory manager (tests, ephemeral runtimes).
func NewManager() *Manager {
	m, _ := NewManagerAt("")
	return m
}

// NewManagerAt loads (or initializes) a manager backed by the durable ledger at
// path. Pending approvals recorded by a prior process stay pending and
// queryable, mirroring approval reconnect recovery.
func NewManagerAt(path string) (*Manager, error) {
	m := &Manager{
		records:     map[string]Record{},
		watchers:    map[string][]chan Record{},
		storagePath: strings.TrimSpace(path),
	}
	if m.storagePath == "" {
		return m, nil
	}
	raw, err := os.ReadFile(m.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, fmt.Errorf("read plugin approval ledger: %w", err)
	}
	var doc ledgerDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode plugin approval ledger: %w", err)
	}
	if doc.Version != ledgerVersion {
		return nil, fmt.Errorf("unsupported plugin approval ledger version %d", doc.Version)
	}
	for _, rec := range doc.Records {
		if strings.TrimSpace(rec.ID) == "" {
			continue
		}
		m.records[rec.ID] = cloneRecord(rec)
	}
	return m, nil
}

func cloneDetail(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneRecord(rec Record) Record {
	out := rec
	out.Detail = cloneDetail(rec.Detail)
	return out
}

func cloneRecords(src map[string]Record) map[string]Record {
	out := make(map[string]Record, len(src))
	for id, rec := range src {
		out[id] = cloneRecord(rec)
	}
	return out
}

func (m *Manager) persistLocked(records map[string]Record) error {
	if m.storagePath == "" {
		return nil
	}
	doc := ledgerDocument{Version: ledgerVersion, Records: make([]Record, 0, len(records))}
	for _, rec := range records {
		doc.Records = append(doc.Records, cloneRecord(rec))
	}
	sort.Slice(doc.Records, func(i, j int) bool {
		if doc.Records[i].CreatedAtMs == doc.Records[j].CreatedAtMs {
			return doc.Records[i].ID < doc.Records[j].ID
		}
		return doc.Records[i].CreatedAtMs < doc.Records[j].CreatedAtMs
	})
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plugin approval ledger: %w", err)
	}
	dir := filepath.Dir(m.storagePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create plugin approval ledger directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".plugin-approval-ledger-*.tmp")
	if err != nil {
		return fmt.Errorf("create plugin approval ledger temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, m.storagePath); err != nil {
		return fmt.Errorf("replace plugin approval ledger: %w", err)
	}
	return nil
}

func (m *Manager) nextIDLocked(now int64) string {
	for {
		m.seq++
		id := fmt.Sprintf("plugin-approval-%d-%d", now, m.seq)
		if _, exists := m.records[id]; !exists {
			return id
		}
	}
}

// sweepLocked lazily expires overdue pending records and drops resolved records
// past their retention grace. Returns terminal transitions that must be
// broadcast to watchers after persistence.
func (m *Manager) sweepLocked(now int64) []Record {
	next := cloneRecords(m.records)
	notifications := make([]Record, 0, 2)
	changed := false
	for id, rec := range next {
		switch rec.Status {
		case StatusPending:
			if rec.ExpiresAtMs > 0 && now >= rec.ExpiresAtMs {
				rec.Status = StatusExpired
				rec.ResolvedAtMs = now
				next[id] = rec
				notifications = append(notifications, rec)
				changed = true
			}
		default:
			resolvedAt := rec.ResolvedAtMs
			if resolvedAt == 0 {
				resolvedAt = rec.ExpiresAtMs
			}
			if now-resolvedAt > resolvedRetentionMS && len(m.watchers[id]) == 0 {
				delete(next, id)
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	if err := m.persistLocked(next); err != nil {
		return nil
	}
	m.records = next
	return notifications
}

// sweep runs a lazy expiry pass and notifies watchers of transitions.
func (m *Manager) sweep() {
	m.mu.Lock()
	notifications := m.sweepLocked(time.Now().UnixMilli())
	for _, rec := range notifications {
		m.notifyWatchersLocked(rec.ID, rec)
	}
	hook := m.onExpired
	m.mu.Unlock()
	if hook != nil {
		for _, rec := range notifications {
			hook(cloneRecord(rec))
		}
	}
}

func (m *Manager) notifyWatchersLocked(id string, rec Record) {
	for _, ch := range m.watchers[id] {
		select {
		case ch <- cloneRecord(rec):
		default:
		}
	}
	delete(m.watchers, id)
}

// Request records a new pending approval and persists it durably.
func (m *Manager) Request(params RequestParams) (Record, error) {
	action := strings.TrimSpace(params.Action)
	if action == "" {
		return Record{}, &Error{Code: ErrCodeInvalidDecision, Message: "plugin approval request requires an action"}
	}
	m.sweep()
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UnixMilli()
	timeoutMS := params.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = DefaultTimeoutMS
	}
	id := strings.TrimSpace(params.ID)
	if id == "" {
		id = m.nextIDLocked(now)
	} else if _, exists := m.records[id]; exists {
		return Record{}, &Error{Code: ErrCodeIDInUse, Message: fmt.Sprintf("plugin approval %q already exists", id)}
	}
	rec := Record{
		ID:          id,
		PluginID:    strings.TrimSpace(params.PluginID),
		Action:      action,
		Reason:      strings.TrimSpace(params.Reason),
		SessionKey:  strings.TrimSpace(params.SessionKey),
		AgentID:     strings.TrimSpace(params.AgentID),
		Detail:      cloneDetail(params.Detail),
		CreatedAtMs: now,
		ExpiresAtMs: now + int64(timeoutMS),
		Status:      StatusPending,
	}
	next := cloneRecords(m.records)
	next[rec.ID] = cloneRecord(rec)
	if err := m.persistLocked(next); err != nil {
		return Record{}, err
	}
	m.records = next
	return cloneRecord(rec), nil
}

// Get returns one record, lazily expiring it first when overdue.
func (m *Manager) Get(id string) (Record, error) {
	m.sweep()
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[strings.TrimSpace(id)]
	if !ok {
		return Record{}, notFoundErr(id)
	}
	return cloneRecord(rec), nil
}

// List returns pending records ordered by creation time then id.
func (m *Manager) List() []Record {
	m.sweep()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Record, 0, len(m.records))
	for _, rec := range m.records {
		if rec.Status != StatusPending {
			continue
		}
		out = append(out, cloneRecord(rec))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAtMs == out[j].CreatedAtMs {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAtMs < out[j].CreatedAtMs
	})
	return out
}

func waitResult(rec Record) WaitResult {
	return WaitResult{Status: rec.Status, Decision: rec.Decision, Note: rec.Note}
}

// WaitDecision blocks until the approval resolves, expires, the optional
// timeout elapses (status "pending"), or ctx is cancelled. This is the
// plugin-side integration point: a plugin posts plugin.approval.request and
// parks here exactly like exec.approval.waitDecision.
func (m *Manager) WaitDecision(ctx context.Context, id string, timeoutMS int) (WaitResult, error) {
	m.sweep()
	m.mu.Lock()
	rec, ok := m.records[strings.TrimSpace(id)]
	if !ok {
		m.mu.Unlock()
		return WaitResult{}, notFoundErr(id)
	}
	if rec.Status != StatusPending {
		result := waitResult(rec)
		m.mu.Unlock()
		return result, nil
	}
	ch := make(chan Record, 1)
	m.watchers[rec.ID] = append(m.watchers[rec.ID], ch)
	expiresAtMs := rec.ExpiresAtMs
	recID := rec.ID
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		watchers := m.watchers[recID]
		for i, candidate := range watchers {
			if candidate == ch {
				m.watchers[recID] = append(watchers[:i], watchers[i+1:]...)
				break
			}
		}
		if len(m.watchers[recID]) == 0 {
			delete(m.watchers, recID)
		}
		m.mu.Unlock()
	}()

	now := time.Now().UnixMilli()
	expiryDelay := time.Duration(expiresAtMs-now) * time.Millisecond
	if expiryDelay < 0 {
		expiryDelay = 0
	}
	expiryTimer := time.NewTimer(expiryDelay)
	defer expiryTimer.Stop()

	var waitTimeout <-chan time.Time
	if timeoutMS > 0 {
		waitTimer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
		defer waitTimer.Stop()
		waitTimeout = waitTimer.C
	}

	select {
	case resolved := <-ch:
		return waitResult(resolved), nil
	case <-expiryTimer.C:
		m.sweep()
		if rec, err := m.Get(recID); err == nil {
			return waitResult(rec), nil
		}
		return WaitResult{Status: StatusExpired}, nil
	case <-waitTimeout:
		return WaitResult{Status: StatusPending}, nil
	case <-ctx.Done():
		return WaitResult{Status: StatusPending}, nil
	}
}

func (m *Manager) terminalize(id string, mutate func(rec *Record)) (Record, error) {
	m.sweep()
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[strings.TrimSpace(id)]
	if !ok {
		return Record{}, notFoundErr(id)
	}
	if rec.Status != StatusPending {
		return Record{}, &Error{
			Code:    ErrCodeAlreadyTerminal,
			Message: fmt.Sprintf("plugin approval %q is already %s", rec.ID, rec.Status),
		}
	}
	mutate(&rec)
	rec.ResolvedAtMs = time.Now().UnixMilli()
	next := cloneRecords(m.records)
	next[rec.ID] = cloneRecord(rec)
	if err := m.persistLocked(next); err != nil {
		return Record{}, err
	}
	m.records = next
	m.notifyWatchersLocked(rec.ID, rec)
	return cloneRecord(rec), nil
}

// Resolve records an operator decision (approve or deny) and marks the record
// terminal. decision must be "approve" or "deny".
func (m *Manager) Resolve(id, decision, decidedBy, note string) (Record, error) {
	decision = strings.ToLower(strings.TrimSpace(decision))
	var status string
	switch decision {
	case DecisionApprove:
		status = StatusApproved
	case DecisionDeny:
		status = StatusDenied
	default:
		return Record{}, &Error{
			Code:    ErrCodeInvalidDecision,
			Message: fmt.Sprintf("plugin approval decision %q must be %q or %q", decision, DecisionApprove, DecisionDeny),
		}
	}
	return m.terminalize(id, func(rec *Record) {
		rec.Status = status
		rec.Decision = decision
		rec.DecidedBy = strings.TrimSpace(decidedBy)
		rec.Note = strings.TrimSpace(note)
	})
}
