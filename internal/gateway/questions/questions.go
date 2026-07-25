// Package questions implements the OpenClaw question.* agent-asks-user
// surface: structured multiple-choice questions with a durable
// pending/answered/cancelled/expired lifecycle. The manager mirrors the
// unified approval owner ledger (cmd/metiqd/approval_owners.go): pending
// questions persist to an atomic JSON ledger so they survive daemon restarts
// and WebSocket reconnects, while waiters block in-process on watcher
// channels exactly like exec.approval.waitDecision.
package questions

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

// Question lifecycle statuses (OpenClaw gateway-protocol parity).
const (
	StatusPending   = "pending"
	StatusAnswered  = "answered"
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"
)

// DefaultTimeoutMS matches OpenClaw's DEFAULT_QUESTION_TIMEOUT_MS (15 min).
const DefaultTimeoutMS = 15 * 60 * 1000

// resolvedRetentionMS is the grace period during which resolved records stay
// queryable for late question.waitAnswer / question.get calls.
const resolvedRetentionMS = 15_000

const ledgerVersion = 1

// Error codes surfaced in error messages (OpenClaw QuestionManagerErrorCodes).
const (
	ErrCodeNotFound        = "QUESTION_NOT_FOUND"
	ErrCodeAlreadyTerminal = "QUESTION_ALREADY_TERMINAL"
	ErrCodeIDInUse         = "QUESTION_ID_IN_USE"
	ErrCodeInvalidAnswer   = "QUESTION_INVALID_ANSWER"
)

// Error is a typed question lifecycle failure.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func notFoundErr(id string) *Error {
	return &Error{Code: ErrCodeNotFound, Message: fmt.Sprintf("question %q was not found", id)}
}

func invalidAnswerErr(id, reason string) *Error {
	return &Error{Code: ErrCodeInvalidAnswer, Message: fmt.Sprintf("question %q %s", id, reason)}
}

// Option is one selectable answer.
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Question is the canonical normalized question shown to an operator.
type Question struct {
	QuestionID  string   `json:"questionId"`
	Header      string   `json:"header"`
	Question    string   `json:"question"`
	Options     []Option `json:"options"`
	MultiSelect bool     `json:"multiSelect,omitempty"`
	IsOther     bool     `json:"isOther,omitempty"`
	IsSecret    bool     `json:"isSecret,omitempty"`
}

// AnswerSet wraps answers keyed by questionId (wire shape {"answers":{...}}).
type AnswerSet struct {
	Answers map[string][]string `json:"answers"`
}

// Record is one pending or recently resolved question request.
type Record struct {
	ID          string     `json:"id"`
	Questions   []Question `json:"questions"`
	AgentID     string     `json:"agentId,omitempty"`
	SessionKey  string     `json:"sessionKey,omitempty"`
	CreatedAtMs int64      `json:"createdAtMs"`
	ExpiresAtMs int64      `json:"expiresAtMs"`
	Status      string     `json:"status"`
	Answers     *AnswerSet `json:"answers,omitempty"`
	ResolvedBy  string     `json:"resolvedBy,omitempty"`
	// ResolvedAtMs anchors resolved-record retention; it is ledger-internal
	// and omitted from the wire record when zero.
	ResolvedAtMs int64 `json:"resolvedAtMs,omitempty"`
}

// WaitResult reports the outcome of a WaitAnswer call.
type WaitResult struct {
	Status  string     `json:"status"`
	Answers *AnswerSet `json:"answers,omitempty"`
}

// ResolveResult reports the outcome of Resolve / Cancel.
type ResolveResult struct {
	Status  string     `json:"status"`
	Answers *AnswerSet `json:"answers,omitempty"`
}

type ledgerDocument struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

// RequestParams describes one question.request invocation after schema
// normalization.
type RequestParams struct {
	ID         string
	Questions  []Question
	AgentID    string
	SessionKey string
	TimeoutMS  int
}

// Manager is the durable lifecycle owner for pending questions.
type Manager struct {
	mu          sync.Mutex
	records     map[string]Record
	watchers    map[string][]chan Record
	storagePath string
	seq         int64
	onExpired   func(Record)
}

// SetExpiryHook registers a callback fired (outside the manager lock) when a
// pending question lazily transitions to expired, so hosts can broadcast
// question.resolved for expiries they never see through Resolve/Cancel.
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

// NewManagerAt loads (or initializes) a manager backed by the durable ledger
// at path. Pending questions recorded by a prior process stay pending and
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
		return nil, fmt.Errorf("read question ledger: %w", err)
	}
	var doc ledgerDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode question ledger: %w", err)
	}
	if doc.Version != ledgerVersion {
		return nil, fmt.Errorf("unsupported question ledger version %d", doc.Version)
	}
	for _, rec := range doc.Records {
		if strings.TrimSpace(rec.ID) == "" {
			continue
		}
		m.records[rec.ID] = cloneRecord(rec)
	}
	return m, nil
}

func cloneAnswerSet(in *AnswerSet) *AnswerSet {
	if in == nil {
		return nil
	}
	out := &AnswerSet{Answers: make(map[string][]string, len(in.Answers))}
	for id, values := range in.Answers {
		out.Answers[id] = append([]string(nil), values...)
	}
	return out
}

