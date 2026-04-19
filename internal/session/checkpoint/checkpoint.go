// Package checkpoint implements session compaction checkpoints for safely
// compacting long conversation transcripts while preserving the ability to
// recover.  Port of openclaw's session-compaction-checkpoints.ts adapted for
// swarmstr's nostr-event-based transcript storage.
package checkpoint

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MaxCheckpointsPerSession is the upper bound of compaction checkpoints
// retained per session.  Older checkpoints are trimmed on persist.
const MaxCheckpointsPerSession = 25

// ─── Reason enum ────────────────────────────────────────────────────────────

// Reason describes why a compaction checkpoint was created.
type Reason string

const (
	ReasonManual         Reason = "manual"
	ReasonAutoThreshold  Reason = "auto-threshold"
	ReasonOverflowRetry  Reason = "overflow-retry"
	ReasonTimeoutRetry   Reason = "timeout-retry"
)

// ResolveReason maps compaction trigger parameters to a Reason.
func ResolveReason(trigger string, timedOut bool) Reason {
	switch strings.ToLower(strings.TrimSpace(trigger)) {
	case "manual":
		return ReasonManual
	case "overflow":
		return ReasonOverflowRetry
	default:
		if timedOut {
			return ReasonTimeoutRetry
		}
		return ReasonAutoThreshold
	}
}

// ─── Types ──────────────────────────────────────────────────────────────────

// TranscriptRef is a lightweight reference to pre- or post-compaction state.
// Unlike openclaw (which copies session files), swarmstr stores transcripts as
// individual nostr events, so we reference entry IDs and counts.
type TranscriptRef struct {
	SessionID  string `json:"session_id"`
	EntryCount int    `json:"entry_count,omitempty"`
	FirstEntry string `json:"first_entry_id,omitempty"`
	LastEntry  string `json:"last_entry_id,omitempty"`
}

// Checkpoint records a single compaction event for a session.
type Checkpoint struct {
	CheckpointID    string        `json:"checkpoint_id"`
	SessionKey      string        `json:"session_key"`
	SessionID       string        `json:"session_id"`
	CreatedAt       int64         `json:"created_at"`        // unix millis
	Reason          Reason        `json:"reason"`
	TokensBefore    int           `json:"tokens_before,omitempty"`
	TokensAfter     int           `json:"tokens_after,omitempty"`
	Summary         string        `json:"summary,omitempty"`
	FirstKeptEntry  string        `json:"first_kept_entry_id,omitempty"`
	DroppedEntries  int           `json:"dropped_entries,omitempty"`
	KeptEntries     int           `json:"kept_entries,omitempty"`
	PreCompaction   TranscriptRef `json:"pre_compaction"`
	PostCompaction  TranscriptRef `json:"post_compaction"`
}

// Snapshot captures pre-compaction transcript state.  It is created before
// compaction begins and discarded (or persisted) afterward.
type Snapshot struct {
	SessionKey string
	SessionID  string
	EntryCount int
	FirstEntry string
	LastEntry  string
}

// ─── CaptureSnapshot ────────────────────────────────────────────────────────

// CaptureSnapshot records the current transcript state before compaction.
// entryIDs should be the ordered list of transcript entry IDs for the session.
func CaptureSnapshot(sessionKey, sessionID string, entryIDs []string) *Snapshot {
	if sessionKey == "" || sessionID == "" || len(entryIDs) == 0 {
		return nil
	}
	return &Snapshot{
		SessionKey: sessionKey,
		SessionID:  sessionID,
		EntryCount: len(entryIDs),
		FirstEntry: entryIDs[0],
		LastEntry:  entryIDs[len(entryIDs)-1],
	}
}

// ─── PersistParams ──────────────────────────────────────────────────────────

// PersistParams contains all the data needed to record a checkpoint after
// compaction completes.
type PersistParams struct {
	SessionKey     string
	SessionID      string
	Reason         Reason
	Snapshot       *Snapshot
	Summary        string
	FirstKeptEntry string
	DroppedEntries int
	KeptEntries    int
	TokensBefore   int
	TokensAfter    int
	// Post-compaction state.
	PostEntryCount int
	PostFirstEntry string
	PostLastEntry  string
	CreatedAt      int64 // unix millis; 0 = use time.Now()
}

// ─── Store ──────────────────────────────────────────────────────────────────

// Store is a concurrency-safe, in-memory checkpoint store keyed by session key.
// The caller is responsible for persistence (serialising the checkpoint slices
// to/from the session store).
type Store struct {
	mu          sync.Mutex
	bySession   map[string][]Checkpoint
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{bySession: make(map[string][]Checkpoint)}
}

