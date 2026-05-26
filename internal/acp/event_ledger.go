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

// LedgerEvent is one replayable runtime event for a session.
type LedgerEvent struct {
	Sequence   int64        `json:"sequence"`
	SessionKey string       `json:"session_key"`
	RequestID  string       `json:"request_id,omitempty"`
	Event      RuntimeEvent `json:"event"`
	RecordedAt time.Time    `json:"recorded_at"`
}

type EventLedgerStats struct {
	Sessions            int `json:"sessions"`
	Events              int `json:"events"`
	MaxEventsPerSession int `json:"max_events_per_session"`
}

type EventLedger interface {
	StartSession(ctx context.Context, sessionKey, cwd string) error
	RecordEvent(ctx context.Context, sessionKey, requestID string, event RuntimeEvent) error
	Replay(ctx context.Context, sessionKey string) ([]LedgerEvent, error)
	TrimOldSessions(ctx context.Context) error
	Stats(ctx context.Context) EventLedgerStats
}

type InMemoryEventLedger struct {
	mu                  sync.RWMutex
	now                 func() time.Time
	maxSessions         int
	maxEventsPerSession int
	seq                 int64
	sessions            map[string][]LedgerEvent
	lastSeen            map[string]time.Time
	cwd                 map[string]string
}

type EventLedgerOptions struct {
	MaxSessions         int
	MaxEventsPerSession int
	Now                 func() time.Time
}

func NewInMemoryEventLedger(opts EventLedgerOptions) *InMemoryEventLedger {
	if opts.MaxSessions <= 0 {
		opts.MaxSessions = 50
	}
	if opts.MaxEventsPerSession <= 0 {
		opts.MaxEventsPerSession = 500
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &InMemoryEventLedger{now: opts.Now, maxSessions: opts.MaxSessions, maxEventsPerSession: opts.MaxEventsPerSession, sessions: make(map[string][]LedgerEvent), lastSeen: make(map[string]time.Time), cwd: make(map[string]string)}
}

func (l *InMemoryEventLedger) StartSession(_ context.Context, sessionKey, cwd string) error {
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return fmt.Errorf("acp event ledger: session key required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.sessions[key]; !ok {
		l.sessions[key] = nil
	}
	l.lastSeen[key] = l.now()
	if strings.TrimSpace(cwd) != "" {
		l.cwd[key] = strings.TrimSpace(cwd)
	}
	l.trimLocked()
	return nil
}

func (l *InMemoryEventLedger) RecordEvent(_ context.Context, sessionKey, requestID string, event RuntimeEvent) error {
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return fmt.Errorf("acp event ledger: session key required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	ev := LedgerEvent{Sequence: l.seq, SessionKey: key, RequestID: strings.TrimSpace(requestID), Event: cloneRuntimeEvent(event), RecordedAt: l.now()}
	l.sessions[key] = append(l.sessions[key], ev)
	if len(l.sessions[key]) > l.maxEventsPerSession {
		l.sessions[key] = append([]LedgerEvent(nil), l.sessions[key][len(l.sessions[key])-l.maxEventsPerSession:]...)
	}
	l.lastSeen[key] = ev.RecordedAt
	l.trimLocked()
	return nil
}

func (l *InMemoryEventLedger) Replay(_ context.Context, sessionKey string) ([]LedgerEvent, error) {
	key := strings.TrimSpace(sessionKey)
	l.mu.RLock()
	events := cloneLedgerEvents(l.sessions[key])
	l.mu.RUnlock()
	return events, nil
}

func (l *InMemoryEventLedger) TrimOldSessions(_ context.Context) error {
	l.mu.Lock()
	l.trimLocked()
	l.mu.Unlock()
	return nil
}

func (l *InMemoryEventLedger) Stats(_ context.Context) EventLedgerStats {
	l.mu.RLock()
	defer l.mu.RUnlock()
	stats := EventLedgerStats{Sessions: len(l.sessions), MaxEventsPerSession: l.maxEventsPerSession}
	for _, events := range l.sessions {
		stats.Events += len(events)
	}
	return stats
}

func (l *InMemoryEventLedger) trimLocked() {
	if l.maxSessions <= 0 || len(l.sessions) <= l.maxSessions {
		return
	}
	keys := make([]string, 0, len(l.sessions))
	for key := range l.sessions {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return l.lastSeen[keys[i]].Before(l.lastSeen[keys[j]]) })
	for len(keys) > l.maxSessions {
		key := keys[0]
		keys = keys[1:]
		delete(l.sessions, key)
		delete(l.lastSeen, key)
		delete(l.cwd, key)
	}
}

// FileEventLedger adds JSON persistence around InMemoryEventLedger.
type FileEventLedger struct {
	mem  *InMemoryEventLedger
	path string
	mu   sync.Mutex
}

type eventLedgerDoc struct {
	Version  int                      `json:"version"`
	Seq      int64                    `json:"seq"`
	Sessions map[string][]LedgerEvent `json:"sessions"`
	LastSeen map[string]time.Time     `json:"last_seen"`
	CWD      map[string]string        `json:"cwd,omitempty"`
}

func NewFileEventLedger(dir string, opts EventLedgerOptions) (*FileEventLedger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("acp event ledger: create dir %q: %w", dir, err)
	}
	l := &FileEventLedger{mem: NewInMemoryEventLedger(opts), path: filepath.Join(dir, "acp_event_ledger.json")}
	if err := l.load(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *FileEventLedger) StartSession(ctx context.Context, sessionKey, cwd string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.mem.StartSession(ctx, sessionKey, cwd); err != nil {
		return err
	}
	return l.saveLocked()
}
func (l *FileEventLedger) RecordEvent(ctx context.Context, sessionKey, requestID string, event RuntimeEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.mem.RecordEvent(ctx, sessionKey, requestID, event); err != nil {
		return err
	}
	return l.saveLocked()
}
func (l *FileEventLedger) Replay(ctx context.Context, sessionKey string) ([]LedgerEvent, error) {
	return l.mem.Replay(ctx, sessionKey)
}
func (l *FileEventLedger) TrimOldSessions(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.mem.TrimOldSessions(ctx); err != nil {
		return err
	}
	return l.saveLocked()
}
func (l *FileEventLedger) Stats(ctx context.Context) EventLedgerStats { return l.mem.Stats(ctx) }

func (l *FileEventLedger) load() error {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("acp event ledger: load: %w", err)
	}
	var doc eventLedgerDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("acp event ledger: decode: %w", err)
	}
	l.mem.mu.Lock()
	l.mem.seq = doc.Seq
	if doc.Sessions != nil {
		l.mem.sessions = doc.Sessions
	}
	if doc.LastSeen != nil {
		l.mem.lastSeen = doc.LastSeen
	}
	if doc.CWD != nil {
		l.mem.cwd = doc.CWD
	}
	l.mem.mu.Unlock()
	return nil
}