func cloneRecord(rec Record) Record {
	out := rec
	out.Questions = make([]Question, len(rec.Questions))
	for i, q := range rec.Questions {
		cq := q
		cq.Options = append([]Option(nil), q.Options...)
		out.Questions[i] = cq
	}
	out.Answers = cloneAnswerSet(rec.Answers)
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
		return fmt.Errorf("encode question ledger: %w", err)
	}
	dir := filepath.Dir(m.storagePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create question ledger directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".question-ledger-*.tmp")
	if err != nil {
		return fmt.Errorf("create question ledger temp file: %w", err)
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
		return fmt.Errorf("replace question ledger: %w", err)
	}
	return nil
}

func (m *Manager) nextIDLocked(now int64) string {
	for {
		m.seq++
		id := fmt.Sprintf("question-%d-%d", now, m.seq)
		if _, exists := m.records[id]; !exists {
			return id
		}
	}
}

// sweepLocked lazily expires overdue pending records and drops resolved
// records past their retention grace. Returns terminal transitions that must
// be broadcast to watchers after persistence.
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

// Request records a new pending question set and persists it durably.
func (m *Manager) Request(params RequestParams) (Record, error) {
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
		return Record{}, &Error{Code: ErrCodeIDInUse, Message: fmt.Sprintf("question %q already exists", id)}
	}
	rec := Record{
		ID:          id,
		Questions:   params.Questions,
		AgentID:     params.AgentID,
		SessionKey:  params.SessionKey,
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
	if rec.Status == StatusAnswered {
		answers := rec.Answers
		if answers == nil {
			answers = &AnswerSet{Answers: map[string][]string{}}
		}
		return WaitResult{Status: StatusAnswered, Answers: cloneAnswerSet(answers)}
	}
	return WaitResult{Status: rec.Status}
}

// WaitAnswer blocks until the question resolves, the question expires, the
// optional timeout elapses (status "pending"), or ctx is cancelled. This is
// the agent-side integration point: a running agent posts question.request
// and parks its turn here exactly like exec.approval.waitDecision.
func (m *Manager) WaitAnswer(ctx context.Context, id string, timeoutMS int) (WaitResult, error) {
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
			Message: fmt.Sprintf("question %q is already %s", rec.ID, rec.Status),
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

// Resolve validates answers against the stored questions, canonicalizes them,
// and marks the record answered.
func (m *Manager) Resolve(id string, answers AnswerSet, resolvedBy string) (Record, ResolveResult, error) {
	m.sweep()
	m.mu.Lock()
	rec, ok := m.records[strings.TrimSpace(id)]
	m.mu.Unlock()
	if !ok {
		return Record{}, ResolveResult{}, notFoundErr(id)
	}
	canonical, err := canonicalizeAnswers(rec.Questions, answers)
	if err != nil {
		return Record{}, ResolveResult{}, err
	}
	updated, err := m.terminalize(id, func(rec *Record) {
		rec.Status = StatusAnswered
		rec.Answers = canonical
		rec.ResolvedBy = strings.TrimSpace(resolvedBy)
	})
	if err != nil {
		return Record{}, ResolveResult{}, err
	}
	return updated, ResolveResult{Status: StatusAnswered, Answers: cloneAnswerSet(canonical)}, nil
}

// Cancel marks a pending record cancelled.
func (m *Manager) Cancel(id, resolvedBy string) (Record, ResolveResult, error) {
	updated, err := m.terminalize(id, func(rec *Record) {
		rec.Status = StatusCancelled
		rec.ResolvedBy = strings.TrimSpace(resolvedBy)
	})
	if err != nil {
		return Record{}, ResolveResult{}, err
	}
	return updated, ResolveResult{Status: StatusCancelled}, nil
}

// canonicalizeAnswers validates submitted answers and returns them in
// canonical form (OpenClaw QuestionManager.validateAnswers parity).
func canonicalizeAnswers(qs []Question, answers AnswerSet) (*AnswerSet, error) {
	byID := make(map[string]Question, len(qs))
	for _, q := range qs {
		byID[q.QuestionID] = q
	}
	for submitted := range answers.Answers {
		if _, ok := byID[submitted]; !ok {
			return nil, invalidAnswerErr(submitted, "is not part of this request")
		}
	}
	canonical := &AnswerSet{Answers: make(map[string][]string, len(qs))}
	for _, q := range qs {
		values := answers.Answers[q.QuestionID]
		if len(values) == 0 {
			return nil, invalidAnswerErr(q.QuestionID, "requires an answer")
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return nil, invalidAnswerErr(q.QuestionID, "contains an empty answer")
			}
		}
		if !q.MultiSelect && len(values) > 1 {
			return nil, invalidAnswerErr(q.QuestionID, "does not allow multiple answers")
		}
		canonicalValues := make([]string, 0, len(values))
		for _, value := range values {
			trimmed := strings.TrimSpace(value)
			matched := trimmed
			for _, option := range q.Options {
				if strings.TrimSpace(option.Label) == trimmed {
					matched = option.Label
					break
				}
			}
			canonicalValues = append(canonicalValues, matched)
		}
		if len(q.Options) > 0 && !q.IsOther {
			for _, value := range canonicalValues {
				known := false
				for _, option := range q.Options {
					if option.Label == value {
						known = true
						break
					}
				}
				if !known {
					return nil, invalidAnswerErr(q.QuestionID, "contains an unknown option")
				}
			}
		}
		canonical.Answers[q.QuestionID] = canonicalValues
	}
	return canonical, nil
}