// Persist creates and stores a new Checkpoint from the given parameters.
// Returns the created checkpoint.  The snapshot may be nil (checkpoint is
// still created but pre-compaction fields will be empty).
func (s *Store) Persist(p PersistParams) Checkpoint {
	createdAt := p.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().UnixMilli()
	}

	var pre TranscriptRef
	if p.Snapshot != nil {
		pre = TranscriptRef{
			SessionID:  p.Snapshot.SessionID,
			EntryCount: p.Snapshot.EntryCount,
			FirstEntry: p.Snapshot.FirstEntry,
			LastEntry:  p.Snapshot.LastEntry,
		}
	}

	cp := Checkpoint{
		CheckpointID:   uuid.New().String(),
		SessionKey:     p.SessionKey,
		SessionID:      p.SessionID,
		CreatedAt:      createdAt,
		Reason:         p.Reason,
		TokensBefore:   p.TokensBefore,
		TokensAfter:    p.TokensAfter,
		Summary:        strings.TrimSpace(p.Summary),
		FirstKeptEntry: strings.TrimSpace(p.FirstKeptEntry),
		DroppedEntries: p.DroppedEntries,
		KeptEntries:    p.KeptEntries,
		PreCompaction:  pre,
		PostCompaction: TranscriptRef{
			SessionID:  p.SessionID,
			EntryCount: p.PostEntryCount,
			FirstEntry: strings.TrimSpace(p.PostFirstEntry),
			LastEntry:  strings.TrimSpace(p.PostLastEntry),
		},
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cps := s.bySession[p.SessionKey]
	cps = append(cps, cp)
	s.bySession[p.SessionKey] = trimCheckpoints(cps)
	return cp
}

// List returns all checkpoints for the session key, sorted newest-first.
func (s *Store) List(sessionKey string) []Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	src := s.bySession[sessionKey]
	if len(src) == 0 {
		return nil
	}
	out := make([]Checkpoint, len(src))
	copy(out, src)
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

// Get returns a specific checkpoint by ID, or nil if not found.
func (s *Store) Get(sessionKey, checkpointID string) *Checkpoint {
	checkpointID = strings.TrimSpace(checkpointID)
	if checkpointID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, cp := range s.bySession[sessionKey] {
		if cp.CheckpointID == checkpointID {
			out := cp
			return &out
		}
	}
	return nil
}

// Len returns the total number of checkpoints across all sessions.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, cps := range s.bySession {
		n += len(cps)
	}
	return n
}

// SessionCount returns the number of sessions that have checkpoints.
func (s *Store) SessionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bySession)
}

// Load replaces the checkpoint list for a session key.  Used during
// initialisation to hydrate from the session store.
func (s *Store) Load(sessionKey string, checkpoints []Checkpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(checkpoints) == 0 {
		delete(s.bySession, sessionKey)
		return
	}
	s.bySession[sessionKey] = trimCheckpoints(checkpoints)
}

// Export returns a copy of all checkpoints for a session key.  Used
// to serialise back to the session store.
func (s *Store) Export(sessionKey string) []Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	src := s.bySession[sessionKey]
	if len(src) == 0 {
		return nil
	}
	out := make([]Checkpoint, len(src))
	copy(out, src)
	return out
}

// Delete removes all checkpoints for a session key.
func (s *Store) Delete(sessionKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bySession, sessionKey)
}

// ─── helpers ────────────────────────────────────────────────────────────────

func trimCheckpoints(cps []Checkpoint) []Checkpoint {
	if len(cps) <= MaxCheckpointsPerSession {
		return cps
	}
	// Keep the most recent checkpoints.
	sort.Slice(cps, func(i, j int) bool {
		return cps[i].CreatedAt < cps[j].CreatedAt
	})
	return cps[len(cps)-MaxCheckpointsPerSession:]
}

// FormatCheckpointID returns a deterministic checkpoint ID for testing.
// Production code uses uuid.New().String().
func FormatCheckpointID(prefix string, seq int) string {
	return fmt.Sprintf("%s-%04d", prefix, seq)
}

// ─── Conversion helpers ─────────────────────────────────────────────────────

// ToMap serialises a Checkpoint to a map[string]any suitable for embedding
// in a JSON document (e.g. session store).
func (c Checkpoint) ToMap() map[string]any {
	m := map[string]any{
		"checkpoint_id": c.CheckpointID,
		"session_key":   c.SessionKey,
		"session_id":    c.SessionID,
		"created_at":    c.CreatedAt,
		"reason":        string(c.Reason),
	}
	if c.TokensBefore != 0 {
		m["tokens_before"] = c.TokensBefore
	}
	if c.TokensAfter != 0 {
		m["tokens_after"] = c.TokensAfter
	}
	if c.Summary != "" {
		m["summary"] = c.Summary
	}
	if c.FirstKeptEntry != "" {
		m["first_kept_entry_id"] = c.FirstKeptEntry
	}
	if c.DroppedEntries != 0 {
		m["dropped_entries"] = c.DroppedEntries
	}
	if c.KeptEntries != 0 {
		m["kept_entries"] = c.KeptEntries
	}
	pre := map[string]any{"session_id": c.PreCompaction.SessionID}
	if c.PreCompaction.EntryCount > 0 {
		pre["entry_count"] = c.PreCompaction.EntryCount
	}
	if c.PreCompaction.FirstEntry != "" {
		pre["first_entry_id"] = c.PreCompaction.FirstEntry
	}
	if c.PreCompaction.LastEntry != "" {
		pre["last_entry_id"] = c.PreCompaction.LastEntry
	}
	m["pre_compaction"] = pre
	post := map[string]any{"session_id": c.PostCompaction.SessionID}
	if c.PostCompaction.EntryCount > 0 {
		post["entry_count"] = c.PostCompaction.EntryCount
	}
	if c.PostCompaction.FirstEntry != "" {
		post["first_entry_id"] = c.PostCompaction.FirstEntry
	}
	if c.PostCompaction.LastEntry != "" {
		post["last_entry_id"] = c.PostCompaction.LastEntry
	}
	m["post_compaction"] = post
	return m
}