func cloneLedgerEvents(in []LedgerEvent) []LedgerEvent {
	if len(in) == 0 {
		return nil
	}
	out := make([]LedgerEvent, len(in))
	for i, ev := range in {
		out[i] = ev
		out[i].Event = cloneRuntimeEvent(ev.Event)
	}
	return out
}

func cloneRuntimeEvent(ev RuntimeEvent) RuntimeEvent {
	if ev.ApprovalRequest != nil {
		req := *ev.ApprovalRequest
		if len(req.Metadata) > 0 {
			req.Metadata = make(map[string]any, len(ev.ApprovalRequest.Metadata))
			for k, v := range ev.ApprovalRequest.Metadata {
				req.Metadata[k] = v
			}
		}
		ev.ApprovalRequest = &req
	}
	return ev
}

func (l *FileEventLedger) saveLocked() error {
	l.mem.mu.RLock()
	doc := eventLedgerDoc{Version: 1, Seq: l.mem.seq, Sessions: make(map[string][]LedgerEvent, len(l.mem.sessions)), LastSeen: make(map[string]time.Time, len(l.mem.lastSeen)), CWD: make(map[string]string, len(l.mem.cwd))}
	for k, v := range l.mem.sessions {
		doc.Sessions[k] = cloneLedgerEvents(v)
	}
	for k, v := range l.mem.lastSeen {
		doc.LastSeen[k] = v
	}
	for k, v := range l.mem.cwd {
		doc.CWD[k] = v
	}
	l.mem.mu.RUnlock()
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("acp event ledger: encode: %w", err)
	}
	tmp := l.path + "." + randomFileSuffix() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("acp event ledger: write temp: %w", err)
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("acp event ledger: rename temp: %w", err)
	}
	return nil
}
